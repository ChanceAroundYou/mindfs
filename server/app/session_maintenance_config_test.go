package app

import (
	"os"
	"strings"
	"testing"
)

func TestWorktreeAndMaintenanceDefaults(t *testing.T) {
	worktree := WorktreeOptions{}
	if !worktree.autoFinishEnabled() {
		t.Fatal("worktree auto finish must default to true")
	}
	if worktree.sweepInterval().Minutes() != 10 {
		t.Fatalf("worktree sweep default = %v, want 10m", worktree.sweepInterval())
	}
	opts := SessionMaintenanceOptions{}
	if !opts.enabled() || opts.sweepInterval().Minutes() != 10 || !(opts.KeepBackups == nil || *opts.KeepBackups) {
		t.Fatalf("unexpected session maintenance defaults: %#v", opts)
	}
}

func TestExplicitMaintenanceSwitches(t *testing.T) {
	no := false
	worktree := WorktreeOptions{AutoFinish: &no, SweepIntervalMinutes: 4}
	if worktree.autoFinishEnabled() {
		t.Fatal("explicit auto_finish=false must be honored")
	}
	if worktree.sweepInterval().Minutes() != 4 {
		t.Fatalf("worktree sweep interval = %v, want 4m", worktree.sweepInterval())
	}
	opts := SessionMaintenanceOptions{Enabled: &no, KeepBackups: &no, SweepIntervalMinutes: 3}
	if opts.enabled() || opts.sweepInterval().Minutes() != 3 || opts.KeepBackups == nil || *opts.KeepBackups {
		t.Fatalf("explicit maintenance config was not honored: %#v", opts)
	}
}

func TestRuntimeSweepUsesWorktreeInterval(t *testing.T) {
	source, err := os.ReadFile("session_maintenance_sweeper.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	if !strings.Contains(text, "worktree.sweepInterval()") {
		t.Fatal("runtime issue sweep no longer uses worktree.sweep_interval_minutes")
	}
	if !strings.Contains(text, "opts.sweepInterval()") {
		t.Fatal("stale-lock sweep no longer uses session_maintenance.sweep_interval_minutes")
	}
}
