package kanban

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"mindfs/server/internal/fs"
)

// runtimeRunner is deliberately observable: the runtime tests need to prove
// single-flight (no concurrent turn), which session a resumed attempt used, and
// that a paused/cancelled run never claims the next node.
type runtimeRunner struct {
	mu        sync.Mutex
	runCalls  int
	active    int
	maxActive int
	sessions  []string
	outcomes  []WorkflowNodeResult
	errs      []error
	started   chan struct{}
	startOnce sync.Once
	release   chan struct{}
	blockCall int // 0-based; -1 blocks every call
}

func newRuntimeRunner(blockCall int) *runtimeRunner {
	return &runtimeRunner{started: make(chan struct{}), blockCall: blockCall}
}

func (r *runtimeRunner) EnsureWorkflowNodeSession(_ context.Context, exec WorkflowNodeExecution) (string, error) {
	if key := exec.NodeRun.SessionKey; key != "" {
		return key, nil
	}
	return "wf-session:" + exec.NodeRun.ID, nil
}

func (r *runtimeRunner) RunWorkflowNode(ctx context.Context, exec WorkflowNodeExecution) (WorkflowNodeResult, error) {
	r.mu.Lock()
	i := r.runCalls
	r.runCalls++
	r.active++
	if r.active > r.maxActive {
		r.maxActive = r.active
	}
	r.sessions = append(r.sessions, exec.NodeRun.SessionKey)
	block := r.blockCall >= 0 && i == r.blockCall
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.active--
		r.mu.Unlock()
	}()
	r.startOnce.Do(func() { close(r.started) })
	if block {
		select {
		case <-r.release:
		case <-ctx.Done():
			return WorkflowNodeResult{}, ctx.Err()
		}
	}
	if i < len(r.errs) && r.errs[i] != nil {
		return WorkflowNodeResult{}, r.errs[i]
	}
	if i < len(r.outcomes) {
		return r.outcomes[i], nil
	}
	return WorkflowNodeResult{Outcome: WorkflowNodeDone, Output: map[string]any{"summary": "ok"}}, nil
}

func (r *runtimeRunner) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.runCalls
}

func (r *runtimeRunner) sessionSnapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.sessions...)
}

type runtimeCanceller struct {
	mu    sync.Mutex
	calls []NodeRun
	block bool
}

func (c *runtimeCanceller) CancelWorkflowNode(ctx context.Context, nodeRun NodeRun) error {
	c.mu.Lock()
	c.calls = append(c.calls, nodeRun)
	block := c.block
	c.mu.Unlock()
	if block {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func (c *runtimeCanceller) snapshot() []NodeRun {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]NodeRun(nil), c.calls...)
}

func agentGraph(ids ...string) WorkflowDefinition {
	nodes := make([]WorkflowNode, 0, len(ids))
	edges := make([]WorkflowEdge, 0, len(ids)-1)
	for i, id := range ids {
		nodes = append(nodes, WorkflowNode{ID: id, Kind: NodeKindAgent, Params: map[string]any{"prompt": "执行 " + id}})
		if i > 0 {
			edges = append(edges, WorkflowEdge{From: ids[i-1], To: id})
		}
	}
	return WorkflowDefinition{Nodes: nodes, Edges: edges}
}

// newRuntimeHarness creates a task (first stage must be user) plus an explicit
// workflow run bound to the workflow engine. Tests that need legacy stages pass
// them through; graph is what the driver actually executes.
func newRuntimeHarness(t *testing.T, stages []StageTemplate, graph WorkflowDefinition, runID string) (*Service, fs.RootInfo, *TaskStore, Task, *WorkflowRun) {
	t.Helper()
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	detail, err := svc.CreateTask(ctx, CreateTaskInput{RootID: root.ID, Name: "runtime", Stages: stages, Input: "hello"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	if len(graph.Nodes) == 0 {
		// Migration fixtures create their run through the production entry point.
		return svc, root, store, detail.Task, nil
	}
	run := &WorkflowRun{ID: runID, TaskID: detail.Task.ID, Revision: 1, Graph: graph}
	if err := store.CreateWorkflowRunAndSetEngine(ctx, run); err != nil {
		t.Fatalf("CreateWorkflowRunAndSetEngine: %v", err)
	}
	return svc, root, store, detail.Task, run
}

func waitRuntimeIdle(t *testing.T, svc *Service, rootID, taskID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := svc.WaitWorkflowRunIdle(ctx, rootID, taskID); err != nil {
		t.Fatalf("WaitWorkflowRunIdle: %v", err)
	}
}

func waitRuntimeStarted(t *testing.T, runner *runtimeRunner) {
	t.Helper()
	select {
	case <-runner.started:
	case <-time.After(3 * time.Second):
		t.Fatal("workflow runner never started")
	}
}

// Step 1 (comment): posting a comment on a workflow task with no active agent
// starts exactly one driver. Duplicate wake triggers coalesce; the comment that
// started the phase is not retroactively bound to the node that starts later.
func TestWorkflowRuntimeCommentTriggerStartsOneCoalescedDriver(t *testing.T) {
	ctx := context.Background()
	svc, root, store, task, run := newRuntimeHarness(t, []StageTemplate{userStage("输入")}, agentGraph("build"), "run-comment-trigger")
	runner := newRuntimeRunner(0)
	runner.release = make(chan struct{})
	svc.SetWorkflowNodeRunner(runner)

	_, req, err := svc.PostComment(ctx, PostCommentServiceInput{
		RootID: root.ID, TaskID: task.ID, CommentID: "c-start", Author: "user", Body: "开始",
	})
	if err != nil {
		t.Fatalf("PostComment: %v", err)
	}
	waitRuntimeStarted(t, runner)
	if err := svc.WakeWorkflowRun(ctx, root.ID, task.ID, "duplicate"); err != nil {
		t.Fatalf("Wake duplicate: %v", err)
	}
	if err := svc.WakeWorkflowRun(ctx, root.ID, task.ID, "duplicate"); err != nil {
		t.Fatalf("Wake duplicate: %v", err)
	}
	close(runner.release)
	waitRuntimeIdle(t, svc, root.ID, task.ID)

	runner.mu.Lock()
	calls, maxActive := runner.runCalls, runner.maxActive
	runner.mu.Unlock()
	if calls != 1 || maxActive != 1 {
		t.Fatalf("runner calls=%d maxActive=%d, want exactly one turn", calls, maxActive)
	}
	nr, err := store.WorkflowNodeRunByKey(ctx, run.ID, "build", "", 0, 1)
	if err != nil {
		t.Fatalf("WorkflowNodeRunByKey: %v", err)
	}
	if nr.Status != NodeRunSuccess {
		t.Fatalf("node status = %q, want success", nr.Status)
	}
	if req.Status != ContinueRequestPendingPhase {
		t.Fatalf("comment status = %q, want pending_phase (never auto-bound to a later node)", req.Status)
	}
}

// Step 1 (migration/RunNow/Resume): each approved entry point starts the
// workflow driver instead of falling through to the legacy stage executor.
func TestWorkflowRuntimeApprovedTriggersWakeDriver(t *testing.T) {
	t.Run("migration", func(t *testing.T) {
		ctx := context.Background()
		svc, root, store, task, _ := newRuntimeHarness(t,
			[]StageTemplate{userStage("输入"), agentStage("Build", "实现")}, WorkflowDefinition{}, "run-migrate-trigger")
		runner := newRuntimeRunner(-1)
		svc.SetWorkflowNodeRunner(runner)

		run, _, err := svc.MigrateTaskToWorkflowRun(ctx, root.ID, task.ID, "", "user")
		if err != nil {
			t.Fatalf("MigrateTaskToWorkflowRun: %v", err)
		}
		waitRuntimeIdle(t, svc, root.ID, task.ID)
		gate, err := store.WorkflowNodeRunByKey(ctx, run.ID, "stage-0", "", 0, 1)
		if err != nil {
			t.Fatalf("human gate node run: %v", err)
		}
		if gate.Status != NodeRunWaitingUser {
			t.Fatalf("gate status = %q, want waiting_user (driver must have started the run)", gate.Status)
		}
		if runner.calls() != 0 {
			t.Fatalf("agent ran %d times before the human gate, want 0", runner.calls())
		}
	})

	t.Run("run_now", func(t *testing.T) {
		ctx := context.Background()
		svc, root, _, task, _ := newRuntimeHarness(t, []StageTemplate{userStage("输入")}, agentGraph("build"), "run-runnow-trigger")
		runner := newRuntimeRunner(-1)
		svc.SetWorkflowNodeRunner(runner)

		if _, err := svc.RunNow(ctx, MoveInput{RootID: root.ID, TaskID: task.ID}); err != nil {
			t.Fatalf("RunNow: %v", err)
		}
		waitRuntimeIdle(t, svc, root.ID, task.ID)
		if runner.calls() != 1 {
			t.Fatalf("runner calls = %d, want 1 (RunNow must wake the workflow driver)", runner.calls())
		}
	})

	t.Run("resume", func(t *testing.T) {
		ctx := context.Background()
		svc, root, store, task, _ := newRuntimeHarness(t, []StageTemplate{userStage("输入")}, agentGraph("build"), "run-resume-trigger")
		runner := newRuntimeRunner(-1)
		svc.SetWorkflowNodeRunner(runner)
		if err := store.UpdateTaskStatus(ctx, task.ID, StatusPaused, false); err != nil {
			t.Fatalf("set paused: %v", err)
		}
		if _, err := svc.Resume(ctx, MoveInput{RootID: root.ID, TaskID: task.ID}); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		waitRuntimeIdle(t, svc, root.ID, task.ID)
		if runner.calls() != 1 {
			t.Fatalf("runner calls = %d, want 1 after resume", runner.calls())
		}
	})
}

// Step 2: pause blocks the next claim but lets the in-flight node finish; it
// must not be overwritten by the finishing node's status projection. Resume
// wakes the same run and the next node runs.
func TestWorkflowRuntimePauseLetsInFlightFinishThenResumeContinues(t *testing.T) {
	ctx := context.Background()
	svc, root, store, task, run := newRuntimeHarness(t, []StageTemplate{userStage("输入")}, agentGraph("a", "b"), "run-pause")
	runner := newRuntimeRunner(0)
	runner.release = make(chan struct{})
	svc.SetWorkflowNodeRunner(runner)

	if err := svc.WakeWorkflowRun(ctx, root.ID, task.ID, "test"); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	waitRuntimeStarted(t, runner)
	if err := svc.PauseWorkflowRun(ctx, root.ID, task.ID, "user_pause"); err != nil {
		t.Fatalf("PauseWorkflowRun: %v", err)
	}
	close(runner.release)
	waitRuntimeIdle(t, svc, root.ID, task.ID)

	afterPause, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if afterPause.Status != StatusPaused {
		t.Fatalf("task status = %q, want paused (finishing node must not overwrite pause)", afterPause.Status)
	}
	if runner.calls() != 1 {
		t.Fatalf("runner calls = %d, want 1 (b must not be claimed while paused)", runner.calls())
	}
	if _, err := store.WorkflowNodeRunByKey(ctx, run.ID, "a", "", 0, 1); err != nil {
		t.Fatalf("in-flight node a must persist its terminal row: %v", err)
	}
	if _, err := store.WorkflowNodeRunByKey(ctx, run.ID, "b", "", 0, 1); err == nil {
		t.Fatal("node b was claimed after pause; pause must block new claims")
	}

	if err := svc.ResumeWorkflowRun(ctx, root.ID, task.ID, "user_resume"); err != nil {
		t.Fatalf("ResumeWorkflowRun: %v", err)
	}
	waitRuntimeIdle(t, svc, root.ID, task.ID)
	if runner.calls() != 2 {
		t.Fatalf("runner calls = %d, want 2 after resume", runner.calls())
	}
	afterResume, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if afterResume.Status != StatusSuccess {
		t.Fatalf("task status = %q, want success after resume", afterResume.Status)
	}
}

// Step 3: cancel stops the driver, asks the session adapter to cancel with a
// bound, marks open nodes/task cancelled, blocks comments, and leaves the
// worktree directory alone (lifecycle cleanup is a separate decision).
func TestWorkflowRuntimeCancelIsBoundedAndPreservesWorktree(t *testing.T) {
	ctx := context.Background()
	svc, root, store, task, run := newRuntimeHarness(t, []StageTemplate{userStage("输入")}, agentGraph("a", "b"), "run-cancel")
	runner := newRuntimeRunner(0)
	runner.release = make(chan struct{})
	svc.SetWorkflowNodeRunner(runner)
	canceller := &runtimeCanceller{block: true}
	svc.SetWorkflowNodeCanceller(canceller)
	svc.workflowCancelTimeout = 50 * time.Millisecond

	worktree := t.TempDir()
	marker := filepath.Join(worktree, "keep.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE tasks SET worktree_root_id=?, worktree_path=? WHERE id=?`, root.ID, worktree, task.ID); err != nil {
		t.Fatalf("set worktree: %v", err)
	}

	if err := svc.WakeWorkflowRun(ctx, root.ID, task.ID, "test"); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	waitRuntimeStarted(t, runner)
	if _, _, err := store.PostComment(ctx, PostCommentInput{
		TaskID: task.ID, CommentID: "c-cancel", Author: "user", Body: "等待投递",
	}); err != nil {
		t.Fatalf("PostComment: %v", err)
	}

	start := time.Now()
	if err := svc.CancelWorkflowRun(ctx, root.ID, task.ID, "user_cancel"); err != nil {
		t.Fatalf("CancelWorkflowRun: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("cancel took %v; per-node cancellation must be bounded", elapsed)
	}
	waitRuntimeIdle(t, svc, root.ID, task.ID)

	calls := canceller.snapshot()
	if len(calls) != 1 || calls[0].SessionKey == "" {
		t.Fatalf("canceller calls = %+v, want one bound session cancellation", calls)
	}
	nr, err := store.WorkflowNodeRunByKey(ctx, run.ID, "a", "", 0, 1)
	if err != nil {
		t.Fatalf("node a: %v", err)
	}
	if nr.Status != NodeRunCancelled {
		t.Fatalf("node a status = %q, want cancelled", nr.Status)
	}
	if _, err := store.WorkflowNodeRunByKey(ctx, run.ID, "b", "", 0, 1); err == nil {
		t.Fatal("node b was claimed after cancel")
	}
	after, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if after.Status != StatusCancelled {
		t.Fatalf("task status = %q, want cancelled", after.Status)
	}
	req, err := store.ListOpenWorkflowComments(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListOpenWorkflowComments: %v", err)
	}
	if len(req) != 0 {
		t.Fatalf("open comments after cancel = %+v, want none (blocked, not dropped)", req)
	}
	blocked, err := (&OutboxService{Store: store}).GetRequest(ctx, "c-cancel")
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if blocked.Status != OutboxBlocked {
		t.Fatalf("comment status = %q, want blocked", blocked.Status)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("worktree marker missing after cancel: %v", err)
	}
}

// D7 board-verb mapping: Complete/Fail are operator assertions. They must stop
// the open Agent turn, freeze later nodes, block queued comments, persist the
// asserted terminal status, and leave a typed run event behind. Neither verb
// may rewrite the historical NodeRun into a fake success.
