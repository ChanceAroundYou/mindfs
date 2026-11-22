//go:build linux

package processguard

import (
	"golang.org/x/sys/unix"
)

// linuxProcess holds a pidfd rather than a bare PID. A PID can be recycled
// while MindFS is alive; killing a recycled PID at shutdown could take down an
// unrelated process. A pidfd always refers to the process that was attached.
type linuxProcess struct {
	fd int
}

func newPlatformProcess(pid int) (platformProcess, error) {
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		return nil, err
	}
	return &linuxProcess{fd: fd}, nil
}

func (p *linuxProcess) Kill() error {
	return unix.PidfdSendSignal(p.fd, unix.SIGKILL, nil, 0)
}

func (p *linuxProcess) Close() error {
	return unix.Close(p.fd)
}
