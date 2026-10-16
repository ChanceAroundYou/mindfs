package claude

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
)

func extractClaudeMessageText(raw any) string {
	if text := strings.TrimSpace(asString(raw)); text != "" {
		return text
	}
	parts, _ := raw.([]any)
	lines := make([]string, 0, len(parts))
	for _, part := range parts {
		item, _ := part.(map[string]any)
		if item == nil {
			continue
		}
		if strings.TrimSpace(asString(item["type"])) != "text" {
			continue
		}
		if text := strings.TrimSpace(asString(item["text"])); text != "" {
			lines = append(lines, text)
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n\n"))
}

func extractClaudeUserPreview(raw any) string {
	if text := strings.TrimSpace(asString(raw)); text != "" {
		if isMeaningfulClaudeUserText(text) {
			return text
		}
		return ""
	}
	parts, _ := raw.([]any)
	for index := len(parts) - 1; index >= 0; index-- {
		item, _ := parts[index].(map[string]any)
		if item == nil || strings.TrimSpace(asString(item["type"])) != "text" {
			continue
		}
		text := strings.TrimSpace(asString(item["text"]))
		if isMeaningfulClaudeUserText(text) {
			return text
		}
	}
	return ""
}

func extractClaudeImportedUserText(raw any) string {
	if text := strings.TrimSpace(asString(raw)); text != "" {
		if isMeaningfulClaudeUserText(text) {
			return text
		}
		return ""
	}
	parts, _ := raw.([]any)
	texts := make([]string, 0, len(parts))
	for _, value := range parts {
		item, _ := value.(map[string]any)
		if item == nil || strings.TrimSpace(asString(item["type"])) != "text" {
			continue
		}
		text := strings.TrimSpace(asString(item["text"]))
		if isMeaningfulClaudeUserText(text) {
			texts = append(texts, text)
		}
	}
	return strings.TrimSpace(strings.Join(texts, "\n\n"))
}

func extractClaudeToolUseAux(raw any) []agenttypes.ImportedExchangeAux {
	parts, _ := raw.([]any)
	aux := make([]agenttypes.ImportedExchangeAux, 0)
	textParts := make([]string, 0)
	for _, part := range parts {
		item, _ := part.(map[string]any)
		if item == nil {
			continue
		}
		switch strings.TrimSpace(asString(item["type"])) {
		case "text":
			if text := strings.TrimSpace(asString(item["text"])); text != "" {
				textParts = append(textParts, text)
			}
		case "tool_use":
			callID := strings.TrimSpace(asString(item["id"]))
			name := strings.TrimSpace(asString(item["name"]))
			if callID == "" {
				continue
			}
			kind := mapToolKind(name)
			if kind != agenttypes.ToolKindExecute &&
				kind != agenttypes.ToolKindEdit &&
				kind != agenttypes.ToolKindThink &&
				kind != agenttypes.ToolKindAskUser {
				continue
			}
			input, _ := json.Marshal(item["input"])
			toolCall := newRunningToolCall(callID, name, "tool_use", input)
			aux = append(aux, agenttypes.ImportedExchangeAux{
				Line:     importedAssistantLine(strings.Join(textParts, "\n\n")),
				ToolCall: &toolCall,
			})
		}
	}
	return aux
}

func applyClaudeToolResults(
	items []importedExchangeLocator,
	locations map[string]importedToolLocation,
	raw any,
	toolUseResult any,
	timestamp time.Time,
) {
	parts, _ := raw.([]any)
	for _, part := range parts {
		item, _ := part.(map[string]any)
		if item == nil || strings.TrimSpace(asString(item["type"])) != "tool_result" {
			continue
		}
		callID := strings.TrimSpace(asString(item["tool_use_id"]))
		location, ok := locations[callID]
		if !ok || location.ExchangeIndex < 0 || location.ExchangeIndex >= len(items) {
			continue
		}
		exchange := &items[location.ExchangeIndex]
		if location.AuxIndex < 0 || location.AuxIndex >= len(exchange.Aux) {
			continue
		}
		aux := &exchange.Aux[location.AuxIndex]
		if aux.ToolCall == nil {
			continue
		}
		if !timestamp.IsZero() {
			exchange.Timestamp = timestamp
		}
		toolCall := *aux.ToolCall
		output := summarizeToolResult(toolCall.Kind, item["content"])
		if output == "" {
			output = summarizeGenericToolResult(item["content"])
		}
		isError, _ := item["is_error"].(bool)
		if isError {
			toolCall.Status = "failed"
		} else {
			toolCall.Status = "complete"
		}
		if strings.TrimSpace(output) != "" {
			toolCall.Meta = mergeToolCallMeta(toolCall.Meta, map[string]any{"output": output})
			if toolCall.Kind != agenttypes.ToolKindEdit || len(toolCall.Content) == 0 {
				toolCall.Content = []agenttypes.ToolCallContentItem{{Type: "text", Text: output}}
			}
		}
		if toolCall.Kind == agenttypes.ToolKindAskUser {
			if answers := importedClaudeAskUserAnswers(toolCall, toolUseResult); len(answers) > 0 {
				toolCall.Meta = mergeToolCallMeta(toolCall.Meta, map[string]any{"answers": answers})
			}
		}
		aux.ToolCall = &toolCall
	}
}

func importedClaudeAskUserAnswers(toolCall agenttypes.ToolCall, raw any) map[string]string {
	result, _ := raw.(map[string]any)
	rawAnswers, _ := result["answers"].(map[string]any)
	if len(rawAnswers) == 0 {
		return nil
	}
	input := importedClaudeToolInput(toolCall)
	questions, _ := input["questions"].([]any)
	answers := make(map[string]string)
	for index, value := range questions {
		question, _ := value.(map[string]any)
		if question == nil {
			continue
		}
		questionText := strings.TrimSpace(asString(question["question"]))
		if questionText == "" {
			continue
		}
		answer := strings.TrimSpace(asString(rawAnswers[questionText]))
		if answer != "" {
			answers[fmt.Sprintf("q_%d", index)] = answer
		}
	}
	return answers
}

func importedClaudeToolInput(toolCall agenttypes.ToolCall) map[string]any {
	if toolCall.Meta == nil {
		return nil
	}
	raw := strings.TrimSpace(asString(toolCall.Meta["input"]))
	if raw == "" {
		return nil
	}
	var input map[string]any
	if json.Unmarshal([]byte(raw), &input) != nil {
		return nil
	}
	return input
}

func importedAssistantLine(content string) int {
	if content == "" {
		return 0
	}
	return strings.Count(content, "\n") + 1
}

// isAutoContinueAck 判定自动续跑一问一答的应答侧：CLI 在会话空闲时自己插
// 「Continue from where you left off.」→「No response requested.」。提问侧由 isMeta
// 挡掉，应答侧没有 isMeta（assistant 条目的 isMeta 恒为 false），只能按内容判。
func isAutoContinueAck(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n")))
	return strings.TrimSuffix(normalized, ".") == "no response requested"
}

// 中断标记等 CLI 自注入内容的名单与剥离逻辑见 agenttypes.TranscriptNoisePrefixes：
// 一侧定义、导入侧与判重侧共用，避免名单走散。
func isMeaningfulClaudeUserText(text string) bool {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if text == "" {
		return false
	}
	// 整条就是 CLI 标记 → 不是用户输入，整条不发射。标记若与真人正文粘在同一条目里，
	// 剥完不会为空，这里会保留（整条丢掉会连正文一起丢）。
	if agenttypes.IsTranscriptNoiseEntry(text) {
		return false
	}
	lower := strings.ToLower(text)
	if strings.HasPrefix(lower, "<local-command-caveat>") ||
		strings.HasPrefix(lower, "<command-name>") ||
		strings.HasPrefix(lower, "<local-command-stdout>") ||
		strings.HasPrefix(lower, "<local-command-stderr>") ||
		// CLI 的子代理完成通知：整块由 CLI 注入（isMeta 为空，前面那道闸拦不住），
		// 正文是子代理的报告。子代理转录已作为子会话导入（parent_tool_call_id 指向
		// Task 调用），这里再落一条就是「用户气泡里装着助手正文」的乱格式块。
		// 同一件事还有一种带前言的外形：后台任务事件会被包上「SYSTEM NOTIFICATION - NOT
		// USER INPUT」抬头（它自己就写明不是用户输入）。
		strings.HasPrefix(lower, "<task-notification>") ||
		strings.HasPrefix(lower, "[system notification - not user input]") ||
		strings.HasPrefix(lower, "this session was migrated from elsewhere.") ||
		strings.HasPrefix(lower, "this session is being continued from a previous conversation") {
		return false
	}
	if strings.Contains(lower, "<command-message>") || strings.Contains(lower, "<command-args>") {
		return false
	}
	if strings.Contains(lower, "<local-command-stdout>") || strings.Contains(lower, "<local-command-stderr>") {
		return false
	}
	if strings.Contains(lower, "<local-command-caveat>") {
		return false
	}
	if strings.Contains(lower, "\"type\": \"tool_result\"") || strings.Contains(lower, "'type': 'tool_result'") {
		return false
	}
	return true
}

func parseTimeRFC3339(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}
