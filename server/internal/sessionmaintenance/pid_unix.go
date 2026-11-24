//go:build !windows

package sessionmaintenance

import (
	"errors"
	"os"
	"syscall"
)

func pidAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	// 存在但没权限发信号（如 PID 1）同样是活着，不能删锁。
	return err == nil || errors.Is(err, syscall.EPERM)
}
