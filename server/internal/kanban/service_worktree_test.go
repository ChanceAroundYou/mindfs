package kanban

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestTaskCreateWorktreeOptionsAreTaskScoped(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID:             root.ID,
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
}

func TestTaskWorktreeNameUsesTaskNumber(t *testing.T) {
	ctx := context.Background()
	runner := &fakeRunner{}
	svc, root := newTestService(t, runner)
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID:             root.ID,
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
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	task, err := svc.ensureTaskWorktree(ctx, store, detail.Task)
	if err != nil {
		t.Fatalf("ensureTaskWorktree: %v", err)
	}
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
	// 任务钉在真实建出来的目录上（目录名带序号，别写死字面量）。
	if len(runner.createdDirs) != 1 {
		t.Fatalf("created dirs=%v, want exactly 1", runner.createdDirs)
	}
	if task.WorktreePath != runner.createdDirs[0] {
		t.Fatalf("task worktree path=%q, want %q", task.WorktreePath, runner.createdDirs[0])
	}
}

// 建树失败的原因必须落在任务上：卡片本来就会渲染 session_error，
// 用户不必去翻日志（典型是 branch already exists）。
func TestWorktreeCreateErrorStoredOnTask(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{worktreeErr: errors.New("git worktree add failed")})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID:         root.ID,
		Input:          "broken save button",
		CreateWorktree: true,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	rebuilt, err := svc.RebuildTaskWorktree(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID})
	if err != nil {
		t.Fatalf("RebuildTaskWorktree: %v", err)
	}
	if rebuilt.Task.AuxFlags.SessionError != "git worktree add failed" {
		t.Fatalf("session error=%q, want git worktree add failed", rebuilt.Task.AuxFlags.SessionError)
	}
	if strings.TrimSpace(rebuilt.Task.WorktreePath) != "" {
		t.Fatalf("failed creation must not persist a path, got %q", rebuilt.Task.WorktreePath)
	}
}

// 重建：目录没了之后点「重建 worktree」要真把树建回来，路径恢复可用并落库。
func TestRebuildTaskWorktreeRecreatesDeletedDirectory(t *testing.T) {
	ctx := context.Background()
	runner := &fakeRunner{}
	svc, root := newTestService(t, runner)
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID:         root.ID,
		Input:          "broken save button",
		CreateWorktree: true,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	created, err := svc.ensureTaskWorktree(ctx, store, detail.Task)
	if err != nil {
		t.Fatalf("ensureTaskWorktree: %v", err)
	}
	oldPath := created.WorktreePath
	if !worktreeDirUsable(oldPath) {
		t.Fatalf("worktree %q should exist right after creation", oldPath)
	}
	if err := os.RemoveAll(oldPath); err != nil {
		t.Fatalf("remove worktree: %v", err)
	}

	rebuilt, err := svc.RebuildTaskWorktree(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID})
	if err != nil {
		t.Fatalf("RebuildTaskWorktree: %v", err)
	}
	if !worktreeDirUsable(rebuilt.Task.WorktreePath) {
		t.Fatalf("rebuilt worktree %q is still not usable", rebuilt.Task.WorktreePath)
	}
	if rebuilt.Task.AuxFlags.SessionError != "" {
		t.Fatalf("rebuild should clear the stale error, got %q", rebuilt.Task.AuxFlags.SessionError)
	}
	reloaded, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask after rebuild: %v", err)
	}
	if reloaded.Task.WorktreePath != rebuilt.Task.WorktreePath {
		t.Fatalf("rebuilt path not persisted: %q vs %q", reloaded.Task.WorktreePath, rebuilt.Task.WorktreePath)
	}
}

// 幂等：目录还在时重建是 no-op，不重复建树。
func TestRebuildTaskWorktreeIsNoopWhenDirectoryExists(t *testing.T) {
	ctx := context.Background()
	runner := &fakeRunner{}
	svc, root := newTestService(t, runner)
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID:         root.ID,
		Input:          "broken save button",
		CreateWorktree: true,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	if _, err := svc.ensureTaskWorktree(ctx, store, detail.Task); err != nil {
		t.Fatalf("ensureTaskWorktree: %v", err)
	}
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