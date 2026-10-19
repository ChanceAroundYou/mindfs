//go:build windows

package main

import (
	"log"
	"os"

	"golang.org/x/sys/windows"
)

// ATTACH_PARENT_PROCESS：(DWORD)-1，挂到父进程的控制台上。
const attachParentProcess = ^uintptr(0)

var (
	consoleKernel32   = windows.NewLazySystemDLL("kernel32.dll")
	procAttachConsole = consoleKernel32.NewProc("AttachConsole")
)

// Windows 版编成 GUI 子系统（见 scripts/build-all.sh 的 -H=windowsgui）：控制台子系统程序被
// 「本来就没有控制台」的启动方拉起时（任务计划、资源管理器双击、Run 键），Windows 会新建一个
// 控制台窗口并一直显示 —— 用户看到的就是那个关不掉的黑框，关掉它还会把服务一起带走。
// 这个窗口在进程内部藏不掉：Windows Terminal 是默认终端时控制台被交接给 Windows Terminal，
// GetConsoleWindow 拿到的是个隐藏的假句柄，ShowWindow 对它无效。不分配控制台才是彻底的解法。
//
// 代价是 CLI 输出没地方去（--version/--status/--stop 看不见了），所以从终端里跑时把父进程的
// 控制台挂回来，行为和普通控制台程序一样。挂不上就什么都不做 —— 那正是我们要的「没有窗口」。
func attachParentConsole() {
	if ok, _, _ := procAttachConsole.Call(attachParentProcess); ok == 0 {
		return
	}
	for _, std := range []struct {
		name string
		dst  **os.File
		flag int
	}{
		{"CONOUT$", &os.Stdout, os.O_WRONLY},
		{"CONOUT$", &os.Stderr, os.O_WRONLY},
		{"CONIN$", &os.Stdin, os.O_RDONLY},
	} {
		if file, err := os.OpenFile(std.name, std.flag, 0); err == nil {
			*std.dst = file
		}
	}
	// log 在包初始化时就把 os.Stderr 存下来了，换掉 os.Stderr 不会换掉它的出口。
	log.SetOutput(os.Stderr)
}

// GUI 子系统下后台实例（任务计划 / Run 键）的 stdout/stderr 直接消失：systemd 的
// StandardOutput 与 launchd 的 StandardOutPath 都让后台实例的日志落盘，Windows 原本拿那个
// 黑框当出口。黑框没有了就得自己落盘，否则节点上一条服务日志都留不下。
func redirectBackgroundLogs(logPath string) {
	if err := rotateLogIfNeeded(logPath, maxLogSizeBytes, maxLogBackups); err != nil {
		log.Printf("[mindfs] log rotate warning: %v", err)
	}
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	os.Stdout, os.Stderr = file, file
	log.SetOutput(file)
}
