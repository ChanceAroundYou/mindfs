package agent

import "testing"

type recordingProcessGuard struct {
	pids []int
}

func (g *recordingProcessGuard) Attach(pid int) error {
	g.pids = append(g.pids, pid)
	return nil
}

func (g *recordingProcessGuard) Close() error { return nil }

type pidOnlySession struct{ pid int }

func (s pidOnlySession) ProcessID() int { return s.pid }

func TestAttachSessionProcessIDRegistersSubprocess(t *testing.T) {
	guard := &recordingProcessGuard{}
	if err := attachSessionProcessID(guard, pidOnlySession{pid: 4242}); err != nil {
		t.Fatalf("attachSessionProcessID: %v", err)
	}
	if len(guard.pids) != 1 || guard.pids[0] != 4242 {
		t.Fatalf("expected session pid 4242 to be registered, got %v", guard.pids)
	}
}

func TestAttachSessionProcessIDIgnoresMissingPID(t *testing.T) {
	guard := &recordingProcessGuard{}
	if err := attachSessionProcessID(guard, pidOnlySession{pid: 0}); err != nil {
		t.Fatalf("attachSessionProcessID: %v", err)
	}
	if len(guard.pids) != 0 {
		t.Fatalf("expected no attachment for zero pid, got %v", guard.pids)
	}
}
