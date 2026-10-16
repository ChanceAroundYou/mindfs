package acp

import (
	"testing"

	types "mindfs/server/internal/agent/types"

	acpsdk "github.com/coder/acp-go-sdk"
)

func TestACPDSHTokenUsagePreservesPromptUsage(t *testing.T) {
	state := &sessionState{}
	firstRead, firstWrite := 4_000, 1_000
	first := state.tokenUsageForPrompt("dsh", &acpsdk.Usage{
		InputTokens:       5_500,
		OutputTokens:      500,
		CachedReadTokens:  &firstRead,
		CachedWriteTokens: &firstWrite,
	})
	if first == nil || first.InputTokens != 5_500 || first.OutputTokens != 500 {
		t.Fatalf("first usage = %#v", first)
	}

	secondRead, secondWrite := 12_000, 1_500
	second := state.tokenUsageForPrompt("dsh", &acpsdk.Usage{
		InputTokens:       14_000,
		OutputTokens:      1_600,
		CachedReadTokens:  &secondRead,
		CachedWriteTokens: &secondWrite,
	})
	if second == nil || second.InputTokens != 14_000 || second.OutputTokens != 1_600 {
		t.Fatalf("second usage = %#v", second)
	}
	if second.CacheReadTokens == nil || *second.CacheReadTokens != 12_000 {
		t.Fatalf("second cache read = %#v", second.CacheReadTokens)
	}
	if second.CacheWriteTokens == nil || *second.CacheWriteTokens != 1_500 {
		t.Fatalf("second cache write = %#v", second.CacheWriteTokens)
	}
}

func TestACPTokenUsageConvertsCumulativeCountersToTurnDelta(t *testing.T) {
	for _, agentName := range []string{"copilot", "unknown", "deepseek", ""} {
		t.Run(agentName, func(t *testing.T) {
			state := &sessionState{}
			firstRead, firstWrite := 4_000, 1_000
			first := state.tokenUsageForPrompt(agentName, &acpsdk.Usage{
				InputTokens:       5_500,
				OutputTokens:      500,
				CachedReadTokens:  &firstRead,
				CachedWriteTokens: &firstWrite,
			})
			if first == nil || first.InputTokens != 5_500 || first.OutputTokens != 500 {
				t.Fatalf("first usage = %#v", first)
			}

			secondRead, secondWrite := 12_000, 1_500
			second := state.tokenUsageForPrompt(agentName, &acpsdk.Usage{
				InputTokens:       14_000,
				OutputTokens:      1_600,
				CachedReadTokens:  &secondRead,
				CachedWriteTokens: &secondWrite,
			})
			if second == nil || second.InputTokens != 8_500 || second.OutputTokens != 1_100 {
				t.Fatalf("second usage = %#v", second)
			}
			if second.CacheReadTokens == nil || *second.CacheReadTokens != 8_000 {
				t.Fatalf("second cache read = %#v", second.CacheReadTokens)
			}
			if second.CacheWriteTokens == nil || *second.CacheWriteTokens != 500 {
				t.Fatalf("second cache write = %#v", second.CacheWriteTokens)
			}
		})
	}
}

func TestACPTokenUsageCounterResetAndMissingUsage(t *testing.T) {
	for _, agentName := range []string{"dsh", "copilot"} {
		t.Run(agentName, func(t *testing.T) {
			state := &sessionState{}
			state.tokenUsageForPrompt(agentName, &acpsdk.Usage{InputTokens: 1000, OutputTokens: 100})
			if got := state.tokenUsageForPrompt(agentName, nil); got != nil {
				t.Fatalf("missing usage = %#v", got)
			}
			got := state.tokenUsageForPrompt(agentName, &acpsdk.Usage{InputTokens: 500, OutputTokens: 50})
			if got == nil || got.InputTokens != 500 || got.OutputTokens != 50 {
				t.Fatalf("usage after counter reset = %#v", got)
			}
		})
	}
}

func TestACPUsageUpdateReplacesCurrentContextUsage(t *testing.T) {
	state := &sessionState{contextWindow: types.ContextWindow{
		TotalTokens:        1_911_735,
		ModelContextWindow: 512_000,
	}}

	state.setUsageUpdate(217_991, 512_000)
	got := state.getContextWindow()
	if got.TotalTokens != 217_991 || got.ModelContextWindow != 512_000 {
		t.Fatalf("context window after first update = %#v", got)
	}
	if !state.hasContextUsageUpdate() {
		t.Fatal("usage update was not marked authoritative")
	}

	state.setUsageUpdate(220_104, 512_000)
	got = state.getContextWindow()
	if got.TotalTokens != 220_104 || got.ModelContextWindow != 512_000 {
		t.Fatalf("context window after second update = %#v", got)
	}
}
