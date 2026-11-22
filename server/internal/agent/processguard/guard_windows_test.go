//go:build windows

package processguard

import (
	"os/exec"
	"testing"
	"time"
)

func TestGuardCloseKillsAttachedProcess(t *testing.T) {
	cmd := exec.Command("cmd.exe", "/c", "ping -n 30 127.0.0.1 >NUL")
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
