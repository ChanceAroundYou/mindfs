package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mindfs/server/internal/kanban"
)

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode body: %v (%s)", err, rec.Body.String())
	}
	return out
}

func TestWorkflowDraftAgentSnapshotPatchAndProposal(t *testing.T) {
	h, svc, _ := workflowsHandler(t)
	if rec := doReq(t, h, http.MethodPost, "/api/workflows", `{"workflow_id":"agent-wf","author":"agent","graph":`+wfGraphJSON+`}`); rec.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec := doReq(t, h, http.MethodGet, "/api/workflows/agent-wf/snapshot", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("snapshot status=%d body=%s", rec.Code, rec.Body.String())
	}
	snap := decodeBody(t, rec)
	draft := snap["draft"].(map[string]any)
	if draft["revision"] != "r1" {
		t.Fatalf("snapshot revision = %v, want r1", draft["revision"])
	}

	bad := `{"base_revision":"r1","operations":[{"id":"op-bad","op":"add_node","node":{"id":"b","kind":"bogus"}}]}`
	rec = doReq(t, h, http.MethodPost, "/api/workflows/agent-wf/patch/validate", bad)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid validate status=%d body=%s", rec.Code, rec.Body.String())
	}
	issues := decodeBody(t, rec)["issues"].([]any)
	first, _ := issues[0].(map[string]any)
	if first["operation_id"] != "op-bad" || first["field"] != "node.kind" {
		t.Fatalf("issue = %+v, want op-bad/node.kind", first)
	}
	rec = doReq(t, h, http.MethodGet, "/api/workflows/agent-wf/snapshot", "")
	if rev := decodeBody(t, rec)["draft"].(map[string]any)["revision"]; rev != "r1" {
		t.Fatalf("validation advanced draft to %v", rev)
	}

	valid := `{"base_revision":"r1","author":"agent","operations":[
		{"id":"op-add","op":"add_node","node":{"id":"b","kind":"notify","params":{"message":"done"}}},
		{"id":"op-edge","op":"add_edge","edge":{"from":"a","to":"b"}}]}`
	rec = doReq(t, h, http.MethodPost, "/api/workflows/agent-wf/patch/apply", valid)
	if rec.Code != http.StatusOK {
		t.Fatalf("apply status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	if body["draft"].(map[string]any)["revision"] != "r2" {
		t.Fatalf("apply did not advance draft: %v", body["draft"])
	}
	if len(body["diff"].(map[string]any)["operations"].([]any)) != 2 {
		t.Fatalf("diff = %+v", body["diff"])
	}

	rec = doReq(t, h, http.MethodPost, "/api/workflows/agent-wf/publish-proposals", `{"revision":"r2","proposer":"agent"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("propose status=%d body=%s", rec.Code, rec.Body.String())
	}
	proposalID := decodeBody(t, rec)["proposal"].(map[string]any)["id"].(string)
	rec = doReq(t, h, http.MethodGet, "/api/workflows/agent-wf/publish-proposals", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("proposal list status=%d body=%s", rec.Code, rec.Body.String())
	}
	items := decodeBody(t, rec)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != proposalID || items[0].(map[string]any)["status"] != kanban.PublishProposalPending {
		t.Fatalf("proposal list = %+v", items)
	}
	if _, err := svc.Workflows.GetVersion(context.Background(), "agent-wf:v1"); err == nil {
		t.Fatal("proposal published a version without confirmation")
	}
	// Move the draft; the old proposal must not publish the new revision.
	if rec := doReq(t, h, http.MethodPut, "/api/workflows/agent-wf", `{"base_revision":"r2","author":"user","graph":`+wfGraphJSON+`}`); rec.Code != http.StatusOK {
		t.Fatalf("move draft status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodPost, "/api/workflows/publish-proposals/"+proposalID+"/confirm", `{"published_by":"user"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale confirm status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodPost, "/api/workflows/agent-wf/publish-proposals", `{"revision":"r3","proposer":"agent"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("re-propose status=%d body=%s", rec.Code, rec.Body.String())
	}
	proposalID = decodeBody(t, rec)["proposal"].(map[string]any)["id"].(string)
	rec = doReq(t, h, http.MethodPost, "/api/workflows/agent-wf/publish-proposals", `{"revision":"r3","proposer":"agent-2"}`)
	if rec.Code != http.StatusOK || decodeBody(t, rec)["proposal"].(map[string]any)["id"] != proposalID {
		t.Fatalf("duplicate proposal status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodPost, "/api/workflows/publish-proposals/"+proposalID+"/confirm", `{"published_by":"user"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm status=%d body=%s", rec.Code, rec.Body.String())
	}
	version := decodeBody(t, rec)["version"].(map[string]any)["version"].(string)
	if _, err := svc.Workflows.GetVersion(context.Background(), version); err != nil {
		t.Fatalf("confirmed version %q not stored: %v", version, err)
	}
	rec = doReq(t, h, http.MethodGet, "/api/workflows/agent-wf/publish-proposals", "")
	items = decodeBody(t, rec)["items"].([]any)
	statuses := map[string]int{}
	for _, item := range items {
		statuses[item.(map[string]any)["status"].(string)]++
	}
	if len(items) != 2 || statuses[kanban.PublishProposalStale] != 1 || statuses[kanban.PublishProposalPublished] != 1 {
		t.Fatalf("proposal list after confirm = %+v", items)
	}
	rec = doReq(t, h, http.MethodGet, "/api/workflows/missing/publish-proposals", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing workflow proposal list status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestTaskWorkflowAgentPatchAndDryRun(t *testing.T) {
	h, svc, rootID := workflowsHandler(t)
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
	run := &kanban.WorkflowRun{ID: "run-agent", TaskID: detail.Task.ID, Revision: 1, Graph: graph,
		NodeStates:  map[string]string{"a": kanban.NodeRunSuccess},
		NodeOutputs: map[string]map[string]any{"a": {"done": true}}, SelectedRoutes: map[string]string{}, LoopState: map[string]*kanban.LoopState{}, LoopExitExpr: map[string]string{}}
	if err := store.CreateWorkflowRunAndSetEngine(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	base := "/api/tasks/" + detail.Task.ID + "/workflow-run"
	frozen := `{"base_revision":"1","operations":[{"id":"op-frozen","op":"remove_future_edge","edge":{"from":"a","to":"b"}}]}`
	rec := doReq(t, h, http.MethodPost, base+"/patch/apply?root_id="+rootID, frozen)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("frozen edge status=%d body=%s", rec.Code, rec.Body.String())
	}
	future := `{"base_revision":"1","operations":[{"id":"op-future","op":"add_node","node":{"id":"future","kind":"notify","params":{"message":"later"}}}]}`
	rec = doReq(t, h, http.MethodPost, base+"/patch/apply?root_id="+rootID, future)
	if rec.Code != http.StatusOK {
		t.Fatalf("future apply status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rev := decodeBody(t, rec)["run"].(map[string]any)["revision"]; rev != float64(2) {
		t.Fatalf("applied revision = %v, want 2", rev)
	}
	rec = doReq(t, h, http.MethodPost, base+"/patch/validate?root_id="+rootID, future)
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale validate status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = doReq(t, h, http.MethodPost, base+"/dry-run?root_id="+rootID, `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("dry-run status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	if body["status"] != kanban.WorkflowDryRunWaitingUser {
		t.Fatalf("dry-run status = %v body=%s", body["status"], rec.Body.String())
	}
	steps := body["steps"].([]any)
	for _, raw := range steps {
		step := raw.(map[string]any)
		if step["node_id"] == "a" && step["action"] == kanban.WorkflowDryRunWouldRunAgent {
			t.Fatalf("dry-run replayed completed node a: %+v", steps)
		}
	}
	foundPending := false
	for _, raw := range steps {
		step := raw.(map[string]any)
		if step["node_id"] == "b" && step["action"] == kanban.WorkflowDryRunWouldRunAgent {
			foundPending = true
		}
	}
	if !foundPending {
		t.Fatalf("dry-run did not report pending node b: %+v", steps)
	}
	rec = doReq(t, h, http.MethodPost, base+"/dry-run?root_id="+rootID, `{"outputs":{"b":{"ok":true}}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("merged dry-run status=%d body=%s", rec.Code, rec.Body.String())
	}
	merged := decodeBody(t, rec)
	if merged["status"] != kanban.WorkflowDryRunDone {
		t.Fatalf("merged dry-run status = %v body=%s", merged["status"], rec.Body.String())
	}
	for _, raw := range merged["steps"].([]any) {
		if raw.(map[string]any)["action"] == kanban.WorkflowDryRunWouldRunAgent {
			t.Fatalf("merged dry-run replayed a completed node: %+v", merged["steps"])
		}
	}
	reloaded, err := store.WorkflowRunByID(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Revision != 2 || reloaded.NodeStates["a"] != kanban.NodeRunSuccess {
		t.Fatalf("dry-run mutated run: revision=%d states=%+v", reloaded.Revision, reloaded.NodeStates)
	}

	rec = doReq(t, h, http.MethodGet, base+"/suggestion?root_id="+rootID+"&author=agent", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("suggestion status=%d body=%s", rec.Code, rec.Body.String())
	}
	suggestion := decodeBody(t, rec)["suggestion"].(map[string]any)
	if suggestion["published"] != false || suggestion["source_run_id"] != run.ID {
		t.Fatalf("suggestion = %+v, want reviewable unpublished suggestion", suggestion)
	}
}

func TestWorkflowDryRunInvalidGraphIsRejectedWithoutStore(t *testing.T) {
	h, _, _ := workflowsHandler(t)
	rec := doReq(t, h, http.MethodPost, "/api/workflows/dry-run", `{"graph":{"nodes":[{"id":"a","kind":"condition","params":{"routes":["pass","fail"]}}]}}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid dry-run status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestWorkflowAgentNotFoundStatuses(t *testing.T) {
	h, _, _ := workflowsHandler(t)
	rec := doReq(t, h, http.MethodPost, "/api/workflows/missing/patch/apply", `{"base_revision":"r1","operations":[{"id":"op","op":"add_node","node":{"id":"b","kind":"notify"}}]}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing draft apply status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodPost, "/api/workflows/publish-proposals/prop-missing/confirm", `{"published_by":"user"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing proposal status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestWorkflowAgentPatchPayloadAndOperationLimits(t *testing.T) {
	h, _, _ := workflowsHandler(t)
	doReq(t, h, http.MethodPost, "/api/workflows", `{"workflow_id":"limit-wf","graph":`+wfGraphJSON+`}`)
	ops := make([]map[string]any, 0, kanban.WorkflowPatchMaxOperations+1)
	for i := 0; i <= kanban.WorkflowPatchMaxOperations; i++ {
		ops = append(ops, map[string]any{"id": "op", "op": "add_node", "node": map[string]any{"id": "n", "kind": "notify"}})
	}
	b, _ := json.Marshal(map[string]any{"base_revision": "r1", "operations": ops})
	rec := doReq(t, h, http.MethodPost, "/api/workflows/limit-wf/patch/validate", string(b))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("too many operations status=%d body=%s", rec.Code, rec.Body.String())
	}

	padding := strings.Repeat("x", workflowAgentBodyLimit)
	oversized := `{"base_revision":"r1","author":"` + padding + `","operations":[]}`
	rec = doReq(t, h, http.MethodPost, "/api/workflows/limit-wf/patch/validate", oversized)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized payload status=%d body=%s", rec.Code, rec.Body.String())
	}
	// A complete JSON document followed by trailing whitespace is valid for a
	// streaming decoder; the size cap must still reject it.
	trailing := `{"base_revision":"r1","operations":[]}` + strings.Repeat(" ", workflowAgentBodyLimit)
	rec = doReq(t, h, http.MethodPost, "/api/workflows/limit-wf/patch/validate", trailing)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("trailing oversized payload status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestTaskWorkflowRunAgentPatchScope(t *testing.T) {
	h, svc, rootID := workflowsHandler(t)
	detail, err := svc.CreateTask(context.Background(), kanban.CreateTaskInput{RootID: rootID, Stages: []kanban.StageTemplate{{Name: "input", Role: kanban.RoleUser}}, Input: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/tasks/" + detail.Task.ID + "/workflow-run"
	rec := doReq(t, h, http.MethodPost, base+"/patch/apply", `{"base_revision":"1","operations":[]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing root status=%d body=%s", rec.Code, rec.Body.String())
	}
	// The task exists but has no workflow run yet: the run-scoped façade must
	// 404 instead of inventing or attaching a run.
	rec = doReq(t, h, http.MethodPost, base+"/patch/apply?root_id="+rootID, `{"base_revision":"1","operations":[]}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing run status=%d body=%s", rec.Code, rec.Body.String())
	}
}
