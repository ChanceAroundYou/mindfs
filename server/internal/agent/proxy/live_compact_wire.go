package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
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
	// parts is the number of text blocks in a tool_result (1 for the plain
	// string form). Fold-in-place requires exactly one; anything else cannot
	// be replaced without changing the block structure.
	parts int
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
	// Failed marks an is_error result. It is foldable once it clears the
	// per-block token floor, and its pointer says the result failed.
	Failed bool
}

type wireAnalysis struct {
	Messages             int
	Rounds               int
	ClosedRounds         int
	Candidates           []foldCandidate
	ErrorCandidates      int
	FailedCandidates     int
	CandidateTokens      int
	CandidateBytes       int
	EstimatedInputTokens int
	// Bytes the fold could not take, bucketed by reason, counted over the
	// eligible rounds only. Without these the rail looks satisfiable while
	// un-foldable blocks keep the context full.
	SkippedErrorBytes     int
	SkippedMultiPartBytes int
	SkippedNonTextBytes   int
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
		text, nonText, parts, err := toolResultText(head.Content)
		if err != nil {
			return wireBlock{}, err
		}
		block.text = text
		block.nonText = nonText
		block.parts = parts
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
// parts is the number of text blocks, which the planner uses to reject results
// that cannot be replaced in place.
func toolResultText(raw json.RawMessage) (text string, nonText bool, parts int, err error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", false, 0, nil
	}
	if trimmed[0] == '"' {
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return "", false, 0, err
		}
		return text, false, 1, nil
	}
	var blocks []struct {
		Type string          `json:"type"`
		Text string          `json:"text"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(trimmed, &blocks); err != nil {
		return "", false, 0, err
	}
	var builder bytes.Buffer
	for _, block := range blocks {
		if block.Type != "text" {
			nonText = true
			continue
		}
		if parts > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(block.Text)
		parts++
	}
	return builder.String(), nonText, parts, nil
}
