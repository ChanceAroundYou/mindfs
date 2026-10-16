package kanban

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mindfs/server/internal/fs"
)

func TestFinishTaskWorktreeStopsOnConflictAndKeepsEverything(t *testing.T) {
	ctx := context.Background()
	// worktree 那边先改，随后主 checkout 在同一行改 —— 必冲突。
	mainDir := newGitRoot(t)
	worktreePath := filepath.Join(mainDir, ".worktree", "task-1")
	gitForTest(t, mainDir, "worktree", "add", "-q", "-b", "task-1", worktreePath)
	if err := os.WriteFile(filepath.Join(worktreePath, "note.txt"), []byte("worktree side\n"), 0o644); err != nil {
		t.Fatalf("seed worktree: %v", err)
	}
	gitForTest(t, worktreePath, "add", "note.txt")
	gitForTest(t, worktreePath, "commit", "-qm", "work")
	if err := os.WriteFile(filepath.Join(mainDir, "note.txt"), []byte("main side\n"), 0o644); err != nil {
		t.Fatalf("seed main: %v", err)
	}
	gitForTest(t, mainDir, "add", "note.txt")
	gitForTest(t, mainDir, "commit", "-qm", "main edit")

	root := fs.NewRootInfo("root", "root", mainDir)
	svc := NewService(NewTemplateStoreAt(t.TempDir()), testRoots{root: root})
	svc.SetRunner(&fakeRunner{})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID:             root.ID,
		Stages:             []StageTemplate{userStage("Describe"), agentStage("Fix", "Fix this:\n{previous_input}")},
		Input:              "something to fix",
		CreateWorktree:     true,
		WorktreeBranchMode: "existing",
		WorktreeBranch:     "task-1",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	store, _ := svc.taskStore(root.ID)
	task, _ := store.GetTask(ctx, detail.Task.ID)
	pinWorktree(t, store, task, root.ID, worktreePath)

	_, err = svc.FinishTaskWorktree(ctx, FinishWorktreeInput{
		RootID:       root.ID,
		TaskID:       detail.Task.ID,
		DeleteBranch: true,
	})
	if err == nil {
		t.Fatal("a conflicting merge must not succeed")
	}
	var conflict *FinishWorktreeConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("err = %v (%T), want *FinishWorktreeConflict", err, err)
	}
	if len(conflict.ConflictFiles) != 1 || conflict.ConflictFiles[0] != "note.txt" {
		t.Fatalf("ConflictFiles = %v, want [note.txt]", conflict.ConflictFiles)
	}
	// 撞上冲突时一件东西都不能丢：worktree 在、分支在、任务归属在。
	// 自动 abort 会把解到一半的取舍连同 MERGE_MSG 一起丢，用户得从头再来。
	if _, statErr := os.Stat(worktreePath); statErr != nil {
		t.Fatalf("the worktree must survive a conflict: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(mainDir, ".git", "refs", "heads", "task-1")); statErr != nil {
		t.Fatalf("the branch must survive a conflict: %v", statErr)
	}
	after, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if after.Task.WorktreePath != worktreePath {
		t.Fatalf("task worktree path changed on conflict: %q", after.Task.WorktreePath)
	}
	// 冲突原因是「仓库待人工处理」，必须让人看见 —— 卡片已经会渲染 session_error。
	if after.Task.AuxFlags.SessionError == "" {
		t.Fatal("the conflict must be recorded on the task so the card can show it")
	}
}

func TestFinishTaskWorktreeIsIdempotent(t *testing.T) {
	ctx := context.Background()
	svc, root, mainDir, _ := finishFixture(t, "from worktree\n")
	taskID := mustTaskID(t, svc, root.ID)
	// 不删分支：第二次还要拿它判「已经合进去了」（删了就说成「分支不存在」，是另一回事）。
	first, err := svc.FinishTaskWorktree(ctx, FinishWorktreeInput{RootID: root.ID, TaskID: taskID})
	if err != nil {
		t.Fatalf("first finish: %v", err)
	}
	if !first.Merged {
		t.Fatal("first run must merge")
	}

	// 第二次：任务还钉着已清的路径就再报「没有 worktree」；把路径按收尾前的样子
	// 放回去，重跑才走到幂等那条路上（这是重复点两次的真实情形）。
	store, _ := svc.taskStore(root.ID)
	task, _ := store.GetTask(ctx, taskID)
	pinWorktree(t, store, task, task.WorktreeRootID, filepath.Join(mainDir, ".worktree", "task-1"))
	second, err := svc.FinishTaskWorktree(ctx, FinishWorktreeInput{RootID: root.ID, TaskID: taskID})
	if err != nil {
		t.Fatalf("second finish must not error: %v", err)
	}
	if second.Merged {
		t.Fatalf("Merged = true on the second run, want the idempotent skip: %+v", second)
	}
	if second.BranchDeleted || second.BranchSkipReason == "" {
		t.Fatalf("no branch was requested for deletion, so nothing may be deleted: %+v", second)
	}
	// 目录本来就不在了 —— 那不是失败。
	if second.WorktreeRemoved {
		t.Fatal("WorktreeRemoved = true, but the directory was already gone")
	}
}

func TestFinishTaskWorktreeRefusesWhenTheWorktreeIsOnAnotherBranch(t *testing.T) {
	// 任务记的是「建树时那个目录 + 那个分支」。目录可能被手工 checkout 到别的分支上，
	// 这时拆它就是拆别人的活 —— 分支已经合过了，事后根本看不出来。
	ctx := context.Background()
	svc, root, _, worktreePath := finishFixture(t, "from worktree\n")
	taskID := mustTaskID(t, svc, root.ID)

	gitForTest(t, worktreePath, "checkout", "-q", "-b", "someone-elses-work")

	_, err := svc.FinishTaskWorktree(ctx, FinishWorktreeInput{
		RootID: root.ID, TaskID: taskID, DeleteBranch: true,
	})
	if err == nil {
		t.Fatal("finish must stop when the worktree is no longer on the task's branch")
	}
	if !strings.Contains(err.Error(), "someone-elses-work") {
		t.Fatalf("the error must name the branch actually found, got %v", err)
	}
	// 别人的活一个字都不能少。
	if _, statErr := os.Stat(filepath.Join(worktreePath, ".git")); statErr != nil {
		t.Fatalf("the worktree must survive: %v", statErr)
	}
}

func TestFinishTaskWorktreeRefusesWhenTheWorktreeIsDetached(t *testing.T) {
	// detached 的 worktree 停在哪一份提交上没人知道，拆之前必须人工确认。
	ctx := context.Background()
	svc, root, _, worktreePath := finishFixture(t, "from worktree\n")
	taskID := mustTaskID(t, svc, root.ID)

	gitForTest(t, worktreePath, "checkout", "-q", "--detach", "HEAD")

	_, err := svc.FinishTaskWorktree(ctx, FinishWorktreeInput{
		RootID: root.ID, TaskID: taskID, DeleteBranch: true,
	})
	if err == nil {
		t.Fatal("finish must stop on a detached worktree")
	}
	if !strings.Contains(err.Error(), "detached") {
		t.Fatalf("the error must say the worktree is detached, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(worktreePath, ".git")); statErr != nil {
		t.Fatalf("the worktree must survive: %v", statErr)
	}
}

func TestFinishTaskWorktreeRefusesADetachedWorktreeEvenWhenTheBranchIsGone(t *testing.T) {
	// 两件事叠在一起：分支被强删 + worktree 停在 detached HEAD。那份提交唯一的
	// 引用就是这个 worktree —— 这是最危险的一格，必须在拆之前拦下。
	ctx := context.Background()
	svc, root, mainDir, worktreePath := finishFixture(t, "from worktree\n")
	taskID := mustTaskID(t, svc, root.ID)

	gitForTest(t, worktreePath, "checkout", "-q", "--detach", "HEAD")
	gitForTest(t, mainDir, "branch", "-D", "task-1")

	_, err := svc.FinishTaskWorktree(ctx, FinishWorktreeInput{
		RootID: root.ID, TaskID: taskID, DeleteBranch: true,
	})
	if err == nil {
		t.Fatal("finish must stop when the only copy of the work is an unreferenced detached HEAD")
	}
	if _, statErr := os.Stat(filepath.Join(worktreePath, ".git")); statErr != nil {
		t.Fatalf("the worktree holding the only copy must survive: %v", statErr)
	}
}

func TestFinishTaskWorktreeRefusesToRunUnderALiveAgent(t *testing.T) {
	// 这是收尾里唯一会「改写历史 + 删目录」的动作，而 agent 的 cwd 就在那个
	// worktree 里。合到一半把目录拆了，agent 后续的提交要么失败、要么落在
	// main 看不到的分支上 —— 所以正在跑时必须**一步都不许动**。
	ctx := context.Background()
	svc, root, mainDir, worktreePath := finishFixture(t, "from worktree\n")
	taskID := mustTaskID(t, svc, root.ID)
	store, _ := svc.taskStore(root.ID)
	task, err := store.GetTask(ctx, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	// 只置 task.status：这是「agent 刚起、status 已翻但段还没记 running」的窗口，
	// 漏掉它就等于漏掉了最容易出事的那一刻。
	task.Status = StatusRunning
	if err := store.UpdateTask(ctx, task); err != nil {
		t.Fatalf("mark running: %v", err)
	}

	if _, err := svc.FinishTaskWorktree(ctx, FinishWorktreeInput{RootID: root.ID, TaskID: taskID, DeleteBranch: true}); err == nil {
		t.Fatal("finishing under a live agent must be refused")
	}
	// 一件东西都不能动：分支在、worktree 在、main 也没被合。
	if _, statErr := os.Stat(filepath.Join(mainDir, ".git", "refs", "heads", "task-1")); statErr != nil {
		t.Fatalf("the branch must survive: %v", statErr)
	}
	if _, statErr := os.Stat(worktreePath); statErr != nil {
		t.Fatalf("the worktree must survive: %v", statErr)
	}
	if body := gitForTest(t, mainDir, "show", "main:note.txt"); strings.Contains(body, "from worktree") {
		t.Fatal("main must not have been merged into while an agent is running")
	}
}

// 反过来：用户的活绝不能被自动清掉。被跟踪文件的未提交改动必须让收尾停下来。
func TestFinishRefusesWhenTheUserStillHasUncommittedWork(t *testing.T) {
	svc, root, _, worktreePath := finishFixture(t, "work")
	// 故意留下一个未提交的改动（agent 该提交而没提交的那种）。
	if err := os.WriteFile(filepath.Join(worktreePath, "note.txt"), []byte("not committed yet"), 0o644); err != nil {
		t.Fatalf("leave uncommitted work: %v", err)
	}
	before := gitForTest(t, worktreePath, "status", "--porcelain")
	if !strings.Contains(before, "note.txt") {
		t.Fatalf("fixture must have a modified tracked file, got: %q", before)
	}

	_, err := svc.FinishTaskWorktree(context.Background(), FinishWorktreeInput{
		RootID: root.ID, TaskID: finishTaskID(t, svc, root.ID),
		DeleteBranch: true, PruneOrphans: true,
	})
	if err == nil {
		t.Fatal("finish must refuse while the user still has uncommitted work")
	}
	// 清单走**结构化字段**，不拼进句子：三十个文件拼成一句读不完的话，等于没说
	// （2026-10-07 用户实测）。所以这里断言类型 + Files，而不是 error 字符串。
	var userChanges *FinishWorktreeUserChanges
	if !errors.As(err, &userChanges) {
		t.Fatalf("err = %T (%v), want *FinishWorktreeUserChanges", err, err)
	}
	if len(userChanges.Files) != 1 || userChanges.Files[0] != "note.txt" {
		t.Fatalf("Files = %v, want [note.txt]", userChanges.Files)
	}
	if strings.Contains(err.Error(), "note.txt") {
		t.Fatalf("清单不该拼进句子（它走 Files 给 UI 列表渲染）：%v", err)
	}
	if _, statErr := os.Stat(worktreePath); statErr != nil {
		t.Fatalf("the worktree must survive a refused finish, stat err = %v", statErr)
	}
	// 未提交的内容必须一字不少地还在。
	if content := readFileString(t, filepath.Join(worktreePath, "note.txt")); content != "not committed yet" {
		t.Fatalf("the user's uncommitted work must be untouched, got %q", content)
	}
}
