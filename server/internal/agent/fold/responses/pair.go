package responses

import "mindfs/server/internal/agent/fold"

// Pairing: a run of call items becomes one round, closed by the outputs that
// answer it. An open round is represented by zero results so the planner leaves
// it alone — the model is still waiting for the answer.

type pendingRound struct {
	results []fold.Result
	filled  []bool
	pending int
}

type callRef struct {
	round  int
	result int
}

// inputSummaryLimit bounds the card text for a call whose parameters are not a
// JSON object (a custom tool's free-form script).
const inputSummaryLimit = 300

// summarizeInput renders the call's parameters for the pointer card.
//
// A function call's arguments are an encoded object, so the shared key
// preference table applies; a custom tool's input is free-form text and is
// truncated instead.
func summarizeInput(body []byte, item scanItem) string {
	if !item.input.Valid() || body[item.input.Start] != '"' {
		return ""
	}
	decoded, err := fold.JSONStringAt(body, item.input.Start, item.input.End)
	if err != nil {
		return ""
	}
	raw := []byte(decoded)
	if len(raw) > 0 && raw[0] == '{' {
		return fold.SummarizeToolInput(raw, fold.Span{Start: 0, End: len(raw)})
	}
	runes := []rune(decoded)
	if len(runes) > inputSummaryLimit {
		return string(runes[:inputSummaryLimit])
	}
	return decoded
}
