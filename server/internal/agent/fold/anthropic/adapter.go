package anthropic

import "mindfs/server/internal/agent/fold"

// Adapter is the Anthropic Messages implementation of fold.Adapter. It holds no
// state: one value serves every session, and the engine keeps the per-session
// bookkeeping.
type Adapter struct{}

// New returns the adapter for Anthropic Messages requests (`/v1/messages`).
func New() fold.Adapter { return Adapter{} }

// Name identifies this adapter in logs and in the server's engine table.
func (Adapter) Name() string { return "anthropic" }

// Scan parses the request body into rounds of paired tool results.
func (Adapter) Scan(body []byte) (*fold.Conversation, error) {
	req, err := scanWireRequest(body)
	if err != nil {
		return nil, err
	}
	return conversation(body, req), nil
}

// Text decodes one result's literal. Only a single-part, text-only result can
// be folded in place.
func (Adapter) Text(body []byte, result fold.Result) (string, bool) {
	if result.NonText || len(result.Parts) != 1 {
		return "", false
	}
	text, err := fold.JSONStringAt(body, result.Parts[0].Start, result.Parts[0].End)
	if err != nil {
		return "", false
	}
	return text, true
}

// Encode writes a pointer as a JSON string literal, quotes included. The engine
// splices the literal verbatim, and measures savings on it, so escaping happens
// here exactly once.
func (Adapter) Encode(text string) string { return fold.EncodeJSONStringLiteral(text) }

// Window reports the model's context window. The proxy knows it from
// configuration, and nothing in the Anthropic fold path needs it here.
func (Adapter) Window(string) int { return 0 }
