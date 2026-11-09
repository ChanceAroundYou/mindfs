package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mindfs/server/internal/kanban"
)

func workflowsHandler(t *testing.T) http.Handler {
	t.Helper()
	svc := kanban.NewService(kanban.NewTemplateStoreAt(t.TempDir()), singleRootProviderT{})
	svc.Workflows = kanban.NewWorkflowStoreAt(t.TempDir())
	return (&HTTPHandler{AppContext: &AppContext{Kanban: svc}}).Routes()
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
	h := workflowsHandler(t)
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
		Version struct{ Version string `json:"version"` } `json:"version"`
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

func TestWorkflowRunCreateAndEditCAS(t *testing.T) {
	h := workflowsHandler(t)
	doReq(t, h, http.MethodPost, "/api/workflows", `{"workflow_id":"wf2","author":"agent","graph":`+wfGraphJSON+`}`)
	doReq(t, h, http.MethodPost, "/api/workflows/wf2/publish", `{"confirmed_revision":"r1","published_by":"user"}`)
	rec := doReq(t, h, http.MethodPost, "/api/workflows/runs", `{"task_id":"t1","version":"wf2:v1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("run create status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Run struct{ ID string `json:"id"` } `json:"run"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Run.ID == "" {
		t.Fatalf("bad run response %s", rec.Body.String())
	}
	// edit with wrong revision -> 409
	rec = doReq(t, h, http.MethodPut, "/api/workflows/runs/"+out.Run.ID, `{"revision":999,"graph":`+wfGraphJSON+`}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("edit wrong rev = %d want 409", rec.Code)
	}
	// edit correct
	rec = doReq(t, h, http.MethodPut, "/api/workflows/runs/"+out.Run.ID, `{"revision":1,"graph":`+wfGraphJSON+`}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("edit status=%d body=%s", rec.Code, rec.Body.String())
	}
}
