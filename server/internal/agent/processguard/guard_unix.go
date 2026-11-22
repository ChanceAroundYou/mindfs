//go:build !windows

package processguard

import (
	"errors"
	"sync"
	"syscall"
)

type platformProcess interface {
	Kill() error
	Close() error
}

type unixGuard struct {
	mu        sync.Mutex
	processes map[int]platformProcess
	closed    bool
}

func newPlatformGuard() platformGuard {
	return &unixGuard{processes: make(map[int]platformProcess)}
}

func (g *unixGuard) Attach(pid int) error {
	proc, err := newPlatformProcess(pid)
	if err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		_ = proc.Close()
		return errors.New("process guard is closed")
	}
	g.processes[pid] = proc
	return nil
}

func (g *unixGuard) Close() error {
	g.mu.Lock()
	processes := g.processes
	g.processes = make(map[int]platformProcess)
	g.closed = true
	g.mu.Unlock()

	var errs []error
	for _, proc := range processes {
		// ESRCH just means the CLI already exited; there is nothing to clean up.
		if err := proc.Kill(); err != nil && !errors.Is(err, syscall.ESRCH) {
			errs = append(errs, err)
		}
		if err := proc.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
