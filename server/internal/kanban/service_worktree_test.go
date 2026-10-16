package kanban

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestTaskCreateWorktreeIsTaskScoped(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID:             root.ID,
		Stages:             []StageTemplate{userStage("Describe")},
		Input:              "one",
		CreateWorktree:     true,
		WorktreeBranchMode: "existing",
		WorktreeBranch:     "feature/a",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if !detail.Task.CreateWorktree {
		t.Fatalf("task CreateWorktree=false, want true")
	}
	if detail.Task.WorktreeBranchMode != "existing" || detail.Task.WorktreeBranch != "feature/a" {
		t.Fatalf("task branch = %q/%q, want existing/feature/a", detail.Task.WorktreeBranchMode, detail.Task.WorktreeBranch)
	}
	disabled := false
	updated, err := svc.UpdateFirstInput(ctx, UpdateTaskInput{RootID: root.ID, TaskID: detail.Task.ID, Input: "two", CreateWorktree: &disabled})
	if err != nil {
		t.Fatalf("UpdateFirstInput: %v", err)
	}
	if updated.Task.CreateWorktree {
		t.Fatalf("updated task CreateWorktree=true, want false")
	}
}

func TestTaskWorktreeNameUsesTaskNumber(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{
			userStage("Describe"),
			agentStage("Fix", "Fix this:\n{previous_input}"),
		},
		Input:              "broken save button",
		CreateWorktree:     true,
		WorktreeBranchMode: "existing",
		WorktreeBranch:     "feature/a",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if detail.Task.TaskNumber != 1 {
		t.Fatalf("task number=%d, want 1", detail.Task.TaskNumber)
	}
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	waitForCondition(t, func() bool {
		runner := svc.Runner.(*fakeRunner)
		runner.mu.Lock()
		defer runner.mu.Unlock()
		return runner.worktreeCreateCalled && len(runner.execs) > 0
	})
	runner := svc.Runner.(*fakeRunner)
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if !runner.worktreeCreateCalled {
		t.Fatalf("worktree was not created")
	}
	if runner.worktreeName != "task-1" {
		t.Fatalf("worktree name=%q, want task-1", runner.worktreeName)
	}
	if runner.worktreeBranchMode != "existing" || runner.worktreeBranch != "feature/a" {
		t.Fatalf("worktree branch=%q/%q, want existing/feature/a", runner.worktreeBranchMode, runner.worktreeBranch)
	}
	if len(runner.execs) != 1 {
		t.Fatalf("runner exec count=%d, want 1", len(runner.execs))
	}
	// agent 的 cwd 必须就是建出来的那个 worktree 目录。比对 fake 记下的真实路径，
	// 不写死字面量：目录名带序号（见 CreateTaskWorktree），写死就成了测常量。
	if len(runner.createdDirs) != 1 {
		t.Fatalf("created dirs=%v, want exactly 1", runner.createdDirs)
	}
	if runner.execs[0].RuntimeRootPath != runner.createdDirs[0] {
		t.Fatalf("runtime root path=%q, want task worktree path %q",
			runner.execs[0].RuntimeRootPath, runner.createdDirs[0])
	}
}

func TestTaskWorktreeCreateErrorStoredOnTask(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{worktreeErr: errors.New("git worktree add failed")})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID:         root.ID,
		Stages:         []StageTemplate{userStage("Describe"), agentStage("Fix", "Fix this:\n{previous_input}")},
		Input:          "broken save button",
		CreateWorktree: true,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if detail.Task.Status != StatusPending {
		t.Fatalf("task status=%s, want pending（新建任务未开始）", detail.Task.Status)
	}
	// 「开始」才真正进执行体；worktree 创建失败在那里被记下。
	if _, err := svc.RunNow(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	waitForCondition(t, func() bool {
		detail, err = svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && detail.Task.AuxFlags.SessionError != ""
	})
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if detail.Task.CurrentStageIndex != 0 {
		t.Fatalf("current stage=%d, want 0", detail.Task.CurrentStageIndex)
	}
	if detail.Task.AuxFlags.SessionError != "git worktree add failed" {
		t.Fatalf("session error=%q, want git worktree add failed", detail.Task.AuxFlags.SessionError)
	}
}

// 建树 → 把目录删掉 → 再点执行：必须立刻报「worktree 目录已不存在」，
// 而不是拿失效 cwd 去跑 agent。
func TestRunNowReportsMissingWorktreeInsteadOfUsingStalePath(t *testing.T) {
	ctx := context.Background()
	runner := &fakeRunner{}
	svc, root := newTestService(t, runner)
	// 三段：user → agent → user。留最后一段是必需的 —— 只有两段时 agent 段跑完就停在
	// 末段，RunNow 里的 Next 会在 moveTo 之前先撞上 "target stage out of range"，
	// 压根走不到 worktree 校验，测的就不是「立即执行」这条路了。
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID:         root.ID,
		Stages:         []StageTemplate{userStage("Describe"), agentStage("Fix", "Fix this:\n{previous_input}"), userStage("Review")},
		Input:          "broken save button",
		CreateWorktree: true,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	// 先建出 worktree（走到 agent 段才会建）。
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	waitForCondition(t, func() bool {
		runner.mu.Lock()
		defer runner.mu.Unlock()
		return runner.worktreeCreateCalled
	})
	// 同上：先等在跑的那段落地，免得删目录和它的收尾写回抢同一行。
	waitForCondition(t, func() bool {
		d, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && d.Task.Status == StatusWaitingUser
	})
	before, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if !worktreeDirUsable(before.Task.WorktreePath) {
		t.Fatalf("worktree %q should exist right after creation", before.Task.WorktreePath)
	}
	runner.mu.Lock()
	execsBeforeDelete := len(runner.execs)
	runner.mu.Unlock()

	// 目录没了，字段还非空 —— 这正是要修的状态。
	if err := os.RemoveAll(before.Task.WorktreePath); err != nil {
		t.Fatalf("remove worktree: %v", err)
	}

	// 关键：点执行必须在**启动 agent 之前**就报出来。
	// RunNow 自己不能失败：用户点按钮要拿到的是卡片上的那条错误，不是一个 HTTP 500。
	if _, err := svc.RunNow(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	detail, err = svc.GetTask(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if !strings.Contains(detail.Task.AuxFlags.SessionError, "worktree 目录已不存在") {
		t.Fatalf("expected a missing-worktree error, got %q", detail.Task.AuxFlags.SessionError)
	}
	if !strings.Contains(detail.Task.AuxFlags.SessionError, before.Task.WorktreePath) {
		t.Fatalf("session error should name the missing path, got %q", detail.Task.AuxFlags.SessionError)
	}
	// agent 不能被启动：它一跑起来就会在失效 cwd 上失败。
	runner.mu.Lock()
	execsAfterDelete := len(runner.execs)
	runner.mu.Unlock()
	if execsAfterDelete != execsBeforeDelete {
		t.Fatalf("agent ran %d extra time(s) with a deleted worktree", execsAfterDelete-execsBeforeDelete)
	}
}

// 重建：目录没了之后点「重建 worktree」要真把树建回来，路径恢复可用。
func TestRebuildTaskWorktreeRecreatesDeletedDirectory(t *testing.T) {
	ctx := context.Background()
	runner := &fakeRunner{}
	svc, root := newTestService(t, runner)
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID:         root.ID,
		Stages:         []StageTemplate{userStage("Describe"), agentStage("Fix", "Fix this:\n{previous_input}")},
		Input:          "broken save button",
		CreateWorktree: true,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	waitForCondition(t, func() bool {
		runner.mu.Lock()
		defer runner.mu.Unlock()
		return runner.worktreeCreateCalled
	})
	before, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	// 必须等在跑的 agent 段落地，否则下面删目录会和它收尾时那次 UpdateTask 抢同一行：
	// executeTask 拿的是自己那份 in-memory task，整行写回会把重建好的新路径覆盖成旧路径。
	// 顺带把任务留在 waiting_user（首段 user 段批准后即终止），再点执行时
	// RunNow 走的就是真实生产路径。
	waitForCondition(t, func() bool {
		d, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && d.Task.Status == StatusWaitingUser
	})
	runner.mu.Lock()
	if n := len(runner.execs); n != 1 {
		runner.mu.Unlock()
		t.Fatalf("expected the agent stage to have run once, got %d", n)
	}
	runner.mu.Unlock()

	before, err = svc.GetTask(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	oldPath := before.Task.WorktreePath
	if err := os.RemoveAll(oldPath); err != nil {
		t.Fatalf("remove worktree: %v", err)
	}

	detail, err = svc.RebuildTaskWorktree(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID})
	if err != nil {
		t.Fatalf("RebuildTaskWorktree: %v", err)
	}
	if !worktreeDirUsable(detail.Task.WorktreePath) {
		t.Fatalf("rebuilt worktree %q is still not usable", detail.Task.WorktreePath)
	}
	if detail.Task.AuxFlags.SessionError != "" {
		t.Fatalf("rebuild should clear the stale error, got %q", detail.Task.AuxFlags.SessionError)
	}
	// 重建后的路径要落库，不能只在内存里对。
	reloaded, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask after rebuild: %v", err)
	}
	if reloaded.Task.WorktreePath != detail.Task.WorktreePath {
		t.Fatalf("rebuilt path not persisted: %q vs %q", reloaded.Task.WorktreePath, detail.Task.WorktreePath)
	}
}

// 幂等：目录还在时重建是 no-op，不重复建树。
func TestRebuildTaskWorktreeIsNoopWhenDirectoryExists(t *testing.T) {
	ctx := context.Background()
	runner := &fakeRunner{}
	svc, root := newTestService(t, runner)
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID:         root.ID,
		Stages:         []StageTemplate{userStage("Describe"), agentStage("Fix", "Fix this:\n{previous_input}")},
		Input:          "broken save button",
		CreateWorktree: true,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.Next(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("Next: %v", err)
	}
	waitForCondition(t, func() bool {
		runner.mu.Lock()
		defer runner.mu.Unlock()
		return runner.worktreeCreateCalled
	})
	runner.mu.Lock()
	before := len(runner.createdDirs)
	runner.mu.Unlock()

	if _, err := svc.RebuildTaskWorktree(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID}); err != nil {
		t.Fatalf("RebuildTaskWorktree: %v", err)
	}
	runner.mu.Lock()
	after := len(runner.createdDirs)
	runner.mu.Unlock()
	if after != before {
		t.Fatalf("rebuild created another worktree (%d -> %d) while one exists", before, after)
	}
}

// mustGetDetail 取一次任务详情并在出错时立刻失败，省掉每个用例重复的 err 检查。
// 与 worktree_finish_stage_test.go 的 mustGetTask（返回 Task）分开：这里要 stage_runs。
func mustGetDetail(t *testing.T, svc *Service, rootID, taskID string) TaskDetail {
	t.Helper()
	d, err := svc.GetTask(context.Background(), rootID, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	return d
}
