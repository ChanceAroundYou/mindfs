package proxy

import (
	"time"

	"mindfs/server/internal/agent/fold"
)

// Cold-cache folding lives in the kernel (fold.ColdDecision). The proxy owns
// only the session state it reads and the one rule that needs a lock: whether
// the PostCompact flag is spent, and when it is too old to mean anything.
//
// Whether the flag is spent is decided by consumeColdFold once the planner
// knows it had candidates: the request right after a compaction frequently has
// nothing foldable, and consuming the flag there loses the one moment a fold is
// free.

// peekColdFold reads this request's cold signals without spending the
// PostCompact flag.
func (s *Server) peekColdFold(token string, cfg LiveCompactConfig, now time.Time) (bool, string) {
	s.mu.Lock()
	st, ok := s.sessions[token]
	if !ok {
		s.mu.Unlock()
		return false, ""
	}
	snapshot := fold.ColdSnapshot{
		PendingCompact: st.live.pendingColdFold,
		CompactedAt:    st.live.pendingColdFoldAt,
		LastUsageAt:    st.live.lastUsageAt,
		NoReadStreak:   st.live.noReadStreak,
		LastContext:    st.live.lastContext,
	}
	if snapshot.PendingCompact {
		ttl := time.Duration(cfg.CacheTTLSeconds) * time.Second
		if !snapshot.CompactedAt.IsZero() && now.Sub(snapshot.CompactedAt) >= ttl {
			st.live.pendingColdFold = false
			snapshot.PendingCompact = false
		}
	}
	s.mu.Unlock()

	return fold.ColdDecision(snapshot, cfg, now)
}

// consumeColdFold spends the PostCompact flag when a decision actually had
// material to act on. One compaction then yields one cold decision without
// being lost to an empty request in between.
func (s *Server) consumeColdFold(token string, used bool) {
	if !used {
		return
	}
	s.mu.Lock()
	if st, ok := s.sessions[token]; ok {
		st.live.pendingColdFold = false
	}
	s.mu.Unlock()
}
