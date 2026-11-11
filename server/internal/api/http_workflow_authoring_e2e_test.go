package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// End-to-end proof for the approved authoring flow: a fake Agent proposes a
// typed patch, the reviewer validates a subset, applies exactly once through
// the existing CAS endpoint, reopens the saved draft, then publishes only
// through a separately confirmed proposal. No step may persist before the
// user's apply or publish confirmation.
func TestWorkflowAgentAuthoringReviewApplyPublishE2E(t *testing.T) {
	const output = `{
		"summary": "Add completion notify",
		"rationale": "announce the end of the run",
		"base_revision": "r1",
		"operations": [
			{"id": "op-node", "op": "add_node", "node": {"id": "b", "kind": "notify", "params": {"message": "done"}}},
			{"id": "op-edge", "op": "add_edge", "edge": {"from": "a", "to": "b"}}
		]
	}`
	h, _, _ := authoringHandler(t, func(workflowAuthoringRequest) (string, error) { return output, nil })
	if rec := doReq(t, h, http.MethodPost, "/api/workflows", `{"workflow_id":"e2e-wf","author":"user","graph":`+wfGraphJSON+`}`); rec.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec := doReq(t, h, http.MethodPost, "/api/workflows/e2e-wf/authoring/proposals",
		`{"base_revision":"r1","instruction":"add a completion notify","agent":"claude"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("generate status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	proposal := body["proposal"].(map[string]any)
	ops := proposal["operations"].([]any)
	if len(ops) != 2 {
		t.Fatalf("operations = %+v", ops)
	}
	if body["dry_run"] == nil {
		t.Fatalf("generation must dry-run: %s", rec.Body.String())
	}

	// Partial acceptance: the node alone is a valid graph (the edge is not part
	// of the subset), and the server re-runs validate + dry-run for exactly that
	// subset before any apply.
	nodeOnly := patchBody(t, "r1", ops[0])
	rec = doReq(t, h, http.MethodPost, "/api/workflows/e2e-wf/patch/validate", nodeOnly)
	if rec.Code != http.StatusOK || decodeBody(t, rec)["dry_run"] == nil {
		t.Fatalf("subset validate status=%d body=%s", rec.Code, rec.Body.String())
	}
	// An edge whose new endpoint is not selected must be rejected, not applied
	// as a dangling reference.
	edgeOnly := patchBody(t, "r1", ops[1])
	rec = doReq(t, h, http.MethodPost, "/api/workflows/e2e-wf/patch/validate", edgeOnly)
	if rec.Code != http.StatusUnprocessableEntity || len(decodeBody(t, rec)["issues"].([]any)) == 0 {
		t.Fatalf("dangling subset status=%d body=%s", rec.Code, rec.Body.String())
	}
	// The full selected subset validates and keeps revision r1.
	full := patchBody(t, "r1", ops...)
	rec = doReq(t, h, http.MethodPost, "/api/workflows/e2e-wf/patch/validate", full)
	if rec.Code != http.StatusOK {
		t.Fatalf("full validate status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec = doReq(t, h, http.MethodGet, "/api/workflows/e2e-wf/snapshot", ""); decodeBody(t, rec)["draft"].(map[string]any)["revision"] != "r1" {
		t.Fatalf("validate advanced the draft: %s", rec.Body.String())
	}

	rec = doReq(t, h, http.MethodPost, "/api/workflows/e2e-wf/patch/apply", full)
	if rec.Code != http.StatusOK {
		t.Fatalf("apply status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodGet, "/api/workflows/e2e-wf/snapshot", "")
	draft := decodeBody(t, rec)["draft"].(map[string]any)
	if draft["revision"] != "r2" || len(draft["graph"].(map[string]any)["nodes"].([]any)) != 2 {
		t.Fatalf("apply did not persist the reviewed graph: %s", rec.Body.String())
	}

	// A second apply with the old base is a CAS conflict and changes nothing.
	rec = doReq(t, h, http.MethodPost, "/api/workflows/e2e-wf/patch/apply", full)
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale apply status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := decodeBody(t, rec)["current_revision"]; got != "r2" {
		t.Fatalf("stale apply current_revision = %v, want r2 (%s)", got, rec.Body.String())
	}

	// Publishing is a separate proposal + explicit confirmation.
	rec = doReq(t, h, http.MethodPost, "/api/workflows/e2e-wf/publish-proposals", `{"revision":"r2","proposer":"user"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("propose publish status=%d body=%s", rec.Code, rec.Body.String())
	}
	proposalID := decodeBody(t, rec)["proposal"].(map[string]any)["id"].(string)
	rec = doReq(t, h, http.MethodGet, "/api/workflows/versions/e2e-wf:v2", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("proposal alone published a version: %d %s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodPost, "/api/workflows/publish-proposals/"+proposalID+"/confirm", `{"published_by":"user"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm publish status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodGet, "/api/workflows/versions/e2e-wf:v2", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("confirmed version missing: %d %s", rec.Code, rec.Body.String())
	}
}

func patchBody(t *testing.T, revision string, ops ...any) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"base_revision": revision,
		"author":        "user",
		"operations":    ops,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}