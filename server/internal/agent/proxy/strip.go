package proxy

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// Markers verified against the Claude Code CLI 2.1.283 attachment renderer.
//
// Attachments are converted to *user messages* by the CLI (`Ae()` returns
// `{type:"user", message:{role:"user", ...}}`), so post-compact bloat lives in
// the request's `messages` array — the `system` array never contains it.
// Stripping the wrong array is a silent no-op, which is why these markers are
// pinned by tests.
const (
	// skillPreambleMarker starts the `invoked_skills` attachment rendered after
	// a compaction. The full sentence continues "...(before the conversation
	// was compacted), not on the current turn...".
	skillPreambleMarker = "The following skills were invoked EARLIER in this session"
	// skillSectionMarker prefixes each restored skill body.
	skillSectionMarker = "### Skill:"
	// planPreambleMarker starts the `plan_file_reference` attachment.
	planPreambleMarker = "A plan file exists from plan mode at:"
	// planContentsMarker precedes the plan body, which is injected whole.
	planContentsMarker = "Plan contents:\n"
	// planTailMarker is the sentence the CLI appends after the plan body.
	planTailMarker = "If this plan is relevant to the current work"
)

const (
	// DefaultSkillBudgetBytes caps the total size of restored skill bodies.
	// The CLI itself caps them at 25000 bytes total / 5000 per skill; keeping
	// 20 KB preserves the most recently invoked skills (the CLI sorts them
	// newest-first) while dropping older ones.
	DefaultSkillBudgetBytes = 20 * 1024
	// DefaultPlanBudgetBytes caps the restored plan body, which the CLI
	// injects without any limit.
	DefaultPlanBudgetBytes = 12 * 1024
)

// stripCompactPayload removes post-compaction attachment bloat from the JSON
// body of an Anthropic Messages API request.
//
// It walks `messages` (not `system`) and rewrites only text blocks carrying the
// post-compact markers:
//
//   - `invoked_skills` blocks are trimmed to a shared budget across the whole
//     request, keeping the newest sections (the CLI renders newest-first).
//   - `plan_file_reference` blocks have the unbounded plan body capped.
//
// Everything else — system, tools, tool_result/image blocks, ordinary user and
// assistant text — is left byte-identical. The function is conservative: any
// parse failure returns the original body so the request still succeeds.
//
// It is safe (and intended) to run on every request: the attachments stay in
// the conversation history until the next compaction, so they would otherwise
// be re-sent on every turn. The rewrite is deterministic, which keeps the
// prompt prefix stable for caching.
func stripCompactPayload(body []byte, skillBudgetBytes, planBudgetBytes int) ([]byte, error) {
	// Cheap pre-check: full JSON parsing of a multi-MB body is only worth it
	// when a post-compact marker is actually present.
	if !bytes.Contains(body, []byte(skillPreambleMarker)) &&
		!bytes.Contains(body, []byte(planPreambleMarker)) {
		return body, nil
	}

	var req map[string]json.RawMessage
	if err := json.Unmarshal(body, &req); err != nil {
		return body, nil
	}
	messagesRaw, ok := req["messages"]
	if !ok {
		return body, nil
	}
	var messages []map[string]json.RawMessage
	if err := json.Unmarshal(messagesRaw, &messages); err != nil {
		return body, nil
	}

	changed := false
	skillBudgetLeft := skillBudgetBytes
	for _, msg := range messages {
		contentRaw, ok := msg["content"]
		if !ok {
			continue
		}
		// String content carries no attachment blocks; leave it alone.
		var blocks []map[string]json.RawMessage
		if err := json.Unmarshal(contentRaw, &blocks); err != nil {
			continue
		}
		blockChanged := false
		for _, block := range blocks {
			typRaw, ok := block["type"]
			var typ string
			if !ok || json.Unmarshal(typRaw, &typ) != nil || typ != "text" {
				continue
			}
			textRaw, ok := block["text"]
			if !ok {
				continue
			}
			var text string
			if json.Unmarshal(textRaw, &text) != nil {
				continue
			}

			var newText string
			truncated := false
			switch {
			case strings.Contains(text, skillPreambleMarker):
				var kept int
				newText, kept, truncated = trimSkillsText(text, skillBudgetLeft)
				if truncated {
					skillBudgetLeft -= kept
					if skillBudgetLeft < 0 {
						skillBudgetLeft = 0
					}
				}
			case strings.Contains(text, planPreambleMarker) && strings.Contains(text, planContentsMarker):
				newText, truncated = capPlanText(text, planBudgetBytes)
			}
			if !truncated || newText == text {
				continue
			}
			encoded, err := json.Marshal(newText)
			if err != nil {
				continue
			}
			block["text"] = encoded
			blockChanged = true
		}
		if !blockChanged {
			continue
		}
		encoded, err := json.Marshal(blocks)
		if err != nil {
			continue
		}
		msg["content"] = encoded
		changed = true
	}

	if !changed {
		return body, nil
	}
	newMessages, err := json.Marshal(messages)
	if err != nil {
		return body, nil
	}
	req["messages"] = newMessages

	out, err := json.Marshal(req)
	if err != nil {
		return body, nil
	}
	return out, nil
}

// trimSkillsText keeps skill bodies up to budgetBytes, newest first.
// It returns the rewritten text, how many bytes of skill bodies were kept, and
// whether anything was dropped.
func trimSkillsText(text string, budgetBytes int) (string, int, bool) {
	idx := strings.Index(text, skillSectionMarker)
	if idx < 0 {
		// Marker text without sections: nothing to trim.
		return text, 0, false
	}
	preamble := text[:idx]
	body := text[idx:]

	if budgetBytes <= 0 {
		return preamble + truncationNotice("invoked_skills", 0), 0, true
	}

	parts := strings.Split(body, skillSectionMarker)
	// parts[0] is always empty: body starts with the section marker.
	var b strings.Builder
	b.WriteString(preamble)
	kept := 0
	for i := 1; i < len(parts); i++ {
		section := skillSectionMarker + parts[i]
		if kept+len(section) > budgetBytes {
			b.WriteString(truncationNotice("invoked_skills", budgetBytes))
			return b.String(), kept, true
		}
		b.WriteString(section)
		kept += len(section)
	}
	return text, kept, false
}

// capPlanText limits the plan body between "Plan contents:" and the CLI's
// trailing sentence. The head (path + "Plan contents:") and the trailing
// sentence are always preserved.
func capPlanText(text string, budgetBytes int) (string, bool) {
	start := strings.Index(text, planContentsMarker)
	if start < 0 {
		return text, false
	}
	headEnd := start + len(planContentsMarker)
	head := text[:headEnd]
	rest := text[headEnd:]

	tail := ""
	if i := strings.LastIndex(rest, planTailMarker); i >= 0 {
		tail = rest[i:]
		rest = rest[:i]
	}
	if len(rest) <= budgetBytes {
		return text, false
	}
	return head + cutRunes(rest, budgetBytes) + truncationNotice("plan content", budgetBytes) + tail, true
}

// cutRunes truncates s to at most n bytes without splitting a UTF-8 rune.
func cutRunes(s string, n int) string {
	if n >= len(s) {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func truncationNotice(what string, budgetBytes int) string {
	return "\n[... " + what + " truncated by mindfs compact-strip (budget " +
		formatKB(budgetBytes) + ") ...]\n"
}

func formatKB(n int) string {
	if n <= 0 {
		return "0 KB"
	}
	return formatInt(n/1024) + " KB"
}

func formatInt(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
