package proxy

import (
	"bytes"
	"fmt"
	"io"
	"mindfs/server/internal/agent/fold"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A fold is a commitment: once a block is a pointer, it must stay one. The
// commitment set lives in memory, and the proxy restarts with the service. If
// it started empty, the first request after every restart would unfold the
// whole prefix — the model would see the original tool output again and the
// upstream cache would be rebuilt from scratch.

func restartFoldBody(t *testing.T) []byte {
	t.Helper()
	return wireBody(t, []any{
		userText("start"),
		assistantToolUse("toolu_a", "Read"),
		userToolResult("toolu_a", bigText("alpha"), false),
		assistantToolUse("toolu_b", "Read"),
		userToolResult("toolu_b", bigText("beta"), false),
		assistantToolUse("toolu_c", "Read"),
		userToolResult("toolu_c", bigText("gamma"), false),
	})
}

// restartProxy starts a proxy on dir and returns it with the forwarded bytes of
// one request. railTokens chooses whether the rail may commit new blocks:
// <= 0 means "unmeasured, commit everything".
func restartProxy(t *testing.T, dir, token string, railTokens int, body []byte) (*Server, []byte) {
	t.Helper()
	s, err := NewServerWithLiveCompact(LiveCompactConfig{
		Mode: LiveCompactOn, KeepRounds: 1, MinBlockTokens: 100, RailTokens: railTokens,
		MinBodyBytes: 1, ArchiveDir: dir, ArchiveEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	var captured []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(upstream.Close)
	s.RegisterSessionWithKey(token, upstream.URL, "session-"+token)

	postToProxy(t, s, token, body)
	if len(captured) == 0 {
		t.Fatal("no request reached the upstream")
	}
	return s, captured
}

func TestRestartRestoresCommitsAndFoldsOnlySticky(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "context")
	body := restartFoldBody(t)

	first, forwardedFirst := restartProxy(t, dir, "restart1", -1, body)
	if bytes.Equal(forwardedFirst, body) {
		t.Fatal("the first instance did not fold anything")
	}
	if commits := first.sessions["restart1"].commits; commits == nil || commits.Len() != 2 {
		t.Fatalf("commits after the first fold = %v, want 2", commits)
	}

	// The second instance has no in-memory state. The rail is set high enough
	// that it would commit nothing new, so any rewrite here can only come from
	// the restored commitments.
	second, forwardedSecond := restartProxy(t, dir, "restart1", 1<<30, body)
	if !bytes.Equal(forwardedSecond, forwardedFirst) {
		t.Fatal("after restart the same request was not folded the same way")
	}
	commits := second.sessions["restart1"].commits
	if commits == nil || commits.Len() != 2 {
		t.Fatalf("restored commits = %v, want 2", commits)
	}
	if commits.RestoredCount() != 0 {
		t.Fatalf("restored names left unmatched = %d, want 0", commits.RestoredCount())
	}
}

// The persisted identity is the archive name, not the in-memory key: a
// restored record whose block is no longer in the transcript must not be
// mistaken for a live commitment, and must not make every later request pay a
// render that can never match.
func TestRestartDropsCommitThatLeftTheTranscript(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "context")
	log := newCommitLog(dir, "session-stale")
	if log == nil {
		t.Fatal("commit log not created")
	}
	log.Append([]foldCommitRecord{{TS: time.Now().Unix(), Name: "deadbeefdeadbeefdeadbeef.txt", Bytes: 1}})

	s, forwarded := restartProxy(t, dir, "stale", -1, restartFoldBody(t))
	if bytes.Equal(forwarded, restartFoldBody(t)) {
		t.Fatal("the request was not folded")
	}
	commits := s.sessions["stale"].commits
	if commits == nil {
		t.Fatal("no commitment set")
	}
	if commits.RestoredCount() != 0 {
		t.Fatalf("unmatched restored names = %d, want 0 after the first plan", commits.RestoredCount())
	}
}

// Shadow rehearses the policy and never rewrites the body, so it must never
// write a commitment either: a later mode=on turn would otherwise treat blocks
// it never folded as already folded.
func TestShadowDoesNotPersistCommits(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "context")
	s, err := NewServerWithLiveCompact(LiveCompactConfig{
		Mode: LiveCompactShadow, KeepRounds: 1, MinBlockTokens: 100, RailTokens: -1,
		MinBodyBytes: 1, ArchiveDir: dir, ArchiveEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()
	s.RegisterSessionWithKey("shadow1", upstream.URL, "session-shadow1")

	postToProxy(t, s, "shadow1", restartFoldBody(t))

	entries, err := os.ReadDir(filepath.Join(dir, fold.CommitLogDirName))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("shadow wrote %d commit logs", len(entries))
	}
}
