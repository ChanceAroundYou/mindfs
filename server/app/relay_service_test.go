package app

// relayService 的节点发布：隧道建起来的那一刻，其他设备应该能在节点表里
// 看到这台边控端，地址是 主控基址 + /relay/<name>。
//
// 这条链路的价值全在「其他设备自动同步到这个访问方式」—— 名字分配、隧道、
// 转发各自都有测试，唯独「发布出去的那条记录写成什么样」没人钉。写错了
// （少了部署前缀、名字用 instance id 而不是主控分配的名字、Remote 没置位）
// 的现象是一句「看不到这台机器」，比崩溃难查得多。

import (
	"context"
	"path/filepath"
	"testing"

	"mindfs/server/internal/controlsync"
	"mindfs/server/internal/nodes"
)

func newRelayServiceFixture(t *testing.T, relayBase string) (*relayService, *nodes.Store) {
	t.Helper()
	store, err := controlsync.Open(filepath.Join(t.TempDir(), "control_sync.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	nodeStore, err := nodes.NewStoreAt(t.TempDir())
	if err != nil {
		t.Fatalf("nodes.NewStoreAt: %v", err)
	}
	svc, err := newRelayService(store, nodeStore, relayBase)
	if err != nil {
		t.Fatalf("newRelayService: %v", err)
	}
	return svc, nodeStore
}

func TestRelayServiceOnTunnelUpPublishesNodeRecord(t *testing.T) {
	svc, nodeStore := newRelayServiceFixture(t, "https://home.example.com/mindfs")
	svc.OnTunnelUp(context.Background(), "hasee-01", "office-pc", "")

	list := nodeStore.List()
	if len(list) != 1 {
		t.Fatalf("节点表有 %d 条，want 1：%+v", len(list), list)
	}
	got := list[0]
	if got.URL != "https://home.example.com/mindfs/relay/office-pc" {
		t.Fatalf("URL = %q（部署前缀与分配名字都必须带上）", got.URL)
	}
	if !got.Remote {
		t.Fatal("relay 发布的记录必须是 Remote：跨节点请求不带 user=")
	}
	if got.ID != "relay:hasee-01" {
		t.Fatalf("ID = %q，应带 relay: 前缀便于与手工节点区分", got.ID)
	}
	if got.Name != "office-pc" {
		t.Fatalf("Name = %q，应是主控分配的名字", got.Name)
	}
}

// 没配 relay_base_url 时用「边控端拨进来的那条请求」里看到的对外基址 ——
// 这是常态：主控不需要知道自己叫什么公网域名。
func TestRelayServiceFallsBackToRequestBase(t *testing.T) {
	svc, nodeStore := newRelayServiceFixture(t, "")
	svc.OnTunnelUp(context.Background(), "hasee-01", "office-pc", "https://home.example.com/mindfs")

	if got := nodeStore.List(); len(got) != 1 || got[0].URL != "https://home.example.com/mindfs/relay/office-pc" {
		t.Fatalf("节点表 = %+v", got)
	}
}

// 两个基址都没有：不发记录（一条明显不对的 URL 比没有更糟），且不能 panic。
func TestRelayServiceSkipsPublishWithoutBase(t *testing.T) {
	svc, nodeStore := newRelayServiceFixture(t, "")
	svc.OnTunnelUp(context.Background(), "hasee-01", "office-pc", "")
	if got := nodeStore.List(); len(got) != 0 {
		t.Fatalf("没有可用基址却发布了 %+v", got)
	}
}

// 重新上线复用同一条记录（按实例 id 幂等），不产生第二条。
func TestRelayServicePublishIsIdempotentPerInstance(t *testing.T) {
	svc, nodeStore := newRelayServiceFixture(t, "https://home.example.com/mindfs")
	svc.OnTunnelUp(context.Background(), "hasee-01", "office-pc", "")
	svc.OnTunnelUp(context.Background(), "hasee-01", "office-pc", "")
	if got := nodeStore.List(); len(got) != 1 {
		t.Fatalf("重复上线后节点表有 %d 条：%+v", len(got), got)
	}
}

// 断开时**保留**记录：删掉等于告诉其他设备「这台机器没了」，而它只是暂时离线。
func TestRelayServiceOnTunnelDownKeepsRecord(t *testing.T) {
	svc, nodeStore := newRelayServiceFixture(t, "https://home.example.com/mindfs")
	svc.OnTunnelUp(context.Background(), "hasee-01", "office-pc", "")
	svc.OnTunnelDown(context.Background(), "hasee-01")
	if got := nodeStore.List(); len(got) != 1 {
		t.Fatalf("断开后记录被删了：%+v", got)
	}
}
