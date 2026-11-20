package fold

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// EncodeJSONStringLiteral renders s as a JSON string literal, including the
// surrounding quotes, ready to be spliced into a request body.
//
// encoding/json escapes U+2028/U+2029 and <,>,& by default. The card contains
// none of those by construction (it is our own text plus an excerpt of the
// model's output, which may contain anything). Escaping more than necessary is
// still valid JSON and still decodes to the same string, so correctness does
// not depend on this; determinism does, and json.Marshal is deterministic.
func EncodeJSONStringLiteral(s string) string {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(s); err != nil {
		// Encode on a string cannot fail; fall back to a quoted literal so the
		// caller still gets valid JSON rather than a panic.
		return strconv.Quote(s)
	}
	return strings.TrimSuffix(buffer.String(), "\n")
}
