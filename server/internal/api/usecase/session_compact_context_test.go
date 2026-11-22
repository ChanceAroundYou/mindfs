package usecase

import (
	"testing"

	agenttypes "mindfs/server/internal/agent/types"
)

func TestCompactPostTokensWinOverStaleMessageDone(t *testing.T) {
	done := agenttypes.ContextWindow{TotalTokens: 180000, ModelContextWindow: 200000}
	got := contextWindowAfterCompact(done, 37062)
	if got.TotalTokens != 37062 || got.ModelContextWindow != 200000 {
		t.Fatalf("context window = %+v, want postTokens 37062 with model window 200000", got)
	}
}

func TestContextWindowWithoutPostTokensIsUnchanged(t *testing.T) {
	done := agenttypes.ContextWindow{TotalTokens: 180000, ModelContextWindow: 200000}
	got := contextWindowAfterCompact(done, 0)
	if got != done {
		t.Fatalf("context window = %+v, want %+v", got, done)
	}
}
