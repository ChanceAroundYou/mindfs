package controlsync

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestRuntimeHandoverAdoptsReplicaAndKeepsPendingEdits(t *testing.T) {
	rt, store, domain, _, _ := newRuntimeFixture(t, edgeConfig())
	ctx := t.Context()
	for _, rec := range []Record{
		testRecord(t, DomainPreferences, "a", `{"v":"primary"}`),
		testRecord(t, DomainPreferences, "b", `{"v":"primary"}`),
	} {
		seedReplicaRecord(t, store, rec)
	}
	tomb := testRecord(t, DomainPreferences, "c", `{"v":"gone"}`)
	tomb.Deleted = true
	tomb.Payload = nil
	seedReplicaRecord(t, store, tomb)
	domain.set("a", `{"v":"stale"}`)
	domain.set("b", `{"v":"local"}`)
	domain.set("c", `{"v":"gone"}`)
	if err := store.PutPending(ctx, PendingChange{
		ChangeID: "hasee:preferences:b:1", Domain: DomainPreferences, Key: "b",
		Payload: json.RawMessage(`{"v":"local"}`),
	}); err != nil {
		t.Fatalf("seed pending: %v", err)
	}

	if err := rt.Handover(ctx); err != nil {
		t.Fatalf("Handover: %v", err)
	}
	if string(domain.items["a"]) != `{"v":"primary"}` {
		t.Fatalf("a = %s，应取副本值", domain.items["a"])
	}
	if string(domain.items["b"]) != `{"v":"local"}` {
		t.Fatalf("b = %s，本地有待回传改动时不该被副本覆盖", domain.items["b"])
	}
	if _, ok := domain.items["c"]; ok {
		t.Fatalf("c 还留着，副本里的墓碑没落地")
	}
	// 交接后基线必须跟上，否则下一拍兜底扫描会把刚写进去的副本值当成新改动再发一遍。
	if _, err := rt.Capturer().CaptureAll(ctx); err != nil {
		t.Fatalf("CaptureAll: %v", err)
	}
	if n, _ := store.CountPending(ctx); n != 1 {
		t.Fatalf("pending = %d，交接不该制造新的待回传", n)
	}
}

// 边缘在线时一律不采集：在线写都转发给主节点，本机权威存储本来不该变。真变了
// （后台任务直写）也不能采 —— 采了就是拿本地过期值去改副本，之后交接与合并都会
// 拿错祖先，恢复时把主节点的新值当成「边缘改过」。

func TestRuntimeHandoverRetriesAfterFailure(t *testing.T) {
	var reported []error
	cfg := edgeConfig()
	cfg.OnError = func(err error) { reported = append(reported, err) }
	rt, store, domain, _, _ := newRuntimeFixture(t, cfg)
	ctx := t.Context()
	seedReplicaRecord(t, store, testRecord(t, DomainPreferences, "agents", `{"v":"primary"}`))
	domain.set("agents", `{"v":"stale"}`)
	domain.applyErr = errors.New("disk full")
	mode := rt.Mode()
	mode.Observe(false)
	mode.Observe(false)
	mode.Observe(false)

	rt.tick(ctx)
	if got := mode.Mode(); got != ModeOfflineLocal {
		t.Fatalf("模式 = %s，交接失败应留在 offline-local", got)
	}
	if string(domain.items["agents"]) != `{"v":"stale"}` {
		t.Fatalf("交接失败却改了权威存储：%s", domain.items["agents"])
	}
	if len(reported) == 0 {
		t.Fatalf("交接失败没有上报")
	}

	domain.applyErr = nil
	rt.tick(ctx)
	if string(domain.items["agents"]) != `{"v":"primary"}` {
		t.Fatalf("重试后权威存储 = %s，应已交接", domain.items["agents"])
	}
}

func TestRuntimeHandoverRunsAgainAfterNextDrop(t *testing.T) {
	rt, store, domain, source, _ := newRuntimeFixture(t, edgeConfig())
	ctx := t.Context()
	mode := rt.Mode()

	first := testRecord(t, DomainPreferences, "agents", `{"v":"v1"}`)
	seedReplicaRecord(t, store, first)
	for i := 0; i < DegradeAfterFailure; i++ {
		mode.Observe(false)
	}
	rt.tick(ctx)
	if string(domain.items["agents"]) != `{"v":"v1"}` {
		t.Fatalf("首次交接后 = %s", domain.items["agents"])
	}

	// 在线（副本继续前进）→ 再次掉线。
	for i := 0; i < UpgradeAfterSuccess; i++ {
		mode.Observe(true)
	}
	if got := mode.Mode(); got != ModeReconciling {
		t.Fatalf("模式 = %s，应为 reconciling", got)
	}
	rt.tick(ctx) // 没有待回传也没有冲突，直接回 online
	if got := mode.Mode(); got != ModeOnline {
		t.Fatalf("模式 = %s，应为 online", got)
	}
	rt.tick(ctx) // 在线的那一拍才追平副本
	if source.syncs == 0 {
		t.Fatalf("在线拍里没有追平")
	}
	second := testRecord(t, DomainPreferences, "agents", `{"v":"v2"}`)
	seedReplicaRecord(t, store, second)

	for i := 0; i < DegradeAfterFailure; i++ {
		mode.Observe(false)
	}
	rt.tick(ctx)
	if string(domain.items["agents"]) != `{"v":"v2"}` {
		t.Fatalf("二次交接后 = %s，应取那时副本里的 v2", domain.items["agents"])
	}
}

// 推送被拒时不能宣布恢复：pending 还在库里，主节点还没有这条改动。
