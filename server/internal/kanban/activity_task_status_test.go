package kanban

import (
	"context"
	"testing"
	"time"
)

// 对话回合一开始，任务头就必须离开「待开始」。直连回合没有 WorkflowRun
// 可以投影状态，过去只改活动状态、任务一直停在 pending，界面左上角因此显示
// 「待开始」；回合结束后停在「待审核」—— agent 回了话，等人看。
type blockingDirectExecutor struct {
	started chan struct{}
	release chan struct{}
}

func (f *blockingDirectExecutor) RunDirectActivity(ctx context.Context, _ Task, _ string) (string, error) {
	close(f.started)
	select {
	case <-f.release:
		return "完成", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func waitTaskStatus(t *testing.T, store *TaskStore, taskID, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		task, err := store.GetTask(context.Background(), taskID)
		if err == nil && task.Status == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	task, err := store.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	t.Fatalf("task status = %q, want %q", task.Status, want)
}

func waitActivityStatus(t *testing.T, store *ActivityStore, id, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		activity, err := store.Get(context.Background(), id)
		if err == nil && activity.Status == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	activity, err := store.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("GetActivity: %v", err)
	}
	t.Fatalf("activity status = %q, want %q", activity.Status, want)
}

func TestActivityService_DirectTurnMarksTaskRunningAndRestoresIdle(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)
	exec := &blockingDirectExecutor{started: make(chan struct{}), release: make(chan struct{})}
	f.service.SetDirectExecutor(exec)

	_, agentAct, err := f.service.CreateUserActivity(ctx, "task-1", "聊一句", CommentWorkflowChoiceDisabled, nil)
	if err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	select {
	case <-exec.started:
	case <-time.After(2 * time.Second):
		t.Fatal("direct executor was never invoked")
	}
	waitTaskStatus(t, f.tasks, "task-1", StatusRunning)

	close(exec.release)
	// 先等终态活动落库（恢复任务状态发生在终态广播之前），再断言任务回空闲；
	// 否则测试会在后台写库完成前结束，TempDir 清理与写入互相打架。
	//
	// 回话之后的「空闲」是**待审核**而不是「未开始」：agent 已经回了话，等用户看
	// （用户实测 #87：回复完成后卡片仍写着未开始）。
	waitActivityStatus(t, f.service.activities, agentAct.ID, ActivityStatusCompleted)
	waitTaskStatus(t, f.tasks, "task-1", StatusWaitingUser)
}

// workflow 回合在 Router/首个节点启动之前就有一个 running 的占位回复；此时
// 任务也必须已经是 running，不能等第一个节点真正跑起来才投影。
type blockingWorkflowExecutor struct {
	started chan struct{}
	release chan struct{}
}

func (f *blockingWorkflowExecutor) ExecuteActivityWorkflow(_ context.Context, _ Task, _ *WorkflowRun) error {
	close(f.started)
	<-f.release
	return nil
}

func TestActivityService_WorkflowTurnMarksTaskRunningBeforeFirstNode(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)
	exec := &blockingWorkflowExecutor{started: make(chan struct{}), release: make(chan struct{})}
	f.service.SetWorkflowExecutor(exec)

	if _, _, err := f.service.CreateUserActivity(ctx, "task-1", "跑一下", CommentWorkflowChoiceEmpty, nil); err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	select {
	case <-exec.started:
	case <-time.After(2 * time.Second):
		t.Fatal("workflow executor was never invoked")
	}
	waitTaskStatus(t, f.tasks, "task-1", StatusRunning)
	close(exec.release)
}

// 投影在「还没有节点开始」时会得到 pending；运行中的任务不能被它降级回
// 「待开始」，否则 Router 决策期间任务头会闪回 pending。
func TestService_ProjectWorkflowTaskStatusKeepsRunningTaskRunning(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)
	_, agentAct, err := f.service.CreateUserActivity(ctx, "task-1", "跑一下", CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	run, err := f.tasks.WorkflowRunByID(ctx, agentAct.RunID)
	if err != nil {
		t.Fatalf("WorkflowRunByID: %v", err)
	}
	if err := f.tasks.UpdateTaskStatus(ctx, "task-1", StatusRunning, false); err != nil {
		t.Fatalf("UpdateTaskStatus: %v", err)
	}

	svc := NewService(testRoots{root: f.root})
	if err := svc.projectWorkflowTaskStatus(ctx, f.service.tasks, run); err != nil {
		t.Fatalf("projectWorkflowTaskStatus: %v", err)
	}
	waitTaskStatus(t, f.tasks, "task-1", StatusRunning)
}
