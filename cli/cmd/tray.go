package main

// 右键菜单只有「退出」一项。想开页面双击图标即可（openTrayPage），不必占菜单位。
const menuQuitID = 101

// 托盘只在 Windows 上有意义：后台实例没有终端可看，界面里也没有「启动某个节点」的动作，
// 所以跑着的进程需要一个可点的入口。其它平台保持原样（前台终端或 systemd 已经够了）。
func shouldShowTray(goos string) bool {
	return goos == "windows"
}

func trayTooltip(addr string) string {
	return "mindfs " + addr
}

// 注册图标时给的 uFlags。**NIF_MESSAGE 必须在里面**：少了它 shell 只把图标和悬停提示画出来、
// 却永远不往窗口回调鼠标事件 —— 用户看到的是「图标在、右键没反应」，很容易误判成菜单弹不出来
// 而去改 TrackPopupMenu / 窗口样式，全都不对症。tray_test.go 钉住这一位。
func trayIconFlags() uint32 {
	return nifIcon | nifTip | nifMessage
}

type trayMenuItem struct {
	ID    uint32
	Label string
}

func trayMenuItems() []trayMenuItem {
	return []trayMenuItem{
		{ID: menuQuitID, Label: "退出"},
	}
}
