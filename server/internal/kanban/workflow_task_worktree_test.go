package kanban

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mindfs/server/internal/fs"
	"mindfs/server/internal/gitview"
)

// 任务级 + 节点级 worktree 的整套行为（用户三条期望）：
//   1. 建卡勾了 worktree 就真的建树；
//   2. 共享节点在任务树里干活，一轮成功后任务树自动并回主分支；
//   3. 节点树的基线是 Run 集成分支（任务树）的 tip，合回任务树再并回主分支。

// gitWorkflowRunner 的基线版建树：真实 git 从 base 的 tip 切新分支。
func (r *gitWorkflowRunner) CreateTaskWorktreeFrom(ctx context.Context, rootID, name, branchMode, branch, base string) (WorktreeInfo, error) {
	if strings.TrimSpace(branch) == "" {
		branch = name
	}
	target := filepath.Join(r.rootPath, ".worktree", branch)
	if err := gitview.AddWorktreeFrom(ctx, r.rootPath, target, branchMode, branch, base); err != nil {
		return WorktreeInfo{}, err
	}
	return WorktreeInfo{RootID: rootID, Path: target}, nil
}

func newTaskWorktreeService(t *testing.T, mainDir string) (*Service, fs.RootInfo, *TaskStore) {
	t.Helper()
	root := fs.NewRootInfo("wt-root", "wt-root", mainDir)
	svc := NewService(testRoots{root: root})
	svc.SetWriterGate(NewWriterGate())
	svc.SetRunner(&gitWorkflowRunner{fakeRunner: &fakeRunner{}, rootPath: mainDir})
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	return svc, root, store
}

// 期望 1：建卡时勾 worktree 就必须把树建出来（而不是等节点、更不是只有显式
// rebuild-worktree 接口才能建）。活库里的 #97 正是「勾了、库里没树」的现场。
func TestCreateTaskWithWorktreeFlagCreatesTreeImmediately(t *testing.T) {
	ctx := context.Background()
	mainDir := newGitRoot(t)
	svc, root, store := newTaskWorktreeService(t, mainDir)

	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID, CreateWorktree: true,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	path := strings.TrimSpace(detail.Task.WorktreePath)
	if path == "" || !worktreeDirUsable(path) {
		t.Fatalf("task worktree path = %q, want an existing directory right after create", path)
	}
	branch := strings.TrimSpace(gitForTest(t, path, "rev-parse", "--abbrev-ref", "HEAD"))
	if branch == "" || branch == "HEAD" {
		t.Fatalf("task worktree HEAD is detached (%q); the tree must sit on its own branch", branch)
	}
	// 生产约定：分支名与目录名同源（建卡没有显式分支名时由任务名生成）。
	if got := worktreeDirName(path); got != branch {
		t.Fatalf("worktree dir %q vs branch %q: the lazy-create path must keep the dir/branch pairing", got, branch)
	}
	entry, err := store.GetWorktreeEntry(ctx, detail.Task.ID, branch)
	if err != nil {
		t.Fatalf("primary worktree should be registered: %v", err)
	}
	if entry.State != WorktreeStateActive || entry.NodeRunID != "" {
		t.Fatalf("primary entry = %#v, want active with empty node_run_id", entry)
	}
}

// 期望 1 的失败面：建树失败不留孤儿任务（否则用户看到一张「勾了却没树」的卡）。
func TestCreateTaskWorktreeFailureLeavesNoTask(t *testing.T) {
	ctx := context.Background()
	mainDir := newGitRoot(t)
	svc, root, store := newTaskWorktreeService(t, mainDir)

	// 指定一个不存在的既有分支 → `git worktree add <path> <branch>` 必失败。
	if _, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID, CreateWorktree: true,
		WorktreeBranchMode: "existing", WorktreeBranch: "no-such-branch",
	}); err == nil {
		t.Fatal("CreateTask must fail when the requested worktree cannot be created")
	}
	tasks, err := store.ListTasks(ctx, ListTasksOptions{Limit: 10})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("failed worktree creation left %d task(s) behind", len(tasks))
	}
}

// createTaskWithWorktree 建一张勾了 worktree 的卡，返回任务与它实际落在的分支。
// 建卡时不传分支名 = 让产品自己生成（与生产 UI 的「新建分支」一致）。
func createTaskWithWorktree(t *testing.T, svc *Service, rootID string) (Task, string) {
	t.Helper()
	detail, err := svc.CreateTask(context.Background(), CreateTaskInput{RootID: rootID, CreateWorktree: true})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	branch := taskWorktreeBranchOf(detail.Task)
	if branch == "" || strings.TrimSpace(detail.Task.WorktreePath) == "" {
		t.Fatalf("task %s has no worktree: path=%q branch=%q", detail.Task.ID, detail.Task.WorktreePath, branch)
	}
	return detail.Task, branch
}

// runForTaskMerge 造一个「全部节点成功」的 Run，集成分支指到任务树。
func runForTaskMerge(t *testing.T, store *TaskStore, task Task, runID, integration string) *WorkflowRun {
	t.Helper()
	node := WorkflowNode{ID: "build", Kind: NodeKindAgent, Params: map[string]any{"prompt": "干活"}}
	run := &WorkflowRun{
		ID: runID, TaskID: task.ID, Revision: 1, IntegrationBranch: integration,
		Graph:      WorkflowDefinition{Nodes: []WorkflowNode{node}},
		NodeStates: map[string]string{"build": NodeRunSuccess},
	}
	if err := store.CreateWorkflowRunAndSetEngine(context.Background(), run); err != nil {
		t.Fatalf("CreateWorkflowRunAndSetEngine: %v", err)
	}
	return run
}

// 期望 1（后半）：Run 全部成功后任务树自动并回主分支，且重复投影仍幂等。
// 这条路径不能复用节点树的 merged 短路 —— 任务树保持 active，下一轮还要继续合并。
func TestTaskWorktreeMergesIntoMainWhenRunSucceeds(t *testing.T) {
	ctx := context.Background()
	mainDir := newGitRoot(t)
	svc, root, store := newTaskWorktreeService(t, mainDir)
	task, branch := createTaskWithWorktree(t, svc, root.ID)
	commitInDir(t, task.WorktreePath, "feature.txt", "task work\n", "task work")
	run := runForTaskMerge(t, store, task, "run-task-merge", branch)

	if err := svc.projectWorkflowTaskStatus(ctx, store, run); err != nil {
		t.Fatalf("projectWorkflowTaskStatus: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(mainDir, "feature.txt")); err != nil || string(got) != "task work\n" {
		t.Fatalf("task work not merged into main: content=%q err=%v", got, err)
	}
	// 任务树自己的 registry 行仍是 active：下一轮还要从它继续长。
	entry, err := store.GetWorktreeEntry(ctx, task.ID, branch)
	if err != nil || entry.State != WorktreeStateActive {
		t.Fatalf("task worktree entry = %#v err=%v, want still active", entry, err)
	}
	// 重复投影（driver 会多次投影）不能再造合并提交，也不该报错。
	before := countMergeCommits(t, mainDir, "main")
	if err := svc.projectWorkflowTaskStatus(ctx, store, run); err != nil {
		t.Fatalf("second projection: %v", err)
	}
	if after := countMergeCommits(t, mainDir, "main"); after != before {
		t.Fatalf("idempotent projection added merge commits: %d -> %d", before, after)
	}
}

// 失败/取消的一轮不合并 —— 只有全部节点成功的 Run 才把任务树并回主分支。
func TestTaskWorktreeDoesNotMergeWhenRunDidNotSucceed(t *testing.T) {
	ctx := context.Background()
	mainDir := newGitRoot(t)
	svc, root, store := newTaskWorktreeService(t, mainDir)
	task, branch := createTaskWithWorktree(t, svc, root.ID)
	commitInDir(t, task.WorktreePath, "feature.txt", "task work\n", "task work")
	run := runForTaskMerge(t, store, task, "run-task-fail", branch)
	run.NodeStates["build"] = NodeRunFail

	if err := svc.projectWorkflowTaskStatus(ctx, store, run); err != nil {
		t.Fatalf("projectWorkflowTaskStatus: %v", err)
	}
	if _, err := os.Stat(filepath.Join(mainDir, "feature.txt")); !os.IsNotExist(err) {
		t.Fatalf("failed run must not merge the task worktree (stat err=%v)", err)
	}
}

// 冲突：留现场、标 needs_user、落可见错误；主分支不能被悄悄改写。
func TestTaskWorktreeMergeConflictBecomesNeedsUser(t *testing.T) {
	ctx := context.Background()
	mainDir := newGitRoot(t)
	svc, root, store := newTaskWorktreeService(t, mainDir)
	task, branch := createTaskWithWorktree(t, svc, root.ID)
	// 两边改同一个文件：主分支与任务树各一条提交。
	commitInDir(t, mainDir, "note.txt", "main change\n", "main change")
	commitInDir(t, task.WorktreePath, "note.txt", "task change\n", "task change")
	run := runForTaskMerge(t, store, task, "run-task-conflict", branch)

	var conflict *MergeConflictError
	if err := svc.autoMergeTaskWorktreeAfterRun(ctx, store, run); !errors.As(err, &conflict) {
		t.Fatalf("err = %v, want MergeConflictError", err)
	}
	updated, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if !strings.Contains(updated.AuxFlags.SessionError, "冲突") {
		t.Fatalf("session error = %q, want a conflict explanation", updated.AuxFlags.SessionError)
	}
	// 现场保留：主 checkout 停在未解完的合并里，用户解完再收尾。
	if out := gitForTest(t, mainDir, "status", "--porcelain"); !strings.Contains(out, "UU note.txt") {
		t.Fatalf("main checkout should keep the unresolved merge, status=%q", out)
	}
}

// 期望 2 + 3：节点树从任务树 tip 切（不是主 checkout HEAD），成功后合回任务树，
// 任务树再自动并回主分支 —— 完整 w2 → w1 → main。
func TestNestedWorktreesMergeNodeIntoTaskTreeThenTaskTreeIntoMain(t *testing.T) {
	ctx := context.Background()
	mainDir := newGitRoot(t)
	svc, root, store := newTaskWorktreeService(t, mainDir)
	task, branch := createTaskWithWorktree(t, svc, root.ID)
	// 任务树里先有一段「共享节点」的活（共享节点与独立节点混跑的现场）。
	commitInDir(t, task.WorktreePath, "shared.txt", "shared work\n", "shared work")

	node := WorkflowNode{ID: "build", Kind: NodeKindAgent, Params: map[string]any{
		"prompt": "实现登录页", "worktree": "independent",
	}}
	run := &WorkflowRun{
		ID: "run-nested", TaskID: task.ID, Revision: 1, IntegrationBranch: branch,
		Graph: WorkflowDefinition{Nodes: []WorkflowNode{node}},
	}
	if err := store.CreateWorkflowRunAndSetEngine(ctx, run); err != nil {
		t.Fatalf("CreateWorkflowRunAndSetEngine: %v", err)
	}
	wf := &scriptedWorkflowNodeRunner{}
	svc.SetWorkflowNodeRunner(wf)
	wf.onRun = func(exec WorkflowNodeExecution) error {
		if samePathOrClean(exec.RuntimeRootPath, mainDir) {
			return errors.New("independent node ran in the main checkout")
		}
		// 缺陷 B：节点树的基线必须是任务树 tip，任务树已有的活必须在里面。
		if _, err := os.Stat(filepath.Join(exec.RuntimeRootPath, "shared.txt")); err != nil {
			return errors.New("node worktree is not based on the task tree tip: shared.txt missing")
		}
		commitInDir(t, exec.RuntimeRootPath, "node.txt", "node work\n", "node work")
		return nil
	}

	nr, err := svc.RunWorkflowNodeAttempt(ctx, store, run, node, NodeRun{
		ID: "nr-nested", RunID: run.ID, NodeID: "build", Attempt: 1, Status: NodeRunQueued,
	})
	if err != nil {
		t.Fatalf("RunWorkflowNodeAttempt: %v", err)
	}
	if nr.Status != NodeRunSuccess {
		t.Fatalf("node status = %q, want success (output=%v)", nr.Status, nr.Output)
	}
	// 第一跳：节点树 → 任务树（任务树此刻正被 checkout 着，老实现会在这里直接 fail）。
	if _, err := os.Stat(filepath.Join(task.WorktreePath, "node.txt")); err != nil {
		t.Fatalf("node work did not merge into the task tree: %v", err)
	}
	// 清理：节点分支已并入任务树，分支本身应该被删掉（不是漏在主 checkout 上）。
	nodeBranch := renderWorktreeName("", task) + "-" + shortID("nr-nested")
	if _, err := gitviewBranchExists(mainDir, nodeBranch); err == nil {
		t.Fatalf("node branch %q should be deleted after merging into the task tree", nodeBranch)
	}
	// 第二跳：任务树 → 主分支。
	run.NodeStates = map[string]string{"build": NodeRunSuccess}
	if err := svc.projectWorkflowTaskStatus(ctx, store, run); err != nil {
		t.Fatalf("projectWorkflowTaskStatus: %v", err)
	}
	for _, name := range []string{"shared.txt", "node.txt"} {
		if _, err := os.Stat(filepath.Join(mainDir, name)); err != nil {
			t.Fatalf("%s did not arrive in main via w2 -> w1 -> main: %v", name, err)
		}
	}
}

// samePathOrClean 只用于断言「不是主 checkout」，不参与产品逻辑。
func samePathOrClean(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

// gitviewBranchExists 只做断言用：返回非 nil 表示分支不存在。
func gitviewBranchExists(dir, branch string) (string, error) {
	cmd := exec.Command("git", "-C", dir, "show-ref", "--verify", "refs/heads/"+branch)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// 集成分支快照必须取**任务树自己的分支**。
//
// 隔离实例 7434 的现场：建卡勾了 worktree、树也真的建在 task-1 上，但
// WorktreeBranch 这一列是空的（分支名在「新建分支」模式下由目录名派生、从没
// 回写），快照于是落到主 checkout 的当前分支 master 上。后果有两层：独立节点
// 把活并进 master 而不是任务树（w2 → w1 断掉），且 autoMergeTaskWorktreeAfterRun
// 因为 run.IntegrationBranch ≠ 任务树分支而静默不动 —— 任务树永远回不了主分支。
//
// 这条用例走的是真实接缝（建卡 → 快照），不是手搓 IntegrationBranch；上一版
// 嵌套用例正是手搓的，所以漏掉了这个缺陷。
func TestSnapshotRunIntegrationBranchUsesTaskWorktreeBranch(t *testing.T) {
	ctx := context.Background()
	mainDir := newGitRoot(t)
	svc, root, _ := newTaskWorktreeService(t, mainDir)
	task, branch := createTaskWithWorktree(t, svc, root.ID)
	if strings.TrimSpace(task.WorktreeBranch) != "" {
		t.Fatalf("precondition: 建卡在新建分支模式下不该写 WorktreeBranch，实际 %q", task.WorktreeBranch)
	}
	graph := WorkflowDefinition{Nodes: []WorkflowNode{{
		ID: "build", Kind: NodeKindAgent,
		Params: map[string]any{"prompt": "干活", "worktree": "independent"},
	}}}
	got, err := svc.snapshotRunIntegrationBranch(ctx, task, graph)
	if err != nil {
		t.Fatalf("snapshotRunIntegrationBranch: %v", err)
	}
	if got != branch {
		t.Fatalf("集成分支 = %q，期望任务树分支 %q（落到主 checkout 会让节点绕过任务树、任务树也永不回主分支）", got, branch)
	}
}

// 独立节点没产出提交时也要把分支清掉。
//
// 隔离实例 7434 的现场（真实 codex）：第二次 Run 的独立节点发现 node.txt 内容
// 没变化、`git add -A` 无事可做，合并因此没有产生提交；收尾去删分支时走的是主
// checkout 的 `branch -d`，而那一刻任务树的新提交还没并回主分支，git 判定「未
// 合并」直接拒绝，分支就永远留在仓库里（活库留下了 task-1-9a8fd007）。
// 判据应当用**合并实际发生的目录**：没有合并提交就说明分支相对该目录 HEAD 是祖先。
func TestIndependentNodeWithoutCommitsStillDeletesBranch(t *testing.T) {
	ctx := context.Background()
	mainDir := newGitRoot(t)
	svc, root, store := newTaskWorktreeService(t, mainDir)
	task, branch := createTaskWithWorktree(t, svc, root.ID)
	commitInDir(t, task.WorktreePath, "shared.txt", "shared work\n", "shared work")
	// 主线还没并回 master：这正是现场里 -d 误拒的时间窗。
	node := WorkflowNode{ID: "build", Kind: NodeKindAgent, Params: map[string]any{
		"prompt": "看一眼就好", "worktree": "independent",
	}}
	run := &WorkflowRun{
		ID: "run-no-commit", TaskID: task.ID, Revision: 1, IntegrationBranch: branch,
		Graph: WorkflowDefinition{Nodes: []WorkflowNode{node}},
	}
	if err := store.CreateWorkflowRunAndSetEngine(ctx, run); err != nil {
		t.Fatalf("CreateWorkflowRunAndSetEngine: %v", err)
	}
	svc.SetWorkflowNodeRunner(&scriptedWorkflowNodeRunner{})
	nr, err := svc.RunWorkflowNodeAttempt(ctx, store, run, node, NodeRun{
		ID: "nr-no-commit", RunID: run.ID, NodeID: "build", Attempt: 1, Status: NodeRunQueued,
	})
	if err != nil || nr.Status != NodeRunSuccess {
		t.Fatalf("node = %#v err=%v, want success", nr, err)
	}
	nodeBranch := renderWorktreeName("", task) + "-" + shortID("nr-no-commit")
	if out, err := gitviewBranchExists(mainDir, nodeBranch); err == nil {
		t.Fatalf("empty node branch %q left behind: %s", nodeBranch, out)
	}
}
