package main

import "testing"

func TestWorktreeAndMaintenanceConfigPassThrough(t *testing.T) {
	worktree := worktreeOptions(startupConfig{})
	if worktree.AutoFinish != nil || worktree.SweepIntervalMinutes != 0 {
		t.Fatalf("missing worktree config must leave defaults to app: %#v", worktree)
	}

	no := false
	minutes := 3
	cfg := startupConfig{
		Worktree:           &startupWorktreeConfig{AutoFinish: &no, SweepIntervalMinutes: &minutes},
		SessionMaintenance: &startupSessionMaintenanceConfig{Enabled: &no, SweepIntervalMinutes: &minutes, KeepBackups: &no},
	}
	gotWorktree := worktreeOptions(cfg)
	if gotWorktree.AutoFinish == nil || *gotWorktree.AutoFinish || gotWorktree.SweepIntervalMinutes != 3 {
		t.Fatalf("explicit worktree config was not passed through: %#v", gotWorktree)
	}
	gotMaintenance := sessionMaintenanceOptions(cfg)
	if gotMaintenance.Enabled == nil || *gotMaintenance.Enabled || gotMaintenance.KeepBackups == nil || *gotMaintenance.KeepBackups {
		t.Fatalf("explicit maintenance config was not passed through: %#v", gotMaintenance)
	}
	if gotMaintenance.SweepIntervalMinutes != 3 {
		t.Fatalf("unexpected maintenance interval %d", gotMaintenance.SweepIntervalMinutes)
	}
	if defaults := sessionMaintenanceOptions(startupConfig{}); defaults.Enabled != nil || defaults.KeepBackups != nil {
		t.Fatalf("missing maintenance config must leave defaults to app: %#v", defaults)
	}
}
