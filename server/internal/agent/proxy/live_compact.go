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
	// MinBlockTokens is the per-tool-result size needed to become a candidate.
	MinBlockTokens int
	// MinBodyBytes skips JSON parsing for requests too small to contain a
	// foldable result. This keeps ordinary short turns on the cheap path.
	MinBodyBytes int
	// MaxDriftMessages bounds the per-message fingerprint history kept for
	// detecting CLI-side history rewrites. A zero value means the default.
	MaxDriftMessages int
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
	if c.MinBlockTokens <= 0 {
		c.MinBlockTokens = 2000
	}
	if c.MinBodyBytes <= 0 {
		c.MinBodyBytes = 100 << 10
	}
	if c.MaxDriftMessages <= 0 {
		c.MaxDriftMessages = 8192
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
}

type liveCompactObservation struct {
	mode            string
	foldAllowed     bool
	messages        int
	rounds          int
	closedRounds    int
	keepRounds      int
	epoch           int
	estimatedInput  int
	candidates      int
	errorCandidates int
	candidateTokens int
	candidateBytes  int
	driftAt         int
	driftFromTail   int
	parseMillis     int64
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
func (s *Server) observeLiveCompact(token string, body []byte) *liveCompactTurn {
	cfg := s.liveCompact
	if !cfg.Observes() || len(body) < cfg.MinBodyBytes {
		return nil
	}
	if s.liveCompactDisabled() {
		return nil
	}
	turn := &liveCompactTurn{}
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
		mode:            cfg.Mode,
		foldAllowed:     foldAllowed,
		messages:        analysis.Messages,
		rounds:          analysis.Rounds,
		closedRounds:    analysis.ClosedRounds,
		keepRounds:      cfg.KeepRounds,
		epoch:           epoch,
		estimatedInput:  estimatedInput,
		candidates:      len(analysis.Candidates),
		errorCandidates: analysis.ErrorCandidates,
		candidateTokens: analysis.CandidateTokens,
		candidateBytes:  analysis.CandidateBytes,
		driftAt:         drift,
		driftFromTail:   driftDistanceFromTail(previousLen, drift),
		parseMillis:     parseMillis,
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
	log.Printf("[live-compact] %s token=%s scope=%s msgs=%d rounds=%d closed=%d keep=%d epoch=%d est_in=%d actual_in=%d cand_blocks=%d cand_error_blocks=%d cand_tokens=%d cand_bytes=%d drift=%d drift_age=%d parse_ms=%d",
		obs.mode, ShortToken(token), scope, obs.messages, obs.rounds, obs.closedRounds,
		obs.keepRounds, obs.epoch, obs.estimatedInput, actual, obs.candidates, obs.errorCandidates,
		obs.candidateTokens, obs.candidateBytes, obs.driftAt, obs.driftFromTail, obs.parseMillis)
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
