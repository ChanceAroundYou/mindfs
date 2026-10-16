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

	// 主 checkout 里一个**已跟踪**文件有未提交改动时，合并会顶掉它 —— 收尾必须停。
	// 用已跟踪文件而不是新写一个未跟踪文件：后者不再挡合并（见
	// TestFinishTaskWorktreeIgnoresUntrackedFilesInTheMainCheckout），
	// 拿它当「脏」的样本会让这条测试永远绿、却什么都没守住。
	scratch := filepath.Join(mainDir, "uncommitted.txt")
	if err := os.WriteFile(scratch, []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("seed dirty: %v", err)
	}
	gitForTest(t, mainDir, "add", "uncommitted.txt")
	gitForTest(t, mainDir, "commit", "-qm", "seed tracked file")
	if err := os.WriteFile(scratch, []byte("wip\n"), 0o644); err != nil {
		t.Fatalf("modify dirty: %v", err)
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
	// 报错必须点名那个文件，且清单**不进句子**（句子是给人读的，清单给 UI 列表渲染）。
	var dirty *FinishWorktreeDirty
	if !errors.As(err, &dirty) {
		t.Fatalf("want *FinishWorktreeDirty, got %T: %v", err, err)
	}
	if len(dirty.Files) != 1 || dirty.Files[0] != "uncommitted.txt" {
		t.Fatalf("dirty files = %v, want [uncommitted.txt]", dirty.Files)
	}
	if strings.Contains(dirty.Error(), "uncommitted.txt") {
		t.Fatalf("the file list leaked into the sentence: %q", dirty.Error())
	}
}

// 主 checkout 里的**未跟踪**文件不该挡住收尾 —— 2026-10-08 用户实测：task-37 的收尾
// 被两个 e2e 探针脚本卡死在「主 checkout 有未提交改动」，而它们一个字节都不会丢。
//
// 真会覆盖同名未跟踪文件的情况由 git 自己拦（"untracked working tree files would
// be overwritten by merge"），那条线由 gitview 的
// TestMergeBranchLetsGitRefuseAnUntrackedCollision 钉住。
func TestFinishTaskWorktreeIgnoresUntrackedFilesInTheMainCheckout(t *testing.T) {
	ctx := context.Background()
	svc, root, mainDir, _ := finishFixture(t, "from worktree\n")
	taskID := mustTaskID(t, svc, root.ID)
	probe := filepath.Join(mainDir, "e2e-pending-probe.mjs")
	if err := os.WriteFile(probe, []byte("// probe\n"), 0o600); err != nil {
		t.Fatalf("seed probe: %v", err)
	}

	result, err := svc.FinishTaskWorktree(ctx, FinishWorktreeInput{RootID: root.ID, TaskID: taskID, DeleteBranch: true})
	if err != nil {
		t.Fatalf("an untracked file must not block the finish: %v", err)
	}
	if !result.Merged {
		t.Fatalf("Merged = false, want the branch merged")
	}
	// 别人的文件必须一字不少地还在。
	data, err := os.ReadFile(probe)
	if err != nil {
		t.Fatalf("the probe must survive: %v", err)
	}
	if string(data) != "// probe\n" {
		t.Fatalf("the probe was touched: %q", data)
	}
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
