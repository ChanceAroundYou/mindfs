//go:build !windows

package processguard

import (
	"os/exec"
	"testing"
	"time"
)

func TestGuardAttachRejectsNonPositivePID(t *testing.T) {
	g := New()
	if err := g.Attach(0); err == nil {
		t.Fatal("Attach(0) should fail")
	}
	if err := g.Attach(-1); err == nil {
		t.Fatal("Attach(-1) should fail")
	}
}

func TestGuardCloseKillsAttachedProcess(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sleep 30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()

	g := New()
	if err := g.Attach(cmd.Process.Pid); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	if err := g.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("attached process survived Guard.Close")
	}
}
