package claude

import (
	"encoding/json"
	"path/filepath"
	"strings"

	types "mindfs/server/internal/agent/types"

	claudeagent "github.com/roasbeef/claude-agent-sdk-go"
)

func summarizeToolCall(name string, input json.RawMessage) (string, map[string]any, []types.ToolCallLocation, []types.ToolCallContentItem) {
	rawInput := strings.TrimSpace(string(input))
	if rawInput == "" {
		return name, nil, nil, nil
	}

	meta := map[string]any{"input": rawInput}
	switch mapToolKind(name) {
	case types.ToolKindRead, types.ToolKindEdit:
		return summarizePathToolCall(name, input, meta)
	case types.ToolKindExecute:
		title, nextMeta := summarizeExecuteToolCall(name, input, meta)
		return title, nextMeta, nil, nil
	case types.ToolKindSearch:
		title, nextMeta := summarizeSearchToolCall(name, input, meta)
		return title, nextMeta, nil, nil
	case types.ToolKindWebSearch:
		title, nextMeta, content := summarizeWebSearchToolCall(input, meta)
		return title, nextMeta, nil, content
	case types.ToolKindTask:
		title, nextMeta, content := summarizeTaskToolCall(name, input, meta)
		return title, nextMeta, nil, content
	case types.ToolKindAskUser:
		title, nextMeta, content := summarizeAskUserToolCall(input, meta)
		return title, nextMeta, nil, content
	case types.ToolKindTodo:
		title, nextMeta, content := summarizeTodoToolCall(input, meta)
		return title, nextMeta, nil, content
	default:
		return name, meta, nil, nil
	}
}

func summarizePathToolCall(name string, input json.RawMessage, fallbackMeta map[string]any) (string, map[string]any, []types.ToolCallLocation, []types.ToolCallContentItem) {
	var payload struct {
		FilePath   string `json:"file_path"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		Content    string `json:"content"`
		ReplaceAll bool   `json:"replace_all"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return name, fallbackMeta, nil, nil
	}

	path := strings.TrimSpace(payload.FilePath)
	if path == "" {
		return name, fallbackMeta, nil, nil
	}

	base := strings.TrimSpace(filepath.Base(path))
	if base == "" || base == "." || base == string(filepath.Separator) {
		return name, fallbackMeta, nil, nil
	}

	meta := map[string]any{"filePath": path}
	if fallbackMeta != nil {
		meta["input"] = fallbackMeta["input"]
	}
	if payload.ReplaceAll {
		meta["replaceAll"] = true
	}

	locations := []types.ToolCallLocation{{Path: path}}
	content := make([]types.ToolCallContentItem, 0, 1)

	switch strings.ToLower(strings.TrimSpace(name)) {
	case "write":
		if payload.Content != "" {
			content = append(content, types.ToolCallContentItem{
				Type:       "text",
				Text:       payload.Content,
				Path:       path,
				ChangeKind: "add",
			})
		}
	case "edit", "multiedit":
		if payload.OldString != "" || payload.NewString != "" {
			oldText := payload.OldString
			content = append(content, types.ToolCallContentItem{
				Type:    "diff",
				Path:    path,
				OldText: &oldText,
				NewText: payload.NewString,
			})
		}
	}

	return base, meta, locations, content
}

func summarizeExecuteToolCall(name string, input json.RawMessage, fallbackMeta map[string]any) (string, map[string]any) {
	var payload claudeagent.BashInput
	if err := json.Unmarshal(input, &payload); err != nil {
		return name, fallbackMeta
	}

	command := strings.TrimSpace(payload.Command)
	if command == "" {
		return name, fallbackMeta
	}

	meta := map[string]any{"command": command}
	if desc := strings.TrimSpace(payload.Description); desc != "" {
		meta["description"] = desc
		return desc, meta
	}
	return "Run command", meta
}

func summarizeSearchToolCall(name string, input json.RawMessage, fallbackMeta map[string]any) (string, map[string]any) {
	var payload struct {
		Pattern string `json:"pattern"`
		Query   string `json:"query"`
		Path    string `json:"path"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return name, fallbackMeta
	}

	switch {
	case strings.TrimSpace(payload.Pattern) != "":
		return payload.Pattern, map[string]any{"pattern": payload.Pattern}
	case strings.TrimSpace(payload.Query) != "":
		return payload.Query, map[string]any{"query": payload.Query}
	case strings.TrimSpace(payload.Path) != "":
		return payload.Path, map[string]any{"path": payload.Path}
	default:
		return name, fallbackMeta
	}
}

func summarizeTodoToolCall(input json.RawMessage, fallbackMeta map[string]any) (string, map[string]any, []types.ToolCallContentItem) {
	var payload claudeagent.TodoWriteInput
	if err := json.Unmarshal(input, &payload); err != nil {
		return "todos", fallbackMeta, nil
	}
	if len(payload.Todos) == 0 {
		return "todos", fallbackMeta, nil
	}

	lines := make([]string, 0, len(payload.Todos))
	for _, todo := range payload.Todos {
		label := strings.TrimSpace(todo.Content)
		active := strings.TrimSpace(todo.ActiveForm)
		status := strings.ToLower(strings.TrimSpace(todo.Status))
		switch status {
		case "completed":
			if label == "" {
				label = active
			}
			if label == "" {
				continue
			}
			lines = append(lines, "- [x] "+label)
		case "in_progress":
			if active != "" {
				label = active
			}
			if label == "" {
				continue
			}
			lines = append(lines, "- [ ] "+label+" _(in progress)_")
		default:
			if label == "" {
				label = active
			}
			if label == "" {
				continue
			}
			lines = append(lines, "- [ ] "+label)
		}
	}
	if len(lines) == 0 {
		return "todos", fallbackMeta, nil
	}

	meta := map[string]any{"todoCount": len(lines)}
	if fallbackMeta != nil {
		for k, v := range fallbackMeta {
			meta[k] = v
		}
	}

	return "todos", meta, []types.ToolCallContentItem{{
		Type: "text",
		Text: strings.Join(lines, "\n"),
	}}
}
