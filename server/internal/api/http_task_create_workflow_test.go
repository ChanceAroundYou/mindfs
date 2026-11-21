package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mindfs/server/internal/kanban"
)

// 新建任务面板的「选择工作流」走 POST /api/tasks 的 workflow_choice /
// workflow_version_id。校验口径与 POST /api/tasks/{id}/activities 一致：
// 空串＝交给 Router，非法值当场 400，不悄悄退化成默认。
func TestTaskCreateWorkflowChoice(t *testing.T) {
	cases := []struct {
		name string
		body func(rootID string) string
		want int
	}{
		{
			name: "invalid choice rejected",
			body: func(rootID string) string {
				return `{"root_id":"` + rootID + `","input":"hi","start_immediately":false,"workflow_choice":"bogus"}`
			},
			want: http.StatusBadRequest,
		},
		{
			name: "specified without version rejected",
			body: func(rootID string) string {
				return `{"root_id":"` + rootID + `","input":"hi","start_immediately":false,"workflow_choice":"specified"}`
			},
			want: http.StatusBadRequest,
		},
		{
			name: "specified queued with version",
			body: func(rootID string) string {
				return `{"root_id":"` + rootID + `","input":"跑 debug","start_immediately":false,` +
					`"workflow_choice":"specified","workflow_version_id":"workflow-1:v1"}`
			},
			want: http.StatusOK,
		},
		{
			name: "blank choice falls back to router",
			body: func(rootID string) string {
				return `{"root_id":"` + rootID + `","input":"随便","start_immediately":false}`
			},
			want: http.StatusOK,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, svc, rootID, _ := activitiesHandler(t)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/tasks", strings.NewReader(tc.body(rootID)))
			req.Header.Set("Content-Type", "application/json")
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body.String())
			}
			if tc.want != http.StatusOK {
				return
			}
			var detail kanban.TaskDetail
			if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
				t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
			}
			waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = svc.WaitActivityTurnsIdle(waitCtx)
			acts, err := svc.ListTaskActivities(context.Background(), rootID, detail.Task.ID)
			if err != nil {
				t.Fatalf("ListTaskActivities: %v", err)
			}
			var queued *kanban.TaskActivity
			for i := range acts {
				if acts[i].Status == kanban.ActivityStatusQueued {
					queued = acts[i]
				}
			}
			if queued == nil {
				t.Fatalf("no queued first message in %+v", acts)
			}
			wantChoice := kanban.CommentWorkflowChoiceEmpty
			wantVersion := ""
			if strings.Contains(tc.body(rootID), `"specified"`) {
				wantChoice, wantVersion = kanban.CommentWorkflowChoiceSpecified, "workflow-1:v1"
			}
			if queued.WorkflowChoice != wantChoice || queued.WorkflowVersionID != wantVersion {
				t.Fatalf("stored choice=%q version=%q, want %q/%q",
					queued.WorkflowChoice, queued.WorkflowVersionID, wantChoice, wantVersion)
			}
		})
	}
}
