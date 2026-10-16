package codex

import (
	"encoding/json"
	"strings"

	agenttypes "mindfs/server/internal/agent/types"
)

func quoteImportedShellCommand(command string) string {
	return "'" + strings.ReplaceAll(command, "'", "'\\''") + "'"
}

func parseImportedCodexPatch(input any) ([]agenttypes.ToolCallLocation, []agenttypes.ToolCallContentItem) {
	patch := importedCodexPatchText(input)
	if patch == "" {
		return nil, nil
	}

	type patchSection struct {
		kind    string
		path    string
		oldPath string
		lines   []string
	}

	var sections []patchSection
	var current *patchSection
	flush := func() {
		if current == nil || strings.TrimSpace(current.path) == "" {
			current = nil
			return
		}
		sections = append(sections, *current)
		current = nil
	}

	for _, line := range strings.Split(strings.ReplaceAll(patch, "\r\n", "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "*** Add File: "):
			flush()
			current = &patchSection{kind: "add", path: strings.TrimSpace(strings.TrimPrefix(line, "*** Add File: "))}
		case strings.HasPrefix(line, "*** Delete File: "):
			flush()
			current = &patchSection{kind: "delete", path: strings.TrimSpace(strings.TrimPrefix(line, "*** Delete File: "))}
		case strings.HasPrefix(line, "*** Update File: "):
			flush()
			path := strings.TrimSpace(strings.TrimPrefix(line, "*** Update File: "))
			current = &patchSection{kind: "update", path: path, oldPath: path}
		case strings.HasPrefix(line, "*** Move to: "):
			if current != nil {
				nextPath := strings.TrimSpace(strings.TrimPrefix(line, "*** Move to: "))
				if nextPath != "" {
					current.path = nextPath
				}
			}
		case line == "*** Begin Patch", line == "*** End Patch", line == "*** End of File":
			// Container markers are not part of a file diff.
		default:
			if current != nil {
				current.lines = append(current.lines, line)
			}
		}
	}
	flush()

	locations := make([]agenttypes.ToolCallLocation, 0, len(sections))
	content := make([]agenttypes.ToolCallContentItem, 0, len(sections))
	for _, section := range sections {
		locations = append(locations, agenttypes.ToolCallLocation{Path: section.path})
		switch section.kind {
		case "add":
			content = append(content, agenttypes.ToolCallContentItem{
				Type:       "text",
				Text:       stripImportedPatchPrefixes(section.lines, "+"),
				Path:       section.path,
				ChangeKind: "add",
			})
		case "delete":
			content = append(content, agenttypes.ToolCallContentItem{
				Type:       "text",
				Text:       stripImportedPatchPrefixes(section.lines, "-"),
				Path:       section.path,
				ChangeKind: "delete",
			})
		default:
			oldPath := section.oldPath
			if oldPath == "" {
				oldPath = section.path
			}
			diffLines := []string{"--- a/" + oldPath, "+++ b/" + section.path}
			diffLines = append(diffLines, section.lines...)
			content = append(content, agenttypes.ToolCallContentItem{
				Type:       "text",
				Text:       strings.Join(diffLines, "\n"),
				Path:       section.path,
				ChangeKind: "update",
			})
		}
	}
	return locations, content
}

func importedCodexPatchText(input any) string {
	text := strings.TrimSpace(asString(input))
	if decoded := importedCodexInputObject(input); decoded != nil {
		for _, key := range []string{"patch", "input"} {
			if candidate := strings.TrimSpace(asString(decoded[key])); candidate != "" {
				return candidate
			}
		}
	}
	return text
}

func stripImportedPatchPrefixes(lines []string, prefix string) string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(line, prefix) {
			line = strings.TrimPrefix(line, prefix)
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func importedCodexInputText(input any) string {
	if input == nil {
		return ""
	}
	if text := strings.TrimSpace(asString(input)); text != "" {
		return text
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return ""
	}
	text := strings.TrimSpace(string(encoded))
	if text == "null" {
		return ""
	}
	return text
}

func importedCodexInputObject(input any) map[string]any {
	if object, ok := input.(map[string]any); ok {
		return object
	}
	text := strings.TrimSpace(asString(input))
	if text == "" {
		return nil
	}
	var object map[string]any
	if json.Unmarshal([]byte(text), &object) != nil {
		return nil
	}
	return object
}

func importedCodexToolOutput(raw any) (string, bool) {
	if blocks, ok := raw.([]any); ok {
		return importedCodexOutputBlockText(blocks), false
	}
	text := strings.TrimSpace(asString(raw))
	if text == "" {
		if encoded, err := json.Marshal(raw); err == nil {
			text = strings.TrimSpace(string(encoded))
		}
	}
	if text == "" || text == "null" {
		return "", false
	}
	var blocks []any
	if json.Unmarshal([]byte(text), &blocks) == nil {
		return importedCodexOutputBlockText(blocks), false
	}
	var decoded map[string]any
	if json.Unmarshal([]byte(text), &decoded) == nil {
		failed := false
		if value, ok := decoded["is_error"].(bool); ok {
			failed = value
		}
		if exitCode, ok := decoded["exit_code"].(float64); ok && exitCode != 0 {
			failed = true
		}
		if metadata, ok := decoded["metadata"].(map[string]any); ok {
			if exitCode, ok := metadata["exit_code"].(float64); ok && exitCode != 0 {
				failed = true
			}
		}
		if errText := strings.TrimSpace(asString(decoded["error"])); errText != "" {
			failed = true
		}
		if output := strings.TrimSpace(asString(decoded["output"])); output != "" {
			return output, failed
		}
		return text, failed
	}
	return text, false
}

func importedCodexOutputBlockText(blocks []any) string {
	var output strings.Builder
	for _, block := range blocks {
		item, _ := block.(map[string]any)
		if item == nil {
			continue
		}
		text := asString(item["text"])
		if text == "" {
			continue
		}
		if output.Len() > 0 {
			current := output.String()
			if !strings.HasSuffix(current, "\n") && !strings.HasPrefix(text, "\n") {
				output.WriteByte('\n')
			}
		}
		output.WriteString(text)
	}
	return strings.TrimSpace(output.String())
}

func cleanImportedCodexExecOutput(output string) string {
	normalized := strings.ReplaceAll(output, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	if len(lines) >= 3 &&
		strings.TrimSpace(lines[0]) == "Script completed" &&
		strings.HasPrefix(strings.TrimSpace(lines[1]), "Wall time ") &&
		strings.TrimSpace(lines[2]) == "Output:" {
		return strings.TrimSpace(strings.Join(lines[3:], "\n"))
	}
	return strings.TrimSpace(normalized)
}

func importedCodexAssistantLine(content string) int {
	if content == "" {
		return 0
	}
	return strings.Count(content, "\n") + 1
}
