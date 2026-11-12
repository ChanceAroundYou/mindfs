package kanban

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Task 2 (Phase 1): workflow recommendation.
//
// The plan describes a SQL-backed store and a testify-style test. This
// repository stores workflows in the control-plane file store and its tests use
// the standard library, so the contract is pinned with plain testing and real
// WorkflowStore instances.

type fakeLLMClient struct {
	response string
	err      error
	prompts  []string
}

func (f *fakeLLMClient) Call(_ context.Context, prompt string) (string, error) {
	f.prompts = append(f.prompts, prompt)
	return f.response, f.err
}

// seedRecommenderStore creates three workflows: two auto-select eligible and
// one disabled. ListAvailable sorts eligible workflows by id, so
// "workflow-debug" is the deterministic default.
func seedRecommenderStore(t *testing.T) *WorkflowStore {
	t.Helper()
	ctx := context.Background()
	s := newWorkflowStore(t.TempDir())
	metas := []*WorkflowMeta{
		{
			ID:                "workflow-debug",
			Name:              "调试工作流",
			Description:       "定位和修复代码 bug",
			UseCases:          []string{"代码报错需要定位原因"},
			Tags:              []string{"debug", "fix"},
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
			Description:       "不应参与自动选择",
			AutoSelectEnabled: false,
		},
	}
	for _, meta := range metas {
		if _, err := s.CreateDraft(ctx, meta.ID, "", "tester", wfDef(wfNode("a", NodeKindAgent))); err != nil {
			t.Fatalf("CreateDraft(%s): %v", meta.ID, err)
		}
		if err := s.UpdateMeta(ctx, meta.ID, meta); err != nil {
			t.Fatalf("UpdateMeta(%s): %v", meta.ID, err)
		}
	}
	return s
}

func TestWorkflowRecommender_Recommend(t *testing.T) {
	store := seedRecommenderStore(t)
	client := &fakeLLMClient{response: `{"workflow_id":"workflow-debug","reason":"用户报告登录报错，适合调试工作流","match_score":0.92}`}
	rec, err := NewWorkflowRecommender(store, client).RecommendWorkflow(context.Background(), "登录报错了")
	if err != nil {
		t.Fatalf("RecommendWorkflow: %v", err)
	}
	if rec.WorkflowID != "workflow-debug" {
		t.Fatalf("workflow_id = %q, want workflow-debug", rec.WorkflowID)
	}
	if rec.Reason == "" {
		t.Fatal("reason must not be empty")
	}
	if rec.MatchScore <= 0 {
		t.Fatalf("match_score = %v, want > 0", rec.MatchScore)
	}

	if len(client.prompts) != 1 {
		t.Fatalf("LLM calls = %d, want 1", len(client.prompts))
	}
	prompt := client.prompts[0]
	for _, want := range []string{"登录报错了", "workflow-debug", "定位和修复代码 bug", "workflow-new-feature"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	// Auto-select disabled workflows must not be offered to the model.
	if strings.Contains(prompt, "workflow-hidden") {
		t.Fatalf("prompt leaked auto-select disabled workflow:\n%s", prompt)
	}
}

func TestWorkflowRecommender_RecommendParsesFencedJSON(t *testing.T) {
	store := seedRecommenderStore(t)
	client := &fakeLLMClient{response: "```json\n{\"workflow_id\":\"workflow-new-feature\",\"reason\":\"新增需求\",\"match_score\":0.7}\n```"}
	rec, err := NewWorkflowRecommender(store, client).RecommendWorkflow(context.Background(), "帮我加个登录接口")
	if err != nil {
		t.Fatalf("RecommendWorkflow: %v", err)
	}
	if rec.WorkflowID != "workflow-new-feature" {
		t.Fatalf("workflow_id = %q, want workflow-new-feature", rec.WorkflowID)
	}
}

func TestWorkflowRecommender_FallsBackWhenLLMUnavailable(t *testing.T) {
	store := seedRecommenderStore(t)

	for name, client := range map[string]LLMClient{
		"call error":    &fakeLLMClient{err: errors.New("connection refused")},
		"nil client":    nil,
		"invalid json":  &fakeLLMClient{response: "not json"},
		"unknown wf id": &fakeLLMClient{response: `{"workflow_id":"workflow-ghost","reason":"x","match_score":0.9}`},
	} {
		t.Run(name, func(t *testing.T) {
			rec, err := NewWorkflowRecommender(store, client).RecommendWorkflow(context.Background(), "登录报错了")
			if err != nil {
				t.Fatalf("RecommendWorkflow: %v", err)
			}
			if rec.WorkflowID != "workflow-debug" {
				t.Fatalf("fallback workflow_id = %q, want workflow-debug", rec.WorkflowID)
			}
			if rec.MatchScore <= 0 {
				t.Fatalf("fallback match_score = %v, want > 0", rec.MatchScore)
			}
			if rec.Reason == "" {
				t.Fatal("fallback reason must not be empty")
			}
		})
	}
}

func TestWorkflowRecommender_NoAvailableWorkflows(t *testing.T) {
	store := newWorkflowStore(t.TempDir())
	_, err := NewWorkflowRecommender(store, &fakeLLMClient{response: `{}`}).RecommendWorkflow(context.Background(), "登录报错了")
	if !errors.Is(err, ErrNoAvailableWorkflows) {
		t.Fatalf("error = %v, want ErrNoAvailableWorkflows", err)
	}
}

func TestWorkflowRecommender_RecommendAlias(t *testing.T) {
	store := seedRecommenderStore(t)
	rec, err := NewWorkflowRecommender(store, nil).Recommend(context.Background(), "登录报错了")
	if err != nil {
		t.Fatalf("Recommend: %v", err)
	}
	if rec.WorkflowID != "workflow-debug" {
		t.Fatalf("workflow_id = %q, want workflow-debug", rec.WorkflowID)
	}
}
