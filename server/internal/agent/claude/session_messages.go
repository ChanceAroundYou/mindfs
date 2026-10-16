package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	types "mindfs/server/internal/agent/types"

	claudeagent "github.com/roasbeef/claude-agent-sdk-go"
)

func (s *session) handleUserMessage(msg claudeagent.UserMessage) {
	// Claude tool results do not come back on AssistantMessage. They arrive
	// here, and we map the result onto the earliest pending tool call.
	update, ok := s.toolResultUpdate(msg)
	if !ok {
		return
	}
	s.emit(types.Event{
		Type:      types.EventTypeToolUpdate,
		SessionID: s.SessionID(),
		Data:      update,
	})
}

func (s *session) awaitAskUserQuestion(ctx context.Context, qs claudeagent.QuestionSet) (claudeagent.Answers, error) {
	callID := strings.TrimSpace(qs.ToolUseID)
	if callID == "" {
		return nil, errors.New("ask user question missing tool use id")
	}

	waiter := make(chan askUserAnswerResult, 1)
	s.questionMu.Lock()
	if s.questionWaits == nil {
		s.questionWaits = make(map[string]chan askUserAnswerResult)
	}
	if _, exists := s.questionWaits[callID]; exists {
		s.questionMu.Unlock()
		return nil, errors.New("ask user question already pending: " + callID)
	}
	s.questionWaits[callID] = waiter
	s.questionMu.Unlock()
	defer func() {
		s.questionMu.Lock()
		delete(s.questionWaits, callID)
		s.questionMu.Unlock()
	}()

	toolCall := askUserQuestionToolCall(qs)
	s.trackPendingToolCall(toolCall)
	// 与 handleAssistantMessage 的 tool_use 分支二选一发射：谁先到谁发，避免重复卡片。
	if s.claimAskUserEmit(callID) {
		s.emit(types.Event{
			Type:      types.EventTypeToolCall,
			SessionID: s.SessionID(),
			Data:      toolCall,
		})
	}

	select {
	case result := <-waiter:
		if result.err != nil {
			return nil, result.err
		}
		if len(result.answers) == 0 {
			return nil, errors.New("empty ask user answers")
		}
		return result.answers, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func askUserQuestionToolCall(qs claudeagent.QuestionSet) types.ToolCall {
	questions := make([]types.AskUserQuestionItem, 0, len(qs.Questions))
	for _, question := range qs.Questions {
		options := make([]types.AskUserQuestionOption, 0, len(question.Options))
		for _, option := range question.Options {
			options = append(options, types.AskUserQuestionOption{
				Label:       option.Label,
				Description: option.Description,
			})
		}
		questions = append(questions, types.AskUserQuestionItem{
			Question:    question.Question,
			Header:      question.Header,
			Options:     options,
			MultiSelect: question.MultiSelect,
		})
	}

	payload := claudeagent.AskUserQuestionInput{Questions: qs.Questions}
	raw, _ := json.Marshal(payload)
	toolCall := newRunningToolCall(qs.ToolUseID, "AskUserQuestion", "tool_use", raw)
	if toolCall.Meta == nil {
		toolCall.Meta = map[string]any{}
	}
	toolCall.Meta["toolUseId"] = qs.ToolUseID
	toolCall.Meta["questions"] = questions
	if qs.ParentToolUseID != nil && strings.TrimSpace(*qs.ParentToolUseID) != "" {
		toolCall.Meta["parentToolUseId"] = strings.TrimSpace(*qs.ParentToolUseID)
	}
	return toolCall
}

func newRunningToolCall(callID, name, rawType string, input json.RawMessage) types.ToolCall {
	title, meta, locations, content := summarizeToolCall(name, input)
	return types.ToolCall{
		CallID:    callID,
		Title:     title,
		Status:    "running",
		Kind:      mapToolKind(name),
		Locations: locations,
		Content:   content,
		RawType:   rawType,
		Meta:      meta,
	}
}

func subagentMetaFromPartial(msg claudeagent.PartialAssistantMessage) subagentMeta {
	meta := subagentMeta{}
	if msg.ParentToolUseID != nil {
		meta.ParentToolUseID = strings.TrimSpace(*msg.ParentToolUseID)
	}
	return meta
}

func subagentMetaFromAssistant(msg claudeagent.AssistantMessage) subagentMeta {
	meta := subagentMeta{
		SubagentType:    strings.TrimSpace(msg.SubagentType),
		TaskDescription: strings.TrimSpace(msg.TaskDescription),
	}
	if msg.ParentToolUseID != nil {
		meta.ParentToolUseID = strings.TrimSpace(*msg.ParentToolUseID)
	}
	return meta
}

func subagentMetaFromToolProgress(msg claudeagent.ToolProgressMessage) subagentMeta {
	meta := subagentMeta{}
	if msg.ParentToolUseID != nil {
		meta.ParentToolUseID = strings.TrimSpace(*msg.ParentToolUseID)
	}
	if msg.TaskID != nil {
		meta.TaskID = strings.TrimSpace(*msg.TaskID)
	}
	return meta
}

func (m subagentMeta) hasSubagentRef() bool {
	return strings.TrimSpace(m.ParentToolUseID) != "" || strings.TrimSpace(m.TaskID) != ""
}

func (m subagentMeta) toMap() map[string]any {
	out := map[string]any{}
	if value := strings.TrimSpace(m.ParentToolUseID); value != "" {
		out["parentToolUseId"] = value
	}
	if value := strings.TrimSpace(m.TaskID); value != "" {
		out["taskId"] = value
	}
	if value := strings.TrimSpace(m.SubagentType); value != "" {
		out["subagentType"] = value
	}
	if value := strings.TrimSpace(m.TaskDescription); value != "" {
		out["taskDescription"] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func compactBoundarySummary(msg claudeagent.CompactBoundaryMessage) string {
	parts := make([]string, 0, 2)
	if trigger := strings.TrimSpace(msg.CompactMetadata.Trigger); trigger != "" {
		parts = append(parts, "trigger: "+trigger)
	}
	if msg.CompactMetadata.PreTokens > 0 {
		parts = append(parts, fmt.Sprintf("tokens before compact: %d", msg.CompactMetadata.PreTokens))
	}
	return strings.Join(parts, "\n")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func boolPtrValue(value *bool) any {
	if value == nil {
		return nil
	}
	return *value
}
