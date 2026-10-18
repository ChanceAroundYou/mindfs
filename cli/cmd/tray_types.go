package main

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

type trayNotifyIcon struct {
	Size      uint32
	Hwnd      uintptr
	ID        uint32
	Flags     uint32
	Callback  uint32
	Icon      uintptr
	Tip       [64]uint16
	State     uint32
	StateMask uint32
	Info      [256]uint16
	Union     uint32
	InfoTitle [64]uint16
	InfoFlags uint32
	GUID      [16]byte
}
