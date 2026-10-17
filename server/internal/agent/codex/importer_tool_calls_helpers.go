package codex

import (
	"encoding/json"
	"fmt"
	"strings"

	agenttypes "mindfs/server/internal/agent/types"
)

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
