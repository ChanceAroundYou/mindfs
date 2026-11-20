package proxy

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"
)

// Preserved-thinking models fingerprint the request prefix. A proxy-side
// rewrite is a client-side edit, so folding one of these requests invalidates
// every thinking block after the fold boundary (design 2026-11-20 §4).

func TestModelFamilyNormalization(t *testing.T) {
	cases := map[string]string{
		"claude-sonnet-5-5":           "sonnet-5-5",
		"claude-sonnet-5-5-20260101":  "sonnet-5-5",
		"claude-opus-5-5[1m]":         "opus-5-5",
		"Claude-Opus-5-5-20260101":    "opus-5-5",
		"opus-5":                      "opus-5",
		"ok":                          "ok",
		"claude-haiku-4-5-20251001":   "haiku-4-5",
		"claude-fable-5-2":            "fable-5-2",
		"claude-sonnet-4-5-20250929 ": "sonnet-4-5",
	}
	for model, want := range cases {
		if got := modelFamily(model); got != want {
			t.Errorf("modelFamily(%q) = %q, want %q", model, got, want)
		}
	}
}

func TestPrefixGuardBlocksPreservedThinkingFamilies(t *testing.T) {
	cfg := LiveCompactConfig{}.Normalized()
	blocked := []string{
		"claude-fable-5-2", "claude-mythos-5-2", "claude-fable-5-1", "claude-mythos-5-1",
		"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5",
		"claude-sonnet-5-5-20260101", "sonnet-5-5[1m]",
	}
	for _, model := range blocked {
		s := &Server{sessions: map[string]*sessionState{"tok": {foldAllowed: true}}}
		if !s.prefixGuardBlocks("tok", model, cfg) {
			t.Errorf("model %q must be refused", model)
		}
	}
}

// The families are versioned: 5.0 and 4.x do not run the check, and matching
// must not slide from "opus-5-5" to "opus-5".
func TestPrefixGuardAllowsOlderFamilies(t *testing.T) {
	cfg := LiveCompactConfig{}.Normalized()
	allowed := []string{"claude-opus-5", "claude-opus-4-5", "claude-sonnet-5", "claude-haiku-4-5-20251001"}
	for _, model := range allowed {
		s := &Server{sessions: map[string]*sessionState{"tok": {foldAllowed: true}}}
		if s.prefixGuardBlocks("tok", model, cfg) {
			t.Errorf("model %q must be allowed", model)
		}
	}
}

// An alias this process cannot classify is allowed, with a warning that fires
// once per session. Blocking it would silently turn folding off for the node
// that is running it today (llmux's "ok").
func TestPrefixGuardAllowsUnknownAliasOnce(t *testing.T) {
	cfg := LiveCompactConfig{}.Normalized()
	s := &Server{sessions: map[string]*sessionState{"tok": {foldAllowed: true}}}
	for i := 0; i < 3; i++ {
		if s.prefixGuardBlocks("tok", "ok", cfg) {
			t.Fatalf("unknown alias must not be blocked")
		}
	}
	if !s.sessions["tok"].live.prefixWarned {
		t.Fatal("unknown alias must be flagged for the once-per-session warning")
	}
}

// The alias becomes an operator decision: list it and folding stops.
func TestPrefixGuardConfigListBlocksAlias(t *testing.T) {
	cfg := LiveCompactConfig{PrefixUnsafeModels: []string{"ok"}}.Normalized()
	s := &Server{sessions: map[string]*sessionState{"tok": {foldAllowed: true}}}
	if !s.prefixGuardBlocks("tok", "ok", cfg) {
		t.Fatal("configured unsafe alias must be refused")
	}
}

// The guard is a kill switch, not a permanent policy: an operator who knows the
// model is safe must be able to turn it off.
func TestPrefixGuardDisabledAllowsPreservedModel(t *testing.T) {
	off := false
	cfg := LiveCompactConfig{PrefixGuard: &off}.Normalized()
	s := &Server{sessions: map[string]*sessionState{"tok": {foldAllowed: true}}}
	if s.prefixGuardBlocks("tok", "claude-opus-5-5", cfg) {
		t.Fatal("disabled guard must not block")
	}
}

func TestScanRequestCapturesModel(t *testing.T) {
	body, err := json.Marshal(map[string]any{
		"messages": []any{userText("hi")},
		"model":    "claude-opus-5-5",
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := scanWireRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if req.model != "claude-opus-5-5" {
		t.Fatalf("model = %q, want claude-opus-5-5", req.model)
	}
}

// A model string that merely appears inside a message must not be mistaken for
// the top-level member.
func TestScanRequestIgnoresModelInsideMessages(t *testing.T) {
	body, err := json.Marshal(map[string]any{
		"messages": []any{userText(`{"model":"claude-opus-5-5"}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := scanWireRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if req.model != "" {
		t.Fatalf("model = %q, want empty", req.model)
	}
}

// End to end through the handler: a preserved-thinking request is forwarded
// byte-identical even in mode=on with folding otherwise allowed.
func TestProxyFoldSkipsPreservedThinkingModel(t *testing.T) {
	archiveDir := filepath.Join(t.TempDir(), "context")
	s, err := NewServerWithLiveCompact(LiveCompactConfig{
		Mode: LiveCompactOn, KeepRounds: 1, MinBlockTokens: 100, RailTokens: -1,
		MinBodyBytes: 1, ArchiveDir: archiveDir, ArchiveEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	body, err := json.Marshal(map[string]any{
		"model":    "claude-opus-5-5-20260101",
		"messages": foldTestMessages(4, 8001),
	})
	if err != nil {
		t.Fatal(err)
	}
	captured, status := postMessages(t, s, "pg1", body)
	if status != 200 {
		t.Fatalf("status = %d", status)
	}
	if !bytes.Equal(captured, body) {
		t.Fatalf("guarded request was rewritten (%d -> %d bytes)", len(body), len(captured))
	}
	if bytes.Contains(captured, []byte("[MindFS live-compact]")) {
		t.Fatal("guarded request must not carry a fold pointer")
	}
}

// Prefix matching must stop at a dash: "opus-5-50" is not "opus-5-5", and a
// derivative such as "opus-5-5-1" still is.
func TestFamilyMatchesRequiresBoundary(t *testing.T) {
	if familyMatches("opus-5-50", "opus-5-5") {
		t.Fatal("opus-5-50 must not match opus-5-5")
	}
	if familyMatches("opus-5", "opus-5-5") {
		t.Fatal("opus-5 must not match opus-5-5")
	}
	if !familyMatches("opus-5-5-1", "opus-5-5") {
		t.Fatal("opus-5-5-1 must match opus-5-5")
	}
}
