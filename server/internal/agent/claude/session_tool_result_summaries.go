package claude

import (
	"encoding/json"
	"strings"

	types "mindfs/server/internal/agent/types"

	claudeagent "github.com/roasbeef/claude-agent-sdk-go"
)

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
