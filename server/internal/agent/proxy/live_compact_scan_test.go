package proxy

import (
	"testing"
	"time"
)

// The reference parser and the span scanner must agree on what is foldable.
// They are separate implementations on purpose; if they drift, Phase A's

// measurements stop describing what Phase B1 actually does.
func TestFoldScannerAgreesWithReferenceAnalysis(t *testing.T) {
	cases := map[string][]any{
		"string content": {
			userText("hi"),
			assistantToolUse("toolu_1", "Read"),
			userToolResult("toolu_1", bigText("a"), false),
			assistantToolUse("toolu_2", "Read"),
			userToolResult("toolu_2", bigText("b"), false),
		},
		"array content": {
			userText("hi"),
			assistantToolUse("toolu_1", "Read"),
			userToolResultBlocks("toolu_1", []any{map[string]any{"type": "text", "text": bigText("a")}}, false),
			assistantToolUse("toolu_2", "Read"),
			userToolResult("toolu_2", bigText("b"), false),
		},
		"error result in window": {
			userText("hi"),
			assistantToolUse("toolu_1", "Bash"),
			userToolResult("toolu_1", bigText("a"), true),
			assistantToolUse("toolu_2", "Read"),
			userToolResult("toolu_2", bigText("b"), false),
		},
		"open round": {
			userText("hi"),
			assistantToolUse("toolu_1", "Read"),
			assistantToolUse("toolu_2", "Read"),
			userToolResult("toolu_2", bigText("b"), false),
		},
	}
	cfg := foldTestConfig(t.TempDir())
	for name, messages := range cases {
		t.Run(name, func(t *testing.T) {
			body := wireBody(t, messages)
			reference, err := parseWireRequest(body)
			if err != nil {
				t.Fatalf("parseWireRequest: %v", err)
			}
			referenceAnalysis := analyzeWire(reference, cfg.KeepRounds, cfg.MinBlockTokens)

			scanned, err := scanWireRequest(body)
			if err != nil {
				t.Fatalf("scanWireRequest: %v", err)
			}
			dir := t.TempDir()
			store := newArchiveStore(dir, time.Hour, archiveMaxBytes)
			result := planFold(body, scanned, cfg, store, nil)

			if result.foldedBlocks != len(referenceAnalysis.Candidates) {
				t.Fatalf("scanner folded %d blocks, reference found %d candidates",
					result.foldedBlocks, len(referenceAnalysis.Candidates))
			}
			if result.foldedBytes != referenceAnalysis.CandidateBytes {
				t.Fatalf("scanner folded %d bytes, reference counted %d",
					result.foldedBytes, referenceAnalysis.CandidateBytes)
			}
		})
	}
}
