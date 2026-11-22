//go:build !windows && !linux

package processguard

import "syscall"

// pidProcess is the fallback for Unix systems without pidfd support. MindFS
// endpoints use Linux, where process_linux.go pins the exact process instead.
type pidProcess int

func newPlatformProcess(pid int) (platformProcess, error) {
	return pidProcess(pid), nil
}

func (p pidProcess) Kill() error {
	return syscall.Kill(int(p), syscall.SIGKILL)
}

func (p pidProcess) Close() error { return nil }
