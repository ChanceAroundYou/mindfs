package fold

import "fmt"

// SpliceReplacements writes each replacement's wire literal into its span and
// copies every other byte verbatim.
//
// Splicing is byte surgery on a request body, and that is the same operation in
// every wire format: a span was recorded during the scan, and the literal is
// already encoded by the adapter. Only the scanning, decoding, and encoding are
// protocol-specific, so this lives in the kernel instead of being copied into
// each adapter's splice.
//
// The engine has already checked ordering and range (checkSpliceBounds); this
// guards an adapter that is called directly.
func SpliceReplacements(body []byte, replacements []Replacement) ([]byte, error) {
	cursor := 0
	total := len(body)
	for _, replacement := range replacements {
		if replacement.Span.Start < cursor || replacement.Span.End > len(body) || !replacement.Span.Valid() {
			return nil, fmt.Errorf("fold: replacement span out of order or range")
		}
		total += len(replacement.Literal) - (replacement.Span.End - replacement.Span.Start)
		cursor = replacement.Span.End
	}
	if total <= 0 {
		return nil, fmt.Errorf("fold: replacement set empties the body")
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
