package main

import (
	"encoding/json"
	"testing"
)

// The per-harness section is config.json only, and it must survive parsing with
// its pointer semantics intact: an absent field keeps the node value, an
// explicit false must stay false.
func TestLiveCompactHarnessOverridesParsed(t *testing.T) {
	var cfg startupConfig
	raw := []byte(`{"live_compact":{"mode":"on","rail_tokens":100000,"harness":{
		"codex":{"enabled":false,"rail_tokens":200000,"cold_fold":false}}}}`)
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	got := liveCompactHarnessOverrides(&cfg)
	codex, ok := got["codex"]
	if !ok {
		t.Fatalf("codex override missing: %+v", got)
	}
	if codex.Enabled == nil || *codex.Enabled {
		t.Fatalf("enabled = %v, want explicit false", codex.Enabled)
	}
	if codex.RailTokens != 200000 {
		t.Fatalf("rail = %d", codex.RailTokens)
	}
	if codex.ColdFold == nil || *codex.ColdFold {
		t.Fatalf("cold_fold = %v, want explicit false", codex.ColdFold)
	}
	// Fields the override does not mention stay zero so ForHarness falls back.
	if codex.TargetTokens != 0 || codex.MinFoldBytes != 0 {
		t.Fatalf("unspecified fields must stay zero: %+v", codex)
	}
}

func TestLiveCompactHarnessOverridesAbsent(t *testing.T) {
	if got := liveCompactHarnessOverrides(&startupConfig{}); got != nil {
		t.Fatalf("no live_compact section must yield nil, got %+v", got)
	}
	var cfg startupConfig
	if err := json.Unmarshal([]byte(`{"live_compact":{"mode":"on"}}`), &cfg); err != nil {
		t.Fatal(err)
	}
	if got := liveCompactHarnessOverrides(&cfg); got != nil {
		t.Fatalf("no harness section must yield nil, got %+v", got)
	}
}
