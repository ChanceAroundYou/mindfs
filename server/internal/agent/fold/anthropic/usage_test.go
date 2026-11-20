package anthropic

import (
	"testing"

	"mindfs/server/internal/agent/fold"
)

func TestUsageScannerMergesStartAndDelta(t *testing.T) {
	scanner := NewUsageScanner()
	stream := "event: message_start\n" +
		`data: {"type":"message_start","message":{"usage":{"input_tokens":12,"cache_creation_input_tokens":300,"cache_read_input_tokens":4000,"output_tokens":1}}}` + "\n\n" +
		"event: message_delta\n" +
		`data: {"type":"message_delta","usage":{"output_tokens":250}}` + "\n\n"
	if _, err := scanner.Write([]byte(stream)); err != nil {
		t.Fatal(err)
	}
	totals := scanner.Totals()
	if totals.InputTokens != 12 || totals.CacheCreationInputTokens != 300 || totals.CacheReadInputTokens != 4000 || totals.OutputTokens != 250 {
		t.Fatalf("totals = %+v", totals)
	}
	if got := totals.ContextTokens(); got != 4312 {
		t.Fatalf("context = %d", got)
	}
	usage := totals.FoldUsage()
	if usage.RewriteTokens != 312 || usage.Source != fold.UsageSourceResponse {
		t.Fatalf("fold usage = %+v", usage)
	}
}

func TestAdapterUsageReadsJSONBody(t *testing.T) {
	body := []byte(`{"id":"msg_1","usage":{"input_tokens":7,"cache_creation_input_tokens":0,"cache_read_input_tokens":900,"output_tokens":33}}`)
	usage, ok := New().Usage(body)
	if !ok {
		t.Fatal("usage not found")
	}
	if usage.ContextTokens != 907 || usage.CacheRead != 900 || usage.Output != 33 || usage.RewriteTokens != 7 {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestAdapterUsageReadsReplayedStream(t *testing.T) {
	stream := []byte("data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":5,\"cache_read_input_tokens\":100}}}\n\n")
	usage, ok := New().Usage(stream)
	if !ok {
		t.Fatal("stream usage not found")
	}
	if usage.ContextTokens != 105 {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestAdapterUsageReportsMissing(t *testing.T) {
	if _, ok := New().Usage([]byte(`{"id":"msg_2"}`)); ok {
		t.Fatal("a body without usage must report missing")
	}
	if _, ok := New().Usage(nil); ok {
		t.Fatal("an empty body must report missing")
	}
}

func TestUsageScannerDropsOversizedLine(t *testing.T) {
	scanner := NewUsageScanner()
	scanner.maxLine = 16
	if _, err := scanner.Write([]byte(`data: {"usage":{"input_tokens":1}}`)); err != nil {
		t.Fatal(err)
	}
	if scanner.Totals().Seen {
		t.Fatal("an unterminated oversized line must be dropped, not parsed")
	}
}
