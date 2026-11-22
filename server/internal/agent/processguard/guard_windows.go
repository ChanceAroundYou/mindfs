//go:build windows

package processguard

import (
	"errors"
	"fmt"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsGuard struct {
	mu      sync.Mutex
	job     windows.Handle
	initErr error
	closed  bool
}

func newPlatformGuard() platformGuard {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return &windowsGuard{initErr: fmt.Errorf("create agent job: %w", err)}
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)),
		uint32(unsafe.Sizeof(limits)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return &windowsGuard{initErr: fmt.Errorf("configure agent job: %w", err)}
	}
	return &windowsGuard{job: job}
}

func (g *windowsGuard) Attach(pid int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.initErr != nil {
		return g.initErr
	}
	if g.closed {
		return errors.New("process guard is closed")
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("open agent process %d: %w", pid, err)
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(g.job, process); err != nil {
		return fmt.Errorf("assign agent process %d to job: %w", pid, err)
	}
	return nil
}

func (g *windowsGuard) Close() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.job == 0 {
		return nil
	}
	// KILL_ON_JOB_CLOSE alone is enough for hard process death; terminating
	// here makes an orderly shutdown deterministic and lets Attach fail fast
	// afterwards.
	termErr := windows.TerminateJobObject(g.job, 1)
	closeErr := windows.CloseHandle(g.job)
	g.job = 0
	g.closed = true
	if closeErr != nil {
		return closeErr
	}
	if termErr != nil && !errors.Is(termErr, windows.ERROR_NOT_FOUND) {
		return termErr
	}
	return nil
}
