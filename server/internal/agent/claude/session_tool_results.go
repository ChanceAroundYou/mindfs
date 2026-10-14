package claude

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	types "mindfs/server/internal/agent/types"

	claudeagent "github.com/roasbeef/claude-agent-sdk-go"
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

func (s *session) toolResultUpdate(msg claudeagent.UserMessage) (types.ToolCall, bool) {
	if msg.ToolUseResult == nil {
		return types.ToolCall{}, false
	}

	callID := ""
	if msg.ParentToolUseID != nil {
		callID = strings.TrimSpace(*msg.ParentToolUseID)
	}
	if callID == "" {
		callID = extractToolResultCallID(msg.ToolUseResult)
	}
	base, ok := s.popPendingToolCall(callID)
	if !ok && callID == "" {
		base, ok = s.popOnlyPendingToolCall()
	}
	if !ok {
		return types.ToolCall{}, false
	}
	if isTaskListToolCall(base) {
		return taskListToolResultUpdate(base, msg.ToolUseResult)
	}

	result := summarizeToolResult(base.Kind, msg.ToolUseResult)
	if result == "" {
		result = summarizeUserToolResultMessage(msg)
	}
	update := base
	update.Status = "complete"
	if result != "" {
		update.Meta = mergeToolCallMeta(update.Meta, map[string]any{"output": result})
		if base.Kind != types.ToolKindEdit || len(base.Content) == 0 {
			update.Content = []types.ToolCallContentItem{{Type: "text", Text: result}}
		}
	}
	return update, true
}

func isTaskListToolCall(toolCall types.ToolCall) bool {
	return toolCall.Kind == types.ToolKindTask && canonicalTaskListToolName(stringMeta(toolCall.Meta, "taskTool")) != ""
}

func taskListToolResultUpdate(base types.ToolCall, raw any) (types.ToolCall, bool) {
	taskTool := canonicalTaskListToolName(stringMeta(base.Meta, "taskTool"))
	if taskTool == "" {
		return types.ToolCall{}, false
	}

	result := summarizeToolResult(base.Kind, raw)
	if result == "" {
		result = summarizeGenericToolResult(raw)
	}
	if isEmptyJSONResult(result) {
		result = ""
	}

	switch taskTool {
	case "TaskCreate":
		taskID := firstNonEmpty(extractTaskListResultID(raw), stringMeta(base.Meta, "taskId"))
		update := base
		update.Status = "running"
		if taskID != "" {
			update.CallID = taskListCallID(taskID)
			update.Meta = mergeToolCallMeta(update.Meta, map[string]any{"taskId": taskID})
		}
		return update, true
	case "TaskUpdate":
		taskID := firstNonEmpty(extractTaskListResultID(raw), stringMeta(base.Meta, "taskId"))
		status := firstNonEmpty(extractTaskListResultStatus(raw), stringMeta(base.Meta, "taskStatus"), "complete")
		update := base
		update.Title = ""
		update.Content = nil
		update.Status = status
		meta := map[string]any{"taskStatus": status}
		if taskID != "" {
			update.CallID = taskListCallID(taskID)
			meta["taskId"] = taskID
		}
		update.Meta = mergeToolCallMeta(update.Meta, meta)
		return update, true
	case "TaskList":
		if result == "" {
			return types.ToolCall{}, false
		}
		update := base
		update.Status = "complete"
		update.Meta = mergeToolCallMeta(update.Meta, map[string]any{"output": result})
		update.Content = []types.ToolCallContentItem{{Type: "text", Text: result}}
		return update, true
	case "TaskGet":
		if result == "" {
			return types.ToolCall{}, false
		}
		taskID := firstNonEmpty(extractTaskListResultID(raw), stringMeta(base.Meta, "taskId"))
		update := base
		update.Status = "complete"
		if taskID != "" {
			update.CallID = taskListCallID(taskID)
			update.Meta = mergeToolCallMeta(update.Meta, map[string]any{"taskId": taskID})
		}
		update.Meta = mergeToolCallMeta(update.Meta, map[string]any{"output": result})
		update.Content = []types.ToolCallContentItem{{Type: "text", Text: result}}
		return update, true
	default:
		return types.ToolCall{}, false
	}
}

func isEmptyJSONResult(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return true
	}
	return trimmed == "{}" || trimmed == "[]"
}

func extractTaskListResultID(raw any) string {
	value := normalizeToolResultValue(raw)
	switch v := value.(type) {
	case map[string]any:
		if taskID := stringField(v, "taskId"); taskID != "" {
			return taskID
		}
		if id := stringField(v, "id"); id != "" {
			return id
		}
		if task, ok := v["task"].(map[string]any); ok {
			if taskID := stringField(task, "taskId"); taskID != "" {
				return taskID
			}
			if id := stringField(task, "id"); id != "" {
				return id
			}
		}
		if result, ok := v["result"].(map[string]any); ok {
			if taskID := stringField(result, "taskId"); taskID != "" {
				return taskID
			}
			if id := stringField(result, "id"); id != "" {
				return id
			}
			if task, ok := result["task"].(map[string]any); ok {
				if taskID := stringField(task, "taskId"); taskID != "" {
					return taskID
				}
				if id := stringField(task, "id"); id != "" {
					return id
				}
			}
		}
	}
	return ""
}

func extractTaskListResultStatus(raw any) string {
	value := normalizeToolResultValue(raw)
	switch v := value.(type) {
	case map[string]any:
		if status := normalizeTaskListStatus(stringField(v, "status")); status != "" {
			return status
		}
		if statusChange, ok := v["statusChange"].(map[string]any); ok {
			if status := normalizeTaskListStatus(stringField(statusChange, "to")); status != "" {
				return status
			}
		}
		if task, ok := v["task"].(map[string]any); ok {
			if status := normalizeTaskListStatus(stringField(task, "status")); status != "" {
				return status
			}
		}
		if result, ok := v["result"].(map[string]any); ok {
			if status := normalizeTaskListStatus(stringField(result, "status")); status != "" {
				return status
			}
			if statusChange, ok := result["statusChange"].(map[string]any); ok {
				if status := normalizeTaskListStatus(stringField(statusChange, "to")); status != "" {
					return status
				}
			}
			if task, ok := result["task"].(map[string]any); ok {
				if status := normalizeTaskListStatus(stringField(task, "status")); status != "" {
					return status
				}
			}
		}
	}
	return ""
}

func normalizeToolResultValue(raw any) any {
	switch v := raw.(type) {
	case string:
		var decoded any
		if err := json.Unmarshal([]byte(v), &decoded); err == nil {
			return decoded
		}
		return raw
	case []byte:
		var decoded any
		if err := json.Unmarshal(v, &decoded); err == nil {
			return decoded
		}
		return raw
	case json.RawMessage:
		var decoded any
		if err := json.Unmarshal(v, &decoded); err == nil {
			return decoded
		}
		return raw
	default:
		data, err := json.Marshal(raw)
		if err != nil {
			return raw
		}
		var decoded any
		if err := json.Unmarshal(data, &decoded); err == nil {
			return decoded
		}
		return raw
	}
}

func stringField(data map[string]any, key string) string {
	value, ok := data[key]
	if !ok {
		return ""
	}
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strings.TrimSpace(fmt.Sprint(v))
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func mergeToolCallMeta(base map[string]any, extra map[string]any) map[string]any {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	out := make(map[string]any, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func stringMeta(meta map[string]any, key string) string {
	if len(meta) == 0 {
		return ""
	}
	value, ok := meta[key]
	if !ok {
		return ""
	}
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
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

func summarizeToolResult(kind types.ToolKind, raw any) string {
	switch kind {
	case types.ToolKindExecute:
		return summarizeExecuteToolResult(raw)
	case types.ToolKindEdit:
		return summarizeEditToolResult(raw)
	default:
		return ""
	}
}

func summarizeExecuteToolResult(raw any) string {
	var payload struct {
		Stdout string `json:"stdout"`
		Stderr string `json:"stderr"`
	}
	if decodeToolResult(raw, &payload) {
		if strings.TrimSpace(payload.Stdout) != "" {
			return payload.Stdout
		}
		if strings.TrimSpace(payload.Stderr) != "" {
			return payload.Stderr
		}
	}
	return summarizeGenericToolResult(raw)
}

func summarizeEditToolResult(raw any) string {
	var payload struct {
		Content string `json:"content"`
	}
	if decodeToolResult(raw, &payload) && strings.TrimSpace(payload.Content) != "" {
		return payload.Content
	}
	return summarizeGenericToolResult(raw)
}

func decodeToolResult(raw any, out any) bool {
	data, err := json.Marshal(raw)
	if err != nil {
		return false
	}
	if err := json.Unmarshal(data, out); err != nil {
		return false
	}
	return true
}

func extractToolResultCallID(raw any) string {
	var payload map[string]any
	if !decodeToolResult(raw, &payload) {
		return ""
	}
	for _, key := range []string{"parent_tool_use_id", "tool_use_id", "toolUseId", "id"} {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func summarizeUserToolResultMessage(msg claudeagent.UserMessage) string {
	for _, block := range msg.Message.Content {
		if strings.TrimSpace(block.Text) == "" {
			continue
		}
		if block.Type == "tool_result" || len(msg.Message.Content) == 1 {
			if text := summarizeGenericToolResult(block.Text); strings.TrimSpace(text) != "" {
				return text
			}
			return block.Text
		}
	}
	return ""
}

func summarizeGenericToolResult(raw any) string {
	var payload any
	data, err := json.Marshal(raw)
	if err != nil {
		return ""
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		if text, ok := raw.(string); ok {
			return strings.TrimSpace(text)
		}
		return ""
	}
	return summarizeGenericToolResultValue(payload)
}

func summarizeGenericToolResultValue(value any) string {
	switch v := value.(type) {
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return ""
		}
		var nested any
		if err := json.Unmarshal([]byte(trimmed), &nested); err == nil {
			if text := summarizeGenericToolResultValue(nested); text != "" {
				return text
			}
		}
		return v
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if text := summarizeGenericToolResultValue(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		for _, key := range []string{"stdout", "stderr", "output", "content", "text", "error"} {
			if text := summarizeGenericToolResultValue(v[key]); text != "" {
				return text
			}
		}
	}
	return ""
}
