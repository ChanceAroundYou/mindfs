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
	"mindfs/server/internal/gitview"
)

// ── Task 3：成功 merge / 冲突 needs-user / 脏主树 ─────────────────────────

// gitWorkflowRunner 用真 git 建 NodeRun 独立树（merge/close 的行为全靠 git 本身）。
type gitWorkflowRunner struct {
	*fakeRunner
	rootPath string
}

func (r *gitWorkflowRunner) CreateTaskWorktree(ctx context.Context, rootID, name, branchMode, branch string) (WorktreeInfo, error) {
	if strings.TrimSpace(branch) == "" {
		branch = name
	}
	target := filepath.Join(r.rootPath, ".worktree", name)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return WorktreeInfo{}, err
	}
	if err := gitview.AddWorktree(ctx, r.rootPath, target, branchMode, branch); err != nil {
		return WorktreeInfo{}, err
	}
	return WorktreeInfo{RootID: rootID, Path: target}, nil
}

func newGitWorkflowFixture(t *testing.T, mainDir, taskID, runID, integration string) *workflowWorktreeFixture {
	t.Helper()
	ctx := context.Background()
	root := fs.NewRootInfo("wf-git-root", "wf-git-root", mainDir)
	svc := NewService(testRoots{root: root})
	svc.SetWriterGate(NewWriterGate())
	runner := &gitWorkflowRunner{fakeRunner: &fakeRunner{}, rootPath: mainDir}
	svc.SetRunner(runner)
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	if _, err := store.CreateTask(ctx, Task{ID: taskID, RootID: root.ID, Status: StatusPending, Stages: []StageTemplate{userStage("input")}},
		StageRun{ID: taskID + "-stage", TaskID: taskID, StageIndex: 0, StageName: "input", Role: RoleUser, Status: StageStatusPending},
		TaskEvent{ID: taskID + "-event", TaskID: taskID, Type: "created"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	node := WorkflowNode{ID: "build", Kind: NodeKindAgent, Params: map[string]any{
		"prompt": "实现登录页", "worktree": "independent",
	}}
	run := &WorkflowRun{
		ID: runID, TaskID: taskID, Revision: 1, IntegrationBranch: integration,
		Graph: WorkflowDefinition{Nodes: []WorkflowNode{node}},
	}
	if err := store.CreateWorkflowRunAndSetEngine(ctx, run); err != nil {
		t.Fatalf("CreateWorkflowRunAndSetEngine: %v", err)
	}
	wf := &scriptedWorkflowNodeRunner{}
	svc.SetWorkflowNodeRunner(wf)
	return &workflowWorktreeFixture{root: root, store: store, svc: svc, run: run, node: node, fake: runner.fakeRunner, wf: wf}
}

func commitInDir(t *testing.T, dir, name, content, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s in %s: %v", name, dir, err)
	}
	gitForTest(t, dir, "add", name)
	gitForTest(t, dir, "commit", "-qm", msg)
}

// countMergeCommits 数目标分支上的合并提交：重复 merge 最容易在这里暴露。
func countMergeCommits(t *testing.T, dir, branch string) int {
	t.Helper()
	out := gitForTest(t, dir, "log", "--format=%s", branch)
	n := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "Merge branch ") {
			n++
		}
	}
	return n
}

// D11：done 的独立节点恰好合并一次到 Run 快照的集成分支，随后关闭工作树。
// 当前 checkout 不是 main 时也必须用 snapshot，而不是 "main"。
func TestWorkflowNodeSuccessMergesToRunSnapshotBranch(t *testing.T) {
	ctx := context.Background()
	mainDir := newGitRoot(t)
	gitForTest(t, mainDir, "checkout", "-qb", "release/2026-11")
	f := newGitWorkflowFixture(t, mainDir, "merge-task", "merge-run", "release/2026-11")
	f.wf.onRun = func(exec WorkflowNodeExecution) error {
		if exec.RuntimeRootPath == mainDir {
			return errors.New("independent node ran in the main checkout")
		}
		commitInDir(t, exec.RuntimeRootPath, "feature.txt", "merged\n", "work")
		return nil
	}

	nr, err := f.attempt(t, "nr-merge")
	if err != nil {
		t.Fatalf("attempt: %v", err)
	}
	if nr.Status != NodeRunSuccess {
		t.Fatalf("status = %q, want success", nr.Status)
	}
	if got, err := os.ReadFile(filepath.Join(mainDir, "feature.txt")); err != nil || string(got) != "merged\n" {
		t.Fatalf("feature not merged to release snapshot (content=%q err=%v)", got, err)
	}
	// main 分支不该收到这次合并。
	if _, err := exec.Command("git", "-C", mainDir, "show", "main:feature.txt").CombinedOutput(); err == nil {
		t.Fatal("merge leaked into main; run snapshot branch must be the only target")
	}
	if n := countMergeCommits(t, mainDir, "release/2026-11"); n != 1 {
		t.Fatalf("merge commits on release = %d, want exactly 1", n)
	}
	entry, err := f.store.FindWorktreeEntryByNodeRun(ctx, "merge-task", "nr-merge")
	if err != nil {
		t.Fatalf("registry entry: %v", err)
	}
	if entry.State != WorktreeStateClosed || entry.MergedCommit == "" {
		t.Fatalf("entry after merge = %#v, want closed with merged commit", entry)
	}
	if _, statErr := os.Stat(entry.Path); !os.IsNotExist(statErr) {
		t.Fatalf("merged worktree dir must be removed, stat err = %v", statErr)
	}
}

// 没有 Run 快照分支时既不建树也不跑 Agent（D11 fail closed）；AutoMerge 本身
// 也绝不能回落 "main"。
func TestWorkflowNodeMergeRequiresRunSnapshotBranch(t *testing.T) {
	mainDir := newGitRoot(t)
	f := newGitWorkflowFixture(t, mainDir, "no-branch-task", "no-branch-run", "")
	f.wf.onRun = func(exec WorkflowNodeExecution) error {
		commitInDir(t, exec.RuntimeRootPath, "feature.txt", "x\n", "work")
		return nil
	}
	nr, err := f.attempt(t, "nr-no-branch")
	if !errors.Is(err, ErrWorkflowIntegrationBranchMissing) {
		t.Fatalf("attempt err = %v, want ErrWorkflowIntegrationBranchMissing", err)
	}
	if nr.Status != NodeRunFail {
		t.Fatalf("status = %q, want fail", nr.Status)
	}
	if f.wf.runCalls != 0 {
		t.Fatal("agent ran without a run-pinned integration branch")
	}
	if n := countMergeCommits(t, mainDir, "main"); n != 0 {
		t.Fatalf("merge commits on main = %d, want 0", n)
	}
}

func TestAutoMergeWorktreeRefusesEmptyIntegrationBranch(t *testing.T) {
	ctx := context.Background()
	svc, root, _, taskID := multiMergeFixture(t, "a\n", "")
	if _, err := svc.AutoMergeWorktree(ctx, AutoMergeWorktreeInput{RootID: root.ID, TaskID: taskID, NodeRunID: "nr-a"}); !errors.Is(err, ErrWorkflowIntegrationBranchMissing) {
		t.Fatalf("err = %v, want ErrWorkflowIntegrationBranchMissing", err)
	}
	store, _ := svc.taskStore(root.ID)
	entry, err := store.GetWorktreeEntry(ctx, taskID, "agent/a")
	if err != nil {
		t.Fatalf("GetWorktreeEntry: %v", err)
	}
	if entry.State != WorktreeStateActive {
		t.Fatalf("state = %q after refused merge, want active", entry.State)
	}
}

// 冲突：NodeRun/Task 进 waiting_user，registry needs_user，两边现场都保留，
// 不自动 abort/stash，也不把节点标成 success。
func TestWorkflowNodeMergeConflictWaitsUserAndKeepsBothSides(t *testing.T) {
	mainDir := newGitRoot(t)
	f := newGitWorkflowFixture(t, mainDir, "conflict-task", "conflict-run", "main")
	f.wf.onRun = func(exec WorkflowNodeExecution) error {
		commitInDir(t, exec.RuntimeRootPath, "note.txt", "worktree side\n", "worktree edit")
		commitInDir(t, mainDir, "note.txt", "main side\n", "main edit")
		return nil
	}
	nr, err := f.attempt(t, "nr-conflict")
	if err != nil {
		t.Fatalf("waiting_user must not surface as an execution error: %v", err)
	}
	if nr.Status != NodeRunWaitingUser {
		t.Fatalf("status = %q, want waiting_user", nr.Status)
	}
	entry, err := f.store.FindWorktreeEntryByNodeRun(context.Background(), "conflict-task", "nr-conflict")
	if err != nil {
		t.Fatalf("registry entry: %v", err)
	}
	if entry.State != WorktreeStateNeedsUser || len(entry.ConflictFiles) == 0 {
		t.Fatalf("entry = %#v, want needs_user with conflict files", entry)
	}
	if !worktreeDirUsable(entry.Path) {
		t.Fatalf("conflict worktree dir %q must survive", entry.Path)
	}
	if out := gitForTest(t, mainDir, "branch", "--list", entry.Branch); strings.TrimSpace(out) == "" {
		t.Fatalf("source branch %q must survive conflict", entry.Branch)
	}
	if _, statErr := os.Stat(filepath.Join(mainDir, ".git", "MERGE_HEAD")); statErr != nil {
		t.Fatalf("MERGE_HEAD must remain (no auto abort): %v", statErr)
	}
	if out := gitForTest(t, mainDir, "stash", "list"); strings.TrimSpace(out) != "" {
		t.Fatalf("no stash must be created, got %q", out)
	}
	task, err := f.store.GetTask(context.Background(), "conflict-task")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != StatusWaitingUser {
		t.Fatalf("task status = %q, want waiting_user", task.Status)
	}
}

// 脏主 checkout：明确拒绝，不 stash/不覆盖用户文件；NodeRun/task 等用户处理。
func TestWorkflowNodeMergeRefusesDirtyMainCheckout(t *testing.T) {
	mainDir := newGitRoot(t)
	f := newGitWorkflowFixture(t, mainDir, "dirty-task", "dirty-run", "main")
	f.wf.onRun = func(exec WorkflowNodeExecution) error {
		commitInDir(t, exec.RuntimeRootPath, "feature.txt", "x\n", "work")
		if err := os.WriteFile(filepath.Join(mainDir, "note.txt"), []byte("user edit\n"), 0o644); err != nil {
			return err
		}
		return nil
	}
	nr, err := f.attempt(t, "nr-dirty")
	if err != nil {
		t.Fatalf("dirty main must surface as waiting_user, got error: %v", err)
	}
	if nr.Status != NodeRunWaitingUser {
		t.Fatalf("status = %q, want waiting_user", nr.Status)
	}
	if got, _ := os.ReadFile(filepath.Join(mainDir, "note.txt")); string(got) != "user edit\n" {
		t.Fatalf("user edit overwritten: %q", got)
	}
	if out := gitForTest(t, mainDir, "stash", "list"); strings.TrimSpace(out) != "" {
		t.Fatalf("no stash must be created, got %q", out)
	}
	entry, err := f.store.FindWorktreeEntryByNodeRun(context.Background(), "dirty-task", "nr-dirty")
	if err != nil {
		t.Fatalf("registry entry: %v", err)
	}
	if entry.State != WorktreeStateActive {
		t.Fatalf("state = %q, want active (retryable after user cleans main)", entry.State)
	}
	if !worktreeDirUsable(entry.Path) {
		t.Fatalf("worktree dir %q must survive dirty refusal", entry.Path)
	}
}

// 合并成功但关闭失败：合并事实已持久化，重试只补关闭，绝不第二次 merge。
func TestAutoMergeCloseFailureRetriesWithoutSecondMerge(t *testing.T) {
	ctx := context.Background()
	svc, root, mainDir, taskID := multiMergeFixture(t, "a\n", "")
	store, _ := svc.taskStore(root.ID)
	wtA := filepath.Join(mainDir, ".worktree", "agent-a")

	first, err := svc.AutoMergeWorktree(ctx, AutoMergeWorktreeInput{
		RootID: root.ID, TaskID: taskID, NodeRunID: "nr-a", IntegrationBranch: "main",
	})
	if err != nil {
		t.Fatalf("first merge: %v", err)
	}
	if first.Entry.State != WorktreeStateMerged || first.Entry.MergedCommit == "" {
		t.Fatalf("first entry = %#v, want merged with commit", first.Entry)
	}

	// 在已合并的树里放一个未跟踪文件，让关闭按「用户改动」拒绝。
	blocker := filepath.Join(wtA, "user.txt")
	if err := os.WriteFile(blocker, []byte("keep\n"), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	if _, err := svc.AutoMergeWorktree(ctx, AutoMergeWorktreeInput{
		RootID: root.ID, TaskID: taskID, NodeRunID: "nr-a", IntegrationBranch: "main",
		CloseAfterMerge: true, DeleteBranch: true,
	}); err == nil || !strings.Contains(err.Error(), "合并已成功") {
		t.Fatalf("close failure err = %v, want merge-succeeded-but-close-failed", err)
	}
	if n := countMergeCommits(t, mainDir, "main"); n != 1 {
		t.Fatalf("merge commits after failed close = %d, want 1", n)
	}
	entry, err := store.GetWorktreeEntry(ctx, taskID, "agent/a")
	if err != nil {
		t.Fatalf("GetWorktreeEntry: %v", err)
	}
	if entry.State != WorktreeStateMerged || entry.MergedCommit != first.Entry.MergedCommit {
		t.Fatalf("entry after failed close = %#v, want merged with original commit", entry)
	}

	if err := os.Remove(blocker); err != nil {
		t.Fatalf("remove blocker: %v", err)
	}
	retry, err := svc.AutoMergeWorktree(ctx, AutoMergeWorktreeInput{
		RootID: root.ID, TaskID: taskID, NodeRunID: "nr-a", IntegrationBranch: "main",
		CloseAfterMerge: true, DeleteBranch: true,
	})
	if err != nil {
		t.Fatalf("close retry: %v", err)
	}
	if retry.Entry.State != WorktreeStateClosed {
		t.Fatalf("retry state = %q, want closed", retry.Entry.State)
	}
	if n := countMergeCommits(t, mainDir, "main"); n != 1 {
		t.Fatalf("merge commits after close retry = %d, want still 1", n)
	}
}

// 重启窗口：agent 已返回、git merge 可能已经发生，但 NodeRun 还没终态。
// 恢复必须凭 agent-done 证据续 merge，不重跑 agent，也不产生第二个合并提交。
func TestWorkflowNodeRestartAfterAgentDoneResumesMergeOnce(t *testing.T) {
	ctx := context.Background()
	mainDir := newGitRoot(t)
	f := newGitWorkflowFixture(t, mainDir, "restart-task", "restart-run", "main")
	nr := f.beginNodeRun(t, "nr-restart")
	if err := f.store.BindWorkflowNodeSession(ctx, nr, "sess-restart"); err != nil {
		t.Fatalf("BindWorkflowNodeSession: %v", err)
	}
	lease, err := f.svc.PrepareWorkflowNodeWorktree(ctx, f.store, f.run, f.node, nr)
	if err != nil {
		t.Fatalf("PrepareWorkflowNodeWorktree: %v", err)
	}
	commitInDir(t, lease.RootPath, "feature.txt", "x\n", "work")
	// 生产路径在 merge 前写下的持久证据。
	if _, err := f.store.AppendWorkflowRunEvent(ctx, WorkflowRunEvent{
		RunID: f.run.ID, Type: WorkflowEventNodeAgentDone, NodeID: f.node.ID,
		NodeRunID: nr.ID, Attempt: nr.Attempt,
		Payload: map[string]any{"output": map[string]any{"summary": "done"}},
	}); err != nil {
		t.Fatalf("append agent-done: %v", err)
	}
	// 模拟死在 git merge 之后、registry/NodeRun 落库之前。
	gitForTest(t, mainDir, "merge", "--no-ff", "-m", "Merge branch '"+lease.Entry.Branch+"'", lease.Entry.Branch)
	lease.Release()
	if n := countMergeCommits(t, mainDir, "main"); n != 1 {
		t.Fatalf("pre-restart merge commits = %d, want 1", n)
	}

	plan, err := f.svc.recoverWorkflowRunsOnStartup(ctx, f.root.ID, f.store, nil)
	if err != nil {
		t.Fatalf("recoverWorkflowRunsOnStartup: %v", err)
	}
	if len(plan.ResumeNodeRunIDs) != 1 || plan.ResumeNodeRunIDs[0] != nr.ID {
		t.Fatalf("resume plan = %#v, want node run %s", plan.ResumeNodeRunIDs, nr.ID)
	}
	got, err := f.store.WorkflowNodeRunByID(ctx, nr.ID)
	if err != nil {
		t.Fatalf("WorkflowNodeRunByID: %v", err)
	}
	if got.Status != NodeRunSuccess {
		t.Fatalf("status after recovery = %q, want success", got.Status)
	}
	if resumed, _ := got.Output["resumed_after_restart"].(bool); !resumed {
		t.Fatalf("output = %#v, want resumed_after_restart", got.Output)
	}
	if n := countMergeCommits(t, mainDir, "main"); n != 1 {
		t.Fatalf("merge commits after recovery = %d, want still 1", n)
	}
	if f.wf.runCalls != 0 {
		t.Fatalf("agent re-ran during recovery (runCalls=%d)", f.wf.runCalls)
	}
	entry, err := f.store.FindWorktreeEntryByNodeRun(ctx, "restart-task", nr.ID)
	if err != nil {
		t.Fatalf("registry entry: %v", err)
	}
	if entry.State != WorktreeStateClosed {
		t.Fatalf("entry state = %q, want closed", entry.State)
	}
}

// 恢复续 merge 后必须继续驱动 DAG：NodeRun 已成功，但如果没有唤醒 driver，
// 下游节点会永远停在未开始。这个窗口只在「重启时 agent 已返回」路径出现，
// 普通 driver 自循环不会碰到。
func TestWorkflowRecoveryResumeMergeWakesDownstream(t *testing.T) {
	ctx := context.Background()
	mainDir := newGitRoot(t)
	f := newGitWorkflowFixture(t, mainDir, "restart-next-task", "restart-next-run", "main")
	next := WorkflowNode{ID: "next", Kind: NodeKindAgent, Params: map[string]any{"prompt": "继续"}}
	f.run.Graph.Nodes = append(f.run.Graph.Nodes, next)
	f.run.Graph.Edges = append(f.run.Graph.Edges, WorkflowEdge{From: f.node.ID, To: next.ID})
	if err := f.store.SaveWorkflowRun(ctx, f.run); err != nil {
		t.Fatalf("SaveWorkflowRun: %v", err)
	}

	nr := f.beginNodeRun(t, "nr-restart-next")
	if err := f.store.BindWorkflowNodeSession(ctx, nr, "sess-restart-next"); err != nil {
		t.Fatalf("BindWorkflowNodeSession: %v", err)
	}
	lease, err := f.svc.PrepareWorkflowNodeWorktree(ctx, f.store, f.run, f.node, nr)
	if err != nil {
		t.Fatalf("PrepareWorkflowNodeWorktree: %v", err)
	}
	commitInDir(t, lease.RootPath, "feature.txt", "x\n", "work")
	if _, err := f.store.AppendWorkflowRunEvent(ctx, WorkflowRunEvent{
		RunID: f.run.ID, Type: WorkflowEventNodeAgentDone, NodeID: f.node.ID,
		NodeRunID: nr.ID, Attempt: nr.Attempt,
		Payload: map[string]any{"output": map[string]any{"summary": "done"}},
	}); err != nil {
		t.Fatalf("append agent-done: %v", err)
	}
	gitForTest(t, mainDir, "merge", "--no-ff", "-m", "Merge branch '"+lease.Entry.Branch+"'", lease.Entry.Branch)
	lease.Release()

	if _, err := f.svc.recoverWorkflowRunsOnStartup(ctx, f.root.ID, f.store, nil); err != nil {
		t.Fatalf("recoverWorkflowRunsOnStartup: %v", err)
	}
	idle, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := f.svc.WaitWorkflowRunIdle(idle, f.root.ID, "restart-next-task"); err != nil {
		t.Fatalf("WaitWorkflowRunIdle: %v", err)
	}
	downstream, err := f.store.WorkflowNodeRunByKey(ctx, f.run.ID, next.ID, "", 0, 1)
	if err != nil {
		t.Fatalf("downstream NodeRun: %v", err)
	}
	if downstream.Status != NodeRunSuccess {
		t.Fatalf("downstream status = %q, want success", downstream.Status)
	}
	if f.wf.runCalls != 1 {
		t.Fatalf("agent run calls = %d, want only the downstream node", f.wf.runCalls)
	}
	if len(f.wf.execs) != 1 || f.wf.execs[0].Node.ID != next.ID {
		t.Fatalf("executed nodes = %#v, want only %q", f.wf.execs, next.ID)
	}
}
