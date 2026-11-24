//go:build windows

package sessionmaintenance

import "golang.org/x/sys/windows"

const stillActive = 259 // Windows GetExitCodeProcess 对仍在运行的进程返回 STILL_ACTIVE。

// Windows 没有 POSIX signal 0；用 OpenProcess + GetExitCodeProcess 做等价判断。
// 查询失败时按「仍活着」处理，宁可漏删也不误删仍在用的锁。
func pidAlive(pid int) bool {
	if pid <= 0 {
		return true
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return true
	}
	defer windows.CloseHandle(handle)
	var code uint32
	if err := windows.GetExitCodeProcess(handle, &code); err != nil {
		return true
	}
	return code == stillActive
}
