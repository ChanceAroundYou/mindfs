package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 官方 Responses API 要求 message 条目的 id 以 msg_ 开头，其余前缀整轮 400：
// `Invalid 'input[N].id': 'item_…'. Expected an ID that begins with 'msg'`。
// Codex 自己造的本地消息（pre-sampling compact 的提示词）用 item_<hex>，llmux
// 不校验，迁到官方 provider 后才炸。这里把这类 id 去掉 —— 普通用户消息本来
// 就不带 id，条目本体与顺序保持原样。
func TestNormalizeInputMessageIDs(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantRemoved int
		wantBody    string
	}{
		{
			name:        "drops a non-msg id from a message item",
			body:        `{"input":[{"type":"message","id":"item_84ffddf4811bac4ed5111e26","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`,
			wantRemoved: 1,
			wantBody:    `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`,
		},
		{
			name:        "keeps a msg_ id",
			body:        `{"input":[{"type":"message","id":"msg_abc","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`,
			wantRemoved: 0,
		},
		{
			name:        "drops the id of a message without an explicit type",
			body:        `{"input":[{"id":"item_abc","role":"user","content":"hi"}]}`,
			wantRemoved: 1,
			wantBody:    `{"input":[{"role":"user","content":"hi"}]}`,
		},
		{
			name:        "keeps message without an id",
			body:        `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`,
			wantRemoved: 0,
		},
		{
			// 只改 message。reasoning / function_call 的 id 前缀规则不同，
			// 这里不动，免得误伤配对关系。
			name:        "keeps a reasoning id",
			body:        `{"input":[{"type":"reasoning","id":"item_abc","summary":[]}]}`,
			wantRemoved: 0,
		},
		{
			name:        "keeps ids nested inside a compacted blob",
			body:        `{"input":[{"type":"compacted","encrypted_content":"zz","guardian_history":[{"type":"message","id":"item_nested","role":"user","content":[{"type":"input_text","text":"hi"}]}]}]}`,
			wantRemoved: 0,
		},
		{
			name:        "keeps a string input untouched",
			body:        `{"input":"plain string"}`,
			wantRemoved: 0,
		},
		{
			name:        "keeps malformed json untouched",
			body:        `{"input":[`,
			wantRemoved: 0,
		},
		{
			name: "only the offending items change, sibling order preserved",
			body: `{"model":"gpt-6","input":[` +
				`{"type":"message","id":"item_a","role":"user","content":[{"type":"input_text","text":"one"}]},` +
				`{"type":"message","id":"msg_ok","role":"assistant","content":[{"type":"output_text","text":"two"}]},` +
				`{"type":"message","id":"item_b","role":"user","content":[{"type":"input_text","text":"three"}]}` +
				`]}`,
			wantRemoved: 2,
			wantBody: `{"model":"gpt-6","input":[` +
				`{"type":"message","role":"user","content":[{"type":"input_text","text":"one"}]},` +
				`{"type":"message","id":"msg_ok","role":"assistant","content":[{"type":"output_text","text":"two"}]},` +
				`{"type":"message","role":"user","content":[{"type":"input_text","text":"three"}]}` +
				`]}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, removed, changed := normalizeInputMessageIDs([]byte(tt.body))
			if removed != tt.wantRemoved || changed != (tt.wantRemoved > 0) {
				t.Fatalf("removed=%d changed=%v, want %d/%v (body=%s)", removed, changed, tt.wantRemoved, tt.wantRemoved > 0, got)
			}
			if tt.wantBody != "" {
				if string(got) != tt.wantBody {
					t.Fatalf("body = %s, want %s", got, tt.wantBody)
				}
				if !json.Valid(got) {
					t.Fatalf("rewritten body is not valid json: %s", got)
				}
				return
			}
			if string(got) != tt.body {
				t.Fatalf("unchanged body rewritten: %s", got)
			}
		})
	}
}

// 端到端：注册线程的请求在转发前就被归一，上游永远看不到 item_ 前缀的消息 id。
func TestHandleProviderNormalizesMessageIDsBeforeForwarding(t *testing.T) {
	forwarded := make(chan string, 2)
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
	s.RegisterThread("thread-ids", "session-key")

	body := `{"input":[` +
		`{"type":"message","id":"item_84ffddf4811bac4ed5111e26","role":"user","content":[{"type":"input_text","text":"hi"}]},` +
		`{"type":"message","id":"msg_keep","role":"assistant","content":[{"type":"output_text","text":"ok"}]}` +
		`]}`
	req := httptest.NewRequest(http.MethodPost, "/c/chatgpt/responses", strings.NewReader(body))
	req.Header.Set("session-id", "thread-ids")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleProvider(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	got := <-forwarded
	if strings.Contains(got, "item_84ffddf4811bac4ed5111e26") {
		t.Fatalf("non-msg id reached upstream: %s", got)
	}
	if !strings.Contains(got, `"id":"msg_keep"`) {
		t.Fatalf("valid msg_ id was stripped: %s", got)
	}
	if !strings.Contains(got, `"text":"hi"`) || !strings.Contains(got, `"text":"ok"`) {
		t.Fatalf("visible content was dropped: %s", got)
	}
}
