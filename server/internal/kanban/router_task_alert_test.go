package kanban

import (
	"context"
	"testing"
	"time"
)

// W3-4：卡片角标的事实来源。
//
// 角标回答的是「Router 是否已经看过这件事」，所以判据只能是事件账本的**未处理**
// 状态：pending / claimed 算未处理；done / skipped / failed 都算 Router 已经处理过
// （failed 的来由在监管记录里，不靠一张永远擦不掉的角标表达）。前端只做展示，
// 不在浏览器里重算这套语义。

func insertAlertEvent(t *testing.T, store *TaskStore, id string, kind RouterEventKind, at time.Time) {
	t.Helper()
	_, _, err := NewRouterStore(store.db).InsertEvent(context.Background(), RouterEvent{
		ID:          id,
		RootID:      "root",
		TaskID:      "task-alert",
		Kind:        kind,
		InstanceKey: id,
		TurnID:      "turn-" + id,
		PayloadJSON: "{}",
		CreatedAt:   at,
	})
	if err != nil {
		t.Fatalf("InsertEvent(%s): %v", id, err)
	}
}

func finishAlertEvent(t *testing.T, store *TaskStore, id string) {
	t.Helper()
	ctx := context.Background()
	rs := NewRouterStore(store.db)
	if ok, err := rs.ClaimEvent(ctx, id, "test"); err != nil || !ok {
		t.Fatalf("ClaimEvent(%s) ok=%v err=%v", id, ok, err)
	}
	if err := rs.CompleteEvent(ctx, id, RouterEventDone, ""); err != nil {
		t.Fatalf("CompleteEvent(%s): %v", id, err)
	}
}

// 行为：未处理的 turn_interrupted 让任务卡带角标；Router 处理完之后（事件 done）
// 同一张卡再读一次就没有角标 —— 「处理后消失」由账本状态决定，不靠前端计时。
func TestTaskCarriesUnhandledHealthAlert(t *testing.T) {
	f := newTaskHealthFixture(t, "task-alert", StatusWaitingUser, "sess-alert")
	ctx := context.Background()
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	insertAlertEvent(t, f.store, "ev-interrupted", RouterEventTurnInterrupted, now.Add(-3*time.Minute))

	tasks, err := f.store.ListTasks(ctx, ListTasksOptions{})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].RouterAlert == nil {
		t.Fatalf("tasks = %+v, want one task with a router alert", tasks)
	}
	alert := tasks[0].RouterAlert
	if alert.Kind != string(RouterEventTurnInterrupted) || alert.Count != 1 {
		t.Fatalf("alert = %+v, want turn_interrupted ×1", alert)
	}
	if alert.Since != now.Add(-3*time.Minute).Format(routerTimeLayout) {
		t.Fatalf("alert.since = %q, want the event timestamp", alert.Since)
	}
	// 详情走 GetTask，也必须带着同一个角标：广播与列表不能有两种形状。
	single, err := f.store.GetTask(ctx, "task-alert")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if single.RouterAlert == nil || single.RouterAlert.Kind != string(RouterEventTurnInterrupted) {
		t.Fatalf("GetTask alert = %+v, want the same alert", single.RouterAlert)
	}

	finishAlertEvent(t, f.store, "ev-interrupted")
	tasks, err = f.store.ListTasks(ctx, ListTasksOptions{})
	if err != nil {
		t.Fatalf("ListTasks after handling: %v", err)
	}
	if tasks[0].RouterAlert != nil {
		t.Fatalf("alert = %+v after the event was handled, want nil", tasks[0].RouterAlert)
	}
}

// 行为：只有巡检/恢复这两类事件带角标。turn_settled 这类正常收尾、以及已结算的
// 事件都不该点亮卡片 —— 否则每一轮会话都会让卡片闪一次红点。
func TestTaskAlertOnlyCoversHealthEvents(t *testing.T) {
	f := newTaskHealthFixture(t, "task-alert", StatusRunning, "sess-alert")
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	insertAlertEvent(t, f.store, "ev-settled", RouterEventTurnSettled, now)
	insertAlertEvent(t, f.store, "ev-stalled-1", RouterEventTurnStalled, now.Add(-20*time.Minute))
	insertAlertEvent(t, f.store, "ev-stalled-2", RouterEventTurnStalled, now.Add(-15*time.Minute))

	tasks, err := f.store.ListTasks(context.Background(), ListTasksOptions{})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	alert := tasks[0].RouterAlert
	if alert == nil {
		t.Fatal("stalled turns produced no alert")
	}
	if alert.Kind != string(RouterEventTurnStalled) || alert.Count != 2 {
		t.Fatalf("alert = %+v, want turn_stalled ×2 (settled events ignored)", alert)
	}
}

// 行为：重启带走一轮（turn_interrupted）比停滞更需要用户看见，两者同时挂着时
// 角标报前者。
func TestTaskAlertPrefersInterruptedOverStalled(t *testing.T) {
	f := newTaskHealthFixture(t, "task-alert", StatusRunning, "sess-alert")
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	insertAlertEvent(t, f.store, "ev-stalled", RouterEventTurnStalled, now.Add(-20*time.Minute))
	insertAlertEvent(t, f.store, "ev-interrupted", RouterEventTurnInterrupted, now.Add(-10*time.Minute))

	tasks, err := f.store.ListTasks(context.Background(), ListTasksOptions{})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	alert := tasks[0].RouterAlert
	if alert == nil || alert.Kind != string(RouterEventTurnInterrupted) {
		t.Fatalf("alert = %+v, want turn_interrupted to win", alert)
	}
	if alert.Since != now.Add(-10*time.Minute).Format(routerTimeLayout) {
		t.Fatalf("alert.since = %q, want the interrupted event's own timestamp", alert.Since)
	}
}
