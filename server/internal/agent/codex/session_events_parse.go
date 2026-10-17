package codex

import (
	"encoding/json"
	"log"
	"strings"

	types "mindfs/server/internal/agent/types"

	codexsdk "github.com/fanwenlin/codex-go-sdk/codex"
)

func parseUnknownPlan(raw json.RawMessage) (types.PlanUpdate, bool) {
	var payload struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return types.PlanUpdate{}, false
	}
	content := strings.TrimSpace(payload.Text)
	if content == "" {
		return types.PlanUpdate{}, false
	}
	return types.PlanUpdate{ID: payload.ID, Content: content}, true
}

func parseUnknownContextCompaction(raw json.RawMessage) types.CompactNotice {
	var payload struct {
		ID      string `json:"id"`
		Summary string `json:"summary"`
	}
	_ = json.Unmarshal(raw, &payload)
	return types.CompactNotice{ID: payload.ID, Status: "complete", Summary: payload.Summary}
}

func (s *session) handleRawEvent(event *codexsdk.RawEvent) bool {
	if event == nil {
		return false
	}
	switch normalizeEventType(event.Type) {
	case "thread.status.changed":
		return true
	case "thread.tokenUsage.updated":
		usage, ok := parseContextWindow(event.Raw)
		if !ok {
			return false
		}
		s.mu.Lock()
		s.contextWindow = usage
		s.mu.Unlock()
		return true
	case "item.plan.delta":
		plan, ok := parsePlanDelta(event.Raw)
		if !ok {
			return false
		}
		plan = s.accumulatePlanUpdate(plan)
		s.emit(types.Event{Type: types.EventTypePlanUpdate, SessionID: s.SessionID(), Data: plan})
		return true
	case "turn.plan.updated":
		todo, ok := parseTurnPlanUpdate(event.Raw)
		if !ok {
			return false
		}
		s.emit(types.Event{Type: types.EventTypeTodoUpdate, SessionID: s.SessionID(), Data: todo})
		return true
	}
	return false
}

func (s *session) accumulatePlanUpdate(plan types.PlanUpdate) types.PlanUpdate {
	if !plan.Delta {
		return plan
	}
	id := strings.TrimSpace(plan.ID)
	if id == "" {
		return plan
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.planTextByID == nil {
		s.planTextByID = make(map[string]string)
	}
	next := s.planTextByID[id] + plan.Content
	s.planTextByID[id] = next
	plan.Content = next
	plan.Delta = false
	return plan
}

func normalizeEventType(value string) string {
	return strings.ReplaceAll(strings.TrimSpace(value), "/", ".")
}

func parsePlanDelta(raw json.RawMessage) (types.PlanUpdate, bool) {
	var payload struct {
		ItemID string `json:"itemId"`
		Delta  string `json:"delta"`
		Raw    struct {
			ItemID string `json:"itemId"`
			Delta  string `json:"delta"`
		} `json:"raw"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return types.PlanUpdate{}, false
	}
	itemID := firstNonEmpty(payload.ItemID, payload.Raw.ItemID)
	delta := firstNonEmpty(payload.Delta, payload.Raw.Delta)
	if strings.TrimSpace(itemID) == "" && strings.TrimSpace(delta) == "" {
		return types.PlanUpdate{}, false
	}
	return types.PlanUpdate{ID: itemID, Content: delta, Delta: true}, true
}

func parseTurnPlanUpdate(raw json.RawMessage) (types.TodoUpdate, bool) {
	var payload struct {
		Plan []struct {
			Step   string `json:"step"`
			Status string `json:"status"`
		} `json:"plan"`
		Raw struct {
			Plan []struct {
				Step   string `json:"step"`
				Status string `json:"status"`
			} `json:"plan"`
		} `json:"raw"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return types.TodoUpdate{}, false
	}
	steps := payload.Plan
	if len(steps) == 0 {
		steps = payload.Raw.Plan
	}
	if len(steps) == 0 {
		return types.TodoUpdate{}, false
	}
	items := make([]types.TodoItem, 0, len(steps))
	for _, step := range steps {
		content := strings.TrimSpace(step.Step)
		if content == "" {
			continue
		}
		items = append(items, types.TodoItem{Content: content, Status: normalizeTodoStatus(step.Status)})
	}
	return types.TodoUpdate{Items: items}, len(items) > 0
}

func normalizeTodoStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "complete", "done":
		return "completed"
	case "inprogress", "in_progress", "running":
		return "in_progress"
	default:
		return "pending"
	}
}

func parseContextWindow(raw json.RawMessage) (types.ContextWindow, bool) {
	var payload struct {
		TokenUsage struct {
			Last struct {
				TotalTokens int `json:"totalTokens"`
			} `json:"last"`
			Total struct {
				TotalTokens int `json:"totalTokens"`
			} `json:"total"`
			ModelContextWindow int `json:"modelContextWindow"`
		} `json:"tokenUsage"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return types.ContextWindow{}, false
	}
	totalTokens := payload.TokenUsage.Last.TotalTokens
	if totalTokens == 0 {
		totalTokens = payload.TokenUsage.Total.TotalTokens
	}
	if totalTokens == 0 && payload.TokenUsage.ModelContextWindow == 0 {
		return types.ContextWindow{}, false
	}
	return types.ContextWindow{
		TotalTokens:        totalTokens,
		ModelContextWindow: payload.TokenUsage.ModelContextWindow,
	}, true
}

func codexTokenUsage(usage codexsdk.Usage) *types.TokenUsage {
	inputTokens := max(0, usage.InputTokens)
	outputTokens := max(0, usage.OutputTokens)
	cacheReadTokens := max(0, usage.CachedInputTokens)
	if inputTokens == 0 && outputTokens == 0 && cacheReadTokens == 0 {
		return nil
	}
	inputTokens = max(inputTokens, cacheReadTokens)
	return &types.TokenUsage{
		InputTokens:     inputTokens,
		OutputTokens:    outputTokens,
		CacheReadTokens: &cacheReadTokens,
	}
}

func messageDelta(prev, next string) string {
	if next == "" {
		return ""
	}
	if prev == "" {
		return next
	}
	if strings.HasPrefix(next, prev) {
		return next[len(prev):]
	}
	return next
}

func normalizeStatus(status string, started bool) string {
	s := strings.ToLower(strings.TrimSpace(status))
	switch s {
	case "inprogress", "in_progress", "running", "pending":
		return "running"
	case "completed", "complete", "success":
		return "complete"
	case "failed", "error", "declined":
		return "failed"
	case "":
		if started {
			return "running"
		}
		return "complete"
	default:
		return s
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func preview(content string) string {
	trimmed := strings.TrimSpace(content)
	if len(trimmed) <= 300 {
		return trimmed
	}
	return trimmed[:300] + "...(truncated)"
}

func logUnhandledEvent(sessionKey, scope string, raw []byte) {
	const maxRawLogBytes = 1024
	if len(raw) > maxRawLogBytes {
		raw = append(raw[:maxRawLogBytes], []byte("...(truncated)")...)
	}
	log.Printf("[agent/codex] unhandled.%s raw=%s", scope, string(raw))
}
