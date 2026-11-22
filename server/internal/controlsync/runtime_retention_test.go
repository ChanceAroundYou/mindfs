package controlsync

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// 变更流被清理到边缘的水位以下时，重试再多次也补不回来：留在 reconciling 只会
// 每 20 秒撞一次同一个缺口。必须和身份不符、schema 不符一样当场锁进本地模式，
// 让人看见「这份副本得重新采纳主节点」。
func TestRuntimeBlocksReconcileOnRetentionGap(t *testing.T) {
	rt, store, _, source, _ := newRuntimeFixture(t, edgeConfig())
	ctx := t.Context()
	prober := &fakeProber{}
	mc, err := NewModeController(ctx, store, prober)
	if err != nil {
		t.Fatalf("NewModeController: %v", err)
	}
	mc.OnModeChange = rt.modeChanged
	rt.mode = mc

	for i := 0; i < DegradeAfterFailure; i++ {
		mc.Observe(false)
	}
	rt.tick(ctx) // 离线拍：完成交接

	source.mu.Lock()
	source.err = fmt.Errorf("%w: since=3 pruned_through=9", ErrRetentionGap)
	source.mu.Unlock()
	prober.errs = []error{nil}
	if _, err := mc.Retry(ctx); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if got := mc.Mode(); got != ModeReconciling {
		t.Fatalf("模式 = %s，应为 reconciling", got)
	}
	rt.tick(ctx)
	if got := mc.Mode(); got != ModeOfflineLocal {
		t.Fatalf("保留窗口缺口后模式 = %s，应停在 offline-local", got)
	}
	st, err := store.LoadState(ctx)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if !strings.Contains(st.BlockedReason, "retention gap") {
		t.Fatalf("blocked_reason = %q，应记下保留窗口缺口", st.BlockedReason)
	}
	for i := 0; i < UpgradeAfterSuccess+2; i++ {
		if got := mc.Observe(true); got != ModeOfflineLocal {
			t.Fatalf("第 %d 次探测成功后模式 = %s，锁着不该自动升级", i+1, got)
		}
	}
}

// 清理是主节点的职责：启动就跑一次，之后每小时一次。边缘没有变更流，跑它只会
// 白扫一张空表；而主节点漏跑就意味着库无限长大。
func TestRuntimePrimaryPrunesExpiredChangesAtStartup(t *testing.T) {
	cfg := Config{
		Enabled:    true,
		Role:       RolePrimary,
		InstanceID: "home",
	}
	rt, store, _, _, _ := newRuntimeFixture(t, cfg)
	ctx := t.Context()
	now := time.Now()
	var expired int64
	for _, key := range []string{"a", "b"} {
		expired = writeTestChange(t, store, key, false)
		backdateChange(t, store, expired, now.Add(-100*24*time.Hour))
	}
	writeTestChange(t, store, "c", false)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go rt.Run(runCtx)

	deadline := time.Now().Add(2 * time.Second)
	for {
		if revs := changeRevs(t, store); len(revs) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("启动后没有清理过期变更，剩余 rev = %v", changeRevs(t, store))
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
}
