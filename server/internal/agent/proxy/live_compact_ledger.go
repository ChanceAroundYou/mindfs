package proxy

import (
	"time"

	"mindfs/server/internal/agent/fold"
)

// foldLedgerPending is one folded request whose ledger line is not written yet:
// the rewrite cost is only known once the response stream has ended.
type foldLedgerPending struct {
	mode           string
	reqBytes       int
	forwardedBytes int
	result         foldResult
	elapsed        time.Duration
}

// newFoldLedgerPending captures the sizes a fold event will report. Returns nil
// when no ledger is configured, so the request path pays nothing for it.
func (s *Server) newFoldLedgerPending(mode string, originalBytes, forwardedBytes int, result foldResult, elapsed time.Duration) *foldLedgerPending {
	if s.ledger == nil {
		return nil
	}
	return &foldLedgerPending{
		mode:           mode,
		reqBytes:       originalBytes,
		forwardedBytes: forwardedBytes,
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
	s.writeFoldLedgerUsage(token, pending, usage)
}

// writeFoldLedgerUsage appends the pending line from a normalized usage block,
// which is what lets both wire formats share one ledger shape.
func (s *Server) writeFoldLedgerUsage(token string, pending *foldLedgerPending, usage fold.Usage) {
	if s.ledger == nil || pending == nil {
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
		Blocks:             pending.result.foldedBlocks,
		TokensEst:          pending.result.foldedTokens,
		Skipped:            pending.result.skipped,
		ArchiveErrors:      pending.result.archiveFailures,
		FoldMS:             pending.elapsed.Milliseconds(),
		RewriteTokens:      rewrite,
		UsageInput:         fresh,
		UsageCacheCreation: usage.CacheWrite,
		UsageCacheRead:     usage.CacheRead,
		UsageSource:        source,
	}
	// The ledger is a diagnostic; a failed write must not fail the turn.
	_ = s.ledger.Write(sessionKey, entry)
}

// SessionContextSaving returns the ledger summary for a session. It is the
// read path behind the "context" chip, and it is safe to call for a session
// that has never folded (returns a zero summary).
func (s *Server) SessionContextSaving(sessionKey string) fold.ContextSaving {
	if s.ledger == nil {
		return fold.ContextSaving{}
	}
	return fold.SummarizeLedger(s.ledger.Read(sessionKey))
}
