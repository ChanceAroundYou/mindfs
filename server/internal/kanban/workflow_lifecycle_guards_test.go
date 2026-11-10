package kanban

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestWorkflowNodeProjectionRetriesRevisionConflict(t *testing.T) {
	ctx := context.Background()
	graph := WorkflowDefinition{Nodes: []WorkflowNode{
		{ID: "build", Kind: NodeKindAgent, Params: map[string]any{"prompt": "实现登录页"}},
	}}
	store, svc, run, _ := newWorkflowCommentFixture(t, "wf-projection-task", "wf-projection-run", graph)
	build := startWorkflowAgentNode(t, store, run.ID, "build", "sess-build")
	stale, err := store.WorkflowRunByID(ctx, run.ID)
	if err != nil {
		t.Fatalf("WorkflowRunByID: %v", err)
	}
	// Another writer (graph edit/loop advance) moves the revision first.
	other, err := store.WorkflowRunByID(ctx, run.ID)
	if err != nil {
		t.Fatalf("WorkflowRunByID fresh: %v", err)
	}
	other.NodeStates = map[string]string{"elsewhere": NodeRunSuccess}
	if err := store.SaveWorkflowRun(ctx, other); err != nil {
		t.Fatalf("concurrent SaveWorkflowRun: %v", err)
	}

	if _, err := svc.finishWorkflowNodeAttempt(ctx, store, stale, graph.Nodes[0], build, NodeRunSuccess,
		WorkflowEventNodeSucceeded, map[string]any{"summary": "ok"}, ""); err != nil {
		t.Fatalf("finishWorkflowNodeAttempt: %v", err)
	}
	reloaded, err := store.WorkflowRunByID(ctx, run.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.NodeStates["build"] != NodeRunSuccess {
		t.Fatalf("node projection lost: %#v", reloaded.NodeStates)
	}
	if reloaded.NodeStates["elsewhere"] != NodeRunSuccess {
		t.Fatalf("concurrent writer's field was overwritten: %#v", reloaded.NodeStates)
	}
}

// The driver checks task status at the top of its step. An operator decision
// can still land before this attempt's queued→running transition, so the guard
// must live in the same UPDATE: a terminal or paused task never starts a turn.
func TestBeginWorkflowNodeRunRefusesTerminalOrPausedTask(t *testing.T) {
	for _, status := range []string{StatusSuccess, StatusFail, StatusCancelled, StatusPaused} {
		t.Run(status, func(t *testing.T) {
			ctx := context.Background()
			graph := WorkflowDefinition{Nodes: []WorkflowNode{
				{ID: "build", Kind: NodeKindAgent, Params: map[string]any{"prompt": "实现登录页"}},
			}}
			store, _, run, _ := newWorkflowCommentFixture(t, "wf-guard-task-"+status, "wf-guard-run-"+status, graph)
			nr := NodeRun{
				ID: workflowNodeRunID(run.ID, "build", "", 0, 1), RunID: run.ID, NodeID: "build",
				Attempt: 1, Status: NodeRunQueued,
			}
			claimed, won, err := store.ClaimWorkflowNodeRun(ctx, nr)
			if err != nil || !won {
				t.Fatalf("claim won=%v err=%v", won, err)
			}
			if err := store.UpdateTaskStatus(ctx, run.TaskID, status, false); err != nil {
				t.Fatalf("UpdateTaskStatus: %v", err)
			}
			if err := store.BeginWorkflowNodeRun(ctx, claimed, time.Now().UTC()); !errors.Is(err, ErrWorkflowNodeRunNotQueued) {
				t.Fatalf("BeginWorkflowNodeRun on %s err = %v, want ErrWorkflowNodeRunNotQueued", status, err)
			}
			row, err := store.WorkflowNodeRunByKey(ctx, run.ID, "build", "", 0, 1)
			if err != nil || row.Status != NodeRunQueued || !row.StartedAt.IsZero() {
				t.Fatalf("row after refused begin = %#v err=%v, want untouched queued", row, err)
			}
		})
	}
}

// A gate approval wakes the driver and returns promptly even when the
// downstream Agent turn is long-running. The decision is already durable; the
// HTTP/WS caller must not sit on a multi-minute turn. Progress arrives through
// task broadcasts instead.
func TestHumanGateApproveDoesNotBlockOnLongDownstreamTurn(t *testing.T) {
	ctx := context.Background()
	graph := WorkflowDefinition{
		Nodes: []WorkflowNode{
			{ID: "gate", Kind: NodeKindHumanGate, Params: map[string]any{"prompt": "确认可以上线"}},
			{ID: "build", Kind: NodeKindAgent, Params: map[string]any{"prompt": "执行上线"}},
		},
		Edges: []WorkflowEdge{{From: "gate", To: "build"}},
	}
	store, svc, run, root := newWorkflowCommentFixture(t, "wf-gate-slow-task", "wf-gate-slow-run", graph)
	runner := newRuntimeRunner(0)
	runner.release = make(chan struct{})
	svc.SetWorkflowNodeRunner(runner)
	if err := svc.AdvanceWorkflowRun(ctx, store, run.TaskID); err != nil {
		t.Fatalf("AdvanceWorkflowRun: %v", err)
	}

	requestCtx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	started := time.Now()
	approved, err := svc.ResolveWorkflowHumanGate(requestCtx, root.ID, run.TaskID, "gate", "approve", "")
	if err != nil {
		t.Fatalf("approve with blocking downstream: %v", err)
	}
	if approved.Status != NodeRunSuccess {
		t.Fatalf("approved gate = %q, want success", approved.Status)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("approve blocked for %v on a downstream Agent turn", elapsed)
	}
	close(runner.release)
	if err := svc.WaitWorkflowRunIdle(context.Background(), root.ID, run.TaskID); err != nil {
		t.Fatalf("wait idle: %v", err)
	}
}

// Terminal evidence keeps its start stamp. Requeue is the only transition that
// clears it (the next attempt has not begun); cancel/unknown must still record
// when the cancelled turn started.
func TestCancelAndUnknownPreserveNodeRunStartedAt(t *testing.T) {
	for _, tc := range []struct {
		name string
		end  func(*TaskStore, NodeRun) (bool, error)
		want string
	}{
		{"cancel", func(s *TaskStore, n NodeRun) (bool, error) {
			return s.CancelWorkflowNodeRun(context.Background(), n, "operator")
		}, NodeRunCancelled},
		{"unknown", func(s *TaskStore, n NodeRun) (bool, error) {
			return s.MarkWorkflowNodeRunUnknown(context.Background(), n, "ambiguous")
		}, NodeRunUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			graph := WorkflowDefinition{Nodes: []WorkflowNode{
				{ID: "build", Kind: NodeKindAgent, Params: map[string]any{"prompt": "实现登录页"}},
			}}
			store, _, run, _ := newWorkflowCommentFixture(t, "wf-stamp-task-"+tc.name, "wf-stamp-run-"+tc.name, graph)
			started := startWorkflowAgentNode(t, store, run.ID, "build", "sess-build")
			if started.StartedAt.IsZero() {
				t.Fatal("fixture did not record a start time")
			}
			if changed, err := tc.end(store, started); err != nil || !changed {
				t.Fatalf("transition changed=%v err=%v", changed, err)
			}
			row, err := store.WorkflowNodeRunByKey(ctx, run.ID, "build", "", 0, 1)
			if err != nil || row.Status != tc.want {
				t.Fatalf("row = %#v err=%v, want %s", row, err, tc.want)
			}
			if !row.StartedAt.Equal(started.StartedAt) {
				t.Fatalf("started_at = %v, want preserved %v", row.StartedAt, started.StartedAt)
			}
			if row.FinishedAt.IsZero() {
				t.Fatal("terminal transition must record finished_at")
			}
		})
	}
}

// A task the human already finished must not be resurrected by approving a
// gate that was still waiting. The gate stays historical waiting_user evidence;
// no downstream node is created.
func TestWorkflowHumanGateCannotResurrectTerminalTask(t *testing.T) {
	ctx := context.Background()
	store, svc, run, root := newWorkflowCommentFixture(t, "wf-gate-terminal", "wf-gate-terminal-run", humanGateGraph())
	if err := svc.AdvanceWorkflowRun(ctx, store, run.TaskID); err != nil {
		t.Fatalf("AdvanceWorkflowRun: %v", err)
	}
	if _, err := store.WorkflowNodeRunByKey(ctx, run.ID, "gate", "", 0, 1); err != nil {
		t.Fatalf("gate node run: %v", err)
	}
	if _, err := svc.Complete(ctx, MoveInput{RootID: root.ID, TaskID: run.TaskID, Reason: "done_by_user"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if _, err := svc.ResolveWorkflowHumanGate(ctx, root.ID, run.TaskID, "gate", "approve", ""); !errors.Is(err, ErrWorkflowNodeRunNotActionable) {
		t.Fatalf("approve on terminal task err = %v, want ErrWorkflowNodeRunNotActionable", err)
	}
	gate, err := store.WorkflowNodeRunByKey(ctx, run.ID, "gate", "", 0, 1)
	if err != nil || gate.Status != NodeRunWaitingUser {
		t.Fatalf("gate after refused approval = %#v err=%v, want waiting_user", gate, err)
	}
	if _, err := store.WorkflowNodeRunByKey(ctx, run.ID, "done", "", 0, 1); err == nil {
		t.Fatal("downstream node was created after terminal-task approval")
	}
}

func humanGateGraph() WorkflowDefinition {
	return WorkflowDefinition{
		Nodes: []WorkflowNode{
			{ID: "gate", Kind: NodeKindHumanGate, Params: map[string]any{"prompt": "确认可以上线"}},
			{ID: "done", Kind: NodeKindNotify, Params: map[string]any{"message": "已上线"}},
		},
		Edges: []WorkflowEdge{{From: "gate", To: "done"}},
	}
}

// D4: a normal comment never approves the gate; only the dedicated decision
// moves it, once, and repeating that same decision is idempotent.
func TestWorkflowHumanGateRequiresExplicitDecisionAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store, svc, run, root := newWorkflowCommentFixture(t, "wf-gate-task", "wf-gate-run", humanGateGraph())
	if err := svc.AdvanceWorkflowRun(ctx, store, run.TaskID); err != nil {
		t.Fatalf("AdvanceWorkflowRun: %v", err)
	}
	gate, err := store.WorkflowNodeRunByKey(ctx, run.ID, "gate", "", 0, 1)
	if err != nil || gate.Status != NodeRunWaitingUser {
		t.Fatalf("gate = %#v err=%v, want waiting_user", gate, err)
	}

	req := postTestComment(t, store, run.TaskID, "c1", "同意，继续", "")
	if req.Status != ContinueRequestPendingPhase || req.NodeRunID != "" {
		t.Fatalf("comment during gate = %#v, want pending_phase (no implicit approval)", req)
	}
	gate, err = store.WorkflowNodeRunByKey(ctx, run.ID, "gate", "", 0, 1)
	if err != nil || gate.Status != NodeRunWaitingUser {
		t.Fatalf("normal comment decided the gate: %#v err=%v", gate, err)
	}
	task, _ := store.GetTask(ctx, run.TaskID)
	if task.Status != StatusWaitingUser {
		t.Fatalf("task status after comment = %q, want waiting_user", task.Status)
	}

	approved, err := svc.ResolveWorkflowHumanGate(ctx, root.ID, run.TaskID, "gate", "approve", "c1")
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if approved.Status != NodeRunSuccess {
		t.Fatalf("approved gate status = %q, want success", approved.Status)
	}
	task, _ = store.GetTask(ctx, run.TaskID)
	if task.Status != StatusSuccess {
		t.Fatalf("task after approval = %q, want success (downstream ran)", task.Status)
	}
	downstream, err := store.WorkflowNodeRunByKey(ctx, run.ID, "done", "", 0, 1)
	if err != nil || downstream.Status != NodeRunSuccess {
		t.Fatalf("downstream = %#v err=%v, want success", downstream, err)
	}

	dup, err := svc.ResolveWorkflowHumanGate(ctx, root.ID, run.TaskID, "gate", "approve", "c1")
	if err != nil || dup.Status != NodeRunSuccess {
		t.Fatalf("duplicate approve = %#v err=%v, want idempotent success", dup, err)
	}
	if _, err := svc.ResolveWorkflowHumanGate(ctx, root.ID, run.TaskID, "gate", "reject", "c1"); !errors.Is(err, ErrWorkflowHumanGateAlreadyDecided) {
		t.Fatalf("opposite decision err = %v, want ErrWorkflowHumanGateAlreadyDecided", err)
	}
	events, err := store.ListWorkflowRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatalf("ListWorkflowRunEvents: %v", err)
	}
	approvedEvents := 0
	for _, ev := range events {
		if ev.Type == WorkflowEventHumanApproved {
			approvedEvents++
		}
	}
	if approvedEvents != 1 {
		t.Fatalf("human_approved events = %d, want exactly 1", approvedEvents)
	}
}

func TestWorkflowHumanGateRejectCancelsAndBlocksComments(t *testing.T) {
	ctx := context.Background()
	store, svc, run, root := newWorkflowCommentFixture(t, "wf-gate-reject-task", "wf-gate-reject-run", humanGateGraph())
	if err := svc.AdvanceWorkflowRun(ctx, store, run.TaskID); err != nil {
		t.Fatalf("AdvanceWorkflowRun: %v", err)
	}
	postTestComment(t, store, run.TaskID, "c1", "先别上线", "")

	rejected, err := svc.ResolveWorkflowHumanGate(ctx, root.ID, run.TaskID, "gate", "reject", "c1")
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if rejected.Status != NodeRunCancelled {
		t.Fatalf("rejected gate status = %q, want cancelled", rejected.Status)
	}
	task, _ := store.GetTask(ctx, run.TaskID)
	if task.Status != StatusCancelled {
		t.Fatalf("task after reject = %q, want cancelled", task.Status)
	}
	if _, err := store.WorkflowNodeRunByKey(ctx, run.ID, "done", "", 0, 1); err == nil {
		t.Fatal("downstream node must not run after reject")
	}
	requests, err := store.ListContinueRequests(ctx, run.TaskID)
	if err != nil {
		t.Fatalf("ListContinueRequests: %v", err)
	}
	if len(requests) != 1 || requests[0].Status != ContinueRequestBlocked {
		t.Fatalf("comments after reject = %#v, want blocked", requests)
	}
	dup, err := svc.ResolveWorkflowHumanGate(ctx, root.ID, run.TaskID, "gate", "reject", "c1")
	if err != nil || dup.Status != NodeRunCancelled {
		t.Fatalf("duplicate reject = %#v err=%v, want idempotent cancelled", dup, err)
	}
}

func TestWorkflowHumanGateInvalidDecisionKeepsWaiting(t *testing.T) {
	ctx := context.Background()
	store, svc, run, root := newWorkflowCommentFixture(t, "wf-gate-bad-task", "wf-gate-bad-run", humanGateGraph())
	if err := svc.AdvanceWorkflowRun(ctx, store, run.TaskID); err != nil {
		t.Fatalf("AdvanceWorkflowRun: %v", err)
	}
	_, err := svc.ResolveWorkflowHumanGate(ctx, root.ID, run.TaskID, "gate", "maybe", "")
	if err == nil || !strings.Contains(err.Error(), "invalid human gate decision") {
		t.Fatalf("invalid decision err = %v, want explicit invalid error", err)
	}
	gate, err := store.WorkflowNodeRunByKey(ctx, run.ID, "gate", "", 0, 1)
	if err != nil || gate.Status != NodeRunWaitingUser {
		t.Fatalf("gate after invalid decision = %#v err=%v, want still waiting_user", gate, err)
	}
}
