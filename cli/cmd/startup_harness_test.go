package main

import (
	"os"
	"strings"
	"testing"
)

func intPtr(v int) *int    { return &v }
func boolPtr(v bool) *bool { return &v }

// `make build` 安装的是 cli/cmd，而 harness 覆盖只有经过这条转换才会进入
// proxy。丢掉它不会有任何报错：Codex 只是永远按节点 rail 折叠（实测症状是
// gate 恒为 below-rail、胶囊 requests 永远 0）。
func TestLiveCompactHarnessOverridesReachTheServer(t *testing.T) {
	cfg := startupConfig{LiveCompact: &startupLiveCompactConfig{
		Mode: stringPtr("on"),
		Harness: map[string]startupLiveCompactHarnessConfig{
			"Codex": {
				RailTokens:     intPtr(20000),
				TargetTokens:   intPtr(8000),
				MinBodyBytes:   intPtr(65536),
				KeepRounds:     intPtr(1),
				KeepTailTokens: intPtr(4000),
				ColdFold:       boolPtr(false),
			},
		},
	}}
	got := liveCompactHarnessOverrides(cfg)
	codex, ok := got["codex"]
	if !ok {
		t.Fatalf("harness key not normalized: %+v", got)
	}
	if codex.RailTokens != 20000 || codex.TargetTokens != 8000 || codex.MinBodyBytes != 65536 {
		t.Fatalf("rail/target/body = %+v", codex)
	}
	if codex.KeepRounds != 1 || codex.KeepTailTokens != 4000 {
		t.Fatalf("keep = %+v", codex)
	}
	// A pointer field: explicit false must survive, absent must stay nil.
	if codex.ColdFold == nil || *codex.ColdFold {
		t.Fatalf("cold_fold = %v", codex.ColdFold)
	}
}

func TestLiveCompactHarnessOverridesAbsent(t *testing.T) {
	if got := liveCompactHarnessOverrides(startupConfig{}); got != nil {
		t.Fatalf("no live_compact section must yield nil, got %+v", got)
	}
	if got := liveCompactHarnessOverrides(startupConfig{LiveCompact: &startupLiveCompactConfig{}}); got != nil {
		t.Fatalf("no harness section must yield nil, got %+v", got)
	}
}

func stringPtr(v string) *string { return &v }

// 上面测的是转换本身；这条钉住它真的被接进 server.Options —— 这段装配在一个
// 巨大的闭包里，没有可以注入的接缝，只能按源码契约核对。
func TestServerOptionsCarryHarnessOverrides(t *testing.T) {
	raw, err := os.ReadFile("mindfs.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "LiveCompactHarnesses:               liveCompactHarnessOverrides(startupCfg),") {
		t.Fatal("server.Options no longer receives liveCompactHarnessOverrides(startupCfg)")
	}
}
