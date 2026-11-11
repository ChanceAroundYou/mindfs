package kanban

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mindfs/server/internal/fs"
)

// ── H1：shortID 必须对完整 NodeRun id 取哈希 ────────────────────────────────
//
// NodeRun id 形如 run-<hex>:<node>:<loop>:<iteration>:<attempt>。截前 8 位会让
// 同一次 Run 的所有节点得到同一个短 id，撞进同一棵 worktree/分支。
func TestShortIDDistinguishesNodeRunsOfTheSameRun(t *testing.T) {
	build := shortID(workflowNodeRunID("run-abcdef12", "build", "", 0, 1))
	review := shortID(workflowNodeRunID("run-abcdef12", "review", "", 0, 1))
	if build == "" || review == "" {
		t.Fatalf("shortID returned empty (%q, %q)", build, review)
	}
	if build == review {
		t.Fatalf("shortID collision for one run: %q == %q", build, review)
	}
	if build == "run-abcd" {
		t.Fatal("shortID still truncates the shared run prefix instead of hashing the full id")
	}
}

// ── CRITICAL：执行租约不能跨过收尾/留言投递 ─────────────────────────────────
//
// 生产路径 RunWorkflowNodeAttempt 曾在 defer 里把节点 workspace 的 writer gate
// 一路握到 finalize。收尾阶段要把留言续聊回同一个 workspace，于是再次申请同
// 一把**不可重入**的 gate，driver 永久挂起、gate 永久泄漏。
func TestRunWorkflowNodeAttemptDeliversCommentWithoutGateDeadlock(t *testing.T) {
	ctx := context.Background()
	graph := WorkflowDefinition{Nodes: []WorkflowNode{{
		ID: "build", Kind: NodeKindAgent, Params: map[string]any{"prompt": "实现登录页"},
	}}}
	store, svc, run, _ := newWorkflowCommentFixture(t, "wf-prod-deliver-task", "wf-prod-deliver-run", graph)
	node := graph.Nodes[0]
	runner := &scriptedWorkflowNodeRunner{onRun: func(WorkflowNodeExecution) error {
		// 留言在执行器眼里必须绑定到「提交时仍 active」的 NodeRun。
		_, req, err := store.PostComment(ctx, PostCommentInput{
			TaskID: run.TaskID, CommentID: "c1", Author: "user", Role: RoleUser, Body: "继续",
		})
		if err != nil {
			return err
		}
		if req.NodeRunID == "" {
			return errors.New("comment did not bind to the active node run")
		}
		return nil
	}}
	svc.SetWorkflowNodeRunner(runner)
	sender := &recordingCommentSender{}
	svc.SetWorkflowCommentSender(sender)

	done := make(chan error, 1)
	go func() {
		_, err := svc.RunWorkflowNodeAttempt(ctx, store, run, node, workflowAttemptNodeRun(run, node))
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunWorkflowNodeAttempt: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunWorkflowNodeAttempt deadlocked: the node lease was still held during comment delivery")
	}
	if len(sender.calls) != 1 || sender.calls[0].message != "继续" {
		t.Fatalf("comment deliveries = %#v, want exactly one", sender.calls)
	}
	requests, err := store.ListContinueRequests(ctx, run.TaskID)
	if err != nil || len(requests) != 1 || requests[0].Status != ContinueRequestConsumed {
		t.Fatalf("requests = %#v err=%v, want consumed", requests, err)
	}
}

// ── H2：取消与 merge 收尾共用锁，取消先赢时绝不补 merge ──────────────────────
//
// 生产路径：finalize 在锁外等主 checkout gate，操作员的取消走
// cancelOpenWorkflowNodeRuns。旧实现把收尾锁一路握到 merge，取消会被一个
// 无关的写者（这里就是测试持有的 gate）无限期阻塞 —— 本测试同时钉住
// 「取消能及时返回」和「取消赢后不 merge」。
func TestWorkflowNodeCancelWinsAgainstMergeFinalize(t *testing.T) {
	ctx := context.Background()
	mainDir := newGitRoot(t)
	f := newGitWorkflowFixture(t, mainDir, "cancel-race-task", "cancel-race-run", "main")
	nr := f.beginNodeRun(t, "nr-cancel-race")
	if err := f.store.BindWorkflowNodeSession(ctx, nr, "sess-cancel-race"); err != nil {
		t.Fatalf("BindWorkflowNodeSession: %v", err)
	}
	lease, err := f.svc.PrepareWorkflowNodeWorktree(ctx, f.store, f.run, f.node, nr)
	if err != nil {
		t.Fatalf("PrepareWorkflowNodeWorktree: %v", err)
	}
	commitInDir(t, lease.RootPath, "feature.txt", "x\n", "work")
	lease.Release()
	nr, err = f.store.WorkflowNodeRunByID(ctx, nr.ID)
	if err != nil {
		t.Fatalf("WorkflowNodeRunByID: %v", err)
	}

	// 另一个写者占着主 checkout：finalize 必须在这里等，且不能因此握着收尾锁。
	releaseMain, err := f.svc.AcquireNodeWriter(ctx, mainDir, "main")
	if err != nil {
		t.Fatalf("hold main checkout gate: %v", err)
	}
	done := make(chan NodeRun, 1)
	go func() {
		got, _ := f.svc.finalizeWorkflowNodeWithWorktree(ctx, f.store, f.run, f.node, nr,
			map[string]any{"summary": "done"}, "test")
		done <- got
	}()
	// 等 agent-done 证据落库：证明 finalize 已进入「锁外等 gate」的阶段。
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, recorded, rerr := f.svc.workflowNodeAgentDoneOutput(ctx, f.store, nr)
		if rerr != nil {
			t.Fatalf("read agent-done evidence: %v", rerr)
		}
		if recorded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("finalize did not record agent-done evidence")
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case got := <-done:
		t.Fatalf("finalize returned before the main gate was released: %#v", got)
	default:
	}

	cancelDone := make(chan error, 1)
	go func() { cancelDone <- f.svc.cancelOpenWorkflowNodeRuns(ctx, f.root.ID, f.store, f.run.ID, "operator") }()
	select {
	case cerr := <-cancelDone:
		if cerr != nil {
			t.Fatalf("cancelOpenWorkflowNodeRuns: %v", cerr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("operator cancel blocked on the finish lock while finalize waited for the main gate")
	}
	releaseMain()

	select {
	case got := <-done:
		if got.Status != NodeRunCancelled {
			t.Fatalf("finalize status = %q, want cancelled", got.Status)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("finalize did not return after the finish lock was released")
	}
	if n := countMergeCommits(t, mainDir, "main"); n != 0 {
		t.Fatalf("merge commits = %d, want 0 (cancel must win)", n)
	}
	if _, statErr := os.Stat(filepath.Join(mainDir, "feature.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("feature was merged after cancel, stat err = %v", statErr)
	}
	entry, err := f.store.FindWorktreeEntryByNodeRun(ctx, "cancel-race-task", nr.ID)
	if err != nil || entry.State != WorktreeStateActive {
		t.Fatalf("entry after cancelled finalize = %#v err=%v, want active retained", entry, err)
	}
}

// ── H3：自动合并必须持有主 checkout 的物理写 gate ───────────────────────────
//
// 另一个写者正持有主树时，merge 不能插进去改工作区；它必须等到 gate 释放。
func TestAutoMergeWorktreeWaitsForMainCheckoutGate(t *testing.T) {
	ctx := context.Background()
	svc, root, mainDir, taskID := multiMergeFixture(t, "a\n", "")
	release, err := svc.AcquireNodeWriter(ctx, mainDir, "main")
	if err != nil {
		t.Fatalf("hold main checkout gate: %v", err)
	}
	type result struct {
		merged bool
		err    error
	}
	done := make(chan result, 1)
	go func() {
		res, err := svc.AutoMergeWorktree(ctx, AutoMergeWorktreeInput{
			RootID: root.ID, TaskID: taskID, NodeRunID: "nr-a", IntegrationBranch: "main",
		})
		done <- result{merged: res.Entry.State == WorktreeStateMerged || res.Entry.State == WorktreeStateClosed, err: err}
	}()
	select {
	case got := <-done:
		t.Fatalf("merge ran while the main checkout gate was held: %+v", got)
	case <-time.After(300 * time.Millisecond):
	}
	release()
	select {
	case got := <-done:
		if got.err != nil || !got.merged {
			t.Fatalf("merge after gate release = %+v, want merged", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("merge did not resume after the main checkout gate was released")
	}
	if n := countMergeCommits(t, mainDir, "main"); n != 1 {
		t.Fatalf("merge commits = %d, want 1", n)
	}
}

// ── H4：留言投递使用真实的投递工作区，并共用 writer gate ─────────────────────

func bindCommentWorktree(t *testing.T, store *TaskStore, taskID string, nr NodeRun, path, branch string) NodeRun {
	t.Helper()
	entry := WorktreeEntry{
		TaskID: taskID, WorktreeID: "wt-" + nr.NodeID, Branch: branch, Path: path,
		NodeRunID: nr.ID, State: WorktreeStateActive,
	}
	if err := store.BindWorkflowNodeWorktree(context.Background(), nr, entry); err != nil {
		t.Fatalf("BindWorkflowNodeWorktree: %v", err)
	}
	fresh, err := store.WorkflowNodeRunByID(context.Background(), nr.ID)
	if err != nil {
		t.Fatalf("WorkflowNodeRunByID: %v", err)
	}
	return fresh
}

func setTaskWorktreePath(t *testing.T, store *TaskStore, taskID, path string) {
	t.Helper()
	ctx := context.Background()
	task, err := store.GetTask(ctx, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	task.WorktreePath = path
	if err := store.SetWorktreeRefsAndTask(ctx, task); err != nil {
		t.Fatalf("SetWorktreeRefsAndTask: %v", err)
	}
}

// finishCommentNode 直接落 NodeRun 终态并触发 reconcile；避免走独立树的
// merge 收尾（这里测的是留言投递路径，不是合并）。
func finishCommentNode(t *testing.T, svc *Service, store *TaskStore, run *WorkflowRun, nr NodeRun) {
	t.Helper()
	ctx := context.Background()
	if err := store.FinishWorkflowNodeRun(ctx, nr, NodeRunSuccess, map[string]any{"summary": "ok"},
		time.Now().UTC(), WorkflowEventNodeSucceeded, nil); err != nil {
		t.Fatalf("FinishWorkflowNodeRun: %v", err)
	}
	if err := svc.reconcileWorkflowCommentsAfterNodeRun(ctx, store, run, nr, NodeRunSuccess); err != nil {
		t.Fatalf("reconcileWorkflowCommentsAfterNodeRun: %v", err)
	}
}

func TestWorkflowCommentDeliveryPrefersNodeWorktreeAndHoldsGate(t *testing.T) {
	store, svc, run, _ := newWorkflowCommentFixture(t, "wf-deliver-node-task", "wf-deliver-node-run", workflowCommentTestGraph)
	build := startWorkflowAgentNode(t, store, run.ID, "build", "sess-build")
	nodeDir := t.TempDir()
	otherDir := t.TempDir()
	setTaskWorktreePath(t, store, run.TaskID, otherDir)
	build = bindCommentWorktree(t, store, run.TaskID, build, nodeDir, "node-branch")
	postTestComment(t, store, run.TaskID, "c1", "继续", "")

	sender := &recordingCommentSender{}
	var heldWithoutRelease bool
	sender.onSend = func(exec WorkflowNodeExecution) {
		heldWithoutRelease = svc.WriterGate.IsWorkspaceHeld(exec.RuntimeRootPath, "")
	}
	svc.SetWorkflowCommentSender(sender)
	finishCommentNode(t, svc, store, run, build)

	if len(sender.execs) != 1 {
		t.Fatalf("delivered %d comments, want 1", len(sender.execs))
	}
	if got := sender.execs[0].RuntimeRootPath; got != nodeDir {
		t.Fatalf("delivery cwd = %q, want node worktree %q", got, nodeDir)
	}
	if !heldWithoutRelease {
		t.Fatal("delivery did not hold the writer gate for its workspace")
	}
	requests, err := store.ListContinueRequests(context.Background(), run.TaskID)
	if err != nil || len(requests) != 1 || requests[0].Status != ContinueRequestConsumed {
		t.Fatalf("requests = %#v err=%v, want consumed", requests, err)
	}
}

func TestWorkflowCommentDeliveryFallsBackWhenNodeWorktreeIsGone(t *testing.T) {
	store, svc, run, _ := newWorkflowCommentFixture(t, "wf-deliver-fallback-task", "wf-deliver-fallback-run", workflowCommentTestGraph)
	build := startWorkflowAgentNode(t, store, run.ID, "build", "sess-build")
	nodeDir := t.TempDir()
	taskDir := t.TempDir()
	setTaskWorktreePath(t, store, run.TaskID, taskDir)
	build = bindCommentWorktree(t, store, run.TaskID, build, nodeDir, "node-branch")
	if err := os.RemoveAll(nodeDir); err != nil {
		t.Fatalf("remove node worktree: %v", err)
	}
	postTestComment(t, store, run.TaskID, "c1", "继续", "")

	sender := &recordingCommentSender{}
	svc.SetWorkflowCommentSender(sender)
	finishCommentNode(t, svc, store, run, build)

	if len(sender.execs) != 1 || sender.execs[0].RuntimeRootPath != taskDir {
		t.Fatalf("delivery = %#v, want fallback to task worktree %q", sender.execs, taskDir)
	}
}

func TestWorkflowCommentDeliveryFallsBackToRootAndFailsClosedWithoutWorkspace(t *testing.T) {
	ctx := context.Background()
	store, svc, run, root := newWorkflowCommentFixture(t, "wf-deliver-root-task", "wf-deliver-root-run", workflowCommentTestGraph)
	build := startWorkflowAgentNode(t, store, run.ID, "build", "sess-build")
	postTestComment(t, store, run.TaskID, "c1", "继续", "")
	sender := &recordingCommentSender{}
	svc.SetWorkflowCommentSender(sender)
	finishCommentNode(t, svc, store, run, build)
	if len(sender.execs) != 1 || sender.execs[0].RuntimeRootPath != root.RootPath {
		t.Fatalf("delivery = %#v, want root fallback %q", sender.execs, root.RootPath)
	}

	// 没有任何可用工作区：fail closed，不猜路径、不发送。
	store2, svc2, run2, _ := newWorkflowCommentFixture(t, "wf-deliver-none-task", "wf-deliver-none-run", workflowCommentTestGraph)
	build2 := startWorkflowAgentNode(t, store2, run2.ID, "build", "sess-build")
	postTestComment(t, store2, run2.TaskID, "c1", "继续", "")
	svc2.Roots = nil
	sender2 := &recordingCommentSender{}
	svc2.SetWorkflowCommentSender(sender2)
	finishCommentNode(t, svc2, store2, run2, build2)
	if len(sender2.calls) != 0 {
		t.Fatalf("comment sent without a usable workspace: %#v", sender2.calls)
	}
	requests, err := store2.ListContinueRequests(ctx, run2.TaskID)
	if err != nil || len(requests) != 1 || requests[0].Status != ContinueRequestDeliveryFailed {
		t.Fatalf("requests without workspace = %#v err=%v, want delivery_failed", requests, err)
	}
}

func TestWorkflowCommentDeliveryFailsClosedWithoutWriterGate(t *testing.T) {
	store, svc, run, _ := newWorkflowCommentFixture(t, "wf-deliver-gate-task", "wf-deliver-gate-run", workflowCommentTestGraph)
	build := startWorkflowAgentNode(t, store, run.ID, "build", "sess-build")
	postTestComment(t, store, run.TaskID, "c1", "继续", "")
	sender := &recordingCommentSender{}
	svc.SetWorkflowCommentSender(sender)
	svc.WriterGate = nil
	finishCommentNode(t, svc, store, run, build)
	if len(sender.calls) != 0 {
		t.Fatalf("comment sent without a writer gate: %#v", sender.calls)
	}
	requests, err := store.ListContinueRequests(context.Background(), run.TaskID)
	if err != nil || len(requests) != 1 || requests[0].Status != ContinueRequestDeliveryFailed {
		t.Fatalf("requests without gate = %#v err=%v, want delivery_failed", requests, err)
	}
}

// 空留言只阻塞它自己，不能让 FIFO 后面正常留言一起饿死。
func TestWorkflowCommentEmptyBodyDoesNotStarveLaterComments(t *testing.T) {
	ctx := context.Background()
	store, svc, run, _ := newWorkflowCommentFixture(t, "wf-empty-comment-task", "wf-empty-comment-run", workflowCommentTestGraph)
	build := startWorkflowAgentNode(t, store, run.ID, "build", "sess-build")
	postTestComment(t, store, run.TaskID, "c1", "占位", "")
	// 存量/异常数据可能留下空白正文；PostComment 本身拒绝空 body。
	if _, err := store.db.ExecContext(ctx, `UPDATE task_activities SET body='   ' WHERE task_id=? AND id=?`, run.TaskID, "c1"); err != nil {
		t.Fatalf("blank comment body: %v", err)
	}
	postTestComment(t, store, run.TaskID, "c2", "继续", "")
	sender := &recordingCommentSender{}
	svc.SetWorkflowCommentSender(sender)
	finishCommentNode(t, svc, store, run, build)

	if len(sender.calls) != 1 || sender.calls[0].message != "继续" {
		t.Fatalf("deliveries = %#v, want only the non-empty comment", sender.calls)
	}
	requests, err := store.ListContinueRequests(ctx, run.TaskID)
	if err != nil || len(requests) != 2 {
		t.Fatalf("requests = %#v err=%v", requests, err)
	}
	if requests[0].Status != ContinueRequestBlocked || requests[1].Status != ContinueRequestConsumed {
		t.Fatalf("statuses = %q/%q, want blocked/consumed", requests[0].Status, requests[1].Status)
	}
}

// ── H5：loop 内部节点的重启恢复必须能递归查到节点 ────────────────────────────
func TestWorkflowRecoveryResumesLoopInnerAgentDone(t *testing.T) {
	ctx := context.Background()
	inner := WorkflowDefinition{Nodes: []WorkflowNode{{
		ID: "write", Kind: NodeKindAgent, Params: map[string]any{"prompt": "写"},
	}}}
	graph := loopTestGraph("loop1", inner, "true", 1)
	store, svc, run, _ := newWorkflowCommentFixture(t, "wf-loop-recover-task", "wf-loop-recover-run", graph)
	nr := NodeRun{
		ID: workflowNodeRunID(run.ID, "write", "loop1", 1, 1), RunID: run.ID, NodeID: "write",
		LoopPath: "loop1", Iteration: 1, Attempt: 1, Status: NodeRunQueued,
	}
	claimed, won, err := store.ClaimWorkflowNodeRun(ctx, nr)
	if err != nil || !won {
		t.Fatalf("ClaimWorkflowNodeRun won=%v err=%v", won, err)
	}
	if err := store.BeginWorkflowNodeRun(ctx, claimed, time.Now().UTC()); err != nil {
		t.Fatalf("BeginWorkflowNodeRun: %v", err)
	}
	resumed, err := svc.resumeWorkflowNodeMergeAfterRestart(ctx, store, run.ID, claimed, map[string]any{"summary": "done"})
	if err != nil {
		t.Fatalf("resume loop inner node: %v", err)
	}
	if !resumed {
		t.Fatal("loop inner node was not resumed")
	}
	got, err := store.WorkflowNodeRunByID(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("WorkflowNodeRunByID: %v", err)
	}
	if got.Status != NodeRunSuccess || got.Output["resumed_after_restart"] != true {
		t.Fatalf("loop inner node after resume = %#v, want success+resumed", got)
	}
}

// ── M1：同一物理目录不同分支名必须落到同一把锁 ──────────────────────────────
func TestNormalizeWorkspaceKeyUsesPathOnlyWhenPresent(t *testing.T) {
	dir := t.TempDir()
	a := normalizeWorkspaceKey(dir, "main")
	b := normalizeWorkspaceKey(dir+string(os.PathSeparator), "release/2026-11")
	if a != b {
		t.Fatalf("same path with different branch got different keys: %#v vs %#v", a, b)
	}
	if normalizeWorkspaceKey("", "main") == normalizeWorkspaceKey("", "release") {
		t.Fatal("empty path must fall back to branch identity")
	}
}

// ── M2：共享主树节点的 worktree 目录被删后必须明确报错 ──────────────────────
func TestPrepareWorkflowNodeWorktreeFailsWhenTaskWorktreeDeleted(t *testing.T) {
	ctx := context.Background()
	f := newWorkflowWorktreeFixture(t, "wt-gone-task", "wt-gone-run", false, "")
	nr := f.beginNodeRun(t, "nr-wt-gone")
	gone := filepath.Join(t.TempDir(), "removed")
	setTaskWorktreePath(t, f.store, "wt-gone-task", gone)
	_, err := f.svc.PrepareWorkflowNodeWorktree(ctx, f.store, f.run, f.node, nr)
	if err == nil || !strings.Contains(err.Error(), "已不存在") {
		t.Fatalf("prepare err = %v, want explicit deleted-worktree error", err)
	}
}

// ── M3：只剩未提交的用户改动也是「需要人处理」，不是执行失败 ─────────────────
func TestWorkflowNeedsUserMergeErrorIncludesUserChanges(t *testing.T) {
	if !workflowNeedsUserMergeError(&FinishWorktreeUserChanges{Files: []string{"a.txt"}}) {
		t.Fatal("user changes must map to waiting_user, not fail")
	}
}

// ── M4：Run 快照优先取任务 worktree 的分支，目录失效才回落到主 checkout ───────
func TestSnapshotRunIntegrationBranchPrefersTaskWorktreeBranch(t *testing.T) {
	ctx := context.Background()
	mainDir := newGitRoot(t)
	gitForTest(t, mainDir, "checkout", "-qb", "release/2026-11")
	root := fs.NewRootInfo("wf-branch-root", "wf-branch-root", mainDir)
	svc := NewService(testRoots{root: root})
	graph := WorkflowDefinition{Nodes: []WorkflowNode{{
		ID: "build", Kind: NodeKindAgent, Params: map[string]any{"prompt": "x", "worktree": "independent"},
	}}}
	task := Task{RootID: root.ID, WorktreePath: t.TempDir(), WorktreeBranch: "task-9"}
	got, err := svc.snapshotRunIntegrationBranch(ctx, task, graph)
	if err != nil || got != "task-9" {
		t.Fatalf("snapshot with live task worktree = %q err=%v, want task-9", got, err)
	}
	if err := os.RemoveAll(task.WorktreePath); err != nil {
		t.Fatalf("remove task worktree: %v", err)
	}
	got, err = svc.snapshotRunIntegrationBranch(ctx, task, graph)
	if err != nil || got != "release/2026-11" {
		t.Fatalf("snapshot after worktree removal = %q err=%v, want checkout branch", got, err)
	}
}
