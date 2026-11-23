package main

import (
	"os"
	"path/filepath"
	"testing"
)

func strPtr(v string) *string { return &v }

// 关闭时必须完全不碰 token_file：配置文件里留着路径、机器上暂时没有这个文件
// （或换了机器），不该让整台机器起不来。
func TestControlSyncOptionsDisabledIgnoresTokenFile(t *testing.T) {
	cfg := startupConfig{ControlSync: &startupControlSyncConfig{
		Enabled:   boolPtr(false),
		TokenFile: strPtr(filepath.Join(t.TempDir(), "missing.token")),
	}}
	opts, err := controlSyncOptions(cfg, "control", "")
	if err != nil {
		t.Fatalf("关闭时不该读 token：%v", err)
	}
	if opts != nil {
		t.Fatalf("关闭时应返回 nil，得到 %+v", opts)
	}
}

// 主控端默认打开（用户裁定）：没写 control_sync 段也当作 primary + 启用。
// token 不在这里生成 —— 生成要落盘，属于启动装配（server/app），而这里是
// 配置翻译。所以 Token 空 + TokenFile 指向默认路径，由装配层负责补。
func TestControlSyncOptionsDefaultEnabledForControl(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	opts, err := controlSyncOptions(startupConfig{}, "control", "")
	if err != nil {
		t.Fatalf("controlSyncOptions: %v", err)
	}
	if opts == nil || !opts.Enabled {
		t.Fatalf("opts = %+v，主控端未配置时应默认启用", opts)
	}
	if opts.Role != "primary" {
		t.Fatalf("role = %q，默认应是 primary", opts.Role)
	}
	if opts.Token != "" {
		t.Fatalf("token = %q，配置翻译不该自己造密钥", opts.Token)
	}
	want := filepath.Join(dir, "mindfs", "control_sync.token")
	if opts.TokenFile != want {
		t.Fatalf("token_file = %q，应落到 %q", opts.TokenFile, want)
	}
}

// worker 节点不跑同步：它不是控制面节点，起了也只会被装配层拒绝。
func TestControlSyncOptionsWorkerStaysOff(t *testing.T) {
	opts, err := controlSyncOptions(startupConfig{}, "worker", "")
	if err != nil || opts != nil {
		t.Fatalf("opts=%+v err=%v，worker 应 (nil,nil)", opts, err)
	}
}

// 显式关闭仍然管用 —— 默认开不等于不能关。
func TestControlSyncOptionsExplicitDisableWins(t *testing.T) {
	cfg := startupConfig{ControlSync: &startupControlSyncConfig{Enabled: boolPtr(false)}}
	opts, err := controlSyncOptions(cfg, "control", "")
	if err != nil || opts != nil {
		t.Fatalf("opts=%+v err=%v，显式关闭应 (nil,nil)", opts, err)
	}
}

// token 只从文件读，行尾换行要吃掉（`echo` 生成的文件几乎必然带）。
func TestControlSyncOptionsReadsTokenFile(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "sync.token")
	if err := os.WriteFile(tokenPath, []byte("  s3cret-token-0123456789\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	cfg := startupConfig{ControlSync: &startupControlSyncConfig{
		Enabled:    boolPtr(true),
		Role:       strPtr("edge"),
		InstanceID: strPtr("hasee"),
		PrimaryURL: strPtr("https://home.example:7331"),
		CAFile:     strPtr("/etc/mindfs/ca.pem"),
		TokenFile:  strPtr(tokenPath),
	}}
	opts, err := controlSyncOptions(cfg, "control", "")
	if err != nil {
		t.Fatalf("controlSyncOptions: %v", err)
	}
	if opts == nil || !opts.Enabled {
		t.Fatalf("opts = %+v，应已启用", opts)
	}
	if opts.Token != "s3cret-token-0123456789" {
		t.Fatalf("token = %q，应去掉首尾空白", opts.Token)
	}
	if opts.Role != "edge" || opts.InstanceID != "hasee" ||
		opts.PrimaryURL != "https://home.example:7331" || opts.CAFile != "/etc/mindfs/ca.pem" {
		t.Fatalf("字段映射不对：%+v", opts)
	}
}

// 边缘读不到密钥：进「等配对」而不是拒绝启动。
//
// 边缘的 token 是别人给的，本机生成不出来。但拒绝启动会把用户关在门外 —— 唯一能
// 拿到 token 的路径是这台机器自己的配对页面，而页面要先起得来。所以这里返回一档
// 「服务照常起、不启同步、只挂配对端点」。
func TestControlSyncOptionsEdgeMissingTokenFileAwaitsPairing(t *testing.T) {
	cfg := startupConfig{ControlSync: &startupControlSyncConfig{
		Enabled:   boolPtr(true),
		Role:      strPtr("edge"),
		TokenFile: strPtr(filepath.Join(t.TempDir(), "missing.token")),
	}}
	opts, err := controlSyncOptions(cfg, "control", "/etc/mindfs/config.json")
	if err != nil {
		t.Fatalf("未配对边缘不该在这里失败：%v", err)
	}
	if opts == nil || !opts.PendingPairing || opts.Enabled {
		t.Fatalf("opts = %+v，应为「等配对」：不启同步、挂配对端点", opts)
	}
	if opts.Role != "edge" || opts.ConfigPath != "/etc/mindfs/config.json" {
		t.Fatalf("opts = %+v，配对要能写回同一个 config.json", opts)
	}
}

// 主节点读不到 token 不算失败：装配层会生成一份（首次上线不该要求用户先手搓密钥）。
func TestControlSyncOptionsPrimaryMissingTokenFileIsGeneratedLater(t *testing.T) {
	dir := t.TempDir()
	tokPath := filepath.Join(dir, "missing.token")
	cfg := startupConfig{ControlSync: &startupControlSyncConfig{
		Enabled:   boolPtr(true),
		Role:      strPtr("primary"),
		TokenFile: strPtr(tokPath),
	}}
	opts, err := controlSyncOptions(cfg, "control", "")
	if err != nil {
		t.Fatalf("primary 缺 token_file 不该在这里失败：%v", err)
	}
	if opts == nil || opts.Token != "" || opts.TokenFile != tokPath {
		t.Fatalf("opts = %+v，应留给装配层在该路径生成", opts)
	}
}

// 空 token 文件与「文件不存在」同一档：都等于还没配对。
func TestControlSyncOptionsEmptyTokenFileAwaitsPairing(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "sync.token")
	if err := os.WriteFile(tokenPath, []byte(" \n\t\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	cfg := startupConfig{ControlSync: &startupControlSyncConfig{
		Enabled:   boolPtr(true),
		Role:      strPtr("edge"),
		TokenFile: strPtr(tokenPath),
	}}
	opts, err := controlSyncOptions(cfg, "control", "")
	if err != nil {
		t.Fatalf("空 token 文件应按未配对处理：%v", err)
	}
	if opts == nil || !opts.PendingPairing || opts.Enabled {
		t.Fatalf("opts = %+v，应为「等配对」", opts)
	}
}

// 没配 token_file 时沿用默认路径（主节点）或留空（边缘，由装配层报缺 token）。
func TestControlSyncOptionsWithoutTokenFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	opts, err := controlSyncOptions(startupConfig{ControlSync: &startupControlSyncConfig{
		Enabled: boolPtr(true),
	}}, "control", "")
	if err != nil {
		t.Fatalf("controlSyncOptions: %v", err)
	}
	want := filepath.Join(dir, "mindfs", "control_sync.token")
	if opts == nil || opts.Token != "" || opts.TokenFile != want {
		t.Fatalf("opts = %+v，token_file 缺省应为 %q", opts, want)
	}
}

// 配对写回的必须是本进程真正读的那份 config.json：显式 -config 优先，
// 否则缺省路径。写错文件的表现是「配对成功、重启后回到原样」，没有报错。
func TestEffectiveConfigPathPrefersExplicitFlag(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	if got := effectiveConfigPath("/etc/mindfs/custom.json"); got != "/etc/mindfs/custom.json" {
		t.Fatalf("显式 -config 应优先，got %q", got)
	}
	want := filepath.Join(dir, "mindfs", "config.json")
	if got := effectiveConfigPath(""); got != want {
		t.Fatalf("缺省路径 = %q，want %q", got, want)
	}
	if got := effectiveConfigPath("   "); got != want {
		t.Fatalf("空白 -config 应按未提供处理，got %q", got)
	}
}
