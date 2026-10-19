package main

import (
	"runtime"
	"testing"
	"unsafe"
)

// 这三个结构是手工镜像的 Windows x64 布局（cbSize 按版本校验，布局错了 Shell_NotifyIconW 直接返回 false）。
// amd64 上 Go 的自然对齐与 MSVC 一致，所以在 Linux 上跑同样能钉住布局；32 位平台不参与。
func TestTrayStructsMatchWindowsX64Layout(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("layout pins are for amd64")
	}
	for _, want := range []struct {
		name string
		got  uintptr
		size uintptr
	}{
		{"WNDCLASSW", unsafe.Sizeof(trayWindowClass{}), 72},
		{"MSG", unsafe.Sizeof(trayMessage{}), 48},
		// sizeof(NOTIFYICONDATAW)：给 shell 的 cbSize 必须是这个数，它靠它判断结构有多长。
		{"NOTIFYICONDATAW", unsafe.Sizeof(trayNotifyIcon{}), 976},
	} {
		if want.got != want.size {
			t.Fatalf("%s = %d bytes, want %d", want.name, want.got, want.size)
		}
	}
	// cbSize 校验的是整份布局，所以这几个偏移也得对上。
	for _, off := range []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"Flags", unsafe.Offsetof(trayNotifyIcon{}.Flags), 20},
		{"Callback", unsafe.Offsetof(trayNotifyIcon{}.Callback), 24},
		{"Icon", unsafe.Offsetof(trayNotifyIcon{}.Icon), 32},
		{"Tip", unsafe.Offsetof(trayNotifyIcon{}.Tip), 40},
		{"StateMask", unsafe.Offsetof(trayNotifyIcon{}.StateMask), 300},
		{"GUID", unsafe.Offsetof(trayNotifyIcon{}.GUID), 952},
		{"BalloonIcon", unsafe.Offsetof(trayNotifyIcon{}.BalloonIcon), 968},
	} {
		if off.got != off.want {
			t.Fatalf("NOTIFYICONDATAW.%s offset = %d, want %d", off.name, off.got, off.want)
		}
	}
}

// shellapi.h / winuser.h 的字面值。这张表写错一位的表现极隐蔽，所以逐位钉死 —— 已经栽过两次：
//   - nifMessage 写成 0x4（那是 NIF_TIP）、nifTip 写成 0x8（那是 NIF_STATE）：图标照常出现、
//     悬停提示照常有（NIF_TIP 被误打误撞置上了），但 shell 从不回调，用户只看到「右键没反应」；
//   - wmRButtonUp 写成 0x202（那是 WM_LBUTTONUP）：右键毫无反应，左键反而弹出菜单。
func TestTrayConstantsMatchWinuserHeader(t *testing.T) {
	for _, c := range []struct {
		name string
		got  uint32
		want uint32
	}{
		{"NIF_MESSAGE", nifMessage, 0x0001},
		{"NIF_ICON", nifIcon, 0x0002},
		{"NIF_TIP", nifTip, 0x0004},
		{"NIM_ADD", nimAdd, 0x0000},
		{"NIM_DELETE", nimDelete, 0x0002},
		{"WM_USER", trayCallbackMessage, 0x0400},
		{"WM_COMMAND", wmCommand, 0x0111},
		{"WM_CONTEXTMENU", wmContextMenu, 0x007B},
		{"WM_LBUTTONDBLCLK", wmLButtonDoubleClick, 0x0203},
		{"WM_RBUTTONUP", wmRButtonUp, 0x0205},
		{"TPM_RIGHTBUTTON", tpmRightButton, 0x0008},
		{"SM_CXSMICON", smCxSmIcon, 49},
	} {
		if c.got != c.want {
			t.Fatalf("%s = %#x, want %#x (winuser.h/shellapi.h)", c.name, c.got, c.want)
		}
	}
}

// 常量对了还不够：真正的契约是「注册图标时带上 NIF_MESSAGE」。少了它 shell 只画图标和提示，
// 一个回调都不发，右键菜单永远出不来。
func TestTrayIconRegistersForCallbackMessages(t *testing.T) {
	flags := trayIconFlags()
	if flags&nifMessage == 0 {
		t.Fatalf("trayIconFlags() = %#x without NIF_MESSAGE: the shell would never send the click callback", flags)
	}
	if flags&nifIcon == 0 || flags&nifTip == 0 {
		t.Fatalf("trayIconFlags() = %#x, want the icon and the tooltip too", flags)
	}
}

func TestTrayIsWindowsOnlyAndOpensThePageOnQuit(t *testing.T) {
	if !shouldShowTray("windows") {
		t.Fatal("Windows background instance has no terminal and no UI action to start it, so the tray must show there")
	}
	for _, goos := range []string{"linux", "darwin"} {
		if shouldShowTray(goos) {
			t.Fatalf("tray must not show on %s: terminal or systemd already covers it", goos)
		}
	}

	if got := trayTooltip("0.0.0.0:7332"); got != "mindfs 0.0.0.0:7332" {
		t.Fatalf("tooltip = %q, want the addr in it so a multi-node box is distinguishable", got)
	}

	// 右键菜单只留「退出」：开页面双击图标就行，不必占菜单位（用户 2026-10-19 明确要求）。
	items := trayMenuItems()
	if len(items) != 1 || items[0].ID != menuQuitID {
		t.Fatalf("menu must be exactly quit, got %v", items)
	}
}
