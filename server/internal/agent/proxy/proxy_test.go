package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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
	got, err := stripCompactPayload(body, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Error("system array must never be rewritten")
	}
}

func TestStripCompactPayload_SkillsWithinBudgetUnchanged(t *testing.T) {
	body := mustMarshal(map[string]any{
		"messages": []any{userMessage(skillAttachment(2, 50))},
	})
	got, err := stripCompactPayload(body, DefaultSkillBudgetBytes, DefaultPlanBudgetBytes)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Error("skills under budget must pass through unchanged")
	}
}

func TestStripCompactPayload_SkillsExceedBudget(t *testing.T) {
	const budget = 1024
	body := mustMarshal(map[string]any{
		"messages": []any{userMessage("before compaction? no", skillAttachment(30, 200))},
	})
	got, err := stripCompactPayload(body, budget, DefaultPlanBudgetBytes)
	if err != nil {
		t.Fatal(err)
	}
	texts := messageTexts(t, got)
	if len(texts) != 2 {
		t.Fatalf("message count changed: %d", len(texts))
	}
	skills := texts[1]
	if !strings.HasPrefix(skills, skillPreambleMarker) {
		t.Error("preamble must be preserved")
	}
	if !strings.Contains(skills, "invoked_skills truncated") {
		t.Error("expected truncation marker")
	}
	if len(skills) > budget+1024 {
		t.Errorf("trimmed block too large: %d bytes", len(skills))
	}
	if n := strings.Count(skills, skillSectionMarker); n == 0 || n >= 30 {
		t.Errorf("expected a partial set of sections, got %d", n)
	}
}

// The budget is per request, shared across every skills block: two messages
// each under budget can still overflow together.
func TestStripCompactPayload_SkillBudgetSharedAcrossMessages(t *testing.T) {
	const budget = 1200
	body := mustMarshal(map[string]any{
		"messages": []any{
			userMessage(skillAttachment(5, 200)),
			userMessage(skillAttachment(5, 200)),
		},
	})
	got, err := stripCompactPayload(body, budget, DefaultPlanBudgetBytes)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, text := range messageTexts(t, got) {
		if strings.Contains(text, skillPreambleMarker) {
			total += len(text)
		}
	}
	if total > budget+2048 { // budgets + preambles + markers
		t.Errorf("combined skills still too large: %d bytes", total)
	}
}

func TestStripCompactPayload_PlanContentCapped(t *testing.T) {
	const budget = 512
	body := mustMarshal(map[string]any{
		"messages": []any{userMessage(planAttachment(strings.Repeat("plan line\n", 500)))},
	})
	got, err := stripCompactPayload(body, DefaultSkillBudgetBytes, budget)
	if err != nil {
		t.Fatal(err)
	}
	text := messageTexts(t, got)[0]
	if !strings.HasPrefix(text, planPreambleMarker) {
		t.Error("plan header must be preserved")
	}
	if !strings.Contains(text, "plan content truncated") {
		t.Error("expected plan truncation marker")
	}
	if !strings.Contains(text, planTailMarker) {
		t.Error("CLI trailing sentence must be preserved")
	}
	if len(text) > budget+1024 {
		t.Errorf("plan block too large: %d bytes", len(text))
	}
}

func TestStripCompactPayload_ShortPlanUnchanged(t *testing.T) {
	body := mustMarshal(map[string]any{
		"messages": []any{userMessage(planAttachment("a short plan"))},
	})
	got, err := stripCompactPayload(body, DefaultSkillBudgetBytes, DefaultPlanBudgetBytes)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Error("short plan must pass through unchanged")
	}
}

// Determinism matters for prompt caching: the same request must always be
// rewritten to the same bytes.
func TestStripCompactPayload_Idempotent(t *testing.T) {
	body := mustMarshal(map[string]any{
		"messages": []any{
			userMessage(skillAttachment(30, 300)),
			userMessage(planAttachment(strings.Repeat("p", 5000))),
		},
	})
	first, err := stripCompactPayload(body, 2000, 1000)
	if err != nil {
		t.Fatal(err)
	}
	second, err := stripCompactPayload(first, 2000, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Error("strip is not idempotent")
	}
	third, err := stripCompactPayload(second, 2000, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(second, third) {
		t.Error("strip is not stable on repeat")
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
	got, err := stripCompactPayload(body, 500, DefaultPlanBudgetBytes)
	if err != nil {
		t.Fatal(err)
	}
	out := decodeBody(t, got)
	msgs := out["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("message count changed: %d", len(msgs))
	}
	blocks := msgs[0].(map[string]any)["content"].([]any)
	if len(blocks) != 3 {
		t.Fatalf("block count changed: %d", len(blocks))
	}
	if blocks[0].(map[string]any)["type"] != "tool_result" ||
		blocks[2].(map[string]any)["type"] != "image" {
		t.Error("non-text blocks must be preserved")
	}
	if msgs[2].(map[string]any)["content"] != "plain string content" {
		t.Error("string content must be preserved")
	}
	if blocks[1].(map[string]any)["text"].(string) == skillAttachment(30, 200) {
		t.Error("skills block should have been trimmed")
	}
}

func TestStripCompactPayload_NoMarkersPassThrough(t *testing.T) {
	body := mustMarshal(map[string]any{
		"system":   []any{map[string]any{"type": "text", "text": "You are Claude Code."}},
		"messages": []any{userMessage("a normal turn"), userMessage("and another")},
		"model":    "opus",
	})
	got, err := stripCompactPayload(body, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Error("bodies without post-compact markers must pass through unchanged")
	}
}

func TestStripCompactPayload_MalformedBodyPassThrough(t *testing.T) {
	body := []byte(`{"messages": [ this is not json ` + skillPreambleMarker + `}`)
	got, err := stripCompactPayload(body, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Error("malformed bodies must be returned unchanged")
	}
}

func TestCutRunesDoesNotSplitUTF8(t *testing.T) {
	s := strings.Repeat("中", 10) // 3 bytes each
	for n := 0; n <= len(s); n++ {
		if got := cutRunes(s, n); len(got) > n {
			t.Fatalf("cutRunes(%d) returned %d bytes", n, len(got))
		} else if !strings.HasPrefix(s, got) {
			t.Fatalf("cutRunes(%d) is not a prefix", n)
		}
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
	s.SkillBudgetBytes = 100

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
		"messages": []any{userMessage(skillAttachment(10, 100))},
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
	if bytes.Equal(bodies[1], payload) {
		t.Error("second request must also be stripped")
	}
	if !bytes.Contains(bodies[1], []byte("invoked_skills truncated")) {
		t.Error("second request missing truncation marker")
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
