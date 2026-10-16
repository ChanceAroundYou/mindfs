package codex

import (
	"encoding/json"
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

func importedCodexAskUserAnswers(toolCall agenttypes.ToolCall, raw any) map[string]string {
	output := importedCodexInputObject(raw)
	rawAnswers, _ := output["answers"].(map[string]any)
	if len(rawAnswers) == 0 {
		return nil
	}
	input := importedCodexInputObject(toolCall.Meta["input"])
	questions, _ := input["questions"].([]any)
	answers := make(map[string]string)
	for index, value := range questions {
		question, _ := value.(map[string]any)
		if question == nil {
			continue
		}
		questionID := strings.TrimSpace(asString(question["id"]))
		if questionID == "" {
			continue
		}
		answer := importedCodexAskUserAnswerText(rawAnswers[questionID])
		if answer != "" {
			answers[fmt.Sprintf("q_%d", index)] = answer
		}
	}
	return answers
}

func importedCodexAskUserAnswerText(raw any) string {
	switch value := raw.(type) {
	case string:
		return strings.TrimSpace(value)
	case []any:
		items := make([]string, 0, len(value))
		for _, item := range value {
			if text := importedCodexAskUserAnswerText(item); text != "" {
				items = append(items, text)
			}
		}
		return strings.Join(items, ", ")
	case map[string]any:
		for _, key := range []string{"answers", "answer", "value"} {
			if text := importedCodexAskUserAnswerText(value[key]); text != "" {
				return text
			}
		}
	}
	return ""
}

func rebuildImportedCodexToolLocations(items []agenttypes.ImportedExchange) map[string]importedToolLocation {
	locations := make(map[string]importedToolLocation)
	for exchangeIndex := range items {
		for auxIndex := range items[exchangeIndex].Aux {
			toolCall := items[exchangeIndex].Aux[auxIndex].ToolCall
			if toolCall == nil || strings.TrimSpace(toolCall.CallID) == "" {
				continue
			}
			locations[strings.TrimSpace(toolCall.CallID)] = importedToolLocation{
				ExchangeIndex: exchangeIndex,
				AuxIndex:      auxIndex,
			}
		}
	}
	return locations
}

func importedCodexToolKind(name string) agenttypes.ToolKind {
	normalized := strings.ToLower(strings.TrimSpace(name))
	switch {
	case normalized == "apply_patch" || strings.Contains(normalized, "edit") ||
		strings.Contains(normalized, "write_file"):
		return agenttypes.ToolKindEdit
	case normalized == "exec" || normalized == "exec_command" ||
		normalized == "write_stdin" || strings.Contains(normalized, "shell") ||
		strings.Contains(normalized, "command"):
		return agenttypes.ToolKindExecute
	case normalized == "web__run" || normalized == "web_search":
		return agenttypes.ToolKindWebSearch
	case normalized == "update_plan" || normalized == "plan":
		return agenttypes.ToolKindThink
	case strings.Contains(normalized, "request_user_input") ||
		strings.Contains(normalized, "ask_user"):
		return agenttypes.ToolKindAskUser
	default:
		return agenttypes.ToolKindOther
	}
}

func importedCodexToolInputContent(kind agenttypes.ToolKind, input any) []agenttypes.ToolCallContentItem {
	if kind != agenttypes.ToolKindEdit {
		return nil
	}
	text := strings.TrimSpace(asString(input))
	if text == "" {
		if encoded, err := json.Marshal(input); err == nil {
			text = strings.TrimSpace(string(encoded))
		}
	}
	if text == "" || text == "null" {
		return nil
	}
	return []agenttypes.ToolCallContentItem{{Type: "text", Text: text}}
}

func importedCodexStructuredEditContent(input any) []agenttypes.ToolCallContentItem {
	decoded := importedCodexInputObject(input)
	if decoded == nil {
		return nil
	}
	path := strings.TrimSpace(asString(decoded["path"]))
	if path == "" {
		path = strings.TrimSpace(asString(decoded["file_path"]))
	}
	if path == "" {
		return nil
	}

	oldText, hasOldText := decoded["old_string"].(string)
	newText, hasNewText := decoded["new_string"].(string)
	if hasOldText || hasNewText {
		return []agenttypes.ToolCallContentItem{{
			Type:    "diff",
			Path:    path,
			OldText: &oldText,
			NewText: newText,
		}}
	}
	if text, ok := decoded["content"].(string); ok {
		return []agenttypes.ToolCallContentItem{{
			Type:       "text",
			Text:       text,
			Path:       path,
			ChangeKind: "add",
		}}
	}
	return nil
}

func importedCodexExecCommand(input any, sessionShell string, includeShell bool) (string, bool) {
	args := importedCodexInputObject(input)
	if args == nil {
		return "", false
	}
	command := strings.TrimSpace(asString(args["cmd"]))
	if command == "" {
		return "", false
	}
	shell := strings.TrimSpace(asString(args["shell"]))
	if shell == "" {
		shell = strings.TrimSpace(sessionShell)
	}
	flag := "-lc"
	if login, ok := args["login"].(bool); ok && !login {
		flag = "-c"
	}
	if shell == "" || !includeShell {
		return command, true
	}
	return shell + " " + flag + " " + quoteImportedShellCommand(command), true
}

func importedCodexWebQuery(input map[string]any) string {
	for _, key := range []string{"search_query", "image_query"} {
		items, _ := input[key].([]any)
		for _, item := range items {
			entry, _ := item.(map[string]any)
			if query := strings.TrimSpace(asString(entry["q"])); query != "" {
				return query
			}
		}
	}
	items, _ := input["open"].([]any)
	for _, item := range items {
		entry, _ := item.(map[string]any)
		if refID := strings.TrimSpace(asString(entry["ref_id"])); refID != "" {
			return refID
		}
	}
	return ""
}

func importedCodexWorldStateShell(raw map[string]any) string {
	payload, _ := raw["payload"].(map[string]any)
	state, _ := payload["state"].(map[string]any)
	environmentsState, _ := state["environments"].(map[string]any)
	environments, _ := environmentsState["environments"].(map[string]any)
	local, _ := environments["local"].(map[string]any)
	return strings.TrimSpace(asString(local["shell"]))
}

func importedCodexWrappedTool(input any) (string, any, bool) {
	script := strings.TrimSpace(asString(input))
	callIndex := importedCodexToolCallIndex(script)
	if callIndex < 0 {
		return "", nil, false
	}
	nameStart := callIndex + len("tools.")
	nameEnd := nameStart
	for nameEnd < len(script) && isImportedJavaScriptIdentifierByte(script[nameEnd]) {
		nameEnd++
	}
	name := strings.TrimSpace(script[nameStart:nameEnd])
	if name == "" {
		return "", nil, false
	}
	index := nameEnd
	for index < len(script) && (script[index] == ' ' || script[index] == '\t' || script[index] == '\r' || script[index] == '\n') {
		index++
	}
	if index >= len(script) || script[index] != '(' {
		return "", nil, false
	}
	index++
	for index < len(script) && (script[index] == ' ' || script[index] == '\t' || script[index] == '\r' || script[index] == '\n') {
		index++
	}
	if index >= len(script) {
		return name, nil, true
	}
	switch script[index] {
	case '{':
		literal, ok := importedCodexBalancedJavaScriptValue(script, index, '{', '}')
		if !ok {
			return name, nil, true
		}
		var decoded map[string]any
		if json.Unmarshal([]byte(quoteImportedJavaScriptObjectKeys(literal)), &decoded) == nil {
			return name, decoded, true
		}
	case '"', '\'', '`':
		if value, _, ok := importedCodexJavaScriptString(script, index); ok {
			return name, value, true
		}
	default:
		end := index
		for end < len(script) && isImportedJavaScriptIdentifierByte(script[end]) {
			end++
		}
		identifier := script[index:end]
		if identifier != "" {
			if value, ok := importedCodexJavaScriptVariable(script[:callIndex], identifier); ok {
				return name, value, true
			}
		}
	}
	return name, nil, true
}
