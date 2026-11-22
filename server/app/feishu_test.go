package app

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"mindfs/server/internal/api"
	"mindfs/server/internal/channels/feishu"
)

// 卡片状态文件读不回来时不能装作没事继续跑：空的仓库会忘掉哪些卡片已经答过，
// 同一张 gate 卡重启后再点一次就被当成新的点击。宁可这次不启动通道 ——
// 文件保持原样，人修好再重启即可。
func TestNewFeishuChannelRefusesToStartOnCorruptState(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "feishu"), 0o700); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(dir, "feishu", "cards.json")
	if err := os.WriteFile(statePath, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := newFeishuChannel(FeishuOptions{Enabled: true, OwnerOpenID: "ou_owner"}, dir)
	if svc != nil {
		t.Fatal("channel started on a corrupt card store")
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("corrupt file must be left in place for inspection: %v", err)
	}
}

func TestFeishuRelayConfigFromStartOptions(t *testing.T) {
	cfg := feishuRelayConfigFromOptions(StartOptions{
		Feishu:         FeishuOptions{RelayToken: "  shared-token  "},
		FeishuRelayURL: "  https://control.example/api/channels/feishu/relay  ",
		FeishuNodeID:   "  wsl  ",
	})
	if cfg.Token != "shared-token" || cfg.URL != "https://control.example/api/channels/feishu/relay" || cfg.NodeID != "wsl" {
		t.Fatalf("relay config drifted from StartOptions: %+v", cfg)
	}
}

func TestStartPassesFeishuRelayConfigToSharedServices(t *testing.T) {
	body, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`feishuRelay:\s+feishuRelayConfigFromOptions\(opts\)`).Match(body) {
		t.Fatal("Start must inject relay config from StartOptions into sharedServices")
	}
}

func TestNewHTTPHandlerInjectsFeishuRelayIdentity(t *testing.T) {
	m := &workspaceManager{shared: sharedServices{feishuRelay: feishuRelayConfig{
		Token: "shared-token", URL: "https://control.example/relay", NodeID: "wsl",
	}}}
	h := m.NewHTTPHandler(&api.AppContext{}, "v-test")
	if h.FeishuRelayToken != "shared-token" || h.FeishuNodeID != "wsl" {
		t.Fatalf("handler did not receive relay identity: token=%q node=%q", h.FeishuRelayToken, h.FeishuNodeID)
	}
}

func TestCardNotifierPrefersRelayURLEvenWhenChannelDisabled(t *testing.T) {
	relayCfg := feishuRelayConfig{Token: "shared-token", URL: "https://control.example/relay", NodeID: "wsl"}
	got := feishuCardNotifierFor(sharedServices{feishuRelay: relayCfg}, "u_1")
	relay, ok := got.(*feishu.RelayNotifier)
	if !ok {
		t.Fatalf("relay URL must select RelayNotifier, got %T", got)
	}
	if relay.URL != relayCfg.URL || relay.Token != relayCfg.Token || relay.NodeID != relayCfg.NodeID {
		t.Fatalf("relay notifier config drifted: %+v", relay)
	}

	local := feishu.New(feishu.Config{}, feishu.NewStore(filepath.Join(t.TempDir(), "cards.json")), nil)
	if _, ok := feishuCardNotifierFor(sharedServices{feishu: local}, "u_1").(feishuCardNotifier); !ok {
		t.Fatal("without a relay URL the local M1 notifier must be retained")
	}
	if got := feishuCardNotifierFor(sharedServices{}, "u_1"); got != nil {
		t.Fatalf("no channel and no relay URL must leave the notifier nil, got %T", got)
	}
}
