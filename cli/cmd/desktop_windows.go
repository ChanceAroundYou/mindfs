//go:build windows

package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"mindfs/internal/brand"
)

// Windows 套壳进程：单实例 → 拉起/等待服务 → WebView2 窗口 + 托盘。
//
// 进程边界（方案已定）：一个 mindfs.exe 三种入口。
//   - 无参数 / --desktop：本文件（登录会话，有窗口和托盘）
//   - --serve / --internal-autostart：服务（计划任务，Session 0，无 UI）
//
// 托盘与窗口只属于本进程；服务进程不再注册托盘 —— Session 0 里图标本来就看不见。

const (
	desktopMutexName    = `Local\MindFS-Desktop`
	desktopServiceTask  = "mindfs"
	webView2ClientCLSID = "{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}"
	webView2DownloadURL = "https://go.microsoft.com/fwlink/p/?LinkId=2124703"

	desktopServiceTimeout = 60 * time.Second
	desktopSuperviseEvery = 5 * time.Second

	// Win32 常量：x/sys/windows 没有导出这几个，值按 winuser.h 抄。
	swHide        = 0
	swShow        = 5
	wmClose       = 0x0010
	swpNoZOrder   = 0x0004
	swpNoActivate = 0x0010

	mbYes = 6
	mbNo  = 7
)

// x/sys/windows 只封装了 MessageBox/ShellExecute，窗口过程与位置读写仍要直接取 user32。
// 命名前缀 desk*，与 tray_windows.go 的 tray* 分开，避免两个文件抢同一个变量。
// GWLP_WNDPROC 的值是 -4，uintptr 不能直接写负数；用取反表达同一个位模式。
var gwlpWndProc = ^uintptr(3)

var (
	deskUser32            = windows.NewLazySystemDLL("user32.dll")
	deskSetWindowLongPtrW = deskUser32.NewProc("SetWindowLongPtrW")
	deskCallWindowProcW   = deskUser32.NewProc("CallWindowProcW")
	deskDefWindowProcW    = deskUser32.NewProc("DefWindowProcW")
	deskFindWindowW       = deskUser32.NewProc("FindWindowW")
	deskShowWindow        = deskUser32.NewProc("ShowWindow")
	deskIsZoomed          = deskUser32.NewProc("IsZoomed")
	deskSetForeground     = deskUser32.NewProc("SetForegroundWindow")
	deskGetWindowRect     = deskUser32.NewProc("GetWindowRect")
	deskSetWindowPos      = deskUser32.NewProc("SetWindowPos")

	desktopMutex  windows.Handle
	desktopActive *desktopShellState
)

type desktopRect struct {
	Left, Top, Right, Bottom int32
}

type desktopShellState struct {
	cfg      desktopShellConfig
	wv       webview2.WebView
	hwnd     uintptr
	oldProc  uintptr
	quitting atomic.Bool
}

// desktopWndProc 在 WebView2 自己的窗口过程前面加一层：
// WM_CLOSE 只把窗口收进托盘（壳还要继续跑，服务也要继续被监督），
// 「退出」走 quitting 标记，放行给原来的过程去销毁并结束消息循环。
func desktopWndProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	s := desktopActive
	if s != nil && msg == wmClose {
		s.saveBounds()
		if !s.quitting.Load() {
			deskShowWindow.Call(hwnd, swHide)
			return 0
		}
	}
	if s != nil && s.oldProc != 0 {
		r, _, _ := deskCallWindowProcW.Call(s.oldProc, hwnd, msg, wparam, lparam)
		return r
	}
	r, _, _ := deskDefWindowProcW.Call(hwnd, msg, wparam, lparam)
	return r
}

func runDesktopShell(cfg desktopShellConfig) error {
	// 单实例：.exe 被双击两次时第二个进程只负责把已有窗口叫到前台。
	// 句柄必须活到进程结束 —— 提前 CloseHandle 会让互斥体消失，下一句又变成「首个实例」。
	mutex, err := windows.CreateMutex(nil, false, windows.StringToUTF16Ptr(desktopMutexName))
	if err == windows.ERROR_ALREADY_EXISTS {
		focusDesktopWindow(cfg.title())
		return nil
	}
	if err != nil {
		return fmt.Errorf("创建单实例互斥体失败: %w", err)
	}
	desktopMutex = mutex

	if !ensureWebView2Runtime() {
		// 用户选了「用浏览器打开」或者关掉了下载页：套壳开不了，但功能不中断。
		if err := openTarget(cfg.Addr, cfg.UseTLS, ""); err != nil {
			return fmt.Errorf("WebView2 运行时不可用，且无法打开浏览器: %w", err)
		}
		return nil
	}

	// WebView2 的窗口与消息循环必须固定在同一个 OS 线程上。
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	bounds, hasBounds := loadDesktopBounds(cfg.boundsPath())
	width, height := desktopDefaultWidth, desktopDefaultHeight
	if hasBounds {
		width, height = bounds.Width, bounds.Height
	}

	wv := webview2.NewWithOptions(webview2.WebViewOptions{
		DataPath:  filepath.Join(cfg.StateDir, "shell", "webview2"),
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  cfg.title(),
			Width:  uint(width),
			Height: uint(height),
			IconId: desktopIconResourceID,
		},
	})
	if wv == nil {
		return fmt.Errorf("创建原生窗口失败：WebView2 运行时可能已损坏")
	}
	// 必须在任何导航之前注册：AddScriptToExecuteOnDocumentCreated 只对新文档生效。
	wv.Init(desktopShellInitScript)

	state := &desktopShellState{cfg: cfg, wv: wv, hwnd: uintptr(wv.Window())}
	desktopActive = state
	state.subclass()
	if hasBounds {
		state.restoreBounds(bounds)
	}

	// 窗口先出来，再用等待页顶上：用户看到的是「正在启动」，不是白屏或黑框。
	wv.SetHtml(desktopWaitingPage(cfg))

	go state.startServiceAndWait()
	go runDesktopTray(state)

	wv.Run()

	state.saveBounds()
	wv.Destroy()
	desktopActive = nil
	return nil
}

func (s *desktopShellState) subclass() {
	s.oldProc, _, _ = deskSetWindowLongPtrW.Call(s.hwnd, gwlpWndProc, syscall.NewCallback(desktopWndProc))
}

func (s *desktopShellState) restoreBounds(b desktopBounds) {
	deskSetWindowPos.Call(
		s.hwnd, 0,
		uintptr(b.X), uintptr(b.Y), uintptr(b.Width), uintptr(b.Height),
		swpNoZOrder|swpNoActivate,
	)
}

// saveBounds 只在窗口处于普通状态时记录：最大化/最小化时的 rect 是屏幕或图标区，
// 存下来下次启动会得到一个「一开就铺满」或不可见的位置。
func (s *desktopShellState) saveBounds() {
	if s.hwnd == 0 {
		return
	}
	if zoomed, _, _ := deskIsZoomed.Call(s.hwnd); zoomed != 0 {
		return
	}
	var rect desktopRect
	if ok, _, _ := deskGetWindowRect.Call(s.hwnd, uintptr(unsafe.Pointer(&rect))); ok == 0 {
		return
	}
	b := desktopBounds{
		X:      int(rect.Left),
		Y:      int(rect.Top),
		Width:  int(rect.Right - rect.Left),
		Height: int(rect.Bottom - rect.Top),
	}
	if err := saveDesktopBounds(s.cfg.boundsPath(), b); err != nil {
		log.Printf("[desktop] save window bounds: %v", err)
	}
}

func (s *desktopShellState) showWindow() {
	deskShowWindow.Call(s.hwnd, swShow)
	deskSetForeground.Call(s.hwnd)
}

func (s *desktopShellState) quit() {
	s.quitting.Store(true)
	s.wv.Dispatch(func() { s.wv.Terminate() })
}

func (s *desktopShellState) startServiceAndWait() {
	if serverRunning(s.cfg.Addr, s.cfg.UseTLS) {
		s.openApp()
		go s.watchService()
		return
	}
	if err := startDesktopService(s.cfg.LogPath); err != nil {
		log.Printf("[desktop] start service: %v", err)
	}
	if !waitForDesktopHealth(func() bool { return serverRunning(s.cfg.Addr, s.cfg.UseTLS) }, desktopServiceTimeout) {
		// 等待页本身带 3 秒刷新；这里再换成错误页，把日志路径摆出来。
		s.wv.Dispatch(func() {
			s.wv.SetHtml(desktopErrorPage(s.cfg, fmt.Errorf("%s 内没有等到服务就绪", desktopServiceTimeout)))
		})
		return
	}
	s.openApp()
	go s.watchService()
}

// watchService 只做一件事：健康从「断」变「通」时重新加载页面。
// 服务进程被更新/崩溃拉起后，旧页面里的 WS 已经失效，导航一遍是最省心的恢复方式。
func (s *desktopShellState) watchService() {
	healthy := true
	ticker := time.NewTicker(desktopSuperviseEvery)
	defer ticker.Stop()
	for range ticker.C {
		if s.quitting.Load() {
			return
		}
		up := serverRunning(s.cfg.Addr, s.cfg.UseTLS)
		if up && !healthy {
			s.openApp()
		}
		healthy = up
	}
}

func (s *desktopShellState) openApp() {
	if name := fetchDesktopNodeName(s.cfg); name != "" {
		// 服务端节点名只提供后缀，窗口标题前缀始终是产品名。
		s.wv.Dispatch(func() { s.wv.SetTitle(brand.Title(name)) })
	}
	s.wv.Dispatch(func() { s.wv.Navigate(s.cfg.appURL()) })
}

// startDesktopService 优先走安装器注册的计划任务（S4U，Session 0）；
// 便携运行/任务不存在时退回分离子进程。两条路都只保证「发出启动」，就绪与否交给健康轮询。
func startDesktopService(logPath string) error {
	if err := runScheduledTask(desktopServiceTask); err == nil {
		return nil
	}
	return startServeProcess(logPath)
}

func runScheduledTask(name string) error {
	cmd := exec.Command("schtasks.exe", "/run", "/tn", name)
	configureBackgroundCommand(cmd)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("schtasks /run /tn %s: %w", name, err)
	}
	return nil
}

func startServeProcess(logPath string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := rotateLogIfNeeded(logPath, maxLogSizeBytes, maxLogBackups); err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "--serve")
	cmd.Env = append(cmd.Environ(), daemonEnvKey+"=1")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	configureBackgroundCommand(cmd)
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return err
	}
	return logFile.Close()
}

func focusDesktopWindow(title string) {
	name := windows.StringToUTF16Ptr(title)
	hwnd, _, _ := deskFindWindowW.Call(0, uintptr(unsafe.Pointer(name)))
	if hwnd == 0 {
		return
	}
	deskShowWindow.Call(hwnd, swShow)
	deskSetForeground.Call(hwnd)
}

func runDesktopTray(s *desktopShellState) {
	err := runTray(trayOptions{
		Addr:        s.cfg.Addr,
		UseTLS:      s.cfg.UseTLS,
		Label:       strings.TrimSpace(s.cfg.NodeName),
		OpenWindow:  s.showWindow,
		OpenBrowser: func() { _ = openTarget(s.cfg.Addr, s.cfg.UseTLS, "") },
		CheckUpdate: func() { desktopCheckUpdateDialog(s.cfg) },
		OpenDataDir: func() { desktopOpenPath(s.cfg.StateDir) },
		Uninstall:   desktopRunUninstaller,
		Quit:        s.quit,
	})
	if err != nil {
		log.Printf("[tray] %v", err)
	}
}

// —— WebView2 运行时 ——

func webView2Installed() bool {
	keys := []struct {
		root registry.Key
		path string
	}{
		{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\` + webView2ClientCLSID},
		{registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\EdgeUpdate\Clients\` + webView2ClientCLSID},
		{registry.CURRENT_USER, `SOFTWARE\Microsoft\EdgeUpdate\Clients\` + webView2ClientCLSID},
	}
	for _, k := range keys {
		key, err := registry.OpenKey(k.root, k.path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		pv, _, err := key.GetStringValue("pv")
		key.Close()
		if err == nil && strings.TrimSpace(pv) != "" && pv != "0.0.0.0" {
			return true
		}
	}
	return false
}

// ensureWebView2Runtime 返回 false 表示用户选择/只能走浏览器。
// 选项必须写进正文：MessageBox 的按钮固定是「是/否/取消」，不解释没人知道各自对应什么。
func ensureWebView2Runtime() bool {
	for {
		if webView2Installed() {
			return true
		}
		choice := desktopWebView2Dialog()
		switch choice {
		case mbYes:
			_ = windows.ShellExecute(0, strPtr("open"), strPtr(webView2DownloadURL), nil, nil, swShow)
			time.Sleep(2 * time.Second)
		case mbNo:
			return false
		default: // 取消 = 重试
		}
	}
}

func desktopWebView2Dialog() int32 {
	text := brand.Name() + " 需要 Microsoft Edge WebView2 运行时才能显示窗口。\n\n" +
		"是：打开官方下载页安装运行时（装完回到这里点「取消」重试）\n" +
		"否：改用浏览器打开 " + brand.Name() + "\n" +
		"取消：重试检测"
	ret, _ := windows.MessageBox(0, windows.StringToUTF16Ptr(text), windows.StringToUTF16Ptr(brand.Name()),
		windows.MB_YESNOCANCEL|windows.MB_ICONWARNING)
	return ret
}

// —— 托盘动作 ——

func desktopCheckUpdateDialog(cfg desktopShellConfig) {
	desktopMessageBox("检查更新", postDesktopUpdate(cfg))
}

func desktopOpenPath(path string) {
	if strings.TrimSpace(path) == "" {
		return
	}
	_ = windows.ShellExecute(0, strPtr("open"), windows.StringToUTF16Ptr(path), nil, nil, swShow)
}

func desktopRunUninstaller() {
	// 卸载注册表键是安装身份，不随展示名变化（见 internal/brand 的边界说明）。
	const keyPath = `Software\Microsoft\Windows\CurrentVersion\Uninstall\MindFS`
	title := "卸载 " + brand.Name()
	key, err := registry.OpenKey(registry.CURRENT_USER, keyPath, registry.QUERY_VALUE)
	if err != nil {
		desktopMessageBox(title, "没有找到卸载信息。\n\n如果这是便携运行，直接删除程序目录即可；数据在 %APPDATA%\\mindfs。")
		return
	}
	defer key.Close()
	command, _, err := key.GetStringValue("UninstallString")
	if err != nil || strings.TrimSpace(command) == "" {
		desktopMessageBox(title, "卸载程序未注册（UninstallString 为空）。")
		return
	}
	if err := windows.ShellExecute(0, strPtr("open"), windows.StringToUTF16Ptr(command), nil, nil, swShow); err != nil {
		desktopMessageBox(title, "无法启动卸载程序："+err.Error())
	}
}

func desktopMessageBox(title, text string) {
	_, _ = windows.MessageBox(0, windows.StringToUTF16Ptr(text), windows.StringToUTF16Ptr(title),
		windows.MB_OK|windows.MB_ICONINFORMATION)
}

func strPtr(s string) *uint16 {
	return windows.StringToUTF16Ptr(s)
}
