//go:build windows

package processguard

import (
	"errors"
	"fmt"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// windowsGuard keeps one kill-on-close job object per attached child instead of
// one shared job.
//
// A shared job works exactly once on this Windows build. A process that already
// belongs to a job object — the Claude CLI puts itself in one within ~100 ms of
// starting — can only be assigned again into an *empty* job; assigning it into a
// job that already holds another process fails with ERROR_ACCESS_DENIED. So the
// first attach lands in the shared job, and every later one returns
//
//	assign agent process <pid> to job: Access is denied.
//
// and takes the whole session open down with it. Fresh per-child jobs stay empty
// until their own assignment, so each attach succeeds, and every job handle is
// still owned by this process, which preserves the kill-on-close guarantee.
type windowsGuard struct {
	mu     sync.Mutex
	jobs   []windows.Handle
	closed bool
}

// jobBasicAccountingInformation mirrors JOBOBJECT_BASIC_ACCOUNTING_INFORMATION.
type jobBasicAccountingInformation struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
}

func newPlatformGuard() platformGuard {
	return &windowsGuard{}
}

func newKillOnCloseJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("create agent job: %w", err)
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
		return 0, fmt.Errorf("configure agent job: %w", err)
	}
	return job, nil
}

func (g *windowsGuard) Attach(pid int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return errors.New("process guard is closed")
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("open agent process %d: %w", pid, err)
	}
	defer windows.CloseHandle(process)

	// Reap jobs whose child already exited before adding another handle.
	g.pruneLocked()

	job, err := newKillOnCloseJob()
	if err != nil {
		return err
	}
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		_ = windows.CloseHandle(job)
		return fmt.Errorf("assign agent process %d to job: %w", pid, err)
	}
	g.jobs = append(g.jobs, job)
	return nil
}

// pruneLocked drops job handles that no longer hold a process. Only jobs whose
// accounting information can be read are dropped; an unreadable job is kept so
// that Close still terminates whatever it holds.
func (g *windowsGuard) pruneLocked() {
	if len(g.jobs) == 0 {
		return
	}
	kept := g.jobs[:0]
	for _, job := range g.jobs {
		active, err := jobActiveProcesses(job)
		if err == nil && active == 0 {
			_ = windows.CloseHandle(job)
			continue
		}
		kept = append(kept, job)
	}
	for i := len(kept); i < len(g.jobs); i++ {
		g.jobs[i] = 0
	}
	g.jobs = kept
}

func jobActiveProcesses(job windows.Handle) (uint32, error) {
	var info jobBasicAccountingInformation
	if err := windows.QueryInformationJobObject(
		job,
		windows.JobObjectBasicAccountingInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
		nil,
	); err != nil {
		return 0, err
	}
	return info.ActiveProcesses, nil
}

func (g *windowsGuard) Close() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closed = true
	var firstErr error
	for _, job := range g.jobs {
		// KILL_ON_JOB_CLOSE alone is enough for hard process death;
		// terminating here makes an orderly shutdown deterministic.
		termErr := windows.TerminateJobObject(job, 1)
		closeErr := windows.CloseHandle(job)
		if closeErr != nil && firstErr == nil {
			firstErr = closeErr
		}
		if termErr != nil && !errors.Is(termErr, windows.ERROR_NOT_FOUND) && firstErr == nil {
			firstErr = termErr
		}
	}
	g.jobs = nil
	return firstErr
}
