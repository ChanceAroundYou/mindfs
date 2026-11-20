package proxy

import (
	"log"

	"mindfs/server/internal/agent/fold"
)

// The proxy's view of one fold decision.
//
// The policy lives in the fold kernel; this struct is the log line's shape and
// the observation worker's input, kept under its old name so the Phase A
// logging pipeline and its tests read unchanged.

// The gate decision strings are the kernel's: changing them would break
// existing log tooling for no gain.
const (
	gateDisabled    = fold.GateDisabled
	gateObserveOnly = fold.GateObserveOnly
	gateScanFailed  = fold.GateScanFailed
	gateUnmeasured  = fold.GateUnmeasured
	gateBelowRail   = fold.GateBelowRail
	gateFold        = fold.GateFold
	gateNoBacklog   = fold.GateNoBacklog
	gatePrefixGuard = fold.GatePrefixGuard
)

// liveCompactGate is one request's rail decision, for the log and the shadow
// rehearsal.
type liveCompactGate struct {
	decision string
	// est and growth are the request's measured size and its increment over the
	// previous request, in estimator tokens.
	est    int
	growth int
	// projected is the rail input in upstream tokens when calibration is
	// available (estimator tokens otherwise).
	projected int
	// wouldFoldTokens / wouldFoldBlocks describe what this request's fold
	// replaces (sticky plus newly committed). Shadow reports them so a
	// rehearsal can measure the savings mode=on would have produced; on reports
	// what was actually folded.
	wouldFoldTokens int
	wouldFoldBlocks int
	// cold / coldReason describe the cache state the decision was taken on.
	cold       bool
	coldReason string
	// candidates is how many foldable blocks this request had. Zero means the
	// decision had nothing to act on, which is also what keeps a PostCompact
	// cold signal alive for the next request instead of spending it here.
	candidates int
}

// gateFromOutcome is the kernel outcome in the log's shape.
func gateFromOutcome(out fold.Outcome, growth int) liveCompactGate {
	gate := liveCompactGate{
		decision:        out.Verdict.Reason,
		est:             out.Estimated,
		growth:          growth,
		projected:       out.Projected,
		wouldFoldTokens: out.FoldedTokens,
		wouldFoldBlocks: out.FoldedBlocks,
		cold:            out.Cold,
		coldReason:      out.ColdReason,
		candidates:      out.Candidates,
	}
	return gate
}

// foldResult is one applied fold, in the ledger and log shape. The body is nil
// when nothing was folded.
type foldResult struct {
	body         []byte
	foldedBlocks int
	foldedTokens int
	foldedBytes  int
	// stickyBlocks were folded because the session had already committed to
	// them; newBlocks were committed by this request.
	stickyBlocks int
	newBlocks    int
	// skipped counts candidates that were eligible but not folded.
	skipped int
	// archiveFailures counts blocks left alone because the archive write
	// failed.
	archiveFailures int
}

func (r foldResult) changed() bool { return len(r.body) > 0 }

// foldResultFromOutcome is the ledger/log shape of a fold.
func foldResultFromOutcome(out fold.Outcome) foldResult {
	result := foldResult{
		foldedBlocks:    out.FoldedBlocks,
		foldedTokens:    out.FoldedTokens,
		foldedBytes:     out.FoldedBytes,
		stickyBlocks:    out.StickyBlocks,
		newBlocks:       out.NewBlocks,
		skipped:         out.Skipped,
		archiveFailures: out.ArchiveFailures,
	}
	if out.Folded {
		result.body = out.Body
	}
	return result
}

// warnUnknownModelOnce keeps the alias warning to one line per session.
func (s *Server) warnUnknownModelOnce(token, model string) {
	s.mu.Lock()
	st, ok := s.sessions[token]
	if !ok || st.live.prefixWarned {
		s.mu.Unlock()
		return
	}
	st.live.prefixWarned = true
	s.mu.Unlock()
	log.Printf("[live-compact] prefix-guard: unknown model %q, folding allowed (add it to prefix_unsafe_models if it maps to a preserved-thinking model)", model)
}
