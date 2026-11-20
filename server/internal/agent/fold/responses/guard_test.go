package responses

import (
	"testing"

	"mindfs/server/internal/agent/fold"
)

// Responses has no preserved-thinking blocks for a rewrite to invalidate, so
// the guard must allow every model — folding tool output is always safe there.
func TestGuardAlwaysAllows(t *testing.T) {
	cfg := fold.Config{}.Normalized()
	for _, model := range []string{"gpt-6-luna", "claude-opus-5-5", "some-alias"} {
		if v := New().Guard(model, cfg); v.Block {
			t.Fatalf("%s must not block: %+v", model, v)
		}
	}
}
