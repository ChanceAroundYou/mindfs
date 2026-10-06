package kanban

import (
	"context"
	"errors"
	"os"
	"testing"
)

// 收尾按钮的分流判据（2026-10-05 重做）：会话在不在回复、分支合没合进主干、
// 流水上有没有收尾段。这三条判据各自独立，所以各自一个用例 —— 混在一起写，
// 一条判据坏了另外两条照样绿。

// 会话正在回复时必须拦下，而且**报的是 ErrTaskSessionRunning**，不是泛泛一句
// 「正在执行中」：api 层要按它区分「agent 在动，什么都不做只回一句」（HTTP 200）
// 和「其它拒绝」（409）。两者混成一个错误码，前端就只能把正常等待渲染成红色失败。
func TestAssertNotRunningConsultsTheSessionProbeFirst(t *testing.T) {
	ctx := context.Background()
	svc, root, _, _ := finishFixture(t, "from worktree\n")
	taskID := mustTaskID(t, svc, root.ID)
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	task, err := store.GetTask(ctx, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	// 探针说在跑，任务状态却是 waiting_user —— 这正是「任务卡在待审核、agent 其实
	// 还在动」的形状。判据只看状态就会把保护整个架空。
	task.Status = StatusWaitingUser
	task.MainSessionKey = "live-session"
	if err := store.UpdateTask(ctx, task); err != nil {
		t.Fatalf("mark waiting_user: %v", err)
	}
	svc.SetSessionRunningProbe(func(key string) bool { return key == "live-session" })

	err = svc.assertNotRunning(ctx, store, task)
	if !errors.Is(err, ErrTaskSessionRunning) {
		t.Fatalf("err = %v, want ErrTaskSessionRunning", err)
	}
}

// 探针说没在回复就是没在跑 —— 不再叠 task.Status 那套旧判据。
//
// 为什么这条要紧：任务可以长时间停在 running 却早就没 agent 了（agent 进程死了、
// 状态没人清）。旧判据下那种任务**永远收不了尾**，而收尾恰恰是唯一能把 worktree
// 拆掉、让界面不再谎称「有 worktree」的动作。
func TestAssertNotRunningTrustsTheProbeOverAStaleRunningStatus(t *testing.T) {
	ctx := context.Background()
	svc, root, _, _ := finishFixture(t, "from worktree\n")
	taskID := mustTaskID(t, svc, root.ID)
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	task, err := store.GetTask(ctx, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	task.Status = StatusRunning
	if err := store.UpdateTask(ctx, task); err != nil {
		t.Fatalf("mark running: %v", err)
	}
	svc.SetSessionRunningProbe(func(string) bool { return false })

	if err := svc.assertNotRunning(ctx, store, task); err != nil {
		t.Fatalf("a stale running status must not block finishing once the session is idle: %v", err)
	}
}

// 探针没装配时（单测、老装配）回落到状态判据 —— 行为与改造前逐字一致。
// 少了这条回落，任何没挂探针的调用方都会把「探针恒 false」读成「agent 一定没在跑」。
func TestAssertNotRunningFallsBackToStatusWhenNoProbeIsConfigured(t *testing.T) {
	ctx := context.Background()
	svc, root, _, _ := finishFixture(t, "from worktree\n")
	taskID := mustTaskID(t, svc, root.ID)
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	task, err := store.GetTask(ctx, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	task.Status = StatusRunning
	if err := store.UpdateTask(ctx, task); err != nil {
		t.Fatalf("mark running: %v", err)
	}

	err = svc.assertNotRunning(ctx, store, task)
	if err == nil {
		t.Fatal("without the probe the old status guard must still refuse a running task")
	}
	if errors.Is(err, ErrTaskSessionRunning) {
		t.Fatalf("the fallback must keep its own wording, not borrow the probe's: %v", err)
	}
}

// TaskSessionRunning 是 api 层做分流的入口：探针没装配时恒 false（调用方据此回落），
// 装配了就读真值。
func TestTaskSessionRunningFollowsTheProbe(t *testing.T) {
	ctx := context.Background()
	svc, root, _, _ := finishFixture(t, "from worktree\n")
	taskID := mustTaskID(t, svc, root.ID)

	if svc.TaskSessionRunning(ctx, root.ID, taskID) {
		t.Fatal("no probe configured: must report false, not guess")
	}
	svc.SetSessionRunningProbe(func(key string) bool { return key == "live" })
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	task, err := store.GetTask(ctx, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	task.MainSessionKey = "live"
	if err := store.UpdateTask(ctx, task); err != nil {
		t.Fatalf("bind session: %v", err)
	}
	if !svc.TaskSessionRunning(ctx, root.ID, taskID) {
		t.Fatal("the bound session is replying: must report true")
	}
	task.MainSessionKey = "idle"
	if err := store.UpdateTask(ctx, task); err != nil {
		t.Fatalf("rebind session: %v", err)
	}
	if svc.TaskSessionRunning(ctx, root.ID, taskID) {
		t.Fatal("the bound session is idle: must report false")
	}
}

// 「分支已合进主干」= 活已经在主干里，收尾只剩机械清场，不必再让 agent 跑一遍。
func TestTaskWorktreeBranchMerged(t *testing.T) {
	ctx := context.Background()
	svc, root, mainDir, _ := finishFixture(t, "from worktree\n")
	taskID := mustTaskID(t, svc, root.ID)

	merged, err := svc.TaskWorktreeBranchMerged(ctx, root.ID, taskID, "")
	if err != nil {
		t.Fatalf("TaskWorktreeBranchMerged: %v", err)
	}
	if merged {
		t.Fatal("the worktree branch has an unmerged commit: must report false")
	}
	// 真合一次 —— 判据必须跟着真 git 走，不能靠字段猜。
	gitForTest(t, mainDir, "merge", "-q", "--no-ff", "task-1")
	merged, err = svc.TaskWorktreeBranchMerged(ctx, root.ID, taskID, "")
	if err != nil {
		t.Fatalf("TaskWorktreeBranchMerged after merge: %v", err)
	}
	if !merged {
		t.Fatal("the branch is now an ancestor of main: must report true")
	}
}

// 没开过 worktree 的任务返回 false（不是 error）：那种任务该走正常收尾流程，
// 让调用方去追加收尾段，而不是被一个「分支不存在」的错挡住。
func TestTaskWorktreeBranchMergedWithoutAWorktree(t *testing.T) {
	ctx := context.Background()
	svc, root, _, _ := finishFixture(t, "")
	taskID := mustTaskID(t, svc, root.ID)
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	task, err := store.GetTask(ctx, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	task.CreateWorktree = false
	if err := store.UpdateTask(ctx, task); err != nil {
		t.Fatalf("clear create_worktree: %v", err)
	}
	merged, err := svc.TaskWorktreeBranchMerged(ctx, root.ID, taskID, "")
	if err != nil {
		t.Fatalf("a task without a worktree must not error: %v", err)
	}
	if merged {
		t.Fatal("a task without a worktree must report false")
	}
}

// TaskFinishStageIndex 决定「重跑那段继续收尾」还是「追加一段新收尾段」。
func TestTaskFinishStageIndex(t *testing.T) {
	ctx := context.Background()
	svc, root, _, _ := finishFixture(t, "")
	taskID := mustTaskID(t, svc, root.ID)
	parkAtWaitingUser(t, svc, root.ID, taskID)

	if _, ok, err := svc.TaskFinishStageIndex(ctx, root.ID, taskID); err != nil || ok {
		t.Fatalf("no finish stage yet: ok=%v err=%v", ok, err)
	}
	if _, err := svc.BeginFinishWorktree(ctx, BeginFinishInput{RootID: root.ID, TaskID: taskID}); err != nil {
		t.Fatalf("BeginFinishWorktree: %v", err)
	}
	index, ok, err := svc.TaskFinishStageIndex(ctx, root.ID, taskID)
	if err != nil {
		t.Fatalf("TaskFinishStageIndex: %v", err)
	}
	if !ok {
		t.Fatal("the finish stage was appended: must report ok")
	}
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	task, err := store.GetTask(ctx, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if index != len(task.Stages)-1 {
		t.Fatalf("index = %d, want %d (the appended tail)", index, len(task.Stages)-1)
	}
	if !IsFinishStage(task.Stages[index]) {
		t.Fatalf("stage %d is not the finish stage", index)
	}
}

// 目录被删（手工 rm / wt-finish.sh cleanup / DELETE /api/git/worktrees）之后，
// 「目录不在」必须仍然成立 —— 那是徽标读「已收尾」的唯一依据，也是「收尾一直转」
// 的根源：判据要是跟着字段走而不是跟着磁盘走，收完尾的任务会永远显示「有 worktree」。
func TestWorktreeGoneIsDerivedFromTheDiskNotTheFields(t *testing.T) {
	ctx := context.Background()
	svc, root, _, worktreePath := finishFixture(t, "from worktree\n")
	taskID := mustTaskID(t, svc, root.ID)
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	task, err := store.GetTask(ctx, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.WorktreeMissingNow() {
		t.Fatal("the directory is on disk: must not read as missing")
	}
	if err := os.RemoveAll(worktreePath); err != nil {
		t.Fatalf("tear down worktree: %v", err)
	}
	if !task.WorktreeMissingNow() {
		t.Fatal("the directory is gone: must read as missing")
	}
}
