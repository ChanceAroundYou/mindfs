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

type singleRootProviderT struct{ root fs.RootInfo }

func (p singleRootProviderT) GetRoot(id string) (fs.RootInfo, error) { return p.root, nil }
func (p singleRootProviderT) ListRoots() []fs.RootInfo               { return []fs.RootInfo{p.root} }

func commentsHandler(t *testing.T) (http.Handler, *kanban.Service, fs.RootInfo, string) {
	t.Helper()
	root := fs.NewRootInfo("root", "root", t.TempDir())
	svc := kanban.NewService(kanban.NewTemplateStoreAt(t.TempDir()), singleRootProviderT{root: root})
	svc.SetWriterGate(kanban.NewWriterGate())
	detail, err := svc.CreateTask(context.Background(), kanban.CreateTaskInput{
		RootID: root.ID,
		Stages: []kanban.StageTemplate{{Name: "u", Role: "user"}},
		Input:  "hi",
	})
	if err != nil {
		t.Fatal(err)
	}
	return (&HTTPHandler{AppContext: &AppContext{Kanban: svc}}).Routes(), svc, root, detail.Task.ID
}

func TestTaskCommentsPostAndGet(t *testing.T) {
	handler, _, root, taskID := commentsHandler(t)
	body := `{"root_id":"` + root.ID + `","comment_id":"c-1","author":"alice","body":"hello","workflow_choice":"empty"}`
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+taskID+"/comments", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Activity struct {
			ID string `json:"id"`
		} `json:"activity"`
		Continue struct {
			Status string `json:"status"`
		} `json:"continue_request"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Activity.ID != "c-1" || out.Continue.Status != "queued" {
		t.Fatalf("bad response %s err=%v", rec.Body.String(), err)
	}
	// 幂等重放
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/api/tasks/"+taskID+"/comments", strings.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("replay status=%d", rec2.Code)
	}
	// GET 列表
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodGet, "/api/tasks/"+taskID+"/comments?root_id="+root.ID, nil)
	handler.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", rec3.Code, rec3.Body.String())
	}
	var list struct {
		Activities []map[string]any `json:"activities"`
	}
	if err := json.Unmarshal(rec3.Body.Bytes(), &list); err != nil || len(list.Activities) != 1 {
		t.Fatalf("bad list %s err=%v", rec3.Body.String(), err)
	}
	// 异体 conflict
	bad := `{"root_id":"` + root.ID + `","comment_id":"c-1","author":"alice","body":"different","workflow_choice":"empty"}`
	rec4 := httptest.NewRecorder()
	req4 := httptest.NewRequest(http.MethodPost, "/api/tasks/"+taskID+"/comments", strings.NewReader(bad))
	req4.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec4, req4)
	if rec4.Code != http.StatusConflict {
		t.Fatalf("want 409 got %d %s", rec4.Code, rec4.Body.String())
	}
}
