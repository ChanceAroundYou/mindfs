package proxy

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// End-to-end fold behavior through the real proxy handler. The unit tests pin
// the planner; these pin that the handler forwards what the planner produced,
// and that every mode other than `on` leaves the request byte-identical.

// postMessages sends body through the proxy to a fresh upstream and returns the
// upstream's view of the request body. The upstream answers with a minimal SSE
// stream so the response path runs too.
func postMessages(t *testing.T, s *Server, token string, body []byte) ([]byte, int) {
	t.Helper()
	var captured []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":100}}}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()
	s.RegisterSessionWithKey(token, upstream.URL, "session-"+token)

	resp, err := http.Post(
		fmt.Sprintf("http://127.0.0.1:%d/p/%s/v1/messages", s.Port(), token),
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return captured, resp.StatusCode
}

func foldBody(t *testing.T, rounds, size int) []byte {
	t.Helper()
	return wireBody(t, foldTestMessages(rounds, size))
}

// mode=on with an archive directory rewrites the eligible old rounds and leaves
// the newest ones alone.
func TestProxyFoldOnRewritesOldToolResults(t *testing.T) {
	archiveDir := filepath.Join(t.TempDir(), "context")
	s, err := NewServerWithLiveCompact(LiveCompactConfig{
		Mode: LiveCompactOn, KeepRounds: 1, MinBlockTokens: 100,
		MinBodyBytes: 1, ArchiveDir: archiveDir, ArchiveEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	body := foldBody(t, 4, 8001)
	forwarded, status := postMessages(t, s, "foldon1", body)
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if bytes.Equal(forwarded, body) {
		t.Fatal("mode=on did not rewrite an eligible body")
	}
	if !bytes.Contains(forwarded, []byte("[MindFS live-compact]")) {
		t.Fatal("forwarded body has no fold pointer")
	}
	// The newest round must still be verbatim: its filler must still be in the
	// body at full length.
	if !bytes.Contains(forwarded, []byte(strings.Repeat("a", 8001))) {
		t.Fatal("the kept round was folded or truncated")
	}
	// The rewritten body must still be valid JSON with the same message count.
	before := decodedMessages(t, body)
	after := decodedMessages(t, forwarded)
	if len(before) != len(after) {
		t.Fatalf("message count %d -> %d", len(before), len(after))
	}
	// And the archive it points at must exist.
	entries, err := os.ReadDir(archiveDir)
	if err != nil {
		t.Fatalf("archive dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("folded blocks produced no archive files")
	}
}

// Every mode except `on` must forward byte-identical requests even when the
// body is eligible and an archive directory is configured.
func TestProxyFoldOffAndShadowForwardUnchanged(t *testing.T) {
	for _, mode := range []string{LiveCompactOff, LiveCompactShadow} {
		t.Run(mode, func(t *testing.T) {
			archiveDir := filepath.Join(t.TempDir(), "context")
			s, err := NewServerWithLiveCompact(LiveCompactConfig{
				Mode: mode, KeepRounds: 1, MinBlockTokens: 100,
				MinBodyBytes: 1, ArchiveDir: archiveDir, ArchiveEnabled: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()

			body := foldBody(t, 4, 8001)
			forwarded, status := postMessages(t, s, "mode"+mode, body)
			if status != http.StatusOK {
				t.Fatalf("status = %d", status)
			}
			if !bytes.Equal(forwarded, body) {
				t.Fatalf("mode=%s changed the request body", mode)
			}
			if entries, err := os.ReadDir(archiveDir); err == nil && len(entries) > 0 {
				t.Fatalf("mode=%s wrote %d archive files", mode, len(entries))
			}
		})
	}
}

// Isolated-ConfigDir sessions (Router, workflow, result transformers) inherit
// no tool permissions, so a pointer could be unreadable. They observe but never
// fold, even in mode=on with an archive configured.
func TestProxyFoldSkipsObserveOnlySessions(t *testing.T) {
	archiveDir := filepath.Join(t.TempDir(), "context")
	s, err := NewServerWithLiveCompact(LiveCompactConfig{
		Mode: LiveCompactOn, KeepRounds: 1, MinBlockTokens: 100,
		MinBodyBytes: 1, ArchiveDir: archiveDir, ArchiveEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	var captured []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()
	s.RegisterSessionObserveOnly("isolated", upstream.URL, "router-session")

	body := foldBody(t, 4, 8001)
	resp, err := http.Post(
		fmt.Sprintf("http://127.0.0.1:%d/p/isolated/v1/messages", s.Port()),
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if !bytes.Equal(captured, body) {
		t.Fatal("an observe-only session was folded")
	}
	if entries, err := os.ReadDir(archiveDir); err == nil && len(entries) > 0 {
		t.Fatalf("an observe-only session wrote archive files")
	}
}

// mode=on without an archive directory cannot fold anything: a pointer would
// name a file that does not exist. It degrades to observation.
func TestProxyFoldOnWithoutArchiveObservesOnly(t *testing.T) {
	s, err := NewServerWithLiveCompact(LiveCompactConfig{
		Mode: LiveCompactOn, KeepRounds: 1, MinBlockTokens: 100, MinBodyBytes: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	body := foldBody(t, 4, 8001)
	forwarded, status := postMessages(t, s, "noarchive", body)
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if !bytes.Equal(forwarded, body) {
		t.Fatal("mode=on without an archive directory must not rewrite")
	}
}

// The emergency brake stops folding immediately, without a restart.
func TestProxyFoldRespectsDisableFile(t *testing.T) {
	dir := t.TempDir()
	brake := filepath.Join(dir, "DISABLED")
	if err := os.WriteFile(brake, []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := NewServerWithLiveCompact(LiveCompactConfig{
		Mode: LiveCompactOn, KeepRounds: 1, MinBlockTokens: 100, MinBodyBytes: 1,
		ArchiveDir: filepath.Join(dir, "context"), ArchiveEnabled: true, DisableFile: brake,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	body := foldBody(t, 4, 8001)
	forwarded, _ := postMessages(t, s, "brake", body)
	if !bytes.Equal(forwarded, body) {
		t.Fatal("the brake file did not stop folding")
	}

	if err := os.Remove(brake); err != nil {
		t.Fatal(err)
	}
	s.disableMu.Lock()
	s.disableCheckedAt = time.Time{}
	s.disableMu.Unlock()

	forwarded, _ = postMessages(t, s, "brake", body)
	if bytes.Equal(forwarded, body) {
		t.Fatal("folding did not resume after the brake file was removed")
	}
}

// A body that is eligible but malformed must be forwarded unchanged: fail-open
// (I7) through the real handler, not just the planner.
func TestProxyFoldForwardsMalformedBody(t *testing.T) {
	dir := t.TempDir()
	s, err := NewServerWithLiveCompact(LiveCompactConfig{
		Mode: LiveCompactOn, KeepRounds: 1, MinBlockTokens: 100, MinBodyBytes: 1,
		ArchiveDir: filepath.Join(dir, "context"), ArchiveEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"},`)
	forwarded, status := postMessages(t, s, "malformed", body)
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if !bytes.Equal(forwarded, body) {
		t.Fatal("a malformed body was rewritten")
	}
}

// httptestUpstream starts a minimal SSE upstream and returns its URL. Shared by
// the handler-level tests in this package.
func httptestUpstream(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data\n")
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// postToProxy sends body through the proxy for token and discards the response.
func postToProxy(t *testing.T, s *Server, token string, body []byte) {
	t.Helper()
	resp, err := http.Post(
		fmt.Sprintf("http://127.0.0.1:%d/p/%s/v1/messages", s.Port(), token),
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}
