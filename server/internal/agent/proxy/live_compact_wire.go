package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"hash/fnv"
	"strings"
)

// This file parses just enough of an Anthropic Messages request to plan a
// live-compact fold. Every message and block keeps its raw JSON: the rewrite
// (Phase B1) replaces only the text of a tool_result block and must leave every
// other byte alone.
//
// Phase A uses the same parser in shadow mode and only counts what it would
// fold, so the parser and its invariants are exercised on real traffic before
// any request is rewritten.

type wireRequest struct {
	raw      map[string]json.RawMessage
	messages []wireMessage
}

type wireMessage struct {
	raw    json.RawMessage
	role   string
	blocks []wireBlock
}

type wireBlock struct {
	raw       json.RawMessage
	index     int
	typ       string
	toolUseID string // tool_use: its own id; tool_result: the id it answers
	toolName  string // tool_use name
	text      string // text block body, or the concatenated text of a tool_result
	isError   bool   // tool_result.is_error
	nonText   bool   // tool_result carries an image/document/unknown block
}

// wireToolRound groups one assistant tool_use message with the user message(s)
// that answer each of its tool calls. A round is closed only when every call
// has a result; open rounds are never fold candidates.
type wireToolRound struct {
	assistantIndex int
	toolNames      map[string]string
	resultIndexes  []int
	closeIndex     int
	pending        int
	closed         bool
}

type foldCandidate struct {
	MessageIndex int
	BlockIndex   int
	ToolUseID    string
	ToolName     string
	Tokens       int
	Bytes        int
	// ErrorText marks a successful (is_error=false) result whose text still
	// looks like a failure report. It is foldable, but the pointer keeps a
	// wider head/tail excerpt so the diagnostic stays readable.
	ErrorText bool
}

type wireAnalysis struct {
	Messages             int
	Rounds               int
	ClosedRounds         int
	Candidates           []foldCandidate
	ErrorCandidates      int
	CandidateTokens      int
	CandidateBytes       int
	EstimatedInputTokens int
}

func parseWireRequest(body []byte) (*wireRequest, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	messagesRaw, ok := raw["messages"]
	if !ok {
		return nil, errors.New("no messages")
	}
	var messagePayloads []json.RawMessage
	if err := json.Unmarshal(messagesRaw, &messagePayloads); err != nil {
		return nil, err
	}

	req := &wireRequest{raw: raw, messages: make([]wireMessage, 0, len(messagePayloads))}
	for _, payload := range messagePayloads {
		msg, err := parseWireMessage(payload)
		if err != nil {
			return nil, err
		}
		req.messages = append(req.messages, msg)
	}
	return req, nil
}

func parseWireMessage(payload json.RawMessage) (wireMessage, error) {
	var head struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(payload, &head); err != nil {
		return wireMessage{}, err
	}
	msg := wireMessage{raw: payload, role: head.Role}

	trimmed := bytes.TrimSpace(head.Content)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return msg, nil
	}
	// Plain string content: one implicit text block and no tool structures.
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return wireMessage{}, err
		}
		msg.blocks = []wireBlock{{index: 0, typ: "text", text: text, raw: head.Content}}
		return msg, nil
	}

	var blockPayloads []json.RawMessage
	if err := json.Unmarshal(trimmed, &blockPayloads); err != nil {
		return wireMessage{}, err
	}
	msg.blocks = make([]wireBlock, 0, len(blockPayloads))
	for index, blockPayload := range blockPayloads {
		block, err := parseWireBlock(blockPayload, index)
		if err != nil {
			return wireMessage{}, err
		}
		msg.blocks = append(msg.blocks, block)
	}
	return msg, nil
}

func parseWireBlock(payload json.RawMessage, index int) (wireBlock, error) {
	var head struct {
		Type      string          `json:"type"`
		Text      string          `json:"text"`
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		ToolUseID string          `json:"tool_use_id"`
		IsError   bool            `json:"is_error"`
		Content   json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(payload, &head); err != nil {
		return wireBlock{}, err
	}
	block := wireBlock{
		raw:       payload,
		index:     index,
		typ:       head.Type,
		toolUseID: head.ID,
		toolName:  head.Name,
		text:      head.Text,
		isError:   head.IsError,
	}
	switch head.Type {
	case "tool_result":
		block.toolUseID = head.ToolUseID
		text, nonText, err := toolResultText(head.Content)
		if err != nil {
			return wireBlock{}, err
		}
		block.text = text
		block.nonText = nonText
	case "text":
		// handled by head.Text
	default:
		// thinking / redacted_thinking / image / document / unknown.
		// These are never rewritten; marking them non-text keeps a
		// surrounding tool_result out of the candidate set.
		block.nonText = true
	}
	return block, nil
}

// toolResultText flattens the two wire shapes of tool_result.content:
// a plain string, or an array of blocks where text is the only foldable part.
func toolResultText(raw json.RawMessage) (text string, nonText bool, err error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", false, nil
	}
	if trimmed[0] == '"' {
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return "", false, err
		}
		return text, false, nil
	}
	var blocks []struct {
		Type string          `json:"type"`
		Text string          `json:"text"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(trimmed, &blocks); err != nil {
		return "", false, err
	}
	var builder bytes.Buffer
	for _, block := range blocks {
		if block.Type != "text" {
			nonText = true
			continue
		}
		if builder.Len() > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(block.Text)
	}
	return builder.String(), nonText, nil
}

// pairToolRounds returns rounds in assistant order. A round stays open until
// every tool_use id has a tool_result in a later user message.
func pairToolRounds(messages []wireMessage) []*wireToolRound {
	rounds := make([]*wireToolRound, 0, 8)
	byToolID := make(map[string]*wireToolRound)
	for index, msg := range messages {
		if msg.role == "assistant" {
			var round *wireToolRound
			for _, block := range msg.blocks {
				if block.typ != "tool_use" || block.toolUseID == "" {
					continue
				}
				if round == nil {
					round = &wireToolRound{
						assistantIndex: index,
						toolNames:      make(map[string]string),
					}
				}
				round.toolNames[block.toolUseID] = block.toolName
				round.pending++
				byToolID[block.toolUseID] = round
			}
			if round != nil {
				rounds = append(rounds, round)
			}
			continue
		}
		if msg.role != "user" {
			continue
		}
		seen := make(map[int]bool)
		for _, block := range msg.blocks {
			if block.typ != "tool_result" {
				continue
			}
			round, ok := byToolID[block.toolUseID]
			if !ok || round.closed {
				continue
			}
			if !seen[round.assistantIndex] {
				round.resultIndexes = append(round.resultIndexes, index)
				seen[round.assistantIndex] = true
			}
			round.pending--
			if round.pending <= 0 {
				round.closed = true
				round.closeIndex = index
			}
		}
	}
	return rounds
}

// analyzeWire plans a fold without changing anything.
//
// Candidate rules (Phase B1):
//   - only closed tool rounds outside the newest keepRounds rounds;
//   - never a failed result (is_error or error-looking content);
//   - only pure-text results (images/documents keep the block untouched);
//   - only results estimated at or above minBlockTokens.
func analyzeWire(req *wireRequest, keepRounds, minBlockTokens int) wireAnalysis {
	if keepRounds < 0 {
		keepRounds = 0
	}
	if minBlockTokens < 0 {
		minBlockTokens = 0
	}
	analysis := wireAnalysis{
		Messages: len(req.messages),
	}
	rounds := pairToolRounds(req.messages)
	analysis.Rounds = len(rounds)

	closed := make([]*wireToolRound, 0, len(rounds))
	for _, round := range rounds {
		if round.closed {
			closed = append(closed, round)
		}
	}
	analysis.ClosedRounds = len(closed)
	if len(closed) <= keepRounds {
		return analysis
	}
	eligible := closed[:len(closed)-keepRounds]

	for _, round := range eligible {
		for _, messageIndex := range round.resultIndexes {
			msg := req.messages[messageIndex]
			for _, block := range msg.blocks {
				if block.typ != "tool_result" {
					continue
				}
				name, ok := round.toolNames[block.toolUseID]
				if !ok || block.isError || block.nonText || block.text == "" {
					continue
				}
				tokens := estimateTokens(block.text)
				if tokens < minBlockTokens {
					continue
				}
				errorText := looksLikeErrorText(block.text)
				if errorText {
					analysis.ErrorCandidates++
				}
				analysis.Candidates = append(analysis.Candidates, foldCandidate{
					MessageIndex: messageIndex,
					BlockIndex:   block.index,
					ToolUseID:    block.toolUseID,
					ToolName:     name,
					Tokens:       tokens,
					Bytes:        len(block.text),
					ErrorText:    errorText,
				})
				analysis.CandidateTokens += tokens
				analysis.CandidateBytes += len(block.text)
			}
		}
	}
	return analysis
}

// messageHashes fingerprints each message by its content so Phase A can detect
// how often the CLI itself rewrites an earlier part of the history.
//
// Two encoding-level rewrites are normalized away first, because neither
// changes what the model sees but both changed the raw bytes on real traffic
// (2026-11-18):
//
//   - cache_control relocation: Claude Code moves its ephemeral breakpoint to
//     the newest message every turn.
//   - text-shape flip: the system message arrives as [{"type":"text",...}]
//     on turn 1 and as a plain string on later turns; the tool_result at the
//     end of an assistant turn flips the other way.
//
// Anything still different after normalization is real content drift.
func messageHashes(req *wireRequest, limit int) []uint64 {
	count := len(req.messages)
	if limit > 0 && count > limit {
		count = limit
	}
	hashes := make([]uint64, 0, count)
	for index := 0; index < count; index++ {
		hash := fnv.New64a()
		_, _ = hash.Write(normalizeMessageHash(req.messages[index]))
		hashes = append(hashes, hash.Sum64())
	}
	return hashes
}

// normalizeMessageHash renders role plus block content in a canonical form:
// text-shape differences collapse, cache_control is dropped, and tool blocks
// keep their identifiers so a real edit still changes the hash.
func normalizeMessageHash(msg wireMessage) []byte {
	var builder bytes.Buffer
	builder.WriteString(msg.role)
	builder.WriteByte('\n')
	for _, block := range msg.blocks {
		switch block.typ {
		case "text":
			builder.WriteString("t:")
			builder.WriteString(block.text)
		case "tool_use":
			builder.WriteString("u:")
			builder.WriteString(block.toolUseID)
			builder.WriteByte(':')
			builder.WriteString(block.toolName)
		case "tool_result":
			builder.WriteString("r:")
			builder.WriteString(block.toolUseID)
			if block.isError {
				builder.WriteString(":err")
			}
			builder.WriteString(":")
			builder.WriteString(block.text)
		default:
			// thinking / image / document: the raw bytes are stable and any
			// change is real, so hash them as-is minus cache_control.
			builder.WriteByte('x')
			builder.Write(stripCacheControl(block.raw))
		}
		builder.WriteByte('\n')
	}
	return builder.Bytes()
}

// stripCacheControl removes `"cache_control": {...}` members from raw JSON
// before hashing. The output does not have to be valid JSON; it only has to be
// deterministic and content-equivalent.
func stripCacheControl(raw []byte) []byte {
	const key = `"cache_control"`
	out := make([]byte, 0, len(raw))
	for {
		index := bytes.Index(raw, []byte(key))
		if index < 0 {
			out = append(out, raw...)
			return out
		}
		out = append(out, raw[:index]...)
		rest := skipSpace(raw[index+len(key):])
		if len(rest) == 0 || rest[0] != ':' {
			// Not an object member (inside a string, or a different shape).
			// Keep the key verbatim and continue after it.
			out = append(out, raw[index:index+len(key)]...)
			raw = raw[index+len(key):]
			continue
		}
		value := skipSpace(rest[1:])
		if len(value) == 0 || value[0] != '{' {
			out = append(out, raw[index:index+len(key)]...)
			raw = raw[index+len(key):]
			continue
		}
		skip := skipJSONObject(value)
		after := rest[1:]
		after = after[len(after)-len(value)+skip:]
		// Drop one adjacent comma so the result stays close to canonical.
		if trimmed := bytes.TrimRight(out, " \t\r\n"); len(trimmed) > 0 && trimmed[len(trimmed)-1] == ',' {
			out = trimmed[:len(trimmed)-1]
		} else {
			after = bytes.TrimLeft(after, " \t\r\n")
			if len(after) > 0 && after[0] == ',' {
				after = after[1:]
			}
		}
		raw = after
	}
}

func skipSpace(in []byte) []byte {
	for len(in) > 0 && (in[0] == ' ' || in[0] == '\t' || in[0] == '\r' || in[0] == '\n') {
		in = in[1:]
	}
	return in
}

// skipJSONObject returns the byte length of the object starting at in[0] (or
// len(in) when unterminated), respecting strings and escapes.
func skipJSONObject(in []byte) int {
	depth := 0
	inString := false
	escaped := false
	for index := 0; index < len(in); index++ {
		ch := in[index]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return index + 1
			}
		}
	}
	return len(in)
}

// firstDrift returns the first message index whose fingerprint changed
// compared with the previous request, or -1 when the old prefix is intact.
// A pure append is not drift.
func firstDrift(previous, current []uint64) int {
	limit := len(previous)
	if len(current) < limit {
		limit = len(current)
	}
	for index := 0; index < limit; index++ {
		if previous[index] != current[index] {
			return index
		}
	}
	if len(current) < len(previous) {
		return len(current)
	}
	return -1
}

// looksLikeErrorText is a heuristic for the is_error=false-but-it-clearly-failed
// case: some tools report failure as normal output. It only widens the pointer
// excerpt, so a false positive costs a few lines, not correctness.
//
// Markers must start a line (after trimming). Searching anywhere matched Go
// source that merely contains fmt.Errorf / "failed" in string literals: real
// traffic flagged 2 of 3 file reads as error output (2026-11-18).
func looksLikeErrorText(text string) bool {
	const headLines = 20
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) > 2*headLines {
		tail := lines[len(lines)-headLines:]
		lines = append(append([]string{}, lines[:headLines]...), tail...)
	}
	for _, line := range lines {
		lower := strings.ToLower(strings.TrimSpace(line))
		for _, marker := range []string{
			"traceback (most recent call last)",
			"panic:",
			"fatal:",
			"exception:",
			"exit status",
			"permission denied",
			"no such file or directory",
			"command not found",
			"error:",
			"failed",
		} {
			if strings.HasPrefix(lower, marker) {
				return true
			}
		}
	}
	return false
}
