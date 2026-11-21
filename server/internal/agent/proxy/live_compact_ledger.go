package proxy

import (
	"time"

	"mindfs/server/internal/agent/fold"
)

// foldLedgerPending is one folded request whose ledger line is not written yet:
// the rewrite cost is only known once the response stream has ended.
type foldLedgerPending struct {
	harness        string
	mode           string
	reqBytes       int
	forwardedBytes int
	reqTokens      int
	forwardTokens  int
	result         foldResult
	elapsed        time.Duration
}

// newFoldLedgerPending captures the sizes a fold event will report. Returns nil
// when no ledger is configured, so the request path pays nothing for it.
func (s *Server) newFoldLedgerPending(harness, mode string, originalBytes, forwardedBytes, reqTokens, forwardTokens int, result foldResult, elapsed time.Duration) *foldLedgerPending {
	if s.harnessRuntimeFor(harness).ledger == nil {
		return nil
	}
	return &foldLedgerPending{
		harness:        harness,
		mode:           mode,
		reqBytes:       originalBytes,
		forwardedBytes: forwardedBytes,
		reqTokens:      reqTokens,
		forwardTokens:  forwardTokens,
		result:         result,
		elapsed:        elapsed,
	}
}

// writeFoldLedger appends the pending line with this turn's rewrite cost. It
// reads the session key from the proxy's session state rather than taking it as
// a parameter, so the fold path cannot accidentally log a key it does not own.
//
// Observe-only sessions never reach here (they never fold), which is what the
// design requires: isolated sessions must not inflate the user-facing saving.
func (s *Server) writeFoldLedger(token string, pending *foldLedgerPending, totals usageTotals) {
	usage := fold.Usage{Source: fold.UsageSourceMissing, RewriteTokens: -1}
	if totals.Seen {
		// Messages reports three disjoint counters; a rewrite costs the fresh
		// input plus whatever was written to cache.
		usage = fold.Usage{
			ContextTokens: totals.ContextTokens(),
			CacheRead:     totals.CacheReadInputTokens,
			CacheWrite:    totals.CacheCreationInputTokens,
			Output:        totals.OutputTokens,
			RewriteTokens: totals.InputTokens + totals.CacheCreationInputTokens,
			Source:        fold.UsageSourceResponse,
		}
	}
	s.writeFoldLedgerUsage(token, pending, usage, s.sessionUpstreamBase(token))
}

// writeFoldLedgerUsage appends the pending line from a normalized usage block,
// which is what lets both wire formats share one ledger shape. upstreamBase is
// the URL the request was actually forwarded to; it is what decides whether the
// counters are the provider's own or a relay's (see usageProvenance).
func (s *Server) writeFoldLedgerUsage(token string, pending *foldLedgerPending, usage fold.Usage, upstreamBase string) {
	if pending == nil {
		return
	}
	rt := s.harnessRuntimeFor(pending.harness)
	if rt.ledger == nil {
		return
	}
	s.mu.Lock()
	sessionKey := ""
	if st, ok := s.sessions[token]; ok {
		sessionKey = st.sessionKey
	}
	s.mu.Unlock()
	if sessionKey == "" {
		return
	}
	rewrite := usage.RewriteTokens
	source := usage.Source
	if source == "" {
		source = fold.UsageSourceMissing
		rewrite = -1
	}
	// The three stored counters are disjoint (fresh input, cache write, cache
	// read), so the fresh part is what is left after both cache paths.
	fresh := usage.ContextTokens - usage.CacheRead - usage.CacheWrite
	if fresh < 0 {
		fresh = 0
	}
	entry := fold.LedgerEntry{
		TS:                 time.Now().Unix(),
		Mode:               pending.mode,
		ReqBytes:           pending.reqBytes,
		FoldedBytes:        pending.forwardedBytes,
		SavedBytes:         pending.reqBytes - pending.forwardedBytes,
		ReqTokensEst:       pending.reqTokens,
		ForwardTokensEst:   pending.forwardTokens,
		Blocks:             pending.result.foldedBlocks,
		NewBlocks:          pending.result.newBlocks,
		NewTokensEst:       pending.result.newTokens,
		TokensEst:          pending.result.foldedTokens,
		Skipped:            pending.result.skipped,
		ArchiveErrors:      pending.result.archiveFailures,
		FoldMS:             pending.elapsed.Milliseconds(),
		RewriteTokens:      rewrite,
		UsageInput:         fresh,
		UsageOutput:        usage.Output,
		UsageCacheCreation: usage.CacheWrite,
		UsageCacheRead:     usage.CacheRead,
		UsageSource:        source,
		UsageProvenance:    usageProvenance(source, upstreamBase),
	}
	// The ledger is a diagnostic; a failed write must not fail the turn.
	_ = rt.ledger.Write(sessionKey, entry)
}

// SessionFoldUsage returns the raw ledger rows for a session. The context chip
// only needs the summary; the completion record (Task 13) needs each row's
// timestamp to cut a run window, which a summary no longer carries.
func (s *Server) SessionFoldUsage(sessionKey string) []fold.LedgerEntry {
	rt := s.harnessRuntimeFor(harnessForPoolKey(sessionKey))
	if rt.ledger == nil {
		return nil
	}
	return rt.ledger.Read(sessionKey)
}

// SessionContextSaving returns the ledger summary for a session. It is the
// read path behind the "context" chip, and it is safe to call for a session
// that has never folded (returns a zero summary).
func (s *Server) SessionContextSaving(sessionKey string) fold.ContextSaving {
	rt := s.harnessRuntimeFor(harnessForPoolKey(sessionKey))
	if rt.ledger == nil {
		return fold.ContextSaving{}
	}
	return fold.SummarizeLedger(rt.ledger.Read(sessionKey))
}
