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

// D4 at the transport boundary: only /human-gate decides, once, and the route
// maps conflicts to 409. Normal messages cannot approve (service-level test).
func TestTaskHumanGateRouteDecidesOnce(t *testing.T) {
	ctx := context.Background()
	handler, svc, root, taskID := kanbanHTTPFixture(t)
	store, err := kanban.NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	defer store.Close()
	run := &kanban.WorkflowRun{
		ID: "api-gate-run", TaskID: taskID, Revision: 1,
		Graph: kanban.WorkflowDefinition{
			Nodes: []kanban.WorkflowNode{
				{ID: "gate", Kind: kanban.NodeKindHumanGate, Params: map[string]any{"prompt": "确认上线"}},
				{ID: "done", Kind: kanban.NodeKindNotify, Params: map[string]any{"message": "已上线"}},
			},
			Edges: []kanban.WorkflowEdge{{From: "gate", To: "done"}},
		},
	}
	if err := store.CreateWorkflowRunAndSetEngine(ctx, run); err != nil {
		t.Fatalf("CreateWorkflowRunAndSetEngine: %v", err)
	}
	if err := svc.AdvanceWorkflowRun(ctx, store, taskID); err != nil {
		t.Fatalf("AdvanceWorkflowRun: %v", err)
	}

	approve := `{"root_id":"` + root.ID + `","node_id":"gate","decision":"approve","comment_id":"c-1"}`
	code, body := postJSON(t, handler, "/api/tasks/"+taskID+"/human-gate", approve)
	if code != http.StatusOK {
		t.Fatalf("approve status=%d body=%s", code, body)
	}
	var out struct {
		NodeRun struct {
			Status string `json:"status"`
		} `json:"node_run"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil || out.NodeRun.Status != kanban.NodeRunSuccess {
		t.Fatalf("approve response=%s err=%v, want success node_run", body, err)
	}
	if code, body := postJSON(t, handler, "/api/tasks/"+taskID+"/human-gate", approve); code != http.StatusOK {
		t.Fatalf("duplicate approve status=%d body=%s, want idempotent 200", code, body)
	}
	reject := `{"root_id":"` + root.ID + `","node_id":"gate","decision":"reject","comment_id":"c-1"}`
	if code, _ := postJSON(t, handler, "/api/tasks/"+taskID+"/human-gate", reject); code != http.StatusConflict {
		t.Fatalf("opposite decision status=%d, want 409", code)
	}
}

func postJSON(t *testing.T, handler http.Handler, path, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}
