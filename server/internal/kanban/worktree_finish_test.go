package kanban

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mindfs/server/internal/fs"
)

// 收尾要动真 git（merge / worktree remove / branch -d），所以这里用真仓库而不是
// fakeRunner 造的空目录：那几个命令的行为（哪些退出码代表什么、什么时候留
// MERGE_HEAD、什么时候拒绝删分支）全靠 git 自己，mock 掉就等于把要验的东西也 mock 掉了。

func gitForTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out)
}

// newGitRoot 建一个主 checkout，当前分支叫 main，落在 t.TempDir() 下。
// 分支名显式设成 main：git init 的默认分支随版本变（master/main），不能在测试里赌。
func newGitRoot(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found")
	}
	root := t.TempDir()
	gitForTest(t, root, "init", "-q")
	gitForTest(t, root, "symbolic-ref", "HEAD", "refs/heads/main")
	gitForTest(t, root, "config", "user.email", "test@example.com")
	gitForTest(t, root, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	gitForTest(t, root, "add", "note.txt")
	gitForTest(t, root, "commit", "-qm", "initial")
	return root
}

// finishFixture 建一个「任务在 worktree 里干活」的真实局面：主 checkout + 真
// linked worktree + 一个钉着 WorktreePath/WorktreeBranch 的任务。
func finishFixture(t *testing.T, edit string) (*Service, fs.RootInfo, string, string) {
	t.Helper()
	mainDir := newGitRoot(t)
	worktreePath := filepath.Join(mainDir, ".worktree", "task-1")
	gitForTest(t, mainDir, "worktree", "add", "-q", "-b", "task-1", worktreePath)
	if edit != "" {
		if err := os.WriteFile(filepath.Join(worktreePath, "note.txt"), []byte(edit), 0o644); err != nil {
			t.Fatalf("edit in worktree: %v", err)
		}
		gitForTest(t, worktreePath, "add", "note.txt")
		gitForTest(t, worktreePath, "commit", "-qm", "work")
	}

	root := fs.NewRootInfo("root", "root", mainDir)
	svc := NewService(NewTemplateStoreAt(t.TempDir()), testRoots{root: root})
	runner := &fakeRunner{}
	svc.SetRunner(runner)
	detail, err := svc.CreateTask(context.Background(), CreateTaskInput{
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
	// 直接把 worktree 归属钉上：CreateTask 只在推进到 agent 段时才建树，而这里
	// 要的局面是「树已经在了」，让被测逻辑面对的是真数据而不是空字段。
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	task, err := store.GetTask(context.Background(), detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	pinWorktree(t, store, task, root.ID, worktreePath)
	return svc, root, mainDir, worktreePath
}

// pinWorktree 把任务钉在某个 worktree 上。
//
// 必须走 SetWorktreeRefsAndTask 而不是 UpdateTask：归属那两列的唯一出口是它
// （见 task_store.go 的 updateTaskCore 说明）。用 UpdateTask 钉的话，测试里
// 「任务在 worktree 里」这个前提根本立不住 —— 字段静默不落库，收尾一律报
// 「该任务还没有 worktree」，看起来像被测逻辑坏了。
func pinWorktree(t *testing.T, store *TaskStore, task Task, rootID, path string) Task {
	t.Helper()
	task.WorktreeRootID = rootID
	task.WorktreePath = path
	task.UpdatedAt = time.Now().UTC()
	if err := store.SetWorktreeRefsAndTask(context.Background(), task); err != nil {
		t.Fatalf("pin worktree: %v", err)
	}
	return task
}

// 清归属不许抹掉「建过 worktree」这个事实（2026-10-01 加的字段存在的全部理由）。
//
// 丢了这个事实，客户端就分不出「还没建树」（首段还是 user 段）和「建过、目录已拆」，
// 于是把一棵还在被人用的树显示成「已收尾」—— 2026-09-29 实测的正是这个。
func TestClearingTheWorktreeRecordKeepsTheBuiltFact(t *testing.T) {
	svc, root, _, _ := finishFixture(t, "work")
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	taskID := finishTaskID(t, svc, root.ID)
	pinned := pinWorktree(t, store, mustGetTask(t, svc, root.ID, taskID), root.ID, "/tmp/pinned")

	if !pinned.WorktreeBuilt {
		t.Fatal("pinning a worktree must record that one was built")
	}

	if err := store.ClearWorktreeRefs(context.Background(), taskID); err != nil {
		t.Fatalf("ClearWorktreeRefs: %v", err)
	}
	after := mustGetTask(t, svc, root.ID, taskID)
	if strings.TrimSpace(after.WorktreePath) != "" || strings.TrimSpace(after.WorktreeRootID) != "" {
		t.Fatalf("clearing must drop the path/root binding, got path=%q root=%q", after.WorktreePath, after.WorktreeRootID)
	}
	if !after.WorktreeBuilt {
		t.Fatal("clearing the binding must NOT forget that a worktree was built: that is the only field telling \"not yet built\" apart from \"built and torn down\"")
	}
	// 界面靠这条组合判「已收尾」：清过归属（路径空）+ 建过 + 目录确实没了。
	if !after.WorktreeBuilt || after.WorktreeMissingNow() {
		t.Fatalf("a torn-down task must read as finished (built=true, missing=false), got built=%v missing=%v", after.WorktreeBuilt, after.WorktreeMissingNow())
	}
}

func TestFinishTaskWorktreeMergesThenRemovesAndDeletesBranch(t *testing.T) {
	ctx := context.Background()
	svc, root, mainDir, worktreePath := finishFixture(t, "from worktree\n")

	result, err := svc.FinishTaskWorktree(ctx, FinishWorktreeInput{
		RootID:       root.ID,
		TaskID:       mustTaskID(t, svc, root.ID),
		DeleteBranch: true,
		PruneOrphans: true,
	})
	if err != nil {
		t.Fatalf("FinishTaskWorktree: %v", err)
	}
	if !result.Merged {
		t.Fatalf("Merged = false, want true: %+v", result)
	}
	if !result.WorktreeRemoved {
		t.Fatal("WorktreeRemoved = false, want true")
	}
	if !result.BranchDeleted {
		t.Fatalf("BranchDeleted = false, want true (reason: %q)", result.BranchSkipReason)
	}
	// 内容真的到 main 上了 —— 顺序反了的话这里会空。
	if body := gitForTest(t, mainDir, "show", "main:note.txt"); !strings.Contains(body, "from worktree") {
		t.Fatalf("main did not receive the worktree content:\n%s", body)
	}
	if _, statErr := os.Stat(worktreePath); statErr == nil {
		t.Fatal("the worktree directory should be gone")
	}
	if _, statErr := os.Stat(filepath.Join(mainDir, ".git", "refs", "heads", "task-1")); statErr == nil {
		t.Fatal("the branch should be gone")
	}
	// 任务侧归属必须跟着清：前端 relatedWorktree 是任务优先，留着会让 git 面板去
	// 展开一个已经没了的目录。
	if result.Task.WorktreePath != "" || result.Task.WorktreeRootID != "" {
		t.Fatalf("task still pins a dead worktree: %+v", result.Task)
	}
	stored, err := svc.GetTask(ctx, root.ID, mustTaskID(t, svc, root.ID))
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if stored.Task.WorktreePath != "" {
		t.Fatalf("cleared worktree path not persisted: %q", stored.Task.WorktreePath)
	}
}

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

func TestFinishTaskWorktreeBlocksANewRunFromStartingMidFinish(t *testing.T) {
	// 收尾期间起 agent，agent 的 cwd 就是那个即将被拆掉的目录。
	// 串行的一次性检查挡不住这个 —— 收尾要跑好几秒，中间任何 Next/RunNow 都能插进来。
	svc, root, _, _ := finishFixture(t, "from worktree\n")
	taskID := mustTaskID(t, svc, root.ID)

	release, err := svc.acquireTaskFinish(root.ID, taskID)
	if err != nil {
		t.Fatalf("acquireTaskFinish: %v", err)
	}
	defer release()
	// 必须报错而不是静默丢弃：调用方（Next/RunNow/Resume）靠这个告诉用户
	// 「正在收尾」，静默返回的话就是「点了没反应」。
	if runErr := svc.RunTask(root.ID, taskID); !errors.Is(runErr, errTaskFinishing) {
		t.Fatalf("RunTask during finish = %v, want errTaskFinishing so the caller can tell the user", runErr)
	}
	runner, _ := svc.Runner.(*fakeRunner)
	runner.mu.Lock()
	started := runner.worktreeCreateCalled || len(runner.execs) > 0
	runner.mu.Unlock()
	if started {
		t.Fatal("no agent work may start while a finish holds the lock")
	}
}

func TestFinishTaskWorktreeRefusesASecondConcurrentFinish(t *testing.T) {
	// 两个收尾不能并着跑：它们对着同一个 worktree 一起 merge / remove，
	// 而且先完成的那个 release 会把还在跑的另一个的锁一起放开 ——
	// 之后 RunTask 就能对着一个正在被拆的目录起来。
	svc, root, _, _ := finishFixture(t, "from worktree\n")
	taskID := mustTaskID(t, svc, root.ID)

	release, err := svc.acquireTaskFinish(root.ID, taskID)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer release()

	if _, err := svc.acquireTaskFinish(root.ID, taskID); err == nil {
		t.Fatal("a second concurrent finish must be refused")
	}
	if runErr := svc.RunTask(root.ID, taskID); !errors.Is(runErr, errTaskFinishing) {
		t.Fatalf("RunTask during finish = %v, want errTaskFinishing", runErr)
	}

	// 关键的一条：先完成的那个 release 不能放开锁。
	release()
	if runErr := svc.RunTask(root.ID, taskID); errors.Is(runErr, errTaskFinishing) {
		t.Fatal("after every finish released, a new run must be allowed again")
	}
}

func TestFinishDuringAResumeLeavesNoTaskStuckRunning(t *testing.T) {
	// 收尾撞上 Resume：Resume 先把状态置 running、再起执行体。如果顺序是那样，
	// 起执行体被拒之后任务就永远 running、没有 agent 会再碰它 —— 看起来像卡死。
	// 所以准入检查必须在改状态**之前**。
	ctx := context.Background()
	svc, root, _, _ := finishFixture(t, "from worktree\n")
	taskID := mustTaskID(t, svc, root.ID)

	release, err := svc.acquireTaskFinish(root.ID, taskID)
	if err != nil {
		t.Fatalf("acquireTaskFinish: %v", err)
	}
	defer release()

	if _, err := svc.Resume(ctx, MoveInput{RootID: root.ID, TaskID: taskID}); err != nil {
		t.Fatalf("Resume during a finish must not error outright, got %v", err)
	}
	detail, err := svc.GetTask(ctx, root.ID, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if detail.Task.Status == StatusRunning {
		t.Fatal("Resume must not leave the task marked running with no agent to advance it")
	}
}

func TestFinishCannotSlipInBetweenAdmissionAndTheStatusChange(t *testing.T) {
	// 真正的竞态形状：准入通过之后、状态改成 running 之前，一个收尾抢进来。
	// 纯「查一下」挡不住 —— 查和改之间不是原子的。留着的后果是「状态 running、
	// 没有执行体」，没有 agent 会再碰它，看起来像卡死。
	svc, root, _, _ := finishFixture(t, "from worktree\n")
	taskID := mustTaskID(t, svc, root.ID)

	// 动词拿到准入（= 查过、还没改状态）。
	release, err := svc.beginRunAdmission(root.ID, taskID)
	if err != nil {
		t.Fatalf("beginRunAdmission: %v", err)
	}
	defer release()

	// 收尾在这个窗口里来 —— 必须被拒，而不是插进来拆掉 agent 即将用的目录。
	if _, err := svc.acquireTaskFinish(root.ID, taskID); err == nil {
		t.Fatal("a finish must not acquire while a run admission is in flight")
	}
	release()
	// 准入走完之后，收尾就该能拿到了。
	finishRelease, err := svc.acquireTaskFinish(root.ID, taskID)
	if err != nil {
		t.Fatalf("the finish must be admitted once the run window closed: %v", err)
	}
	finishRelease()
}

func TestRunAdmissionRefusesTwoAtOnceForTheSameTask(t *testing.T) {
	// 两个动词同时要起同一个任务：不能让两处一起改状态。
	svc, root, _, _ := finishFixture(t, "from worktree\n")
	taskID := mustTaskID(t, svc, root.ID)

	release, err := svc.beginRunAdmission(root.ID, taskID)
	if err != nil {
		t.Fatalf("first admission: %v", err)
	}
	defer release()
	if _, err := svc.beginRunAdmission(root.ID, taskID); err == nil {
		t.Fatal("a second run admission for the same task must be refused")
	}
	release()
	// 释放之后还能再拿 —— 锁没有泄漏。
	again, err := svc.beginRunAdmission(root.ID, taskID)
	if err != nil {
		t.Fatalf("the admission must be re-acquirable after release: %v", err)
	}
	again()
}

func TestFinishTaskWorktreeStopsWhileMainCheckoutIsDirty(t *testing.T) {
	ctx := context.Background()
	svc, root, mainDir, _ := finishFixture(t, "from worktree\n")
	taskID := mustTaskID(t, svc, root.ID)

	// 合并在主 checkout 有未提交改动时失败 —— 收尾必须停在这里。
	if err := os.WriteFile(filepath.Join(mainDir, "uncommitted.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatalf("seed dirty: %v", err)
	}
	_, err := svc.FinishTaskWorktree(ctx, FinishWorktreeInput{RootID: root.ID, TaskID: taskID, DeleteBranch: true})
	if err == nil {
		t.Fatal("finish must stop while the main checkout is dirty")
	}
	// 停在这里时 worktree 和分支都还在，什么都没丢。
	if _, statErr := os.Stat(filepath.Join(mainDir, ".git", "refs", "heads", "task-1")); statErr != nil {
		t.Fatalf("the branch must survive an aborted finish: %v", statErr)
	}
	// 冲突类型不该被误报 —— 那是「要人工解冲突」，这里是别的原因。
	var conflict *FinishWorktreeConflict
	if errors.As(err, &conflict) {
		t.Fatalf("a dirty main checkout must not be reported as a conflict: %v", err)
	}
}

func TestFinishTaskWorktreeListsOrphansButKeepsTheirContents(t *testing.T) {
	svc, root, mainDir, worktreePath := finishFixture(t, "from worktree\n")
	taskID := mustTaskID(t, svc, root.ID)
	// 真实形状是：worktree 被拆掉（git 的登记和源码都没了），但目录里 git 完全不跟踪的
	// .mindfs/ 会话库还在。worktree 里有未跟踪内容时 `git worktree remove` 会拒绝，
	// 所以这里直接造出拆完之后的样子，而不是指望收尾能拆掉一个带会话库的树。
	if err := os.MkdirAll(filepath.Join(worktreePath, ".mindfs"), 0o755); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktreePath, ".mindfs", "sessions.db"), []byte("state"), 0o644); err != nil {
		t.Fatalf("seed orphan file: %v", err)
	}
	gitForTest(t, mainDir, "worktree", "remove", "--force", worktreePath)
	if err := os.MkdirAll(filepath.Join(worktreePath, ".mindfs"), 0o755); err != nil {
		t.Fatalf("reseed orphan: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktreePath, ".mindfs", "sessions.db"), []byte("state"), 0o644); err != nil {
		t.Fatalf("reseed orphan file: %v", err)
	}

	result, err := svc.FinishTaskWorktree(context.Background(), FinishWorktreeInput{
		RootID:       root.ID,
		TaskID:       taskID,
		PruneOrphans: true,
	})
	if err != nil {
		t.Fatalf("FinishTaskWorktree: %v", err)
	}
	if len(result.Orphans) != 1 || result.Orphans[0].Path != worktreePath {
		t.Fatalf("Orphans = %+v, want the leftover %q", result.Orphans, worktreePath)
	}
	if !result.Orphans[0].NonEmpty {
		t.Fatalf("orphan = %+v, want NonEmpty so the user knows there is something inside", result.Orphans[0])
	}
	// 只列不删：里面的会话库删了不可恢复。
	if _, statErr := os.Stat(filepath.Join(worktreePath, ".mindfs", "sessions.db")); statErr != nil {
		t.Fatalf("orphan contents must survive: %v", statErr)
	}
	_ = mainDir
}

func TestFinishTaskWorktreeRefusesTaskWithoutWorktree(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{userStage("Describe"), agentStage("Fix", "Fix this:\n{previous_input}")},
		Input:  "no worktree here",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.FinishTaskWorktree(ctx, FinishWorktreeInput{RootID: root.ID, TaskID: detail.Task.ID}); err == nil {
		t.Fatal("a task without a worktree must not be finishable")
	}
}

// mustTaskID 取这个 root 下唯一那个任务的 id（这些用例都只建了一个任务）。
func mustTaskID(t *testing.T, svc *Service, rootID string) string {
	t.Helper()
	tasks, err := svc.ListTasks(context.Background(), rootID, ListTasksOptions{})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected exactly one task, got %d", len(tasks))
	}
	return tasks[0].ID
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

func TestFinishTaskWorktreeDoesNotClobberConcurrentAuxFlags(t *testing.T) {
	// 第 4 步清 worktree 归属时如果拿开头的快照整行写回，会把合并那几秒里
	// agent 写进来的 aux 标记一起覆盖掉（症状：明明报过 plan，任务上却是空的）。
	ctx := context.Background()
	svc, root, _, _ := finishFixture(t, "from worktree\n")
	taskID := mustTaskID(t, svc, root.ID)
	store, _ := svc.taskStore(root.ID)
	markAuxFlags(t, ctx, store, taskID)

	if _, err := svc.FinishTaskWorktree(ctx, FinishWorktreeInput{RootID: root.ID, TaskID: taskID}); err != nil {
		t.Fatalf("FinishTaskWorktree: %v", err)
	}
	after, err := svc.GetTask(ctx, root.ID, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if !after.Task.AuxFlags.HasPlan || !after.Task.AuxFlags.HasTodos {
		t.Fatalf("concurrent aux flags were clobbered: %+v", after.Task.AuxFlags)
	}
	if after.Task.WorktreePath != "" {
		t.Fatalf("worktree path should still be cleared, got %q", after.Task.WorktreePath)
	}
}

func TestFinishTaskWorktreeDoesNotReportAnAlreadyDeletedBranchAsAWarning(t *testing.T) {
	// 重复收尾时分支必然已经没了，git 的原话是 "branch 'task-1' not found."。
	// 透出去的话一次**成功**的收尾会显示成红色报错。
	ctx := context.Background()
	svc, root, mainDir, worktreePath := finishFixture(t, "from worktree\n")
	taskID := mustTaskID(t, svc, root.ID)
	// 先合再删：合过的分支删掉之后，重跑走的是「已合并 → 幂等跳过」那条路，
	// 才轮得到 branch -d 去面对一个不存在的分支（分支从没合过的话，merge 那步
	// 就会先报「分支不存在」，测不到 branch -d 那一段）。
	if _, err := svc.FinishTaskWorktree(ctx, FinishWorktreeInput{RootID: root.ID, TaskID: taskID}); err != nil {
		t.Fatalf("first finish: %v", err)
	}
	// 把路径放回去，重跑才走得到 branch -d 那一段（重复点两次的真实情形）。
	store, _ := svc.taskStore(root.ID)
	task, _ := store.GetTask(ctx, taskID)
	pinWorktree(t, store, task, task.WorktreeRootID, worktreePath)
	// 第一次收尾已经把 worktree 拆掉了，这里只需要把分支删掉。
	gitForTest(t, mainDir, "branch", "-D", "task-1")
	result, err := svc.FinishTaskWorktree(ctx, FinishWorktreeInput{RootID: root.ID, TaskID: taskID, DeleteBranch: true})
	if err != nil {
		t.Fatalf("FinishTaskWorktree: %v", err)
	}
	if result.BranchSkipReason != "" {
		t.Fatalf("an absent branch is not a warning, got %q", result.BranchSkipReason)
	}
}

// markAuxFlags 模拟「合并那几秒里 agent 把 aux 标记写进了库」。
// 走 UpdateTaskAuxFlags 而不是整行 UpdateTask —— 前者只碰 aux 几列，正是线上
// live 路径写它们的方式。
func markAuxFlags(t *testing.T, ctx context.Context, store *TaskStore, taskID string) {
	t.Helper()
	yes := true
	if err := store.UpdateTaskAuxFlags(ctx, taskID, TaskAuxFlagsPatch{HasPlan: &yes, HasTodos: &yes}); err != nil {
		t.Fatalf("mark aux flags: %v", err)
	}
}

// 收尾不能被**自己产生的**未跟踪目录挡住：agent 在收尾段跑完，worktree 里会留下
// .claude/ 和 .omc/，而服务端下一步就是拆这个目录。2026-10-02 端到端实测撞的就是
// 这个 —— git 报「contains modified or untracked files」，收尾卡住，活其实早就
// 并回主干了。
func TestFinishTearsDownDespiteTheStateDirsTheAgentLeftBehind(t *testing.T) {
	svc, root, mainDir, worktreePath := finishFixture(t, "work")
	task := parkAtWaitingUser(t, svc, root.ID, finishTaskID(t, svc, root.ID))
	_ = task
	// 造出「收尾段跑完的现场」：工具状态目录，未跟踪。
	for _, name := range []string{".claude", ".omc"} {
		dir := filepath.Join(worktreePath, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("{}"), 0o644); err != nil {
			t.Fatalf("write %s state: %v", name, err)
		}
	}
	if out := gitForTest(t, worktreePath, "status", "--porcelain"); !strings.Contains(out, "?? .claude") {
		t.Fatalf("fixture must have untracked state dirs, got: %q", out)
	}

	if _, err := svc.FinishTaskWorktree(context.Background(), FinishWorktreeInput{
		RootID: root.ID, TaskID: finishTaskID(t, svc, root.ID),
		DeleteBranch: true, PruneOrphans: true,
	}); err != nil {
		t.Fatalf("finish must not be blocked by the tool's own leftovers: %v", err)
	}
	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Fatalf("the worktree directory should be gone, stat err = %v", err)
	}
	if out := gitForTest(t, mainDir, "branch", "--list", "task-1"); strings.Contains(out, "task-1") {
		t.Fatalf("the branch should be deleted, got: %q", out)
	}
	_ = mainDir
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
	if !strings.Contains(err.Error(), "note.txt") {
		t.Fatalf("the error must name the offending file, got: %v", err)
	}
	if _, statErr := os.Stat(worktreePath); statErr != nil {
		t.Fatalf("the worktree must survive a refused finish, stat err = %v", statErr)
	}
	// 未提交的内容必须一字不少地还在。
	if content := readFileString(t, filepath.Join(worktreePath, "note.txt")); content != "not committed yet" {
		t.Fatalf("the user's uncommitted work must be untouched, got %q", content)
	}
}

// 状态目录 + 用户自己的未跟踪文件混在一起时：只清状态目录，用户的那个要挡住收尾。
func TestFinishCleansStateDirsButStillRefusesForTheUsersOwnFiles(t *testing.T) {
	svc, root, _, worktreePath := finishFixture(t, "work")
	if err := os.MkdirAll(filepath.Join(worktreePath, ".omc", "nested"), 0o755); err != nil {
		t.Fatalf("mkdir .omc: %v", err)
	}
	mine := filepath.Join(worktreePath, "my-notes.md")
	if err := os.WriteFile(mine, []byte("keep me"), 0o644); err != nil {
		t.Fatalf("write user file: %v", err)
	}

	_, err := svc.FinishTaskWorktree(context.Background(), FinishWorktreeInput{
		RootID: root.ID, TaskID: finishTaskID(t, svc, root.ID),
		DeleteBranch: true, PruneOrphans: true,
	})
	if err == nil {
		t.Fatal("an untracked file the user wrote must still block the teardown")
	}
	if !strings.Contains(err.Error(), "my-notes.md") {
		t.Fatalf("the error must name the user's file, got: %v", err)
	}
	if _, statErr := os.Stat(mine); statErr != nil {
		t.Fatalf("the user's own file must not be deleted: %v", statErr)
	}
	if content := readFileString(t, mine); content != "keep me" {
		t.Fatalf("the user's file content must be untouched, got %q", content)
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
