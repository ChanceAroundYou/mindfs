package proxy

import (
	"path/filepath"
	"testing"

	"mindfs/server/internal/agent/fold"
)

// The chip's "这些数字来自中转估算" note used to be inferred from the counters
// themselves: cache_read > 0 && cache_creation == 0. That is false in both
// directions — a relay can report cache writes, and the provider's own API
// reports none on a cold prefix — and it mislabelled every Codex turn once the
// Responses usage block started reaching the ledger. The host the request was
// forwarded to is the only honest signal, so provenance is decided there.
func TestUsageProvenanceFollowsTheUpstreamHost(t *testing.T) {
	cases := []struct{ name, base, want string }{
		{"anthropic direct", "https://api.anthropic.com", fold.UsageProvenanceUpstream},
		{"anthropic direct with case port and path", "https://API.Anthropic.com:443/v1", fold.UsageProvenanceUpstream},
		{"chatgpt backend", "https://chatgpt.com/backend-api/codex", fold.UsageProvenanceUpstream},
		{"openai api", "https://api.openai.com/v1", fold.UsageProvenanceUpstream},
		{"llmux relay", "https://openwrt.xiaokubao.space/llmux/v1", fold.UsageProvenanceRelay},
		{"loopback relay", "http://127.0.0.1:7331/v1", fold.UsageProvenanceRelay},
		{"no base", "", ""},
		{"unparsable base", "://nope", ""},
	}
	for _, c := range cases {
		if got := usageProvenance(fold.UsageSourceResponse, c.base); got != c.want {
			t.Errorf("%s: %q -> %q, want %q", c.name, c.base, got, c.want)
		}
	}
	// No sample means no numbers to attribute: provenance must stay empty rather
	// than inherit the host and imply the counters were measured.
	if got := usageProvenance(fold.UsageSourceMissing, "https://api.anthropic.com"); got != "" {
		t.Fatalf("a missing sample has no provenance, got %q", got)
	}
}

// The /c/ route knows the forwarded base at write time; the row must carry it.
func TestCodexLedgerRowRecordsWhichUpstreamProducedTheUsage(t *testing.T) {
	s, _ := newCodexRoute(t, codexFoldConfig(t), codexStreamWithUsage(120000, 40000, 10))
	s.RegisterThread("thread-2", "codex-session-b")
	s.RegisterThread("thread-3", "codex-session-c")

	measured := fold.Usage{Source: fold.UsageSourceResponse, ContextTokens: 1000, CacheRead: 200, RewriteTokens: 800}
	cases := []struct {
		thread, key, base, want string
		usage                   fold.Usage
	}{
		{"thread-1", "codex-mindfs-session-key", "https://chatgpt.com/backend-api/codex", fold.UsageProvenanceUpstream, measured},
		{"thread-2", "codex-session-b", "https://openwrt.xiaokubao.space/llmux/v1", fold.UsageProvenanceRelay, measured},
		{"thread-3", "codex-session-c", "https://chatgpt.com/backend-api/codex", "", fold.Usage{Source: fold.UsageSourceMissing, RewriteTokens: -1}},
	}
	for _, c := range cases {
		_, _, pending, _ := s.foldRequestLedgerFor(threadStateKey(c.thread), "responses", codexTestBody())
		if pending == nil {
			t.Fatalf("%s: fixture must fold", c.thread)
		}
		s.writeFoldLedgerUsage(threadStateKey(c.thread), pending, c.usage, c.base)
		rows := s.SessionFoldUsage(c.key)
		if len(rows) != 1 {
			t.Fatalf("%s: rows = %d, want 1", c.thread, len(rows))
		}
		if rows[0].UsageProvenance != c.want {
			t.Fatalf("%s: provenance = %q, want %q", c.thread, rows[0].UsageProvenance, c.want)
		}
	}
}

// The Messages path derives the same field from the base URL the session was
// registered with (realBaseURL), without the fold code passing it around.
func TestMessagesLedgerRowTakesProvenanceFromTheRegisteredBase(t *testing.T) {
	s, err := NewServerWithLiveCompact(LiveCompactConfig{
		Mode: LiveCompactOn, KeepRounds: 1, MinBlockTokens: 100, RailTokens: -1,
		MinBodyBytes: 1, ArchiveDir: filepath.Join(t.TempDir(), "context"), ArchiveEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	cases := []struct{ token, base, key, want string }{
		{"direct", "https://api.anthropic.com", "session-direct", fold.UsageProvenanceUpstream},
		{"relay", "https://openwrt.xiaokubao.space/llmux/v1", "session-relay", fold.UsageProvenanceRelay},
	}
	for _, c := range cases {
		s.RegisterSessionWithKey(c.token, c.base, c.key)
		pending := s.newFoldLedgerPending(harnessClaude, "on", 100, 50, 40, 20, foldResult{}, 0)
		if pending == nil {
			t.Fatal("server has no ledger")
		}
		s.writeFoldLedger(c.token, pending, usageTotals{
			InputTokens: 10, CacheCreationInputTokens: 2, CacheReadInputTokens: 5,
			OutputTokens: 3, Seen: true,
		})
		rows := s.SessionFoldUsage(c.key)
		if len(rows) != 1 {
			t.Fatalf("%s: rows = %d, want 1", c.key, len(rows))
		}
		if rows[0].UsageProvenance != c.want {
			t.Fatalf("%s: provenance = %q, want %q", c.key, rows[0].UsageProvenance, c.want)
		}
	}
}
