package anthropic

import (
	"bytes"

	"mindfs/server/internal/agent/fold"
)

// The byte-span scanner for an Anthropic Messages request.
//
// It records spans into the original body, never decoded copies: the fold
// replaces exactly one string literal and must carry every other byte over
// untouched (key order, number formatting, escape choices).
//
// A separate reference parser (encoding/json based) grew up alongside this one
// in the proxy; the two are pinned against each other by the proxy's
// TestFoldScannerAgreesWithReferenceAnalysis. Only this one survives a rewrite,
// so it is the one that moved into the adapter.

type scanBlock struct {
	typ       string
	id        string // tool_use id
	name      string // tool_use name
	toolUseID string // tool_result: the id it answers
	isError   bool
	input     fold.Span // tool_use input object (raw)
	// textParts are the string literal spans that make up the block's text, in
	// order. A tool_result in string form has one part; in array form it has one
	// part per text block. Folding in place requires exactly one part.
	textParts []fold.Span
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
	open := fold.ScanSkipSpace(body, 0)
	if open >= len(body) || body[open] != '{' {
		return nil, fold.ErrScanMalformed
	}
	var messagesStart, messagesEnd int
	found := false
	model := ""
	err := fold.ScanEachMember(body, open, func(key []byte, vs, ve int) (bool, error) {
		switch {
		case fold.MemberKeyIs(key, "messages"):
			messagesStart, messagesEnd = vs, ve
			found = true
		case fold.MemberKeyIs(key, "model"):
			// A malformed model value must not abort the scan: the request is
			// still foldable as long as the prefix guard can see it, and an
			// unparseable model is handled as unknown by the guard.
			if vs < ve && body[vs] == '"' {
				if value, err := fold.JSONStringAt(body, vs, ve); err == nil {
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
		return nil, fold.ErrScanMalformed
	}
	req := &scanRequest{model: model}
	err = fold.ScanEachElement(body, messagesStart, func(vs, ve int) error {
		if body[vs] != '{' {
			// Non-object message: keep the slot so indexes stay aligned with the
			// reference parser, which also tolerates it at this level.
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
	err := fold.ScanEachMember(b, open, func(key []byte, vs, ve int) (bool, error) {
		switch {
		case fold.MemberKeyIs(key, "role"):
			role, err := fold.JSONStringAt(b, vs, ve)
			if err != nil {
				return false, err
			}
			msg.role = role
		case fold.MemberKeyIs(key, "content"):
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
		msg.blocks = []scanBlock{{typ: "text", textParts: []fold.Span{{Start: contentStart, End: contentEnd}}}}
	case '[':
		err := fold.ScanEachElement(b, contentStart, func(vs, ve int) error {
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
	err := fold.ScanEachMember(b, open, func(key []byte, vs, ve int) (bool, error) {
		switch {
		case fold.MemberKeyIs(key, "type"):
			typ, err := fold.JSONStringAt(b, vs, ve)
			if err != nil {
				return false, err
			}
			block.typ = typ
		case fold.MemberKeyIs(key, "id"):
			id, err := fold.JSONStringAt(b, vs, ve)
			if err != nil {
				return false, err
			}
			block.id = id
		case fold.MemberKeyIs(key, "name"):
			name, err := fold.JSONStringAt(b, vs, ve)
			if err != nil {
				return false, err
			}
			block.name = name
		case fold.MemberKeyIs(key, "tool_use_id"):
			id, err := fold.JSONStringAt(b, vs, ve)
			if err != nil {
				return false, err
			}
			block.toolUseID = id
		case fold.MemberKeyIs(key, "is_error"):
			block.isError = bytes.Equal(b[vs:ve], []byte("true"))
		case fold.MemberKeyIs(key, "input"):
			block.input = fold.Span{Start: vs, End: ve}
		case fold.MemberKeyIs(key, "content"):
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
		block.textParts = []fold.Span{{Start: start, End: end}}
		return
	case '[':
	default:
		if b[start] == '{' {
			block.nonText = true
		}
		return
	}
	_ = fold.ScanEachElement(b, start, func(vs, ve int) error {
		if b[vs] != '{' {
			block.nonText = true
			return nil
		}
		var typ string
		var textSpan fold.Span
		hasText := false
		err := fold.ScanEachMember(b, vs, func(key []byte, ks, ke int) (bool, error) {
			switch {
			case fold.MemberKeyIs(key, "type"):
				value, err := fold.JSONStringAt(b, ks, ke)
				if err != nil {
					return false, err
				}
				typ = value
			case fold.MemberKeyIs(key, "text"):
				if b[ks] == '"' {
					textSpan = fold.Span{Start: ks, End: ke}
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
