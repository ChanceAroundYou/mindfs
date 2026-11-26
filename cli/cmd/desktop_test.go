package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mindfs/internal/deploy"
)

// 套壳入口判据：带任何「起服务」语义的参数都不能被当成双击。
// 这条错了的后果不是少个窗口，而是计划任务里的服务进程在 Session 0 里建窗然后退出。
func TestDesktopEntryOnlyForBareWindowsDoubleClick(t *testing.T) {
	for _, c := range []struct {
		name                                  string
		goos                                  string
		args                                  int
		desktop, serve, fg, auto, restart, dm bool
		want                                  bool
	}{
		{"windows 双击", "windows", 1, false, false, false, false, false, false, true},
		{"linux 双击不开窗", "linux", 1, false, false, false, false, false, false, false},
		{"显式 --desktop 任何平台都认", "linux", 2, true, false, false, false, false, false, true},
		{"计划任务 --serve", "windows", 2, false, true, false, false, false, false, false},
		{"前台调试 --foreground", "windows", 2, false, false, true, false, false, false, false},
		{"--internal-autostart", "windows", 2, false, false, false, true, false, false, false},
		{"内部重启", "windows", 3, false, false, false, false, true, false, false},
		{"daemon 环境继承", "windows", 1, false, false, false, false, false, true, false},
		{"带位置参数（老快捷方式）", "windows", 2, false, false, false, false, false, false, false},
		{"显式 --desktop 优先于其它 flag", "windows", 2, true, false, false, false, false, true, true},
	} {
		got := desktopEntry(c.goos, c.args, c.desktop, c.serve, c.fg, c.auto, c.restart, c.dm)
		if got != c.want {
			t.Errorf("%s: desktopEntry = %v, want %v", c.name, got, c.want)
		}
	}
}

func withDeployPrefix(t *testing.T, prefix string) {
	t.Helper()
	old := deploy.Prefix
	deploy.Prefix = prefix
	t.Cleanup(func() { deploy.Prefix = old })
}

// 壳窗口加载的地址必须和部署前缀一致：漏了前缀会打开一个服务器 404 的页面，
// 但窗口本身看起来正常，很容易被当成「服务没起来」。
func TestDesktopAppURLFollowsDeployPrefix(t *testing.T) {
	for _, c := range []struct {
		name   string
		prefix string
		cfg    desktopShellConfig
		want   string
	}{
		{"根部署", "", desktopShellConfig{Addr: "127.0.0.1:7332"}, "http://127.0.0.1:7332/"},
		{"带前缀", "/mindfs", desktopShellConfig{Addr: "127.0.0.1:7332"}, "http://127.0.0.1:7332/mindfs/"},
		{"前缀没有前导斜杠", "mindfs", desktopShellConfig{Addr: "127.0.0.1:7332"}, "http://127.0.0.1:7332/mindfs/"},
		{"TLS", "/mindfs", desktopShellConfig{Addr: "127.0.0.1:7332", UseTLS: true}, "https://127.0.0.1:7332/mindfs/"},
		// 监听通配地址不能直接拿去连；addrToURL 会换成回环地址。
		{"通配地址走回环", "/mindfs", desktopShellConfig{Addr: "0.0.0.0:7332"}, "http://127.0.0.1:7332/mindfs/"},
		{"已经是完整 URL", "", desktopShellConfig{Addr: "http://127.0.0.1:9000"}, "http://127.0.0.1:9000/"},
	} {
		t.Run(c.name, func(t *testing.T) {
			withDeployPrefix(t, c.prefix)
			if got := c.cfg.appURL(); got != c.want {
				t.Fatalf("appURL = %q, want %q", got, c.want)
			}
		})
	}
}

func TestDesktopTitleFallsBackToProductName(t *testing.T) {
	if got := (desktopShellConfig{NodeName: "  书房 PC  "}).title(); got != "书房 PC" {
		t.Fatalf("title = %q, want trimmed node name", got)
	}
	// 未配对/老配置没有 node.name 时，标题不能是空字符串：空标题的窗口在任务栏里没法认。
	if got := (desktopShellConfig{}).title(); got != "MindFS" {
		t.Fatalf("title = %q, want MindFS fallback", got)
	}
}

func TestDesktopBoundsValidationRejectsUnusableGeometry(t *testing.T) {
	good := desktopBounds{X: 120, Y: 80, Width: 1280, Height: 860}
	if !good.valid() {
		t.Fatalf("normal bounds rejected: %+v", good)
	}
	for _, c := range []struct {
		name string
		b    desktopBounds
	}{
		{"宽度小于最小", desktopBounds{Width: desktopMinWidth - 1, Height: 800}},
		{"高度小于最小", desktopBounds{Width: 1000, Height: desktopMinHeight - 1}},
		{"尺寸离谱", desktopBounds{Width: 40000, Height: 860}},
		{"坐标在屏幕外", desktopBounds{X: 30000, Width: 1280, Height: 860}},
		{"全零（文件被截断）", desktopBounds{}},
	} {
		if c.b.valid() {
			t.Errorf("%s: 应当被判定为不可用: %+v", c.name, c.b)
		}
	}
}

func TestDesktopBoundsRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "desktop-window.json")
	if _, ok := loadDesktopBounds(path); ok {
		t.Fatal("不存在的文件不该读出位置")
	}

	want := desktopBounds{X: -1920, Y: 40, Width: 1440, Height: 900}
	if err := saveDesktopBounds(path, want); err != nil {
		t.Fatalf("saveDesktopBounds: %v", err)
	}
	got, ok := loadDesktopBounds(path)
	if !ok || got != want {
		t.Fatalf("loadDesktopBounds = %+v/%v, want %+v", got, ok, want)
	}

	// 损坏的文件要当成「没有记录」而不是让套壳起不来。
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadDesktopBounds(path); ok {
		t.Fatal("损坏的窗口位置文件应当被忽略")
	}

	// 空路径（配置目录都取不到时）不能 panic。
	if _, ok := loadDesktopBounds(""); ok {
		t.Fatal("空路径不该读出位置")
	}
	if err := saveDesktopBounds("", want); err == nil {
		t.Fatal("空路径不该写入成功")
	}
}

func TestWaitForDesktopHealth(t *testing.T) {
	if !waitForDesktopHealth(func() bool { return true }, time.Second) {
		t.Fatal("已经就绪时应当立即返回 true")
	}

	calls := 0
	if !waitForDesktopHealth(func() bool { calls++; return calls >= 3 }, 5*time.Second) {
		t.Fatal("第三次探测就绪时应当返回 true")
	}
	if calls < 3 {
		t.Fatalf("探测次数 = %d, want >= 3", calls)
	}

	start := time.Now()
	if waitForDesktopHealth(func() bool { return false }, 300*time.Millisecond) {
		t.Fatal("一直不健康时必须返回 false")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("超时后应当很快返回，实际等了 %s", elapsed)
	}
}

// 等待/错误页是用户看到的第一屏：节点名和日志路径都不能漏转义
// （节点名是用户输入，日志路径含 Windows 反斜杠与 &，直接拼进 HTML 会截断或注入）。
func TestDesktopPagesEscapeAndExposeLogPath(t *testing.T) {
	cfg := desktopShellConfig{
		NodeName: `<b>"书房" & PC</b>`,
		LogPath:  `C:\Users\10178\AppData\Roaming\mindfs\mindfs.log&x`,
	}
	waiting := desktopWaitingPage(cfg)
	if strings.Contains(waiting, "<b>") || strings.Contains(waiting, "mindfs.log&x") {
		t.Fatalf("等待页没有转义用户/路径内容:\n%s", waiting)
	}
	if !strings.Contains(waiting, "&lt;b&gt;") || !strings.Contains(waiting, "mindfs.log&amp;x") {
		t.Fatalf("等待页缺少转义后的节点名或日志路径:\n%s", waiting)
	}
	// 自己会重试：服务晚于壳就绪时，不需要用户刷新或重开窗口。
	if !strings.Contains(waiting, `http-equiv="refresh" content="3"`) {
		t.Fatalf("等待页必须自带定时刷新:\n%s", waiting)
	}

	errPage := desktopErrorPage(cfg, errString("60s 内没有等到服务就绪"))
	if !strings.Contains(errPage, "60s 内没有等到服务就绪") || !strings.Contains(errPage, "本机服务未能启动") {
		t.Fatalf("错误页缺少原因或标题:\n%s", errPage)
	}

	// 没有日志路径时不能留一行空的「日志：」。
	if strings.Contains(desktopWaitingPage(desktopShellConfig{}), "日志：") {
		t.Fatal("未配置日志路径时不该渲染日志行")
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func httptestShell(t *testing.T, status int, body string) desktopShellConfig {
	t.Helper()
	withDeployPrefix(t, "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return desktopShellConfig{Addr: srv.URL}
}

// 窗口标题取服务自报的节点名：配置里没有 node.name 的老安装靠它才显示成用户认得的名字。
func TestFetchDesktopNodeName(t *testing.T) {
	if got := fetchDesktopNodeName(httptestShell(t, 200, `{"id":"pc-win","name":"  书房 PC "}`)); got != "书房 PC" {
		t.Fatalf("name = %q, want trimmed node name", got)
	}
	if got := fetchDesktopNodeName(httptestShell(t, 200, `{"id":"pc-win"}`)); got != "" {
		t.Fatalf("空名字应返回空串，实际 %q", got)
	}
	if got := fetchDesktopNodeName(httptestShell(t, 500, `{"name":"x"}`)); got != "" {
		t.Fatalf("非 200 应返回空串，实际 %q", got)
	}
	if got := fetchDesktopNodeName(desktopShellConfig{Addr: "127.0.0.1:1"}); got != "" {
		t.Fatalf("连不上应返回空串，实际 %q", got)
	}
}

// 托盘「检查更新」的反馈文案：每种状态都要说清楚发生了什么，
// 否则用户点完看到一句「已是最新」或者永远转圈，无从判断是失败还是真没更新。
func TestPostDesktopUpdateMessages(t *testing.T) {
	for _, c := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"有更新", 200, `{"current_version":"0.5.4","latest_version":"0.5.5","has_update":true}`, "发现新版本 0.5.5（当前 0.5.4）"},
		{"下载中", 200, `{"current_version":"0.5.4","status":"downloading"}`, "更新正在进行：downloading"},
		{"已最新", 200, `{"current_version":"0.5.5"}`, "已是最新版本（0.5.5）"},
		{"服务端错误带消息", 500, `{"message":"更新源不可达"}`, "更新源不可达"},
		{"服务端错误无消息", 502, ``, "检查更新失败（HTTP 502）"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := postDesktopUpdate(httptestShell(t, c.status, c.body)); !strings.Contains(got, c.want) {
				t.Fatalf("message = %q, want it to contain %q", got, c.want)
			}
		})
	}
	if got := postDesktopUpdate(desktopShellConfig{Addr: "127.0.0.1:1"}); !strings.Contains(got, "无法连接本机服务") {
		t.Fatalf("message = %q, want a connection error", got)
	}
}

// 套壳页面里不该出现 PWA 安装入口：程序已由安装器装好，WebView2 也没有可安装的
// 浏览器上下文。前端只在 `window.__MIND_FS_DESKTOP_SHELL__ === true` 时隐藏，
// 所以注入脚本必须设置同名全局（跨端核对在 web/tests/desktop-shell-install-cta.test.mjs）。
func TestDesktopShellInjectsInstallMarker(t *testing.T) {
	if !strings.Contains(desktopShellInitScript, "__MIND_FS_DESKTOP_SHELL__") {
		t.Fatalf("desktopShellInitScript = %q, want it to set __MIND_FS_DESKTOP_SHELL__", desktopShellInitScript)
	}
	if !strings.Contains(desktopShellInitScript, "= true") {
		t.Fatalf("desktopShellInitScript = %q, want a truthy marker", desktopShellInitScript)
	}
}
