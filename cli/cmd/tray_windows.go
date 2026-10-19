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

var (
	trayUser32          = windows.NewLazySystemDLL("user32.dll")
	trayShell32         = windows.NewLazySystemDLL("shell32.dll")
	trayRegisterProc    = trayUser32.NewProc("RegisterClassW")
	trayCreateProc      = trayUser32.NewProc("CreateWindowExW")
	trayCreateIconProc  = trayUser32.NewProc("CreateIconFromResourceEx")
	trayMetricProc      = trayUser32.NewProc("GetSystemMetrics")
	trayGetMsgProc      = trayUser32.NewProc("GetMessageW")
	trayTranslateProc   = trayUser32.NewProc("TranslateMessage")
	trayDispatchProc    = trayUser32.NewProc("DispatchMessageW")
	trayDefProc         = trayUser32.NewProc("DefWindowProcW")
	trayRegisterMsgProc = trayUser32.NewProc("RegisterWindowMessageW")
	trayNotifyProc      = trayShell32.NewProc("Shell_NotifyIconW")
	trayMenuProc        = trayUser32.NewProc("CreatePopupMenu")
	trayDestroyMenuProc = trayUser32.NewProc("DestroyMenu")
	trayAppendProc      = trayUser32.NewProc("AppendMenuW")
	trayTrackProc       = trayUser32.NewProc("TrackPopupMenu")
	trayForegroundProc  = trayUser32.NewProc("SetForegroundWindow")
	trayCursorProc      = trayUser32.NewProc("GetCursorPos")
	trayQuitProc        = trayUser32.NewProc("PostQuitMessage")
	trayPostMsgProc     = trayUser32.NewProc("PostMessageW")

	trayState struct {
		addr           string
		useTLS         bool
		icon           trayNotifyIcon
		taskbarCreated uint32
		onQuit         func()
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

// LazyProc.Call 的 error 是 GetLastError，只有 API 自己置错时才有意义。所以下面一律看返回值。
func trayWndProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	if trayState.taskbarCreated != 0 && msg == uintptr(trayState.taskbarCreated) {
		// explorer 重启会清空整个通知区，图标得自己补回来（所有托盘程序都要处理这条）。
		if !addTrayIcon(hwnd) {
			log.Printf("[tray] re-add after taskbar restart failed")
		}
		return 0
	}
	switch msg {
	case trayCallbackMessage:
		// 没调 NIM_SETVERSION，lparam 就是鼠标消息本身（经典回调语义）。
		switch uint32(lparam) {
		case wmRButtonUp, wmContextMenu:
			log.Printf("[tray] menu requested")
			showTrayMenu(hwnd)
		case wmLButtonDoubleClick:
			openTrayPage()
		}
		return 0
	case wmCommand:
		switch uint32(wparam & 0xffff) {
		case menuQuitID:
			log.Printf("[tray] quit from menu")
			removeTrayIcon()
			trayQuitProc.Call(0)
		}
		return 0
	}
	result, _, _ := trayDefProc.Call(hwnd, msg, wparam, lparam)
	return result
}

// 三步都是 Windows 的硬要求（KB135788）：先 SetForegroundWindow，菜单才会在用户点别处时
// 正常关掉；TrackPopupMenu 之后再补一条 WM_NULL，否则菜单会卡在屏幕上不消失。
func showTrayMenu(hwnd uintptr) {
	menu, _, _ := trayMenuProc.Call()
	if menu == 0 {
		return
	}
	defer trayDestroyMenuProc.Call(menu)

	for _, item := range trayMenuItems() {
		label := windows.StringToUTF16Ptr(item.Label)
		trayAppendProc.Call(menu, mfString, uintptr(item.ID), uintptr(unsafe.Pointer(label)))
	}

	var point trayPoint
	if ok, _, _ := trayCursorProc.Call(uintptr(unsafe.Pointer(&point))); ok == 0 {
		point.X, point.Y = 0, 0
	}
	trayForegroundProc.Call(hwnd)
	trayTrackProc.Call(menu, tpmRightButton, uintptr(point.X), uintptr(point.Y), 0, hwnd, 0)
	trayPostMsgProc.Call(hwnd, wmNull, 0, 0)
}

func openTrayPage() {
	if err := openTarget(trayState.addr, trayState.useTLS, ""); err != nil {
		log.Printf("[tray] open failed: %v", err)
	}
}

func removeTrayIcon() {
	trayNotifyProc.Call(nimDelete, uintptr(unsafe.Pointer(&trayState.icon)))
}

// 注册图标。单独拎出来是因为 explorer 重启（TaskbarCreated）后要原样再来一次。
func addTrayIcon(hwnd uintptr) bool {
	trayState.icon.Hwnd = hwnd
	trayState.icon.Size = uint32(unsafe.Sizeof(trayState.icon))
	trayState.icon.ID = 1
	trayState.icon.Flags = trayIconFlags()
	trayState.icon.Callback = trayCallbackMessage
	copyUTF16Into(&trayState.icon.Tip, trayTooltip(trayState.addr))
	ok, _, _ := trayNotifyProc.Call(nimAdd, uintptr(unsafe.Pointer(&trayState.icon)))
	return ok != 0
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

	// 这个窗口只用来收托盘消息，永不显示，所以不需要任何样式。
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
	trayState.icon.Icon = icon

	created := windows.StringToUTF16Ptr("TaskbarCreated")
	if id, _, _ := trayRegisterMsgProc.Call(uintptr(unsafe.Pointer(created))); id != 0 {
		trayState.taskbarCreated = uint32(id)
	}

	if !addTrayIcon(hwnd) {
		// 以前这里静默返回：图标没加上，用户只看到「托盘里什么都没有」而没有任何线索。
		log.Printf("[tray] Shell_NotifyIconW add failed")
		return nil
	}
	log.Printf("[tray] icon added tip=%q", trayTooltip(addr))
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

func copyUTF16Into(dst *[128]uint16, value string) {
	utf16 := windows.StringToUTF16(value)
	for i := 0; i < len(dst) && i < len(utf16); i++ {
		dst[i] = utf16[i]
	}
}
