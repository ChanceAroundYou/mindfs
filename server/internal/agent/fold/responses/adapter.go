package responses

import "mindfs/server/internal/agent/fold"

// Adapter is the OpenAI Responses implementation of fold.Adapter. It holds no
// state: one value serves every session, and the engine keeps the per-session
// bookkeeping.
type Adapter struct{}

// New returns the adapter for Responses requests (`/v1/responses`). It is the
// wire format Codex sends; every request that reaches the fold proxy carries a
// full history in `input`.
func New() Adapter { return Adapter{} }

// A Responses adapter is a complete fold.Adapter; the compiler checks it here
// rather than at the one call site that wires it into the engine.
var _ fold.Adapter = Adapter{}

// Name identifies this adapter in logs and in the server's engine table.
func (Adapter) Name() string { return "responses" }

// Scan parses the request body into rounds of paired tool results.
func (Adapter) Scan(body []byte) (*fold.Conversation, error) {
	req, err := scanWireRequest(body)
	if err != nil {
		return nil, err
	}
	return conversation(body, req), nil
}
