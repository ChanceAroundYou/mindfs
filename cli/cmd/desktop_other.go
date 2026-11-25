//go:build !windows

package main

import "fmt"

// 套壳窗口只有 Windows 有：其它平台的入口是浏览器 + systemd/前台终端。
// 这里返回错误而不是静默成功 —— 显式 --desktop 打错了平台要能看见原因。
func runDesktopShell(desktopShellConfig) error {
	return fmt.Errorf("--desktop 只在 Windows 上可用；其它平台直接访问服务地址")
}
