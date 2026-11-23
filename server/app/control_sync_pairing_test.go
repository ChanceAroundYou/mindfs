package app

import (
	"context"
	"testing"

	"mindfs/server/internal/nodeinfo"
)

// 未配对的边缘：服务必须起得来，但只挂配对端点 —— 没有同步、没有采集、没有转发。
// 少挂一条（比如连带 capturer）会让这台机器在还没配对时就开始记变更流。
func TestPendingPairingWiringOnlyExposesPairing(t *testing.T) {
	w := pendingPairingWiring(&ControlSyncOptions{
		Enabled:        false,
		PendingPairing: true,
		Role:           "edge",
		InstanceID:     "hasee",
		TokenFile:      "/tmp/does-not-matter.token",
		ConfigPath:     "/tmp/does-not-matter.json",
	}, nodeinfo.RoleControl)
	if w == nil {
		t.Fatal("未配对边缘应挂上配对端点，而不是完全不起服务")
	}
	if w.pairing == nil {
		t.Fatal("配对端点为 nil，用户没有地方填主控端地址")
	}
	if w.runtime != nil || w.capturer != nil || w.proxy != nil || w.service != nil ||
		w.pusher != nil || w.feed != nil || w.edges != nil || w.token != "" {
		t.Fatalf("未配对边缘不该有同步能力：%+v", w)
	}
}

// 接线层也要把 pending 与实例名交出去：前端靠这条探针决定「要不要出配对表单」。
// API 用例用的是 fake，钉得住路由与 403，钉不住这里到底回什么。
func TestPendingPairingWiringReportsState(t *testing.T) {
	w := pendingPairingWiring(&ControlSyncOptions{
		PendingPairing: true,
		InstanceID:     "hasee",
		TokenFile:      "/tmp/does-not-matter.token",
		ConfigPath:     "/tmp/does-not-matter.json",
	}, nodeinfo.RoleControl)
	if w == nil || w.pairing == nil {
		t.Fatalf("wiring = %+v，应挂上配对端点", w)
	}
	state := w.pairing.PairingState()
	if !state.PendingPairing {
		t.Fatal("未配对的边缘必须自报 pending —— 否则没有 token 可用的机器在页面上看不见")
	}
	if state.InstanceID != "hasee" {
		t.Fatalf("instance_id = %q，应回显本次实例名", state.InstanceID)
	}
}

// 已配对边缘也挂配对端点（就地换主控端），但它绝不能自报 pending：
// 前端见到 pending 会把整条横幅换成配对表单 —— 一台正在 online 同步的机器
// 页面上永远显示「请配对」。这条是 e2e 抓出来的：探针恒回 pending=true。
func TestPairedEdgePairerDoesNotReportPending(t *testing.T) {
	p := newControlSyncPairer(&ControlSyncOptions{
		Role:       "edge",
		InstanceID: "hasee",
		TokenFile:  "/tmp/t",
		ConfigPath: "/tmp/c",
	}, false)
	if p == nil {
		t.Fatal("已配对边缘应保留换主控端的入口")
	}
	if state := p.PairingState(); state.PendingPairing {
		t.Fatalf("已配对边缘自报 pending：%+v —— 前端会一直显示配对表单", state)
	}
	if p.PairingState().InstanceID != "hasee" {
		t.Fatalf("实例名应照旧回显：%+v", p.PairingState())
	}
}

// worker 上没有控制面、也不出页面，配对在它身上没有意义。
func TestPendingPairingWiringSkipsWorker(t *testing.T) {
	w := pendingPairingWiring(&ControlSyncOptions{
		PendingPairing: true,
		TokenFile:      "/tmp/t",
		ConfigPath:     "/tmp/c",
	}, nodeinfo.RoleWorker)
	if w != nil {
		t.Fatalf("worker 不该挂配对端点：%+v", w)
	}
}

// 没有 config.json 路径就写不回落盘配置：配完重启还是未配对，
// 用户会看到一次「配对成功」然后回到原样。宁可少这个入口。
func TestPendingPairingWiringWithoutConfigPathIsNil(t *testing.T) {
	w := pendingPairingWiring(&ControlSyncOptions{
		PendingPairing: true,
		TokenFile:      "/tmp/t",
	}, nodeinfo.RoleControl)
	if w != nil {
		t.Fatalf("缺 config 路径时应不挂端点：%+v", w)
	}
}

// 走真正的装配入口：未配对必须在建库、校验、起后台循环之前就分叉。
// 直接调 pendingPairingWiring 验不到这一跳 —— 把它关掉，那条用例照样绿。
func TestStartControlSyncPendingPairingSkipsWiring(t *testing.T) {
	w, err := startControlSync(context.Background(), &ControlSyncOptions{
		Enabled:        false,
		PendingPairing: true,
		Role:           "edge",
		InstanceID:     "hasee",
		TokenFile:      "/tmp/does-not-matter.token",
		ConfigPath:     "/tmp/does-not-matter.json",
	}, nodeinfo.RoleControl, t.TempDir(), controlSyncStores{})
	if err != nil {
		t.Fatalf("未配对边缘不该在装配期失败（它连 token 都还没有）：%v", err)
	}
	if w == nil || w.pairing == nil {
		t.Fatalf("wiring = %+v，应只挂配对端点", w)
	}
	if w.capturer != nil || w.runtime != nil || w.token != "" {
		t.Fatalf("未配对边缘不该启动同步：%+v", w)
	}
}
