package main

import (
	"testing"
	"time"
)

// channels.feishu 只走 config.json（没有对应命令行开关）：每台机器的飞书通道
// 开关与接收人都是部署事实，不该跟着命令行到处走。
func TestStartupConfigReadsFeishuChannel(t *testing.T) {
	cfg := startupConfig{Channels: &startupChannelsConfig{Feishu: &startupFeishuConfig{
		Enabled:      boolPtr(true),
		OwnerOpenID:  stringPtr("ou_owner"),
		LarkCLI:      stringPtr("lark-cli"),
		CardTTLHours: intPtr(24),
	}}}
	enabled, owner, path, ttl := false, "", "", 0
	if err := applyStartupConfig(cfg, map[string]bool{}, startupFlags{
		FeishuEnabled:      &enabled,
		FeishuOwnerOpenID:  &owner,
		FeishuLarkCLI:      &path,
		FeishuCardTTLHours: &ttl,
	}); err != nil {
		t.Fatal(err)
	}
	if !enabled || owner != "ou_owner" || path != "lark-cli" || ttl != 24 {
		t.Fatalf("got %v %q %q %d", enabled, owner, path, ttl)
	}
}

// 缺省必须是「关」：没有配置飞书的人不该因为这次改动多出一个后台进程。
func TestFeishuOptionsDefaultsToOff(t *testing.T) {
	opts := feishuOptions(startupFlags{})
	if opts.Enabled {
		t.Fatal("feishu enabled without configuration")
	}
	if opts.LarkCLIPath != "lark-cli" {
		t.Fatalf("lark cli = %q", opts.LarkCLIPath)
	}
	if opts.CardTTL != 168*time.Hour {
		t.Fatalf("card ttl = %v", opts.CardTTL)
	}
}

// 配了 owner 但没写 lark_cli / ttl 时，阻塞的只有「发不出去」这一件事，
// 不该因为缺省值让整个通道起不来。
func TestFeishuOptionsFillsMissingFields(t *testing.T) {
	enabled, owner, path, ttl := true, "ou_owner", "", 0
	opts := feishuOptions(startupFlags{
		FeishuEnabled:      &enabled,
		FeishuOwnerOpenID:  &owner,
		FeishuLarkCLI:      &path,
		FeishuCardTTLHours: &ttl,
	})
	if !opts.Enabled || opts.OwnerOpenID != "ou_owner" {
		t.Fatalf("opts = %+v", opts)
	}
	if opts.LarkCLIPath != "lark-cli" || opts.CardTTL != 168*time.Hour {
		t.Fatalf("opts = %+v", opts)
	}
}
