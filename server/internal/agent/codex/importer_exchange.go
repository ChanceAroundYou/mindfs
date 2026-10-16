package codex

import (
	"errors"
	"strings"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
)

func extractImportedCodexProposedPlan(text string) (string, []agenttypes.ImportedExchangeAux) {
	const openTag = "<proposed_plan>"
	const closeTag = "</proposed_plan>"
	start := strings.Index(text, openTag)
	if start < 0 {
		return text, nil
	}
	contentStart := start + len(openTag)
	closeOffset := strings.Index(text[contentStart:], closeTag)
	if closeOffset < 0 {
		return text, nil
	}
	end := contentStart + closeOffset
	planContent := strings.TrimSpace(text[contentStart:end])
	if planContent == "" {
		return text, nil
	}
	before := strings.TrimSpace(text[:start])
	after := strings.TrimSpace(text[end+len(closeTag):])
	remaining := strings.TrimSpace(strings.Join(nonEmptyStrings(before, after), "\n\n"))
	return remaining, []agenttypes.ImportedExchangeAux{{
		Line: importedCodexAssistantLine(before),
		Plan: &agenttypes.PlanUpdate{Content: planContent},
	}}
}

func nonEmptyStrings(values ...string) []string {
	items := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			items = append(items, value)
		}
	}
	return items
}

var errStopJSONL = errors.New("stop jsonl")

func extractCodexMessageText(raw any) string {
	parts, _ := raw.([]any)
	lines := make([]string, 0, len(parts))
	for _, part := range parts {
		item, _ := part.(map[string]any)
		if item == nil {
			continue
		}
		switch strings.TrimSpace(asString(item["type"])) {
		case "input_text", "output_text", "text":
			if text := strings.TrimSpace(asString(item["text"])); text != "" {
				lines = append(lines, text)
			}
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n\n"))
}

func extractCodexUserPreview(raw any) string {
	parts, _ := raw.([]any)
	for index := len(parts) - 1; index >= 0; index-- {
		item, _ := parts[index].(map[string]any)
		if item == nil {
			continue
		}
		switch strings.TrimSpace(asString(item["type"])) {
		case "input_text", "text":
			text := strings.TrimSpace(asString(item["text"]))
			if isMeaningfulCodexUserText(text) {
				return text
			}
		}
	}
	return ""
}

func extractCodexImportedUserText(raw any) string {
	if text := strings.TrimSpace(asString(raw)); text != "" {
		if isMeaningfulCodexUserText(text) {
			return text
		}
		return ""
	}
	parts, _ := raw.([]any)
	texts := make([]string, 0, len(parts))
	for _, value := range parts {
		item, _ := value.(map[string]any)
		if item == nil {
			continue
		}
		switch strings.TrimSpace(asString(item["type"])) {
		case "input_text", "text":
			text := strings.TrimSpace(asString(item["text"]))
			if isMeaningfulCodexUserText(text) {
				texts = append(texts, text)
			}
		}
	}
	return strings.TrimSpace(strings.Join(texts, "\n\n"))
}

func isMeaningfulCodexUserText(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	for _, tags := range [][2]string{
		{"<recommended_plugins>", "</recommended_plugins>"},
		{"<environment_context>", "</environment_context>"},
		{"<permissions instructions>", "</permissions instructions>"},
		{"<skills_instructions>", "</skills_instructions>"},
		{"<apps_instructions>", "</apps_instructions>"},
		{"<plugins_instructions>", "</plugins_instructions>"},
	} {
		if strings.HasPrefix(text, tags[0]) && strings.HasSuffix(text, tags[1]) {
			return false
		}
	}
	if strings.HasPrefix(text, "# AGENTS.md instructions") && strings.HasSuffix(text, "</INSTRUCTIONS>") {
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
