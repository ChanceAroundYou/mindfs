package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"mindfs/server/internal/kanban"
)

// GET /api/tasks/{id}/completion 是完成证据清单的读口：没有记录时 completion 为
// null，有记录时把报告条目、补做预算与复核结论一起交出去。
func TestTaskCompletionRoute(t *testing.T) {
	handler, _, root, taskID := kanbanHTTPFixture(t)
	store, err := kanban.NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	// ① 没有记录：completion 必须是 null，而不是一个空对象冒充「有记录」。
	code, body := getJSON(t, handler, "/api/tasks/"+taskID+"/completion?root_id="+root.ID)
	if code != http.StatusOK {
		t.Fatalf("empty status=%d body=%s", code, body)
	}
	var empty struct {
		Completion *map[string]any `json:"completion"`
	}
	if err := json.Unmarshal([]byte(body), &empty); err != nil {
		t.Fatalf("empty body %s: %v", body, err)
	}
	if empty.Completion != nil {
		t.Fatalf("empty completion = %v, want null", *empty.Completion)
	}

	// ② 有记录（缺证据被驳回）：条目按编号给出，复核结论为空。
	report := kanban.CompletionReport{
		Contract: true,
		Items: []kanban.CompletionItemStatus{
			{ID: "build-done", Label: "构建通过", Kind: kanban.CompletionKindNodeDone, Pass: false, Detail: "节点没有产出 build 键"},
			{ID: "all-done", Label: "全部节点收尾", Kind: kanban.CompletionKindAllNodesTerminal, Pass: true},
		},
	}
	raw, _ := json.Marshal(report)
	if err := store.UpsertTaskCompletion(ctx, kanban.TaskCompletion{
		TaskID:     taskID,
		RunID:      "run-1",
		Status:     kanban.CompletionStatusBlocked,
		ReportJSON: string(raw),
		DecisionID: "dec-1",
		RepairsUsed: 1,
		UpdatedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertTaskCompletion: %v", err)
	}
	code, body = getJSON(t, handler, "/api/tasks/"+taskID+"/completion?root_id="+root.ID)
	if code != http.StatusOK {
		t.Fatalf("blocked status=%d body=%s", code, body)
	}
	var blocked struct {
		Completion *struct {
			Present         bool   `json:"present"`
			Status          string `json:"status"`
			Passed          bool   `json:"passed"`
			RepairsUsed     int    `json:"repairs_used"`
			RepairsMax      int    `json:"repairs_max"`
			VerifierVerdict string `json:"verifier_verdict"`
			UpdatedAt       string `json:"updated_at"`
			Items           []struct {
				Index  int    `json:"index"`
				ID     string `json:"id"`
				Label  string `json:"label"`
				Kind   string `json:"kind"`
				Pass   bool   `json:"pass"`
				Detail string `json:"detail"`
			} `json:"items"`
		} `json:"completion"`
	}
	if err := json.Unmarshal([]byte(body), &blocked); err != nil || blocked.Completion == nil {
		t.Fatalf("blocked body %s err=%v", body, err)
	}
	got := blocked.Completion
	if !got.Present || got.Status != kanban.CompletionStatusBlocked || got.Passed {
		t.Fatalf("completion = %+v, want present/blocked/not passed", *got)
	}
	if got.RepairsUsed != 1 || got.RepairsMax != 2 {
		t.Fatalf("repairs = %d/%d, want 1/2 (default max)", got.RepairsUsed, got.RepairsMax)
	}
	if got.VerifierVerdict != "" {
		t.Fatalf("verifier verdict = %q, want empty for a contract failure", got.VerifierVerdict)
	}
	if len(got.Items) != 2 {
		t.Fatalf("items = %+v, want 2", got.Items)
	}
	if got.Items[0].Index != 0 || got.Items[0].ID != "build-done" || got.Items[0].Pass ||
		got.Items[0].Detail == "" || got.Items[1].Index != 1 || !got.Items[1].Pass {
		t.Fatalf("items = %+v, want numbered items with pass flags and detail", got.Items)
	}

	// ③ 复核收尾：verifier_verdict 必须原样交出去（前端据此标注「模型判定」）。
	if err := store.UpsertTaskCompletion(ctx, kanban.TaskCompletion{
		TaskID:          taskID,
		Status:          kanban.CompletionStatusCompleted,
		ReportJSON:      string(raw),
		VerifierKey:     "verify-abc",
		VerifierVerdict: kanban.CompletionVerdictPass,
		DecisionID:      "dec-2",
		UpdatedAt:       time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertTaskCompletion pass: %v", err)
	}
	code, body = getJSON(t, handler, "/api/tasks/"+taskID+"/completion?root_id="+root.ID)
	if code != http.StatusOK {
		t.Fatalf("completed status=%d body=%s", code, body)
	}
	if err := json.Unmarshal([]byte(body), &blocked); err != nil || blocked.Completion == nil {
		t.Fatalf("completed body %s err=%v", body, err)
	}
	if blocked.Completion.Status != kanban.CompletionStatusCompleted || blocked.Completion.VerifierVerdict != kanban.CompletionVerdictPass {
		t.Fatalf("completion = %+v, want completed/pass", *blocked.Completion)
	}

	// ④ 任务不存在 → 404，而不是「没有记录」。
	code, _ = getJSON(t, handler, "/api/tasks/task-nope/completion?root_id="+root.ID)
	if code != http.StatusNotFound {
		t.Fatalf("unknown task status=%d, want 404", code)
	}
}

func getJSON(t *testing.T, handler http.Handler, path string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}
