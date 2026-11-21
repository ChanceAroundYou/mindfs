package proxy

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"mindfs/server/internal/agent/fold"
)

// LiveCompactMode returns the effective mode for startup logging.
func (s *Server) LiveCompactMode() string {
	return s.liveCompact.Mode
}

type liveCompactState struct {
	requests         int
	foldedBlocks     int
	foldedTokens     int
	lastHashes       []uint64
	lastMessageCount int
	compactionEpoch  int
	// Cold-cache signals (design 2026-11-20 §5). pendingColdFold is set by the
	// PostCompact hook and cleared once by a fold decision that actually had
	// material to consider: a request with no candidates must not spend it,
	// because the request right after a compaction often has nothing foldable.
	// pendingColdFoldAt bounds its life; past the cache TTL the idle rule takes
	// over anyway.
	pendingColdFold   bool
	pendingColdFoldAt time.Time
	lastUsageAt       time.Time
	lastContext       int
	// noReadStreak counts consecutive responses that neither read from nor
	// wrote to the upstream cache (see live_compact_cold.go).
	noReadStreak int
	// prefixWarned keeps the unknown-model warning to one line per session.
	prefixWarned bool
}

type liveCompactObservation struct {
	mode             string
	foldAllowed      bool
	messages         int
	rounds           int
	closedRounds     int
	keepRounds       int
	epoch            int
	estimatedInput   int
	candidates       int
	errorCandidates  int
	failedCandidates int
	candidateTokens  int
	candidateBytes   int
	// Un-foldable bytes in eligible rounds, bucketed by reason (B6). The rail
	// is only satisfiable if these stay small; without them a log reader
	// cannot tell a context that folds well from one that cannot be folded.
	skippedErrorBytes     int
	skippedMultiPartBytes int
	skippedNonTextBytes   int
	driftAt               int
	driftFromTail         int
	parseMillis           int64
	// gate is the rail decision taken on the request path (design §12.6).
	gateDecision string
	gateEst      int
	gateGrowth   int
	// cold / coldReason say whether the fold decision was taken on an assumed
	// cold prompt cache (design 2026-11-20 §5).
	cold       bool
	coldReason string
	// gateProjected is the rail input in upstream tokens when the session has a
	// calibration sample, estimator tokens otherwise.
	gateProjected int
	// wouldFoldTokens is what this request's fold replaces (sticky plus new).
	// In shadow it is the rehearsal's answer, not a rewrite.
	wouldFoldTokens int
	// plan is the kernel planner's composition of this body: how much tool
	// output it could never fold and how much it was willing to. The cand_*
	// counters above come from the older reference analyzer and do not model
	// the current policy; these come from the planner that actually decided.
	plan fold.PlanStats
}

// isMessagesPath reports whether a proxied path is the main Messages endpoint.
// Everything else (count_tokens, models, token counting variants) is out of
// scope: rewriting those would change what the CLI itself believes about its
// context size.
func isMessagesPath(path string) bool {
	return strings.TrimSuffix(path, "/") == "/v1/messages"
}

// liveCompactDisabled checks the emergency brake file. The stat is cached for
// one second so a hot request path does not pay for it on every turn, while a
// toggle still takes effect almost immediately.
func (s *Server) liveCompactDisabled() bool {
	if s.liveCompact.DisableFile == "" {
		return false
	}
	s.disableMu.Lock()
	defer s.disableMu.Unlock()
	if !s.disableCheckedAt.IsZero() && time.Since(s.disableCheckedAt) < time.Second {
		return s.disableCache
	}
	s.disableCheckedAt = time.Now()
	_, err := os.Stat(s.liveCompact.DisableFile)
	s.disableCache = err == nil
	return s.disableCache
}

// observeLiveCompact measures what a fold would do. Phase A never returns a
// modified body: the caller keeps forwarding the original bytes.
//
// Cost: parsing is linear in body size but the constant is high — encoding/json
// re-validates the whole body at every nesting level (measured: 450 ms for an
// 8 MB body, 4 full scans; real 1M-context turns are ~4 MB). The request path
// therefore only enqueues; analyzeLiveCompact runs on a per-token worker.
//
// The returned turn is nil when nothing was queued. Otherwise the caller must
// hand the same turn to recordLiveCompactUsage (or abandonLiveCompactTurn when
// the upstream call failed) so the log line can pair this request's estimate
// with this request's usage.
func (s *Server) observeLiveCompact(token string, body []byte, gate liveCompactGate) *liveCompactTurn {
	cfg := s.harnessRuntimeFor(harnessClaude).cfg
	if !cfg.Observes() || len(body) < cfg.MinBodyBytes {
		return nil
	}
	if s.liveCompactDisabled() {
		return nil
	}
	turn := &liveCompactTurn{gate: gate}
	tokenCopy := token
	// body comes from io.ReadAll in the handler and is never mutated after this
	// point, so it can be shared with the worker instead of copied. A copy here
	// was 8 MB of request-path work for nothing.
	if !s.enqueueLiveCompactWork(token, len(body), func() {
		s.analyzeLiveCompactSafe(tokenCopy, turn, body)
	}) {
		return nil
	}
	return turn
}

// analyzeLiveCompactSafe wraps analyzeLiveCompact so a bug in the analysis
// path can never affect a request. The design requires fail-open (I7).
func (s *Server) analyzeLiveCompactSafe(token string, turn *liveCompactTurn, body []byte) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Printf("[live-compact] skip token=%s reason=panic %v", ShortToken(token), recovered)
		}
	}()
	s.analyzeLiveCompact(token, turn, body)
}

func (s *Server) analyzeLiveCompact(token string, turn *liveCompactTurn, body []byte) {
	cfg := s.harnessRuntimeFor(harnessClaude).cfg
	if !cfg.Observes() {
		return
	}

	start := time.Now()
	req, err := parseWireRequest(body)
	if err != nil {
		log.Printf("[live-compact] skip token=%s reason=parse", ShortToken(token))
		return
	}
	analysis := analyzeWire(req, cfg.KeepRounds, cfg.MinBlockTokens)
	estimatedInput := estimateByteTokens(body)
	hashes := messageHashes(req, cfg.MaxDriftMessages)
	parseMillis := time.Since(start).Milliseconds()

	s.mu.Lock()
	st, ok := s.sessions[token]
	if !ok {
		// Session closed while the work was queued. Nothing to report.
		s.mu.Unlock()
		return
	}
	previousLen := len(st.live.lastHashes)
	drift := firstDrift(st.live.lastHashes, hashes)
	foldAllowed := st.foldAllowed
	epoch := st.live.compactionEpoch
	st.live.lastHashes = hashes
	st.live.lastMessageCount = len(hashes)
	st.live.requests++
	s.mu.Unlock()

	obs := liveCompactObservation{
		mode:                  cfg.Mode,
		foldAllowed:           foldAllowed,
		messages:              analysis.Messages,
		rounds:                analysis.Rounds,
		closedRounds:          analysis.ClosedRounds,
		keepRounds:            cfg.KeepRounds,
		epoch:                 epoch,
		estimatedInput:        estimatedInput,
		candidates:            len(analysis.Candidates),
		errorCandidates:       analysis.ErrorCandidates,
		failedCandidates:      analysis.FailedCandidates,
		candidateTokens:       analysis.CandidateTokens,
		candidateBytes:        analysis.CandidateBytes,
		skippedErrorBytes:     analysis.SkippedErrorBytes,
		skippedMultiPartBytes: analysis.SkippedMultiPartBytes,
		skippedNonTextBytes:   analysis.SkippedNonTextBytes,
		driftAt:               drift,
		driftFromTail:         driftDistanceFromTail(previousLen, drift),
		parseMillis:           parseMillis,
		gateDecision:          turn.gate.decision,
		gateEst:               turn.gate.est,
		gateGrowth:            turn.gate.growth,
		cold:                  turn.gate.cold,
		coldReason:            turn.gate.coldReason,
		gateProjected:         turn.gate.projected,
		wouldFoldTokens:       turn.gate.wouldFoldTokens,
		plan:                  turn.gate.plan,
	}
	if final, totals, ready := turn.completeAnalysis(obs); ready {
		s.logLiveCompact(token, final, totals)
	}
}

// recordLiveCompactUsage closes this request's turn with its upstream totals.
// The line is written by whichever half finishes second. Totals with Seen=false
// (compressed stream, error response) close the turn with actual_in=-1 instead
// of leaving it pending forever.
func (s *Server) recordLiveCompactUsage(token string, turn *liveCompactTurn, totals usageTotals) {
	if turn == nil {
		return
	}
	s.noteActualContext(token, totals)
	if turn.ledger != nil {
		s.writeFoldLedger(token, turn.ledger, totals)
	}
	if final, usage, ready := turn.completeTurn(totals); ready {
		s.logLiveCompact(token, final, usage)
	}
}

// logLiveCompact writes one line per parsed request. Only sizes and counters
// are logged: no message text, tool arguments, or session key ever reach the
// log.
func (s *Server) logLiveCompact(token string, obs liveCompactObservation, totals usageTotals) {
	actual := -1
	if totals.Seen {
		actual = totals.ContextTokens()
	}
	log.Print(liveCompactObservationLine(token, obs, totals, actual))
}

// liveCompactObservationLine is the line's text, split out so the format can be
// pinned by a test without capturing the process log.
func liveCompactObservationLine(token string, obs liveCompactObservation, totals usageTotals, actual int) string {
	scope := "fold"
	if !obs.foldAllowed {
		scope = "observe-only"
	}
	return fmt.Sprintf("[live-compact] %s token=%s scope=%s msgs=%d rounds=%d closed=%d keep=%d epoch=%d est_in=%d actual_in=%d usg_in=%d usg_create=%d usg_read=%d cand_blocks=%d cand_error_blocks=%d cand_failed_blocks=%d cand_tokens=%d cand_bytes=%d skip_bytes_error=%d skip_bytes_multipart=%d skip_bytes_nontext=%d drift=%d drift_age=%d parse_ms=%d gate=%s gate_est=%d gate_growth=%d gate_projected=%d cold=%t cold_reason=%s would_fold_tokens=%d plan_body_bytes=%d plan_result_bytes=%d plan_protected_bytes=%d plan_nontext_bytes=%d plan_multipart_kept_bytes=%d plan_failed_small_bytes=%d plan_candidate_bytes=%d",
		obs.mode, ShortToken(token), scope, obs.messages, obs.rounds, obs.closedRounds,
		obs.keepRounds, obs.epoch, obs.estimatedInput, actual,
		totals.InputTokens, totals.CacheCreationInputTokens, totals.CacheReadInputTokens, obs.candidates, obs.errorCandidates,
		obs.failedCandidates, obs.candidateTokens, obs.candidateBytes,
		obs.skippedErrorBytes, obs.skippedMultiPartBytes, obs.skippedNonTextBytes,
		obs.driftAt, obs.driftFromTail, obs.parseMillis,
		obs.gateDecision, obs.gateEst, obs.gateGrowth, obs.gateProjected,
		obs.cold, obs.coldReason, obs.wouldFoldTokens,
		obs.plan.BodyBytes, obs.plan.ResultBytes, obs.plan.ProtectedBytes, obs.plan.NonTextBytes,
		obs.plan.MultiPartKeptBytes, obs.plan.FailedSmallBytes, obs.plan.CandidateBytes)
}

// driftDistanceFromTail reports how far before the previous request's tail the
// first changed message sits (0 = the last message changed). Tail rewrites are
// harmless for folding: only old rounds outside keep_rounds are ever folded.
func driftDistanceFromTail(previousLen, drift int) int {
	if drift < 0 || previousLen == 0 {
		return -1
	}
	return previousLen - 1 - drift
}

// logLiveCompactSkip records a request that was left byte-identical because
// the fold path could not proceed. Sizes only, never content.
