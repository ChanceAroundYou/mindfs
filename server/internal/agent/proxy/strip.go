package proxy

import (
	"bytes"
	"encoding/json"
	"strings"
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

// stripCompactPayload removes post-compaction attachment bloat from the JSON
// body of an Anthropic Messages API request.
//
// It walks `messages` (not `system`) and rewrites only text blocks carrying the
// post-compact markers:
//
//   - `invoked_skills` blocks are deleted outright (the whole block, and the
//     message too when nothing else is left in it). The CLI restores up to
//     25000 tokens of skill bodies per compaction, and the attachment then
//     stays in history and is re-sent on every turn. The bodies are only
//     "for context" pointers; dropping them removes the repeated cost without
//     losing the skill definitions, which remain on disk (the `### Skill:`
//     sections carry their paths).
//   - `plan_file_reference` blocks are reduced to the path line alone
//     (`A plan file exists from plan mode at: <path>`). The CLI injects the
//     plan body uncapped; the model can read the file when it needs it.
//
// Everything else — system, tools, tool_result/image blocks, ordinary user and
// assistant text — is left byte-identical. The function is conservative: any
// parse failure returns the original body so the request still succeeds, and
// nothing is rewritten when no marker is present.
//
// It is safe (and intended) to run on every request: the attachments stay in
// the conversation history until the next compaction, so they would otherwise
// be re-sent on every turn. The rewrite is deterministic, which keeps the
// prompt prefix stable for caching.
func stripCompactPayload(body []byte) []byte {
	// Cheap pre-check: full JSON parsing of a multi-MB body is only worth it
	// when a post-compact marker is actually present.
	if !bytes.Contains(body, []byte(skillPreambleMarker)) &&
		!bytes.Contains(body, []byte(planPreambleMarker)) {
		return body
	}

	var req map[string]json.RawMessage
	if err := json.Unmarshal(body, &req); err != nil {
		return body
	}
	messagesRaw, ok := req["messages"]
	if !ok {
		return body
	}
	var messages []map[string]json.RawMessage
	if err := json.Unmarshal(messagesRaw, &messages); err != nil {
		return body
	}

	changed := false
	keptMessages := make([]map[string]json.RawMessage, 0, len(messages))
	for _, msg := range messages {
		contentRaw, ok := msg["content"]
		if !ok {
			keptMessages = append(keptMessages, msg)
			continue
		}
		// String content carries no attachment blocks; leave it alone.
		var blocks []map[string]json.RawMessage
		if err := json.Unmarshal(contentRaw, &blocks); err != nil {
			keptMessages = append(keptMessages, msg)
			continue
		}

		blockChanged := false
		dropped := false
		keptBlocks := make([]map[string]json.RawMessage, 0, len(blocks))
		for _, block := range blocks {
			typRaw, ok := block["type"]
			var typ string
			if !ok || json.Unmarshal(typRaw, &typ) != nil || typ != "text" {
				keptBlocks = append(keptBlocks, block)
				continue
			}
			textRaw, ok := block["text"]
			if !ok {
				keptBlocks = append(keptBlocks, block)
				continue
			}
			var text string
			if json.Unmarshal(textRaw, &text) != nil {
				keptBlocks = append(keptBlocks, block)
				continue
			}

			switch {
			case strings.Contains(text, skillPreambleMarker):
				// Whole block goes away. Everything it says is a historical
				// pointer to a skill file whose body is already on disk.
				dropped = true
				continue
			case strings.Contains(text, planPreambleMarker) && strings.Contains(text, planContentsMarker):
				newText, reduced := planPathLine(text)
				if reduced {
					encoded, err := json.Marshal(newText)
					if err != nil {
						keptBlocks = append(keptBlocks, block)
						continue
					}
					block["text"] = encoded
					blockChanged = true
				}
			}
			keptBlocks = append(keptBlocks, block)
		}

		if dropped || blockChanged {
			encoded, err := json.Marshal(keptBlocks)
			if err != nil {
				keptMessages = append(keptMessages, msg)
				continue
			}
			changed = true
			if dropped && len(keptBlocks) == 0 {
				// The message existed only to carry the attachment.
				continue
			}
			msg["content"] = encoded
		}
		keptMessages = append(keptMessages, msg)
	}

	if !changed {
		return body
	}
	newMessages, err := json.Marshal(keptMessages)
	if err != nil {
		return body
	}
	req["messages"] = newMessages

	out, err := json.Marshal(req)
	if err != nil {
		return body
	}
	return out
}

// planPathLine reduces a `plan_file_reference` block to its first line, which
// is `A plan file exists from plan mode at: <path>`. The plan body and the
// CLI's trailing sentence are dropped. Already-reduced text has no newline and
// is returned unchanged, so repeated passes are no-ops.
func planPathLine(text string) (string, bool) {
	i := strings.IndexByte(text, '\n')
	if i < 0 {
		return text, false
	}
	return strings.TrimRight(text[:i], " \t"), true
}
