package agent

import "testing"

func TestBackgroundRuntimeProbeRunsOnWindows(t *testing.T) {
	// 上游把 Windows 整个排除在运行时探测之外，理由是「探测会弹控制台窗口」。
	// 后果是 Windows 节点上 agent 永远停在 available=false / "probe pending"：
	// 状态灯红、选不了 agent，界面里没有任何一条路能让它变绿。
	// 窗口问题在进程标志层已经解决（agent CLI 一律走 acp 进程路径，那里带
	// CREATE_NO_WINDOW —— acp/process_windows_flags_test.go 钉着），所以按平台一刀切不再必要。
	for _, goos := range []string{"windows", "darwin", "linux"} {
		if !shouldRunBackgroundRuntimeProbe(goos) {
			t.Fatalf("runtime probe must run on %s: skipping it leaves probe status stuck at \"probe pending\" forever", goos)
		}
	}
}
