package controlsync

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeReplicaSource 同时充当追平源（SyncOnce）与合并拉取源（FetchPage）：
// 生产里两者都是 *Replicator，测试用内存分页代替 HTTP。
type fakeReplicaSource struct {
	mu     sync.Mutex
	pages  map[int64][]RemoteChange
	syncs  int
	synced int
	err    error
}

func (f *fakeReplicaSource) FetchPage(ctx context.Context, since int64) ([]RemoteChange, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return f.pages[since], nil
}

func (f *fakeReplicaSource) SyncOnce(ctx context.Context) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return 0, f.err
	}
	f.syncs++
	return f.synced, nil
}

func (f *fakeReplicaSource) setPage(since int64, changes ...RemoteChange) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pages == nil {
		f.pages = make(map[int64][]RemoteChange)
	}
	f.pages[since] = changes
}

// seedReplicaRecord 把一条记录直接写进副本表：不产生 change 行，也不推进 base_rev。
//
// 生产里副本只由 Replicator 写（它走 ApplyRemoteChanges，会一起推进水位）；这里要的是
// 「交接那一刻副本里已经有什么」，用 store.WriteRecord 会顺带伪造一段边缘永不该有的
// 变更流，用 ApplyRemoteChanges 又会用掉合并阶段要用的水位。
func seedReplicaRecord(t *testing.T, store *Store, rec Record) {
	t.Helper()
	var payload any
	if len(rec.Payload) > 0 {
		payload = []byte(rec.Payload)
	}
	if _, err := store.db.ExecContext(t.Context(), `
INSERT INTO records(domain, key, rev, hash, payload, deleted, updated_at, origin)
VALUES(?, ?, 0, ?, ?, ?, ?, '')
ON CONFLICT(domain, key) DO UPDATE SET
	hash = excluded.hash,
	payload = excluded.payload,
	deleted = excluded.deleted,
	updated_at = excluded.updated_at`,
		rec.Domain, rec.Key, rec.Hash, payload, boolInt(rec.Deleted),
		store.now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("seed replica record: %v", err)
	}
}

func edgeConfig() Config {
	return Config{
		Enabled:    true,
		Role:       RoleEdge,
		InstanceID: "hasee",
		PrimaryURL: "https://home.example:7331",
		Token:      "t",
		// 用例里每拍都要求真的跑一次：默认间隔是给生产节流用的，放进单测只会
		// 让第二拍起被静默跳过。
		ProbeInterval:   time.Nanosecond,
		PushInterval:    time.Nanosecond,
		ReplicaInterval: time.Nanosecond,
	}
}

func newRuntimeFixture(t *testing.T, cfg Config) (*Runtime, *Store, *memDomain, *fakeReplicaSource, *fakePushSender) {
	t.Helper()
	store := openTestStore(t)
	domain := newMemDomain(DomainPreferences)
	registry, err := NewRegistry(domain)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	capturer, err := NewCapturer(store, registry, "hasee")
	if err != nil {
		t.Fatalf("NewCapturer: %v", err)
	}
	source := &fakeReplicaSource{}
	sender := &fakePushSender{handler: func(req PushRequest) (PushBatch, error) {
		return appliedResult(req, "sha256:applied"), nil
	}}
	rt, err := NewRuntime(t.Context(), cfg, Deps{
		Store:    store,
		Registry: registry,
		Capturer: capturer,
		Source:   source,
		Sender:   sender,
	})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	return rt, store, domain, source, sender
}

// 边缘缺任何一项都起不来：同步通道要把整份控制面（含口令 hash）来回搬，
// 少一项就不是「配错了」，是根本不安全。
func TestNewRuntimeRejectsIncompleteEdgeConfig(t *testing.T) {
	base := edgeConfig()
	cases := []struct {
		name string
		mut  func(*Config)
		want string
	}{
		{"实例 id", func(c *Config) { c.InstanceID = "" }, "instance_id required"},
		{"主节点地址", func(c *Config) { c.PrimaryURL = "" }, "primary_url required"},
		{"明文地址", func(c *Config) { c.PrimaryURL = "http://home:7331" }, "must be https"},
		{"token", func(c *Config) { c.Token = "" }, "token required"},
		{"未知角色", func(c *Config) { c.Role = "worker" }, "unknown role"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			tc.mut(&cfg)
			// 依赖照常注入，确保失败原因只可能来自配置校验。
			store := openTestStore(t)
			domain := newMemDomain(DomainPreferences)
			registry, _ := NewRegistry(domain)
			capturer, _ := NewCapturer(store, registry, "hasee")
			_, err := NewRuntime(t.Context(), cfg, Deps{
				Store: store, Registry: registry, Capturer: capturer,
				Source: &fakeReplicaSource{}, Sender: &fakePushSender{},
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v，应含 %q", err, tc.want)
			}
		})
	}
}

// 交接：副本整体写进权威存储；本地已有待回传改动的 key 跳过，绝不拿副本旧值盖掉用户编辑。
func TestRuntimeEdgeOnlineDoesNotCaptureLocalWrites(t *testing.T) {
	rt, store, domain, _, _ := newRuntimeFixture(t, edgeConfig())
	ctx := t.Context()
	if got := rt.Mode().Mode(); got != ModeOnline {
		t.Fatalf("初始模式 = %s，应为 online", got)
	}
	domain.set("agents", `{"v":"stale"}`)
	if _, err := rt.Capturer().CaptureAll(ctx); err != nil {
		t.Fatalf("CaptureAll: %v", err)
	}
	if n, _ := store.CountPending(ctx); n != 0 {
		t.Fatalf("在线时采出了待回传：%d", n)
	}
	if got := countRows(t, store, "changes"); got != 0 {
		t.Fatalf("changes = %d，边缘永远不写变更流", got)
	}
	if got := countRows(t, store, "records"); got != 0 {
		t.Fatalf("records = %d，在线采集把本地过期值写进了副本", got)
	}

	// 掉线之后同一个改动就该被采到 —— 而且只落 pending。
	for i := 0; i < DegradeAfterFailure; i++ {
		rt.Mode().Observe(false)
	}
	if _, err := rt.Capturer().CaptureAll(ctx); err != nil {
		t.Fatalf("离线采集: %v", err)
	}
	if n, _ := store.CountPending(ctx); n != 1 {
		t.Fatalf("离线改动没落 pending：%d", n)
	}
	if got := countRows(t, store, "changes"); got != 0 {
		t.Fatalf("changes = %d，边缘的本地改动不进变更流", got)
	}
}

// 在线 → 离线 → 恢复的整条走法：离线写落 pending，恢复后推送落地，全程不写 changes。
func TestRuntimeTickWalksOnlineOfflineAndBack(t *testing.T) {
	rt, store, domain, source, sender := newRuntimeFixture(t, edgeConfig())
	ctx := t.Context()
	mode := rt.Mode()

	if got := mode.Mode(); got != ModeOnline {
		t.Fatalf("初始模式 = %s，应为 online", got)
	}
	rt.tick(ctx)
	if source.syncs != 1 {
		t.Fatalf("在线拍里追平次数 = %d，应为 1", source.syncs)
	}

	// 主节点不可达：连续 3 次失败降级；降级这一拍完成交接。
	for i := 0; i < DegradeAfterFailure; i++ {
		mode.Observe(false)
	}
	if got := mode.Mode(); got != ModeOfflineLocal {
		t.Fatalf("模式 = %s，应为 offline-local", got)
	}
	// 离线期间本机看到的应当是主节点最后给的那一版：副本里有什么，交接就该落什么。
	replica := testRecord(t, DomainPreferences, "agents", `{"v":"primary"}`)
	seedReplicaRecord(t, store, replica)
	rt.tick(ctx) // 交接把副本写进权威存储
	if string(domain.items["agents"]) != `{"v":"primary"}` {
		t.Fatalf("交接后权威存储 = %s", domain.items["agents"])
	}
	// 之后再掉线也不会重复交接：期间主节点没变，副本也没变。
	rt.tick(ctx)
	domain.set("agents", `{"v":"local"}`)
	if _, err := rt.Capturer().CaptureAll(ctx); err != nil {
		t.Fatalf("离线采集: %v", err)
	}
	pending, ok, err := store.PendingChange(ctx, DomainPreferences, "agents")
	if err != nil || !ok {
		t.Fatalf("离线改动没落 pending：ok=%v err=%v", ok, err)
	}
	if pending.BaseHash != replica.Hash {
		t.Fatalf("base_hash = %q，应为交接后的副本值 %q", pending.BaseHash, replica.Hash)
	}

	// 主节点恢复：合并页里那条记录还是边缘看到的祖先值 → 保留 pending，等推送。
	source.setPage(1, RemoteChange{
		Rev: 1, Domain: DomainPreferences, Key: "agents",
		Hash: replica.Hash, Payload: replica.Payload,
	})
	for i := 0; i < UpgradeAfterSuccess; i++ {
		mode.Observe(true)
	}
	if got := mode.Mode(); got != ModeReconciling {
		t.Fatalf("模式 = %s，应为 reconciling", got)
	}
	rt.tick(ctx)

	if got := mode.Mode(); got != ModeOnline {
		t.Fatalf("模式 = %s，恢复完成后应回 online", got)
	}
	if n, _ := store.CountPending(ctx); n != 0 {
		t.Fatalf("pending = %d，推送成功应清空", n)
	}
	if len(sender.requests) == 0 {
		t.Fatalf("恢复后没有发生推送")
	}
	if got := countRows(t, store, "changes"); got != 0 {
		t.Fatalf("changes = %d，边缘永远不写变更流", got)
	}
}

// 两侧都改：停在 conflict-review 冻结，本地值不被覆盖；裁决与回传都结束后才回 online。
func TestRuntimeTickFreezesOnConflictUntilResolved(t *testing.T) {
	rt, store, domain, source, _ := newRuntimeFixture(t, edgeConfig())
	ctx := t.Context()
	mode := rt.Mode()
	mode.Observe(false)
	mode.Observe(false)
	mode.Observe(false)
	rt.tick(ctx)

	domain.set("agents", `{"v":"local"}`)
	if _, err := rt.Capturer().CaptureAll(ctx); err != nil {
		t.Fatalf("离线采集: %v", err)
	}
	remote := testRecord(t, DomainPreferences, "agents", `{"v":"primary"}`)
	source.setPage(1, RemoteChange{
		Rev: 1, Domain: DomainPreferences, Key: "agents",
		Hash: remote.Hash, Payload: remote.Payload,
	})
	mode.Observe(true)
	mode.Observe(true)
	rt.tick(ctx)

	if got := mode.Mode(); got != ModeConflictReview {
		t.Fatalf("模式 = %s，应为 conflict-review", got)
	}
	if string(domain.items["agents"]) != `{"v":"local"}` {
		t.Fatalf("冲突时本地值被覆盖成：%s", domain.items["agents"])
	}
	if n, _ := store.CountPending(ctx); n != 1 {
		t.Fatalf("冲突时 pending 必须保留：%d", n)
	}

	// 用户裁决 + 推送结束后，闸门才放行。
	if _, err := store.SupersedeConflicts(ctx, DomainPreferences, "agents", "resolved"); err != nil {
		t.Fatalf("SupersedeConflicts: %v", err)
	}
	pending, _, _ := store.PendingChange(ctx, DomainPreferences, "agents")
	if _, err := store.DeletePendingIfChange(ctx, DomainPreferences, "agents", pending.ChangeID); err != nil {
		t.Fatalf("DeletePendingIfChange: %v", err)
	}
	rt.tick(ctx)
	if got := mode.Mode(); got != ModeOnline {
		t.Fatalf("模式 = %s，裁决且回传清空后应回 online", got)
	}
}

// 交接失败不能假装完成：模式留在本地，下一拍重试，本地数据一个字都不许被改坏。
// 交接不是一次性的：回到在线后再掉线，必须按**那时**的副本重新交接。
// 只做一次的话，第二次离线看到的还是第一次交接时的旧数据。
func TestRuntimeStaysReconcilingWhilePendingRemains(t *testing.T) {
	rt, store, domain, _, sender := newRuntimeFixture(t, edgeConfig())
	ctx := t.Context()
	mode := rt.Mode()
	sender.setHandler(func(req PushRequest) (PushBatch, error) {
		batch := PushBatch{Results: make([]PushResult, 0, len(req.Changes))}
		for _, ch := range req.Changes {
			batch.Results = append(batch.Results, PushResult{
				ChangeID: ch.ChangeID, Status: PushRejected, Reason: "bad payload",
			})
		}
		return batch, nil
	})

	for i := 0; i < DegradeAfterFailure; i++ {
		mode.Observe(false)
	}
	rt.tick(ctx)
	domain.set("agents", `{"v":"local"}`)
	if _, err := rt.Capturer().CaptureAll(ctx); err != nil {
		t.Fatalf("离线采集: %v", err)
	}
	for i := 0; i < UpgradeAfterSuccess; i++ {
		mode.Observe(true)
	}
	rt.tick(ctx)
	if got := mode.Mode(); got != ModeReconciling {
		t.Fatalf("模式 = %s，回传没成功就不该宣布恢复", got)
	}
	if n, _ := store.CountPending(ctx); n != 1 {
		t.Fatalf("pending = %d，被拒的改动必须留着重试", n)
	}

	sender.setHandler(func(req PushRequest) (PushBatch, error) {
		return appliedResult(req, "sha256:applied"), nil
	})
	rt.tick(ctx)
	if got := mode.Mode(); got != ModeOnline {
		t.Fatalf("模式 = %s，回传清空后应回 online", got)
	}
	if n, _ := store.CountPending(ctx); n != 0 {
		t.Fatalf("pending = %d，应清空", n)
	}
}

// 主节点角色没有状态机与回传循环：它只是变更流的源。
func TestRuntimePrimaryRoleHasNoEdgeLoops(t *testing.T) {
	rt, store, domain, source, _ := newRuntimeFixture(t, Config{Enabled: true, Role: RolePrimary})
	ctx := t.Context()
	if rt.Mode() != nil {
		t.Fatalf("主节点不该有模式状态机")
	}
	rt.tick(ctx)
	if source.syncs != 0 {
		t.Fatalf("主节点跑了追平：%d", source.syncs)
	}
	domain.set("agents", `{"v":1}`)
	if _, err := rt.Capturer().CaptureAll(ctx); err != nil {
		t.Fatalf("CaptureAll: %v", err)
	}
	if got := countRows(t, store, "changes"); got != 1 {
		t.Fatalf("changes = %d，主节点的本地改动应进变更流", got)
	}
	if n, _ := store.CountPending(ctx); n != 0 {
		t.Fatalf("pending = %d，主节点不写待回传", n)
	}
}
