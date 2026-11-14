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

// --- strip logic tests ---

func TestStripCompactPayload_NoSkills(t *testing.T) {
	body := mustMarshal(map[string]any{
		"model": "opus",
		"system": []map[string]any{
			{"type": "text", "text": "You are helpful."},
		},
	})
	got, err := stripCompactPayload(body, DefaultSkillBudgetBytes, DefaultMaxFileRestoreBlocks)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Error("expected no change when no skill markers")
	}
}

func TestStripCompactPayload_SkillsWithinBudget(t *testing.T) {
	skillText := "### Skill: foo\nPath: /x\nsome content\n### Skill: bar\nPath: /y\nother"
	body := mustMarshal(map[string]any{
		"system": []map[string]any{
			{"type": "text", "text": skillText},
		},
	})
	got, err := stripCompactPayload(body, DefaultSkillBudgetBytes, DefaultMaxFileRestoreBlocks)
	if err != nil {
		t.Fatal(err)
	}
	// Content fits within budget — should be unchanged.
	if !bytes.Equal(got, body) {
		t.Error("expected no change when skills fit within budget")
	}
}

func TestStripCompactPayload_SkillsExceedBudget(t *testing.T) {
	// Build a skills block larger than 1 KB budget.
	var sb strings.Builder
	for i := range 20 {
		sb.WriteString(fmt.Sprintf("### Skill: skill%d\nPath: /skills/skill%d\n%s\n",
			i, i, strings.Repeat("x", 200)))
	}
	body := mustMarshal(map[string]any{
		"system": []map[string]any{
			{"type": "text", "text": sb.String()},
		},
	})
	got, err := stripCompactPayload(body, 1024, DefaultMaxFileRestoreBlocks)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(got, body) {
		t.Error("expected body to be modified when skills exceed budget")
	}
	var out map[string]any
	if err := json.Unmarshal(got, &out); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}
	sys := out["system"].([]any)
	text := sys[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "truncated") {
		t.Error("expected truncation marker in output")
	}
	if len(text) > 1024+200 { // budget + small overhead for truncation message
		t.Errorf("output too large: %d bytes", len(text))
	}
}

func TestStripCompactPayload_ExcessFileRestoreBlocks(t *testing.T) {
	fileBlock := func(path string) map[string]any {
		content := fmt.Sprintf("```go\n// %s\npackage main\n%s\n```",
			path, strings.Repeat("// code\n", 50))
		return map[string]any{"type": "text", "text": content}
	}
	body := mustMarshal(map[string]any{
		"system": []map[string]any{
			{"type": "text", "text": "instructions"},
			fileBlock("/a/b.go"),
			fileBlock("/c/d.go"),
			fileBlock("/e/f.go"), // should be dropped (exceeds max=2)
		},
	})
	got, err := stripCompactPayload(body, DefaultSkillBudgetBytes, 2)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(got, &out); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}
	sys := out["system"].([]any)
	if len(sys) != 3 { // instructions + 2 file blocks
		t.Errorf("expected 3 blocks, got %d", len(sys))
	}
}

// --- proxy server tests ---

func TestProxyServer_PassThrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	s, err := NewServer()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	token := "testtoken1"
	s.RegisterSession(token, upstream.URL)

	resp, err := http.Post(
		fmt.Sprintf("http://127.0.0.1:%d/p/%s/v1/messages", s.Port(), token),
		"application/json",
		strings.NewReader(`{"model":"opus","messages":[]}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestProxyServer_CompactSignalAndStrip(t *testing.T) {
	var capturedBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
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

	token := "testtoken2"
	s.RegisterSession(token, upstream.URL)

	// Send compact signal.
	resp, err := http.Post(
		fmt.Sprintf("http://127.0.0.1:%d/compact-signal", s.Port()),
		"application/json",
		strings.NewReader(fmt.Sprintf(`{"token":%q}`, token)),
	)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 204 {
		t.Fatalf("compact-signal expected 204, got %d", resp.StatusCode)
	}

	// Build a large skills payload.
	var sb strings.Builder
	for i := range 10 {
		sb.WriteString(fmt.Sprintf("### Skill: s%d\nPath: /x\n%s\n", i, strings.Repeat("y", 100)))
	}
	payload := mustMarshal(map[string]any{
		"model": "opus",
		"system": []map[string]any{
			{"type": "text", "text": sb.String()},
		},
	})

	resp2, err := http.Post(
		fmt.Sprintf("http://127.0.0.1:%d/p/%s/v1/messages", s.Port(), token),
		"application/json",
		bytes.NewReader(payload),
	)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()

	// The upstream should have received a stripped body.
	if bytes.Equal(capturedBody, payload) {
		t.Error("expected body to be stripped before forwarding")
	}
	var out map[string]any
	if err := json.Unmarshal(capturedBody, &out); err != nil {
		t.Fatalf("upstream received invalid JSON: %v", err)
	}
	sys := out["system"].([]any)
	text := sys[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "truncated") {
		t.Error("expected truncation marker in forwarded body")
	}

	// Second request should NOT be stripped (flag is one-shot).
	capturedBody = nil
	resp3, err := http.Post(
		fmt.Sprintf("http://127.0.0.1:%d/p/%s/v1/messages", s.Port(), token),
		"application/json",
		bytes.NewReader(payload),
	)
	if err != nil {
		t.Fatal(err)
	}
	resp3.Body.Close()
	if !bytes.Equal(capturedBody, payload) {
		t.Error("expected second request to pass through unchanged")
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
