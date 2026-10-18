package main

const (
	menuOpenID = 100
	menuQuitID = 101
)

// 托盘只在 Windows 上有意义：后台实例没有终端可看，界面里也没有「启动某个节点」的动作，
// 所以跑着的进程需要一个可点的入口。其它平台保持原样（前台终端或 systemd 已经够了）。
func shouldShowTray(goos string) bool {
	return goos == "windows"
}

func trayTooltip(addr string) string {
	return "mindfs " + addr
}

type trayMenuItem struct {
	ID    uint32
	Label string
}

func trayMenuItems() []trayMenuItem {
	return []trayMenuItem{
		{ID: menuOpenID, Label: "打开 mindfs 页面"},
		{ID: menuQuitID, Label: "退出 mindfs"},
	}
}
