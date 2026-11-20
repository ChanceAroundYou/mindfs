package proxy

import (
	"time"
)

// Cold-cache folding (design 2026-11-20 §5).
//
// Folding's only hard cost is the upstream prefix cache: the request carrying a
// new fold boundary is billed at 1x (plus cache write) instead of the 0.1x read
// it would otherwise have been. That is why the rail exists. But when the cache
// is already gone, the rewrite costs nothing extra — the request was going to
// be billed at full price anyway. Those moments are the only ones where folding
// is free, and two of them are directly observable:
//
//   - the CLI just compacted (its own PostCompact hook tells us), so the whole
//     transcript was resent and nothing old is cached;
//   - the session was idle longer than the provider's cache TTL.
//
// The third signal is weaker and deliberately hard to trigger: two consecutive
// responses that read nothing from cache *and* wrote nothing to it, with a
// context above the cold rail. A single such response is just a cold start (the
// next request is warm again); two in a row means this upstream is not caching
// the session at all, in which case a fold costs nothing either.
//
// Being cold does not relax any other rule: the protected tail, the per-block
// floor, the chunk budget, the commitment set and the structural verification
// are all unchanged. It only replaces the rail with a lower one, because a
// lower one is what is worth doing when the rewrite is free.
const (
	coldReasonCompact = "compact"
	coldReasonIdle    = "idle"
	coldReasonNoRead  = "no-read"
)

// coldNoReadStreak is how many consecutive cache-less responses make a session
// count as "this upstream does not cache it".
const coldNoReadStreak = 2

// coldFoldContext consumes this request's cold signals. The PostCompact flag is
// cleared here, on the first fold decision after it was set, so one compaction
// yields one cold decision rather than marking the whole epoch cold.
func (s *Server) coldFoldContext(token string, cfg LiveCompactConfig, now time.Time) (bool, string) {
	s.mu.Lock()
	st, ok := s.sessions[token]
	if !ok {
		s.mu.Unlock()
		return false, ""
	}
	compacted := st.live.pendingColdFold
	st.live.pendingColdFold = false
	lastUsageAt := st.live.lastUsageAt
	noReadStreak := st.live.noReadStreak
	lastContext := st.live.lastContext
	s.mu.Unlock()

	if !cfg.ColdFoldOn() {
		return false, ""
	}
	if compacted {
		return true, coldReasonCompact
	}
	if !lastUsageAt.IsZero() && now.Sub(lastUsageAt) >= time.Duration(cfg.CacheTTLSeconds)*time.Second {
		return true, coldReasonIdle
	}
	if noReadStreak >= coldNoReadStreak && lastContext >= cfg.ColdRailTokens {
		return true, coldReasonNoRead
	}
	return false, ""
}
