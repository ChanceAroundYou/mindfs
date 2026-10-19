//go:build windows

package main

import (
	_ "embed"
	"fmt"
	"log"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

//go:embed assets/tray.ico
var trayIconData []byte

const (
	trayCallbackMessage  = 0x0400 // WM_USER
	nimAdd               = 0x0000
	nimDelete            = 0x0002
	nifIcon              = 0x0002
	nifTip               = 0x0008
	nifMessage           = 0x0004
	wmRButtonUp          = 0x0202
	wmLButtonDoubleClick = 0x0203
	wmCommand            = 0x0111
	smCxSmIcon           = 49
	iconResourceVersion  = 0x00030000
	mfString             = 0x0000
	tpmRightButton       = 0x0008
)

var (
	trayUser32         = windows.NewLazySystemDLL("user32.dll")
	trayShell32        = windows.NewLazySystemDLL("shell32.dll")
	trayRegisterProc   = trayUser32.NewProc("RegisterClassW")
	trayCreateProc     = trayUser32.NewProc("CreateWindowExW")
	trayCreateIconProc = trayUser32.NewProc("CreateIconFromResourceEx")
	trayMetricProc     = trayUser32.NewProc("GetSystemMetrics")
	trayGetMsgProc     = trayUser32.NewProc("GetMessageW")
	trayTranslateProc  = trayUser32.NewProc("TranslateMessage")
	trayDispatchProc   = trayUser32.NewProc("DispatchMessageW")
	trayDefProc        = trayUser32.NewProc("DefWindowProcW")
	trayNotifyProc     = trayShell32.NewProc("Shell_NotifyIconW")
	trayMenuProc       = trayUser32.NewProc("CreatePopupMenu")
	trayAppendProc     = trayUser32.NewProc("AppendMenuW")
	trayTrackProc      = trayUser32.NewProc("TrackPopupMenu")
	trayForegroundProc = trayUser32.NewProc("SetForegroundWindow")
	trayAllowFgProc    = trayUser32.NewProc("AllowSetForegroundWindow")
	trayCursorProc     = trayUser32.NewProc("GetCursorPos")
	trayQuitProc       = trayUser32.NewProc("PostQuitMessage")
	trayPostMsgProc    = trayUser32.NewProc("PostMessageW")

	trayState struct {
		addr   string
		useTLS bool
		icon   trayNotifyIcon
		onQuit func()
	}
)

// 托盘尺寸随 DPI 变（100% 是 16px，150% 是 24px），按系统度量挑最接近的那张，
// 尺寸交给 Windows 自己缩放。加载失败返回错误而不是退回系统默认图标 ——
// 否则「托盘里是哪个程序」这件事就悄悄退化了，用户只会看到一个不认识的白板图标。
func loadTrayIcon() (uintptr, error) {
	size, _, _ := trayMetricProc.Call(smCxSmIcon)
	image, err := pickTrayIconImage(trayIconData, int(size))
	if err != nil {
		return 0, err
	}
	icon, _, lastErr := trayCreateIconProc.Call(
		uintptr(unsafe.Pointer(&image[0])),
		uintptr(len(image)),
		1,                   // fIcon
		iconResourceVersion, // dwVer
		0, 0,                // cx/cy 给 0：用图片自己的尺寸，不再二次缩放
		0,
	)
	if icon == 0 {
		return 0, fmt.Errorf("CreateIconFromResourceEx: %v", lastErr)
	}
	return icon, nil
}

// LazyProc.Call 的 error 是 GetLastError，只有 API 自己置错时才有意义。
// 所以下面一律看返回值。
func trayWndProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	switch msg {
	case trayCallbackMessage:
		// 没设 NIF_VERSION，lparam 就是事件本身（经典回调）。
		switch uint32(lparam) {
		case wmRButtonUp:
			showTrayMenu(hwnd)
		case wmLButtonDoubleClick:
			openTrayPage()
		}
		return 0
	case wmCommand:
		switch uint32(wparam & 0xffff) {
		case menuOpenID:
			openTrayPage()
		case menuQuitID:
			removeTrayIcon()
			trayQuitProc.Call(0)
		}
		return 0
	}
	result, _, _ := trayDefProc.Call(hwnd, msg, wparam, lparam)
	return result
}

func showTrayMenu(hwnd uintptr) {
	menu, _, _ := trayMenuProc.Call()
	if menu == 0 {
		return
	}
	for _, item := range trayMenuItems() {
		label := windows.StringToUTF16Ptr(item.Label)
		trayAppendProc.Call(menu, mfString, uintptr(item.ID), uintptr(unsafe.Pointer(label)))
	}

	var point trayPoint
	if ok, _, _ := trayCursorProc.Call(uintptr(unsafe.Pointer(&point))); ok == 0 {
		point.X, point.Y = 0, 0
	}
	// 从任务计划/Run 键启动的进程不是前台进程，SetForegroundWindow 会被 Windows 静默拒绝，
	// TrackPopupMenu 因而显示不出来 —— 先 AllowSetForegroundWindow(ASFW_ANY) 放行，再置顶。
	const asfwAny = ^uintptr(0) // -1
	trayAllowFgProc.Call(asfwAny)
	trayForegroundProc.Call(hwnd)
	trayTrackProc.Call(menu, tpmRightButton, uintptr(point.X), uintptr(point.Y), 0, hwnd, 0)
	// 不发 WM_NULL，菜单在点别处后不会收起、下次右键也显示不出来；这是托盘菜单的标准收尾。
	trayPostMsgProc.Call(hwnd, 0, 0, 0)
}

func openTrayPage() {
	if err := openTarget(trayState.addr, trayState.useTLS, ""); err != nil {
		log.Printf("[tray] open failed: %v", err)
	}
}

func removeTrayIcon() {
	trayNotifyProc.Call(nimDelete, uintptr(unsafe.Pointer(&trayState.icon)))
}

// 消息循环必须和建窗口的线程是同一个，所以这里锁线程。
// 托盘只是入口，拿不到（没有 shell、shell 还没起）时只少一个图标，不该把服务带停。
func runTray(addr string, useTLS bool, onQuit func()) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	trayState.addr = addr
	trayState.useTLS = useTLS
	trayState.onQuit = onQuit

	className := windows.StringToUTF16Ptr("mindfs.tray")
	class := trayWindowClass{
		WndProc:   syscall.NewCallback(trayWndProc),
		ClassName: className,
	}
	if atom, _, lastErr := trayRegisterProc.Call(uintptr(unsafe.Pointer(&class))); atom == 0 {
		return fmt.Errorf("RegisterClassW: %v", lastErr)
	}

	hwnd, _, lastErr := trayCreateProc.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(className)),
		0, 0, 0, 0, 0,
		0, 0, 0, 0,
	)
	if hwnd == 0 {
		return fmt.Errorf("CreateWindowExW: %v", lastErr)
	}

	icon, err := loadTrayIcon()
	if err != nil {
		return err
	}

	trayState.icon = trayNotifyIcon{
		Size:     uint32(unsafe.Sizeof(trayState.icon)),
		Hwnd:     hwnd,
		ID:       1,
		Flags:    nifIcon | nifTip | nifMessage,
		Callback: trayCallbackMessage,
		Icon:     icon,
	}
	copyUTF16Into(&trayState.icon.Tip, trayTooltip(addr))
	if ok, _, _ := trayNotifyProc.Call(nimAdd, uintptr(unsafe.Pointer(&trayState.icon))); ok == 0 {
		// 以前这里静默返回：图标没加上，用户只看到「托盘里什么都没有」而没有任何线索。
		log.Printf("[tray] Shell_NotifyIconW add failed")
		return nil
	}
	defer removeTrayIcon()

	for {
		var message trayMessage
		got, _, lastErr := trayGetMsgProc.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(got) == 0 {
			break // WM_QUIT：菜单里点了退出
		}
		if int32(got) < 0 {
			return fmt.Errorf("GetMessageW: %v", lastErr)
		}
		trayTranslateProc.Call(uintptr(unsafe.Pointer(&message)))
		// DispatchMessageW 收的是 MSG 指针，不是 hwnd：传句柄值会让 user32 把它当结构体解引用，当场段错误。
		trayDispatchProc.Call(uintptr(unsafe.Pointer(&message)))
	}

	if trayState.onQuit != nil {
		trayState.onQuit()
	}
	return nil
}

func copyUTF16Into(dst *[64]uint16, value string) {
	utf16 := windows.StringToUTF16(value)
	for i := 0; i < len(dst) && i < len(utf16); i++ {
		dst[i] = utf16[i]
	}
}
