package responses

import (
	"testing"

	"mindfs/server/internal/agent/fold"
)

// Responses reports `input_tokens` as the whole context (cache included) and
// the cached part separately, so context is the input as-is and a rewrite costs
// the input that was not cached.
func TestUsageNormalization(t *testing.T) {
	resp := []byte(`{"usage":{"input_tokens":13582,"input_tokens_details":{"cached_tokens":13246,"cache_write_tokens":333},"output_tokens":25}}`)
	got, ok := New().Usage(resp)
	if !ok {
		t.Fatal("usage missing")
	}
	if got.ContextTokens != 13582 {
		t.Fatalf("context = %d", got.ContextTokens)
	}
	if got.CacheRead != 13246 || got.CacheWrite != 333 {
		t.Fatalf("cache = %+v", got)
	}
	if got.Output != 25 {
		t.Fatalf("output = %d", got.Output)
	}
	if got.RewriteTokens != 336 {
		t.Fatalf("rewrite = %d, want input-cached = 336", got.RewriteTokens)
	}
	if got.Source != fold.UsageSourceResponse {
		t.Fatalf("source = %q", got.Source)
	}
}

// A streamed turn reports usage once, inside the `response.completed` event;
// the deltas before it carry none. The last block wins.
func TestUsageFromStreamedResponse(t *testing.T) {
	resp := []byte("event: response.created\n" +
		`data: {"type":"response.created","response":{"id":"resp_1","usage":null}}` + "\n\n" +
		"event: response.output_text.delta\n" +
		`data: {"type":"response.output_text.delta","delta":"ok"}` + "\n\n" +
		"event: response.completed\n" +
		`data: {"type":"response.completed","response":{"id":"resp_1","usage":{"input_tokens":40000,"input_tokens_details":{"cached_tokens":39000},"output_tokens":12}}}` + "\n\n")
	got, ok := New().Usage(resp)
	if !ok {
		t.Fatal("usage missing")
	}
	if got.ContextTokens != 40000 || got.CacheRead != 39000 || got.Output != 12 || got.RewriteTokens != 1000 {
		t.Fatalf("got %+v", got)
	}
}

// The completed event is also served as the body of a non-streaming call; its
// usage sits one level down under `response`.
func TestUsageFromResponseObject(t *testing.T) {
	resp := []byte(`{"id":"resp_1","object":"response","status":"completed","usage":{"input_tokens":900,"input_tokens_details":{"cached_tokens":0},"output_tokens":4}}`)
	got, ok := New().Usage(resp)
	if !ok || got.ContextTokens != 900 || got.RewriteTokens != 900 || got.CacheRead != 0 {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
}

// A turn that ended without counters reports that explicitly instead of
// looking like a zero-cost turn.
func TestUsageMissingSource(t *testing.T) {
	got, ok := New().Usage([]byte(`{"id":"x"}`))
	if ok {
		t.Fatal("no usage block must report !ok")
	}
	if got.Source != fold.UsageSourceMissing || got.RewriteTokens != -1 {
		t.Fatalf("got %+v", got)
	}
	if _, ok := New().Usage(nil); ok {
		t.Fatal("empty body must report !ok")
	}
}

// A detail block that lags behind input_tokens cannot make a rewrite negative.
func TestUsageClampsRewriteAtZero(t *testing.T) {
	got, ok := New().Usage([]byte(`{"usage":{"input_tokens":100,"input_tokens_details":{"cached_tokens":140},"output_tokens":1}}`))
	if !ok {
		t.Fatal("usage missing")
	}
	if got.RewriteTokens != 0 {
		t.Fatalf("rewrite = %d, want 0", got.RewriteTokens)
	}
}

// The proxy feeds the scanner arbitrary chunk boundaries, so an event split
// across two writes must still be read exactly once.
func TestUsageScannerHandlesChunkBoundaries(t *testing.T) {
	stream := []byte("event: response.completed\n" +
		`data: {"type":"response.completed","response":{"usage":{"input_tokens":5000,"input_tokens_details":{"cached_tokens":4000},"output_tokens":7}}}` + "\n\n")
	scanner := NewUsageScanner()
	// Split in the middle of the JSON line.
	for _, chunk := range [][]byte{stream[:40], stream[40:91], stream[91:]} {
		if _, err := scanner.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	got, ok := scanner.Totals().Usage()
	if !ok {
		t.Fatal("usage missing")
	}
	if got.ContextTokens != 5000 || got.Output != 7 || got.RewriteTokens != 1000 {
		t.Fatalf("got %+v", got)
	}
}

// A stream that ends without a usage event reports missing, not zero.
func TestUsageScannerWithoutUsageEvent(t *testing.T) {
	scanner := NewUsageScanner()
	_, _ = scanner.Write([]byte("event: response.output_text.delta\ndata: {\"delta\":\"hi\"}\n\n"))
	got, ok := scanner.Totals().Usage()
	if ok || got.Source != fold.UsageSourceMissing || got.RewriteTokens != -1 {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
}
