package kanban

import (
	"context"
	"testing"
	"time"
)

// 卡片派生字段的判据：
//   - router_ask 只认 session_key 为空的 waiting_user 提问卡；
//   - worker 透传 ask 仍只体现在 aux_flags.ask_user_waiting，不进 router_ask；
//   - latest_attention 取最新一条非 hidden 的 attention，severity 原样透传。
func TestTaskStoreCardPromptsSeparateRouterAskFromWorkerAsk(t *testing.T) {
	svc, root := newTestService(t, nil)
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("task store: %v", err)
	}
	ctx := context.Background()
	base := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)

	for _, id := range []string{"task-router", "task-worker"} {
		if _, err := store.CreateTask(ctx,
			Task{ID: id, RootID: root.ID, Name: id, Status: StatusWaitingUser},
			TaskEvent{ID: id + "-created", TaskID: id, Type: "created"}); err != nil {
			t.Fatalf("CreateTask(%s): %v", id, err)
		}
	}
	// worker 透传 ask 的真实形态：会话卡（session_key 非空）+ aux flag。
	yes := true
	if err := store.UpdateTaskAuxFlags(ctx, "task-worker", TaskAuxFlagsPatch{AskUserWaiting: &yes}); err != nil {
		t.Fatalf("UpdateTaskAuxFlags: %v", err)
	}

	activities := []*TaskActivity{
		// Router 自己派发的两轮提问：后一张是当前问题，最早时间仍从第一张算。
		{ID: "router-old", TaskID: "task-router", RootID: root.ID, Type: ActivityTypeQuestion,
			Role: "agent", Status: ActivityStatusWaitingUser, Body: "旧问题", CreatedAt: base},
		{ID: "router-new", TaskID: "task-router", RootID: root.ID, Type: ActivityTypeQuestion,
			Role: "agent", Status: ActivityStatusWaitingUser, Body: "新问题", CreatedAt: base.Add(time.Second)},
		// 会话 worker 透传的提问卡：同类型同状态，但 session_key 非空。
		{ID: "worker-ask", TaskID: "task-worker", RootID: root.ID, Type: ActivityTypeQuestion,
			Role: "agent", Status: ActivityStatusWaitingUser, SessionKey: "session-1", Body: "会话问题", CreatedAt: base},
		// 提醒：隐藏行不参与，最新可见行决定卡片内容。
		{ID: "att-old", TaskID: "task-router", RootID: root.ID, Type: ActivityTypeAttention,
			Role: "system", Status: ActivityStatusCompleted, Severity: ActivitySeverityWarning,
			Body: "旧提醒", CreatedAt: base},
		{ID: "att-new", TaskID: "task-router", RootID: root.ID, Type: ActivityTypeAttention,
			Role: "system", Status: ActivityStatusCompleted, Severity: ActivitySeverityError,
			Body: "最新提醒", CreatedAt: base.Add(2 * time.Second)},
		{ID: "att-hidden", TaskID: "task-router", RootID: root.ID, Type: ActivityTypeAttention,
			Role: "system", Status: ActivityStatusCompleted, Severity: ActivitySeverityError,
			Body: "隐藏提醒", Hidden: true, CreatedAt: base.Add(3 * time.Second)},
		// 存量提醒没有 severity。
		{ID: "att-legacy", TaskID: "task-worker", RootID: root.ID, Type: ActivityTypeAttention,
			Role: "system", Status: ActivityStatusCompleted, Body: "存量提醒", CreatedAt: base},
	}
	acts := NewActivityStore(store.db)
	for _, activity := range activities {
		if err := acts.Insert(ctx, activity); err != nil {
			t.Fatalf("Insert(%s): %v", activity.ID, err)
		}
	}

	tasks, err := store.ListTasks(ctx, ListTasksOptions{})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	byID := map[string]Task{}
	for _, task := range tasks {
		byID[task.ID] = task
	}

	routerTask := byID["task-router"]
	if routerTask.RouterAsk == nil {
		t.Fatal("router-dispatched ask did not produce router_ask")
	}
	if routerTask.RouterAsk.Count != 2 || routerTask.RouterAsk.Question != "新问题" {
		t.Fatalf("router_ask = %+v, want count 2 with the newest question", routerTask.RouterAsk)
	}
	if want := base.Format(time.RFC3339Nano); routerTask.RouterAsk.Since != want {
		t.Fatalf("router_ask.since = %q, want %q", routerTask.RouterAsk.Since, want)
	}
	if routerTask.AuxFlags.AskUserWaiting {
		t.Fatal("router-dispatched ask must not set aux_flags.ask_user_waiting")
	}
	if routerTask.LatestAttention == nil || routerTask.LatestAttention.Body != "最新提醒" ||
		routerTask.LatestAttention.Severity != ActivitySeverityError {
		t.Fatalf("latest_attention = %+v, want the newest visible error notice", routerTask.LatestAttention)
	}

	workerTask := byID["task-worker"]
	if workerTask.RouterAsk != nil {
		t.Fatalf("session worker ask leaked into router_ask: %+v", workerTask.RouterAsk)
	}
	if !workerTask.AuxFlags.AskUserWaiting {
		t.Fatal("worker ask must keep riding on aux_flags.ask_user_waiting")
	}
	if workerTask.LatestAttention == nil || workerTask.LatestAttention.Severity != "" {
		t.Fatalf("latest_attention = %+v, want a legacy row with empty severity", workerTask.LatestAttention)
	}

	// GetTask 与列表必须给出同一份派生值。
	got, err := store.GetTask(ctx, "task-router")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.RouterAsk == nil || got.RouterAsk.Count != 2 || got.LatestAttention == nil ||
		got.LatestAttention.Body != "最新提醒" {
		t.Fatalf("GetTask derived fields = %+v / %+v", got.RouterAsk, got.LatestAttention)
	}
}

// 已回答/收口的 Router 提问卡不再算角标：状态不是 waiting_user 就不显示。
func TestTaskStoreCardPromptsDropAnsweredRouterAsk(t *testing.T) {
	svc, root := newTestService(t, nil)
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("task store: %v", err)
	}
	ctx := context.Background()
	if _, err := store.CreateTask(ctx,
		Task{ID: "task-1", RootID: root.ID, Status: StatusWaitingUser},
		TaskEvent{ID: "task-1-created", TaskID: "task-1", Type: "created"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if err := NewActivityStore(store.db).Insert(ctx, &TaskActivity{
		ID: "answered", TaskID: "task-1", RootID: root.ID, Type: ActivityTypeQuestion,
		Role: "agent", Status: ActivityStatusCompleted, Body: "已答问题",
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	tasks, err := store.ListTasks(ctx, ListTasksOptions{})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].RouterAsk != nil {
		t.Fatalf("router_ask = %+v, want nil after the question is answered", tasks[0].RouterAsk)
	}
}
