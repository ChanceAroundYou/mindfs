package usecase

import (
	"os"
	"strings"
	"testing"
)

func TestMaintenanceIdlePoolKeysIncludeHarnessPrefix(t *testing.T) {
	keys := sessionMaintenancePoolKeys("claude", "s1")
	if len(keys) == 0 || keys[0] != "claude-s1" {
		t.Fatalf("pool keys = %#v, want the harness-prefixed key first", keys)
	}
	// 守卫必须同时认带前缀与裸键；只查裸键正是活跃会话被误判为空闲的根因。
	source, err := os.ReadFile("session_maintenance.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "sessionMaintenancePoolKeys(") {
		t.Fatal("ensureSessionIdle no longer uses the shared pool-key helper")
	}
}

func TestRuntimeSweepUsesSharedBusyGuard(t *testing.T) {
	source, err := os.ReadFile("session_runtime_issues.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "sessionMaintenanceBusy(pool, issue.Agent, issue.SessionKey)") {
		t.Fatal("runtime sweep no longer uses the harness-prefixed busy guard")
	}
}
