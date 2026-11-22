//go:build !windows

package processguard

import (
	"errors"
	"sync"
	"syscall"
)

type unixGuard struct {
	mu     sync.Mutex
	pids   map[int]struct{}
	closed bool
}

func newPlatformGuard() platformGuard {
	return &unixGuard{pids: make(map[int]struct{})}
}

func (g *unixGuard) Attach(pid int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return errors.New("process guard is closed")
	}
	g.pids[pid] = struct{}{}
	return nil
}

func (g *unixGuard) Close() error {
	g.mu.Lock()
	pids := make([]int, 0, len(g.pids))
	for pid := range g.pids {
		pids = append(pids, pid)
	}
	g.pids = make(map[int]struct{})
	g.closed = true
	g.mu.Unlock()

	var errs []error
	for _, pid := range pids {
		// ESRCH just means the CLI already exited; there is nothing to clean up.
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
