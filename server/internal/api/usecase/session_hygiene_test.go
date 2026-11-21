package usecase

import (
	"testing"
	"time"
)

func hygieneConfig() HygieneConfig {
	return HygieneConfig{
		Enabled:        true,
		ActiveSeconds:  2 * 3600,
		ContextTokens:  140000,
		HintTurns:      3,
		IdleGapSeconds: 1800,
	}
}

// seedActive 用 30 分钟一格的观测把活跃时长堆到 hours 小时；格距在 idle gap
// 之内，所以每一格都计入。
func seedActive(tracker *HygieneTracker, root, key string, hours int) {
	base := time.Unix(1_700_000_000, 0)
	for i := 0; i <= hours*2; i++ {
		tracker.ObserveAt(root, key, "claude", base.Add(time.Duration(i)*30*time.Minute))
	}
}

// 三档行为是这次需求的全部：又久又大才提示并自动压缩；只大提示；只久什么
// 都不做。任何一个判据写反，用户要么被永久静音，要么在不该压缩的时候被压缩。
func TestEvaluateHygieneBands(t *testing.T) {
	cfg := hygieneConfig()
	cases := []struct {
		name    string
		active  int
		context int
		want    string
	}{
		{"long and large", 3 * 3600, 200000, HygieneStateHint},
		{"large only", 1800, 200000, HygieneStateNotice},
		{"long only", 3 * 3600, 50000, HygieneStateNormal},
		{"neither", 1800, 50000, HygieneStateNormal},
		{"exactly at both thresholds", 2 * 3600, 140000, HygieneStateHint},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			view := EvaluateHygiene(cfg, HygieneSnapshot{ActiveSeconds: tc.active, ContextTokens: tc.context})
			if view.State != tc.want {
				t.Fatalf("state = %q, want %q", view.State, tc.want)
			}
		})
	}
}

func TestEvaluateHygieneDisabledStopsEverything(t *testing.T) {
	cfg := hygieneConfig()
	cfg.Enabled = false
	view := EvaluateHygiene(cfg, HygieneSnapshot{ActiveSeconds: 10 * 3600, ContextTokens: 900000})
	if view.State != HygieneStateDisabled {
		t.Fatalf("state = %q, want disabled", view.State)
	}
	if view.AutoCompactDue {
		t.Fatal("disabled hygiene must never be due")
	}
	if view.TurnsUntilAutoCompact >= 0 {
		t.Fatalf("turns until auto = %d, want -1 when disabled", view.TurnsUntilAutoCompact)
	}
}

// 提示出现的那一轮不计数：用户看到提示时还剩完整的 N 轮，而不是一出现就少一轮。
func TestHygieneHintTurnIsNotCountedOnArrival(t *testing.T) {
	tracker := NewHygieneTracker(hygieneConfig())
	seedActive(tracker, "root", "s1", 3)
	view := tracker.Observe("root", "s1", "claude", 200000, true)
	if view.State != HygieneStateHint {
		t.Fatalf("state = %q, want hint", view.State)
	}
	if view.TurnsSinceHint != 0 {
		t.Fatalf("turns since hint = %d, want 0 on the arrival turn", view.TurnsSinceHint)
	}
	if view.TurnsUntilAutoCompact != 3 {
		t.Fatalf("turns until auto = %d, want 3", view.TurnsUntilAutoCompact)
	}
}

// 胶囊的读口在回合中间就会刷新：它先看到「又久又大」时绝不能把提示算作已经
// 出现过，否则紧接着的回合结束会把它当成「提示之后的第 1 轮」，用户的 N 轮
// 窗口整体少一轮 —— 线上实测出现过「提示和自动压缩同一刻发生」。
func TestHygieneSnapshotDoesNotConsumeTheArrivalTurn(t *testing.T) {
	tracker := NewHygieneTracker(hygieneConfig())
	seedActive(tracker, "root", "s1", 3)
	for i := 0; i < 3; i++ {
		if view := tracker.Snapshot("root", "s1", "claude", 200000); view.State != HygieneStateHint {
			t.Fatalf("snapshot %d state = %q, want hint", i, view.State)
		}
	}
	view := tracker.Observe("root", "s1", "claude", 200000, true)
	if view.TurnsSinceHint != 0 {
		t.Fatalf("turns since hint = %d, want 0: a read consumed the arrival turn", view.TurnsSinceHint)
	}
	if view.TurnsUntilAutoCompact != 3 {
		t.Fatalf("turns until auto = %d, want 3", view.TurnsUntilAutoCompact)
	}
	if view.AutoCompactDue {
		t.Fatal("the hint arrival turn must not be due")
	}
}

func TestHygieneAutoCompactBecomesDueAfterHintTurns(t *testing.T) {
	tracker := NewHygieneTracker(hygieneConfig())
	seedActive(tracker, "root", "s1", 3)
	tracker.Observe("root", "s1", "claude", 200000, true) // hint appears here, not counted
	for i := 1; i <= 2; i++ {
		view := tracker.Observe("root", "s1", "claude", 200000, true)
		if view.AutoCompactDue {
			t.Fatalf("due after %d of 3 turns", i)
		}
		if view.TurnsUntilAutoCompact != 3-i {
			t.Fatalf("turns until auto = %d after %d turns, want %d", view.TurnsUntilAutoCompact, i, 3-i)
		}
	}
	view := tracker.Observe("root", "s1", "claude", 200000, true)
	if !view.AutoCompactDue {
		t.Fatal("third turn after the hint must be due")
	}
}

// 读到不变的回包不等于用户又说了话：中间态查询不能偷偷把轮数推满。
func TestHygieneOnlyTurnEndsAdvanceTheCounter(t *testing.T) {
	tracker := NewHygieneTracker(hygieneConfig())
	seedActive(tracker, "root", "s1", 3)
	tracker.Observe("root", "s1", "claude", 200000, true)
	for i := 0; i < 5; i++ {
		view := tracker.Observe("root", "s1", "claude", 200000, false)
		if view.TurnsSinceHint != 0 {
			t.Fatalf("read-only observation advanced the counter to %d", view.TurnsSinceHint)
		}
	}
}

// 压缩之后上下文变小，提示必须自己消失；再变长要重新积累 N 轮，不能沿用旧计数。
func TestHygieneCompactionClearsHint(t *testing.T) {
	tracker := NewHygieneTracker(hygieneConfig())
	seedActive(tracker, "root", "s1", 3)
	tracker.Observe("root", "s1", "claude", 200000, true)
	tracker.Observe("root", "s1", "claude", 200000, true)
	// 压缩之后上下文仍然很大（真实压缩只是把历史摘要掉）：清提示不能靠
	// 「掉出大档」这条分支顺带完成，MarkCompacted 必须自己把状态清干净。
	tracker.MarkCompacted("root", "s1")
	view := tracker.Snapshot("root", "s1", "claude", 200000)
	if view.TurnsSinceHint != 0 || view.TurnsUntilAutoCompact != 3 {
		t.Fatalf("snapshot after compaction = %+v, want a fresh %d-turn window", view, hygieneConfig().HintTurns)
	}
	first := tracker.Observe("root", "s1", "claude", 200000, true)
	if first.State != HygieneStateHint {
		t.Fatalf("state after compaction = %q, want hint", first.State)
	}
	if first.TurnsSinceHint != 0 {
		t.Fatalf("re-hinted session counted the arrival turn: %d", first.TurnsSinceHint)
	}
	if first.AutoCompactDue {
		t.Fatal("re-hinted session must not be due on the arrival turn")
	}
	if first.TurnsUntilAutoCompact != 3 {
		t.Fatalf("re-hinted session starts at %d turns until auto, want 3", first.TurnsUntilAutoCompact)
	}
}

// 活跃时长按间隔累计，空闲超过 idle gap 的那一段不算 —— 挂着过夜不是"持续久"。
func TestHygieneActiveTimeIgnoresIdleGaps(t *testing.T) {
	cfg := hygieneConfig()
	cfg.ActiveSeconds = 3600
	cfg.ContextTokens = 1
	tracker := NewHygieneTracker(cfg)
	base := time.Unix(1_700_000_000, 0)
	tracker.ObserveAt("root", "s1", "claude", base)
	tracker.ObserveAt("root", "s1", "claude", base.Add(30*time.Minute))
	tracker.ObserveAt("root", "s1", "claude", base.Add(30*time.Minute+5*time.Minute))
	// 挂机 4 小时后回来：这一段不能计入。
	tracker.ObserveAt("root", "s1", "claude", base.Add(4*time.Hour+35*time.Minute))
	view := tracker.Snapshot("root", "s1", "claude", 1)
	if view.ActiveSeconds != 35*60 {
		t.Fatalf("active seconds = %d, want %d", view.ActiveSeconds, 35*60)
	}
}

func TestHygieneSessionsAreIsolated(t *testing.T) {
	tracker := NewHygieneTracker(hygieneConfig())
	seedActive(tracker, "root", "s1", 3)
	tracker.Observe("root", "s1", "claude", 200000, true)
	view := tracker.Snapshot("root", "s2", "claude", 200000)
	// 另一个会话只共享配置，不共享任何计数；它够大但不够久，停在 notice 档。
	if view.State != HygieneStateNotice || view.TurnsSinceHint != 0 || view.TurnsUntilAutoCompact != -1 {
		t.Fatalf("session s2 inherited s1 state: %+v", view)
	}
}

// 自动压缩失败一次后不再重试，直到用户手动压缩或会话离开提示带 —— 否则每轮
// 都朝一个坏通道重试，把失败变成噪声。
func TestHygieneAutoFailureStopsRetries(t *testing.T) {
	tracker := NewHygieneTracker(hygieneConfig())
	seedActive(tracker, "root", "s1", 3)
	tracker.Observe("root", "s1", "claude", 200000, true)
	tracker.Observe("root", "s1", "claude", 200000, true)
	tracker.Observe("root", "s1", "claude", 200000, true)
	tracker.MarkAutoFailed("root", "s1")
	view := tracker.Observe("root", "s1", "claude", 200000, true)
	if view.AutoCompactDue {
		t.Fatal("auto compaction must not be due after a failure")
	}
	if !view.AutoFailed {
		t.Fatal("view must report the previous failure")
	}
	tracker.MarkCompacted("root", "s1")
	view = tracker.Observe("root", "s1", "claude", 200000, true)
	if view.State != HygieneStateNotice && view.AutoFailed {
		t.Fatalf("compaction did not clear the failure: %+v", view)
	}
}

// 到期只报一次：自动压缩是异步的，派发之后、结果回来之前每轮都会再算出「到期」。
// 不拦的话，用户每说一句话就再开一次压缩回合。
func TestHygieneAutoCompactIsReportedOnlyOnce(t *testing.T) {
	tracker := NewHygieneTracker(hygieneConfig())
	seedActive(tracker, "root", "s1", 3)
	tracker.Observe("root", "s1", "claude", 200000, true)
	tracker.Observe("root", "s1", "claude", 200000, true)
	tracker.Observe("root", "s1", "claude", 200000, true)
	if !tracker.Observe("root", "s1", "claude", 200000, true).AutoCompactDue {
		t.Fatal("first due observation must report the trigger")
	}
	for i := 0; i < 3; i++ {
		view := tracker.Observe("root", "s1", "claude", 200000, true)
		if view.AutoCompactDue {
			t.Fatalf("due was reported again on follow-up observation %d", i)
		}
		if view.State != HygieneStateHint || !view.Enabled {
			t.Fatalf("view should keep showing the hint while the trigger is in flight: %+v", view)
		}
	}
	// 失败之后不再重试；一次真实压缩之后重新积累。
	tracker.MarkAutoFailed("root", "s1")
	if tracker.Observe("root", "s1", "claude", 200000, true).AutoCompactDue {
		t.Fatal("failed auto compaction must not be retried")
	}
	tracker.MarkCompacted("root", "s1")
	view := tracker.Observe("root", "s1", "claude", 200000, true)
	if view.AutoCompactDue || view.AutoFailed {
		t.Fatalf("compaction must clear the in-flight/failed state: %+v", view)
	}
}

func TestHygieneConfigHarnessOverride(t *testing.T) {
	cfg := HygieneConfig{
		Enabled:       true,
		ActiveSeconds: 7200,
		ContextTokens: 140000,
		HintTurns:     3,
		Harness: map[string]HygieneConfig{
			"codex": {ContextTokens: 180000, HintTurns: 2},
		},
	}
	tracker := NewHygieneTracker(cfg)
	codex := tracker.ConfigFor("codex")
	if codex.ContextTokens != 180000 || codex.HintTurns != 2 {
		t.Fatalf("codex override = %+v", codex)
	}
	if codex.ActiveSeconds != 7200 || !codex.Enabled {
		t.Fatalf("codex override dropped global fields: %+v", codex)
	}
	claude := tracker.ConfigFor("claude")
	if claude.ContextTokens != 140000 || claude.HintTurns != 3 {
		t.Fatalf("claude inherited codex override: %+v", claude)
	}
}

func TestCompactPendingAndErrorAreVisibleInSnapshot(t *testing.T) {
	tracker := NewHygieneTracker(hygieneConfig())
	tracker.MarkCompactPending("root", "s1")
	view := tracker.Snapshot("root", "s1", "claude", 200000)
	if !view.CompactPending || view.CompactError != "" {
		t.Fatalf("pending compact was not surfaced: %+v", view)
	}

	tracker.FinishCompact("root", "s1", "codex thread error: token expired\nplease sign in again")
	view = tracker.Snapshot("root", "s1", "claude", 200000)
	if view.CompactPending {
		t.Fatalf("finished compact still pending: %+v", view)
	}
	if want := "codex thread error: token expired please sign in again"; view.CompactError != want {
		t.Fatalf("compact error = %q, want %q", view.CompactError, want)
	}

	tracker.MarkCompactPending("root", "s1")
	view = tracker.Snapshot("root", "s1", "claude", 200000)
	if !view.CompactPending || view.CompactError != "" {
		t.Fatalf("a new compact must clear the previous error: %+v", view)
	}
}
