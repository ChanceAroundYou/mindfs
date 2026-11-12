package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"mindfs/server/internal/fs"
	"mindfs/server/internal/kanban"
)

// Task 2 (Phase 1): /api/workflows/available and /api/workflows/recommend.

type fakeWorkflowLLM struct {
	response string
	err      error
}

func (f fakeWorkflowLLM) Call(context.Context, string) (string, error) {
	return f.response, f.err
}

func seedWorkflowMetas(t *testing.T, store *kanban.WorkflowStore) {
	t.Helper()
	ctx := context.Background()
	var graph kanban.WorkflowDefinition
	if err := json.Unmarshal([]byte(wfGraphJSON), &graph); err != nil {
		t.Fatalf("decode wfGraphJSON: %v", err)
	}
	metas := []*kanban.WorkflowMeta{
		{
			ID:                "workflow-debug",
			Name:              "调试工作流",
			Description:       "定位和修复代码 bug",
			UseCases:          []string{"代码报错需要定位原因"},
			Tags:              []string{"debug"},
			AutoSelectEnabled: true,
		},
		{
			ID:                "workflow-new-feature",
			Name:              "新功能开发",
			Description:       "实现新的功能需求",
			UseCases:          []string{"新增页面或接口"},
			Tags:              []string{"feature"},
			AutoSelectEnabled: true,
		},
		{
			ID:                "workflow-hidden",
			Name:              "隐藏流程",
			AutoSelectEnabled: false,
		},
	}
	for _, meta := range metas {
		if _, err := store.CreateDraft(ctx, meta.ID, "", "tester", graph); err != nil {
			t.Fatalf("CreateDraft(%s): %v", meta.ID, err)
		}
		if err := store.UpdateMeta(ctx, meta.ID, meta); err != nil {
			t.Fatalf("UpdateMeta(%s): %v", meta.ID, err)
		}
	}
}

type availableItem struct {
	ID          string  `json:"id"`
	Recommended bool    `json:"recommended"`
	MatchScore  float64 `json:"match_score"`
}

func decodeAvailable(t *testing.T, rec *httptest.ResponseRecorder) []availableItem {
	t.Helper()
	var body struct {
		Workflows []availableItem `json:"workflows"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	return body.Workflows
}

func TestWorkflowAPI_ListAvailable(t *testing.T) {
	h, svc, _ := workflowsHandler(t)
	seedWorkflowMetas(t, svc.Workflows)

	rec := doReq(t, h, http.MethodGet, "/api/workflows/available", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	items := decodeAvailable(t, rec)
	if len(items) != 2 {
		t.Fatalf("workflows = %d, want 2 (auto-select disabled excluded): %+v", len(items), items)
	}
	if items[0].ID != "workflow-debug" || items[1].ID != "workflow-new-feature" {
		t.Fatalf("ids = %q,%q; want sorted eligible workflows", items[0].ID, items[1].ID)
	}
	for _, item := range items {
		if item.Recommended {
			t.Fatalf("workflow %q marked recommended without user_input", item.ID)
		}
	}
}

func TestWorkflowAPI_ListAvailableWithUserInput(t *testing.T) {
	h, svc, _ := workflowsHandler(t)
	seedWorkflowMetas(t, svc.Workflows)

	path := "/api/workflows/available?user_input=" + url.QueryEscape("登录报错了")
	rec := doReq(t, h, http.MethodGet, path, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	items := decodeAvailable(t, rec)
	if len(items) != 2 {
		t.Fatalf("workflows = %d, want 2", len(items))
	}
	// No LLM client: deterministic fallback is the first eligible workflow.
	if !items[0].Recommended || items[0].ID != "workflow-debug" {
		t.Fatalf("item[0] = %+v, want workflow-debug recommended", items[0])
	}
	if items[0].MatchScore <= 0 {
		t.Fatalf("match_score = %v, want > 0", items[0].MatchScore)
	}
	if items[1].Recommended {
		t.Fatalf("item[1] = %+v, want not recommended", items[1])
	}
}

func TestWorkflowAPI_AvailableUsesInjectedLLM(t *testing.T) {
	root := fs.NewRootInfo("root", "root", t.TempDir())
	svc := kanban.NewService(singleRootProviderT{root: root})
	svc.SetWriterGate(kanban.NewWriterGate())
	svc.Workflows = kanban.NewWorkflowStoreAt(t.TempDir())
	seedWorkflowMetas(t, svc.Workflows)
	h := &HTTPHandler{
		AppContext:  &AppContext{Kanban: svc},
		WorkflowLLM: fakeWorkflowLLM{response: `{"workflow_id":"workflow-new-feature","reason":"新增需求","match_score":0.8}`},
	}

	path := "/api/workflows/available?user_input=" + url.QueryEscape("帮我加个登录接口")
	items := decodeAvailable(t, doReq(t, h.Routes(), http.MethodGet, path, ""))
	byID := map[string]availableItem{}
	for _, item := range items {
		byID[item.ID] = item
	}
	if got := byID["workflow-new-feature"]; !got.Recommended || got.MatchScore != 0.8 {
		t.Fatalf("new-feature = %+v, want recommended score 0.8", got)
	}
	if byID["workflow-debug"].Recommended {
		t.Fatalf("debug = %+v, want not recommended", byID["workflow-debug"])
	}
}

func TestWorkflowAPI_Recommend(t *testing.T) {
	h, svc, _ := workflowsHandler(t)
	seedWorkflowMetas(t, svc.Workflows)

	rec := doReq(t, h, http.MethodPost, "/api/workflows/recommend", `{"user_input":"登录报错了"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Recommended *kanban.WorkflowRecommendation `json:"recommended"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	if body.Recommended == nil {
		t.Fatal("recommended is nil")
	}
	if body.Recommended.WorkflowID != "workflow-debug" {
		t.Fatalf("workflow_id = %q, want workflow-debug", body.Recommended.WorkflowID)
	}
	if body.Recommended.Reason == "" || body.Recommended.MatchScore <= 0 {
		t.Fatalf("recommendation = %+v, want reason and positive score", body.Recommended)
	}
}

func TestWorkflowAPI_RecommendValidation(t *testing.T) {
	h, _, _ := workflowsHandler(t)

	rec := doReq(t, h, http.MethodPost, "/api/workflows/recommend", `{"user_input":"   "}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("blank user_input status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}

	rec = doReq(t, h, http.MethodPost, "/api/workflows/recommend", `{"user_input":"登录报错了"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("no workflows status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), kanban.ErrNoAvailableWorkflows.Error()) {
		t.Fatalf("body = %q, want %q", rec.Body.String(), kanban.ErrNoAvailableWorkflows.Error())
	}
}
