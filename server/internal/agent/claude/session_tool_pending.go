package claude

import (
	"strings"

	types "mindfs/server/internal/agent/types"

)

func (s *session) trackPendingToolCall(toolCall types.ToolCall) {
	s.pendingToolMu.Lock()
	defer s.pendingToolMu.Unlock()
	if strings.TrimSpace(toolCall.CallID) == "" {
		return
	}
	if s.pendingToolCalls == nil {
		s.pendingToolCalls = make(map[string]types.ToolCall)
	}
	key := stringMeta(toolCall.Meta, "toolUseId")
	if key == "" {
		key = toolCall.CallID
	}
	s.pendingToolCalls[key] = toolCall
}

func (s *session) hasPendingToolCall(callID string) bool {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return false
	}
	s.pendingToolMu.Lock()
	defer s.pendingToolMu.Unlock()
	_, ok := s.pendingToolCalls[callID]
	return ok
}

func (s *session) cancelPendingToolCall(callID, reason string) (types.ToolCall, bool) {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return types.ToolCall{}, false
	}
	s.pendingToolMu.Lock()
	defer s.pendingToolMu.Unlock()
	if len(s.pendingToolCalls) == 0 {
		return types.ToolCall{}, false
	}
	toolCall, ok := s.pendingToolCalls[callID]
	if !ok {
		return types.ToolCall{}, false
	}
	delete(s.pendingToolCalls, callID)
	toolCall.Status = "failed"
	toolCall.Meta = mergeToolCallMeta(toolCall.Meta, map[string]any{"error": reason, "canceled": true})
	if len(toolCall.Content) == 0 && strings.TrimSpace(reason) != "" {
		toolCall.Content = []types.ToolCallContentItem{{Type: "text", Text: reason}}
	}
	return toolCall, true
}

func (s *session) popPendingToolCall(callID string) (types.ToolCall, bool) {
	s.pendingToolMu.Lock()
	defer s.pendingToolMu.Unlock()

	callID = strings.TrimSpace(callID)
	if callID == "" || len(s.pendingToolCalls) == 0 {
		return types.ToolCall{}, false
	}
	toolCall, ok := s.pendingToolCalls[callID]
	if !ok {
		return types.ToolCall{}, false
	}
	delete(s.pendingToolCalls, callID)
	return toolCall, true
}

func (s *session) popOnlyPendingToolCall() (types.ToolCall, bool) {
	s.pendingToolMu.Lock()
	defer s.pendingToolMu.Unlock()

	if len(s.pendingToolCalls) != 1 {
		return types.ToolCall{}, false
	}
	for callID, toolCall := range s.pendingToolCalls {
		delete(s.pendingToolCalls, callID)
		return toolCall, true
	}
	return types.ToolCall{}, false
}
