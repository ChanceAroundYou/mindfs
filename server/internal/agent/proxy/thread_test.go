package proxy

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mindfs/server/internal/agent/fold"
)

// A Responses body with two tool rounds, each carrying 40 KB of output. With
// one round of tail protected, the older one is a fold candidate.
func codexTestBody() []byte {
	big := strings.Repeat("A", 40000)
	return []byte(`{"model":"gpt-6-luna","store":false,"input":[` +
		`{"type":"custom_tool_call","call_id":"c1","name":"exec","input":"ls"},` +
		`{"type":"custom_tool_call_output","call_id":"c1","output":[{"type":"input_text","text":"` + big + `"}]},` +
		`{"type":"custom_tool_call","call_id":"c2","name":"exec","input":"ls"},` +
		`{"type":"custom_tool_call_output","call_id":"c2","output":[{"type":"input_text","text":"` + big + `"}]}` +
		`]}`)
}

const codexUsageEvent = "event: response.completed\n" +
	`data: {"type":"response.completed","response":{"usage":{"input_tokens":500000,"input_tokens_details":{"cached_tokens":0},"output_tokens":10}}}` + "\n\n"

// A folding configuration that fires on the small test body instead of waiting
// for a real 100k-token rail.
func codexFoldConfig(t *testing.T) LiveCompactConfig {
	return LiveCompactConfig{
		Mode:             LiveCompactOn,
		ArchiveDir:       t.TempDir(),
		ArchiveEnabled:   true,
		KeepRounds:       1,
		KeepTailTokens:   1,
		RailTokens:       10,
		TargetTokens:     5,
		MinFoldBytes:     1,
		MinBodyBytes:     1,
		LargeBlockTokens: 100000,
	}
}

// postCodexThread sends a Responses request the way Codex does: through
// /c/{provider}/v1/responses with the thread identity in a header.
func postCodexThread(t *testing.T, s *Server, header, threadID string, body []byte) []byte {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost,
		fmt.Sprintf("http://127.0.0.1:%d/c/llmux/v1/responses", s.Port()),
		strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(header, threadID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return body
}

// The whole point of thread registration: a Codex request is folded as the
// MindFS session that opened it, and the turn lands in that session's ledger
// with a real rewrite cost from the streamed usage block.
func TestCodexRequestUsesRegisteredThreadSession(t *testing.T) {
	var forwarded []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(codexUsageEvent))
	}))
	defer upstream.Close()

	s, err := NewServerWithLiveCompact(codexFoldConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.RegisterProvider("llmux", upstream.URL+"/v1")
	s.RegisterThread("thread-1", "codex-mindfs-session-key")

	body := codexTestBody()
	postCodexThread(t, s, "session-id", "thread-1", body)

	if len(forwarded) >= len(body) {
		t.Fatalf("request was not folded: %d -> %d bytes", len(body), len(forwarded))
	}
	// The ledger line is written after the response stream ends, so the client
	// can observe EOF first; poll rather than assume the order.
	var saving fold.ContextSaving
	deadline := time.Now().Add(2 * time.Second)
	for {
		saving = s.SessionContextSaving("codex-mindfs-session-key")
		if saving.Requests > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if saving.Requests != 1 {
		t.Fatalf("ledger requests = %d, want 1", saving.Requests)
	}
	if saving.MaxSavedBytes <= 0 {
		t.Fatalf("ledger saved bytes = %d", saving.MaxSavedBytes)
	}
	if saving.Latest.UsageSource != "response" || saving.Latest.RewriteTokens != 500000 {
		t.Fatalf("usage = %+v", saving.Latest)
	}
	// keep_rounds=1 protects the newest round: its 40 KB output stays verbatim
	// while the older one is replaced by a pointer, so the request shrinks by
	// about one block and no more.
	if !strings.Contains(string(forwarded), strings.Repeat("A", 40000)) {
		t.Fatal("the newest round must stay verbatim")
	}
	if len(forwarded) > len(body)-20000 {
		t.Fatalf("the older round was not folded: %d -> %d bytes", len(body), len(forwarded))
	}
}

// Without a registration the proxy has no session to attribute the turn to, so
// the only safe behaviour is forwarding the exact bytes it received.
func TestUnknownThreadIsObserveOnly(t *testing.T) {
	var forwarded []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(codexUsageEvent))
	}))
	defer upstream.Close()

	s, err := NewServerWithLiveCompact(codexFoldConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.RegisterProvider("llmux", upstream.URL+"/v1")

	body := codexTestBody()
	postCodexThread(t, s, "session-id", "thread-unknown", body)

	if string(forwarded) != string(body) {
		t.Fatalf("unregistered thread must be forwarded verbatim: %d -> %d bytes", len(body), len(forwarded))
	}
}

// `session-id` is the documented header; the other two are fallbacks that
// carried the same value in real captures.
func TestProviderThreadIDHeaderFallbacks(t *testing.T) {
	cases := []struct{ header, want string }{
		{"session-id", "thread-1"},
		{"thread-id", "thread-1"},
		{"x-client-request-id", "thread-1"},
	}
	for _, tc := range cases {
		t.Run(tc.header, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/c/llmux/v1/responses", nil)
			req.Header.Set(tc.header, "thread-1")
			if got := providerThreadID(req); got != "thread-1" {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
	if got := providerThreadID(httptest.NewRequest(http.MethodPost, "/c/x/v1/responses", nil)); got != "" {
		t.Fatalf("no header must yield empty, got %q", got)
	}
}

// A closed session must stop being foldable: its thread id will be reused by a
// fresh session that re-registers.
func TestUnregisterThreadDropsState(t *testing.T) {
	s, err := NewServerWithLiveCompact(LiveCompactConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.RegisterThread("thread-1", "key-1")
	if key, ok := s.ThreadSessionKey("thread-1"); !ok || key != "key-1" {
		t.Fatalf("got %q ok=%v", key, ok)
	}
	s.UnregisterThread("thread-1")
	if _, ok := s.ThreadSessionKey("thread-1"); ok {
		t.Fatal("thread state must be gone")
	}
}
