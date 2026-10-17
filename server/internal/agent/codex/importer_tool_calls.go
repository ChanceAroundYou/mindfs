package codex

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
)

func parseImportedCodexToolCall(payload map[string]any, ordinal int, sessionShell ...string) (agenttypes.ToolCall, bool) {
	rawType := strings.TrimSpace(asString(payload["type"]))
	callID := strings.TrimSpace(asString(payload["call_id"]))
	if callID == "" {
		callID = strings.TrimSpace(asString(payload["id"]))
	}
	if callID == "" {
		callID = "codex-import-tool-" + strings.TrimSpace(asString(payload["timestamp"]))
	}
	if callID == "codex-import-tool-" {
		callID += fmt.Sprint(ordinal)
	}

	name := strings.TrimSpace(asString(payload["name"]))
	var input any
	switch rawType {
	case "function_call":
		input = payload["arguments"]
	case "custom_tool_call":
		input = payload["input"]
	default:
		return agenttypes.ToolCall{}, false
	}

	wrappedTool := false
	if rawType == "custom_tool_call" && name == "exec" {
		if innerName, innerInput, ok := importedCodexWrappedTool(input); ok {
			name = innerName
			input = innerInput
			wrappedTool = true
		}
	}
	title := name
	kind := importedCodexToolKind(name)
	if kind != agenttypes.ToolKindExecute &&
		kind != agenttypes.ToolKindEdit &&
		kind != agenttypes.ToolKindThink &&
		kind != agenttypes.ToolKindAskUser &&
		!(wrappedTool && kind == agenttypes.ToolKindWebSearch) &&
		!(wrappedTool && kind == agenttypes.ToolKindOther) {
		return agenttypes.ToolCall{}, false
	}
	if title == "" {
		title = rawType
	}

	meta := map[string]any{"rawType": rawType}
	if wrappedTool {
		meta["tool"] = name
	}
	if inputText := importedCodexInputText(input); inputText != "" {
		meta["input"] = inputText
	}
	locations := make([]agenttypes.ToolCallLocation, 0, 1)
	if kind == agenttypes.ToolKindExecute && name == "exec_command" {
		shell := ""
		if len(sessionShell) > 0 {
			shell = sessionShell[0]
		}
		if command, ok := importedCodexExecCommand(input, shell, wrappedTool); ok {
			title = command
			meta["command"] = command
		}
	}
	if decoded := importedCodexInputObject(input); decoded != nil {
		switch kind {
		case agenttypes.ToolKindExecute:
			command := strings.TrimSpace(asString(decoded["command"]))
			if command == "" {
				command = strings.TrimSpace(asString(decoded["cmd"]))
			}
			if command != "" && strings.TrimSpace(asString(meta["command"])) == "" {
				title = command
				meta["command"] = command
			}
		case agenttypes.ToolKindEdit:
			path := strings.TrimSpace(asString(decoded["path"]))
			if path == "" {
				path = strings.TrimSpace(asString(decoded["file_path"]))
			}
			if path != "" {
				locations = append(locations, agenttypes.ToolCallLocation{Path: path})
				if title == "" || title == rawType || title == name {
					title = path
				}
			}
		case agenttypes.ToolKindAskUser:
			if questions := decoded["questions"]; questions != nil {
				meta["questions"] = questions
			}
		case agenttypes.ToolKindWebSearch:
			if query := importedCodexWebQuery(decoded); query != "" {
				title = query
				meta["query"] = query
			}
		}
	}
	status := "running"
	switch strings.ToLower(strings.TrimSpace(asString(payload["status"]))) {
	case "completed", "complete", "success", "succeeded":
		status = "complete"
	case "failed", "error":
		status = "failed"
	}
	content := importedCodexToolInputContent(kind, input)
	if kind == agenttypes.ToolKindEdit {
		if patchLocations, patchContent := parseImportedCodexPatch(input); len(patchLocations) > 0 {
			locations = patchLocations
			content = patchContent
			if len(patchLocations) == 1 {
				title = filepath.Base(patchLocations[0].Path)
			}
		} else if editContent := importedCodexStructuredEditContent(input); len(editContent) > 0 {
			content = editContent
		}
	}
	return agenttypes.ToolCall{
		CallID:    callID,
		Title:     title,
		Status:    status,
		Kind:      kind,
		Content:   content,
		Locations: locations,
		RawType:   rawType,
		Meta:      meta,
	}, true
}

func applyImportedCodexToolOutput(
	items []agenttypes.ImportedExchange,
	locations map[string]importedToolLocation,
	payload map[string]any,
	timestamp time.Time,
) {
	callID := strings.TrimSpace(asString(payload["call_id"]))
	location, ok := locations[callID]
	if !ok || location.ExchangeIndex < 0 || location.ExchangeIndex >= len(items) {
		return
	}
	exchange := &items[location.ExchangeIndex]
	if location.AuxIndex < 0 || location.AuxIndex >= len(exchange.Aux) {
		return
	}
	aux := &exchange.Aux[location.AuxIndex]
	if aux.ToolCall == nil {
		return
	}
	if !timestamp.IsZero() {
		exchange.Timestamp = timestamp
	}
	toolCall := *aux.ToolCall
	rawOutput := payload["output"]
	if rawOutput == nil {
		rawOutput = payload
	}
	output, failed := importedCodexToolOutput(rawOutput)
	if toolCall.Kind == agenttypes.ToolKindAskUser {
		if answers := importedCodexAskUserAnswers(toolCall, rawOutput); len(answers) > 0 {
			if toolCall.Meta == nil {
				toolCall.Meta = make(map[string]any)
			}
			toolCall.Meta["answers"] = answers
		}
	}
	if toolCall.Kind == agenttypes.ToolKindExecute {
		output = cleanImportedCodexExecOutput(output)
	}
	if failed || strings.EqualFold(strings.TrimSpace(asString(payload["status"])), "failed") {
		toolCall.Status = "failed"
	} else {
		toolCall.Status = "complete"
	}
	if strings.TrimSpace(output) != "" {
		if toolCall.Meta == nil {
			toolCall.Meta = make(map[string]any)
		}
		toolCall.Meta["output"] = output
		if toolCall.Kind != agenttypes.ToolKindEdit || len(toolCall.Content) == 0 {
			toolCall.Content = []agenttypes.ToolCallContentItem{{Type: "text", Text: output}}
		}
	}
	aux.ToolCall = &toolCall
}
