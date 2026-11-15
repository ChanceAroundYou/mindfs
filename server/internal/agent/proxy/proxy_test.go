package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- helpers ---------------------------------------------------------------

// skillAttachment renders the exact text block the CLI emits after a
// compaction (2.1.283, `eot()` case "invoked_skills"), followed by n skill
// sections of `sectionBytes` bytes each.
func skillAttachment(n, sectionBytes int) string {
	var sb strings.Builder
	sb.WriteString(skillPreambleMarker)
	sb.WriteString(` (before the conversation was compacted), not on the current turn. `)
	sb.WriteString("They are shown here for context only.\n")
	for i := range n {
		fmt.Fprintf(&sb, "%s skill%d\nPath: /skills/skill%d\n%s\n\n",
			skillSectionMarker, i, i, strings.Repeat("x", sectionBytes))
	}
	return sb.String()
}

// planAttachment renders the `plan_file_reference` text block.
func planAttachment(body string) string {
	return planPreambleMarker + " /work/PLAN.md\n" + planContentsMarker + body + "\n" +
		planTailMarker + " and not already complete, continue working on it."
}

// userMessage mirrors the wire shape: role user, content array of text blocks.
func userMessage(texts ...string) map[string]any {
	blocks := make([]map[string]any, 0, len(texts))
	for _, t := range texts {
		blocks = append(blocks, map[string]any{"type": "text", "text": t})
	}
	return map[string]any{"role": "user", "content": blocks}
}

func decodeBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	return out
}

func messageTexts(t *testing.T, body []byte) []string {
	t.Helper()
	out := decodeBody(t, body)
	msgs, _ := out["messages"].([]any)
	var texts []string
	for _, m := range msgs {
		msg := m.(map[string]any)
		blocks, ok := msg["content"].([]any)
		if !ok {
			continue
		}
		for _, b := range blocks {
			block := b.(map[string]any)
			if block["type"] == "text" {
				texts = append(texts, block["text"].(string))
			}
		}
	}
	return texts
}

// --- strip logic -----------------------------------------------------------

// The post-compact payload is a user message, not a system block. A system
// array that happens to contain skill-ish text must be left alone: the old
// implementation only looked there and stripped the wrong thing.
func TestStripCompactPayload_LeavesSystemArrayAlone(t *testing.T) {
	bigSystem := "```go\n" + strings.Repeat("// filler\n", 100) + "```"
	systemText := skillAttachment(20, 500) // marker present, but in the wrong field
	body := mustMarshal(map[string]any{
		"system": []map[string]any{
			{"type": "text", "text": bigSystem},
			{"type": "text", "text": systemText},
		},
		"messages": []any{userMessage("hello")},
	})
	got := stripCompactPayload(body)
	if !bytes.Equal(got, body) {
		t.Error("system array must never be rewritten")
	}
}

// 2026-11-15 决策：压缩后的 invoked_skills 附件整块删除（不是裁剪）。
// 它们只是「仅供参考」的历史指针，正文对后续回合没有增量价值；每轮重发才是
// 上下文反复被吃满的根因。
func TestStripCompactPayload_RemovesSkillAttachmentEntirely(t *testing.T) {
	body := mustMarshal(map[string]any{
		"messages": []any{userMessage("normal turn"), userMessage(skillAttachment(3, 100))},
	})
	got := stripCompactPayload(body)
	if bytes.Contains(got, []byte(skillPreambleMarker)) ||
		bytes.Contains(got, []byte(skillSectionMarker)) {
		t.Fatal("skill attachment must be gone, not truncated")
	}
	msgs := decodeBody(t, got)["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("empty attachment message must be dropped, got %d messages", len(msgs))
	}
	texts := messageTexts(t, got)
	if len(texts) != 1 || texts[0] != "normal turn" {
		t.Fatalf("unrelated text changed: %#v", texts)
	}
}

// 附件块可能和正常文本/图片在同一条消息里：只删那一块，消息本身要保住。
func TestStripCompactPayload_RemovesOnlySkillBlockFromMixedMessage(t *testing.T) {
	body := mustMarshal(map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": []map[string]any{
				{"type": "text", "text": "keep me"},
				{"type": "text", "text": skillAttachment(30, 200)},
				{"type": "image", "source": map[string]any{"type": "base64", "data": "AAA"}},
			}},
		},
	})
	got := stripCompactPayload(body)
	msgs := decodeBody(t, got)["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("message must survive: %d", len(msgs))
	}
	blocks := msgs[0].(map[string]any)["content"].([]any)
	if len(blocks) != 2 {
		t.Fatalf("skill block must be dropped, got %d blocks", len(blocks))
	}
	if blocks[0].(map[string]any)["text"] != "keep me" ||
		blocks[1].(map[string]any)["type"] != "image" {
		t.Error("non-skill blocks must be preserved in order")
	}
}

// plan 附件只留路径行：正文删掉，模型需要时自己 Read。
func TestStripCompactPayload_PlanKeepsPathOnly(t *testing.T) {
	body := mustMarshal(map[string]any{
		"messages": []any{userMessage(planAttachment(strings.Repeat("plan line\n", 500)))},
	})
	got := stripCompactPayload(body)
	text := messageTexts(t, got)[0]
	want := planPreambleMarker + " /work/PLAN.md"
	if text != want {
		t.Fatalf("plan text = %q, want %q", text, want)
	}
	if strings.Contains(text, planContentsMarker) || strings.Contains(text, planTailMarker) {
		t.Error("plan body and tail must be gone")
	}
}

// 短 plan 也照样收成路径行 —— 以前的「预算内原样透传」会让同一条附件
// 每轮重发，即使它只有几 KB。
func TestStripCompactPayload_ShortPlanAlsoReduced(t *testing.T) {
	body := mustMarshal(map[string]any{
		"messages": []any{userMessage(planAttachment("a short plan"))},
	})
	got := stripCompactPayload(body)
	if text := messageTexts(t, got)[0]; text != planPreambleMarker+" /work/PLAN.md" {
		t.Fatalf("short plan not reduced: %q", text)
	}
}

// Determinism matters for prompt caching: the same request must always be
// rewritten to the same bytes, and a second pass must be a no-op.
func TestStripCompactPayload_Idempotent(t *testing.T) {
	body := mustMarshal(map[string]any{
		"messages": []any{
			userMessage(skillAttachment(30, 300)),
			userMessage(planAttachment(strings.Repeat("p", 5000))),
		},
	})
	first := stripCompactPayload(body)
	second := stripCompactPayload(first)
	if !bytes.Equal(first, second) {
		t.Error("strip is not idempotent")
	}
	if bytes.Contains(second, []byte(skillPreambleMarker)) {
		t.Error("skill attachment came back on the second pass")
	}
}

// Non-text blocks (tool results, images) and unrelated messages must survive
// byte-for-byte, including ordering.
func TestStripCompactPayload_PreservesNonTextBlocks(t *testing.T) {
	body := mustMarshal(map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": []map[string]any{
				{"type": "tool_result", "tool_use_id": "t1", "content": "output"},
				{"type": "text", "text": skillAttachment(30, 200)},
				{"type": "image", "source": map[string]any{"type": "base64", "data": "AAA"}},
			}},
			map[string]any{"role": "assistant", "content": []map[string]any{
				{"type": "text", "text": "assistant reply"},
			}},
			map[string]any{"role": "user", "content": "plain string content"},
		},
		"tools": []any{map[string]any{"name": "Read"}},
	})
	got := stripCompactPayload(body)
	out := decodeBody(t, got)
	msgs := out["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("message count changed: %d", len(msgs))
	}
	blocks := msgs[0].(map[string]any)["content"].([]any)
	if len(blocks) != 2 {
		t.Fatalf("block count changed: %d", len(blocks))
	}
	if blocks[0].(map[string]any)["type"] != "tool_result" ||
		blocks[1].(map[string]any)["type"] != "image" {
		t.Error("non-text blocks must be preserved")
	}
	if msgs[2].(map[string]any)["content"] != "plain string content" {
		t.Error("string content must be preserved")
	}
}

func TestStripCompactPayload_NoMarkersPassThrough(t *testing.T) {
	body := mustMarshal(map[string]any{
		"system":   []any{map[string]any{"type": "text", "text": "You are Claude Code."}},
		"messages": []any{userMessage("a normal turn"), userMessage("and another")},
		"model":    "opus",
	})
	got := stripCompactPayload(body)
	if !bytes.Equal(got, body) {
		t.Error("bodies without post-compact markers must pass through unchanged")
	}
}

func TestStripCompactPayload_MalformedBodyPassThrough(t *testing.T) {
	body := []byte(`{"messages": [ this is not json ` + skillPreambleMarker + `}`)
	got := stripCompactPayload(body)
	if !bytes.Equal(got, body) {
		t.Error("malformed bodies must be returned unchanged")
	}
}

// --- proxy server ----------------------------------------------------------

func TestProxyServer_PassThrough(t *testing.T) {
	var captured []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	s, err := NewServer()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	token := "passthrough1"
	s.RegisterSession(token, upstream.URL)

	payload := mustMarshal(map[string]any{
		"model":    "opus",
		"messages": []any{userMessage("hello")},
	})
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
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if !bytes.Equal(captured, payload) {
		t.Error("marker-free request must reach upstream unchanged")
	}
}

// Stripping is marker-driven and applies on every request: the attachment
// message stays in history after compaction, so a one-shot guarantee would let
// every later turn re-send the full bloat.
func TestProxyServer_StripsOnEveryRequest(t *testing.T) {
	var bodies [][]byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, b)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"msg1"}`))
	}))
	defer upstream.Close()

	s, err := NewServer()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	token := "strip3"
	s.RegisterSession(token, upstream.URL)

	// The hook signal is accepted but no longer gates stripping.
	resp, err := http.Post(
		fmt.Sprintf("http://127.0.0.1:%d/compact-signal", s.Port()),
		"application/json",
		strings.NewReader(fmt.Sprintf(`{"token":%q}`, token)),
	)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("compact-signal expected 204, got %d", resp.StatusCode)
	}

	payload := mustMarshal(map[string]any{
		"model":    "opus",
		"messages": []any{userMessage("hello"), userMessage(skillAttachment(10, 100))},
	})
	for i := range 2 {
		r2, err := http.Post(
			fmt.Sprintf("http://127.0.0.1:%d/p/%s/v1/messages", s.Port(), token),
			"application/json",
			bytes.NewReader(payload),
		)
		if err != nil {
			t.Fatal(err)
		}
		r2.Body.Close()
		if i == 0 && bytes.Equal(bodies[0], payload) {
			t.Fatal("first request was not stripped")
		}
	}
	if len(bodies) != 2 {
		t.Fatalf("expected 2 upstream requests, got %d", len(bodies))
	}
	for i, b := range bodies {
		if bytes.Contains(b, []byte(skillPreambleMarker)) {
			t.Errorf("request %d still carries the skill attachment", i)
		}
		if !bytes.Contains(b, []byte("hello")) {
			t.Errorf("request %d lost its ordinary content", i)
		}
	}
}

// A signal for an unknown token must not break the hook: the CLI runs with the
// hook installed globally, and a stale session would otherwise surface errors.
func TestProxyServer_SignalForUnknownTokenIsAccepted(t *testing.T) {
	s, err := NewServer()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	resp, err := http.Post(
		fmt.Sprintf("http://127.0.0.1:%d/compact-signal", s.Port()),
		"application/json",
		strings.NewReader(`{"token":"never-registered"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
}

func TestProxyServer_UnknownToken(t *testing.T) {
	s, err := NewServer()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	resp, err := http.Post(
		fmt.Sprintf("http://127.0.0.1:%d/p/unknowntoken/v1/messages", s.Port()),
		"application/json",
		strings.NewReader(`{}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("expected 502 for unknown token, got %d", resp.StatusCode)
	}
}

func mustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// 首个请求的日志只用于证明「CLI 真的走了代理」。路径里带着完整能力 token，
// 直接打 r.URL.Path 等于把 token 写进日志；必须只记短 token 和去 token 的路径。
func TestProxyFirstRequestLogDoesNotLeakFullToken(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer upstream.Close()

	s, err := NewServer()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	token := "capability-token-that-must-not-be-logged"
	s.RegisterSession(token, upstream.URL)

	var buf bytes.Buffer
	oldWriter, oldFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(oldWriter)
		log.SetFlags(oldFlags)
	}()

	resp, err := http.Post(
		fmt.Sprintf("http://127.0.0.1:%d/p/%s/v1/messages", s.Port(), token),
		"application/json",
		strings.NewReader(`{"messages":[]}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	logged := buf.String()
	if strings.Contains(logged, token) {
		t.Fatalf("proxy log leaked the full capability token: %q", logged)
	}
	if !strings.Contains(logged, "first request") {
		t.Fatalf("first request was not logged: %q", logged)
	}
	if !strings.Contains(logged, "/v1/messages") {
		t.Fatalf("log should keep the token-free request path: %q", logged)
	}
}
