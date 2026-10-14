package claude

import (
	"encoding/json"
	"fmt"
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

func summarizeTaskToolCall(name string, input json.RawMessage, fallbackMeta map[string]any) (string, map[string]any, []types.ToolCallContentItem) {
	if title, meta, content, ok := summarizeTaskListToolCall(name, input, fallbackMeta); ok {
		return title, meta, content
	}
	var payload claudeagent.TaskInput
	if err := json.Unmarshal(input, &payload); err != nil {
		return "task", fallbackMeta, nil
	}

	description := strings.TrimSpace(payload.Description)
	prompt := strings.TrimSpace(payload.Prompt)
	subagentType := strings.TrimSpace(payload.SubagentType)

	title := description
	if title == "" {
		title = prompt
	}
	if title == "" {
		title = "task"
	}

	meta := cloneToolMeta(fallbackMeta)
	if subagentType != "" {
		meta["subagentType"] = subagentType
	}
	if description != "" {
		meta["description"] = description
	}
	if prompt != "" {
		meta["prompt"] = prompt
	}

	lines := make([]string, 0, 3)
	if subagentType != "" {
		lines = append(lines, "**Subagent:** "+subagentType)
	}
	if description != "" {
		lines = append(lines, "**Description:** "+description)
	}
	if prompt != "" && prompt != description {
		lines = append(lines, "**Prompt:**\n"+prompt)
	}
	if len(lines) == 0 {
		return title, meta, nil
	}

	return title, meta, []types.ToolCallContentItem{{
		Type: "text",
		Text: strings.Join(lines, "\n\n"),
	}}
}

func summarizeTaskListToolCall(name string, input json.RawMessage, fallbackMeta map[string]any) (string, map[string]any, []types.ToolCallContentItem, bool) {
	switch normalizeToolName(name) {
	case "taskcreate":
		var payload struct {
			Subject     string `json:"subject"`
			Description string `json:"description"`
			ActiveForm  string `json:"activeForm"`
		}
		if err := json.Unmarshal(input, &payload); err != nil {
			return "task", fallbackMeta, nil, true
		}
		subject := strings.TrimSpace(payload.Subject)
		description := strings.TrimSpace(payload.Description)
		activeForm := strings.TrimSpace(payload.ActiveForm)
		title := firstNonEmpty(subject, activeForm, description, "task")
		meta := cloneToolMeta(fallbackMeta)
		if subject != "" {
			meta["subject"] = subject
		}
		if description != "" {
			meta["description"] = description
		}
		if activeForm != "" {
			meta["activeForm"] = activeForm
		}
		if description == "" || description == title {
			return title, meta, nil, true
		}
		return title, meta, []types.ToolCallContentItem{{Type: "text", Text: description}}, true
	case "taskupdate":
		var payload struct {
			TaskID string `json:"taskId"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal(input, &payload); err != nil {
			return "", fallbackMeta, nil, true
		}
		meta := cloneToolMeta(fallbackMeta)
		if taskID := strings.TrimSpace(payload.TaskID); taskID != "" {
			meta["taskId"] = taskID
		}
		if status := normalizeTaskListStatus(payload.Status); status != "" {
			meta["taskStatus"] = status
		}
		return "", meta, nil, true
	case "tasklist":
		return "TaskList", cloneToolMeta(fallbackMeta), nil, true
	case "taskget":
		var payload struct {
			TaskID string `json:"taskId"`
		}
		meta := cloneToolMeta(fallbackMeta)
		if err := json.Unmarshal(input, &payload); err == nil {
			if taskID := strings.TrimSpace(payload.TaskID); taskID != "" {
				meta["taskId"] = taskID
			}
		}
		return "TaskGet", meta, nil, true
	default:
		return "", nil, nil, false
	}
}

func normalizeTaskListStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "complete", "done":
		return "complete"
	case "inprogress", "in_progress", "running":
		return "running"
	case "failed", "error":
		return "failed"
	case "cancelled", "canceled":
		return "cancelled"
	default:
		return ""
	}
}

func taskListCallID(taskID string) string {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return ""
	}
	return "claude-task-list:" + taskID
}

func isTaskListToolName(name string) bool {
	return canonicalTaskListToolName(name) != ""
}

func canonicalTaskListToolName(name string) string {
	switch normalizeToolName(name) {
	case "taskcreate":
		return "TaskCreate"
	case "taskupdate":
		return "TaskUpdate"
	case "tasklist":
		return "TaskList"
	case "taskget":
		return "TaskGet"
	default:
		return ""
	}
}

func summarizeAskUserToolCall(input json.RawMessage, fallbackMeta map[string]any) (string, map[string]any, []types.ToolCallContentItem) {
	var payload claudeagent.AskUserQuestionInput
	if err := json.Unmarshal(input, &payload); err != nil {
		return "ask user", fallbackMeta, nil
	}
	if len(payload.Questions) == 0 {
		return "ask user", fallbackMeta, nil
	}

	meta := cloneToolMeta(fallbackMeta)
	meta["questionCount"] = len(payload.Questions)

	sections := make([]string, 0, len(payload.Questions))
	for index, question := range payload.Questions {
		lines := make([]string, 0, 2+len(question.Options))
		header := strings.TrimSpace(question.Header)
		text := strings.TrimSpace(question.Question)
		if header != "" {
			lines = append(lines, fmt.Sprintf("**Q%d · %s**", index+1, header))
		} else {
			lines = append(lines, fmt.Sprintf("**Q%d**", index+1))
		}
		if text != "" {
			lines = append(lines, text)
		}
		if question.MultiSelect {
			lines = append(lines, "_Multiple selection allowed._")
		}
		for _, option := range question.Options {
			label := strings.TrimSpace(option.Label)
			description := strings.TrimSpace(option.Description)
			if label == "" {
				continue
			}
			if description != "" {
				lines = append(lines, fmt.Sprintf("- **%s**: %s", label, description))
			} else {
				lines = append(lines, "- "+label)
			}
		}
		sections = append(sections, strings.Join(lines, "\n"))
	}

	title := "ask user"
	if len(payload.Questions) == 1 {
		if header := strings.TrimSpace(payload.Questions[0].Header); header != "" {
			title = header
		} else if text := strings.TrimSpace(payload.Questions[0].Question); text != "" {
			title = text
		}
	}

	return title, meta, []types.ToolCallContentItem{{
		Type: "text",
		Text: strings.Join(sections, "\n\n"),
	}}
}

func summarizeWebSearchToolCall(input json.RawMessage, fallbackMeta map[string]any) (string, map[string]any, []types.ToolCallContentItem) {
	var payload claudeagent.WebSearchInput
	if err := json.Unmarshal(input, &payload); err != nil {
		return "web search", fallbackMeta, nil
	}

	query := strings.TrimSpace(payload.Query)
	title := query
	if title == "" {
		title = "web search"
	}

	meta := cloneToolMeta(fallbackMeta)
	if query != "" {
		meta["query"] = query
	}
	if len(payload.AllowedDomains) > 0 {
		meta["allowedDomains"] = payload.AllowedDomains
	}
	if len(payload.BlockedDomains) > 0 {
		meta["blockedDomains"] = payload.BlockedDomains
	}

	lines := make([]string, 0, 3)
	if query != "" {
		lines = append(lines, "**Query:** "+query)
	}
	if len(payload.AllowedDomains) > 0 {
		lines = append(lines, "**Allowed domains:** "+strings.Join(payload.AllowedDomains, ", "))
	}
	if len(payload.BlockedDomains) > 0 {
		lines = append(lines, "**Blocked domains:** "+strings.Join(payload.BlockedDomains, ", "))
	}
	if len(lines) == 0 {
		return title, meta, nil
	}

	return title, meta, []types.ToolCallContentItem{{
		Type: "text",
		Text: strings.Join(lines, "\n\n"),
	}}
}

func cloneToolMeta(meta map[string]any) map[string]any {
	if len(meta) == 0 {
		return nil
	}
	out := make(map[string]any, len(meta))
	for k, v := range meta {
		out[k] = v
	}
	return out
}
