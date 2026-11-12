package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"mindfs/server/internal/fs"
	"mindfs/server/internal/kanban"
)

// Task 6 (Phase 2): Activity HTTP API.
//
// The plan's handler sketch holds one ActivityService/ActivityStore, but this
// repository is multi-account and every task store is per-root. The tests build
// the real per-root wiring (kanban.Service -> TaskStore -> ActivityService) and
// exercise the HTTP surface through Routes(), including the root_id scoping that
// the real frontend sends.
func activitiesHandler(t *testing.T) (http.Handler, *kanban.Service, string, string) {
	t.Helper()
	ctx := context.Background()
	root := fs.NewRootInfo("root", "root", t.TempDir())
	svc := kanban.NewService(singleRootProviderT{root: root})
	svc.SetWriterGate(kanban.NewWriterGate())
	svc.Workflows = kanban.NewWorkflowStoreAt(t.TempDir())

	// One published workflow so workflow_choice=empty has a real target; the
	// notifier node needs no params and keeps the fixture minimal.
	graph := kanban.WorkflowDefinition{Nodes: []kanban.WorkflowNode{
		{ID: "done", Name: "done", Kind: kanban.NodeKindNotify},
	}}
	if _, err := svc.Workflows.CreateDraft(ctx, "workflow-1", "", "tester", graph); err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	if _, err := svc.Workflows.PublishDraft(ctx, "workflow-1", "r1", "tester"); err != nil {
		t.Fatalf("PublishDraft: %v", err)
	}

	detail, err := svc.CreateTask(ctx, kanban.CreateTaskInput{
		RootID: root.ID,
		Stages: []kanban.StageTemplate{{Name: "input", Role: kanban.RoleUser}},
		Input:  "hi",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	h := (&HTTPHandler{AppContext: &AppContext{Kanban: svc}}).Routes()
	return h, svc, root.ID, detail.Task.ID
}

func TestActivityAPI_Create(t *testing.T) {
	h, _, rootID, taskID := activitiesHandler(t)

	body := `{"root_id":"` + rootID + `","body":"请修复 bug","workflow_choice":"empty"}`
	rec := doReq(t, h, http.MethodPost, "/api/tasks/"+taskID+"/activities", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}

	out := decodeBody(t, rec)
	user, ok := out["user_activity"].(map[string]any)
	if !ok || user == nil {
		t.Fatalf("user_activity missing: %s", rec.Body.String())
	}
	agent, ok := out["agent_activity"].(map[string]any)
	if !ok || agent == nil {
		t.Fatalf("agent_activity missing: %s", rec.Body.String())
	}
	if user["type"] != kanban.ActivityTypeUserMessage || user["body"] != "请修复 bug" {
		t.Fatalf("user activity = %+v", user)
	}
	if agent["type"] != kanban.ActivityTypeAgentReply {
		t.Fatalf("agent activity = %+v", agent)
	}
	if agent["run_id"] == "" || agent["run_id"] == nil {
		t.Fatalf("agent activity not linked to a run: %+v", agent)
	}
}

// Review Focus #1: 空留言必须 400，且在校验 root/task 之前就拒绝 —— 表单空提交
// 不该因为别处参数缺失而变成另一种错误。
func TestActivityAPI_Create_EmptyBody(t *testing.T) {
	h, _, rootID, taskID := activitiesHandler(t)

	cases := []struct {
		name string
		body string
		want string
	}{
		{"empty", `{"body":"","workflow_choice":"empty"}`, "body cannot be empty"},
		{"whitespace", `{"root_id":"` + rootID + `","body":"   ","workflow_choice":"empty"}`, "body cannot be empty"},
		{"invalid choice", `{"body":"hi","workflow_choice":"bogus"}`, "invalid workflow_choice"},
		{"missing root", `{"body":"hi","workflow_choice":"empty"}`, "root_id required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doReq(t, h, http.MethodPost, "/api/tasks/"+taskID+"/activities", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s, want 400", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("body=%s, want %q", rec.Body.String(), tc.want)
			}
		})
	}
}

func TestActivityAPI_List(t *testing.T) {
	h, _, rootID, taskID := activitiesHandler(t)

	body := `{"root_id":"` + rootID + `","body":"hello","workflow_choice":"empty"}`
	if rec := doReq(t, h, http.MethodPost, "/api/tasks/"+taskID+"/activities", body); rec.Code != http.StatusOK {
		t.Fatalf("seed create status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec := doReq(t, h, http.MethodGet, "/api/tasks/"+taskID+"/activities?root="+rootID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", rec.Code, rec.Body.String())
	}
	out := decodeBody(t, rec)
	items, ok := out["activities"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("activities = %v, want 2", out["activities"])
	}
	first, _ := items[0].(map[string]any)
	if first["body"] != "hello" {
		t.Fatalf("first activity = %+v", first)
	}
	// root_id alias must work too (comments endpoint convention).
	rec = doReq(t, h, http.MethodGet, "/api/tasks/"+taskID+"/activities?root_id="+rootID, "")
	if rec.Code != http.StatusOK || len(decodeBody(t, rec)["activities"].([]any)) != 2 {
		t.Fatalf("root_id alias status=%d body=%s", rec.Code, rec.Body.String())
	}
	// Without a root the activities cannot be scoped to a task store.
	rec = doReq(t, h, http.MethodGet, "/api/tasks/"+taskID+"/activities", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing root status=%d body=%s, want 400", rec.Code, rec.Body.String())
	}
}

func TestActivityAPI_Get(t *testing.T) {
	h, _, rootID, taskID := activitiesHandler(t)

	body := `{"root_id":"` + rootID + `","body":"detail","workflow_choice":"empty"}`
	rec := doReq(t, h, http.MethodPost, "/api/tasks/"+taskID+"/activities", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("seed create status=%d body=%s", rec.Code, rec.Body.String())
	}
	user := decodeBody(t, rec)["user_activity"].(map[string]any)
	activityID := user["id"].(string)

	rec = doReq(t, h, http.MethodGet, "/api/tasks/"+taskID+"/activities/"+activityID+"?root_id="+rootID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", rec.Code, rec.Body.String())
	}
	got := decodeBody(t, rec)
	if got["id"] != activityID || got["body"] != "detail" {
		t.Fatalf("activity = %+v", got)
	}

	rec = doReq(t, h, http.MethodGet, "/api/tasks/"+taskID+"/activities/missing?root_id="+rootID, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown activity status=%d body=%s, want 404", rec.Code, rec.Body.String())
	}
}
