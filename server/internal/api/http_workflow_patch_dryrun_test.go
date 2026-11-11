package api

import (
	"context"
	"net/http"
	"testing"

	"mindfs/server/internal/kanban"
)

// Subset review happens before apply: the validate endpoints must return the
// same static dry-run a full proposal gets, so the UI can re-check exactly the
// operations the user accepted without computing a second graph client-side.
func TestWorkflowPatchValidateReturnsServerDryRun(t *testing.T) {
	h, svc, rootID := workflowsHandler(t)
	if rec := doReq(t, h, http.MethodPost, "/api/workflows", `{"workflow_id":"dryrun-wf","author":"agent","graph":`+wfGraphJSON+`}`); rec.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}
	patch := `{"base_revision":"r1","operations":[{"id":"op-add","op":"add_node","node":{"id":"b","kind":"notify","params":{"message":"done"}}},{"id":"op-edge","op":"add_edge","edge":{"from":"a","to":"b"}}]}`
	rec := doReq(t, h, http.MethodPost, "/api/workflows/dryrun-wf/patch/validate", patch)
	if rec.Code != http.StatusOK {
		t.Fatalf("draft validate status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	if _, ok := body["diff"].(map[string]any); !ok {
		t.Fatalf("draft validate lost diff: %s", rec.Body.String())
	}
	dry, ok := body["dry_run"].(map[string]any)
	if !ok {
		t.Fatalf("draft validate missing dry_run: %s", rec.Body.String())
	}
	if dry["status"] == nil || dry["status"] == "" {
		t.Fatalf("draft dry_run status missing: %s", rec.Body.String())
	}
	if _, ok := dry["steps"].([]any); !ok {
		t.Fatalf("draft dry_run steps missing: %s", rec.Body.String())
	}
	if rec := doReq(t, h, http.MethodGet, "/api/workflows/dryrun-wf/snapshot", ""); decodeBody(t, rec)["draft"].(map[string]any)["revision"] != "r1" {
		t.Fatal("validate advanced the draft revision")
	}

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
	run := &kanban.WorkflowRun{ID: "run-dryrun", TaskID: detail.Task.ID, Revision: 1, Graph: graph,
		NodeStates: map[string]string{}, NodeOutputs: map[string]map[string]any{}, SelectedRoutes: map[string]string{}, LoopState: map[string]*kanban.LoopState{}, LoopExitExpr: map[string]string{}}
	if err := store.CreateWorkflowRunAndSetEngine(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	base := "/api/tasks/" + detail.Task.ID + "/workflow-run"
	runPatch := `{"base_revision":"1","operations":[{"id":"op-future","op":"add_node","node":{"id":"future","kind":"notify","params":{"message":"later"}}}]}`
	rec = doReq(t, h, http.MethodPost, base+"/patch/validate?root_id="+rootID, runPatch)
	if rec.Code != http.StatusOK {
		t.Fatalf("run validate status=%d body=%s", rec.Code, rec.Body.String())
	}
	body = decodeBody(t, rec)
	if _, ok := body["dry_run"].(map[string]any); !ok {
		t.Fatalf("run validate missing dry_run: %s", rec.Body.String())
	}
	rec = doReq(t, h, http.MethodGet, base+"?root_id="+rootID, "")
	if rev := decodeBody(t, rec)["run"].(map[string]any)["revision"]; rev != float64(1) {
		t.Fatalf("run validate advanced revision to %v", rev)
	}
}