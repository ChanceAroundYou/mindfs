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
		{"NOTIFYICONDATAW", unsafe.Sizeof(trayNotifyIcon{}), 840},
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
		{"Icon", unsafe.Offsetof(trayNotifyIcon{}.Icon), 32},
		{"Tip", unsafe.Offsetof(trayNotifyIcon{}.Tip), 40},
		{"StateMask", unsafe.Offsetof(trayNotifyIcon{}.StateMask), 172},
	} {
		if off.got != off.want {
			t.Fatalf("NOTIFYICONDATAW.%s offset = %d, want %d", off.name, off.got, off.want)
		}
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

	items := trayMenuItems()
	if len(items) != 2 || items[0].ID != menuOpenID || items[1].ID != menuQuitID {
		t.Fatalf("menu must be exactly open + quit, got %v", items)
	}
}
