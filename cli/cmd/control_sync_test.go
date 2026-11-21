package main

import (
	"os"
	"path/filepath"
	"strings"
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
	opts, err := controlSyncOptions(cfg)
	if err != nil {
		t.Fatalf("关闭时不该读 token：%v", err)
	}
	if opts != nil {
		t.Fatalf("关闭时应返回 nil，得到 %+v", opts)
	}
}

func TestControlSyncOptionsAbsentIsNil(t *testing.T) {
	opts, err := controlSyncOptions(startupConfig{})
	if err != nil || opts != nil {
		t.Fatalf("opts=%+v err=%v，未配置应 (nil,nil)", opts, err)
	}
}

// token 只从文件读，行尾换行要吃掉（`echo` 生成的文件几乎必然带）。
func TestControlSyncOptionsReadsTokenFile(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "sync.token")
	if err := os.WriteFile(tokenPath, []byte("  s3cret-token\n"), 0o600); err != nil {
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
	opts, err := controlSyncOptions(cfg)
	if err != nil {
		t.Fatalf("controlSyncOptions: %v", err)
	}
	if opts == nil || !opts.Enabled {
		t.Fatalf("opts = %+v，应已启用", opts)
	}
	if opts.Token != "s3cret-token" {
		t.Fatalf("token = %q，应去掉首尾空白", opts.Token)
	}
	if opts.Role != "edge" || opts.InstanceID != "hasee" ||
		opts.PrimaryURL != "https://home.example:7331" || opts.CAFile != "/etc/mindfs/ca.pem" {
		t.Fatalf("字段映射不对：%+v", opts)
	}
}

// 打开了同步却读不到密钥：拒绝启动，而不是带着空 token 起一个永远同步不了的边缘。
func TestControlSyncOptionsMissingTokenFileFails(t *testing.T) {
	cfg := startupConfig{ControlSync: &startupControlSyncConfig{
		Enabled:   boolPtr(true),
		Role:      strPtr("edge"),
		TokenFile: strPtr(filepath.Join(t.TempDir(), "missing.token")),
	}}
	_, err := controlSyncOptions(cfg)
	if err == nil || !strings.Contains(err.Error(), "read token_file") {
		t.Fatalf("err = %v，应报读不到 token_file", err)
	}
}

func TestControlSyncOptionsEmptyTokenFileFails(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "sync.token")
	if err := os.WriteFile(tokenPath, []byte(" \n\t\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	cfg := startupConfig{ControlSync: &startupControlSyncConfig{
		Enabled:   boolPtr(true),
		TokenFile: strPtr(tokenPath),
	}}
	_, err := controlSyncOptions(cfg)
	if err == nil || !strings.Contains(err.Error(), "is empty") {
		t.Fatalf("err = %v，应报空 token 文件", err)
	}
}

// 没配 token_file 时 token 为空，交给运行时按角色判定（边缘必须失败）。
func TestControlSyncOptionsWithoutTokenFileLeavesTokenEmpty(t *testing.T) {
	cfg := startupConfig{ControlSync: &startupControlSyncConfig{Enabled: boolPtr(true)}}
	opts, err := controlSyncOptions(cfg)
	if err != nil {
		t.Fatalf("controlSyncOptions: %v", err)
	}
	if opts == nil || opts.Token != "" {
		t.Fatalf("opts = %+v，无 token_file 时 token 应为空", opts)
	}
}
