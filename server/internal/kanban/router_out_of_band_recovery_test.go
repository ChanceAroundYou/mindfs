package kanban

import (
	"context"
	"strings"
	"testing"
	"time"
)

// C2：out-of-band 会话回合的持久化与重启恢复。
//
// 问题：用户在任务绑定的会话里自己推一轮时，任务头被推进到 running，标记只
// 活在进程内的 map 里（service_session_follow.go）。进程一重启，map 没了，
// 「回合结束就把任务放回 waiting_user」的收尾代码也永远不会被调用 —— 任务卡
// 永远转圈，用户看不到任何解释。线上 #106 型现场就是这样。
//
// 口径（用户 2026-11-23 裁定）：标记落在 router_task_state（不用事件反推）；
// 超过 30 分钟的 OOB 回合认定「已被重启带走」，恢复成 waiting_user 并记一条
// turn_interrupted 给 W3 巡检跟进。

func (f *turnSettledFixture) markOutOfBand(t *testing.T, startedAt time.Time) {
	t.Helper()
	if err := f.store.UpdateTaskStatus(context.Background(), f.taskID, StatusRunning, false); err != nil {
		t.Fatalf("UpdateTaskStatus(running): %v", err)
	}
	if err := NewRouterStore(f.store.db).MarkOutOfBand(context.Background(), f.rootID, f.taskID, "sess-main", startedAt); err != nil {
		t.Fatalf("MarkOutOfBand: %v", err)
	}
}

func (f *turnSettledFixture) outOfBandState(t *testing.T) outOfBandState {
	t.Helper()
	state, err := NewRouterStore(f.store.db).LoadOutOfBand(context.Background(), f.taskID)
	if err != nil {
		t.Fatalf("LoadOutOfBand: %v", err)
	}
	return state
}

func (f *turnSettledFixture) taskStatus(t *testing.T) string {
	t.Helper()
	task, err := f.store.GetTask(context.Background(), f.taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	return task.Status
}

// 标记必须真的落库：重启之后进程内 map 是空的，只剩这一行能证明「这一轮是
// OOB 回合、什么时候开始」。
func TestBeginOutOfBandSessionTurnPersistsMarker(t *testing.T) {
	ctx := context.Background()
	f := newTurnSettledFixture(t, "task-oob-mark", "sess-main")

	if !f.svc.BeginOutOfBandSessionTurn(ctx, f.rootID, "sess-main") {
		t.Fatal("BeginOutOfBandSessionTurn = false, want true")
	}
	state := f.outOfBandState(t)
	if !state.Active {
		t.Fatal("out-of-band marker was not persisted")
	}
	if state.SessionKey != "sess-main" {
		t.Fatalf("marker session = %q, want sess-main", state.SessionKey)
	}
	if state.StartedAt.IsZero() || time.Since(state.StartedAt) > time.Minute {
		t.Fatalf("marker started_at = %v, want about now", state.StartedAt)
	}
	if got := f.taskStatus(t); got != StatusRunning {
		t.Fatalf("task status = %q, want running", got)
	}
}

// 正常收尾（进程没死）：回合结算照旧把任务放回 waiting_user，标记必须一起清掉，
// 否则下一拍巡检会把一个已经结束的回合当成「超过 30 分钟被带走」再恢复一次。
func TestSettleSessionTurnClearsOutOfBandMarker(t *testing.T) {
	ctx := context.Background()
	f := newTurnSettledFixture(t, "task-oob-clear", "sess-main")
	// 正常路径必须走真实的 Begin：结算侧按进程内标记决定要不要收尾，只写库
	// 而不认领的回合本来就不该由 settle 负责。
	if !f.svc.BeginOutOfBandSessionTurn(ctx, f.rootID, "sess-main") {
		t.Fatal("BeginOutOfBandSessionTurn = false")
	}

	if _, err := f.svc.SettleSessionTurn(ctx, f.rootID, TurnSettledInput{
		SessionKey: "sess-main",
		TurnID:     "turn-oob-clear",
		Excerpt:    "这一轮正常跑完了。",
	}); err != nil {
		t.Fatalf("SettleSessionTurn: %v", err)
	}
	if state := f.outOfBandState(t); state.Active {
		t.Fatal("settled out-of-band turn left a stale marker")
	}
	if got := f.taskStatus(t); got != StatusWaitingUser {
		t.Fatalf("task status = %q, want waiting_user", got)
	}
}

// 重启恢复：超过 30 分钟的 OOB 回合恢复成 waiting_user，并落一条没有来源留言
// 的 turn_interrupted —— 巡检回合（C1）因此能回话，用户至少能看到「这一轮被
// 重启带走了」。重复对账不得写第二条事件。
func TestRecoverStaleOutOfBandTaskWritesInterruptedTurn(t *testing.T) {
	ctx := context.Background()
	f := newTurnSettledFixture(t, "task-oob-stale", "sess-main")
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	f.markOutOfBand(t, now.Add(-31*time.Minute))

	restored, err := f.svc.restoreStaleOutOfBandTasks(ctx, f.store, now)
	if err != nil {
		t.Fatalf("restoreStaleOutOfBandTasks: %v", err)
	}
	if restored != 1 {
		t.Fatalf("restored = %d, want 1", restored)
	}
	if got := f.taskStatus(t); got != StatusWaitingUser {
		t.Fatalf("task status = %q, want waiting_user", got)
	}
	events := f.events(t, RouterEventTurnInterrupted)
	if len(events) != 1 {
		t.Fatalf("turn_interrupted events = %d, want 1", len(events))
	}
	ev := events[0]
	if ev.UserActivityID != "" {
		t.Fatalf("event user activity = %q, want empty", ev.UserActivityID)
	}
	if ev.TurnID == "" {
		t.Fatal("event has no deterministic turn id")
	}
	if got := routerEventPayloadString(ev.PayloadJSON, "session_key"); got != "sess-main" {
		t.Fatalf("event session_key = %q, want sess-main", got)
	}
	if got := routerEventPayloadString(ev.PayloadJSON, "reason"); !strings.Contains(got, "out_of_band") {
		t.Fatalf("event reason = %q", got)
	}
	if state := f.outOfBandState(t); state.Active {
		t.Fatal("restored task kept its out-of-band marker")
	}

	replayed, err := f.svc.restoreStaleOutOfBandTasks(ctx, f.store, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("restoreStaleOutOfBandTasks (replay): %v", err)
	}
	if replayed != 0 {
		t.Fatalf("replay restored = %d, want 0", replayed)
	}
	if got := len(f.events(t, RouterEventTurnInterrupted)); got != 1 {
		t.Fatalf("replay wrote %d turn_interrupted events, want 1", got)
	}
}

// 事件写入必须按「任务 + 开始时刻」去重：真实恢复路径上如果事件写成功、
// 状态更新失败，下一拍会用同一个 instance key 重放，不能多写一条。
func TestOutOfBandInterruptedEventIsIdempotent(t *testing.T) {
	ctx := context.Background()
	f := newTurnSettledFixture(t, "task-oob-event", "sess-main")
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	f.markOutOfBand(t, now.Add(-31*time.Minute))
	task, err := f.store.GetTask(ctx, f.taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	item := outOfBandTask{TaskID: f.taskID, SessionKey: "sess-main", StartedAt: now.Add(-31 * time.Minute)}
	router := NewRouterStore(f.store.db)
	for i := 0; i < 2; i++ {
		if err := f.svc.insertOutOfBandInterruptedEvent(ctx, router, f.store, task, item, now.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("insert event #%d: %v", i+1, err)
		}
	}
	if got := len(f.events(t, RouterEventTurnInterrupted)); got != 1 {
		t.Fatalf("turn_interrupted events = %d, want 1", got)
	}
}

// 阈值以内不动：用户可能只是推了一轮很长的会话，30 分钟还没到就不是「被重启
// 带走」。恢复必须严格按 30 分钟这条线判定。
func TestRecoverFreshOutOfBandTurnLeavesTaskRunning(t *testing.T) {
	ctx := context.Background()
	f := newTurnSettledFixture(t, "task-oob-fresh", "sess-main")
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	f.markOutOfBand(t, now.Add(-29*time.Minute))

	restored, err := f.svc.restoreStaleOutOfBandTasks(ctx, f.store, now)
	if err != nil {
		t.Fatalf("restoreStaleOutOfBandTasks: %v", err)
	}
	if restored != 0 {
		t.Fatalf("restored = %d, want 0", restored)
	}
	if got := f.taskStatus(t); got != StatusRunning {
		t.Fatalf("task status = %q, want running", got)
	}
	if !f.outOfBandState(t).Active {
		t.Fatal("fresh out-of-band marker was cleared too early")
	}
	if got := len(f.events(t, RouterEventTurnInterrupted)); got != 0 {
		t.Fatalf("fresh turn wrote %d turn_interrupted events, want 0", got)
	}
}

// 会话还在写时绝不能按「被重启带走」恢复：慢回合本来就可能跑过 30 分钟，
// 现场 #104 的两轮都是这样被误判的（块流当时还在持续增长）。此时不恢复、
// 不写事件，**也不清标记** —— 清掉的话这一轮结束时收尾代码找不到 OOB 归属，
// 任务就再也回不到 waiting_user，等于把一种卡死换成另一种。
func TestRecoverStaleOutOfBandSkipsLiveSession(t *testing.T) {
	ctx := context.Background()
	f := newTurnSettledFixture(t, "task-oob-live", "sess-main")
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	f.markOutOfBand(t, now.Add(-31*time.Minute))

	var asked []string
	f.svc.SetSessionRunningProbe(func(sessionKey string) bool {
		asked = append(asked, sessionKey)
		return true
	})

	restored, err := f.svc.restoreStaleOutOfBandTasks(ctx, f.store, now)
	if err != nil {
		t.Fatalf("restoreStaleOutOfBandTasks: %v", err)
	}
	if restored != 0 {
		t.Fatalf("restored = %d, want 0 for a live session", restored)
	}
	if len(asked) != 1 || asked[0] != "sess-main" {
		t.Fatalf("probe asked %v, want exactly [sess-main]", asked)
	}
	if got := f.taskStatus(t); got != StatusRunning {
		t.Fatalf("task status = %q, want running (live turn must not be settled)", got)
	}
	if !f.outOfBandState(t).Active {
		t.Fatal("live out-of-band marker was cleared; the turn can no longer settle itself")
	}
	if got := len(f.events(t, RouterEventTurnInterrupted)); got != 0 {
		t.Fatalf("live turn wrote %d turn_interrupted events, want 0", got)
	}

	// 会话结束之后同一份标记必须仍然有效：它没被丢掉，那一轮跑完会被正常收尾。
	f.svc.SetSessionRunningProbe(func(string) bool { return false })
	restored, err = f.svc.restoreStaleOutOfBandTasks(ctx, f.store, now)
	if err != nil {
		t.Fatalf("restoreStaleOutOfBandTasks (idle): %v", err)
	}
	if restored != 1 {
		t.Fatalf("restored = %d after session went idle, want 1", restored)
	}
	if got := f.taskStatus(t); got != StatusWaitingUser {
		t.Fatalf("task status = %q, want waiting_user", got)
	}
}

// 任务已经不在 running（被用户取消、被别处收尾）时不该再改写状态，但标记是
// 上一轮留下的垃圾，必须清掉 —— 否则它会跟着任务一直留到下次命中阈值。
func TestRecoverOutOfBandSkipsNonRunningTaskAndClearsMarker(t *testing.T) {
	ctx := context.Background()
	f := newTurnSettledFixture(t, "task-oob-ended", "sess-main")
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	f.markOutOfBand(t, now.Add(-45*time.Minute))
	if err := f.store.UpdateTaskStatus(ctx, f.taskID, StatusSuccess, true); err != nil {
		t.Fatalf("UpdateTaskStatus(completed): %v", err)
	}

	restored, err := f.svc.restoreStaleOutOfBandTasks(ctx, f.store, now)
	if err != nil {
		t.Fatalf("restoreStaleOutOfBandTasks: %v", err)
	}
	if restored != 0 {
		t.Fatalf("restored = %d, want 0", restored)
	}
	if got := f.taskStatus(t); got != StatusSuccess {
		t.Fatalf("task status = %q, want completed (must not be reopened)", got)
	}
	if f.outOfBandState(t).Active {
		t.Fatal("stale marker on a non-running task was not cleared")
	}
	if got := len(f.events(t, RouterEventTurnInterrupted)); got != 0 {
		t.Fatalf("non-running task wrote %d turn_interrupted events, want 0", got)
	}
}
