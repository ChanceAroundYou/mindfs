package kanban

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"mindfs/server/internal/fs"
)

func patchNodeOp(id, op string, node WorkflowNode) WorkflowPatchOperation {
	return WorkflowPatchOperation{ID: id, Op: op, Node: &node}
}

func patchEdgeOp(id, op string, edge WorkflowEdge) WorkflowPatchOperation {
	return WorkflowPatchOperation{ID: id, Op: op, Edge: &edge}
}

func issueFor(issues []WorkflowPatchIssue, operationID string) (WorkflowPatchIssue, bool) {
	for _, issue := range issues {
		if issue.OperationID == operationID {
			return issue, true
		}
	}
	return WorkflowPatchIssue{}, false
}

func TestValidateWorkflowPatchRejectsUnknownKindAndDanglingEdge(t *testing.T) {
	base := wfDef(wfNode("a", NodeKindAgent))
	before, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	unknown := WorkflowNode{ID: "b", Name: "b", Kind: "bogus"}
	patch := WorkflowPatch{BaseRevision: "r1", Operations: []WorkflowPatchOperation{
		patchNodeOp("op-add-bad", "add_node", unknown),
		patchEdgeOp("op-dangling", "add_edge", WorkflowEdge{From: "a", To: "missing"}),
	}}
	_, issues, err := ValidateWorkflowPatch(base, patch)
	if err == nil {
		t.Fatal("expected invalid patch error")
	}
	var patchErr *WorkflowPatchError
	if !errors.As(err, &patchErr) {
		t.Fatalf("error type = %T, want *WorkflowPatchError", err)
	}
	if len(issues) < 2 {
		t.Fatalf("issues = %+v, want one per bad operation", issues)
	}
	if issue, ok := issueFor(issues, "op-add-bad"); !ok || issue.Field != "node.kind" {
		t.Fatalf("unknown kind issue = %+v, want operation op-add-bad field node.kind", issues)
	}
	if issue, ok := issueFor(issues, "op-dangling"); !ok || issue.Field != "edge.to" {
		t.Fatalf("dangling edge issue = %+v, want operation op-dangling field edge.to", issues)
	}
	after, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("validation mutated base graph:\nbefore=%s\nafter=%s", before, after)
	}
}

func TestWorkflowPatchDiffIsStableAndOrdered(t *testing.T) {
	base := wfDef(wfNode("a", NodeKindAgent))
	b := WorkflowNode{ID: "b", Name: "b", Kind: NodeKindNotify, Params: map[string]any{"message": "done"}}
	updated := b
	updated.Name = "renamed"
	edge := WorkflowEdge{From: "a", To: "b"}
	patch := WorkflowPatch{Operations: []WorkflowPatchOperation{
		patchNodeOp("op1", "add_node", b),
		patchEdgeOp("op2", "add_edge", edge),
		patchNodeOp("op3", "update_future_node", updated),
		{ID: "op4", Op: "remove_future_node", NodeID: "b"},
	}}
	diff1, issues, err := ValidateWorkflowPatch(base, patch)
	if err != nil || len(issues) != 0 {
		t.Fatalf("validation err=%v issues=%+v", err, issues)
	}
	diff2, _, err := ValidateWorkflowPatch(base, patch)
	if err != nil {
		t.Fatal(err)
	}
	one, _ := json.Marshal(diff1)
	two, _ := json.Marshal(diff2)
	if !bytes.Equal(one, two) {
		t.Fatalf("diff is not stable:\n1=%s\n2=%s", one, two)
	}
	wantOrder := []string{"op1", "op2", "op3", "op4", "op4"}
	if len(diff1.Operations) != len(wantOrder) {
		t.Fatalf("diff operations = %+v, want %d entries (node removal also removes its edge)", diff1.Operations, len(wantOrder))
	}
	for i, want := range wantOrder {
		if diff1.Operations[i].OperationID != want {
			t.Fatalf("diff[%d] operation = %q, want %q (full=%+v)", i, diff1.Operations[i].OperationID, want, diff1.Operations)
		}
	}
}

func TestWorkflowPatchEnforcesLimitsAndOperationIdentity(t *testing.T) {
	base := wfDef(wfNode("a", NodeKindAgent))
	tooMany := make([]WorkflowPatchOperation, WorkflowPatchMaxOperations+1)
	for i := range tooMany {
		tooMany[i] = WorkflowPatchOperation{ID: "op", Op: "add_node", Node: &WorkflowNode{ID: "n", Kind: NodeKindNotify}}
	}
	for name, patch := range map[string]WorkflowPatch{
		"too-many-operations":  {Operations: tooMany},
		"missing-operation-id": {Operations: []WorkflowPatchOperation{{Op: "add_node", Node: &WorkflowNode{ID: "b", Kind: NodeKindNotify}}}},
		"unknown-operation":    {Operations: []WorkflowPatchOperation{{ID: "op", Op: "set_json_path"}}},
	} {
		t.Run(name, func(t *testing.T) {
			_, issues, err := ValidateWorkflowPatch(base, patch)
			if err == nil || len(issues) == 0 {
				t.Fatalf("expected invalid patch for %s, issues=%+v", name, issues)
			}
		})
	}

	full := WorkflowDefinition{Nodes: make([]WorkflowNode, 0, WorkflowPatchMaxNodes)}
	for i := 0; i < WorkflowPatchMaxNodes; i++ {
		full.Nodes = append(full.Nodes, WorkflowNode{ID: fmt.Sprintf("n%d", i), Kind: NodeKindNotify})
	}
	_, issues, err := ValidateWorkflowPatch(full, WorkflowPatch{Operations: []WorkflowPatchOperation{
		patchNodeOp("op-over", "add_node", WorkflowNode{ID: "overflow", Kind: NodeKindNotify}),
	}})
	if err == nil || len(issues) == 0 || issues[0].Field != "nodes" {
		t.Fatalf("node-count limit err=%v issues=%+v", err, issues)
	}
}

func TestWorkflowDraftPatchCASAndProposalDoesNotPublish(t *testing.T) {
	store := newWorkflowStore(t.TempDir())
	ctx := context.Background()
	draft, err := store.CreateDraft(ctx, "wf1", "", "agent", wfDef(wfNode("a", NodeKindAgent)))
	if err != nil {
		t.Fatal(err)
	}
	patch := WorkflowPatch{BaseRevision: draft.Revision, Author: "agent", Operations: []WorkflowPatchOperation{
		patchNodeOp("op1", "add_node", WorkflowNode{ID: "b", Kind: NodeKindNotify, Params: map[string]any{"message": "done"}}),
		patchEdgeOp("op2", "add_edge", WorkflowEdge{From: "a", To: "b"}),
	}}
	updated, diff, err := store.ApplyWorkflowDraftPatch(ctx, "wf1", patch)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision == draft.Revision || len(diff.Operations) != 2 {
		t.Fatalf("updated=%+v diff=%+v", updated, diff)
	}
	if got := workflowVersionCount(t, store); got != 0 {
		t.Fatalf("draft patch created %d versions, want 0", got)
	}
	proposal, err := store.ProposeWorkflowPublish(ctx, "wf1", updated.Revision, "agent")
	if err != nil {
		t.Fatal(err)
	}
	if got := workflowVersionCount(t, store); got != 0 {
		t.Fatalf("publish proposal created %d versions, want 0", got)
	}
	// Draft moves on after the proposal was made. Confirming the old proposal
	// must not publish the new revision.
	if _, err := store.UpdateDraft(ctx, "wf1", updated.Revision, "user", wfDef(wfNode("a", NodeKindAgent), wfNode("c", NodeKindNotify))); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ConfirmWorkflowPublishProposal(ctx, proposal.ID, "user"); !errors.Is(err, ErrPublishStale) {
		t.Fatalf("stale proposal confirm err = %v, want ErrPublishStale", err)
	}
	if got := workflowVersionCount(t, store); got != 0 {
		t.Fatalf("stale confirm created %d versions, want 0", got)
	}
	current, err := store.GetDraft(ctx, "wf1")
	if err != nil {
		t.Fatal(err)
	}
	proposal2, err := store.ProposeWorkflowPublish(ctx, "wf1", current.Revision, "agent")
	if err != nil {
		t.Fatal(err)
	}
	version, confirmed, err := store.ConfirmWorkflowPublishProposal(ctx, proposal2.ID, "user")
	if err != nil {
		t.Fatal(err)
	}
	if version.Version == "" || confirmed.Status != PublishProposalPublished {
		t.Fatalf("version=%+v proposal=%+v", version, confirmed)
	}
	if got := workflowVersionCount(t, store); got != 1 {
		t.Fatalf("confirm created %d versions, want 1", got)
	}
}

func TestStalePublishProposalCannotConfirmNewRevision(t *testing.T) {
	store := newWorkflowStore(t.TempDir())
	ctx := context.Background()
	draft, err := store.CreateDraft(ctx, "wf2", "", "agent", wfDef(wfNode("a", NodeKindAgent)))
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := store.ProposeWorkflowPublish(ctx, "wf2", draft.Revision, "agent")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateDraft(ctx, "wf2", draft.Revision, "user", wfDef(wfNode("a", NodeKindAgent), wfNode("b", NodeKindNotify))); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ConfirmWorkflowPublishProposal(ctx, proposal.ID, "user"); !errors.Is(err, ErrPublishStale) {
		t.Fatalf("confirm err = %v, want ErrPublishStale", err)
	}
	stored, err := store.GetPublishProposal(ctx, proposal.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != PublishProposalStale {
		t.Fatalf("proposal status = %q, want %q", stored.Status, PublishProposalStale)
	}
}

func TestWorkflowRunPatchRejectsFrozenEdgeAndStaleRevision(t *testing.T) {
	ctx := context.Background()
	root := fs.NewRootInfo("root", "root", t.TempDir())
	store, err := NewTaskStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.CreateTask(ctx, Task{ID: "task-1", RootID: root.ID, Status: StatusPending, Stages: []StageTemplate{userStage("u")}}, StageRun{ID: "s1", TaskID: "task-1", StageIndex: 0, StageName: "u", Role: RoleUser, Status: StageStatusPending}, TaskEvent{ID: "e1", TaskID: "task-1", Type: "created"}); err != nil {
		t.Fatal(err)
	}
	graph := wfDef(wfNode("a", NodeKindAgent), wfNode("b", NodeKindNotify))
	graph.Edges = []WorkflowEdge{{From: "a", To: "b"}}
	run := &WorkflowRun{ID: "run-1", TaskID: "task-1", Revision: 1, Graph: graph, NodeStates: map[string]string{"a": NodeRunQueued}, NodeOutputs: map[string]map[string]any{}, SelectedRoutes: map[string]string{}, LoopState: map[string]*LoopState{}, LoopExitExpr: map[string]string{}}
	if err := store.CreateWorkflowRunAndSetEngine(ctx, run); err != nil {
		t.Fatal(err)
	}
	patch := WorkflowPatch{BaseRevision: "1", Operations: []WorkflowPatchOperation{
		patchEdgeOp("op-frozen-edge", "remove_future_edge", WorkflowEdge{From: "a", To: "b"}),
	}}
	_, _, err = store.ApplyWorkflowRunPatch(ctx, "task-1", run.ID, run.Revision, patch)
	if err == nil {
		t.Fatal("expected frozen edge rejection")
	}
	var patchErr *WorkflowPatchError
	if !errors.As(err, &patchErr) {
		t.Fatalf("frozen edge error = %T, want *WorkflowPatchError", err)
	}
	if issue, ok := issueFor(patchErr.Issues, "op-frozen-edge"); !ok || issue.Field != "edge" {
		t.Fatalf("frozen edge issue = %+v", patchErr.Issues)
	}
	if _, _, err := store.ApplyWorkflowRunPatch(ctx, "task-1", run.ID, 999, WorkflowPatch{}); !errors.Is(err, ErrRunConflict) {
		t.Fatalf("stale revision err = %v, want ErrRunConflict", err)
	}
	if _, _, err := store.ApplyWorkflowRunPatch(ctx, "another-task", run.ID, run.Revision, WorkflowPatch{}); !errors.Is(err, ErrWorkflowRunNotFound) {
		t.Fatalf("cross-task run patch err = %v, want ErrWorkflowRunNotFound", err)
	}
	// A standalone future node is allowed and is persisted through the run CAS.
	future := WorkflowPatch{Operations: []WorkflowPatchOperation{
		patchNodeOp("op-add-future", "add_node", WorkflowNode{ID: "future", Kind: NodeKindNotify, Params: map[string]any{"message": "later"}}),
	}}
	applied, diff, err := store.ApplyWorkflowRunPatch(ctx, "task-1", run.ID, run.Revision, future)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Operations) != 1 || applied.Revision != run.Revision+1 {
		t.Fatalf("applied=%+v diff=%+v", applied, diff)
	}
	if _, ok := applied.Graph.node("future"); !ok {
		t.Fatal("future node was not persisted")
	}
}

func TestWorkflowDryRunExplainsGraphWithoutSideEffects(t *testing.T) {
	start := WorkflowNode{ID: "start", Kind: NodeKindAgent, Params: map[string]any{"prompt": "research"}}
	cond := WorkflowNode{ID: "cond", Kind: NodeKindCondition, Params: map[string]any{"expression": "start.output.score >= 80", "routes": []any{WorkflowRoutePass, WorkflowRouteFail}}}
	hit := WorkflowNode{ID: "hit", Kind: NodeKindNotify, Params: map[string]any{"message": "good"}}
	miss := WorkflowNode{ID: "miss", Kind: NodeKindNotify, Params: map[string]any{"message": "bad"}}
	join := WorkflowNode{ID: "join", Kind: NodeKindNotify, Params: map[string]any{"message": "joined"}}
	graph := wfDef(start, cond, hit, miss, join)
	graph.Edges = []WorkflowEdge{
		{From: "start", To: "cond"},
		{From: "cond", To: "hit", Route: WorkflowRoutePass},
		{From: "cond", To: "miss", Route: WorkflowRouteFail},
		{From: "hit", To: "join"},
		{From: "miss", To: "join"},
	}
	outputs := map[string]map[string]any{"start": {"score": float64(90)}}
	before, _ := json.Marshal(struct {
		G WorkflowDefinition
		O map[string]map[string]any
	}{graph, outputs})
	result, err := RunWorkflowDryRun(WorkflowDryRunInput{Graph: graph, Outputs: outputs})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != WorkflowDryRunDone {
		t.Fatalf("status = %q, want done; steps=%+v issues=%+v", result.Status, result.Steps, result.Issues)
	}
	if !dryRunHasAction(result.Steps, "miss", WorkflowDryRunSkip) {
		t.Fatalf("unselected branch was not skipped: %+v", result.Steps)
	}
	if !dryRunHasAction(result.Steps, "hit", WorkflowDryRunWouldNotify) || !dryRunHasAction(result.Steps, "join", WorkflowDryRunWouldNotify) {
		t.Fatalf("notify simulation missing: %+v", result.Steps)
	}
	after, _ := json.Marshal(struct {
		G WorkflowDefinition
		O map[string]map[string]any
	}{graph, outputs})
	if !bytes.Equal(before, after) {
		t.Fatal("dry-run mutated its input")
	}

	gate := WorkflowNode{ID: "gate", Kind: NodeKindHumanGate, Params: map[string]any{"prompt": "approve"}}
	gateGraph := wfDef(start, gate)
	gateResult, err := RunWorkflowDryRun(WorkflowDryRunInput{Graph: gateGraph, Outputs: outputs})
	if err != nil {
		t.Fatal(err)
	}
	if gateResult.Status != WorkflowDryRunWaitingUser || !dryRunHasAction(gateResult.Steps, "gate", WorkflowDryRunHumanGate) {
		t.Fatalf("human gate simulation = %+v", gateResult)
	}

	loop := WorkflowNode{ID: "l", Kind: NodeKindLoop, Params: map[string]any{
		"max_iterations": 2, "exit_expression": "never.output.done == true",
		"graph": wfDef(wfNode("inside", NodeKindNotify)),
	}}
	loopResult, err := RunWorkflowDryRun(WorkflowDryRunInput{
		Graph:     wfDef(loop),
		LoopState: map[string]*LoopState{"l": {Iteration: 2, MaxIterations: 2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if loopResult.Status != WorkflowDryRunWaitingUser || !dryRunHasAction(loopResult.Steps, "l", WorkflowDryRunLoopBudget) {
		t.Fatalf("loop budget simulation = %+v", loopResult)
	}
}

func TestWorkflowDryRunAgentWithoutOutputStopsWithoutRunning(t *testing.T) {
	graph := wfDef(wfNode("agent", NodeKindAgent))
	result, err := RunWorkflowDryRun(WorkflowDryRunInput{Graph: graph})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != WorkflowDryRunWaitingUser || !dryRunHasAction(result.Steps, "agent", WorkflowDryRunWouldRunAgent) {
		t.Fatalf("result = %+v, want waiting_user at agent without output", result)
	}
}

func dryRunHasAction(steps []WorkflowDryRunStep, nodeID, action string) bool {
	for _, step := range steps {
		if step.NodeID == nodeID && step.Action == action {
			return true
		}
	}
	return false
}

func workflowVersionCount(t *testing.T, store *WorkflowStore) int {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	f, err := store.loadLocked()
	if err != nil {
		t.Fatal(err)
	}
	return len(f.Versions)
}
