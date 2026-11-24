package app

import "testing"

// 默认值就是用户这次定的策略，任何一处改动都必须在这里显形：1 小时活跃、
// 上下文窗口的 70%、3 个连续过线回合、30 分钟空闲截断。
func TestSessionHygieneDefaults(t *testing.T) {
	cfg := sessionHygieneConfigFromOptions(SessionHygieneOptions{})
	if !cfg.Enabled {
		t.Fatal("hygiene must default to enabled")
	}
	if cfg.ActiveSeconds != 3600 {
		t.Fatalf("active seconds = %d, want 3600", cfg.ActiveSeconds)
	}
	if cfg.ContextPercent != 70 {
		t.Fatalf("context percent = %d, want 70", cfg.ContextPercent)
	}
	if cfg.OverLineTurns != 3 {
		t.Fatalf("over-line turns = %d, want 3", cfg.OverLineTurns)
	}
	if cfg.IdleGapSeconds != 1800 {
		t.Fatalf("idle gap = %d, want 1800", cfg.IdleGapSeconds)
	}
	// 阈值是相对的，所以两个 harness 共享同一套默认值；再给 Codex 单独的内置
	// 绝对阈值只会让同一策略在 200k 与 272k 窗口上含义不同。
	if len(cfg.Harness) != 0 {
		t.Fatalf("defaults must not ship a built-in harness override: %+v", cfg.Harness)
	}
}

func TestSessionHygieneExplicitOptionsWin(t *testing.T) {
	enabled := false
	cfg := sessionHygieneConfigFromOptions(SessionHygieneOptions{
		Enabled:        &enabled,
		ActiveSeconds:  7200,
		ContextPercent: 65,
		OverLineTurns:  4,
		IdleGapSeconds: 600,
	})
	if cfg.Enabled {
		t.Fatal("explicit enabled=false must win")
	}
	if cfg.ActiveSeconds != 7200 || cfg.ContextPercent != 65 || cfg.OverLineTurns != 4 || cfg.IdleGapSeconds != 600 {
		t.Fatalf("explicit options not applied: %+v", cfg)
	}
}

// harness 覆盖是逐字段合并：只写 over_line_turns 的人不该顺手把该 harness 的
// 其他阈值改成零（零等于回落全局，看起来一样，但一旦全局默认变化就分叉了）。
func TestSessionHygieneHarnessOverrideMergesPerField(t *testing.T) {
	cfg := sessionHygieneConfigFromOptions(SessionHygieneOptions{
		Harnesses: map[string]SessionHygieneHarnessOverride{
			" Codex ": {ContextPercent: 85, OverLineTurns: 2},
			"":        {ContextPercent: 99},
		},
	})
	codex, ok := cfg.Harness["codex"]
	if !ok {
		t.Fatalf("harness key not normalized: %+v", cfg.Harness)
	}
	if codex.ContextPercent != 85 || codex.OverLineTurns != 2 {
		t.Fatalf("codex override = %+v", codex)
	}
	if codex.ActiveSeconds != 0 || codex.IdleGapSeconds != 0 {
		t.Fatalf("unset fields must stay zero so ConfigFor falls back: %+v", codex)
	}
	if _, ok := cfg.Harness[""]; ok {
		t.Fatal("blank harness keys must be skipped")
	}
}