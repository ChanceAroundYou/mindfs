package kanban

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
)

// Task 2 (Phase 1): workflow recommendation.
//
// The recommender turns a free-form user request into one of the auto-select
// eligible workflows returned by WorkflowStore.ListAvailable. It is deliberately
// tolerant: a missing LLM client, a transport error, or a malformed response all
// degrade to a deterministic default (the first available workflow) instead of
// blocking task creation.

var (
	// ErrNoAvailableWorkflows 表示没有任何可自动选择的 workflow。
	ErrNoAvailableWorkflows = errors.New("no auto-select eligible workflows")
	// errInvalidRecommendation 是 LLM 返回内容不可用时的内部错误。
	errInvalidRecommendation = errors.New("invalid workflow recommendation")
)

const (
	defaultMatchScore = 0.5
	defaultReason     = "LLM unavailable, using default"
	invalidReason     = "invalid LLM recommendation, using default"
)

// WorkflowRecommendation is the result of RecommendWorkflow.
type WorkflowRecommendation struct {
	WorkflowID string  `json:"workflow_id"`
	Reason     string  `json:"reason"`
	MatchScore float64 `json:"match_score"`
}

// LLMClient is the minimal language-model surface the recommender needs. Tests
// inject a fake; production leaves it nil until a model client is wired.
type LLMClient interface {
	Call(ctx context.Context, prompt string) (string, error)
}

// WorkflowRecommender selects a workflow for a user request.
type WorkflowRecommender struct {
	store     *WorkflowStore
	llmClient LLMClient
}

// NewWorkflowRecommender builds a recommender. llmClient may be nil, in which
// case recommendations fall back to the first available workflow.
func NewWorkflowRecommender(store *WorkflowStore, llmClient LLMClient) *WorkflowRecommender {
	return &WorkflowRecommender{store: store, llmClient: llmClient}
}

// RecommendWorkflow returns the best workflow for userInput. Failures listing
// workflows are surfaced; every LLM failure degrades to the default workflow.
func (r *WorkflowRecommender) RecommendWorkflow(ctx context.Context, userInput string) (*WorkflowRecommendation, error) {
	if r == nil || r.store == nil {
		return nil, errors.New("workflow recommender: store required")
	}
	workflows, err := r.store.ListAvailable(ctx)
	if err != nil {
		return nil, fmt.Errorf("list available workflows: %w", err)
	}
	if len(workflows) == 0 {
		return nil, ErrNoAvailableWorkflows
	}
	if r.llmClient == nil {
		return defaultRecommendation(workflows, defaultReason), nil
	}
	response, err := r.llmClient.Call(ctx, r.buildPrompt(userInput, workflows))
	if err != nil {
		return defaultRecommendation(workflows, defaultReason), nil
	}
	rec, err := parseWorkflowRecommendation(response, workflows)
	if err != nil {
		return defaultRecommendation(workflows, invalidReason), nil
	}
	return rec, nil
}

// Recommend is the method name used in the implementation plan's sketch. It
// delegates to RecommendWorkflow, the name Phase 1 consumes.
func (r *WorkflowRecommender) Recommend(ctx context.Context, userInput string) (*WorkflowRecommendation, error) {
	return r.RecommendWorkflow(ctx, userInput)
}

func (r *WorkflowRecommender) buildPrompt(userInput string, workflows []*WorkflowMeta) string {
	var b strings.Builder
	b.WriteString("You are selecting the single best workflow for a user request.\n\n")
	b.WriteString("User request:\n<<<\n")
	b.WriteString(strings.TrimSpace(userInput))
	b.WriteString("\n>>>\n\nAvailable workflows:\n")
	for _, wf := range workflows {
		if wf == nil {
			continue
		}
		fmt.Fprintf(&b, "- id: %s\n  name: %s\n", wf.ID, wf.Name)
		if wf.Description != "" {
			fmt.Fprintf(&b, "  description: %s\n", wf.Description)
		}
		if len(wf.UseCases) > 0 {
			fmt.Fprintf(&b, "  use_cases: %s\n", strings.Join(wf.UseCases, "; "))
		}
		if len(wf.Tags) > 0 {
			fmt.Fprintf(&b, "  tags: %s\n", strings.Join(wf.Tags, ", "))
		}
	}
	b.WriteString("\nRespond with ONLY a JSON object, no prose and no code fences:\n")
	b.WriteString(`{"workflow_id":"<one available id>","reason":"<short reason in the user's language>","match_score":<number between 0 and 1>}`)
	return b.String()
}

func parseWorkflowRecommendation(response string, workflows []*WorkflowMeta) (*WorkflowRecommendation, error) {
	raw, err := extractJSONObject(response)
	if err != nil {
		return nil, err
	}
	var rec WorkflowRecommendation
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return nil, fmt.Errorf("%w: %v", errInvalidRecommendation, err)
	}
	rec.WorkflowID = strings.TrimSpace(rec.WorkflowID)
	if rec.WorkflowID == "" {
		return nil, fmt.Errorf("%w: missing workflow_id", errInvalidRecommendation)
	}
	known := false
	for _, wf := range workflows {
		if wf != nil && wf.ID == rec.WorkflowID {
			known = true
			break
		}
	}
	if !known {
		return nil, fmt.Errorf("%w: unknown workflow %q", errInvalidRecommendation, rec.WorkflowID)
	}
	rec.Reason = strings.TrimSpace(rec.Reason)
	if rec.Reason == "" {
		rec.Reason = "LLM selected this workflow"
	}
	if math.IsNaN(rec.MatchScore) || math.IsInf(rec.MatchScore, 0) {
		rec.MatchScore = 0
	} else if rec.MatchScore < 0 {
		rec.MatchScore = 0
	} else if rec.MatchScore > 1 {
		rec.MatchScore = 1
	}
	return &rec, nil
}

// extractJSONObject tolerates code fences and surrounding prose.
func extractJSONObject(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "```") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
		s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "```"))
	}
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start < 0 || end < start {
		return "", fmt.Errorf("%w: no JSON object in response", errInvalidRecommendation)
	}
	return s[start : end+1], nil
}

func defaultRecommendation(workflows []*WorkflowMeta, reason string) *WorkflowRecommendation {
	return &WorkflowRecommendation{
		WorkflowID: workflows[0].ID,
		Reason:     reason,
		MatchScore: defaultMatchScore,
	}
}
