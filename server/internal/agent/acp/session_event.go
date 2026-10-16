package acp

import (
	"encoding/json"
	"strings"

	types "mindfs/server/internal/agent/types"

	acpsdk "github.com/coder/acp-go-sdk"
)

func mapCommandState(commands []acpsdk.AvailableCommand) types.CommandList {
	if len(commands) == 0 {
		return types.CommandList{}
	}
	items := make([]types.CommandInfo, 0, len(commands))
	for _, command := range commands {
		argumentHint := ""
		if command.Input != nil && command.Input.Unstructured != nil {
			argumentHint = strings.TrimSpace(command.Input.Unstructured.Hint)
		}
		items = append(items, types.CommandInfo{
			Name:         command.Name,
			Description:  command.Description,
			ArgumentHint: argumentHint,
		})
	}
	return types.CommandList{Commands: items}
}

func mapModelState(state *acpsdk.SessionModelState) types.ModelList {
	if state == nil {
		return types.ModelList{}
	}
	models := make([]types.ModelInfo, 0, len(state.AvailableModels))
	for _, model := range state.AvailableModels {
		models = append(models, types.ModelInfo{
			ID:          strings.TrimSpace(string(model.ModelId)),
			Name:        firstNonEmpty(strings.TrimSpace(model.Name), strings.TrimSpace(string(model.ModelId))),
			Description: stringPtrValue(model.Description),
		})
	}
	return types.ModelList{
		CurrentModelID: strings.TrimSpace(string(state.CurrentModelId)),
		Models:         models,
	}
}

func mapModeState(state *acpsdk.SessionModeState) types.ModeList {
	if state == nil {
		return types.ModeList{}
	}
	modes := make([]types.ModeInfo, 0, len(state.AvailableModes))
	for _, mode := range state.AvailableModes {
		description := ""
		if mode.Description != nil {
			description = *mode.Description
		}
		modes = append(modes, types.ModeInfo{
			ID:          string(mode.Id),
			Name:        mode.Name,
			Description: description,
		})
	}
	return types.ModeList{
		CurrentModeID: string(state.CurrentModeId),
		Modes:         modes,
	}
}

func (s *session) logRawToolUpdate(update SessionUpdate) {
	if s == nil || s.agentDebugLog == nil {
		return
	}
	if update.Type != UpdateTypeToolCall && update.Type != UpdateTypeToolUpdate {
		return
	}
	raw, err := json.Marshal(update.Raw)
	if err != nil {
		return
	}
	s.agentDebugLog.AppendRaw(raw)
}

// convertEvent converts a wrapped ACP session update into a MindFS event.
//
// onAskUser, when non-nil, is invoked with the call id and normalized questions for
// every ask_user tool card that survives suppression, so the caller can register it
// as a pending question. It is split out because registration needs the Process
// (which owns the pending table) while convertEvent itself stays a pure mapping.
func convertEvent(update SessionUpdate, onAskUser func(callID string, questions []elicitationQuestion)) types.Event {
	ev := types.Event{
		Type:      types.EventType(update.Type),
		SessionID: update.SessionID,
	}
	raw := update.Raw
	switch update.Type {
	case UpdateTypeMessageChunk:
		if raw.AgentMessageChunk != nil && raw.AgentMessageChunk.Content.Text != nil {
			ev.Data = types.MessageChunk{
				Content: raw.AgentMessageChunk.Content.Text.Text,
				// messageId 带上这一块的「所属 agent 原生消息 id」。它是**步级**身份
				// （方案 §3.3）：dsh 一轮里会发多条 assistant/message，各带一个
				// messageId。实时路径取**最后一个**写进 `agent_id`，导入侧对同一
				// 条消息也取最后一个 —— 两边于是对得上，判重不必猜内容。
				MessageID: messageIDOf(raw.AgentMessageChunk.MessageId),
			}
		} else {
			logUnhandledConvertEvent(update, "agent_message_chunk")
		}
	case UpdateTypeThoughtChunk:
		if raw.AgentThoughtChunk != nil && raw.AgentThoughtChunk.Content.Text != nil {
			ev.Data = types.ThoughtChunk{Content: raw.AgentThoughtChunk.Content.Text.Text}
		} else {
			logUnhandledConvertEvent(update, "agent_thought_chunk")
		}
	case UpdateTypeToolCall:
		if raw.ToolCall != nil {
			if isACPTodoTool(raw.ToolCall.Title, string(raw.ToolCall.Kind), raw.ToolCall.RawInput, raw.ToolCall.RawOutput) {
				return types.Event{}
			}
			locations := make([]types.ToolCallLocation, 0, len(raw.ToolCall.Locations))
			for _, loc := range raw.ToolCall.Locations {
				locations = append(locations, types.ToolCallLocation{Path: loc.Path, Line: loc.Line})
			}
			status := "running"
			if raw.ToolCall.Status != "" {
				status = string(raw.ToolCall.Status)
			}
			kind := types.ToolKind(raw.ToolCall.Kind)
			var meta map[string]any
			var askUserQuestions []elicitationQuestion
			// CUSTOM(G-AS): dsh 的 ask_user_question 在适配器那边只是 kind=other 的普通工具卡，
			// 重标成 ask_user 并带上 questions，前端才会渲染成可回答的提问卡。
			if askUserKind, askUserMeta, questions := acpAskUserToolCall(raw.ToolCall.RawInput); askUserKind != "" {
				kind, meta, askUserQuestions = askUserKind, askUserMeta, questions
			}
			ev.Data = types.ToolCall{
				CallID:    string(raw.ToolCall.ToolCallId),
				Title:     raw.ToolCall.Title,
				Status:    status,
				Kind:      kind,
				Content:   convertToolCallContent(raw.ToolCall.Content),
				Locations: locations,
				Meta:      meta,
			}
			if onAskUser != nil && len(askUserQuestions) > 0 {
				onAskUser(string(raw.ToolCall.ToolCallId), askUserQuestions)
			}
		} else {
			logUnhandledConvertEvent(update, "tool_call")
		}
	case UpdateTypeToolUpdate:
		if raw.ToolCallUpdate != nil {
			name := ""
			if raw.ToolCallUpdate.Title != nil {
				name = *raw.ToolCallUpdate.Title
			}
			rawKind := ""
			if raw.ToolCallUpdate.Kind != nil {
				rawKind = string(*raw.ToolCallUpdate.Kind)
			}
			if isACPTodoTool(name, rawKind, raw.ToolCallUpdate.RawInput, raw.ToolCallUpdate.RawOutput) {
				return types.Event{}
			}
			status := "complete"
			if raw.ToolCallUpdate.Status != nil && *raw.ToolCallUpdate.Status == acpsdk.ToolCallStatusFailed {
				status = "failed"
			}
			kind := types.ToolKind("")
			if raw.ToolCallUpdate.Kind != nil {
				kind = types.ToolKind(*raw.ToolCallUpdate.Kind)
			}
			var meta map[string]any
			var askUserQuestions []elicitationQuestion
			// 与 tool_call 分支同理：update 也会带 rawInput，不重标的话
			// mergeBufferedToolCall 会用 kind=other 把 ask_user 覆盖掉。
			if askUserKind, askUserMeta, questions := acpAskUserToolCall(raw.ToolCallUpdate.RawInput); askUserKind != "" {
				kind, meta, askUserQuestions = askUserKind, askUserMeta, questions
			}
			locations := make([]types.ToolCallLocation, 0, len(raw.ToolCallUpdate.Locations))
			for _, loc := range raw.ToolCallUpdate.Locations {
				locations = append(locations, types.ToolCallLocation{Path: loc.Path, Line: loc.Line})
			}
			ev.Data = types.ToolCall{
				CallID:    string(raw.ToolCallUpdate.ToolCallId),
				Title:     name,
				Status:    status,
				Kind:      kind,
				Content:   convertToolCallContent(raw.ToolCallUpdate.Content),
				Locations: locations,
				Meta:      meta,
			}
			if onAskUser != nil && len(askUserQuestions) > 0 {
				onAskUser(string(raw.ToolCallUpdate.ToolCallId), askUserQuestions)
			}
		} else {
			logUnhandledConvertEvent(update, "tool_call_update")
		}
	case UpdateTypePlan:
		if raw.Plan != nil {
			ev.Type = types.EventTypeTodoUpdate
			ev.Data = types.TodoUpdate{Items: convertACPPlanEntriesToTodos(raw.Plan.Entries)}
		} else if raw.PlanUpdate != nil {
			ev.Type, ev.Data = convertACPPlanUpdate(*raw.PlanUpdate)
		} else {
			logUnhandledConvertEvent(update, "plan")
		}
	default:
		logUnhandledConvertEvent(update, "update_type")
	}
	return ev
}

func isACPTodoTool(title, kind string, rawInput, rawOutput any) bool {
	normalizedKind := strings.ToLower(strings.TrimSpace(kind))
	if normalizedKind != "" && normalizedKind != "other" && normalizedKind != "todo" {
		return false
	}
	normalizedTitle := strings.ToLower(strings.TrimSpace(title))
	if normalizedTitle == "todowrite" {
		return true
	}
	return strings.HasSuffix(normalizedTitle, " todos") && (hasACPTodosPayload(rawInput) || hasACPTodosPayload(rawOutput))
}

func hasACPTodosPayload(value any) bool {
	normalized := normalizeACPValue(value)
	data, ok := normalized.(map[string]any)
	if !ok {
		return false
	}
	if _, ok := data["todos"]; ok {
		return true
	}
	metadata, ok := data["metadata"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = metadata["todos"]
	return ok
}

func normalizeACPValue(value any) any {
	switch v := value.(type) {
	case []byte:
		var decoded any
		if err := json.Unmarshal(v, &decoded); err == nil {
			return decoded
		}
	case json.RawMessage:
		var decoded any
		if err := json.Unmarshal(v, &decoded); err == nil {
			return decoded
		}
	case string:
		var decoded any
		if err := json.Unmarshal([]byte(v), &decoded); err == nil {
			return decoded
		}
	}
	return value
}
