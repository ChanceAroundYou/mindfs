package proxy

import (
	"strings"
	"testing"
)

func TestUsageScannerMergesSplitSSELines(t *testing.T) {
	scanner := newUsageScanner()
	stream := "event: message_start\n" +
		`data: {"type":"message_start","message":{"usage":{"input_tokens":25,"cache_creation_input_tokens":100,"cache_read_input_tokens":900,"output_tokens":1}}}` + "\n\n" +
		"event: message_delta\n" +
		`data: {"type":"message_delta","usage":{"output_tokens":42}}` + "\n\n" +
		"data: [DONE]\n\n"
	// Split mid-line to prove the scanner buffers across writes.
	for _, chunk := range []string{stream[:40], stream[40:120], stream[120:]} {
		if _, err := scanner.Write([]byte(chunk)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	totals := scanner.Totals()
	if !totals.Seen {
		t.Fatal("usage not seen")
	}
	if totals.ContextTokens() != 1025 {
		t.Fatalf("context tokens = %d, want 1025", totals.ContextTokens())
	}
	if totals.OutputTokens != 42 {
		t.Fatalf("output tokens = %d, want 42", totals.OutputTokens)
	}
}

func TestUsageScannerIgnoresNonUsageData(t *testing.T) {
	scanner := newUsageScanner()
	_, _ = scanner.Write([]byte("data: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"hi\"}}\n\n"))
	_, _ = scanner.Write([]byte("data: not json\n\n"))
	if scanner.Totals().Seen {
		t.Fatal("unexpected usage")
	}
}

func TestUsageScannerDropsOversizedPartialLine(t *testing.T) {
	scanner := newUsageScanner()
	scanner.maxLine = 16
	_, _ = scanner.Write([]byte(strings.Repeat("x", 32)))
	if len(scanner.pending) != 0 {
		t.Fatalf("pending = %d, want 0", len(scanner.pending))
	}
}
