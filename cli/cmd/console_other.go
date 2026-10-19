//go:build !windows

package main

// 其它平台没有「Windows 替我们新建一个控制台窗口」这回事，后台实例的输出由 systemd
// (StandardOutput) / launchd (StandardOutPath) 负责，进程内不需要做任何事。
func attachParentConsole() {}

func redirectBackgroundLogs(string) {}
