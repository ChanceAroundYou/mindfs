package proxy

import (
	"testing"
	"time"
)

// Cold-cache timing (design 2026-11-20 §5): when the upstream prompt cache is
// already gone, a fold costs no rewrite, so the rail — which exists only to make
// the rewrite pay for itself — is the wrong trigger.

func coldTestConfig() LiveCompactConfig {
	return LiveCompactConfig{
		RailTokens:      100000,
		TargetTokens:    20000,
		ColdRailTokens:  60000,
		CacheTTLSeconds: 300,
		MinFoldBytes:    4096,
		MaxFoldBytes:    262144,
		ColdFold:        nil, // default: on
	}.Normalized()
}

func TestColdFoldFoldsBelowRail(t *testing.T) {
	cfg := coldTestConfig()
	budget, gate := decideFoldBudget(cfg, foldContext{est: 70000, cold: true, coldReason: coldReasonCompact}, 70000, false)
	if gate.decision != gateFold || !budget.commitNew {
		t.Fatalf("decision = %s commitNew=%v, want fold (cold, 70k)", gate.decision, budget.commitNew)
	}
	if !gate.cold || gate.coldReason != coldReasonCompact {
		t.Fatalf("gate cold=%v reason=%q, want true/compact", gate.cold, gate.coldReason)
	}
}

// Below the cold rail the fold buys no headroom and only costs continuity, so
// being cold is not enough on its own.
func TestColdFoldIgnoredBelowColdRail(t *testing.T) {
	cfg := coldTestConfig()
	_, gate := decideFoldBudget(cfg, foldContext{est: 50000, cold: true, coldReason: coldReasonCompact}, 50000, false)
	if gate.decision != gateBelowRail {
		t.Fatalf("decision = %s, want %s", gate.decision, gateBelowRail)
	}
}

// A warm cache keeps the old behaviour no matter how big the context is.
func TestColdFoldNotTriggeredWhenCacheIsWarm(t *testing.T) {
	cfg := coldTestConfig()
	_, gate := decideFoldBudget(cfg, foldContext{est: 90000}, 90000, false)
	if gate.decision != gateBelowRail {
		t.Fatalf("decision = %s, want %s", gate.decision, gateBelowRail)
	}
}

func TestColdFoldDisabledKeepsRail(t *testing.T) {
	off := false
	cfg := coldTestConfig()
	cfg.ColdFold = &off
	_, gate := decideFoldBudget(cfg, foldContext{est: 70000, cold: true, coldReason: coldReasonCompact}, 70000, false)
	if gate.decision != gateBelowRail {
		t.Fatalf("decision = %s, want %s when cold_fold is off", gate.decision, gateBelowRail)
	}
}

// The rail still wins over the cold rail: a hot request above the rail folds.
func TestRailStillFoldsWhenHot(t *testing.T) {
	cfg := coldTestConfig()
	_, gate := decideFoldBudget(cfg, foldContext{est: 120000}, 120000, false)
	if gate.decision != gateFold {
		t.Fatalf("decision = %s, want fold above the rail", gate.decision)
	}
}

// Being cold must not change *what* is eligible, only whether the event may
// start. Same body, same commitment set state: a cold fold must pick exactly
// the blocks a rail fold would have picked.
func TestColdFoldKeepsCandidateEligibility(t *testing.T) {
	dir := t.TempDir()
	coldCfg := gateTestConfig(dir)
	coldCfg.RailTokens = 1 << 30 // far above, so only the cold rail can fire
	coldCfg.ColdRailTokens = 1
	coldCfg.ColdFold = nil

	// The same policy with a rail low enough to fire on its own: this is the
	// reference selection.
	railCfg := coldCfg
	railCfg.RailTokens = 1

	body := distinctFoldBody(t, 4, 8001)
	req, err := scanWireRequest(body)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	rail, railGate := previewFold(body, req, railCfg, newFoldCommits(), foldContext{est: 1 << 20})
	cold, coldGate := previewFold(body, req, coldCfg, newFoldCommits(), foldContext{est: 1 << 20, cold: true, coldReason: coldReasonCompact})
	if coldGate.decision != gateFold || railGate.decision != gateFold {
		t.Fatalf("decisions = %s/%s, want fold/fold", coldGate.decision, railGate.decision)
	}
	if cold.tokens != rail.tokens || cold.blocks != rail.blocks || cold.newBlocks != rail.newBlocks {
		t.Fatalf("cold preview = %+v, rail preview = %+v; eligibility must not change", cold, rail)
	}
}

// The PostCompact signal marks exactly one decision cold: consuming it once is
// what keeps an entire epoch from being treated as freshly compacted.
func TestColdFoldSignalConsumedOnce(t *testing.T) {
	cfg := coldTestConfig()
	s := &Server{sessions: map[string]*sessionState{"tok": {foldAllowed: true}}}
	now := time.Now()
	s.sessions["tok"].live.pendingColdFold = true

	if cold, reason := s.coldFoldContext("tok", cfg, now); !cold || reason != coldReasonCompact {
		t.Fatalf("first decision cold=%v reason=%q, want true/compact", cold, reason)
	}
	if cold, reason := s.coldFoldContext("tok", cfg, now); cold {
		t.Fatalf("second decision cold=%v reason=%q, want hot", cold, reason)
	}
}

func TestColdFoldIdleBeyondTTL(t *testing.T) {
	cfg := coldTestConfig()
	s := &Server{sessions: map[string]*sessionState{"tok": {foldAllowed: true}}}
	s.sessions["tok"].live.lastUsageAt = time.Now().Add(-301 * time.Second)

	cold, reason := s.coldFoldContext("tok", cfg, time.Now())
	if !cold || reason != coldReasonIdle {
		t.Fatalf("cold=%v reason=%q, want true/idle", cold, reason)
	}
}

func TestColdFoldNoReadStreak(t *testing.T) {
	cfg := coldTestConfig()
	s := &Server{sessions: map[string]*sessionState{"tok": {foldAllowed: true}}}
	s.sessions["tok"].live.lastUsageAt = time.Now()
	s.sessions["tok"].live.lastContext = 80000
	s.sessions["tok"].live.noReadStreak = coldNoReadStreak

	cold, reason := s.coldFoldContext("tok", cfg, time.Now())
	if !cold || reason != coldReasonNoRead {
		t.Fatalf("cold=%v reason=%q, want true/no-read", cold, reason)
	}
}

// One cache-less response is an ordinary cold start: the next request is warm
// again, so it must not be enough.
func TestColdFoldSingleNoReadIsNotEnough(t *testing.T) {
	cfg := coldTestConfig()
	s := &Server{sessions: map[string]*sessionState{"tok": {foldAllowed: true}}}
	s.sessions["tok"].live.lastUsageAt = time.Now()
	s.sessions["tok"].live.lastContext = 80000
	s.sessions["tok"].live.noReadStreak = 1

	if cold, _ := s.coldFoldContext("tok", cfg, time.Now()); cold {
		t.Fatal("a single cache-less response must not count as cold")
	}
}

// The usage path feeds the detector, and a cache-writing response resets it.
func TestNoteActualContextTracksCacheSignals(t *testing.T) {
	s := &Server{sessions: map[string]*sessionState{"tok": {foldAllowed: true}}}
	s.noteActualContext("tok", usageTotals{Seen: true, InputTokens: 70000})
	if s.sessions["tok"].live.noReadStreak != 1 {
		t.Fatalf("streak = %d, want 1", s.sessions["tok"].live.noReadStreak)
	}
	if s.sessions["tok"].live.lastContext != 70000 {
		t.Fatalf("last context = %d, want 70000", s.sessions["tok"].live.lastContext)
	}
	s.noteActualContext("tok", usageTotals{Seen: true, CacheReadInputTokens: 5000})
	if s.sessions["tok"].live.noReadStreak != 0 {
		t.Fatalf("streak = %d, want reset to 0", s.sessions["tok"].live.noReadStreak)
	}
}

// A cold fold is a change of trigger, not of budget: it must still carry the
// per-event floor and cap, or one cold event could fold the whole history.
func TestColdFoldKeepsFoldBudget(t *testing.T) {
	cfg := coldTestConfig()
	budget, gate := decideFoldBudget(cfg, foldContext{est: 70000, cold: true, coldReason: coldReasonCompact}, 70000, false)
	if gate.decision != gateFold {
		t.Fatalf("decision = %s, want fold", gate.decision)
	}
	if !budget.commitNew || budget.minNewBytes != cfg.MinFoldBytes || budget.maxNewBytes != cfg.MaxFoldBytes {
		t.Fatalf("budget = %+v, want min=%d max=%d", budget, cfg.MinFoldBytes, cfg.MaxFoldBytes)
	}
}
