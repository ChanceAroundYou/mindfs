package proxy

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLiveCompactNormalizedDefaults(t *testing.T) {
	cfg := LiveCompactConfig{Mode: " SHADOW "}.Normalized()
	if cfg.Mode != LiveCompactShadow {
		t.Fatalf("mode = %q, want shadow", cfg.Mode)
	}
	if cfg.KeepRounds != 3 || cfg.MinBlockTokens != 2000 || cfg.MinBodyBytes != 100<<10 || cfg.MaxDriftMessages != 8192 {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
	if got := (LiveCompactConfig{Mode: "bogus"}).Normalized().Mode; got != LiveCompactOff {
		t.Fatalf("unknown mode = %q, want off", got)
	}
	if got := (LiveCompactConfig{}).Normalized().Mode; got != LiveCompactOff {
		t.Fatalf("zero config = %q, want off", got)
	}
}

func TestLiveCompactShadowForwardsRequestUnchanged(t *testing.T) {
	var captured []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		// A normal message_start carries the real usage; the proxy must
		// forward these bytes untouched while observing them.
		_, _ = fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":100,\"cache_read_input_tokens\":900}}}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	s, err := NewServerWithLiveCompact(LiveCompactConfig{Mode: LiveCompactShadow, MinBodyBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	token := "shadow1"
	s.RegisterSessionWithKey(token, upstream.URL, "session-1")

	payload := wireBody(t, foldTestMessages(5, 8001))
	resp, err := http.Post(
		fmt.Sprintf("http://127.0.0.1:%d/p/%s/v1/messages", s.Port(), token),
		"application/json",
		bytes.NewReader(payload),
	)
	if err != nil {
		t.Fatal(err)
	}
	responseBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !bytes.Equal(captured, payload) {
		t.Fatal("shadow mode must forward the request byte-for-byte")
	}
	if !bytes.Contains(responseBody, []byte(`"input_tokens":100`)) {
		t.Fatal("response stream was altered")
	}

	s.mu.Lock()
	state := s.sessions[token]
	s.mu.Unlock()

	// The handler records usage after the response copy completes, which can
	// land just after the client sees EOF.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if !s.flushLiveCompactWork(2 * time.Second) {
			t.Fatal("queued observation did not finish")
		}
		s.mu.Lock()
		requests := state.live.requests
		s.mu.Unlock()
		if requests == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("observation not recorded: requests=%d", requests)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestLiveCompactOffPassesBrokenBodyThrough(t *testing.T) {
	var captured []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	s, err := NewServerWithLiveCompact(LiveCompactConfig{Mode: LiveCompactOff, MinBodyBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	token := "off1"
	s.RegisterSession(token, upstream.URL)

	payload := []byte(`{"messages": not-json`)
	resp, err := http.Post(
		fmt.Sprintf("http://127.0.0.1:%d/p/%s/v1/messages", s.Port(), token),
		"application/json",
		bytes.NewReader(payload),
	)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if !bytes.Equal(captured, payload) {
		t.Fatal("off mode must not touch the body")
	}
}

func TestLiveCompactShadowToleratesBrokenBody(t *testing.T) {
	var captured []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	s, err := NewServerWithLiveCompact(LiveCompactConfig{Mode: LiveCompactShadow, MinBodyBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	token := "shadow-broken"
	s.RegisterSession(token, upstream.URL)

	payload := []byte(`{"messages": not-json`)
	resp, err := http.Post(
		fmt.Sprintf("http://127.0.0.1:%d/p/%s/v1/messages", s.Port(), token),
		"application/json",
		bytes.NewReader(payload),
	)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !bytes.Equal(captured, payload) {
		t.Fatal("parse failure must forward the original body")
	}
}

func TestLiveCompactDisableFileStopsObservation(t *testing.T) {
	brake := filepath.Join(t.TempDir(), "DISABLED")
	s, err := NewServerWithLiveCompact(LiveCompactConfig{
		Mode:         LiveCompactShadow,
		MinBodyBytes: 1,
		DisableFile:  brake,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.RegisterSessionWithKey("tok", "http://127.0.0.1:1", "s1")
	body := wireBody(t, foldTestMessages(5, 8001))
	if s.observeLiveCompact("tok", body, liveCompactGate{}) == nil {
		t.Fatal("baseline observation missing")
	}

	if err := os.WriteFile(brake, []byte("stop"), 0o644); err != nil {
		t.Fatal(err)
	}
	resetDisableCache(s)
	if s.observeLiveCompact("tok", body, liveCompactGate{}) != nil {
		t.Fatal("brake file present but observation still ran")
	}

	if err := os.Remove(brake); err != nil {
		t.Fatal(err)
	}
	resetDisableCache(s)
	if s.observeLiveCompact("tok", body, liveCompactGate{}) == nil {
		t.Fatal("brake removed but observation stayed blocked")
	}
}

// resetDisableCache rewinds the one-second stat cache so a test does not sleep.
func resetDisableCache(s *Server) {
	s.disableMu.Lock()
	s.disableCheckedAt = time.Time{}
	s.disableMu.Unlock()
}

func TestLiveCompactObserveOnlySessionIsNotFoldable(t *testing.T) {
	s, err := NewServerWithLiveCompact(LiveCompactConfig{Mode: LiveCompactShadow, MinBodyBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.RegisterSessionObserveOnly("tok", "http://127.0.0.1:1", "router-session")
	if s.observeLiveCompact("tok", wireBody(t, foldTestMessages(5, 8001)), liveCompactGate{}) == nil {
		t.Fatal("observe-only session should still be measured")
	}
	if !s.flushLiveCompactWork(2 * time.Second) {
		t.Fatal("queued observation did not run")
	}
	s.mu.Lock()
	st := s.sessions["tok"]
	foldAllowed := st.foldAllowed
	requests := st.live.requests
	s.mu.Unlock()
	if requests != 1 {
		t.Fatalf("observe-only session requests = %d, want 1", requests)
	}
	if foldAllowed {
		t.Fatal("observe-only session must never be folded")
	}
}

func TestLiveCompactIgnoresCountTokensPath(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	s, err := NewServerWithLiveCompact(LiveCompactConfig{Mode: LiveCompactShadow, MinBodyBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.RegisterSessionWithKey("tok", upstream.URL, "s1")

	payload := wireBody(t, foldTestMessages(5, 8001))
	resp, err := http.Post(
		fmt.Sprintf("http://127.0.0.1:%d/p/tok/v1/messages/count_tokens", s.Port()),
		"application/json",
		bytes.NewReader(payload),
	)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	s.mu.Lock()
	requests := s.sessions["tok"].live.requests
	s.mu.Unlock()
	if requests != 0 {
		t.Fatalf("count_tokens must stay out of scope, observed %d request(s)", requests)
	}
}

// Ordinary short turns must not pay for JSON parsing; the default threshold is
// 100 KiB and a typical request is far below it.
func TestLiveCompactSkipsSmallBodies(t *testing.T) {
	s, err := NewServerWithLiveCompact(LiveCompactConfig{Mode: LiveCompactShadow})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.RegisterSessionWithKey("tok", "http://127.0.0.1:1", "s1")
	body := wireBody(t, foldTestMessages(5, 8001)) // ~40 KiB
	if int64(len(body)) >= 100<<10 {
		t.Fatalf("test body grew to %d bytes; adjust the fixture below the threshold", len(body))
	}
	if s.observeLiveCompact("tok", body, liveCompactGate{}) != nil {
		t.Fatal("body below min_body_bytes was queued for parsing")
	}
	s.mu.Lock()
	requests := s.sessions["tok"].live.requests
	s.mu.Unlock()
	if requests != 0 {
		t.Fatalf("small body was analyzed: requests=%d", requests)
	}
}

// When the client negotiates compression, the proxy sees compressed bytes and
// must skip usage scanning while forwarding the stream untouched.
func TestLiveCompactCompressedResponseIsForwardedUnscanned(t *testing.T) {
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	_, _ = zw.Write([]byte("event: message_start\ndata: {\"usage\":{\"input_tokens\":5}}\n\n"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	raw := compressed.Bytes()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(raw)
	}))
	defer upstream.Close()

	s, err := NewServerWithLiveCompact(LiveCompactConfig{Mode: LiveCompactShadow, MinBodyBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.RegisterSessionWithKey("tok", upstream.URL, "s1")

	req, err := http.NewRequest(http.MethodPost,
		fmt.Sprintf("http://127.0.0.1:%d/p/tok/v1/messages", s.Port()),
		bytes.NewReader(wireBody(t, foldTestMessages(5, 8001))))
	if err != nil {
		t.Fatal(err)
	}
	// Explicit Accept-Encoding keeps net/http from transparently decompressing,
	// so the proxy really does see Content-Encoding: gzip.
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Equal(body, raw) {
		t.Fatal("compressed stream was altered")
	}
}

// A single session token can see overlapping requests (tool-result batch and
// a retry). Observation state must stay consistent under -race.
func TestLiveCompactConcurrentRequestsSameToken(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	s, err := NewServerWithLiveCompact(LiveCompactConfig{Mode: LiveCompactShadow, MinBodyBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.RegisterSessionWithKey("tok", upstream.URL, "s1")

	payload := wireBody(t, foldTestMessages(5, 8001))
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Post(
				fmt.Sprintf("http://127.0.0.1:%d/p/tok/v1/messages", s.Port()),
				"application/json",
				bytes.NewReader(payload),
			)
			if err != nil {
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}()
	}
	wg.Wait()

	// Analysis runs off the request path, so wait for the queue to drain.
	if !s.flushLiveCompactWork(5 * time.Second) {
		t.Fatal("queued observations did not finish")
	}
	s.mu.Lock()
	requests := s.sessions["tok"].live.requests
	s.mu.Unlock()
	if requests != 4 {
		t.Fatalf("observed requests = %d, want 4", requests)
	}
}

// The analysis worker shares the handler's body slice instead of copying it
// (that copy was 8 MB of request-path work). That is only safe while the
// handler treats the body as read-only after enqueueing. This test drives a
// body carrying a post-compact marker — the one request-path rewrite in the
// proxy — and checks the forwarded bytes with -race enabled, so an in-place
// mutation on the handler side would be reported as a data race with the
// worker.
func TestLiveCompactSharesBodyWithoutRacingTheRewrite(t *testing.T) {
	var forwarded []byte
	var mu sync.Mutex
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, _ := io.ReadAll(r.Body)
		mu.Lock()
		forwarded = payload
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	s, err := NewServerWithLiveCompact(LiveCompactConfig{Mode: LiveCompactShadow, MinBodyBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.RegisterSessionWithKey("tok", upstream.URL, "s1")

	// A block that stripCompactPayload rewrites, plus a large tool result so the
	// observation actually parses.
	big := strings.Repeat("a", 8001)
	body, err := json.Marshal(map[string]any{
		"model": "test",
		"messages": []any{
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "text", "text": skillPreambleMarker + "\nskill body"},
			}},
			assistantToolUse("toolu_a", "Read"),
			userToolResult("toolu_a", big, false),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.Post(
		fmt.Sprintf("http://127.0.0.1:%d/p/tok/v1/messages", s.Port()),
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if !s.flushLiveCompactWork(5 * time.Second) {
		t.Fatal("queued observation did not finish")
	}
	mu.Lock()
	got := forwarded
	mu.Unlock()
	if len(got) == 0 {
		t.Fatal("nothing was forwarded")
	}
	if len(got) >= len(body) {
		t.Fatalf("strip did not shrink the body: forwarded=%d original=%d", len(got), len(body))
	}
	if !strings.Contains(string(got), big[:64]) {
		t.Fatal("forwarded body lost the tool result")
	}
}
