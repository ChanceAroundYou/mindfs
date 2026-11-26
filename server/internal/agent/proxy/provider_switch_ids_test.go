package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// History replayed after llmux → default, reduced to the shapes that matter:
// reasoning, message, function_call and its output all carry ids minted by
// llmux. The default provider answers 404 for the first unknown reference.
const crossProviderHistoryBody = `{"model":"gpt-6","store":false,"input":[` +
	`{"type":"reasoning","id":"rs_e2bc7716c26840219aebda69f870e237","summary":[{"type":"summary_text","text":"We need continue task."}],"encrypted_content":""},` +
	`{"type":"message","role":"assistant","id":"msg_old","content":[{"type":"output_text","text":"hi"}]},` +
	`{"type":"function_call","id":"fc_old","call_id":"call_old","name":"exec","arguments":"{}"},` +
	`{"type":"function_call_output","call_id":"call_old","output":"ok"},` +
	`{"type":"message","role":"user","content":[{"type":"input_text","text":"next"}]}]}`

const unresolvableItemError = `{"error":{"message":"Item with id 'rs_e2bc7716c26840219aebda69f870e237' not found. ` +
	"Items are not persisted when `store` is set to false. Try again with `store` set to true, " +
	`or remove this item from your input."}}`

func newItemIDTestServer(t *testing.T, upstream *httptest.Server) *Server {
	t.Helper()
	s, err := NewServerWithLiveCompact(LiveCompactConfig{ArchiveDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	s.RegisterProvider("chatgpt", upstream.URL)
	return s
}

func postProviderBody(t *testing.T, s *Server, threadID, body string) *httptest.ResponseRecorder {
	t.Helper()
	return postProviderBodyTo(t, s, "chatgpt", threadID, body)
}

func postProviderBodyTo(t *testing.T, s *Server, provider, threadID, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/c/"+provider+"/responses", strings.NewReader(body))
	req.Header.Set("session-id", threadID)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleProvider(rec, req)
	return rec
}

// The proactive half: a thread already marked as provider-switched must never
// send the old provider's item ids, because store:false leaves the new
// upstream nothing to resolve them against.
func TestHandleProviderStripsItemIDsOnSwitchedThread(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(body))
		mu.Unlock()
		_, _ = w.Write([]byte(`{"id":"resp"}`))
	}))
	defer upstream.Close()

	s := newItemIDTestServer(t, upstream)
	s.RegisterThread("thread-switched-ids", "session-key")
	s.MarkProviderSwitch("thread-switched-ids")

	rec := postProviderBody(t, s, "thread-switched-ids", crossProviderHistoryBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 {
		t.Fatalf("upstream requests = %d, want 1", len(bodies))
	}
	for _, stale := range []string{`"rs_e2bc7716c26840219aebda69f870e237"`, `"msg_old"`, `"fc_old"`} {
		if strings.Contains(bodies[0], stale) {
			t.Fatalf("stale id %s reached the upstream: %s", stale, bodies[0])
		}
	}
	for _, keep := range []string{`"call_id":"call_old"`, `"text":"next"`, `"output":"ok"`} {
		if !strings.Contains(bodies[0], keep) {
			t.Fatalf("payload %s was dropped: %s", keep, bodies[0])
		}
	}
}

// An unmarked thread has ids minted by the current provider; stripping them
// would be a silent behavior change for every normal session.
func TestHandleProviderKeepsItemIDsOnUnswitchedThread(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(body))
		mu.Unlock()
		_, _ = w.Write([]byte(`{"id":"resp"}`))
	}))
	defer upstream.Close()

	s := newItemIDTestServer(t, upstream)
	s.RegisterThread("thread-normal-ids", "session-key")

	rec := postProviderBody(t, s, "thread-normal-ids", crossProviderHistoryBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 || !strings.Contains(bodies[0], `"id":"rs_e2bc7716c26840219aebda69f870e237"`) {
		t.Fatalf("unswitched thread lost its ids: requests=%d body=%s", len(bodies), strings.Join(bodies, "\n---\n"))
	}
}

// The safety net: a thread whose switch marker was never persisted fails its
// first turn with the upstream 404. The retry must drop the references, succeed,
// and mark the thread so later turns strip before sending.
func TestHandleProviderRecoversUnresolvableItemID(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(body))
		n := len(bodies)
		mu.Unlock()
		if n == 1 {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(unresolvableItemError))
			return
		}
		_, _ = w.Write([]byte(`{"id":"resp"}`))
	}))
	defer upstream.Close()

	s := newItemIDTestServer(t, upstream)
	s.RegisterThread("thread-poisoned-ids", "session-key")

	rec := postProviderBody(t, s, "thread-poisoned-ids", crossProviderHistoryBody)
	if rec.Code != http.StatusOK || rec.Body.String() != `{"id":"resp"}` {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !s.providerSwitchedThread("thread-poisoned-ids") {
		t.Fatal("recovery did not mark the thread")
	}

	// A later turn of the same thread goes out stripped on the first attempt.
	rec = postProviderBody(t, s, "thread-poisoned-ids", crossProviderHistoryBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("follow-up status = %d body = %s", rec.Code, rec.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 3 {
		t.Fatalf("upstream requests = %d, want 3 (404, retry, proactive)", len(bodies))
	}
	if strings.Contains(bodies[1], `"id":"rs_e2bc7716c26840219aebda69f870e237"`) || strings.Contains(bodies[2], `"id":"rs_e2bc7716c26840219aebda69f870e237"`) {
		t.Fatalf("stale id survived recovery: retry=%s follow-up=%s", bodies[1], bodies[2])
	}
}

// A 404 from a genuinely missing endpoint is not an item-reference problem and
// must pass through untouched.
func TestHandleProviderPassesThroughUnrelated404(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"message":"unknown endpoint"}}`))
	}))
	defer upstream.Close()

	s := newItemIDTestServer(t, upstream)
	s.RegisterThread("thread-plain-404", "session-key")

	rec := postProviderBody(t, s, "thread-plain-404", crossProviderHistoryBody)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 1 {
		t.Fatalf("upstream requests = %d, want 1 (no retry)", requests)
	}
}

// The reverse direction (default → llmux) must send the same stateless shape;
// the marker belongs to the thread, not to one provider.
func TestHandleProviderStripsItemIDsOnReverseSwitch(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(body))
		mu.Unlock()
		_, _ = w.Write([]byte(`{"id":"resp"}`))
	}))
	defer upstream.Close()

	s := newItemIDTestServer(t, upstream)
	s.RegisterProvider("llmux", upstream.URL)
	s.RegisterThread("thread-reverse-ids", "session-key")
	s.MarkProviderSwitch("thread-reverse-ids")

	rec := postProviderBodyTo(t, s, "llmux", "thread-reverse-ids", crossProviderHistoryBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 {
		t.Fatalf("upstream requests = %d, want 1", len(bodies))
	}
	if strings.Contains(bodies[0], `"id":"rs_e2bc7716c26840219aebda69f870e237"`) {
		t.Fatalf("stale id reached llmux: %s", bodies[0])
	}
	if !strings.Contains(bodies[0], `"call_id":"call_old"`) {
		t.Fatalf("call pairing was dropped: %s", bodies[0])
	}
}
