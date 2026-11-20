package proxy

import (
	"bytes"
)

type scanBlock struct {
	typ       string
	id        string // tool_use id
	name      string // tool_use name
	toolUseID string // tool_result: the id it answers
	isError   bool
	input     span // tool_use input object (raw)
	// textParts are the string literal spans that make up the block's text, in
	// order. A tool_result in string form has one part; in array form it has one
	// part per text block. Folding in place requires exactly one part.
	textParts []span
	nonText   bool // tool_result carried an image/document/unknown block
}

type scanMessage struct {
	role   string
	blocks []scanBlock
}

type scanRequest struct {
	messages []scanMessage
	// model is the top-level "model" member, verbatim. Empty when absent or not
	// a string. It exists for the preserved-thinking prefix guard, which must
	// refuse to rewrite requests aimed at those models.
	model string
}

func scanWireRequest(body []byte) (*scanRequest, error) {
	open := scanSkipSpace(body, 0)
	if open >= len(body) || body[open] != '{' {
		return nil, errScanMalformed
	}
	var messagesStart, messagesEnd int
	found := false
	model := ""
	err := scanEachMember(body, open, func(key []byte, vs, ve int) (bool, error) {
		switch {
		case memberKeyIs(key, "messages"):
			messagesStart, messagesEnd = vs, ve
			found = true
		case memberKeyIs(key, "model"):
			// A malformed model value must not abort the scan: the request is
			// still foldable as long as the prefix guard can see it, and an
			// unparseable model is handled as unknown by the guard.
			if vs < ve && body[vs] == '"' {
				if value, err := jsonStringAt(body, vs, ve); err == nil {
					model = value
				}
			}
		}
		// Keep scanning until both members are seen; the body puts "model"
		// near the top today, but nothing guarantees that.
		return found && model != "", nil
	})
	if err != nil {
		return nil, err
	}
	if !found || messagesStart >= messagesEnd || body[messagesStart] != '[' {
		return nil, errScanMalformed
	}
	req := &scanRequest{model: model}
	err = scanEachElement(body, messagesStart, func(vs, ve int) error {
		if body[vs] != '{' {
			// Non-object message: keep the slot so indexes stay aligned with
			// the reference parser, which also tolerates it at this level.
			req.messages = append(req.messages, scanMessage{})
			return nil
		}
		msg, err := scanMessagePayload(body, vs)
		if err != nil {
			return err
		}
		req.messages = append(req.messages, msg)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return req, nil
}

func scanMessagePayload(b []byte, open int) (scanMessage, error) {
	msg := scanMessage{}
	var contentStart, contentEnd int
	hasContent := false
	err := scanEachMember(b, open, func(key []byte, vs, ve int) (bool, error) {
		switch {
		case memberKeyIs(key, "role"):
			role, err := jsonStringAt(b, vs, ve)
			if err != nil {
				return false, err
			}
			msg.role = role
		case memberKeyIs(key, "content"):
			contentStart, contentEnd, hasContent = vs, ve, true
		}
		return false, nil
	})
	if err != nil {
		return scanMessage{}, err
	}
	if !hasContent || contentStart >= contentEnd {
		return msg, nil
	}
	switch b[contentStart] {
	case '"':
		// Plain string content: one implicit text block, no tool structures.
		msg.blocks = []scanBlock{{typ: "text", textParts: []span{{Start: contentStart, End: contentEnd}}}}
	case '[':
		err := scanEachElement(b, contentStart, func(vs, ve int) error {
			if b[vs] != '{' {
				msg.blocks = append(msg.blocks, scanBlock{nonText: true})
				return nil
			}
			block, err := scanBlockPayload(b, vs)
			if err != nil {
				return err
			}
			msg.blocks = append(msg.blocks, block)
			return nil
		})
		if err != nil {
			return scanMessage{}, err
		}
	}
	return msg, nil
}

func scanBlockPayload(b []byte, open int) (scanBlock, error) {
	block := scanBlock{}
	var contentStart, contentEnd int
	hasContent := false
	err := scanEachMember(b, open, func(key []byte, vs, ve int) (bool, error) {
		switch {
		case memberKeyIs(key, "type"):
			typ, err := jsonStringAt(b, vs, ve)
			if err != nil {
				return false, err
			}
			block.typ = typ
		case memberKeyIs(key, "id"):
			id, err := jsonStringAt(b, vs, ve)
			if err != nil {
				return false, err
			}
			block.id = id
		case memberKeyIs(key, "name"):
			name, err := jsonStringAt(b, vs, ve)
			if err != nil {
				return false, err
			}
			block.name = name
		case memberKeyIs(key, "tool_use_id"):
			id, err := jsonStringAt(b, vs, ve)
			if err != nil {
				return false, err
			}
			block.toolUseID = id
		case memberKeyIs(key, "is_error"):
			block.isError = bytes.Equal(b[vs:ve], []byte("true"))
		case memberKeyIs(key, "input"):
			block.input = span{Start: vs, End: ve}
		case memberKeyIs(key, "content"):
			contentStart, contentEnd, hasContent = vs, ve, true
		}
		return false, nil
	})
	if err != nil {
		return scanBlock{}, err
	}
	if block.typ == "tool_result" {
		scanToolResultContent(b, contentStart, contentEnd, hasContent, &block)
	}
	return block, nil
}

// scanToolResultContent records the text literal spans of a tool_result and
// flags whether anything non-text is present. Anything it does not understand
// is marked non-text, which keeps the block out of the candidate set (I3).
func scanToolResultContent(b []byte, start, end int, ok bool, block *scanBlock) {
	if !ok || start >= end {
		return
	}
	switch b[start] {
	case '"':
		block.textParts = []span{{Start: start, End: end}}
		return
	case '[':
	default:
		if b[start] == '{' {
			block.nonText = true
		}
		return
	}
	_ = scanEachElement(b, start, func(vs, ve int) error {
		if b[vs] != '{' {
			block.nonText = true
			return nil
		}
		var typ string
		var textSpan span
		hasText := false
		err := scanEachMember(b, vs, func(key []byte, ks, ke int) (bool, error) {
			switch {
			case memberKeyIs(key, "type"):
				value, err := jsonStringAt(b, ks, ke)
				if err != nil {
					return false, err
				}
				typ = value
			case memberKeyIs(key, "text"):
				if b[ks] == '"' {
					textSpan = span{Start: ks, End: ke}
					hasText = true
				}
			}
			return false, nil
		})
		if err != nil {
			// A malformed sub-block makes the whole result unknown rather than
			// foldable on partial information.
			block.nonText = true
			block.textParts = nil
			return err
		}
		if typ != "text" || !hasText {
			block.nonText = true
			return nil
		}
		block.textParts = append(block.textParts, textSpan)
		return nil
	})
}
