package kanban

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mindfs/server/internal/fs"
)

// sharedFixture 造「两个任务共用同一棵 worktree」的局面：A 是原本持有它的任务，
// B 是后建、以「已有分支」认领同一个目录的任务。返回的 service 用的是**真 git**，
// 所以断言能落在「目录还在不在 / 分支还在不在」这种真实后果上。
func sharedFixture(t *testing.T, edit string) (svc *Service, root fs.RootInfo, mainDir, worktreePath string, a, b Task) {
	t.Helper()
	mainDir = newGitRoot(t)
	worktreePath = filepath.Join(mainDir, ".worktree", "task-1")
	gitForTest(t, mainDir, "worktree", "add", "-q", "-b", "task-1", worktreePath)
	if edit != "" {
		if err := os.WriteFile(filepath.Join(worktreePath, "note.txt"), []byte(edit), 0o644); err != nil {
			t.Fatalf("edit in worktree: %v", err)
		}
		gitForTest(t, worktreePath, "add", "note.txt")
		gitForTest(t, worktreePath, "commit", "-qm", "work")
	}

	root = fs.NewRootInfo("root", "root", mainDir)
	svc = NewService(NewTemplateStoreAt(t.TempDir()), testRoots{root: root})
	svc.SetRunner(&fakeRunner{})
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	mk := func(input string) Task {
		detail, err := svc.CreateTask(context.Background(), CreateTaskInput{
			RootID:             root.ID,
			Stages:             []StageTemplate{userStage("Describe"), agentStage("Fix", "Fix:\n{previous_input}")},
			Input:              input,
			CreateWorktree:     true,
			WorktreeBranchMode: "existing",
			WorktreeBranch:     "task-1",
		})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		return detail.Task
	}
	a = pinWorktree(t, store, mk("first"), root.ID, worktreePath)
	b = pinWorktree(t, store, mk("second"), root.ID, worktreePath)
	return svc, root, mainDir, worktreePath, a, b
}

// 选了「已有分支」而这个分支正被某个 linked worktree 检出时，新建任务必须**认领**
// 那棵已经存在的树，而不是再去 git worktree add 一遍 —— 后者必然报
// "branch is already checked out"，而那其实是个合法请求。
func TestSharedWorktreeIsReusedByBranch(t *testing.T) {
	ctx := context.Background()
	mainDir := newGitRoot(t)
	worktreePath := filepath.Join(mainDir, ".worktree", "task-1")
	gitForTest(t, mainDir, "worktree", "add", "-q", "-b", "task-1", worktreePath)

	root := fs.NewRootInfo("root", "root", mainDir)
	svc := NewService(NewTemplateStoreAt(t.TempDir()), testRoots{root: root})
	runner := &fakeRunner{}
	svc.SetRunner(runner)
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID:             root.ID,
		Stages:             []StageTemplate{userStage("Describe"), agentStage("Fix", "Fix:\n{previous_input}")},
		Input:              "join the existing tree",
		CreateWorktree:     true,
		WorktreeBranchMode: "existing",
		WorktreeBranch:     "task-1",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	task, err := store.GetTask(ctx, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	got, err := svc.ensureTaskWorktree(ctx, store, task)
	if err != nil {
		t.Fatalf("ensureTaskWorktree: %v", err)
	}
	if got.WorktreePath != worktreePath {
		t.Fatalf("WorktreePath = %q, want the existing tree %q", got.WorktreePath, worktreePath)
	}
	if !got.WorktreeBuilt {
		t.Fatal("claiming an existing worktree must record that one was built")
	}
	// 认领必须落库：agent 的 cwd 读的是库里的路径，只在内存里改等于没认领。
	stored, err := store.GetTask(ctx, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask after claim: %v", err)
	}
	if stored.WorktreePath != worktreePath {
		t.Fatalf("claimed path not persisted: %q", stored.WorktreePath)
	}

	runner.mu.Lock()
	called := runner.worktreeCreateCalled
	runner.mu.Unlock()
	if called {
		t.Fatal("claiming must NOT go through CreateTaskWorktree: git would refuse with 'already checked out'")
	}
}

// 主 checkout 绝不能被认成任务 worktree：那会让 agent 直接在主干上改，
// 与「隔离在 worktree 里干活」正好相反。
func TestSharedWorktreeNeverClaimsTheMainCheckout(t *testing.T) {
	ctx := context.Background()
	mainDir := newGitRoot(t)
	root := fs.NewRootInfo("root", "root", mainDir)
	svc := NewService(NewTemplateStoreAt(t.TempDir()), testRoots{root: root})

	if path, ok := svc.worktreePathForBranch(ctx, root.ID, "main"); ok {
		t.Fatalf("main checkout must not be claimed as a task worktree, got %q", path)
	}
}

// 引用计数收尾：先收尾的只解绑（目录/分支都不动），最后一个才真合并拆除。
func TestFinishSharedWorktreeOnlyUnbinds(t *testing.T) {
	ctx := context.Background()
	svc, root, mainDir, worktreePath, a, b := sharedFixture(t, "from worktree\n")

	first, err := svc.FinishTaskWorktree(ctx, FinishWorktreeInput{
		RootID:       root.ID,
		TaskID:       a.ID,
		DeleteBranch: true,
		PruneOrphans: true,
	})
	if err != nil {
		t.Fatalf("first finish: %v", err)
	}
	if !first.ReferenceReleased {
		t.Fatalf("first finish should only unbind, got %+v", first)
	}
	if first.RemainingReferences != 1 {
		t.Fatalf("RemainingReferences = %d, want 1", first.RemainingReferences)
	}
	if first.Merged || first.WorktreeRemoved || first.BranchDeleted {
		t.Fatalf("unbinding must not touch git at all, got %+v", first)
	}
	if first.Task.WorktreePath != "" {
		t.Fatalf("unbound task still pins a worktree: %q", first.Task.WorktreePath)
	}
	// 目录和分支都还在 —— 兄弟任务正在里面干活。
	if _, statErr := os.Stat(worktreePath); statErr != nil {
		t.Fatalf("worktree directory must survive an unbind: %v", statErr)
	}
	gitForTest(t, mainDir, "rev-parse", "--verify", "refs/heads/task-1")

	second, err := svc.FinishTaskWorktree(ctx, FinishWorktreeInput{
		RootID:       root.ID,
		TaskID:       b.ID,
		DeleteBranch: true,
		PruneOrphans: true,
	})
	if err != nil {
		t.Fatalf("last finish: %v", err)
	}
	if second.ReferenceReleased {
		t.Fatalf("the last holder must do the real teardown, got %+v", second)
	}
	if !second.Merged || !second.WorktreeRemoved || !second.BranchDeleted {
		t.Fatalf("last finish should merge and tear down, got %+v", second)
	}
	if body := gitForTest(t, mainDir, "show", "main:note.txt"); !strings.Contains(body, "from worktree") {
		t.Fatalf("main did not receive the worktree content:\n%s", body)
	}
	if _, statErr := os.Stat(worktreePath); statErr == nil {
		t.Fatal("the worktree directory should be gone after the last finish")
	}
}

// 共享期间树里的未提交改动是**兄弟任务的**，据它退给本任务的 agent 等于让 agent
// 去提交别人的半成品。所以共享一律判机械清场（= 解绑分支）。
func TestPlanFinishWorktreeTreatsSharedWorktreeAsMechanical(t *testing.T) {
	ctx := context.Background()
	svc, root, _, worktreePath, a, _ := sharedFixture(t, "")
	// 在树里留一份没提交的改动。
	if err := os.WriteFile(filepath.Join(worktreePath, "note.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("dirty: %v", err)
	}

	plan, err := svc.PlanFinishWorktree(ctx, root.ID, a.ID)
	if err != nil {
		t.Fatalf("PlanFinishWorktree: %v", err)
	}
	if !plan.Mechanical {
		t.Fatalf("a shared worktree must plan as mechanical, got %+v", plan)
	}
	if !strings.Contains(plan.Reason, "只解绑") {
		t.Fatalf("reason should say this is an unbind, got %q", plan.Reason)
	}
}
