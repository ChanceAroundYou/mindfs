package update

import (
	"context"
	"sync/atomic"
	"testing"
)

// newTestService 造一个不碰网络、不碰重启的 Service：
// 检查与触发都换成桩，判据与顺序仍然走生产代码。calls 数「触发了几次」。
func newTestService(trigger func(context.Context) error) (*Service, *atomic.Int32) {
	svc := NewService("owner/repo", "v0.1.0", "/tmp/bin/mindfs", nil, 0)
	calls := &atomic.Int32{}
	svc.setCheckProbe(func(context.Context) {})
	svc.setTriggerProbe(func(ctx context.Context) error {
		calls.Add(1)
		return trigger(ctx)
	})
	return svc, calls
}

// readyStatus 造出「有新版本 + 布局支持 + available」这一档。
func readyStatus(svc *Service) {
	svc.updateStatus(func(st *Status) {
		st.HasUpdate = true
		st.LatestVersion = "v0.5.6-1300"
		st.Status = "available"
		st.AutoUpdateSupported = true
	})
}

// 判据表：每一条都对应一个真实事故形态，改坏任意一行都会让某条用例变红。
func TestAutoInstallDecision(t *testing.T) {
	ready := Status{
		Status: "available", HasUpdate: true, LatestVersion: "v0.5.6-1300",
		AutoUpdateSupported: true,
	}
	with := func(mut func(*Status)) Status {
		st := ready
		mut(&st)
		return st
	}

	cases := []struct {
		name        string
		st          Status
		autoInstall bool
		busy        bool
		want        bool
		wantReason  string
	}{
		// 默认关：Unix 上静默重启会停死服务，别的部署形态不该被打开。
		{"默认关闭", ready, false, false, false, "disabled"},
		{"默认关闭且忙", ready, false, true, false, "disabled"},
		// 布局不支持（portable / dev / 缺公钥）：装完不会重启，装了等于骗人。
		{"布局不支持", with(func(st *Status) { st.AutoUpdateSupported = false }), true, false, false, "unsupported"},
		// 没新版本：最常走的一拍，不许触发。
		{"无新版本", with(func(st *Status) { st.HasUpdate = false }), true, false, false, "no-update"},
		{"版本号为空", with(func(st *Status) { st.LatestVersion = "  " }), true, false, false, "no-update"},
		// 有一轮在跑：再触发只会让 TriggerUpdate 回 error 并覆盖状态。
		{"下载中", with(func(st *Status) { st.Status = "downloading" }), true, false, false, "in-progress"},
		{"安装中", with(func(st *Status) { st.Status = "installing" }), true, false, false, "in-progress"},
		{"重启中", with(func(st *Status) { st.Status = "restarting" }), true, false, false, "in-progress"},
		// 忙：这一拍只报 available。把这条写成「忙也装」= 腰斩用户的 agent 回合。
		{"有会话在回复", ready, true, true, false, "busy"},
		{"全部就绪", ready, true, false, true, "install"},
		// 已安装完成（status=installed）不是 in-progress，但此时 HasUpdate 通常已翻假；
		// 即便仍为真也不该再装第二次 —— 用失败态兜住。
		{"上一轮失败后重试", with(func(st *Status) { st.Status = "failed" }), true, false, true, "install"},
	}

	for _, tc := range cases {
		got, reason := autoInstallDecision(tc.st, tc.autoInstall, tc.busy)
		if got != tc.want || reason != tc.wantReason {
			t.Fatalf("%s: got (%t, %q)，want (%t, %q)",
				tc.name, got, reason, tc.want, tc.wantReason)
		}
	}
}

// 默认值必须是关闭 —— 构造函数不给出任何自动安装能力。
func TestServiceDefaultsAutoInstallOff(t *testing.T) {
	svc := NewService("owner/repo", "v0.1.0", "/tmp/bin/mindfs", nil, 0)
	if svc.AutoInstallEnabled() {
		t.Fatal("默认必须关闭：Unix 上静默重启会让服务从 systemd 眼里消失")
	}
}

// 启动循环要真的走 maybeAutoInstall。只验判据函数的话，
// 把 checkAndMaybeInstall 里的那一句删掉照样绿。
func TestStartChecksThenMaybeInstalls(t *testing.T) {
	svc, calls := newTestService(func(context.Context) error { return nil })
	svc.SetAutoInstall(true)

	// 这一拍没有新版本 → 不许触发。
	svc.checkAndMaybeInstall(context.Background())
	if got := calls.Load(); got != 0 {
		t.Fatalf("无新版本时触发了 %d 次静默安装", got)
	}

	// 造出「有新版本、布局支持、空闲」的状态。
	readyStatus(svc)
	svc.checkAndMaybeInstall(context.Background())
	if got := calls.Load(); got != 1 {
		t.Fatalf("就绪时应恰好触发 1 次，实际 %d", got)
	}
}

// 忙探针必须真的被问到。把 busy() 写成恒 false 会让这条红。
func TestBusyProbeBlocksAutoInstall(t *testing.T) {
	svc, calls := newTestService(func(context.Context) error { return nil })
	svc.SetAutoInstall(true)
	svc.SetBusyProbe(func() bool { return true })
	readyStatus(svc)

	svc.checkAndMaybeInstall(context.Background())
	if got := calls.Load(); got != 0 {
		t.Fatalf("有会话在回复时仍装了 %d 次 —— 会腰斩用户的回合", got)
	}

	// 空闲之后放行。
	svc.SetBusyProbe(func() bool { return false })
	svc.checkAndMaybeInstall(context.Background())
	if got := calls.Load(); got != 1 {
		t.Fatalf("空闲后应放行，实际 %d", got)
	}
}

// 触发失败的日志路径不许 panic 也不许卡住循环。
func TestAutoInstallTriggerErrorIsSwallowed(t *testing.T) {
	svc, _ := newTestService(func(context.Context) error { return context.Canceled })
	svc.SetAutoInstall(true)
	readyStatus(svc)
	svc.checkAndMaybeInstall(context.Background())
}
