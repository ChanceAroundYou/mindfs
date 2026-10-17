package session

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
)

func (m *Manager) GetFullToolCall(_ context.Context, key, callID string) (*agenttypes.ToolCall, error) {
	if strings.TrimSpace(key) == "" {
		return nil, errors.New("session key required")
	}
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return nil, errors.New("tool call id required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if found, ok := m.pendingFullToolCallUnsafe(key, callID); ok {
		return found, nil
	}
	aux, err := m.loadExchangeAuxEntries(key, 0)
	if err != nil {
		return nil, err
	}
	var found *agenttypes.ToolCall
	for _, item := range aux {
		if item.ToolCall == nil || strings.TrimSpace(item.ToolCall.CallID) != callID {
			continue
		}
		next := *item.ToolCall
		if found == nil {
			found = &next
			continue
		}
		merged := mergeToolCall(*found, next)
		found = &merged
	}
	if found == nil {
		return nil, os.ErrNotExist
	}
	return found, nil
}

func (m *Manager) UpsertPendingExchangeAux(_ context.Context, sessionKey string, aux ExchangeAux) error {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return errors.New("session key required")
	}
	if aux.ToolCall == nil || strings.TrimSpace(aux.ToolCall.CallID) == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pendingToolCalls == nil {
		m.pendingToolCalls = make(map[string]map[string]agenttypes.ToolCall)
	}
	callID := strings.TrimSpace(aux.ToolCall.CallID)
	next := cloneToolCall(*aux.ToolCall)
	byCallID := m.pendingToolCalls[sessionKey]
	if byCallID == nil {
		byCallID = make(map[string]agenttypes.ToolCall)
		m.pendingToolCalls[sessionKey] = byCallID
	}
	if existing, ok := byCallID[callID]; ok {
		next = mergeToolCall(existing, next)
	}
	byCallID[callID] = next
	return nil
}

func (m *Manager) MarkPendingAskUserAnswered(_ context.Context, sessionKey, callID string, answers map[string]string, answeredAt time.Time) error {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return errors.New("session key required")
	}
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return errors.New("tool call id required")
	}
	cleanAnswers := make(map[string]string, len(answers))
	for key, value := range answers {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			cleanAnswers[key] = value
		}
	}
	if len(cleanAnswers) == 0 {
		return errors.New("answers required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pendingToolCalls == nil || m.pendingToolCalls[sessionKey] == nil {
		return errors.New("pending tool call not found")
	}
	existing, ok := m.pendingToolCalls[sessionKey][callID]
	if !ok {
		return errors.New("pending tool call not found")
	}
	if existing.Kind != "" && existing.Kind != agenttypes.ToolKindAskUser {
		return errors.New("pending tool call is not ask_user")
	}
	meta := make(map[string]any, len(existing.Meta)+2)
	for key, value := range existing.Meta {
		meta[key] = value
	}
	meta["answers"] = cleanAnswers
	if !answeredAt.IsZero() {
		meta["answeredAt"] = answeredAt.UTC().Format(time.RFC3339Nano)
	}
	existing.Kind = agenttypes.ToolKindAskUser
	existing.Status = "complete"
	existing.Meta = meta
	m.pendingToolCalls[sessionKey][callID] = existing
	return nil
}

// UnmarkPendingAskUserAnswered 撤回一次「已回答」标记，把卡片放回可回答状态。
//
// 与 MarkPendingAskUserAnswered 配对：标记必须先写（投递成功会让 agent 立刻继续，pending
// 条目随后可能被收尾清掉，那时再写就写不进去了），但投递失败时这个标记就成了谎 ——
// 卡片显示「已回答」而 agent 仍在等，该轮永久卡死且用户没有第二次机会。
//
// answeredAt 用来确认「这中间没人动过」：只有 meta 里记的还是我们写进去的那一刻才撤。
// 不一致说明 agent 的 tool_result 已经把它推进到终态，撤回去反而是倒退。
func (m *Manager) UnmarkPendingAskUserAnswered(_ context.Context, sessionKey, callID string, answeredAt time.Time) {
	sessionKey = strings.TrimSpace(sessionKey)
	callID = strings.TrimSpace(callID)
	if sessionKey == "" || callID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	byCallID := m.pendingToolCalls[sessionKey]
	if byCallID == nil {
		return
	}
	existing, ok := byCallID[callID]
	if !ok || existing.Kind != agenttypes.ToolKindAskUser {
		return
	}
	if got, _ := existing.Meta["answeredAt"].(string); got != answeredAt.UTC().Format(time.RFC3339Nano) {
		return
	}
	meta := make(map[string]any, len(existing.Meta))
	for key, value := range existing.Meta {
		if key == "answers" || key == "answeredAt" {
			continue
		}
		meta[key] = value
	}
	existing.Status = "running"
	existing.Meta = meta
	byCallID[callID] = existing
}

func (m *Manager) ClearPendingExchangeAux(_ context.Context, sessionKey string) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pendingToolCalls, sessionKey)
}

func mergeToolCall(base, next agenttypes.ToolCall) agenttypes.ToolCall {
	merged := base
	if strings.TrimSpace(next.CallID) != "" {
		merged.CallID = next.CallID
	}
	if strings.TrimSpace(next.Title) != "" {
		merged.Title = next.Title
	}
	if strings.TrimSpace(next.Status) != "" {
		merged.Status = next.Status
	}
	if next.Kind != "" {
		merged.Kind = next.Kind
	}
	if len(next.Content) > 0 {
		merged.Content = append([]agenttypes.ToolCallContentItem(nil), next.Content...)
	}
	if len(next.Locations) > 0 {
		merged.Locations = append([]agenttypes.ToolCallLocation(nil), next.Locations...)
	}
	if strings.TrimSpace(next.RawType) != "" {
		merged.RawType = next.RawType
	}
	if len(base.Meta) > 0 || len(next.Meta) > 0 {
		merged.Meta = make(map[string]any, len(base.Meta)+len(next.Meta))
		for key, value := range base.Meta {
			merged.Meta[key] = value
		}
		for key, value := range next.Meta {
			merged.Meta[key] = value
		}
	}
	return merged
}

func (m *Manager) pendingFullToolCallUnsafe(sessionKey, callID string) (*agenttypes.ToolCall, bool) {
	if m.pendingToolCalls == nil {
		return nil, false
	}
	toolCall, ok := m.pendingToolCalls[sessionKey][callID]
	if !ok {
		return nil, false
	}
	out := cloneToolCall(toolCall)
	return &out, true
}

func cloneToolCall(toolCall agenttypes.ToolCall) agenttypes.ToolCall {
	out := toolCall
	out.Content = append([]agenttypes.ToolCallContentItem(nil), toolCall.Content...)
	out.Locations = append([]agenttypes.ToolCallLocation(nil), toolCall.Locations...)
	if len(toolCall.Meta) > 0 {
		out.Meta = make(map[string]any, len(toolCall.Meta))
		for key, value := range toolCall.Meta {
			out.Meta[key] = value
		}
	}
	return out
}
