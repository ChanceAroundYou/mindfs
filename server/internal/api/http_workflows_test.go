package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mindfs/server/internal/fs"
	"mindfs/server/internal/kanban"
)

func workflowsHandler(t *testing.T) (http.Handler, *kanban.Service, string) {
	t.Helper()
	root := fs.NewRootInfo("root", "root", t.TempDir())
	svc := kanban.NewService(kanban.NewTemplateStoreAt(t.TempDir()), singleRootProviderT{root: root})
	svc.Workflows = kanban.NewWorkflowStoreAt(t.TempDir())
	return (&HTTPHandler{AppContext: &AppContext{Kanban: svc}}).Routes(), svc, root.ID
}

const wfGraphJSON = `{"nodes":[{"id":"a","name":"a","kind":"agent","params":{}}]}`

func doReq(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestWorkflowDraftCreateUpdatePublish(t *testing.T) {
	h, _, _ := workflowsHandler(t)
	// create
	rec := doReq(t, h, http.MethodPost, "/api/workflows", `{"workflow_id":"wf1","author":"agent","graph":`+wfGraphJSON+`}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}
	// update with correct base
	rec = doReq(t, h, http.MethodPut, "/api/workflows/wf1", `{"base_revision":"r1","author":"agent","graph":`+wfGraphJSON+`}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", rec.Code, rec.Body.String())
	}
	// stale base -> 409
	rec = doReq(t, h, http.MethodPut, "/api/workflows/wf1", `{"base_revision":"r1","author":"agent","graph":`+wfGraphJSON+`}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale update = %d want 409", rec.Code)
	}
	// publish with stale confirm -> 409
	rec = doReq(t, h, http.MethodPost, "/api/workflows/wf1/publish", `{"confirmed_revision":"r1","published_by":"user"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale publish = %d want 409 body=%s", rec.Code, rec.Body.String())
	}
	// publish with current -> 200
	rec = doReq(t, h, http.MethodPost, "/api/workflows/wf1/publish", `{"confirmed_revision":"r2","published_by":"user"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("publish status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Version struct {
			Version string `json:"version"`
		} `json:"version"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Version.Version != "wf1:v2" {
		t.Fatalf("bad publish response %s err=%v", rec.Body.String(), err)
	}
	// version immutable & fetchable
	rec = doReq(t, h, http.MethodGet, "/api/workflows/versions/wf1:v2", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get version status=%d", rec.Code)
	}
}

func TestWorkflowRunGetRejectsMismatchedRoot(t *testing.T) {
	h, svc, rootID := workflowsHandler(t)
	detail, err := svc.CreateTask(context.Background(), kanban.CreateTaskInput{RootID: rootID, Stages: []kanban.StageTemplate{{Name: "input", Role: kanban.RoleUser}}, Input: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	// The task belongs to rootID; specifying another root must not reveal its run or fall back to a shared store.
	path := "/api/tasks/" + detail.Task.ID + "/workflow-run?root_id=other-root"
	rec := doReq(t, h, http.MethodGet, path, "")
	if rec.Code == http.StatusOK {
		t.Fatalf("mismatched root unexpectedly read task: %s", rec.Body.String())
	}
}

func TestWorkflowRunCreateAndEditCAS(t *testing.T) {
	h, svc, rootID := workflowsHandler(t)
	doReq(t, h, http.MethodPost, "/api/workflows", `{"workflow_id":"wf2","author":"agent","graph":`+wfGraphJSON+`}`)
	doReq(t, h, http.MethodPost, "/api/workflows/wf2/publish", `{"confirmed_revision":"r1","published_by":"user"}`)
	detail, err := svc.CreateTask(context.Background(), kanban.CreateTaskInput{RootID: rootID, Stages: []kanban.StageTemplate{{Name: "input", Role: kanban.RoleUser}}, Input: "hi"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	// Explicit migration must materialize the run on the task node, not in WorkflowStore.
	rec := doReq(t, h, http.MethodPost, "/api/tasks/"+detail.Task.ID+"/migrate-workflow", `{"root_id":"`+rootID+`","version":"wf2:v1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("migrate status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Run struct {
			ID string `json:"id"`
		} `json:"run"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Run.ID == "" {
		t.Fatalf("bad migrate response %s", rec.Body.String())
	}
	// Run GET is task-node scoped; query includes root_id and task_id, so a control-plane
	// WorkflowStore cannot accidentally serve a run from a different data node.
	path := "/api/tasks/" + detail.Task.ID + "/workflow-run?root_id=" + rootID
	rec = doReq(t, h, http.MethodGet, path, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("task-node run get status=%d body=%s", rec.Code, rec.Body.String())
	}
	// Persist one real NodeRun on the task-node DB, then verify the scoped GET returns it.
	store, err := svc.TaskStoreForRoot(rootID)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := store.TaskEngine(context.Background(), detail.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertWorkflowNodeRun(context.Background(), kanban.NodeRun{ID: "nr-1", RunID: engine.WorkflowRunID, NodeID: "a", Attempt: 1, SessionKey: "session-a", Status: kanban.NodeRunRunning}); err != nil {
		t.Fatal(err)
	}
	nodesPath := "/api/tasks/" + detail.Task.ID + "/workflow-run/nodes?root_id=" + rootID
	rec = doReq(t, h, http.MethodGet, nodesPath, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("node runs status=%d body=%s", rec.Code, rec.Body.String())
	}
	var nodeOut struct {
		NodeRuns []kanban.NodeRun `json:"node_runs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &nodeOut); err != nil || len(nodeOut.NodeRuns) != 1 || nodeOut.NodeRuns[0].SessionKey != "session-a" {
		t.Fatalf("bad node runs response %s err=%v", rec.Body.String(), err)
	}
	// Stale graph edit on the task-node snapshot must conflict.
	rec = doReq(t, h, http.MethodPut, path, `{"revision":999,"graph":`+wfGraphJSON+`}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("edit stale task-node run = %d want 409 body=%s", rec.Code, rec.Body.String())
	}
	// Read back on the same task data plane.
	rec = doReq(t, h, http.MethodGet, path, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("task-node run reload status=%d body=%s", rec.Code, rec.Body.String())
	}
	var loaded struct {
		Run struct {
			ID    string `json:"id"`
			Graph struct {
				Nodes []struct {
					ID string `json:"id"`
				} `json:"nodes"`
			} `json:"graph"`
		} `json:"run"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &loaded); err != nil || loaded.Run.ID != out.Run.ID || len(loaded.Run.Graph.Nodes) != 1 {
		t.Fatalf("bad task-node run reload %s err=%v", rec.Body.String(), err)
	}
}
