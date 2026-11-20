package fold

import (
	"testing"
	"time"
)

// One compaction yields exactly one cold decision: the flag is spent by the
// caller after the plan is built, so a second read must not report cold.
func TestColdDecisionCompactConsumesOnce(t *testing.T) {
	cfg := Config{}.Normalized()
	s := ColdSnapshot{PendingCompact: true}
	if cold, reason := ColdDecision(s, cfg, time.Now()); !cold || reason != ColdReasonCompact {
		t.Fatalf("got %v %q", cold, reason)
	}
	s.PendingCompact = false
	if cold, _ := ColdDecision(s, cfg, time.Now()); cold {
		t.Fatal("second decision must not be cold")
	}
}

// A flag older than the cache TTL is stale: the cache is cold anyway, and the
// idle rule reports it with the right reason.
func TestColdDecisionDropsStaleCompactFlag(t *testing.T) {
	cfg := Config{}.Normalized()
	now := time.Now()
	s := ColdSnapshot{
		PendingCompact: true,
		CompactedAt:    now.Add(-time.Duration(cfg.CacheTTLSeconds)*time.Second - time.Second),
	}
	if cold, reason := ColdDecision(s, cfg, now); cold || reason != "" {
		t.Fatalf("got %v %q, want not cold without usage history", cold, reason)
	}
	// With a usage gap it is still cold, but as "idle" rather than "compact".
	s.LastUsageAt = s.CompactedAt
	if cold, reason := ColdDecision(s, cfg, now); !cold || reason != ColdReasonIdle {
		t.Fatalf("got %v %q, want idle", cold, reason)
	}
}

func TestColdDecisionIdleBeyondTTL(t *testing.T) {
	cfg := Config{}.Normalized()
	s := ColdSnapshot{LastUsageAt: time.Now().Add(-301 * time.Second)}
	if cold, reason := ColdDecision(s, cfg, time.Now()); !cold || reason != ColdReasonIdle {
		t.Fatalf("got %v %q", cold, reason)
	}
	// Just inside the TTL is still warm.
	s.LastUsageAt = time.Now().Add(-299 * time.Second)
	if cold, _ := ColdDecision(s, cfg, time.Now()); cold {
		t.Fatal("inside the TTL must not be cold")
	}
}

func TestColdDecisionNoReadNeedsRail(t *testing.T) {
	cfg := Config{}.Normalized()
	if cold, _ := ColdDecision(ColdSnapshot{NoReadStreak: 2, LastContext: 59999}, cfg,
		time.Now()); cold {
		t.Fatal("below cold rail must not fold")
	}
	if cold, reason := ColdDecision(ColdSnapshot{NoReadStreak: 2, LastContext: 60000}, cfg,
		time.Now()); !cold || reason != ColdReasonNoRead {
		t.Fatalf("got %v %q", cold, reason)
	}
	// One cache-less response is a cold start, not an uncached upstream.
	if cold, _ := ColdDecision(ColdSnapshot{NoReadStreak: 1, LastContext: 100000}, cfg,
		time.Now()); cold {
		t.Fatal("a single cache-less response must not count")
	}
}

// Turning cold folding off makes every signal inert, including a pending
// compaction flag.
func TestColdDecisionDisabled(t *testing.T) {
	cfg := Config{}.Normalized()
	off := false
	cfg.ColdFold = &off
	if cold, _ := ColdDecision(ColdSnapshot{PendingCompact: true}, cfg, time.Now()); cold {
		t.Fatal("disabled cold folding decided cold")
	}
}
