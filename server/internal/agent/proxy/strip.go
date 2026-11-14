package proxy

import (
	"bytes"
	"encoding/json"
	"strings"
)

// skillMarker is the rendered prefix of every invoked_skills entry.
const skillMarker = "### Skill:"

// fileRestoreMarker is emitted by the CLI's compact file-restore path.
// The actual format contains the file path as a header; we match the prefix
// that appears in the content block before the file body.
const fileRestoreMarker = "```"

// stripCompactPayload removes excess invoked_skills and file-restore blocks
// from the JSON body of an Anthropic Messages API request.
//
// The function is conservative: if parsing fails at any step, it returns the
// original body unchanged so the request still succeeds.
func stripCompactPayload(body []byte, skillBudgetBytes, maxFileRestoreBlocks int) ([]byte, error) {
	var req map[string]json.RawMessage
	if err := json.Unmarshal(body, &req); err != nil {
		return body, nil
	}

	systemRaw, ok := req["system"]
	if !ok {
		return body, nil
	}

	// system can be a string or an array of content blocks.
	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal(systemRaw, &blocks); err != nil {
		// String form: not expected in modern Claude Code, return unchanged.
		return body, nil
	}

	newBlocks, changed := processBlocks(blocks, skillBudgetBytes, maxFileRestoreBlocks)
	if !changed {
		return body, nil
	}

	newSystemRaw, err := json.Marshal(newBlocks)
	if err != nil {
		return body, nil
	}
	req["system"] = newSystemRaw

	return json.Marshal(req)
}

func processBlocks(
	blocks []map[string]json.RawMessage,
	skillBudgetBytes int,
	maxFileRestoreBlocks int,
) ([]map[string]json.RawMessage, bool) {
	out := make([]map[string]json.RawMessage, 0, len(blocks))
	skillBudgetLeft := skillBudgetBytes
	fileBlocksSeen := 0
	changed := false

	for _, block := range blocks {
		typeRaw, hasType := block["type"]
		if !hasType {
			out = append(out, block)
			continue
		}
		var typ string
		if err := json.Unmarshal(typeRaw, &typ); err != nil || typ != "text" {
			out = append(out, block)
			continue
		}

		textRaw, hasText := block["text"]
		if !hasText {
			out = append(out, block)
			continue
		}
		var text string
		if err := json.Unmarshal(textRaw, &text); err != nil {
			out = append(out, block)
			continue
		}

		// Skills block: contains one or more "### Skill:" sections.
		if strings.Contains(text, skillMarker) {
			trimmed, truncated := trimSkillsBlock(text, skillBudgetLeft)
			skillBudgetLeft -= len(trimmed)
			if skillBudgetLeft < 0 {
				skillBudgetLeft = 0
			}
			if truncated || trimmed == "" {
				changed = true
			}
			if trimmed == "" {
				// Drop the block entirely.
				continue
			}
			if truncated {
				newBlock := cloneBlock(block)
				encoded, _ := json.Marshal(trimmed)
				newBlock["text"] = encoded
				out = append(out, newBlock)
				continue
			}
			out = append(out, block)
			continue
		}

		// File-restore block heuristic: a large text block that starts with a
		// code-fence or file-path header after the compact.  We limit by count.
		if looksLikeFileRestore(text) {
			fileBlocksSeen++
			if fileBlocksSeen > maxFileRestoreBlocks {
				changed = true
				continue // drop excess file-restore blocks
			}
		}

		out = append(out, block)
	}

	return out, changed
}

// trimSkillsBlock keeps skill sections up to budgetBytes total content.
// Returns the (possibly truncated) string and whether truncation happened.
func trimSkillsBlock(text string, budgetBytes int) (string, bool) {
	if budgetBytes <= 0 {
		return "", true
	}
	// Split on "### Skill:" boundaries.
	parts := strings.Split(text, skillMarker)
	// parts[0] is content before the first skill (may be empty preamble).
	var buf bytes.Buffer
	buf.WriteString(parts[0]) // preamble

	for i := 1; i < len(parts); i++ {
		section := skillMarker + parts[i]
		if buf.Len()+len(section) > budgetBytes {
			// Budget exhausted; add a truncation notice.
			buf.WriteString("\n[... invoked_skills truncated by mindfs compact-strip (budget ")
			buf.WriteString(formatKB(budgetBytes))
			buf.WriteString(") ...]")
			return strings.TrimRight(buf.String(), "\n"), true
		}
		buf.WriteString(section)
	}
	return buf.String(), false
}

func looksLikeFileRestore(text string) bool {
	trimmed := strings.TrimSpace(text)
	// File-restore blocks rendered by the CLI start with a path comment or
	// code fence directly (e.g. "```go\n..." or "// path/to/file\n```").
	// They tend to be large and have no "### Skill:" marker.
	if len(trimmed) < 200 {
		return false
	}
	return strings.HasPrefix(trimmed, fileRestoreMarker) ||
		(strings.Contains(trimmed[:min(80, len(trimmed))], "/") &&
			strings.Contains(text, fileRestoreMarker))
}

func cloneBlock(b map[string]json.RawMessage) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(b))
	for k, v := range b {
		out[k] = v
	}
	return out
}

func formatKB(n int) string {
	kb := n / 1024
	if kb == 0 {
		return "< 1 KB"
	}
	return formatInt(kb) + " KB"
}

func formatInt(n int) string {
	b := make([]byte, 0, 8)
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if len(b) == 0 {
		return "0"
	}
	return string(b)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
