package kanban

import (
	"context"
	"errors"
	"testing"

	"mindfs/server/internal/fs"
)

// 「终止」= 停掉正在跑的东西，任务落回待处理（不是取消）。三条契约：
//   1. Workflow Run 在跑 → 节点行与投影取消，任务 waiting_user；
//   2. 直连回合在跑 → 打断会话、在飞回复结算成 cancelled，任务 waiting_user；
//   3. 被打断的回合不能显示成红色失败 —— 标记登记簿必须跨实例可见。

func createRunningTask(t *testing.T, tasks *TaskStore, sessionKey string) {
	t.Helper()
	ctx := context.Background()
	if _, err := tasks.CreateTask(ctx,
		Task{ID: "task-1", RootID: "root", Status: StatusRunning, MainSessionKey: sessionKey},
		TaskEvent{ID: "event-created", TaskID: "task-1", Type: "created"},
	); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
}

func TestServiceTerminateCancelsDirectTurnAndParksTask(t *testing.T) {
	ctx := context.Background()
	root := fs.NewRootInfo("root", "root", t.TempDir())
	tasks, err := NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	t.Cleanup(func() { _ = tasks.Close() })
	createRunningTask(t, tasks, "sess-main")

	activities := NewActivityStore(tasks.db)
	live := &TaskActivity{
		ID: "act-live", TaskID: "task-1", RootID: root.ID,
		Type: ActivityTypeAgentReply, Role: RoleAgent, Status: ActivityStatusRunning, Body: "步骤 2/5",
	}
	if err := activities.Insert(ctx, live); err != nil {
		t.Fatalf("insert live reply: %v", err)
	}

	svc := NewService(testRoots{root: root})
	var cancelled []string
	svc.SetSessionTurnCanceller(func(_ context.Context, rootID, sessionKey string) error {
		cancelled = append(cancelled, rootID+"/"+sessionKey)
		return nil
	})

	if _, err := svc.Terminate(ctx, MoveInput{RootID: root.ID, TaskID: "task-1", Reason: "用户点了终止"}); err != nil {
		t.Fatalf("Terminate: %v", err)
	}

	if len(cancelled) != 1 || cancelled[0] != root.ID+"/sess-main" {
		t.Fatalf("session canceller calls = %v, want one call for the task main session", cancelled)
	}
	got, err := activities.Get(ctx, "act-live")
	if err != nil {
		t.Fatalf("Get activity: %v", err)
	}
	if got.Status != ActivityStatusCancelled || got.Summary != terminatedTurnSummary {
		t.Fatalf("live reply = status %q summary %q, want cancelled with the terminate note", got.Status, got.Summary)
	}
	if got.Body != "" {
		t.Fatalf("live reply body = %q, want cleared progress text", got.Body)
	}

	task, err := tasks.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != StatusWaitingUser {
		t.Fatalf("task status = %q, want %q after terminate", task.Status, StatusWaitingUser)
	}
	events, err := tasks.ListEvents(ctx, "task-1")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	found := false
	for _, ev := range events {
		if ev.Type == "task_terminated" {
			found = true
		}
	}
	if !found {
		t.Fatalf("events = %v, want a task_terminated event so the board can explain the stop", events)
	}
}

// 直连回合被叫停时 harness 返回的是「context canceled」这类错误：它不能被当成
// 故障写红色 failed，而要按终止落 cancelled。
func TestExecuteDirectActivityHonoursTerminationMarker(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)
	f.service.SetDirectExecutor(&fakeDirectExecutor{err: errors.New("context canceled"), done: make(chan struct{})})

	act := &TaskActivity{
		ID: "act-terminated", TaskID: "task-1", RootID: f.root.ID,
		Type: ActivityTypeAgentReply, Role: RoleAgent, Status: ActivityStatusRunning,
	}
	if err := f.service.activities.Insert(ctx, act); err != nil {
		t.Fatalf("insert agent reply: %v", err)
	}
	f.service.terminations.Mark(act.ID)
	task, err := f.tasks.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}

	f.service.executeDirectActivity(ctx, act, task, "跑一下")

	got, err := f.service.activities.Get(ctx, act.ID)
	if err != nil {
		t.Fatalf("Get activity: %v", err)
	}
	if got.Status != ActivityStatusCancelled {
		t.Fatalf("activity status = %q, want cancelled (an interrupted turn is not a failure)", got.Status)
	}
	if got.Summary != terminatedTurnSummary {
		t.Fatalf("activity summary = %q, want %q", got.Summary, terminatedTurnSummary)
	}
	// 终止的落点固定是待处理：不能因为回合开始前是 pending 就回滚成「未开始」。
	after, err := f.tasks.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if after.Status != StatusWaitingUser {
		t.Fatalf("task status = %q, want %q", after.Status, StatusWaitingUser)
	}
}

// Terminations 用完后即失效：同一轮不能因为标记残留而在下次正常失败时被吞掉。
func TestTurnTerminationsConsumeOnce(t *testing.T) {
	reg := newTurnTerminations()
	reg.Mark("act-1")
	if !reg.Consume("act-1") {
		t.Fatal("first Consume must report the marker")
	}
	if reg.Consume("act-1") {
		t.Fatal("marker must be single-use, otherwise a later real failure reads as user stop")
	}
	if reg.Consume("missing") {
		t.Fatal("unknown id must not report a marker")
	}
}

func TestServiceTerminateStopsWorkflowRun(t *testing.T) {
	ctx := context.Background()
	f := stalledRunFixture(t, RouterSupervisionOn)

	if _, err := f.svc.Terminate(ctx, MoveInput{RootID: f.root.ID, TaskID: "task-1", Reason: "用户点了终止"}); err != nil {
		t.Fatalf("Terminate: %v", err)
	}

	run, err := f.store.WorkflowRunByID(ctx, "run-1")
	if err != nil {
		t.Fatalf("WorkflowRunByID: %v", err)
	}
	if got := run.NodeStates["agent-1"]; got != NodeRunCancelled {
		t.Fatalf("run projection = %q, want %q", got, NodeRunCancelled)
	}
	task, err := f.store.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != StatusWaitingUser {
		t.Fatalf("task status = %q, want %q after terminate", task.Status, StatusWaitingUser)
	}
}
