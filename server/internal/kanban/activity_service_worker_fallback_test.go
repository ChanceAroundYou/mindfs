package kanban

import (
	"context"
	"testing"
	"time"

	"mindfs/server/internal/fs"
)

// 运行节点（worker）没有工作流库：`/api/workflows` 属于控制面（worker 上 403），
// deploy 只推二进制与 agents.json，worker 的 <config-dir>/workflows.json 永远不
// 存在。此时若仍按「empty → 推荐/默认 workflow」走，每条留言都会在 13ms 内落
// failed（"no auto-select eligible workflows"），节点等于完全不能执行。
//
// 没有任何已发布 workflow 时把这一轮交给直连回合，是「节点没有工作流能力」的
// 降级，不是用户输入错误：用户留言应该得到回答，而不是一条英文内部错误。
func TestActivityService_NoWorkflowsFallsBackToDirectTurn(t *testing.T) {
	ctx := context.Background()
	f := setupWorkflowlessActivityService(t)
	direct := &fakeDirectExecutor{reply: "直接回复", done: make(chan struct{})}
	f.service.SetDirectExecutor(direct)

	_, agentAct, err := f.service.CreateUserActivity(ctx, "task-1", "你好", CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	select {
	case <-direct.done:
	case <-time.After(2 * time.Second):
		t.Fatal("direct executor was never invoked")
	}
	if direct.got != "你好" {
		t.Fatalf("executor body = %q, want the user message", direct.got)
	}
	stored := waitFinishedActivity(t, f.service, agentAct.ID, ActivityStatusCompleted)
	if stored.Body != "直接回复" {
		t.Fatalf("body = %q, want the direct reply", stored.Body)
	}
	if stored.RunID != "" {
		t.Fatalf("fallback must not create a workflow run, got run_id=%q", stored.RunID)
	}
	if run, runErr := f.tasks.WorkflowRunForTask(ctx, "task-1"); runErr == nil && run != nil {
		t.Fatalf("no workflow run expected, got %#v", run)
	}
}

// 显式指定 workflow 的留言在无工作流库的节点上同样无法执行（版本不存在）。
// 节点没有任何 workflow 时也按同一降级处理——否则界面上仍选着上一次的 workflow
// 时，节点会继续 100% 失败。
func TestActivityService_NoWorkflowsFallsBackForSpecifiedChoice(t *testing.T) {
	ctx := context.Background()
	f := setupWorkflowlessActivityService(t)
	direct := &fakeDirectExecutor{reply: "降级回复", done: make(chan struct{})}
	f.service.SetDirectExecutor(direct)
	version := "workflow-from-control-plane@r1"

	_, agentAct, err := f.service.CreateUserActivity(ctx, "task-1", "继续", CommentWorkflowChoiceSpecified, &version)
	if err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	select {
	case <-direct.done:
	case <-time.After(2 * time.Second):
		t.Fatal("direct executor was never invoked")
	}
	stored := waitFinishedActivity(t, f.service, agentAct.ID, ActivityStatusCompleted)
	if stored.Body != "降级回复" || stored.RunID != "" {
		t.Fatalf("activity = %#v, want direct reply without a run", stored)
	}
}

// 没有直连执行端口时必须保留原来的失败语义：不能把「没人能执行」伪装成成功，
// 也不能留下永久 pending。
func TestActivityService_NoWorkflowsWithoutDirectExecutorFails(t *testing.T) {
	ctx := context.Background()
	f := setupWorkflowlessActivityService(t)

	_, agentAct, err := f.service.CreateUserActivity(ctx, "task-1", "你好", CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	stored := waitFinishedActivity(t, f.service, agentAct.ID, ActivityStatusFailed)
	if stored.Summary != ErrNoAvailableWorkflows.Error() {
		t.Fatalf("summary = %q, want %q", stored.Summary, ErrNoAvailableWorkflows.Error())
	}
}

type workflowlessFixture struct {
	service *ActivityService
	tasks   *TaskStore
}

func setupWorkflowlessActivityService(t *testing.T) *workflowlessFixture {
	t.Helper()
	ctx := context.Background()
	root := fs.NewRootInfo("root", "root", t.TempDir())
	tasks, err := NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	t.Cleanup(func() { _ = tasks.Close() })
	if _, err := tasks.CreateTask(ctx,
		Task{ID: "task-1", RootID: root.ID, Status: StatusPending},
		TaskEvent{ID: "event-1", TaskID: "task-1", Type: "created"},
	); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	workflows := newWorkflowStore(t.TempDir()) // 空库，等价于 worker 上没有 workflows.json
	svc := NewActivityService(tasks, workflows, NewWorkflowRecommender(workflows, nil))
	return &workflowlessFixture{service: svc, tasks: tasks}
}

func waitFinishedActivity(t *testing.T, svc *ActivityService, activityID, want string) *TaskActivity {
	t.Helper()
	ctx := context.Background()
	var stored *TaskActivity
	var err error
	for i := 0; i < 100; i++ {
		stored, err = svc.activities.Get(ctx, activityID)
		if err == nil && stored.Status == want {
			return stored
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("Get(%s): %v", activityID, err)
	}
	t.Fatalf("activity status = %q, want %q", stored.Status, want)
	return nil
}
