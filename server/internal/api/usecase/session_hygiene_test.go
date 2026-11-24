package usecase

import (
	"testing"
	"time"
)

func hygieneConfig() HygieneConfig {
	return HygieneConfig{
		Enabled:        true,
		ActiveSeconds:  3600,
		ContextPercent: 70,
		OverLineTurns:  3,
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
		{"exactly at both thresholds", 3600, 140000, HygieneStateHint},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			view := EvaluateHygiene(cfg, HygieneSnapshot{ActiveSeconds: tc.active, ContextTokens: tc.context, ContextWindow: 200000})
			if view.State != tc.want {
				t.Fatalf("state = %q, want %q", view.State, tc.want)
			}
		})
	}
}

// 「大」是窗口的百分比，不是绝对 token 数：同一个 140k 在 Claude 的 200k 窗口上
// 已是七成，在 Codex 的 272k 窗口上还不够。窗口未知时按 200k 基准，这样导入的
// 旧会话与不报窗口的中转也有确定行为，而不是永远进不了提示档。
func TestHygieneContextThresholdIsAPercentageOfTheWindow(t *testing.T) {
	cfg := hygieneConfig()
	// 272000 的 70% = 190400。
	if view := EvaluateHygiene(cfg, HygieneSnapshot{ActiveSeconds: 3 * 3600, ContextTokens: 190399, ContextWindow: 272000}); view.State != HygieneStateNormal {
		t.Fatalf("below the 272k line = %q, want normal", view.State)
	}
	if view := EvaluateHygiene(cfg, HygieneSnapshot{ActiveSeconds: 3 * 3600, ContextTokens: 190400, ContextWindow: 272000}); view.State != HygieneStateHint {
		t.Fatalf("at the 272k line = %q, want hint", view.State)
	}
	// 未上报窗口：按 200k 基准，70% = 140000。
	if view := EvaluateHygiene(cfg, HygieneSnapshot{ActiveSeconds: 3 * 3600, ContextTokens: 139999}); view.State != HygieneStateNormal {
		t.Fatalf("below the 200k fallback line = %q, want normal", view.State)
	}
	if view := EvaluateHygiene(cfg, HygieneSnapshot{ActiveSeconds: 3 * 3600, ContextTokens: 140000}); view.State != HygieneStateHint {
		t.Fatalf("at the 200k fallback line = %q, want hint", view.State)
	}
	view := EvaluateHygiene(cfg, HygieneSnapshot{ContextWindow: 272000})
	if view.ContextLimit != 190400 || view.ContextWindow != 272000 || view.ContextPercent != 70 {
		t.Fatalf("view does not expose the window arithmetic: %+v", view)
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

// 「过线运行 3 轮」把刚过线的那一轮算作第 1 轮：第 3 个连续过线回合结束时压缩。
// 若不算它，实际会白送用户 4 轮，胶囊上的倒计时也会比真实行为慢一拍。
func TestHygieneOverLineCountsTheArrivalTurn(t *testing.T) {
	tracker := NewHygieneTracker(hygieneConfig())
	seedActive(tracker, "root", "s1", 3)
	view := tracker.Observe("root", "s1", "claude", 200000, 200000, true)
	if view.State != HygieneStateHint {
		t.Fatalf("state = %q, want hint", view.State)
	}
	if view.TurnsOverLine != 1 {
		t.Fatalf("turns over line = %d, want 1 (the arrival turn counts)", view.TurnsOverLine)
	}
	if view.TurnsUntilAutoCompact != 2 {
		t.Fatalf("turns until auto = %d, want 2", view.TurnsUntilAutoCompact)
	}
	if view.AutoCompactDue {
		t.Fatal("the arrival turn must not be due")
	}
}

func TestHygieneAutoCompactBecomesDueOnTheThirdOverLineTurn(t *testing.T) {
	tracker := NewHygieneTracker(hygieneConfig())
	seedActive(tracker, "root", "s1", 3)
	tracker.Observe("root", "s1", "claude", 200000, 200000, true) // 第 1 轮
	for i := 2; i <= 3; i++ {
		view := tracker.Observe("root", "s1", "claude", 200000, 200000, true)
		if i < 3 {
			if view.AutoCompactDue {
				t.Fatalf("due after %d of 3 over-line turns", i)
			}
			if view.TurnsUntilAutoCompact != 3-i {
				t.Fatalf("turns until auto = %d after %d turns, want %d", view.TurnsUntilAutoCompact, i, 3-i)
			}
			continue
		}
		if !view.AutoCompactDue {
			t.Fatal("the third consecutive over-line turn must be due")
		}
		if view.TurnsOverLine != 3 {
			t.Fatalf("turns over line = %d, want 3", view.TurnsOverLine)
		}
	}
}

// 连续过线：中间掉下去过就重新攒。否则一个会话几个月里断断续续过线，
// 攒够的总数也会在某天突然触发压缩。
func TestHygieneUnderLineTurnResetsTheStreak(t *testing.T) {
	tracker := NewHygieneTracker(hygieneConfig())
	seedActive(tracker, "root", "s1", 3)
	tracker.Observe("root", "s1", "claude", 200000, 200000, true)
	tracker.Observe("root", "s1", "claude", 200000, 200000, true)
	if view := tracker.Observe("root", "s1", "claude", 50000, 200000, true); view.TurnsOverLine != 0 {
		t.Fatalf("an under-line turn must reset the streak, got %d", view.TurnsOverLine)
	}
	view := tracker.Observe("root", "s1", "claude", 200000, 200000, true)
	if view.TurnsOverLine != 1 || view.AutoCompactDue {
		t.Fatalf("the streak must restart from 1: %+v", view)
	}
}

// 读到不变的回包不等于用户又说了话：中间态查询不能偷偷把轮数推满。
func TestHygieneOnlyTurnEndsAdvanceTheCounter(t *testing.T) {
	tracker := NewHygieneTracker(hygieneConfig())
	seedActive(tracker, "root", "s1", 3)
	tracker.Observe("root", "s1", "claude", 200000, 200000, true)
	for i := 0; i < 5; i++ {
		view := tracker.Observe("root", "s1", "claude", 200000, 200000, false)
		if view.TurnsOverLine != 1 {
			t.Fatalf("read-only observation advanced the counter to %d", view.TurnsOverLine)
		}
	}
}

// 只读路径同样不能推进：前端每次刷新胶囊都会调 Snapshot，它一旦顺手记一轮，
// 用户看到的倒计时就比真实行为快，实测会出现「提示和自动压缩同一刻发生」。
func TestHygieneReadPathDoesNotAdvanceTheCounter(t *testing.T) {
	tracker := NewHygieneTracker(hygieneConfig())
	seedActive(tracker, "root", "s1", 3)
	for i := 0; i < 3; i++ {
		view := tracker.Snapshot("root", "s1", "claude", 200000, 200000)
		if view.TurnsOverLine != 0 || view.AutoCompactDue {
			t.Fatalf("snapshot consumed a turn: %+v", view)
		}
	}
	if view := tracker.Observe("root", "s1", "claude", 200000, 200000, true); view.TurnsOverLine != 1 {
		t.Fatalf("observe after snapshots = %d, want 1", view.TurnsOverLine)
	}
}

// 压缩之后上下文变小，提示必须自己消失；再变长要重新积累 N 轮，不能沿用旧计数。
// 活跃时长也从这次压缩重新起算：旧会话的「久」不能让刚压缩完的会话立刻又到期。
func TestHygieneCompactionClearsHint(t *testing.T) {
	tracker := NewHygieneTracker(hygieneConfig())
	seedActive(tracker, "root", "s1", 3)
	tracker.Observe("root", "s1", "claude", 200000, 200000, true)
	tracker.Observe("root", "s1", "claude", 200000, 200000, true)
	// 压缩之后上下文仍然很大（真实压缩只是把历史摘要掉）：清提示不能靠
	// 「掉出大档」这条分支顺带完成，MarkCompacted 必须自己把状态清干净。
	base := time.Unix(1_700_000_000, 0)
	tracker.markCompactedAt("root", "s1", base)
	view := tracker.Snapshot("root", "s1", "claude", 200000, 200000)
	if view.ActiveSeconds != 0 || view.TurnsOverLine != 0 || view.State != HygieneStateNotice {
		t.Fatalf("snapshot after compaction = %+v, want fresh active time and no hint", view)
	}
	// 压缩后重新积累够「久」才重新进提示档，且从第 1 轮重新数。
	for i := 1; i <= 4; i++ {
		tracker.ObserveAt("root", "s1", "claude", base.Add(time.Duration(i)*30*time.Minute))
	}
	first := tracker.Observe("root", "s1", "claude", 200000, 200000, true)
	if first.State != HygieneStateHint {
		t.Fatalf("state after re-accumulating active time = %q, want hint", first.State)
	}
	if first.TurnsOverLine != 1 {
		t.Fatalf("re-hinted session = %d turns over line, want 1", first.TurnsOverLine)
	}
	if first.AutoCompactDue {
		t.Fatal("re-hinted session must not be due on the arrival turn")
	}
	if first.TurnsUntilAutoCompact != 2 {
		t.Fatalf("re-hinted session starts at %d turns until auto, want 2", first.TurnsUntilAutoCompact)
	}
}

// 活跃时长按间隔累计，空闲超过 idle gap 的那一段不算 —— 挂着过夜不是"持续久"。
func TestHygieneActiveTimeIgnoresIdleGaps(t *testing.T) {
	cfg := hygieneConfig()
	cfg.ActiveSeconds = 3600
	cfg.ContextPercent = 1
	tracker := NewHygieneTracker(cfg)
	base := time.Unix(1_700_000_000, 0)
	tracker.ObserveAt("root", "s1", "claude", base)
	tracker.ObserveAt("root", "s1", "claude", base.Add(30*time.Minute))
	tracker.ObserveAt("root", "s1", "claude", base.Add(30*time.Minute+5*time.Minute))
	// 挂机 4 小时后回来：这一段不能计入。
	tracker.ObserveAt("root", "s1", "claude", base.Add(4*time.Hour+35*time.Minute))
	view := tracker.Snapshot("root", "s1", "claude", 1, 200000)
	if view.ActiveSeconds != 35*60 {
		t.Fatalf("active seconds = %d, want %d", view.ActiveSeconds, 35*60)
	}
}

func TestHygieneSessionsAreIsolated(t *testing.T) {
	tracker := NewHygieneTracker(hygieneConfig())
	seedActive(tracker, "root", "s1", 3)
	tracker.Observe("root", "s1", "claude", 200000, 200000, true)
	view := tracker.Snapshot("root", "s2", "claude", 200000, 200000)
	// 另一个会话只共享配置，不共享任何计数；它够大但不够久，停在 notice 档。
	if view.State != HygieneStateNotice || view.TurnsOverLine != 0 || view.TurnsUntilAutoCompact != -1 {
		t.Fatalf("session s2 inherited s1 state: %+v", view)
	}
}

// 自动压缩失败一次后不再重试，直到用户手动压缩或会话离开提示带 —— 否则每轮
// 都朝一个坏通道重试，把失败变成噪声。
func TestHygieneAutoFailureStopsRetries(t *testing.T) {
	tracker := NewHygieneTracker(hygieneConfig())
	seedActive(tracker, "root", "s1", 3)
	tracker.Observe("root", "s1", "claude", 200000, 200000, true)
	tracker.Observe("root", "s1", "claude", 200000, 200000, true)
	tracker.Observe("root", "s1", "claude", 200000, 200000, true)
	tracker.MarkAutoFailed("root", "s1")
	view := tracker.Observe("root", "s1", "claude", 200000, 200000, true)
	if view.AutoCompactDue {
		t.Fatal("auto compaction must not be due after a failure")
	}
	if !view.AutoFailed {
		t.Fatal("view must report the previous failure")
	}
	tracker.MarkCompacted("root", "s1")
	view = tracker.Observe("root", "s1", "claude", 200000, 200000, true)
	if view.State != HygieneStateNotice && view.AutoFailed {
		t.Fatalf("compaction did not clear the failure: %+v", view)
	}
}

// 到期只报一次：自动压缩是异步的，派发之后、结果回来之前每轮都会再算出「到期」。
// 不拦的话，用户每说一句话就再开一次压缩回合。
func TestHygieneAutoCompactIsReportedOnlyOnce(t *testing.T) {
	tracker := NewHygieneTracker(hygieneConfig())
	seedActive(tracker, "root", "s1", 3)
	tracker.Observe("root", "s1", "claude", 200000, 200000, true)
	tracker.Observe("root", "s1", "claude", 200000, 200000, true)
	if !tracker.Observe("root", "s1", "claude", 200000, 200000, true).AutoCompactDue {
		t.Fatal("third over-line turn must report the trigger")
	}
	for i := 0; i < 3; i++ {
		view := tracker.Observe("root", "s1", "claude", 200000, 200000, true)
		if view.AutoCompactDue {
			t.Fatalf("due was reported again on follow-up observation %d", i)
		}
		if view.State != HygieneStateHint || !view.Enabled {
			t.Fatalf("view should keep showing the hint while the trigger is in flight: %+v", view)
		}
	}
	// 失败之后不再重试；一次真实压缩之后重新积累。
	tracker.MarkAutoFailed("root", "s1")
	if tracker.Observe("root", "s1", "claude", 200000, 200000, true).AutoCompactDue {
		t.Fatal("failed auto compaction must not be retried")
	}
	tracker.MarkCompacted("root", "s1")
	view := tracker.Observe("root", "s1", "claude", 200000, 200000, true)
	if view.AutoCompactDue || view.AutoFailed {
		t.Fatalf("compaction must clear the in-flight/failed state: %+v", view)
	}
}

func TestHygieneConfigHarnessOverride(t *testing.T) {
	cfg := HygieneConfig{
		Enabled:        true,
		ActiveSeconds:  3600,
		ContextPercent: 70,
		OverLineTurns:  3,
		Harness: map[string]HygieneConfig{
			"codex": {ContextPercent: 80, OverLineTurns: 2},
		},
	}
	tracker := NewHygieneTracker(cfg)
	codex := tracker.ConfigFor("codex")
	if codex.ContextPercent != 80 || codex.OverLineTurns != 2 {
		t.Fatalf("codex override = %+v", codex)
	}
	if codex.ActiveSeconds != 3600 || !codex.Enabled {
		t.Fatalf("codex override dropped global fields: %+v", codex)
	}
	claude := tracker.ConfigFor("claude")
	if claude.ContextPercent != 70 || claude.OverLineTurns != 3 {
		t.Fatalf("claude inherited codex override: %+v", claude)
	}
}

func TestCompactPendingAndErrorAreVisibleInSnapshot(t *testing.T) {
	tracker := NewHygieneTracker(hygieneConfig())
	view := tracker.Snapshot("root", "s1", "claude", 200000, 200000)
	if view.CompactPending || view.CompactError != "" {
		t.Fatalf("fresh snapshot = %+v", view)
	}

	tracker.MarkCompactPending("root", "s1")
	view = tracker.Snapshot("root", "s1", "claude", 200000, 200000)
	if !view.CompactPending {
		t.Fatalf("compact pending not visible: %+v", view)
	}

	tracker.FinishCompact("root", "s1", "codex thread error: token expired\nplease sign in again")
	view = tracker.Snapshot("root", "s1", "claude", 200000, 200000)
	if view.CompactPending {
		t.Fatalf("finished compact still pending: %+v", view)
	}
	if want := "codex thread error: token expired please sign in again"; view.CompactError != want {
		t.Fatalf("compact error = %q, want %q", view.CompactError, want)
	}

	tracker.MarkCompactPending("root", "s1")
	view = tracker.Snapshot("root", "s1", "claude", 200000, 200000)
	if !view.CompactPending || view.CompactError != "" {
		t.Fatalf("a new compact must clear the previous error: %+v", view)
	}
}

// 压缩回合没有新的 message_delta，持久化的上下文大小可能还是压缩前的旧值。
// 在新 token_count 到达之前，不能再拿旧值触发第二第三次压缩。
func TestHygieneStaleContextBlocksAutoCompactUntilCleared(t *testing.T) {
	tracker := NewHygieneTracker(hygieneConfig())
	seedActive(tracker, "root", "s1", 3)
	tracker.Observe("root", "s1", "claude", 200000, 200000, true) // 第 1 轮
	tracker.Observe("root", "s1", "claude", 200000, 200000, true) // 第 2 轮

	tracker.MarkContextStale("root", "s1")
	if view := tracker.Observe("root", "s1", "claude", 200000, 200000, true); view.AutoCompactDue {
		t.Fatal("stale context must not trigger another compaction")
	}
	tracker.ClearContextStale("root", "s1")
	if view := tracker.Observe("root", "s1", "claude", 200000, 200000, true); !view.AutoCompactDue {
		t.Fatalf("fresh context must release the gate: %+v", view)
	}
}
