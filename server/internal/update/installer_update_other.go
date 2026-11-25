//go:build !windows

package update

import "errors"

// 非 Windows 平台的更新是解包覆盖，不需要旁路任务；真被调用说明选产物那一层错了，
// 直接报错而不是静默成功。
func platformRunUpdateTask() error {
	return errors.New("自动更新任务只存在于 Windows")
}
