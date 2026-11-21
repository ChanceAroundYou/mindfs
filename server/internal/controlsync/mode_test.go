package controlsync

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// fakeProber 让用例直接决定每次探测的成败，不碰网络。
type fakeProber struct {
	errs  []error
	calls int
}

func (f *fakeProber) Probe(context.Context) error {
	if f.calls >= len(f.errs) {
		return errors.New("probe: no more scripted results")
	}
	err := f.errs[f.calls]
	f.calls++
	return err
}

func newModeFixture(t *testing.T, prober *fakeProber) (*Store, *ModeController) {
	t.Helper()
	store := openTestStore(t)
	mc, err := NewModeController(t.Context(), store, prober)
	if err != nil {
		t.Fatalf("NewModeController: %v", err)
	}
	return store, mc
}

// 连续 3 次失败才降级，且降级结果要落库：重启后仍停在 offline-local。
func TestModeControllerDegradesAfterThreeProbeFailures(t *testing.T) {
	prober := &fakeProber{}
	store, mc := newModeFixture(t, prober)
	ctx := t.Context()
	if mc.Mode() != ModeOnline {
		t.Fatalf("初始模式 = %s，应为 online", mc.Mode())
	}
	changes := make([][2]Mode, 0, 4)
	mc.OnModeChange = func(from, to Mode) { changes = append(changes, [2]Mode{from, to}) }
	if got := mc.Observe(false); got != ModeOnline {
		t.Fatalf("2 次失败就降级了：%s", got)
	}
	if got := mc.Observe(false); got != ModeOnline {
		t.Fatalf("2 次失败就降级了：%s", got)
	}
	if got := mc.Observe(false); got != ModeOfflineLocal {
		t.Fatalf("3 次失败后模式 = %s，应为 offline-local", got)
	}
	if len(changes) != 1 || changes[0] != [2]Mode{ModeOnline, ModeOfflineLocal} {
		t.Fatalf("模式变化回调 = %v，应恰好报一次 online → offline-local", changes)
	}
	// 重启：新建控制器必须读回持久化的模式。
	reloaded, err := NewModeController(ctx, store, prober)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Mode() != ModeOfflineLocal {
		t.Fatalf("重启后模式 = %s，应仍是 offline-local", reloaded.Mode())
	}
}

// 连续 2 次成功才升级到 reconciling（合并本身在后继任务里推进）。
func TestModeControllerUpgradesAfterTwoSuccesses(t *testing.T) {
	prober := &fakeProber{}
	_, mc := newModeFixture(t, prober)
	ctx := t.Context()
	if err := mc.SwitchLocal(ctx); err != nil {
		t.Fatalf("SwitchLocal: %v", err)
	}
	if got := mc.Observe(true); got != ModeOfflineLocal {
		t.Fatalf("1 次成功就升级了：%s", got)
	}
	if got := mc.Observe(true); got != ModeReconciling {
		t.Fatalf("2 次成功后模式 = %s，应为 reconciling", got)
	}
	// 恢复合并完成后由合并方收尾：无冲突回 online。
	if got, err := mc.CompleteReconcile(ctx, false); err != nil || got != ModeOnline {
		t.Fatalf("CompleteReconcile(false) = %s, %v，应回 online", got, err)
	}
}

// 有冲突就停在 conflict-review，不自动回 online。
func TestModeControllerCompleteReconcileWithConflicts(t *testing.T) {
	prober := &fakeProber{}
	_, mc := newModeFixture(t, prober)
	ctx := t.Context()
	if err := mc.SwitchLocal(ctx); err != nil {
		t.Fatalf("SwitchLocal: %v", err)
	}
	mc.Observe(true)
	mc.Observe(true)
	got, err := mc.CompleteReconcile(ctx, true)
	if err != nil || got != ModeConflictReview {
		t.Fatalf("CompleteReconcile(true) = %s, %v，应停在 conflict-review", got, err)
	}
}

// 用户点「切换本地模式」立即生效，不等探测计数。
func TestModeControllerSwitchLocalIsImmediate(t *testing.T) {
	prober := &fakeProber{}
	_, mc := newModeFixture(t, prober)
	if err := mc.SwitchLocal(t.Context()); err != nil {
		t.Fatalf("SwitchLocal: %v", err)
	}
	if mc.Mode() != ModeOfflineLocal {
		t.Fatalf("模式 = %s，应为 offline-local", mc.Mode())
	}
}

// 用户点「重试」当场探一次：通就进 reconciling，不通留在本地模式并记下原因。
func TestModeControllerRetryProbesImmediately(t *testing.T) {
	prober := &fakeProber{errs: []error{errors.New("dial tcp: connection refused")}}
	_, mc := newModeFixture(t, prober)
	ctx := t.Context()
	if err := mc.SwitchLocal(ctx); err != nil {
		t.Fatalf("SwitchLocal: %v", err)
	}
	got, err := mc.Retry(ctx)
	if err == nil {
		t.Fatalf("主节点不通时 Retry 应返回错误")
	}
	if got != ModeOfflineLocal {
		t.Fatalf("Retry 失败后模式 = %s，应仍是 offline-local", got)
	}
	status, err := mc.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.LastError == "" {
		t.Fatalf("Retry 失败没有记下 last_error")
	}
	prober.errs = append(prober.errs, nil)
	got, err = mc.Retry(ctx)
	if err != nil || got != ModeReconciling {
		t.Fatalf("主节点恢复后 Retry = %s, %v，应进 reconciling", got, err)
	}
}

// reconciling 期间主节点再次掉线，要退回 offline-local，而不是假装还在合并。
func TestModeControllerReconcilingFallsBackWhenPrimaryDrops(t *testing.T) {
	prober := &fakeProber{}
	_, mc := newModeFixture(t, prober)
	ctx := t.Context()
	if err := mc.SwitchLocal(ctx); err != nil {
		t.Fatalf("SwitchLocal: %v", err)
	}
	mc.Observe(true)
	mc.Observe(true)
	if mc.Mode() != ModeReconciling {
		t.Fatalf("前置条件不成立：%s", mc.Mode())
	}
	mc.Observe(false)
	mc.Observe(false)
	if got := mc.Observe(false); got != ModeOfflineLocal {
		t.Fatalf("reconciling 期间连续失败后模式 = %s，应退回 offline-local", got)
	}
}

// 状态接口要能读到水位、上次成功时间与最后一次错误。
func TestModeControllerStatusExposesState(t *testing.T) {
	prober := &fakeProber{}
	store, mc := newModeFixture(t, prober)
	ctx := t.Context()
	if err := store.ApplyRemoteChanges(ctx, []RemoteChange{
		remoteChange(t, 7, DomainPreferences, "agents", `{"v":1}`, false),
	}); err != nil {
		t.Fatalf("ApplyRemoteChanges: %v", err)
	}
	mc.Observe(true)
	status, err := mc.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Mode != ModeOnline || status.BaseRev != 7 {
		t.Fatalf("status = %+v，应 online 且 base_rev=7", status)
	}
	if status.LastPrimaryAt.IsZero() {
		t.Fatalf("LastPrimaryAt 没记")
	}
}

// 探活打的是带部署前缀的 /api/control-sync/head，且同时受 https 约束。
func TestHTTPProberHitsHeadEndpoint(t *testing.T) {
	var mu sync.Mutex
	status := http.StatusOK
	var paths []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		code := status
		mu.Unlock()
		w.WriteHeader(code)
	}))
	t.Cleanup(srv.Close)
	ca := writeTempCA(t, srv.Certificate())

	prober, err := NewHTTPProber(srv.URL+"/mindfs", ca)
	if err != nil {
		t.Fatalf("NewHTTPProber: %v", err)
	}
	if err := prober.Probe(t.Context()); err != nil {
		t.Fatalf("主节点可用时 Probe: %v", err)
	}
	mu.Lock()
	status = http.StatusServiceUnavailable
	mu.Unlock()
	if err := prober.Probe(t.Context()); err == nil {
		t.Fatalf("主节点 503 时 Probe 居然成功")
	}
	mu.Lock()
	got := append([]string(nil), paths...)
	mu.Unlock()
	if len(got) != 2 || got[0] != "/mindfs/api/control-sync/head" {
		t.Fatalf("探活路径 = %v，应带部署前缀", got)
	}

	if _, err := NewHTTPProber("http://home:7331", ""); err == nil {
		t.Fatalf("探活接受明文 primary_url")
	}
}
func TestModeControllerRunDrivesProbes(t *testing.T) {
	prober := &fakeProber{errs: []error{
		errors.New("e1"), errors.New("e2"), errors.New("e3"),
	}}
	store := openTestStore(t)
	mc, err := NewModeController(t.Context(), store, prober)
	if err != nil {
		t.Fatalf("NewModeController: %v", err)
	}
	mc.Interval = 5 * time.Millisecond
	mc.Timeout = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go mc.Run(ctx)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if mc.Mode() == ModeOfflineLocal {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("Run 跑了 2 秒仍未降级，模式 = %s", mc.Mode())
}
