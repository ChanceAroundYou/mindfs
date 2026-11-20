package responses

import "mindfs/server/internal/agent/fold"

// Text decodes one result's literal. Only a single-part, text-only result can
// be folded in place: replacing several blocks with one pointer would change the
// structure the harness sent back to the model.
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

// Splice writes each replacement's literal into its span and copies every other
// byte verbatim. The span points at the text literal inside `output`; whether
// that is the whole `output` string or one `input_text` block inside an array
// makes no difference to the splice.
func (Adapter) Splice(body []byte, replacements []fold.Replacement) ([]byte, error) {
	return fold.SpliceReplacements(body, replacements)
}

// responsesWindows lists the context windows this fork knows, keyed by the model
// id Codex sends. Anything absent is reported as unknown and the engine falls
// back to the configured window.
var responsesWindows = map[string]int{
	"gpt-6-luna": 272000,
}

// Window reports the model's context window, or 0 when unknown.
func (Adapter) Window(model string) int { return responsesWindows[model] }
