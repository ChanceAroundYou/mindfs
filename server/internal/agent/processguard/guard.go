// Package processguard keeps agent CLI subprocesses tied to the MindFS
// process that started them.
//
// On Windows job objects with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE are the
// load-bearing piece: when MindFS is hard-terminated, Windows closes the job
// handles and terminates every attached CLI process. Without it a
// scheduled-task restart leaves Claude/Codex children alive with a dead
// proxy port, which surfaces as a synthetic ECONNREFUSED API error. The guard
// keeps one job object per child, not a shared one: a process that already
// lives in a job (the Claude CLI does, shortly after starting) can only be
// re-assigned into an empty job, so a shared job accepts the first child and
// fails every later attach with ERROR_ACCESS_DENIED. See guard_windows.go.
//
// On Unix the service manager already owns the whole cgroup (systemd
// KillMode=control-group); the guard still tracks PIDs so an orderly MindFS
// shutdown cannot leave an agent behind.
package processguard

import "errors"

type platformGuard interface {
	Attach(pid int) error
	Close() error
}

// Guard owns the platform-specific child-process lifetime mechanism.
type Guard struct {
	impl platformGuard
}

// New creates a process guard. It must be closed when the owning pool exits.
func New() *Guard {
	return &Guard{impl: newPlatformGuard()}
}

// Attach binds an already-running process to the guard.
func (g *Guard) Attach(pid int) error {
	if g == nil || g.impl == nil {
		return errors.New("process guard is not initialized")
	}
	if pid <= 0 {
		return errors.New("invalid process id")
	}
	return g.impl.Attach(pid)
}

// Close releases the guard. On Windows this also terminates attached
// processes; on Unix they are killed explicitly.
func (g *Guard) Close() error {
	if g == nil || g.impl == nil {
		return nil
	}
	return g.impl.Close()
}
