package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const switchedThreadBody = `{"model":"gpt-6","store":false,"input":[` +
	`{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},` +
	`{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"OmalbG91ZA=="},` +
	`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}` +
	`]}`

func TestStripEncryptedReasoningInput(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantRemoved int
		wantChanged bool
		// wantBody is checked verbatim when wantChanged is true.
		wantBody string
	}{
		{
			name:        "drops encrypted reasoning item and keeps the rest",
			body:        switchedThreadBody,
			wantRemoved: 1,
			wantChanged: true,
			wantBody: `{"model":"gpt-6","store":false,"input":[` +
				`{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},` +
				`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}` +
				`]}`,
		},
		{
			name:        "keeps reasoning without encrypted content",
			wantRemoved: 0,
			wantChanged: false,
			body:        `{"input":[{"type":"reasoning","id":"rs_1","summary":[]}]}`,
		},
		{
			name:        "keeps empty encrypted content",
			wantRemoved: 0,
			wantChanged: false,
			body:        `{"input":[{"type":"reasoning","id":"rs_1","encrypted_content":""}]}`,
		},
		{
			name:        "keeps encrypted content on a non-reasoning item",
			wantRemoved: 0,
			wantChanged: false,
			body:        `{"input":[{"type":"message","encrypted_content":"abc"}]}`,
		},
		{
			name:        "keeps a string input untouched",
			wantRemoved: 0,
			wantChanged: false,
			body:        `{"input":"plain string"}`,
		},
		{
			name:        "keeps a body without input untouched",
			wantRemoved: 0,
			wantChanged: false,
			body:        `{"model":"gpt-6"}`,
		},
		{
			name:        "keeps malformed json untouched",
			wantRemoved: 0,
			wantChanged: false,
			body:        `{"input":[`,
		},
		{
			name: "drops every encrypted reasoning item",
			body: `{"input":[` +
				`{"type":"reasoning","encrypted_content":"a"},` +
				`{"type":"message","role":"user"},` +
				`{"type":"reasoning","encrypted_content":"b"}` +
				`]}`,
			wantRemoved: 2,
			wantChanged: true,
			wantBody:    `{"input":[{"type":"message","role":"user"}]}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, removed, changed := stripEncryptedReasoningInput([]byte(tt.body))
			if removed != tt.wantRemoved || changed != tt.wantChanged {
				t.Fatalf("removed=%d changed=%v, want removed=%d changed=%v (body=%s)",
					removed, changed, tt.wantRemoved, tt.wantChanged, got)
			}
			if tt.wantChanged && string(got) != tt.wantBody {
				t.Fatalf("body = %s, want %s", got, tt.wantBody)
			}
			if !tt.wantChanged && string(got) != tt.body {
				t.Fatalf("unchanged body rewritten: %s", got)
			}
			if tt.wantChanged && !json.Valid(got) {
				t.Fatalf("rewritten body is not valid json: %s", got)
			}
		})
	}
}

func TestProviderSwitchMarkerSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	first, err := NewServerWithLiveCompact(LiveCompactConfig{ArchiveDir: dir})
	if err != nil {
		t.Fatalf("first server: %v", err)
	}
	first.MarkProviderSwitch("thread-persisted")
	if !first.providerSwitchedThread("thread-persisted") {
		t.Fatalf("marker not visible on the live server")
	}
	first.Close()

	second, err := NewServerWithLiveCompact(LiveCompactConfig{ArchiveDir: dir})
	if err != nil {
		t.Fatalf("second server: %v", err)
	}
	defer second.Close()
	if !second.providerSwitchedThread("thread-persisted") {
		t.Fatalf("marker lost across restart")
	}
	if second.providerSwitchedThread("thread-other") {
		t.Fatalf("unrelated thread reported as switched")
	}
}

func TestHandleProviderStripsEncryptedReasoningForSwitchedThread(t *testing.T) {
	forwarded := make(chan string, 4)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		forwarded <- string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp"}`))
	}))
	defer upstream.Close()

	s, err := NewServerWithLiveCompact(LiveCompactConfig{ArchiveDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	defer s.Close()
	s.RegisterProvider("chatgpt", upstream.URL)

	post := func(threadID, body string) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/c/chatgpt/responses", strings.NewReader(body))
		req.Header.Set("session-id", threadID)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.handleProvider(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
		}
		return <-forwarded
	}

	s.RegisterThread("thread-switched", "session-key")
	s.MarkProviderSwitch("thread-switched")
	got := post("thread-switched", switchedThreadBody)
	if strings.Contains(got, "encrypted_content") || strings.Contains(got, `"type":"reasoning"`) {
		t.Fatalf("encrypted reasoning reached upstream: %s", got)
	}
	if !bytes.Equal([]byte(got), []byte(`{"model":"gpt-6","store":false,"input":[`+
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},`+
		`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}`+
		`]}`)) {
		t.Fatalf("unexpected forwarded body: %s", got)
	}

	s.RegisterThread("thread-normal", "session-key-2")
	unchanged := post("thread-normal", switchedThreadBody)
	if unchanged != switchedThreadBody {
		t.Fatalf("normal thread body was rewritten: %s", unchanged)
	}
}

// A thread that was already forked onto the new provider before this fix
// shipped carries the old ciphertext without any marker. The upstream then
// rejects the first turn with invalid_encrypted_content. The proxy must retry
// once without the ciphertext and remember the thread for later turns.
func TestHandleProviderRecoversFromInvalidEncryptedContent(t *testing.T) {
	var mu sync.Mutex
	var received []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		received = append(received, string(body))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(body), "encrypted_content") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"invalid_encrypted_content",` +
				`"message":"could not be verified","type":"invalid_request_error"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"resp"}`))
	}))
	defer upstream.Close()

	s, err := NewServerWithLiveCompact(LiveCompactConfig{ArchiveDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	defer s.Close()
	s.RegisterProvider("chatgpt", upstream.URL)
	s.RegisterThread("thread-legacy-fork", "session-key")

	req := httptest.NewRequest(http.MethodPost, "/c/chatgpt/responses", strings.NewReader(switchedThreadBody))
	req.Header.Set("session-id", "thread-legacy-fork")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleProvider(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}

	mu.Lock()
	got := append([]string(nil), received...)
	mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("upstream requests = %d, want 2 (%v)", len(got), got)
	}
	if strings.Contains(got[1], "encrypted_content") {
		t.Fatalf("retry still carried ciphertext: %s", got[1])
	}
	if !s.providerSwitchedThread("thread-legacy-fork") {
		t.Fatalf("thread not remembered after recovery")
	}

	// The next turn must strip proactively instead of failing again.
	req2 := httptest.NewRequest(http.MethodPost, "/c/chatgpt/responses", strings.NewReader(switchedThreadBody))
	req2.Header.Set("session-id", "thread-legacy-fork")
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	s.handleProvider(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("second turn status = %d body = %s", rec2.Code, rec2.Body.String())
	}
	mu.Lock()
	total := len(received)
	mu.Unlock()
	if total != 3 {
		t.Fatalf("upstream requests after second turn = %d, want 3 (no second retry)", total)
	}
}

func TestHandleProviderDoesNotRetryWithoutCiphertext(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"invalid_encrypted_content"}}`))
	}))
	defer upstream.Close()

	s, err := NewServerWithLiveCompact(LiveCompactConfig{ArchiveDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	defer s.Close()
	s.RegisterProvider("chatgpt", upstream.URL)
	s.RegisterThread("thread-plain", "session-key")

	body := `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`
	req := httptest.NewRequest(http.MethodPost, "/c/chatgpt/responses", strings.NewReader(body))
	req.Header.Set("session-id", "thread-plain")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleProvider(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 1 {
		t.Fatalf("upstream requests = %d, want 1 (nothing to strip, no retry)", requests)
	}
}
