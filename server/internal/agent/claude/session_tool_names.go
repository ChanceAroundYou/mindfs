package claude

import (
	"encoding/json"
	"strings"

	types "mindfs/server/internal/agent/types"
)

func mapToolKind(name string) types.ToolKind {
	switch normalizeToolName(name) {
	case "read":
		return types.ToolKindRead
	case "edit", "write", "multiedit":
		return types.ToolKindEdit
	case "delete":
		return types.ToolKindDelete
	case "move", "rename":
		return types.ToolKindMove
	case "glob", "grep", "search":
		return types.ToolKindSearch
	case "websearch":
		return types.ToolKindWebSearch
	case "bash", "execute":
		return types.ToolKindExecute
	case "webfetch", "fetch":
		return types.ToolKindFetch
	case "agent", "task", "taskcreate", "taskupdate", "tasklist", "taskget":
		return types.ToolKindTask
	case "askuserquestion":
		return types.ToolKindAskUser
	case "todowrite", "todos":
		return types.ToolKindTodo
	case "think", "updateplan", "enterplanmode", "exitplanmode":
		return types.ToolKindThink
	case "switchmode":
		return types.ToolKindSwitchMode
	default:
		return types.ToolKindOther
	}
}

func normalizeToolName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, "_", "")
	name = strings.ReplaceAll(name, "-", "")
	return name
}

func preview(content string) string {
	trimmed := strings.TrimSpace(content)
	if len(trimmed) <= 300 {
		return trimmed
	}
	return trimmed[:300] + "...(truncated)"
}

func toolCallLogValue(toolCall types.ToolCall) string {
	raw, err := json.Marshal(toolCall)
	if err != nil {
		return `{"marshal_error":true}`
	}
	return string(raw)
}

func truncateRaw(raw []byte) string {
	const maxRawLogBytes = 1024
	if len(raw) > maxRawLogBytes {
		raw = append(raw[:maxRawLogBytes], []byte("...(truncated)")...)
	}
	return string(raw)
}
