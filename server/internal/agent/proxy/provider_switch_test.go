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
			// 2026-11-25 放宽判据后：非 reasoning 条目上的顶层密文同样不可解密，
			// 必须删掉。这条目的只剩 type，删完没有任何可读载荷，整条丢弃。
			// （旧实现只认 reasoning 条目，于是 `amsg_…` 这类密文原样发给新供应商 → 400。）
			name:        "drops a non-reasoning item that carries only ciphertext",
			wantRemoved: 1,
			wantChanged: true,
			body:        `{"input":[{"type":"message","encrypted_content":"abc"}]}`,
			wantBody:    `{"input":[]}`,
		},
		{
			name:        "keeps visible payload and drops only the ciphertext member",
			wantRemoved: 1,
			wantChanged: true,
			body:        `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}],"encrypted_content":"abc"}]}`,
			wantBody:    `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`,
		},
		{
			// 实测形态（wsl rollout 普查 798 例）：agent_message 的 content[] 里
			// input_text 与 encrypted_content 并列，400 点名的正是这种条目的 id。
			name:        "drops ciphertext inside agent_message content and keeps input_text",
			wantRemoved: 1,
			wantChanged: true,
			body: `{"input":[{"type":"agent_message","id":"amsg_1","author":"/root","recipient":"/root/p",` +
				`"content":[{"type":"input_text","text":"Message Type: NEW_TASK"},` +
				`{"type":"encrypted_content","encrypted_content":"SeCrEt"}],` +
				`"internal_chat_message_metadata_passthrough":{"turn_id":"t1"}}]}`,
			wantBody: `{"input":[{"type":"agent_message","id":"amsg_1","author":"/root","recipient":"/root/p",` +
				`"content":[{"type":"input_text","text":"Message Type: NEW_TASK"}],` +
				`"internal_chat_message_metadata_passthrough":{"turn_id":"t1"}}]}`,
		},
		{
			name:        "drops an item whose content held nothing but ciphertext",
			wantRemoved: 1,
			wantChanged: true,
			body:        `{"input":[{"type":"agent_message","id":"amsg_2","content":[{"type":"encrypted_content","encrypted_content":"x"}]}]}`,
			wantBody:    `{"input":[]}`,
		},
		{
			// guardian_history / replacement_history 里混着可见 message 与密文项：
			// 只丢密文项，可见项与 summary 字段逐字节保留。
			name:        "prunes ciphertext entries from compacted histories",
			wantRemoved: 2,
			wantChanged: true,
			body: `{"input":[{"type":"compacted","summary":"kept",` +
				`"guardian_history":[` +
				`{"type":"message","id":"m1","role":"user","content":[{"type":"input_text","text":"hi"}]},` +
				`{"type":"reasoning","id":"rs_9","encrypted_content":"OmalbG91ZA=="}],` +
				`"replacement_history":[` +
				`{"type":"message","id":"m2","role":"assistant","content":[{"type":"output_text","text":"ok"}]},` +
				`{"type":"compaction","id":"cmp_1","encrypted_content":"zzz"}]}]}`,
			wantBody: `{"input":[{"type":"compacted","summary":"kept",` +
				`"guardian_history":[` +
				`{"type":"message","id":"m1","role":"user","content":[{"type":"input_text","text":"hi"}]}],` +
				`"replacement_history":[` +
				`{"type":"message","id":"m2","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}]}`,
		},
		{
			name:        "keeps an unknown item shape untouched when nothing is ciphertext",
			wantRemoved: 0,
			wantChanged: false,
			body:        `{"input":[{"type":"future_thing","blob":{"deep":[1,2,3]}}]}`,
		},
		{
			// encrypted_content 也可能是工具 schema 之类的对象值，不是密文。
			// 只删非空字符串，对象原样保留（否则会误删工具定义）。
			name:        "keeps a non-string encrypted_content value",
			wantRemoved: 0,
			wantChanged: false,
			body:        `{"input":[{"type":"tool","name":"x","encrypted_content":{"type":"object","properties":{"a":{"type":"string"}}}}]}`,
		},
		{
			// 判据只看成员名，可见文本里出现同名字样不能被当成密文。
			name:        "keeps the literal member name inside visible text",
			wantRemoved: 0,
			wantChanged: false,
			body:        `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"encrypted_content is a field name"}]}]}`,
		},
		{
			// compacted 的 guardian_history 里也会混进 agent_message（多智能体）。
			// 只删它 content[] 里的密文元素，input_text 与同级顺序保持原样。
			name:        "prunes ciphertext from an agent_message inside compacted history",
			wantRemoved: 1,
			wantChanged: true,
			body: `{"input":[{"type":"compacted","summary":"kept","guardian_history":[` +
				`{"type":"agent_message","id":"amsg_9","content":[` +
				`{"type":"input_text","text":"hi"},` +
				`{"type":"encrypted_content","encrypted_content":"Q"}]}]}]}`,
			wantBody: `{"input":[{"type":"compacted","summary":"kept","guardian_history":[` +
				`{"type":"agent_message","id":"amsg_9","content":[` +
				`{"type":"input_text","text":"hi"}]}]}]}`,
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

// Stripping is driven by the member name, not by item shape, so it also
// catches carriers no rollout census has seen yet. It must still leave
// readable content and unknown-but-harmless members alone.
func TestStripEncryptedReasoningInputUnknownShapes(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantRemoved int
		wantChanged bool
		wantBody    string
	}{
		{
			name:        "removes ciphertext under an unknown member",
			body:        `{"input":[{"type":"agent_message","id":"a","proprietary":{"encrypted_content":"x"},"content":[{"type":"input_text","text":"hi"}]}]}`,
			wantRemoved: 1,
			wantChanged: true,
			wantBody:    `{"input":[{"type":"agent_message","id":"a","content":[{"type":"input_text","text":"hi"}]}]}`,
		},
		{
			name:        "drops an item left with only bookkeeping",
			body:        `{"input":[{"type":"agent_message","id":"a","proprietary":{"encrypted_content":"x"}}]}`,
			wantRemoved: 1,
			wantChanged: true,
			wantBody:    `{"input":[]}`,
		},
		{
			name:        "is a no-op when nothing is ciphertext",
			body:        `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`,
			wantRemoved: 0,
			wantChanged: false,
		},
		{
			name:        "keeps malformed json untouched",
			body:        `{"input":[`,
			wantRemoved: 0,
			wantChanged: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, removed, changed := stripEncryptedReasoningInput([]byte(tt.body))
			if removed != tt.wantRemoved || changed != tt.wantChanged {
				t.Fatalf("removed=%d changed=%v, want %d/%v (body=%s)", removed, changed, tt.wantRemoved, tt.wantChanged, got)
			}
			if tt.wantChanged {
				if string(got) != tt.wantBody {
					t.Fatalf("body = %s, want %s", got, tt.wantBody)
				}
				if !json.Valid(got) {
					t.Fatalf("rewritten body is not valid json: %s", got)
				}
			} else if string(got) != tt.body {
				t.Fatalf("unchanged body rewritten: %s", got)
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

// Fork marks the thread before the first request, so a poisoned thread always
// carries the marker. The recovery used to bail out on it, which left the
// safety net gated twice by the same fact. It is still attempted exactly once:
// once the proactive strip has emptied the body, a 400 with the same marker
// finds nothing left to remove and must not be retried with identical bytes.
func TestHandleProviderMarkedThreadDoesNotRetryIdenticalBody(t *testing.T) {
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
	s.RegisterThread("thread-marked", "session-key")
	s.MarkProviderSwitch("thread-marked")

	body := `{"input":[{"type":"reasoning","id":"rs_1","encrypted_content":"abc"},` +
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`
	req := httptest.NewRequest(http.MethodPost, "/c/chatgpt/responses", strings.NewReader(body))
	req.Header.Set("session-id", "thread-marked")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleProvider(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 1 {
		t.Fatalf("upstream requests = %d, want 1 (proactive strip already emptied it, no identical retry)", requests)
	}
}

// Stripping is not limited to the carriers the old predicate knew about: a
// member named encrypted_content is removed wherever it hides, so the turn
// succeeds on the first request instead of relying on the 400 safety net.
func TestHandleProviderStripsNestedCiphertextBeforeForwarding(t *testing.T) {
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
			_, _ = w.Write([]byte(`{"error":{"code":"invalid_encrypted_content"}}`))
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
	s.RegisterThread("thread-unknown-shape", "session-key")
	s.MarkProviderSwitch("thread-unknown-shape")

	// 密文挂在不认识的成员下：旧判据看不到，按成员名递归剥离才能清掉。
	body := `{"input":[{"type":"agent_message","id":"amsg_1",` +
		`"content":[{"type":"input_text","text":"hi"}],` +
		`"proprietary_blob":{"encrypted_content":"SeCrEt"}}]}`
	req := httptest.NewRequest(http.MethodPost, "/c/chatgpt/responses", strings.NewReader(body))
	req.Header.Set("session-id", "thread-unknown-shape")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleProvider(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	mu.Lock()
	got := append([]string(nil), received...)
	mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("upstream requests = %d, want 1 (%v)", len(got), got)
	}
	if strings.Contains(got[0], "encrypted_content") {
		t.Fatalf("forward still carried ciphertext: %s", got[0])
	}
	if !strings.Contains(got[0], `"text":"hi"`) {
		t.Fatalf("forward dropped visible payload: %s", got[0])
	}
}

func TestHandleProviderDoesNotRetryWithoutCiphertext(t *testing.T) {	var mu sync.Mutex
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

// 范围化剥离：快照里记录的旧密文删掉，切换后新 provider 产生的密文保留。
func TestStripEncryptedInputScoped(t *testing.T) {
	body := `{"input":[` +
		`{"type":"reasoning","id":"rs_old","encrypted_content":"OLD"},` +
		`{"type":"agent_message","id":"amsg_new","content":[` +
		`{"type":"input_text","text":"hi"},` +
		`{"type":"encrypted_content","encrypted_content":"NEW"}]}` +
		`]}`
	allowed := map[string]struct{}{fingerprintCiphertext("OLD"): {}}
	got, removed, changed, found := stripEncryptedInput([]byte(body), allowed)
	if !changed || removed != 1 {
		t.Fatalf("changed=%v removed=%d, want true/1 (%s)", changed, removed, got)
	}
	if len(found) != 1 {
		t.Fatalf("found = %v, want only the stripped value", found)
	}
	if _, ok := found[fingerprintCiphertext("OLD")]; !ok {
		t.Fatalf("found = %v, want the OLD fingerprint", found)
	}
	want := `{"input":[{"type":"agent_message","id":"amsg_new","content":[` +
		`{"type":"input_text","text":"hi"},` +
		`{"type":"encrypted_content","encrypted_content":"NEW"}]}]}`
	if string(got) != want {
		t.Fatalf("body = %s, want %s", got, want)
	}
	if !json.Valid(got) {
		t.Fatalf("rewritten body is not valid json: %s", got)
	}

	// 无范围模式（首个切换请求 / 400 兜底）仍然全剥。
	if _, removedAll, changedAll, _ := stripEncryptedInput([]byte(body), nil); !changedAll || removedAll != 2 {
		t.Fatalf("unscoped removed=%d changed=%v, want 2/true", removedAll, changedAll)
	}
}

// marker 是永久的。若每轮都无范围剥离，切换后新 provider 自己的密文也会被删，
// 长会话的加密推理 / 多智能体 payload / compaction 摘要会静默丢失。首个切换
// 请求把当时在途的密文值记进快照，之后只删这些值。
func TestHandleProviderStripsOnlySnapshotCiphertext(t *testing.T) {
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
	s.RegisterThread("thread-scoped", "session-key")
	s.MarkProviderSwitch("thread-scoped")

	post := func(body string) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/c/chatgpt/responses", strings.NewReader(body))
		req.Header.Set("session-id", "thread-scoped")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.handleProvider(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
		}
		return <-forwarded
	}

	first := post(`{"input":[{"type":"reasoning","id":"rs_old","encrypted_content":"OLD"},` +
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`)
	if strings.Contains(first, "OLD") {
		t.Fatalf("first pass kept old ciphertext: %s", first)
	}
	ids, ok := s.providerSwitchCiphertextIDs("thread-scoped")
	if !ok || len(ids) != 1 {
		t.Fatalf("snapshot = %v ok=%v, want one fingerprint", ids, ok)
	}

	// 旧密文照删，新 provider 自己产出的密文必须原样转发。
	second := post(`{"input":[` +
		`{"type":"reasoning","id":"rs_old","encrypted_content":"OLD"},` +
		`{"type":"reasoning","id":"rs_new","encrypted_content":"NEW"},` +
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`)
	if strings.Contains(second, "OLD") {
		t.Fatalf("old ciphertext still forwarded: %s", second)
	}
	if !strings.Contains(second, `"encrypted_content":"NEW"`) {
		t.Fatalf("new provider ciphertext was stripped: %s", second)
	}
}

// 首个切换请求本来就没有密文时也要落一个空快照。否则之后每一轮都被当成
// 「首个请求」无范围剥离，把新 provider 的密文删光。
func TestHandleProviderSnapshotsEmptyFirstPass(t *testing.T) {
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
	s.RegisterThread("thread-empty", "session-key")
	s.MarkProviderSwitch("thread-empty")

	post := func(body string) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/c/chatgpt/responses", strings.NewReader(body))
		req.Header.Set("session-id", "thread-empty")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.handleProvider(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
		}
		return <-forwarded
	}

	post(`{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`)
	ids, ok := s.providerSwitchCiphertextIDs("thread-empty")
	if !ok || len(ids) != 0 {
		t.Fatalf("snapshot = %v ok=%v, want an initialised empty snapshot", ids, ok)
	}

	body := `{"input":[{"type":"reasoning","id":"rs_new","encrypted_content":"NEW"},` +
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`
	if got := post(body); got != body {
		t.Fatalf("new provider ciphertext was stripped after an empty snapshot: %s", got)
	}
}
