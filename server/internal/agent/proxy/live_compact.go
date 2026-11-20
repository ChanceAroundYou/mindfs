package proxy

import (
	"log"
	"os"
	"strings"
	"time"
)

const (
	LiveCompactOff    = "off"
	LiveCompactShadow = "shadow"
	LiveCompactOn     = "on"
)

// LiveCompactConfig is the node-local context-management configuration. The
// zero value normalizes to off, so an unconfigured node pays nothing.
type LiveCompactConfig struct {
	Mode string
	// KeepRounds is the number of newest completed tool rounds left verbatim.
	KeepRounds int
	// KeepTailTokens bounds the same protected tail by size, not just by round
	// count (design §12.7(d), dsh's keepRoundTokens). Three rounds of small
	// reads is a fine tail; three rounds holding 90 KB reads is 75k tokens of
	// tail on a 200k window, and then a rail that fires at 160k has only one
	// aged-out round to cut — a shallow fold every turn, which §12.7(b) measured
	// as more expensive than not folding. The newest round always stays.
	// Zero means the default.
	KeepTailTokens int
	// MinBlockTokens is the size a *failed* (is_error) tool result needs to
	// become a candidate. Ordinary results are judged per block by how many
	// bytes the pointer actually saves (foldMinBlockSavings), because a token
	// floor of this size rejected most real sessions' 1-7 KB results and left
	// rail-triggered events with nothing to fold.
	MinBlockTokens int
	// MinBodyBytes skips JSON parsing for requests too small to contain a
	// foldable result. This keeps ordinary short turns on the cheap path.
	MinBodyBytes int
	// MaxDriftMessages bounds the per-message fingerprint history kept for
	// detecting CLI-side history rewrites. A zero value means the default.
	MaxDriftMessages int
	// RailTokens is the upstream-token trigger for folding new blocks (design
	// §12.7). Below it, already-committed folds are still applied but no new
	// block is committed, because a fold invalidates the upstream prefix cache
	// from the boundary onward. Zero means the default (100k). The rail is the
	// resident-context ceiling the user asked for: 160k measured within noise of
	// 100k on cost but let requests reach 130k, while 100k peaked at 95k.
	RailTokens int
	// TargetTokens is the low-water line one fold event commits down to. It
	// sits far below the rail on purpose: the model is "fold deep, fold rarely"
	// (§12.7(b) — a shallow fold cannot repay the cache rewrite it costs). In
	// practice keep_rounds and keep_tail_tokens are the binding constraints; the
	// target only stops an event early when the context was already small. At a
	// 100k rail it is also never the binding one: the tail alone can be 50k+.
	TargetTokens int
	// MinFoldBytes rejects a fold event too small to be worth a cache break.
	MinFoldBytes int
	// MaxFoldBytes caps one event. Zero means the default cap (262144); no
	// caller can accidentally disable the cap by leaving it unset.
	MaxFoldBytes int
	// LargeBlockTokens lets a single oversized tool result be folded even below
	// the rail: one such result dominates the context on its own.
	LargeBlockTokens int
	// DisableFile is the emergency brake. When the file exists, folding and
	// observation stop for the running process without a service restart.
	// Empty means no brake file is wired (default off, nothing to stop).
	DisableFile string
	// ArchiveDir is where folded tool output is stored. Empty disables
	// archiving, which also disables folding: a pointer must never name a file
	// that does not exist.
	ArchiveDir string
	// ArchiveEnabled defaults to true when ArchiveDir is set. It exists so a
	// node can keep observing while storage is unavailable.
	ArchiveEnabled bool
	// RetentionDays and MaxArchiveBytes bound the archive directory. Zero
	// values mean the defaults (7 days, 2 GiB).
	RetentionDays   int
	MaxArchiveBytes int64
	// PrefixGuard refuses to fold requests whose model runs Anthropic's
	// preserved-thinking prefix check (design 2026-11-20 §4). That check
	// compares what the client *sent*, so a proxy-side rewrite invalidates
	// every later thinking block on those models. Tri-state: nil means on,
	// because a node that never configured it must still be protected.
	PrefixGuard *bool
	// PrefixUnsafeModels lists model ids (usually aliases such as llmux's
	// "ok") that map to a preserved-thinking model upstream. Matching is on the
	// normalized family, so an alias is written exactly as it appears in the
	// request body.
	PrefixUnsafeModels []string
	// ColdFold allows folding below the rail when the upstream prompt cache is
	// already cold (design 2026-11-20 §5): a fold then costs no cache rewrite
	// because there is no warm prefix to invalidate. Tri-state: nil means on.
	ColdFold *bool
	// ColdRailTokens is the context size at which a cold fold is worth doing.
	// Below it the fold buys no window headroom and only costs continuity.
	// Zero means the default (60000).
	ColdRailTokens int
	// CacheTTLSeconds is how long an idle gap must be before the next request's
	// prefix is assumed evicted upstream. Zero means the default (300, the
	// Anthropic 5-minute cache TTL).
	CacheTTLSeconds int
}

// PrefixGuardOn reports whether the preserved-thinking guard is enabled. The
// zero value is on: protection must not depend on a node opting in.
func (c LiveCompactConfig) PrefixGuardOn() bool {
	return c.PrefixGuard == nil || *c.PrefixGuard
}

// ColdFoldOn reports whether cold-cache folding is enabled. Zero value is on.
func (c LiveCompactConfig) ColdFoldOn() bool {
	return c.ColdFold == nil || *c.ColdFold
}

func (c LiveCompactConfig) Normalized() LiveCompactConfig {
	c.Mode = strings.ToLower(strings.TrimSpace(c.Mode))
	switch c.Mode {
	case LiveCompactOff, LiveCompactShadow, LiveCompactOn:
	case "":
		c.Mode = LiveCompactOff
	default:
		log.Printf("[live-compact] unknown mode %q, using off", c.Mode)
		c.Mode = LiveCompactOff
	}
	if c.KeepRounds <= 0 {
		c.KeepRounds = 3
	}
	if c.KeepTailTokens <= 0 {
		c.KeepTailTokens = 32000
	}
	if c.MinBlockTokens <= 0 {
		c.MinBlockTokens = 2000
	}
	if c.MinBodyBytes <= 0 {
		c.MinBodyBytes = 100 << 10
	}
	if c.MaxDriftMessages <= 0 {
		c.MaxDriftMessages = 8192
	}
	// A negative rail disables the gate; zero means the default.
	//
	// The defaults are the "fold deep, fold rarely" shape measured in design
	// §12.7: cut all the way down (target 20k; the protected tail — keep_rounds
	// plus keep_tail_tokens ≈ 32k — is what actually stops the cut) so one event
	// repays its cache rewrite in a round or two. The rail is 100k, not the
	// CLI's ~167k auto-compact threshold: the seven-arm uniform-read bench put
	// 100k within noise of 160k on cost (339,933–380,111 vs 340,190–367,914) but
	// capped the resident request at 95.2k instead of 129.7k. Going lower is not
	// free: 50k folds one block per turn, re-prices the whole tail every turn,
	// and measured ~+10% (plan, B4 Step 8). The earlier 130k/100k pair folded
	// every ~3 rounds by ~15%, which needs ~33 rounds to break even and measured
	// 2.26x the cost of not folding at all.
	if c.RailTokens == 0 {
		c.RailTokens = 100000
	}
	if c.RailTokens > 0 {
		if c.TargetTokens <= 0 {
			c.TargetTokens = 20000
		}
		// A target at or above the rail would make one event commit nothing and
		// leave the next request above the rail again.
		if c.TargetTokens >= c.RailTokens {
			c.TargetTokens = c.RailTokens * 3 / 4
		}
	}
	// dsh's chunkMin is 1000 tokens for one cut; below that the cache break is
	// pure loss (design §12.7(b)).
	if c.MinFoldBytes <= 0 {
		c.MinFoldBytes = 4096
	}
	// Zero means the default cap, not "unlimited": one event must not fold the
	// whole history (design §12.6 budget). A value below the minimum would make
	// every event impossible, so it is raised to the minimum.
	//
	// This caps one chunk; one event may drain up to foldMaxChunksPerEvent of
	// them (design §12.7(d)). A whole-event cap would stop the drain after ~2
	// blocks, which is the shallow fold §12.7(b) measured as worse than none.
	if c.MaxFoldBytes == 0 {
		c.MaxFoldBytes = 262144
	}
	if c.MaxFoldBytes > 0 && c.MaxFoldBytes < c.MinFoldBytes {
		c.MaxFoldBytes = c.MinFoldBytes
	}
	if c.LargeBlockTokens <= 0 {
		c.LargeBlockTokens = 32000
	}
	// 60k: below this there is still ~100k of room to the CLI's auto-compact
	// threshold, so a cold fold buys no headroom and only risks continuity.
	if c.ColdRailTokens <= 0 {
		c.ColdRailTokens = 60000
	}
	if c.CacheTTLSeconds <= 0 {
		c.CacheTTLSeconds = 300
	}
	if c.RetentionDays <= 0 {
		c.RetentionDays = int(archiveRetention / (24 * time.Hour))
	}
	if c.MaxArchiveBytes <= 0 {
		c.MaxArchiveBytes = archiveMaxBytes
	}
	if c.ArchiveDir == "" {
		c.ArchiveEnabled = false
	}
	return c
}

// Observes reports whether requests should be parsed for measurement. Both
// shadow and on parse; only on may rewrite (Phase B1).
func (c LiveCompactConfig) Observes() bool {
	return c.Mode == LiveCompactShadow || c.Mode == LiveCompactOn
}

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
	cfg := s.liveCompact
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
	cfg := s.liveCompact
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
	scope := "fold"
	if !obs.foldAllowed {
		scope = "observe-only"
	}
	log.Printf("[live-compact] %s token=%s scope=%s msgs=%d rounds=%d closed=%d keep=%d epoch=%d est_in=%d actual_in=%d usg_in=%d usg_create=%d usg_read=%d cand_blocks=%d cand_error_blocks=%d cand_failed_blocks=%d cand_tokens=%d cand_bytes=%d skip_bytes_error=%d skip_bytes_multipart=%d skip_bytes_nontext=%d drift=%d drift_age=%d parse_ms=%d gate=%s gate_est=%d gate_growth=%d gate_projected=%d cold=%t cold_reason=%s would_fold_tokens=%d",
		obs.mode, ShortToken(token), scope, obs.messages, obs.rounds, obs.closedRounds,
		obs.keepRounds, obs.epoch, obs.estimatedInput, actual,
		totals.InputTokens, totals.CacheCreationInputTokens, totals.CacheReadInputTokens,
		obs.candidates, obs.errorCandidates,
		obs.failedCandidates, obs.candidateTokens, obs.candidateBytes,
		obs.skippedErrorBytes, obs.skippedMultiPartBytes, obs.skippedNonTextBytes,
		obs.driftAt, obs.driftFromTail, obs.parseMillis,
		obs.gateDecision, obs.gateEst, obs.gateGrowth, obs.gateProjected,
		obs.cold, obs.coldReason, obs.wouldFoldTokens)
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
