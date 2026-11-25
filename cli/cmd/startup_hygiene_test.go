package main

import (
	"encoding/json"
	"testing"
)

// session_hygiene 只在 config.json：新键落到 StartOptions 上，harness 段逐字段覆盖。
func TestSessionHygieneConfigFromFile(t *testing.T) {
	var cfg startupConfig
	raw := []byte(`{"session_hygiene":{"enabled":true,"active_hours":2.5,"context_percent":65,"over_line_turns":4,"idle_gap_minutes":45,"harness":{"codex":{"context_percent":85,"over_line_turns":2}}}}`)
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	opts := sessionHygieneOptions(cfg)
	if opts.ActiveSeconds != 9000 || opts.ContextPercent != 65 || opts.OverLineTurns != 4 || opts.IdleGapSeconds != 2700 {
		t.Fatalf("global section not applied: %+v", opts)
	}
	codex, ok := opts.Harnesses["codex"]
	if !ok {
		t.Fatalf("codex override missing: %+v", opts.Harnesses)
	}
	if codex.ContextPercent != 85 || codex.OverLineTurns != 2 {
		t.Fatalf("codex override = %+v", codex)
	}
}

// 旧键不能静默换含义：hint_turns 只在新键缺席时按别名生效；context_tokens 是
// 绝对阈值，改成百分比之后没有等价物，只能忽略（并留日志，见实现）。
func TestSessionHygieneDeprecatedKeys(t *testing.T) {
	var cfg startupConfig
	raw := []byte(`{"session_hygiene":{"hint_turns":5,"context_tokens":140000,"harness":{"codex":{"hint_turns":2,"context_tokens":180000}}}}`)
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	opts := sessionHygieneOptions(cfg)
	if opts.OverLineTurns != 5 {
		t.Fatalf("legacy hint_turns should become over_line_turns, got %d", opts.OverLineTurns)
	}
	if opts.ContextPercent != 0 {
		t.Fatalf("legacy context_tokens must not be reinterpreted as a percent: %d", opts.ContextPercent)
	}
	if codex := opts.Harnesses["codex"]; codex.OverLineTurns != 2 || codex.ContextPercent != 0 {
		t.Fatalf("harness legacy keys = %+v", codex)
	}

	// 新键在场时旧键必须让位。
	var both startupConfig
	if err := json.Unmarshal([]byte(`{"session_hygiene":{"over_line_turns":4,"hint_turns":5}}`), &both); err != nil {
		t.Fatal(err)
	}
	if got := sessionHygieneOptions(both).OverLineTurns; got != 4 {
		t.Fatalf("over_line_turns must win over hint_turns, got %d", got)
	}
}
