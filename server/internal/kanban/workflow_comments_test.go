package kanban

import (
	"context"
	"errors"
	"testing"
	"time"

	"mindfs/server/internal/fs"
)

type recordingCommentSender struct {
	calls []struct {
		sessionKey string
		message    string
	}
	execs  []WorkflowNodeExecution
	onSend func(WorkflowNodeExecution)
	err    error
}

func (r *recordingCommentSender) SendWorkflowComment(_ context.Context, exec WorkflowNodeExecution, message string) error {
	r.calls = append(r.calls, struct {
		sessionKey string
		message    string
	}{exec.NodeRun.SessionKey, message})
	r.execs = append(r.execs, exec)
	if r.onSend != nil {
		r.onSend(exec)
	}
	return r.err
}

// newWorkflowCommentFixture builds one task + one engine-bound run and wires the
// service to the exact same TaskStore (no second connection to the same file).
func newWorkflowCommentFixture(t *testing.T, taskID, runID string, graph WorkflowDefinition) (*TaskStore, *Service, *WorkflowRun, fs.RootInfo) {
	t.Helper()
	ctx := context.Background()
	root := fs.NewRootInfo("wf-comment-root", "wf-comment-root", t.TempDir())
	store, err := NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateTask(ctx, Task{ID: taskID, RootID: root.ID, Status: StatusPending, Stages: []StageTemplate{userStage("input")}},
		StageRun{ID: taskID + "-stage", TaskID: taskID, StageIndex: 0, StageName: "input", Role: RoleUser, Status: StageStatusPending},
		TaskEvent{ID: taskID + "-event", TaskID: taskID, Type: "created"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	run := &WorkflowRun{ID: runID, TaskID: taskID, Revision: 1, Graph: graph}
	if err := store.CreateWorkflowRunAndSetEngine(ctx, run); err != nil {
		t.Fatalf("CreateWorkflowRunAndSetEngine: %v", err)
	}
	svc := NewService(testRoots{root: root})
	svc.SetWriterGate(NewWriterGate())
	svc.stores = map[string]*TaskStore{root.ID: store}
	return store, svc, run, root
}

// startWorkflowAgentNode puts one agent NodeRun into the exact state a live turn
// has at submission time: claimed, running, session already persisted.
func startWorkflowAgentNode(t *testing.T, store *TaskStore, runID, nodeID, sessionKey string) NodeRun {
	t.Helper()
	ctx := context.Background()
	nr := NodeRun{ID: workflowNodeRunID(runID, nodeID, "", 0, 1), RunID: runID, NodeID: nodeID, Attempt: 1, Status: NodeRunQueued}
	claimed, won, err := store.ClaimWorkflowNodeRun(ctx, nr)
	if err != nil || !won {
		t.Fatalf("ClaimWorkflowNodeRun(%s) won=%v err=%v", nodeID, won, err)
	}
	started := time.Now().UTC()
	if err := store.BeginWorkflowNodeRun(ctx, claimed, started); err != nil {
		t.Fatalf("BeginWorkflowNodeRun(%s): %v", nodeID, err)
	}
	if err := store.BindWorkflowNodeSession(ctx, claimed, sessionKey); err != nil {
		t.Fatalf("BindWorkflowNodeSession(%s): %v", nodeID, err)
	}
	got, err := store.WorkflowNodeRunByKey(ctx, runID, nodeID, "", 0, 1)
	if err != nil {
		t.Fatalf("WorkflowNodeRunByKey(%s): %v", nodeID, err)
	}
	return got
}

func postTestComment(t *testing.T, store *TaskStore, taskID, commentID, body, expected string) ContinueRequest {
	t.Helper()
	_, req, err := store.PostComment(context.Background(), PostCommentInput{
		TaskID: taskID, CommentID: commentID, Author: "user", Role: RoleUser, Body: body,
		ExpectedNodeRunID: expected,
	})
	if err != nil {
		t.Fatalf("PostComment(%s): %v", commentID, err)
	}
	return req
}

var workflowCommentTestGraph = WorkflowDefinition{
	Nodes: []WorkflowNode{
		{ID: "build", Kind: NodeKindAgent, Params: map[string]any{"prompt": "实现登录页"}},
		{ID: "review", Kind: NodeKindAgent, Params: map[string]any{"prompt": "审查登录页"}},
		{ID: "done", Kind: NodeKindNotify, Params: map[string]any{"message": "完成"}},
	},
	Edges: []WorkflowEdge{{From: "build", To: "review"}, {From: "review", To: "done"}},
}

// D3: exactly one active Agent NodeRun receives the comment, bound to the
// exact node run id and session; that binding never moves afterwards.
func TestWorkflowCommentBindsExactActiveNodeRunAndIsImmutable(t *testing.T) {
	ctx := context.Background()
	store, _, run, _ := newWorkflowCommentFixture(t, "wf-bind-task", "wf-bind-run", workflowCommentTestGraph)
	build := startWorkflowAgentNode(t, store, run.ID, "build", "sess-build")

	req := postTestComment(t, store, run.TaskID, "c1", "继续实现", "")
	if req.Status != ContinueRequestQueued || req.NodeRunID != build.ID || req.SessionKey != "sess-build" {
		t.Fatalf("binding = %#v, want queued to %s/sess-build", req, build.ID)
	}
	acts, err := store.ListActivities(ctx, run.TaskID)
	if err != nil {
		t.Fatalf("ListActivities: %v", err)
	}
	if len(acts) != 1 || acts[0].RunID != run.ID || acts[0].NodeRunID != build.ID {
		t.Fatalf("comment activity correlation = %#v, want run/node", acts)
	}

	// A second branch becomes active; the frozen binding must not move to it.
	review := startWorkflowAgentNode(t, store, run.ID, "review", "sess-review")
	if _, err := store.BindCommentToActiveNodeRun(ctx, run.TaskID, "c1", review.ID); !errors.Is(err, ErrCommentBindingConflict) {
		t.Fatalf("explicit rebind err = %v, want ErrCommentBindingConflict", err)
	}
	if _, _, err := store.PostComment(ctx, PostCommentInput{
		TaskID: run.TaskID, CommentID: "c1", Author: "user", Role: RoleUser, Body: "继续实现",
		ExpectedNodeRunID: review.ID,
	}); !errors.Is(err, ErrCommentBindingConflict) {
		t.Fatalf("retry with different target err = %v, want ErrCommentBindingConflict", err)
	}
	again, err := store.BindCommentToActiveNodeRun(ctx, run.TaskID, "c1", "")
	if err != nil || again.NodeRunID != build.ID || again.SessionKey != "sess-build" {
		t.Fatalf("immutable binding = %#v err=%v, want %s/sess-build", again, err, build.ID)
	}

	// An explicit target that is not active is rejected before anything persists.
	if _, _, err := store.PostComment(ctx, PostCommentInput{
		TaskID: run.TaskID, CommentID: "c2", Author: "user", Role: RoleUser, Body: "改到不存在节点",
		ExpectedNodeRunID: "missing-node-run",
	}); !errors.Is(err, ErrCommentTargetNotActive) {
		t.Fatalf("invalid target err = %v, want ErrCommentTargetNotActive", err)
	}
	if _, err := store.ListContinueRequests(ctx, run.TaskID); err != nil {
		t.Fatalf("ListContinueRequests: %v", err)
	}
}

// D3 default: several active branches queue the comment; it binds only after
// convergence to exactly one active node, and is never fanned out.
func TestWorkflowCommentQueuesUntilUniqueActiveNodeConverges(t *testing.T) {
	ctx := context.Background()
	store, svc, run, _ := newWorkflowCommentFixture(t, "wf-converge-task", "wf-converge-run", workflowCommentTestGraph)
	build := startWorkflowAgentNode(t, store, run.ID, "build", "sess-build")
	review := startWorkflowAgentNode(t, store, run.ID, "review", "sess-review")

	req := postTestComment(t, store, run.TaskID, "c1", "两个分支都在跑", "")
	if req.Status != ContinueRequestQueued || req.NodeRunID != "" || req.SessionKey != "" {
		t.Fatalf("multi-active binding = %#v, want queued without target", req)
	}

	// build ends; review is the only active agent branch left.
	finishedAt := time.Now().UTC()
	if err := store.FinishWorkflowNodeRun(ctx, build, NodeRunSuccess, map[string]any{"summary": "ok"}, finishedAt, WorkflowEventNodeSucceeded, nil); err != nil {
		t.Fatalf("FinishWorkflowNodeRun(build): %v", err)
	}
	if err := svc.reconcileWorkflowCommentsAfterNodeRun(ctx, store, run, build, NodeRunSuccess); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got, err := store.BindCommentToActiveNodeRun(ctx, run.TaskID, "c1", "")
	if err != nil {
		t.Fatalf("BindCommentToActiveNodeRun: %v", err)
	}
	if got.NodeRunID != review.ID || got.SessionKey != "sess-review" || got.Status != ContinueRequestQueued {
		t.Fatalf("converged binding = %#v, want queued to %s/sess-review", got, review.ID)
	}
}

// D3: zero active agent nodes means a new execution phase. The comment must not
// be auto-bound to a node that starts later, and a terminal run must not revive.
func TestWorkflowCommentZeroActiveAndTerminalRunDoNotBind(t *testing.T) {
	ctx := context.Background()
	store, svc, run, _ := newWorkflowCommentFixture(t, "wf-phase-task", "wf-phase-run", workflowCommentTestGraph)

	req := postTestComment(t, store, run.TaskID, "c1", "开启新阶段", "")
	if req.Status != ContinueRequestPendingPhase || req.NodeRunID != "" {
		t.Fatalf("zero-active binding = %#v, want pending_phase without target", req)
	}

	// A branch starting (and finishing) later must not pick up the phase comment.
	build := startWorkflowAgentNode(t, store, run.ID, "build", "sess-build")
	sender := &recordingCommentSender{}
	svc.SetWorkflowCommentSender(sender)
	if err := store.FinishWorkflowNodeRun(ctx, build, NodeRunSuccess, map[string]any{"summary": "ok"}, time.Now().UTC(), WorkflowEventNodeSucceeded, nil); err != nil {
		t.Fatalf("FinishWorkflowNodeRun: %v", err)
	}
	if err := svc.reconcileWorkflowCommentsAfterNodeRun(ctx, store, run, build, NodeRunSuccess); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(sender.calls) != 0 {
		t.Fatalf("pending_phase comment was delivered: %#v", sender.calls)
	}
	got, err := store.BindCommentToActiveNodeRun(ctx, run.TaskID, "c1", "")
	if err != nil {
		t.Fatalf("BindCommentToActiveNodeRun: %v", err)
	}
	if got.Status != ContinueRequestPendingPhase || got.NodeRunID != "" {
		t.Fatalf("phase comment changed = %#v, want still pending_phase", got)
	}

	// Terminal task: new comments stay blocked and never resurrect the run.
	if err := store.UpdateTaskStatus(ctx, run.TaskID, StatusSuccess, true); err != nil {
		t.Fatalf("UpdateTaskStatus: %v", err)
	}
	term := postTestComment(t, store, run.TaskID, "c2", "完成之后再说", "")
	if term.Status != ContinueRequestBlocked || term.NodeRunID != "" {
		t.Fatalf("terminal binding = %#v, want blocked without target", term)
	}
	blocked, err := store.BindCommentToActiveNodeRun(ctx, run.TaskID, "c2", "")
	if err != nil || blocked.Status != ContinueRequestBlocked || blocked.NodeRunID != "" {
		t.Fatalf("terminal run resurrected: %#v err=%v", blocked, err)
	}
}

// D3 delivery: once the bound NodeRun is terminal, queued comments drain FIFO
// into that exact session. The task is still non-terminal here (next node has
// not started), which is when a continuation turn is allowed.
func TestWorkflowCommentsDrainFIFOToSameSessionAfterNodeRun(t *testing.T) {
	ctx := context.Background()
	store, svc, run, _ := newWorkflowCommentFixture(t, "wf-fifo-task", "wf-fifo-run", workflowCommentTestGraph)
	build := startWorkflowAgentNode(t, store, run.ID, "build", "sess-build")
	for _, id := range []string{"c1", "c2", "c3"} {
		req := postTestComment(t, store, run.TaskID, id, "msg-"+id, "")
		if req.NodeRunID != build.ID {
			t.Fatalf("comment %s bound to %q, want %s", id, req.NodeRunID, build.ID)
		}
	}

	sender := &recordingCommentSender{}
	svc.SetWorkflowCommentSender(sender)
	if _, err := svc.finishWorkflowNodeAttempt(ctx, store, run, run.Graph.Nodes[0], build, NodeRunSuccess,
		WorkflowEventNodeSucceeded, map[string]any{"summary": "ok"}, ""); err != nil {
		t.Fatalf("finishWorkflowNodeAttempt: %v", err)
	}
	if len(sender.calls) != 3 {
		t.Fatalf("delivered %d comments, want 3: %#v", len(sender.calls), sender.calls)
	}
	for i, want := range []string{"msg-c1", "msg-c2", "msg-c3"} {
		if sender.calls[i].message != want || sender.calls[i].sessionKey != "sess-build" {
			t.Fatalf("delivery[%d] = %#v, want %q to sess-build", i, sender.calls[i], want)
		}
	}
	requests, err := store.ListContinueRequests(ctx, run.TaskID)
	if err != nil {
		t.Fatalf("ListContinueRequests: %v", err)
	}
	for _, r := range requests {
		if r.Status != ContinueRequestConsumed || r.NodeRunID != build.ID || r.SessionKey != "sess-build" {
			t.Fatalf("request %s = %#v, want consumed on the bound node", r.CommentID, r)
		}
	}
	// The outbox projection must be a faithful copy, not an empty message row.
	outbox := &OutboxService{Store: store}
	projected, err := outbox.GetRequest(ctx, "c2")
	if err != nil {
		t.Fatalf("outbox GetRequest: %v", err)
	}
	if projected.Message != "msg-c2" || projected.NodeRunID != build.ID || projected.SessionKey != "sess-build" {
		t.Fatalf("outbox projection = %#v, want msg-c2 on %s/sess-build", projected, build.ID)
	}
	task, err := store.GetTask(ctx, run.TaskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != StatusPending {
		t.Fatalf("task status = %q, want pending (review not started)", task.Status)
	}
}

// D3 delivery on the node that itself completes the task: a comment posted
// during a single-agent run must still drain FIFO into that session before the
// task projects to success. Projecting first would make reconcile see a terminal
// task and block a message that belongs to the NodeRun that just finished.
func TestWorkflowCommentDeliveredWhenFinishingNodeCompletesTask(t *testing.T) {
	ctx := context.Background()
	graph := WorkflowDefinition{Nodes: []WorkflowNode{
		{ID: "build", Kind: NodeKindAgent, Params: map[string]any{"prompt": "实现登录页"}},
	}}
	store, svc, run, _ := newWorkflowCommentFixture(t, "wf-final-comment-task", "wf-final-comment-run", graph)
	build := startWorkflowAgentNode(t, store, run.ID, "build", "sess-build")
	postTestComment(t, store, run.TaskID, "c1", "顺便改一下文案", "")

	sender := &recordingCommentSender{}
	svc.SetWorkflowCommentSender(sender)
	if _, err := svc.finishWorkflowNodeAttempt(ctx, store, run, graph.Nodes[0], build, NodeRunSuccess,
		WorkflowEventNodeSucceeded, map[string]any{"summary": "ok"}, ""); err != nil {
		t.Fatalf("finishWorkflowNodeAttempt: %v", err)
	}
	if len(sender.calls) != 1 || sender.calls[0].message != "顺便改一下文案" || sender.calls[0].sessionKey != "sess-build" {
		t.Fatalf("delivery = %#v, want the comment on sess-build before the task turns terminal", sender.calls)
	}
	task, err := store.GetTask(ctx, run.TaskID)
	if err != nil || task.Status != StatusSuccess {
		t.Fatalf("task after final node = %#v err=%v, want success", task, err)
	}
	requests, err := store.ListContinueRequests(ctx, run.TaskID)
	if err != nil || len(requests) != 1 || requests[0].Status != ContinueRequestConsumed {
		t.Fatalf("continue requests = %#v err=%v, want consumed", requests, err)
	}
}

// A concurrent graph edit legitimately bumps the run revision between the
// NodeRun terminal write and the projection write. The projection must reload,
// reapply only its own node keys, and preserve the other writer's fields.
