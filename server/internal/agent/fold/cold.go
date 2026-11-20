package fold

import "time"

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
	// ColdReasonCompact is returned when the CLI just compacted.
	ColdReasonCompact = "compact"
	// ColdReasonIdle is returned when the session was idle past the cache TTL.
	ColdReasonIdle = "idle"
	// ColdReasonNoRead is returned when consecutive responses neither read nor
	// wrote the cache.
	ColdReasonNoRead = "no-read"
)

// ColdNoReadStreak is how many consecutive cache-less responses make a session
// count as "this upstream does not cache it".
const ColdNoReadStreak = 2

// ColdSnapshot is what the decision depends on. The adapter/engine reads it
// from session state; ColdDecision itself is pure so the rule can be tested
// without a server.
//
// CompactedAt is the timestamp of the PostCompact flag. A flag older than the
// cache TTL is dropped: by then the cache is cold anyway and the idle rule
// reports it. Deciding *whether* to spend the flag (only when the plan had
// candidates) stays with the caller, because the request right after a
// compaction frequently has nothing foldable and consuming the flag there
// loses the one moment a fold is free.
type ColdSnapshot struct {
	PendingCompact bool
	CompactedAt    time.Time
	LastUsageAt    time.Time
	NoReadStreak   int
	LastContext    int
}

// ColdDecision reports whether this request may fold below the rail, and why.
// It does not mutate the snapshot; clearing a spent or stale flag is the
// caller's job, done under the same lock that read the snapshot.
func ColdDecision(s ColdSnapshot, cfg Config, now time.Time) (bool, string) {
	if !cfg.ColdFoldOn() {
		return false, ""
	}
	ttl := time.Duration(cfg.CacheTTLSeconds) * time.Second
	if s.PendingCompact && (s.CompactedAt.IsZero() || now.Sub(s.CompactedAt) < ttl) {
		return true, ColdReasonCompact
	}
	if !s.LastUsageAt.IsZero() && now.Sub(s.LastUsageAt) >= ttl {
		return true, ColdReasonIdle
	}
	if s.NoReadStreak >= ColdNoReadStreak && s.LastContext >= cfg.ColdRailTokens {
		return true, ColdReasonNoRead
	}
	return false, ""
}
