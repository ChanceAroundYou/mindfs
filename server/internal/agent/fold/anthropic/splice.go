package anthropic

import "mindfs/server/internal/agent/fold"

// Splice replaces each span with its wire literal and copies every other byte
// verbatim. Byte surgery is protocol-independent, so the kernel owns it.
func (Adapter) Splice(body []byte, replacements []fold.Replacement) ([]byte, error) {
	return fold.SpliceReplacements(body, replacements)
}
