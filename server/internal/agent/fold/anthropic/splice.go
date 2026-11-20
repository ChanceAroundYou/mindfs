package anthropic

import (
	"fmt"

	"mindfs/server/internal/agent/fold"
)

// Splice replaces each span with its wire literal and copies every other byte
// verbatim. The engine has already checked ordering and range; this only guards
// against being called directly with a bad set.
func (Adapter) Splice(body []byte, replacements []fold.Replacement) ([]byte, error) {
	cursor := 0
	total := len(body)
	for _, replacement := range replacements {
		if replacement.Span.Start < cursor || replacement.Span.End > len(body) || !replacement.Span.Valid() {
			return nil, fmt.Errorf("anthropic: replacement span out of order or range")
		}
		total += len(replacement.Literal) - (replacement.Span.End - replacement.Span.Start)
		cursor = replacement.Span.End
	}
	if total <= 0 {
		return nil, fmt.Errorf("anthropic: replacement set empties the body")
	}
	out := make([]byte, 0, total)
	cursor = 0
	for _, replacement := range replacements {
		out = append(out, body[cursor:replacement.Span.Start]...)
		out = append(out, replacement.Literal...)
		cursor = replacement.Span.End
	}
	out = append(out, body[cursor:]...)
	return out, nil
}
