//go:build !windows

package main

// 其它平台没有托盘概念（前台终端或 systemd 就是入口）。留空实现，让调用点只有一处判断。
func runTray(_ string, _ bool, _ func()) error { return nil }
