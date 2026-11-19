package proxy

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// The chip shows what a fold saved; it must also show what the fold request
// itself paid at 1x. That number is the fold request's own input_tokens, which
// only exists after the response stream ends — so the ledger line has to be
// written on the response path, not when the body is rewritten.

func upstreamWithUsage(t *testing.T, start string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":%s}\n\n", start)
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	return server
}

func TestProxyFoldLedgerRecordsRewrite(t *testing.T) {
	archiveDir := filepath.Join(t.TempDir(), "context")
	s, err := NewServerWithLiveCompact(LiveCompactConfig{
		Mode: LiveCompactOn, KeepRounds: 1, MinBlockTokens: 100, RailTokens: -1,
		MinBodyBytes: 1, ArchiveDir: archiveDir, ArchiveEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	upstream := upstreamWithUsage(t, `{"usage":{"input_tokens":1234,"cache_read_input_tokens":5678,"output_tokens":7}}`)
	s.RegisterSessionWithKey("rw1", upstream.URL, "session-rw1")

	postToProxy(t, s, "rw1", foldBody(t, 4, 8001))
	summary := waitForLedgerRequests(t, s, "session-rw1", 1)
	if summary.Latest.RewriteTokens != 1234 {
		t.Fatalf("rewrite tokens = %d, want 1234 (the fold request's own input_tokens)", summary.Latest.RewriteTokens)
	}
	if summary.TotalRewriteTokens != 1234 {
		t.Fatalf("total rewrite tokens = %d, want 1234", summary.TotalRewriteTokens)
	}
}

// A turn whose usage cannot be read (upstream error, compressed stream) still
// gets its ledger line — with rewrite unknown, never a fabricated zero.
func TestProxyFoldLedgerRewriteUnknownWithoutUsage(t *testing.T) {
	archiveDir := filepath.Join(t.TempDir(), "context")
	s, err := NewServerWithLiveCompact(LiveCompactConfig{
		Mode: LiveCompactOn, KeepRounds: 1, MinBlockTokens: 100, RailTokens: -1,
		MinBodyBytes: 1, ArchiveDir: archiveDir, ArchiveEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer upstream.Close()
	s.RegisterSessionWithKey("rw2", upstream.URL, "session-rw2")

	postToProxy(t, s, "rw2", foldBody(t, 4, 8001))
	// Even a failed turn gets its line: the fold already happened, only the
	// rewrite cost is unknown.
	summary := waitForLedgerRequests(t, s, "session-rw2", 1)
	if summary.Latest.RewriteTokens != -1 {
		t.Fatalf("rewrite tokens = %d, want -1 (unknown)", summary.Latest.RewriteTokens)
	}
	if summary.TotalRewriteTokens != 0 {
		t.Fatalf("total rewrite tokens = %d, want 0 when every sample is unknown", summary.TotalRewriteTokens)
	}
}

func TestLedgerSummarySumsKnownRewrite(t *testing.T) {
	first := ledgerTestEntry(100)
	first.RewriteTokens = 40
	second := ledgerTestEntry(200)
	second.RewriteTokens = 60
	unknown := ledgerTestEntry(300)
	unknown.RewriteTokens = -1
	summary := summarizeLedger([]LedgerEntry{first, second, unknown})
	if summary.TotalRewriteTokens != 100 {
		t.Fatalf("total rewrite = %d, want 100 (known samples only)", summary.TotalRewriteTokens)
	}
	if summary.Latest.RewriteTokens != -1 {
		t.Fatalf("latest rewrite = %d, want -1", summary.Latest.RewriteTokens)
	}
}
