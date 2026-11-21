package kanban

import (
	"context"
	"strings"
	"testing"
	"time"
)

// 「一轮挂了」不等于「任务结束」。
//
// 2026-11-19 用户现场：任务 #91 的 execute 节点撞 502（insufficient credits），
// 一轮失败就把任务投影成终态 fail，看板把它归进「已结束 → 取消」，对话输入框
// 禁用，再发留言直接 ErrTaskClosed —— 一次可以重试的偶发失败被当成了封板。
//
// 现在的语义：失败的一轮和其它跑完的一轮一样停在「待审核」，用户读完失败原因
// 可以直接继续（新的一轮），也可以显式「完成」或「取消」。
func TestFailedRoundKeepsTaskInReviewAndAllowsNextMessage(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)
	f.wireRunLifecycle(t)
	oldRun, oldReply := f.seedRunState(t, NodeRunFail, NodeRunFail, StatusRunning)

	projector := NewService(testRoots{root: f.root})
	projector.SetActivityBroadcaster(f.broadcast)
	if err := projector.projectWorkflowTaskStatus(ctx, f.service.tasks, oldRun); err != nil {
		t.Fatalf("projectWorkflowTaskStatus: %v", err)
	}

	settled, err := f.tasks.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if settled.Status != StatusWaitingUser {
		t.Fatalf("failed round status = %q, want waiting_user (a single failed turn is work to review, not a closed task)", settled.Status)
	}
	if settled.CompletedAt != "" {
		t.Fatalf("failed round stamped completed_at = %q, want empty", settled.CompletedAt)
	}
	// 失败结论仍然可见：回复是 failed 且带原因，不能被吞成一句「已完成」。
	stored, err := f.service.activities.Get(ctx, oldReply.ID)
	if err != nil {
		t.Fatalf("reload reply: %v", err)
	}
	if stored.Status != ActivityStatusFailed {
		t.Fatalf("failed round reply status = %q, want failed", stored.Status)
	}

	// 用户继续发消息 = 新的一轮。旧 Run 的证据原样保留，不冻结成 cancelled，
	// 也不把用户看到的失败回复改写成「已取消」。
	_, agentAct, err := f.service.CreateUserActivity(ctx, "task-1", "额度补上了，再试一次", CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	if agentAct.Status == ActivityStatusFailed {
		t.Fatalf("continuation failed: %s", agentAct.Summary)
	}
	if agentAct.RunID == "" || agentAct.RunID == oldRun.ID {
		t.Fatalf("new turn run = %q, want a fresh run (old %q)", agentAct.RunID, oldRun.ID)
	}

	frozen, err := f.tasks.WorkflowRunByID(ctx, oldRun.ID)
	if err != nil {
		t.Fatalf("reload old run: %v", err)
	}
	if got := ProjectStatusFromRun(frozen); got != StatusFail {
		t.Fatalf("old run projects as %q, want fail (a failed turn is not superseded)", got)
	}
	events, err := f.tasks.ListWorkflowRunEvents(ctx, oldRun.ID)
	if err != nil {
		t.Fatalf("ListWorkflowRunEvents: %v", err)
	}
	for _, event := range events {
		if event.Type == WorkflowEventRunSuperseded {
			t.Fatalf("failed run got a run_superseded event: %#v", events)
		}
	}
	stored, err = f.service.activities.Get(ctx, oldReply.ID)
	if err != nil {
		t.Fatalf("reload old reply: %v", err)
	}
	if stored.Status != ActivityStatusFailed {
		t.Fatalf("old reply status = %q, want failed left untouched", stored.Status)
	}

	task, err := f.tasks.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if isTerminalStatus(task.Status) {
		t.Fatalf("task status = %q, want drivable after a new message", task.Status)
	}
}

// 完成 / 失败 / 取消的任务都能一键回到「待审核」，继续这条对话。
// 只动任务行（状态 + completed_at）；上一轮的 Run、回复与结果摘要都是证据。
func TestReopenTerminalTaskReturnsToReview(t *testing.T) {
	ctx := context.Background()
	runner := &fakeRunner{}
	svc, root := newTestService(t, runner)
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}

	for _, status := range []string{StatusSuccess, StatusFail, StatusCancelled} {
		taskID := "task-reopen-" + status
		now := time.Now().UTC()
		if _, err := store.CreateTask(ctx, Task{
			ID: taskID, RootID: root.ID, Status: status, ResultSummary: "上一轮的结论",
			CompletedAt: now.Format(time.RFC3339Nano), CreatedAt: now, UpdatedAt: now,
		}, TaskEvent{ID: "event-" + taskID, TaskID: taskID, Type: "created", CreatedAt: now}); err != nil {
			t.Fatalf("CreateTask(%s): %v", status, err)
		}
		before := runner.updateCount()

		detail, err := svc.Reopen(ctx, MoveInput{RootID: root.ID, TaskID: taskID})
		if err != nil {
			t.Fatalf("Reopen(%s): %v", status, err)
		}
		if detail.Task.Status != StatusWaitingUser {
			t.Fatalf("%s reopened to %q, want waiting_user", status, detail.Task.Status)
		}
		if detail.Task.CompletedAt != "" {
			t.Fatalf("%s reopened kept completed_at %q, want cleared", status, detail.Task.CompletedAt)
		}
		if detail.Task.ResultSummary != "上一轮的结论" {
			t.Fatalf("%s reopened lost the previous result summary: %q", status, detail.Task.ResultSummary)
		}
		if runner.updateCount() == before {
			t.Fatalf("%s reopen did not broadcast the task update", status)
		}
		events, err := store.ListEvents(ctx, taskID)
		if err != nil {
			t.Fatalf("ListEvents(%s): %v", status, err)
		}
		last := events[len(events)-1]
		if last.Type != "reopened" {
			t.Fatalf("%s reopen event = %q, want reopened", status, last.Type)
		}
		if !strings.Contains(last.Payload, `"from":"`+status+`"`) {
			t.Fatalf("%s reopen payload = %s, want from=%s", status, last.Payload, status)
		}
	}
}

// 重开必须幂等：已经可继续的任务再点一次不该写第二条事件，也不该改写状态。
func TestReopenIsIdempotentOutsideTerminalStates(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	now := time.Now().UTC()
	if _, err := store.CreateTask(ctx, Task{
		ID: "task-live", RootID: root.ID, Status: StatusWaitingUser, CreatedAt: now, UpdatedAt: now,
	}, TaskEvent{ID: "event-live", TaskID: "task-live", Type: "created", CreatedAt: now}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	detail, err := svc.Reopen(ctx, MoveInput{RootID: root.ID, TaskID: "task-live"})
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	if detail.Task.Status != StatusWaitingUser {
		t.Fatalf("status = %q, want waiting_user untouched", detail.Task.Status)
	}
	events, err := store.ListEvents(ctx, "task-live")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	for _, event := range events {
		if event.Type == "reopened" {
			t.Fatalf("non-terminal task got a reopen event: %#v", events)
		}
	}
}

// 取消的任务重开之后要真的能继续：绑定着的取消 Run 是上一轮的证据，
// 新留言是新的一轮，不能被当成「取代」再冻结一次，也不能被终态闸门挡掉。
func TestReopenedCancelledTaskAcceptsNewMessage(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)
	f.wireRunLifecycle(t)
	oldRun, oldReply := f.seedRunState(t, NodeRunCancelled, NodeRunCancelled, StatusCancelled)

	svc := NewService(testRoots{root: f.root})
	if _, err := svc.Reopen(ctx, MoveInput{RootID: f.root.ID, TaskID: "task-1"}); err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	reopened, err := f.tasks.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if reopened.Status != StatusWaitingUser {
		t.Fatalf("reopened status = %q, want waiting_user", reopened.Status)
	}

	_, agentAct, err := f.service.CreateUserActivity(ctx, "task-1", "算了，还是把这一步做完", CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		t.Fatalf("CreateUserActivity after reopen: %v", err)
	}
	if agentAct.Status == ActivityStatusFailed {
		t.Fatalf("continuation after reopen failed: %s", agentAct.Summary)
	}
	if agentAct.RunID == "" || agentAct.RunID == oldRun.ID {
		t.Fatalf("new turn run = %q, want a fresh run (old %q)", agentAct.RunID, oldRun.ID)
	}
	frozen, err := f.tasks.WorkflowRunByID(ctx, oldRun.ID)
	if err != nil {
		t.Fatalf("reload old run: %v", err)
	}
	if got := ProjectStatusFromRun(frozen); got != StatusCancelled {
		t.Fatalf("old run projects as %q, want cancelled evidence kept", got)
	}
	events, err := f.tasks.ListWorkflowRunEvents(ctx, oldRun.ID)
	if err != nil {
		t.Fatalf("ListWorkflowRunEvents: %v", err)
	}
	for _, event := range events {
		if event.Type == WorkflowEventRunSuperseded {
			t.Fatalf("already-cancelled run got a second supersede event: %#v", events)
		}
	}
	stored, err := f.service.activities.Get(ctx, oldReply.ID)
	if err != nil {
		t.Fatalf("reload old reply: %v", err)
	}
	if stored.Status == ActivityStatusCompleted {
		t.Fatalf("old cancelled reply was rewritten to completed")
	}
}

// brief 是 Router 的唯一世界快照：任务行被操作员放回「待审核」之后，上一轮
// 终态 Run 不能继续把任务报成已结束 —— 否则 apply 闸门会拿旧结论拒掉新留言。
func TestBriefTaskStatusHonoursReopenedTaskRow(t *testing.T) {
	run := &WorkflowRun{
		ID:         "run-reopened",
		TaskID:     "task-1",
		Graph:      WorkflowDefinition{Nodes: []WorkflowNode{{ID: "execute", Kind: NodeKindAgent}}},
		NodeStates: map[string]string{"execute": NodeRunCancelled},
	}
	task := Task{ID: "task-1", Status: StatusWaitingUser}
	if got := briefTaskStatus(run, task); got != StatusWaitingUser {
		t.Fatalf("reopened task derived %q, want waiting_user (operator state wins over the old terminal run)", got)
	}
	// 任务行自己还是终态时，旧结论照旧是真相。
	task.Status = StatusCancelled
	if got := briefTaskStatus(run, task); got != StatusCancelled {
		t.Fatalf("terminal task derived %q, want cancelled", got)
	}
}
