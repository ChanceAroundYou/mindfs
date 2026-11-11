package kanban

import (
	"context"
	"fmt"
	"testing"
)

func TestDuplicatePublishProposalAndRepublishStayImmutable(t *testing.T) {
	store := newWorkflowStore(t.TempDir())
	ctx := context.Background()
	draft, err := store.CreateDraft(ctx, "wf-dup", "", "agent", wfDef(wfNode("a", NodeKindAgent)))
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.ProposeWorkflowPublish(ctx, "wf-dup", draft.Revision, "agent")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.ProposeWorkflowPublish(ctx, "wf-dup", draft.Revision, "agent-2")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("duplicate proposals at the same revision: %s != %s", first.ID, second.ID)
	}
	other, err := store.CreateDraft(ctx, "wf-other", "", "agent", wfDef(wfNode("x", NodeKindAgent)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ProposeWorkflowPublish(ctx, "wf-other", other.Revision, "agent"); err != nil {
		t.Fatal(err)
	}
	list, err := store.ListPublishProposals(ctx, "wf-dup")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != first.ID {
		t.Fatalf("workflow-scoped proposals = %+v, want only %s", list, first.ID)
	}

	published, _, err := store.ConfirmWorkflowPublishProposal(ctx, first.ID, "alice")
	if err != nil {
		t.Fatal(err)
	}
	again, _, err := store.ConfirmWorkflowPublishProposal(ctx, second.ID, "bob")
	if err != nil {
		t.Fatal(err)
	}
	if again.Version != published.Version || again.PublishedBy != "alice" {
		t.Fatalf("repeat confirmation overwrote the version: %+v", again)
	}
	// Publishing the same draft revision directly must be idempotent too.
	republished, err := store.PublishDraft(ctx, "wf-dup", draft.Revision, "carol")
	if err != nil {
		t.Fatal(err)
	}
	if republished.PublishedBy != "alice" || !republished.PublishedAt.Equal(published.PublishedAt) {
		t.Fatalf("re-publish overwrote immutable version: %+v vs %+v", republished, published)
	}
	if got := workflowVersionCount(t, store); got != 1 {
		t.Fatalf("versions = %d, want 1", got)
	}
}

func TestWorkflowDryRunHonorsPersistedRunState(t *testing.T) {
	graph := wfDef(wfNode("a", NodeKindAgent), wfNode("b", NodeKindAgent))
	graph.Edges = []WorkflowEdge{{From: "a", To: "b"}}
	states := map[string]string{"a": NodeRunSuccess}
	routes := map[string]string{}
	statesBefore := fmt.Sprintf("%v", states)
	result, err := RunWorkflowDryRun(WorkflowDryRunInput{
		Graph:      graph,
		Outputs:    map[string]map[string]any{"a": {"done": true}},
		NodeStates: states,
	})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%v", states) != statesBefore {
		t.Fatalf("dry-run mutated input states: %+v", states)
	}
	if dryRunHasAction(result.Steps, "a", WorkflowDryRunWouldRunAgent) {
		t.Fatalf("completed node was replayed: %+v", result.Steps)
	}
	if result.Status != WorkflowDryRunWaitingUser || !dryRunHasAction(result.Steps, "b", WorkflowDryRunWouldRunAgent) {
		t.Fatalf("result = %+v, want waiting_user at pending node b", result)
	}

	// A completed Agent can legitimately have no output. Its persisted success
	// state alone must keep the dry-run from replaying it.
	noOutputGraph := wfDef(wfNode("done", NodeKindAgent), wfNode("next", NodeKindAgent))
	noOutputGraph.Edges = []WorkflowEdge{{From: "done", To: "next"}}
	noOutput, err := RunWorkflowDryRun(WorkflowDryRunInput{
		Graph:      noOutputGraph,
		NodeStates: map[string]string{"done": NodeRunSuccess},
	})
	if err != nil {
		t.Fatal(err)
	}
	if dryRunHasAction(noOutput.Steps, "done", WorkflowDryRunWouldRunAgent) {
		t.Fatalf("completed node without output was replayed: %+v", noOutput.Steps)
	}
	if noOutput.Status != WorkflowDryRunWaitingUser || !dryRunHasAction(noOutput.Steps, "next", WorkflowDryRunWouldRunAgent) {
		t.Fatalf("no-output result = %+v, want waiting_user at next", noOutput)
	}

	cond := WorkflowNode{ID: "cond", Kind: NodeKindCondition, Params: map[string]any{"expression": "unused", "routes": []any{WorkflowRoutePass, WorkflowRouteFail}}}
	hit := wfNode("hit", NodeKindNotify)
	miss := wfNode("miss", NodeKindNotify)
	condGraph := wfDef(cond, hit, miss)
	condGraph.Edges = []WorkflowEdge{
		{From: "cond", To: "hit", Route: WorkflowRoutePass},
		{From: "cond", To: "miss", Route: WorkflowRouteFail},
	}
	routes = map[string]string{"cond": WorkflowRoutePass}
	routesBefore := fmt.Sprintf("%v", routes)
	routeResult, err := RunWorkflowDryRun(WorkflowDryRunInput{
		Graph:          condGraph,
		NodeStates:     map[string]string{"cond": NodeRunSuccess},
		SelectedRoutes: routes,
	})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%v", routes) != routesBefore {
		t.Fatalf("dry-run mutated selected routes: %+v", routes)
	}
	if !dryRunHasAction(routeResult.Steps, "miss", WorkflowDryRunSkip) {
		t.Fatalf("persisted condition route did not skip the other branch: %+v", routeResult.Steps)
	}
}

func TestWorkflowDryRunReportsNodeScopedIssues(t *testing.T) {
	cond := WorkflowNode{ID: "cond", Kind: NodeKindCondition, Params: map[string]any{"expression": "missing.output.score > 1", "routes": []any{WorkflowRoutePass, WorkflowRouteFail}}}
	result, err := RunWorkflowDryRun(WorkflowDryRunInput{Graph: wfDef(cond)})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != WorkflowDryRunWaitingUser || len(result.Issues) != 1 {
		t.Fatalf("result = %+v, want one waiting_user issue", result)
	}
	if issue := result.Issues[0]; issue.NodeID != "cond" || issue.OperationID != "" {
		t.Fatalf("condition issue = %+v, want node-scoped issue", issue)
	}
}
