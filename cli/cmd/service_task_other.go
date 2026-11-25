//go:build !windows

package main

import "errors"

// 服务任务只存在于 Windows 的计划任务里。其他平台保留同名入口，
// 让 main 的分派逻辑不必按 GOOS 分叉，同时把「跑错平台」说清楚。
func runServiceTaskCommand(args []string) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case "--register-service", "--unregister-service":
		return true, errors.New("service task commands are Windows-only")
	default:
		return false, nil
	}
}
