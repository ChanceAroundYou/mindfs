package responses

import (
	"bytes"

	"mindfs/server/internal/agent/fold"
)

// The byte-span scanner for an OpenAI Responses request (`/v1/responses`).
//
// Like the Anthropic scanner it records spans into the original body and never
// decodes what it is not about to fold: a fold replaces exactly one string
// literal and must carry every other byte over untouched.
//
// The two protocols differ in where a tool round lives, which is why this is a
// separate parser rather than a mode of the Messages one. In `input` an
// assistant turn is not a message: a call is its own item (`custom_tool_call`
// or `function_call`), its answer is another item of a different type, and the
// two are joined only by `call_id`.

// Item types this adapter knows about. Everything else (`message`, `reasoning`,
// `additional_tools`, …) carries no foldable tool result and is skipped.
const (
	itemCustomToolCall       = "custom_tool_call"
	itemCustomToolCallOutput = "custom_tool_call_output"
	itemFunctionCall         = "function_call"
	itemFunctionCallOutput   = "function_call_output"
)

// RefuseUnsupported is the Conversation.Refuse value for a request whose
// history is not in the body. Rewriting such a request would edit a client-side
// copy of a conversation the upstream already stores, so the engine must
// forward it untouched.
const RefuseUnsupported = "unsupported"

// scanItem is one `input` element reduced to what folding needs.
type scanItem struct {
	typ    string
	callID string
	name   string
	// input is the call's parameter span, quotes included. For a custom tool it
	// is free-form text (Codex sends JavaScript); for a function call it is a
	// JSON object encoded as a string.
	input fold.Span
	// textParts are the string literal spans that make up an output's text, in
	// wire order. Folding in place requires exactly one.
	textParts []fold.Span
	// nonText marks an output carrying an image, a file, or a block type this
	// adapter does not understand. Such an output keeps its bytes verbatim.
	nonText bool
	// hasOutput marks a `*_output` item that actually carried an `output`
	// member; an output item without one is treated like a missing result.
	hasOutput bool
}

type scanRequest struct {
	model string
	items []scanItem
	// stateful is set when the request asks the upstream to keep the
	// conversation (`store: true`) or continues one it already keeps
	// (`previous_response_id`). The history is then not in the body.
	stateful bool
}

func scanWireRequest(body []byte) (*scanRequest, error) {
	open := fold.ScanSkipSpace(body, 0)
	if open >= len(body) || body[open] != '{' {
		return nil, fold.ErrScanMalformed
	}
	req := &scanRequest{}
	var inputStart, inputEnd int
	hasInput := false
	err := fold.ScanEachMember(body, open, func(key []byte, vs, ve int) (bool, error) {
		switch {
		case fold.MemberKeyIs(key, "input"):
			inputStart, inputEnd, hasInput = vs, ve, true
		case fold.MemberKeyIs(key, "model"):
			// A malformed model must not abort the scan: the request is still
			// foldable, and the guard handles an unparseable id as unknown.
			if vs < ve && body[vs] == '"' {
				if value, err := fold.JSONStringAt(body, vs, ve); err == nil {
					req.model = value
				}
			}
		case fold.MemberKeyIs(key, "store"):
			req.stateful = req.stateful || bytes.Equal(body[vs:ve], []byte("true"))
		case fold.MemberKeyIs(key, "previous_response_id"):
			req.stateful = req.stateful || (vs < ve && body[vs] == '"' && ve-vs > 2)
		}
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	if !hasInput || inputStart >= inputEnd || body[inputStart] != '[' {
		return nil, fold.ErrScanMalformed
	}
	err = fold.ScanEachElement(body, inputStart, func(vs, ve int) error {
		if body[vs] != '{' {
			req.items = append(req.items, scanItem{})
			return nil
		}
		item, err := scanItemPayload(body, vs)
		if err != nil {
			return err
		}
		req.items = append(req.items, item)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return req, nil
}

func scanItemPayload(b []byte, open int) (scanItem, error) {
	item := scanItem{}
	var outputStart, outputEnd int
	err := fold.ScanEachMember(b, open, func(key []byte, vs, ve int) (bool, error) {
		switch {
		case fold.MemberKeyIs(key, "type"):
			typ, err := fold.JSONStringAt(b, vs, ve)
			if err != nil {
				return false, err
			}
			item.typ = typ
		case fold.MemberKeyIs(key, "call_id"):
			id, err := fold.JSONStringAt(b, vs, ve)
			if err != nil {
				return false, err
			}
			item.callID = id
		case fold.MemberKeyIs(key, "name"):
			name, err := fold.JSONStringAt(b, vs, ve)
			if err != nil {
				return false, err
			}
			item.name = name
		case fold.MemberKeyIs(key, "input"):
			item.input = fold.Span{Start: vs, End: ve}
		case fold.MemberKeyIs(key, "arguments"):
			// function_call carries its parameters as an encoded object; the
			// summary helper decodes it later, so the span is enough here.
			item.input = fold.Span{Start: vs, End: ve}
		case fold.MemberKeyIs(key, "output"):
			outputStart, outputEnd = vs, ve
			item.hasOutput = true
		}
		return false, nil
	})
	if err != nil {
		return scanItem{}, err
	}
	switch item.typ {
	case itemCustomToolCallOutput, itemFunctionCallOutput:
		scanOutput(b, outputStart, outputEnd, item.hasOutput, &item)
	}
	return item, nil
}

// scanOutput records the text spans of a tool output and flags anything it does
// not understand. Both output shapes are accepted: a bare string literal, or an
// array of content blocks whose `input_text` entries are the text.
func scanOutput(b []byte, start, end int, ok bool, item *scanItem) {
	if !ok || start >= end {
		item.hasOutput = false
		return
	}
	switch b[start] {
	case '"':
		if _, err := fold.JSONStringAt(b, start, end); err != nil {
			item.nonText = true
			return
		}
		item.textParts = []fold.Span{{Start: start, End: end}}
	case '[':
		_ = fold.ScanEachElement(b, start, func(vs, ve int) error {
			if b[vs] != '{' {
				item.nonText = true
				return nil
			}
			typ, text, isText := scanTextBlock(b, vs)
			if !isText || typ != "input_text" {
				item.nonText = true
				return nil
			}
			item.textParts = append(item.textParts, text)
			return nil
		})
	case 'n':
		// null: the output produced nothing to fold.
		item.hasOutput = false
	default:
		item.nonText = true
	}
}

// scanTextBlock reads a content block's `type` and, when it has a string
// `text`, its span.
func scanTextBlock(b []byte, open int) (typ string, text fold.Span, ok bool) {
	err := fold.ScanEachMember(b, open, func(key []byte, vs, ve int) (bool, error) {
		switch {
		case fold.MemberKeyIs(key, "type"):
			value, err := fold.JSONStringAt(b, vs, ve)
			if err != nil {
				return false, err
			}
			typ = value
		case fold.MemberKeyIs(key, "text"):
			if vs < ve && b[vs] == '"' {
				text = fold.Span{Start: vs, End: ve}
			}
		}
		return false, nil
	})
	if err != nil || !text.Valid() {
		return typ, text, false
	}
	return typ, text, true
}

// conversation turns the scanned items into rounds in wire order.
func conversation(body []byte, req *scanRequest) *fold.Conversation {
	conv := &fold.Conversation{
		Model:     req.model,
		Estimated: fold.EstimateByteTokens(body),
	}
	if req.stateful {
		// The history is upstream state, not this body: a client-side rewrite
		// would fork the stored conversation.
		conv.Refuse = RefuseUnsupported
		return conv
	}
	rounds := make([]*pendingRound, 0, 8)
	byCall := make(map[string]callRef)
	// current is the round still collecting calls. The first output closes it:
	// a later call in the same request belongs to the next assistant turn.
	var current *pendingRound
	for _, item := range req.items {
		switch item.typ {
		case itemCustomToolCall, itemFunctionCall:
			if item.callID == "" {
				continue
			}
			if current == nil {
				current = &pendingRound{}
				rounds = append(rounds, current)
			}
			byCall[item.callID] = callRef{round: len(rounds) - 1, result: len(current.results)}
			current.results = append(current.results, fold.Result{
				CallID:       item.callID,
				ToolName:     item.name,
				InputSummary: summarizeInput(body, item),
			})
			current.filled = append(current.filled, false)
			current.pending++
		case itemCustomToolCallOutput, itemFunctionCallOutput:
			current = nil
			if !item.hasOutput || item.callID == "" {
				continue
			}
			ref, ok := byCall[item.callID]
			if !ok {
				continue
			}
			round := rounds[ref.round]
			if round.filled[ref.result] {
				continue
			}
			result := &round.results[ref.result]
			result.Parts = item.textParts
			result.NonText = item.nonText
			for _, part := range item.textParts {
				result.Bytes += part.End - part.Start
				result.Tokens += fold.EstimateByteTokens(body[part.Start:part.End])
			}
			round.filled[ref.result] = true
			round.pending--
		}
	}
	for _, round := range rounds {
		if round.pending > 0 {
			conv.Rounds = append(conv.Rounds, fold.Round{})
			continue
		}
		conv.Rounds = append(conv.Rounds, fold.Round{Results: round.results})
	}
	return conv
}
