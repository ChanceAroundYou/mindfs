//go:build windows

package processguard

import (
	"os/exec"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// startWindowsHelper launches a child that stays alive for a while.
func startWindowsHelper(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("cmd.exe", "/c", "ping -n 30 127.0.0.1 >NUL")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	return cmd
}

// putProcessInJob puts an already-running pid into a fresh job object. It
// stands in for the Claude CLI, which lands inside a job object of its own
// within a tenth of a second of starting.
func putProcessInJob(t *testing.T, pid int) windows.Handle {
	t.Helper()
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		t.Fatalf("create helper job: %v", err)
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
		t.Fatalf("configure helper job: %v", err)
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		t.Fatalf("open helper process %d: %v", pid, err)
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		_ = windows.CloseHandle(job)
		t.Fatalf("assign helper process %d to helper job: %v", pid, err)
	}
	t.Cleanup(func() { _ = windows.CloseHandle(job) })
	return job
}

// TestGuardAttachesProcessAlreadyInsideAnotherJob pins the reason the guard
// keeps one job per child. A process that already lives in a job object (the
// Claude CLI does, within ~100 ms of starting) can only be re-assigned into an
// empty job: assigning it into a job that already holds another process fails
// with ERROR_ACCESS_DENIED. A single shared job therefore works exactly once —
// the first child lands in it, every later Attach fails and takes the whole
// session open down with it.
func TestGuardAttachesProcessAlreadyInsideAnotherJob(t *testing.T) {
	first := startWindowsHelper(t)
	second := startWindowsHelper(t)
	putProcessInJob(t, second.Process.Pid)

	g := New()
	if err := g.Attach(first.Process.Pid); err != nil {
		t.Fatalf("first Attach: %v", err)
	}
	defer g.Close()
	if err := g.Attach(second.Process.Pid); err != nil {
		t.Fatalf("second Attach (process already inside another job): %v", err)
	}
}

// TestGuardCloseKillsEveryAttachedProcess keeps the lifetime guarantee intact
// now that each child gets its own job: Close must terminate all of them.
func TestGuardCloseKillsEveryAttachedProcess(t *testing.T) {
	helpers := []*exec.Cmd{startWindowsHelper(t), startWindowsHelper(t)}
	g := New()
	for i, cmd := range helpers {
		if err := g.Attach(cmd.Process.Pid); err != nil {
			t.Fatalf("Attach helper %d: %v", i, err)
		}
	}
	done := make([]chan error, len(helpers))
	for i, cmd := range helpers {
		done[i] = make(chan error, 1)
		go func(c *exec.Cmd, ch chan error) { ch <- c.Wait() }(cmd, done[i])
	}
	if err := g.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	for i := range helpers {
		select {
		case <-done[i]:
		case <-time.After(3 * time.Second):
			t.Fatalf("attached process %d survived Guard.Close", i)
		}
	}
}

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
