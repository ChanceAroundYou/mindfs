package proxy

import (
	"mindfs/server/internal/agent/fold"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// The chip answers "what did folding cost". Before 2026-11-20 the ledger
// recorded only input_tokens, so a fold that re-priced 125k tokens through
// cache creation showed up as a few hundred. These pin the three counters and
// the explicit missing-sample marker.

func TestFoldLedgerRecordsThreeUsageCounters(t *testing.T) {
	archiveDir := filepath.Join(t.TempDir(), "context")
	s, err := NewServerWithLiveCompact(LiveCompactConfig{
		Mode: LiveCompactOn, KeepRounds: 1, MinBlockTokens: 100, RailTokens: -1,
		MinBodyBytes: 1, ArchiveDir: archiveDir, ArchiveEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	upstream := upstreamWithUsage(t, `{"usage":{"input_tokens":1234,"cache_creation_input_tokens":5000,"cache_read_input_tokens":99,"output_tokens":7}}`)
	s.RegisterSessionWithKey("u1", upstream.URL, "session-u1")

	postToProxy(t, s, "u1", foldBody(t, 4, 8001))
	summary := waitForLedgerRequests(t, s, "session-u1", 1)

	latest := summary.Latest
	if latest.UsageInput != 1234 || latest.UsageCacheCreation != 5000 || latest.UsageCacheRead != 99 {
		t.Fatalf("usage counters = (%d,%d,%d), want (1234,5000,99)",
			latest.UsageInput, latest.UsageCacheCreation, latest.UsageCacheRead)
	}
	// 完成记录要报「这一轮输出了多少」（Task 13）；只记输入会把整轮说成纯读。
	if latest.UsageOutput != 7 {
		t.Fatalf("usage output = %d, want 7", latest.UsageOutput)
	}
	if latest.UsageSource != fold.UsageSourceResponse {
		t.Fatalf("usage source = %q, want %q", latest.UsageSource, fold.UsageSourceResponse)
	}
	// The rewrite is what was billed at full price: fresh input plus the cache
	// write. Cache reads are the cheap path and must not be added.
	if latest.RewriteTokens != 6234 {
		t.Fatalf("rewrite tokens = %d, want 6234 (input + cache creation)", latest.RewriteTokens)
	}
	if summary.RewriteSamples != 1 || summary.UsageMissing != 0 {
		t.Fatalf("samples=%d missing=%d, want 1/0", summary.RewriteSamples, summary.UsageMissing)
	}
}

// A turn without a usage block must say so, not fabricate a rewrite of zero and
// not leave a bare -1 for the reader to interpret.
func TestFoldLedgerMarksMissingUsageExplicitly(t *testing.T) {
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
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer upstream.Close()
	s.RegisterSessionWithKey("u2", upstream.URL, "session-u2")

	postToProxy(t, s, "u2", foldBody(t, 4, 8001))
	summary := waitForLedgerRequests(t, s, "session-u2", 1)

	if summary.Latest.UsageSource != fold.UsageSourceMissing {
		t.Fatalf("usage source = %q, want %q", summary.Latest.UsageSource, fold.UsageSourceMissing)
	}
	if summary.Latest.RewriteTokens != -1 {
		t.Fatalf("rewrite tokens = %d, want -1", summary.Latest.RewriteTokens)
	}
	if summary.UsageMissing != 1 || summary.RewriteSamples != 0 {
		t.Fatalf("missing=%d samples=%d, want 1/0", summary.UsageMissing, summary.RewriteSamples)
	}
	if summary.TotalRewriteTokens != 0 {
		t.Fatalf("total rewrite = %d, want 0 with no samples", summary.TotalRewriteTokens)
	}
}
