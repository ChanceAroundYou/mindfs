package main

import "strings"

// 托盘菜单。2026-11 起托盘只属于**套壳进程**（desktop 模式，跑在登录会话里）：
// 服务进程在 Session 0，托盘图标根本不可见。菜单项按用户可见动作组织，
// 退出只结束壳（服务继续跑），关窗口也只是收进托盘。
//
// ID 从 101 起、退出仍是 101：换掉会与已经装出去的版本行为错乱。
const (
	menuQuitID        = 101
	menuOpenWindowID  = 102
	menuOpenBrowserID = 103
	menuCheckUpdateID = 104
	menuOpenDataDirID = 105
	menuUninstallID   = 106
)

// trayOptions 是壳进程提供的动作集合。托盘本身不实现任何业务，
// 只负责把点击转给这些回调 —— 这样 tray_windows.go 不必知道 WebView2 或安装器。
type trayOptions struct {
	Addr        string
	UseTLS      bool
	Label       string // 节点名；悬停提示与菜单标题
	OpenWindow  func()
	OpenBrowser func()
	CheckUpdate func()
	OpenDataDir func()
	Uninstall   func()
	Quit        func() // 退出壳进程；不停止服务
}

func trayTooltip(opts trayOptions) string {
	label := strings.TrimSpace(opts.Label)
	if label == "" {
		return "mindfs " + opts.Addr
	}
	return label + " · mindfs " + opts.Addr
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

// 顺序就是菜单顺序。
func trayMenuItems() []trayMenuItem {
	return []trayMenuItem{
		{ID: menuOpenWindowID, Label: "打开主界面"},
		{ID: menuOpenBrowserID, Label: "在浏览器中打开"},
		{ID: menuCheckUpdateID, Label: "检查更新"},
		{ID: menuOpenDataDirID, Label: "打开数据目录"},
		{ID: menuUninstallID, Label: "卸载 MindFS"},
		{ID: menuQuitID, Label: "退出"},
	}
}
