package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"mindfs/server/internal/fs"
	"mindfs/server/internal/kanban"
)

func authoringHandler(t *testing.T, respond func(workflowAuthoringRequest) (string, error)) (http.Handler, *kanban.Service, string) {
	t.Helper()
	root := fs.NewRootInfo("root", "root", t.TempDir())
	svc := kanban.NewService(singleRootProviderT{root: root})
	svc.SetWriterGate(kanban.NewWriterGate())
	svc.Workflows = kanban.NewWorkflowStoreAt(t.TempDir())
	app := &AppContext{Kanban: svc}
	app.workflowAuthoringOverride = func(_ context.Context, req workflowAuthoringRequest) (string, error) {
		return respond(req)
	}
	return (&HTTPHandler{AppContext: app}).Routes(), svc, root.ID
}

const authoringAddNotify = `{
	"summary": "Add a completion notify",
	"rationale": "The board should announce completion",
	"base_revision": "r1",
	"operations": [
		{"id": "op-node", "op": "add_node", "node": {"id": "b", "kind": "notify", "params": {"message": "done"}}},
		{"id": "op-edge", "op": "add_edge", "edge": {"from": "a", "to": "b"}}
	]
}`

func TestWorkflowAuthoringUsesCurrentSnapshotAndDoesNotWriteBeforeApproval(t *testing.T) {
	var requests []workflowAuthoringRequest
	h, _, _ := authoringHandler(t, func(req workflowAuthoringRequest) (string, error) {
		requests = append(requests, req)
		return authoringAddNotify, nil
	})
	if rec := doReq(t, h, http.MethodPost, "/api/workflows", `{"workflow_id":"author-wf","author":"user","graph":`+wfGraphJSON+`}`); rec.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec := doReq(t, h, http.MethodPost, "/api/workflows/author-wf/authoring/proposals",
		`{"base_revision":"r1","instruction":"add a completion notification","agent":"claude","model":"m1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("generate status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	proposal := body["proposal"].(map[string]any)
	if proposal["summary"] != "Add a completion notify" || proposal["base_revision"] != "r1" {
		t.Fatalf("proposal = %+v", proposal)
	}
	if ops := proposal["operations"].([]any); len(ops) != 2 {
		t.Fatalf("operations = %+v", ops)
	}
	if len(body["diff"].(map[string]any)["operations"].([]any)) != 2 {
		t.Fatalf("diff = %+v", body["diff"])
	}
	if body["dry_run"] == nil {
		t.Fatalf("dry-run missing: %s", rec.Body.String())
	}
	if len(requests) != 1 {
		t.Fatalf("generator calls = %d", len(requests))
	}
	if requests[0].Agent != "claude" || requests[0].Model != "m1" {
		t.Fatalf("generator request = %+v", requests[0])
	}
	for _, want := range []string{"add a completion notification", `"a"`, "r1"} {
		if !strings.Contains(requests[0].Prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, requests[0].Prompt)
		}
	}

	rec = doReq(t, h, http.MethodGet, "/api/workflows/author-wf/snapshot", "")
	if rev := decodeBody(t, rec)["draft"].(map[string]any)["revision"]; rev != "r1" {
		t.Fatalf("generation advanced draft to %v", rev)
	}
	rec = doReq(t, h, http.MethodGet, "/api/workflows/author-wf/publish-proposals", "")
	if items := decodeBody(t, rec)["items"].([]any); len(items) != 0 {
		t.Fatalf("generation created a publish proposal: %+v", items)
	}

	rec = doReq(t, h, http.MethodPost, "/api/workflows/author-wf/authoring/proposals",
		`{"base_revision":"r0","instruction":"add","agent":"claude"}`)
	if rec.Code != http.StatusConflict || decodeBody(t, rec)["current_revision"] != "r1" {
		t.Fatalf("stale request status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(requests) != 1 {
		t.Fatalf("stale request reached the generator: %d calls", len(requests))
	}
}

func TestWorkflowAuthoringRejectsUnsafaeAgentOutputWithoutWriting(t *testing.T) {
	cases := []struct {
		name   string
		output string
		status int
		field  string
	}{
		{"not json", "sure, add a node", http.StatusUnprocessableEntity, "response"},
		{"zero operations", `{"summary":"x","base_revision":"r1","operations":[]}`, http.StatusUnprocessableEntity, "operations"},
		{"invalid node kind", `{"summary":"x","base_revision":"r1","operations":[{"id":"op","op":"add_node","node":{"id":"b","kind":"bogus"}}]}`, http.StatusOK, "node.kind"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _, _ := authoringHandler(t, func(workflowAuthoringRequest) (string, error) { return tc.output, nil })
			doReq(t, h, http.MethodPost, "/api/workflows", `{"workflow_id":"unsafe-wf","author":"user","graph":`+wfGraphJSON+`}`)
			rec := doReq(t, h, http.MethodPost, "/api/workflows/unsafe-wf/authoring/proposals",
				`{"base_revision":"r1","instruction":"add","agent":"claude"}`)
			if rec.Code != tc.status {
				t.Fatalf("status=%d want %d body=%s", rec.Code, tc.status, rec.Body.String())
			}
			body := decodeBody(t, rec)
			issues, _ := body["issues"].([]any)
			if len(issues) == 0 {
				t.Fatalf("issues missing: %s", rec.Body.String())
			}
			first := issues[0].(map[string]any)
			if first["field"] != tc.field {
				t.Fatalf("issue = %+v, want field %q", first, tc.field)
			}
			rec = doReq(t, h, http.MethodGet, "/api/workflows/unsafe-wf/snapshot", "")
			if rev := decodeBody(t, rec)["draft"].(map[string]any)["revision"]; rev != "r1" {
				t.Fatalf("invalid output advanced draft to %v", rev)
			}
		})
	}
}

func TestWorkflowAuthoringRunScopeKeepsFrozenGraphAndRevision(t *testing.T) {
	output := `{"summary":"Add future node","base_revision":"1","operations":[{"id":"op","op":"add_node","node":{"id":"future","kind":"notify","params":{"message":"later"}}}]}`
	h, svc, rootID := authoringHandler(t, func(workflowAuthoringRequest) (string, error) { return output, nil })
	detail, err := svc.CreateTask(context.Background(), kanban.CreateTaskInput{RootID: rootID, Stages: []kanban.StageTemplate{{Name: "input", Role: kanban.RoleUser}}, Input: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	store, err := svc.TaskStoreForRoot(rootID)
	if err != nil {
		t.Fatal(err)
	}
	graph := kanban.WorkflowDefinition{Nodes: []kanban.WorkflowNode{
		{ID: "a", Kind: kanban.NodeKindAgent, Params: map[string]any{"prompt": "do"}},
		{ID: "b", Kind: kanban.NodeKindAgent, Params: map[string]any{"prompt": "next"}},
	}, Edges: []kanban.WorkflowEdge{{From: "a", To: "b"}}}
	run := &kanban.WorkflowRun{ID: "run-author", TaskID: detail.Task.ID, Revision: 1, Graph: graph,
		NodeStates:  map[string]string{"a": kanban.NodeRunSuccess},
		NodeOutputs: map[string]map[string]any{"a": {"done": true}}, SelectedRoutes: map[string]string{}, LoopState: map[string]*kanban.LoopState{}, LoopExitExpr: map[string]string{}}
	if err := store.CreateWorkflowRunAndSetEngine(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	base := "/api/tasks/" + detail.Task.ID + "/workflow-run/authoring/proposals?root_id=" + rootID
	rec := doReq(t, h, http.MethodPost, base, `{"base_revision":"1","instruction":"add a later notify","agent":"claude"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("run generate status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	if body["dry_run"] == nil || len(body["diff"].(map[string]any)["operations"].([]any)) != 1 {
		t.Fatalf("run proposal body = %s", rec.Body.String())
	}
	reloaded, err := store.WorkflowRunByID(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Revision != 1 || len(reloaded.Graph.Nodes) != 2 {
		t.Fatalf("generation mutated run: revision=%d nodes=%d", reloaded.Revision, len(reloaded.Graph.Nodes))
	}

	rec = doReq(t, h, http.MethodPost, base, `{"base_revision":"0","instruction":"add","agent":"claude"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale run request status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestWorkflowAuthoringRejectsOversizedRequest(t *testing.T) {
	h, _, _ := authoringHandler(t, func(workflowAuthoringRequest) (string, error) { return authoringAddNotify, nil })
	doReq(t, h, http.MethodPost, "/api/workflows", `{"workflow_id":"big-wf","author":"user","graph":`+wfGraphJSON+`}`)
	rec := doReq(t, h, http.MethodPost, "/api/workflows/big-wf/authoring/proposals",
		`{"base_revision":"r1","instruction":"`+strings.Repeat("x", workflowAgentBodyLimit)+`","agent":"claude"}`)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status=%d want 413", rec.Code)
	}
}

const createAuthoringOutput = `{
	"summary": "Create a two-step flow",
	"rationale": "Start then notify",
	"base_revision": "r0",
	"operations": [
		{"id": "op-node", "op": "add_node", "node": {"id": "b", "kind": "notify", "params": {"message": "done"}}},
		{"id": "op-edge", "op": "add_edge", "edge": {"from": "start", "to": "b"}}
	]
}`

func TestWorkflowCreateAuthoringProposalDoesNotCreateDraft(t *testing.T) {
	h, svc, _ := authoringHandler(t, func(workflowAuthoringRequest) (string, error) {
		return createAuthoringOutput, nil
	})

	rec := doReq(t, h, http.MethodPost, "/api/workflows/authoring/proposals",
		`{"workflow_id":"new-wf","instruction":"create a flow","agent":"claude"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create proposal status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	proposal := body["proposal"].(map[string]any)
	if proposal["base_revision"] != "r0" || len(proposal["operations"].([]any)) != 2 {
		t.Fatalf("proposal = %+v", proposal)
	}
	if body["dry_run"] == nil || len(body["diff"].(map[string]any)["operations"].([]any)) != 2 {
		t.Fatalf("candidate review missing: %s", rec.Body.String())
	}
	if _, err := svc.Workflows.GetDraft(context.Background(), "new-wf"); !errors.Is(err, kanban.ErrDraftNotFound) {
		t.Fatalf("proposal created a draft: err=%v", err)
	}
}

func TestWorkflowCreateAuthoringValidateDoesNotCreateDraft(t *testing.T) {
	h, svc, _ := authoringHandler(t, func(workflowAuthoringRequest) (string, error) { return "", nil })
	rec := doReq(t, h, http.MethodPost, "/api/workflows/authoring/validate", `{
		"workflow_id":"new-wf",
		"base_revision":"r0",
		"operations":[
			{"id":"op-node","op":"add_node","node":{"id":"b","kind":"notify","params":{"message":"done"}}},
			{"id":"op-edge","op":"add_edge","edge":{"from":"start","to":"b"}}
		]
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("validate status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	if body["dry_run"] == nil || len(body["diff"].(map[string]any)["operations"].([]any)) != 2 {
		t.Fatalf("validate body = %s", rec.Body.String())
	}
	if _, err := svc.Workflows.GetDraft(context.Background(), "new-wf"); !errors.Is(err, kanban.ErrDraftNotFound) {
		t.Fatalf("validate created a draft: err=%v", err)
	}
}

func TestWorkflowCreateAuthoringApplyCreatesDraftAtomically(t *testing.T) {
	h, svc, _ := authoringHandler(t, func(workflowAuthoringRequest) (string, error) { return "", nil })
	body := `{
		"workflow_id":"new-wf",
		"base_revision":"r0",
		"author":"user",
		"operations":[
			{"id":"op-node","op":"add_node","node":{"id":"b","kind":"notify","params":{"message":"done"}}},
			{"id":"op-edge","op":"add_edge","edge":{"from":"start","to":"b"}}
		]
	}`
	rec := doReq(t, h, http.MethodPost, "/api/workflows/authoring/apply", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("apply status=%d body=%s", rec.Code, rec.Body.String())
	}
	payload := decodeBody(t, rec)
	draft := payload["draft"].(map[string]any)
	if draft["revision"] != "r1" || draft["workflow_id"] != "new-wf" {
		t.Fatalf("draft = %+v", draft)
	}
	graph := draft["graph"].(map[string]any)
	nodes := graph["nodes"].([]any)
	edges := graph["edges"].([]any)
	if len(nodes) != 2 || len(edges) != 1 {
		t.Fatalf("graph = %+v", graph)
	}
	stored, err := svc.Workflows.GetDraft(context.Background(), "new-wf")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Revision != "r1" || len(stored.Graph.Nodes) != 2 {
		t.Fatalf("stored = %+v", stored)
	}

	rec = doReq(t, h, http.MethodPost, "/api/workflows/authoring/apply", body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate apply status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestWorkflowCreateAuthoringApplyRejectsInvalidSubset(t *testing.T) {
	h, svc, _ := authoringHandler(t, func(workflowAuthoringRequest) (string, error) { return "", nil })
	rec := doReq(t, h, http.MethodPost, "/api/workflows/authoring/apply", `{
		"workflow_id":"new-wf",
		"base_revision":"r0",
		"operations":[{"id":"op-edge","op":"add_edge","edge":{"from":"start","to":"missing"}}]
	}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid subset status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := svc.Workflows.GetDraft(context.Background(), "new-wf"); !errors.Is(err, kanban.ErrDraftNotFound) {
		t.Fatalf("invalid subset created a draft: err=%v", err)
	}
}
