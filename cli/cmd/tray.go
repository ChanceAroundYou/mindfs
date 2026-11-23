package main

import (
	"strconv"
	"strings"
)

// 右键菜单：打开页面 / 重启以应用配对 / 退出。
//
// 2026-10-19 用户曾要求「只留退出，开页面双击图标就行」；W3 的 Windows 安装包把托盘
// 当成了唯一入口（Session 0、没有终端、没有开始菜单快捷方式），双击图标是个隐藏手势，
// 所以显式补回两项。ID 保持 101 起，退出仍是 101 —— 换掉会让已经装出去的托盘行为错乱。
const (
	menuOpenPageID = 102
	menuRestartID  = 103
	menuQuitID     = 101
)

// trayRestartTaskName 是安装器注册的计划任务名，必须与 scripts/build-windows-setup.sh、
// scripts/deploy-pcwin.sh 同名 —— 写错一个字，菜单点下去就什么都不发生。
const trayRestartTaskName = "mindfs"

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

// 顺序就是菜单顺序。
func trayMenuItems() []trayMenuItem {
	return []trayMenuItem{
		{ID: menuOpenPageID, Label: "打开页面"},
		{ID: menuRestartID, Label: "重启以应用配对"},
		{ID: menuQuitID, Label: "退出"},
	}
}

// trayRestartScript 是本机「重启以应用配对」的全部实现：等本进程真的退出，再 /run 计划任务。
//
// 为什么不用 `schtasks /end`：用户点这一项时手里还有正在写盘的收尾（agent 回合、sqlite WAL、
// 转录落盘），/end 是 TerminateProcess，会把这些砍在半路。这里走的是「自己优雅退出
// （WM_QUIT → onQuit → 关服务），守护进程看到 PID 消失后再把任务拉起来」。
//
// 那个轮询循环不是保险丝：计划任务是 MultipleInstancesPolicy=IgnoreNew，早一步 /run
// 会被**静默吞掉**，服务停在「已退出、没人拉起」的状态，界面上没有任何线索。
//
// 兜底：schtasks /run 失败（没装成计划任务的手工运行）时用原命令行直接拉起一份。
func trayRestartScript(pid int, exe string, args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, trayPSQuote(arg))
	}
	return strings.Join([]string{
		"$ErrorActionPreference = 'Stop'",
		"$pidToWait = " + strconv.Itoa(pid),
		"$exe = " + trayPSQuote(exe),
		"$argList = @(" + strings.Join(quoted, ", ") + ")",
		"for ($i = 0; $i -lt 150; $i++) {",
		"  if (-not (Get-Process -Id $pidToWait -ErrorAction SilentlyContinue)) { break }",
		"  Start-Sleep -Milliseconds 200",
		"}",
		"schtasks.exe /run /tn " + trayRestartTaskName,
		"if ($exe -and $LASTEXITCODE -ne 0) { Start-Process -FilePath $exe -ArgumentList $argList -WindowStyle Hidden }",
	}, "; ")
}

// PowerShell 单引号串里 `'` 要写成 `”`；别改成双引号 —— 那会开始做变量插值。
func trayPSQuote(v string) string {
	return "'" + strings.ReplaceAll(v, "'", "''") + "'"
}
