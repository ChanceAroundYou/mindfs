package claude

import (
	"encoding/json"
	"fmt"
	"strings"

	types "mindfs/server/internal/agent/types"

	claudeagent "github.com/roasbeef/claude-agent-sdk-go"
)

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
