package main

// 手工镜像的 Windows ABI：常量 + 结构布局。**每个数都照 winuser.h / shellapi.h 抄，不许凭记忆写。**
// 这里已经栽过两次，两次的表现都极具误导性：
//   - NIF_* 抄错 → NIF_MESSAGE 从未置位，图标和悬停提示都正常，但 shell 一个回调都不发，
//     看着像「菜单弹不出来」，实际是点击事件根本没送到窗口；
//   - WM_RBUTTONUP 写成 0x202（那是 WM_LBUTTONUP）→ 左键弹出菜单、右键毫无反应。
//
// 所以这张表由 tray_test.go 逐位钉住。故意放在没有 build tag 的文件里：任何平台都能跑那个测试。
const (
	nimAdd    = 0x0000 // NIM_ADD
	nimDelete = 0x0002 // NIM_DELETE

	nifMessage = 0x0001 // NIF_MESSAGE
	nifIcon    = 0x0002 // NIF_ICON
	nifTip     = 0x0004 // NIF_TIP

	trayCallbackMessage  = 0x0400 // WM_USER：托盘回调，lParam 是鼠标消息
	wmCommand            = 0x0111 // WM_COMMAND
	wmContextMenu        = 0x007B // WM_CONTEXTMENU：键盘唤起菜单（菜单键 / Shift+F10）
	wmRButtonUp          = 0x0205 // WM_RBUTTONUP
	wmLButtonDoubleClick = 0x0203 // WM_LBUTTONDBLCLK

	wmNull              = 0x0000
	smCxSmIcon          = 49
	iconResourceVersion = 0x00030000
	mfString            = 0x0000
	tpmRightButton      = 0x0008
)

// 三个结构必须和 Windows 的 WNDCLASSW / MSG / NOTIFYICONDATAW 逐字段对齐：
// cbSize 是按版本校验的，布局错了 Shell_NotifyIconW 直接返回 false。
type trayWindowClass struct {
	Style      uint32
	WndProc    uintptr
	CbClsExtra uint32
	CbWndExtra uint32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
}

type trayMessage struct {
	Hwnd   uintptr
	Msg    uint32
	WParam uintptr
	LParam uintptr
	Time   uint32
	PtX    int32
	PtY    int32
}

type trayPoint struct {
	X int32
	Y int32
}

// NOTIFYICONDATAW 的完整版（szTip 128 字符），amd64 上是 976 字节 —— 就是 sizeof(NOTIFYICONDATAW)。
// 给 shell 的 cbSize 必须是这个数：它是 shell 判断「这份结构有多长」的唯一依据。
type trayNotifyIcon struct {
	Size        uint32
	Hwnd        uintptr
	ID          uint32
	Flags       uint32
	Callback    uint32
	Icon        uintptr
	Tip         [128]uint16
	State       uint32
	StateMask   uint32
	Info        [256]uint16
	Union       uint32
	InfoTitle   [64]uint16
	InfoFlags   uint32
	GUID        [16]byte
	BalloonIcon uintptr
}
