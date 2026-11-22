package update

// IsNewerVersion 判断 latest 是否比 current 新，语义与发布更新完全相同。
//
// 导出的唯一原因是控制面同步要用它做 `min_server_build` 判定：边缘二进制太旧时
// 不激活新前端。另写一份比较器迟早会在本 fork 的构建号标签（`v0.5.5-1115`，
// `v0.5.5-1116` 谁新）上漂移，而那正是这套解析当初专门修过的地方。
func IsNewerVersion(latest, current string) bool {
	return isNewerVersion(latest, current)
}
