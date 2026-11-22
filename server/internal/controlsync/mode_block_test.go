package controlsync

import (
	"strings"
	"testing"
)

// 主节点身份/schema 不符 → 锁住并停在离线本地。锁必须持久化，且探活成功也不许
// 自动升级：否则降级 20 秒后就被拉回 reconciling，再撞同一个不符，模式来回跳。
func TestModeControllerBlockedStaysLocalAcrossProbeSuccess(t *testing.T) {
	prober := &fakeProber{}
	store, mc := newModeFixture(t, prober)
	ctx := t.Context()

	blocked, err := mc.BlockLocal(ctx, "replica origin mismatch: recorded=home primary=hasee2")
	if err != nil {
		t.Fatalf("BlockLocal: %v", err)
	}
	if blocked != ModeOfflineLocal {
		t.Fatalf("BlockLocal 后模式 = %s，应为 offline-local", blocked)
	}
	for i := 0; i < UpgradeAfterSuccess+3; i++ {
		if got := mc.Observe(true); got != ModeOfflineLocal {
			t.Fatalf("第 %d 次探测成功后模式 = %s，锁着不该升级", i+1, got)
		}
	}
	status, err := mc.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.PrimaryReachable != true {
		t.Fatalf("探测成功却没把 primary_reachable 记为真")
	}
	if !strings.Contains(status.BlockedReason, "origin mismatch") {
		t.Fatalf("blocked_reason = %q，状态接口要能解释为什么停在本地", status.BlockedReason)
	}

	// 重启：库里锁着，构造时必须直接落回 offline-local，哪怕持久化的模式是别的。
	if err := store.SetMode(ctx, ModeOnline); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	again, err := NewModeController(ctx, store, prober)
	if err != nil {
		t.Fatalf("NewModeController: %v", err)
	}
	if again.Mode() != ModeOfflineLocal {
		t.Fatalf("重启后模式 = %s，锁着必须回到 offline-local", again.Mode())
	}
	if st, err := store.LoadState(ctx); err != nil {
		t.Fatalf("LoadState: %v", err)
	} else if !strings.Contains(st.BlockedReason, "origin mismatch") {
		t.Fatalf("重启后 blocked_reason 丢了：%q", st.BlockedReason)
	}
}

// 用户点「重试」要拿到一次真正的追平机会（身份核对会在追平开头重做），
// 而不是被锁挡着看起来没反应。
func TestModeControllerRetryPunchesThroughBlockOnce(t *testing.T) {
	prober := &fakeProber{errs: []error{nil}}
	_, mc := newModeFixture(t, prober)
	ctx := t.Context()
	if _, err := mc.BlockLocal(ctx, "replica schema incompatible: primary=2 local=1"); err != nil {
		t.Fatalf("BlockLocal: %v", err)
	}
	got, err := mc.Retry(ctx)
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if got != ModeReconciling {
		t.Fatalf("重试探测成功后模式 = %s，应给一次 reconciling", got)
	}
}

// 追平跑通 = 身份核对也过了，锁该解开；否则以后每次降级都要人再点一次。
func TestModeControllerClearBlockIsIdempotent(t *testing.T) {
	prober := &fakeProber{}
	store, mc := newModeFixture(t, prober)
	ctx := t.Context()
	if _, err := mc.BlockLocal(ctx, "blocked for test"); err != nil {
		t.Fatalf("BlockLocal: %v", err)
	}
	if err := mc.ClearBlock(ctx); err != nil {
		t.Fatalf("ClearBlock: %v", err)
	}
	if st, _ := store.LoadState(ctx); st.BlockedReason != "" {
		t.Fatalf("解锁后 blocked_reason = %q", st.BlockedReason)
	}
	if err := mc.ClearBlock(ctx); err != nil {
		t.Fatalf("重复解锁出错：%v", err)
	}
	// 解锁后自动升级恢复：2 次成功即回 reconciling。
	mc.Observe(true)
	if got := mc.Observe(true); got != ModeReconciling {
		t.Fatalf("解锁后 2 次探测成功模式 = %s，应回 reconciling", got)
	}
}
