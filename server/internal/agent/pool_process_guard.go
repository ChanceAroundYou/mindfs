package agent

import "fmt"

// processGuard is the narrow part of the processguard.Guard used by the pool.
// Claude and Codex sessions expose their CLI PID after OpenSession returns;
// attaching it there is what keeps a hard MindFS exit from leaving a child
// pointed at the dead in-process proxy.
type processGuard interface {
	Attach(pid int) error
	Close() error
}

type processIDProvider interface {
	ProcessID() int
}

func attachSessionProcessID(guard processGuard, sess any) error {
	if guard == nil {
		return nil
	}
	provider, ok := sess.(processIDProvider)
	if !ok {
		return fmt.Errorf("agent session does not expose its process id")
	}
	pid := provider.ProcessID()
	if pid <= 0 {
		return nil
	}
	return guard.Attach(pid)
}
