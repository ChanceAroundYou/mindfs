package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mindfs/server/internal/agent/fold"
)

// A per-harness override must reach the Codex request path: the same node can
// run Claude Code at a high rail and Codex at a lower one, because Codex lives
// in a much larger window.
func TestCodexHarnessOverrideSelectsItsOwnRail(t *testing.T) {
	cfg := codexFoldConfig(t)
	cfg.RailTokens = 1000000
	cfg.TargetTokens = 900000
	cfg.Harnesses = map[string]fold.HarnessOverride{
		harnessCodex: {RailTokens: 10, TargetTokens: 5},
	}

	var forwarded []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(codexUsageEvent))
	}))
	defer upstream.Close()

	s, err := NewServerWithLiveCompact(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.RegisterProvider("llmux", upstream.URL+"/v1")
	s.RegisterThread("thread-1", "codex-key-1")

	body := codexTestBody()
	postCodexThread(t, s, "session-id", "thread-1", body)
	if len(forwarded) >= len(body) {
		t.Fatalf("codex rail override not applied: %d -> %d bytes", len(body), len(forwarded))
	}
	// The node-wide rail still governs the Claude side of the same server.
	if got := s.harnessRuntimeFor(harnessClaude).cfg.RailTokens; got != 1000000 {
		t.Fatalf("claude rail = %d, want the node value", got)
	}
}

// Two harnesses folding into one archive would make their commit logs and
// ledgers overwrite each other, so the Codex runtime gets its own directories
// — and the chip, which is asked for the pool key (`codex-<session>`), must
// read the ledger the Codex folds wrote.
func TestCodexHarnessKeepsItsOwnArchiveAndLedger(t *testing.T) {
	cfg := codexFoldConfig(t)
	dir := t.TempDir()
	cfg.ArchiveDir = dir

	var forwarded []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(codexUsageEvent))
	}))
	defer upstream.Close()

	s, err := NewServerWithLiveCompact(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.RegisterProvider("llmux", upstream.URL+"/v1")
	s.RegisterThread("thread-9", "codex-key-9")
	postCodexThread(t, s, "session-id", "thread-9", codexTestBody())
	if len(forwarded) == 0 {
		t.Fatal("request was not forwarded")
	}

	var saving fold.ContextSaving
	deadline := time.Now().Add(2 * time.Second)
	for {
		saving = s.SessionContextSaving("codex-key-9")
		if saving.Requests > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if saving.Requests != 1 {
		t.Fatalf("codex chip requests = %d, want 1", saving.Requests)
	}
	if got := s.SessionContextSaving("claude-key-9"); got.Requests != 0 {
		t.Fatalf("claude chip must not read the codex ledger: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, harnessCodex, fold.LedgerDirName)); err != nil {
		t.Fatalf("codex ledger dir missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, fold.LedgerDirName)); err == nil {
		t.Fatal("codex folds must not be written to the node ledger")
	}
}

// A harness switched off observes only: the request is forwarded byte for byte
// and nothing is committed.
func TestDisabledHarnessObservesOnly(t *testing.T) {
	disabled := false
	cfg := codexFoldConfig(t)
	cfg.Harnesses = map[string]fold.HarnessOverride{
		harnessCodex: {Enabled: &disabled},
	}

	var forwarded []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(codexUsageEvent))
	}))
	defer upstream.Close()

	s, err := NewServerWithLiveCompact(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.RegisterProvider("llmux", upstream.URL+"/v1")
	s.RegisterThread("thread-1", "codex-key-1")

	body := codexTestBody()
	postCodexThread(t, s, "session-id", "thread-1", body)
	if string(forwarded) != string(body) {
		t.Fatalf("disabled harness rewrote the request: %d -> %d bytes", len(body), len(forwarded))
	}
	if !strings.Contains(string(forwarded), strings.Repeat("A", 40000)) {
		t.Fatal("body must be untouched")
	}
}
