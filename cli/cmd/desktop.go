package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mindfs/internal/deploy"
)

// 套壳进程（Windows 下无参数 / `--desktop`）的跨平台部分：URL、等待与错误页、
// 窗口位置持久化。开窗、托盘、单实例、拉起服务都在 desktop_windows.go ——
// 这里不 import WebView2，Linux 上也能编译并跑这些纯逻辑的测试。

const (
	desktopDefaultWidth  = 1280
	desktopDefaultHeight = 860
	desktopMinWidth      = 640
	desktopMinHeight     = 480
	desktopBoundsFile    = "desktop-window.json"
)

// desktopEntry 决定本次启动是否走套壳。两种命中方式：
//   - 显式 --desktop：任何平台都认，便于调试，具体行为由平台实现决定；
//   - Windows 上「无参数双击」：安装器建的就是这个快捷方式。
//
// 只要带了别的入口参数（--serve、--foreground、内部重启/自启、daemon 环境）
// 就保持旧语义 —— 计划任务、脚本和托盘更新都靠这些参数起服务进程，
// 一旦被当成套壳，服务会在 Session 0 里开一个看不见的窗口然后退出。
func desktopEntry(goos string, argCount int, desktop, serve, foreground, internalAutoStart, internalRestart, daemon bool) bool {
	if desktop {
		return true
	}
	return goos == "windows" && argCount == 1 &&
		!serve && !foreground && !internalAutoStart && !internalRestart && !daemon
}

type desktopShellConfig struct {
	Addr     string
	UseTLS   bool
	NodeName string
	StateDir string // <config-dir>/mindfs：窗口位置写在这里
	LogPath  string // 服务日志；错误页要把它显示出来
}

func (c desktopShellConfig) title() string {
	if name := strings.TrimSpace(c.NodeName); name != "" {
		return name
	}
	return "MindFS"
}

// appURL 是壳窗口要加载的地址：`<scheme>://<host:port><prefix>/`。
func (c desktopShellConfig) appURL() string {
	return addrToURL(c.Addr, deploy.PrefixedPath("/"), c.UseTLS)
}

func (c desktopShellConfig) boundsPath() string {
	if strings.TrimSpace(c.StateDir) == "" {
		return ""
	}
	return filepath.Join(c.StateDir, desktopBoundsFile)
}

type desktopBounds struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

func (b desktopBounds) valid() bool {
	// 多显示器/分辨率变化后旧坐标可能落在屏幕外，这时候宁可用默认位置重新居中，
	// 也不要让窗口出现在用户看不见的地方。
	return b.Width >= desktopMinWidth && b.Height >= desktopMinHeight &&
		b.Width <= 16000 && b.Height <= 16000 &&
		b.X > -20000 && b.X < 20000 && b.Y > -20000 && b.Y < 20000
}

func loadDesktopBounds(path string) (desktopBounds, bool) {
	if strings.TrimSpace(path) == "" {
		return desktopBounds{}, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return desktopBounds{}, false
	}
	var b desktopBounds
	if err := json.Unmarshal(raw, &b); err != nil || !b.valid() {
		return desktopBounds{}, false
	}
	return b, true
}

func saveDesktopBounds(path string, b desktopBounds) error {
	if strings.TrimSpace(path) == "" || !b.valid() {
		return errors.New("invalid desktop bounds")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

// waitForDesktopHealth 轮询到服务健康为止。间隔从 250ms 指数退避到 2s：
// 冷启动通常 1~3 秒，固定 100ms 的密集探测在 SQLite 迁移较慢的机器上纯属空转。
func waitForDesktopHealth(check func() bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	delay := 250 * time.Millisecond
	for {
		if check() {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(delay)
		if delay < 2*time.Second {
			delay *= 2
		}
	}
}

// desktopWaitingPage 是开窗后、服务还没起来时显示的页面。它自己会定时重试，
// 所以即使壳侧的等待逻辑超时（例如服务被安全软件拦了几十秒）也不会把用户钉死在白屏上。
func desktopWaitingPage(cfg desktopShellConfig) string {
	return desktopPage(cfg, "正在启动本机服务…", "服务就绪后会自动打开工作台。")
}

func desktopErrorPage(cfg desktopShellConfig, err error) string {
	reason := "服务在预期时间内没有就绪。"
	if err != nil {
		reason = err.Error()
	}
	return desktopPage(cfg, "本机服务未能启动", reason)
}

func desktopPage(cfg desktopShellConfig, heading, detail string) string {
	logPath := strings.TrimSpace(cfg.LogPath)
	logLine := ""
	if logPath != "" {
		logLine = fmt.Sprintf(`<p class="hint">日志：<code>%s</code></p>`, html.EscapeString(logPath))
	}
	// 3 秒刷新一次：服务晚到时页面自己会跳到工作台，不需要用户理解发生了什么。
	return fmt.Sprintf(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta http-equiv="refresh" content="3">
<title>%s</title>
<style>
  :root { color-scheme: light dark; }
  body { margin: 0; height: 100vh; display: flex; align-items: center; justify-content: center;
         font: 14px/1.6 system-ui, "Segoe UI", sans-serif; background: canvas; color: canvastext; }
  main { max-width: 520px; padding: 32px; text-align: center; }
  h1 { font-size: 18px; margin: 0 0 12px; }
  p { margin: 6px 0; opacity: .8; }
  code { font-size: 12px; word-break: break-all; opacity: .9; }
</style></head>
<body><main>
  <h1>%s</h1>
  <p>%s</p>
  %s
  <p class="hint">若长时间没有进展，可在托盘菜单里用浏览器打开或查看数据目录。</p>
</main></body></html>`,
		html.EscapeString(cfg.title()), html.EscapeString(heading), html.EscapeString(detail), logLine)
}

// fetchDesktopNodeName 在服务就绪后取一次本机名字，窗口标题优先用它：
// 配置里没写 node.name 时（老安装），hostname 往往不是用户认得的「书房 PC」。
func fetchDesktopNodeName(cfg desktopShellConfig) string {
	client := newHTTPClient(cfg.UseTLS, 1500*time.Millisecond)
	resp, err := client.Get(addrToURL(cfg.Addr, deploy.PrefixedPath("/api/node-info"), cfg.UseTLS))
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return ""
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return ""
	}
	return strings.TrimSpace(body.Name)
}

// postDesktopUpdate 触发一次更新检查并返回给用户看的一句话（托盘菜单用）。
func postDesktopUpdate(cfg desktopShellConfig) string {
	client := newHTTPClient(cfg.UseTLS, 15*time.Second)
	resp, err := client.Post(addrToURL(cfg.Addr, deploy.PrefixedPath("/api/app/update"), cfg.UseTLS), "application/json", nil)
	if err != nil {
		return "无法连接本机服务：" + err.Error()
	}
	defer resp.Body.Close()
	var body struct {
		CurrentVersion string `json:"current_version"`
		LatestVersion  string `json:"latest_version"`
		HasUpdate      bool   `json:"has_update"`
		Status         string `json:"status"`
		Message        string `json:"message"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if resp.StatusCode >= 300 {
		if body.Message != "" {
			return body.Message
		}
		return fmt.Sprintf("检查更新失败（HTTP %d）", resp.StatusCode)
	}
	switch {
	case body.HasUpdate && body.LatestVersion != "":
		return fmt.Sprintf("发现新版本 %s（当前 %s）。\n更新会在后台下载并安装。", body.LatestVersion, body.CurrentVersion)
	case body.Status == "downloading" || body.Status == "installing" || body.Status == "restarting":
		return "更新正在进行：" + body.Status
	default:
		return fmt.Sprintf("已是最新版本（%s）。", body.CurrentVersion)
	}
}
