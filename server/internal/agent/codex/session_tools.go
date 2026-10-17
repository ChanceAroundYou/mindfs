package codex

import (
	"strings"

	types "mindfs/server/internal/agent/types"

	codexsdk "github.com/fanwenlin/codex-go-sdk/codex"
	codextypes "github.com/fanwenlin/codex-go-sdk/types"
)

func mapToolItem(item codexsdk.ThreadItem, started bool) (types.ToolCall, bool) {
	switch v := item.(type) {
	case *codexsdk.CommandExecutionItem:
		status := normalizeStatus(string(v.Status), started)
		content := []types.ToolCallContentItem{}
		if v.AggregatedOutput != nil && strings.TrimSpace(*v.AggregatedOutput) != "" {
			content = append(content, types.ToolCallContentItem{Type: "text", Text: *v.AggregatedOutput})
		}
		meta := map[string]any{
			"rawType": "commandExecution",
			"command": v.Command,
		}
		if strings.TrimSpace(v.Source) != "" {
			meta["source"] = v.Source
		}
		if v.ExitCode != nil {
			meta["exitCode"] = *v.ExitCode
		}
		return types.ToolCall{
			CallID:  v.ID,
			Title:   codexCommandTitle(v.CommandActions, v.Command),
			Status:  status,
			Kind:    types.ToolKindExecute,
			Content: content,
			RawType: "commandExecution",
			Meta:    meta,
		}, true
	case *codexsdk.FileChangeItem:
		status := normalizeStatus(string(v.Status), started)
		locations := make([]types.ToolCallLocation, 0, len(v.Changes))
		content := []types.ToolCallContentItem{}
		for _, change := range v.Changes {
			if strings.TrimSpace(change.Path) == "" {
				continue
			}
			locations = append(locations, types.ToolCallLocation{Path: change.Path})
			if strings.TrimSpace(change.Diff) != "" {
				content = append(content, types.ToolCallContentItem{
					Type:       "text",
					Text:       change.Diff,
					Path:       change.Path,
					ChangeKind: string(change.Kind.Type),
				})
			}
		}
		if strings.TrimSpace(v.Output) != "" {
			content = append(content, types.ToolCallContentItem{Type: "text", Text: v.Output})
		}
		return types.ToolCall{
			CallID:    v.ID,
			Status:    status,
			Kind:      types.ToolKindEdit,
			Locations: locations,
			Content:   content,
			RawType:   "fileChange",
		}, true
	case *codexsdk.McpToolCallItem:
		status := normalizeStatus(string(v.Status), started)
		meta := map[string]any{
			"rawType": "mcpToolCall",
			"server":  v.Server,
			"tool":    v.Tool,
		}
		return types.ToolCall{
			CallID:  v.ID,
			Title:   firstNonEmpty(v.Tool, "mcp_tool"),
			Status:  status,
			Kind:    types.ToolKindOther,
			Content: errorContent(v.Error),
			RawType: "mcpToolCall",
			Meta:    meta,
		}, true
	case *codexsdk.WebSearchItem:
		query := strings.TrimSpace(v.Query)
		content := []types.ToolCallContentItem{}
		if query != "" {
			content = append(content, types.ToolCallContentItem{Type: "text", Text: "**Query:** " + query})
		}
		return types.ToolCall{
			CallID:  v.ID,
			Title:   firstNonEmpty(query, "web search"),
			Status:  normalizeStatus("", started),
			Kind:    types.ToolKindWebSearch,
			Content: content,
			RawType: "webSearch",
			Meta: map[string]any{
				"rawType": "webSearch",
				"query":   query,
			},
		}, true
	case *codexsdk.ErrorItem:
		message := strings.TrimSpace(v.Message)
		if message == "" {
			message = "codex item error"
		}
		return types.ToolCall{
			CallID:  v.ID,
			Title:   "error",
			Status:  "failed",
			Kind:    types.ToolKindOther,
			Content: []types.ToolCallContentItem{{Type: "text", Text: message}},
			RawType: "error",
			Meta: map[string]any{
				"rawType": "error",
			},
		}, true
	case *codexsdk.CollabToolCallItem:
		status := normalizeStatus(v.Status, started)
		meta := map[string]any{
			"rawType":           "collabToolCall",
			"type":              v.Type,
			"tool":              v.Tool,
			"status":            status,
			"senderThreadId":    v.SenderThreadID,
			"receiverThreadIds": append([]string(nil), v.ReceiverThreadIDs...),
			"agentsStates":      v.AgentsStates,
		}
		if prompt := stringPtrValue(v.Prompt); prompt != "" {
			meta["prompt"] = prompt
		}
		if model := stringPtrValue(v.Model); model != "" {
			meta["model"] = model
		}
		if effort := stringPtrValue(v.ReasoningEffort); effort != "" {
			meta["reasoningEffort"] = effort
		}
		if v.Arguments != nil {
			meta["arguments"] = v.Arguments
		}
		if v.Result != nil {
			meta["result"] = v.Result
		}
		content := collabToolContent(v)
		content = append(content, errorContent(v.Error)...)
		return types.ToolCall{
			CallID:  v.ID,
			Title:   collabToolTitle(v),
			Status:  status,
			Kind:    types.ToolKindTask,
			Content: content,
			RawType: "collabToolCall",
			Meta:    meta,
		}, true
	case *codextypes.UnknownItem:
		return mapUnknownToolItem(v, started)
	default:
		return types.ToolCall{}, false
	}
}

func codexCommandTitle(actions []codexsdk.CommandAction, command string) string {
	command = firstNonEmpty(strings.TrimSpace(command), "command")
	if len(actions) != 1 {
		for _, action := range actions {
			if action.Type == codexsdk.CommandActionTypeUnknown {
				return command
			}
		}
		if len(actions) > 1 {
			return "Explore files · " + command
		}
		return command
	}

	action := actions[0]
	path := ""
	if action.Path != nil {
		path = strings.TrimSpace(*action.Path)
	}
	switch action.Type {
	case codexsdk.CommandActionTypeRead:
		name := firstNonEmpty(strings.TrimSpace(action.Name), path)
		if name != "" {
			return "Read " + name + " · " + command
		}
		return "Read file · " + command
	case codexsdk.CommandActionTypeListFiles:
		if path != "" {
			return "List files in " + path + " · " + command
		}
		return "List files · " + command
	case codexsdk.CommandActionTypeSearch:
		query := ""
		if action.Query != nil {
			query = strings.TrimSpace(*action.Query)
		}
		switch {
		case query != "" && path != "":
			return "Search for " + query + " in " + path + " · " + command
		case query != "":
			return "Search for " + query + " · " + command
		case path != "":
			return "Search in " + path + " · " + command
		default:
			return "Search files · " + command
		}
	default:
		return command
	}
}
