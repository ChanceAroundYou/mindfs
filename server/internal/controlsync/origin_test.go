package controlsync

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// 主节点身份进入 head：边缘靠它识别「对面还是不是原来那台主节点」。
// spec §6 要求 head 至少带 instance_id / build_version / schema_version。
func TestChangeFeedHeadCarriesIdentity(t *testing.T) {
	store := openTestStore(t)
	feed, err := NewChangeFeed(store)
	if err != nil {
		t.Fatalf("NewChangeFeed: %v", err)
	}
	feed.SetIdentity(SyncIdentity{InstanceID: "home", BuildVersion: "v9.9.9"})

	head, err := feed.Head(t.Context())
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if head.HeadRev != 0 {
		t.Fatalf("空库 head_rev = %d，应为 0", head.HeadRev)
	}
	if head.InstanceID != "home" || head.BuildVersion != "v9.9.9" {
		t.Fatalf("身份 = %q/%q", head.InstanceID, head.BuildVersion)
	}
	if head.SchemaVersion != SyncSchemaVersion {
		t.Fatalf("schema_version = %d，应为 %d", head.SchemaVersion, SyncSchemaVersion)
	}

	// 没配身份的主节点（老版本或没填 instance_id）不编造一个：如实留空，
	// 边缘据此跳过身份比对，而不是拿一个假 id 把真正的替换盖住。
	bare, err := NewChangeFeed(store)
	if err != nil {
		t.Fatalf("NewChangeFeed: %v", err)
	}
	head, err = bare.Head(t.Context())
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if head.InstanceID != "" || head.BuildVersion != "" {
		t.Fatalf("未设身份却报出 %q/%q", head.InstanceID, head.BuildVersion)
	}
}

// 第一次追平把主节点身份落到 state：之后的替换检测才有基准。
func TestReplicatorAdoptsPrimaryIdentityOnFirstSync(t *testing.T) {
	primary := &fakePrimary{
		instanceID:    "primary-01",
		buildVersion:  "v1.2.3",
		schemaVersion: SyncSchemaVersion,
		changes: []RemoteChange{
			remoteChange(t, 1, DomainPreferences, "agents", `{"v":1}`, false),
		},
	}
	store, rep := newReplicatorFixture(t, primary)
	ctx := t.Context()
	if _, err := rep.SyncOnce(ctx); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	st, err := store.LoadState(ctx)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if st.PrimaryInstanceID != "primary-01" {
		t.Fatalf("primary_instance_id = %q，应为 primary-01", st.PrimaryInstanceID)
	}
	if st.BaseRev != 1 {
		t.Fatalf("base_rev = %d，应为 1", st.BaseRev)
	}
}

// 主节点被重建/替换：身份变了就必须停下，不能把一台空库的内容当成「主节点删光了」。
func TestReplicatorRefusesReplacedPrimary(t *testing.T) {
	primary := &fakePrimary{
		instanceID:    "primary-02",
		schemaVersion: SyncSchemaVersion,
		changes: []RemoteChange{
			remoteChange(t, 1, DomainPreferences, "agents", `{"v":2}`, false),
		},
	}
	store, rep := newReplicatorFixture(t, primary)
	ctx := t.Context()
	if err := store.SetPrimaryInstanceID(ctx, "primary-01"); err != nil {
		t.Fatalf("SetPrimaryInstanceID: %v", err)
	}

	_, err := rep.SyncOnce(ctx)
	if !errors.Is(err, ErrReplicaOriginMismatch) {
		t.Fatalf("err = %v，应为 ErrReplicaOriginMismatch", err)
	}
	if len(primary.requests) != 0 {
		t.Fatalf("身份不符时仍去拉了变更：%v", primary.requests)
	}
	assertReplicaUntouched(t, store)
	if st, err := store.LoadState(ctx); err != nil {
		t.Fatalf("LoadState: %v", err)
	} else if st.PrimaryInstanceID != "primary-01" {
		t.Fatalf("身份不符时把记录改成了 %q", st.PrimaryInstanceID)
	}
}

// schema 版本不同意味着两端的表/载荷语义已经对不上，合并只会写坏数据。
func TestReplicatorRefusesSchemaMismatch(t *testing.T) {
	primary := &fakePrimary{
		instanceID:    "primary-01",
		schemaVersion: SyncSchemaVersion + 1,
		changes: []RemoteChange{
			remoteChange(t, 1, DomainPreferences, "agents", `{"v":2}`, false),
		},
	}
	store, rep := newReplicatorFixture(t, primary)
	_, err := rep.SyncOnce(t.Context())
	if !errors.Is(err, ErrReplicaSchemaMismatch) {
		t.Fatalf("err = %v，应为 ErrReplicaSchemaMismatch", err)
	}
	if len(primary.requests) != 0 {
		t.Fatalf("schema 不符时仍去拉了变更：%v", primary.requests)
	}
	assertReplicaUntouched(t, store)
}

// 老主节点两份新字段都没有：只能放行并如实留空，不能因为「无法验证」就永久离线。
func TestReplicatorAcceptsPrimaryWithoutIdentity(t *testing.T) {
	primary := &fakePrimary{changes: []RemoteChange{
		remoteChange(t, 1, DomainPreferences, "agents", `{"v":1}`, false),
	}}
	store, rep := newReplicatorFixture(t, primary)
	ctx := t.Context()
	if _, err := rep.SyncOnce(ctx); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	st, err := store.LoadState(ctx)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if st.PrimaryInstanceID != "" {
		t.Fatalf("无身份的主节点却记下了 %q", st.PrimaryInstanceID)
	}
	if st.BaseRev != 1 {
		t.Fatalf("base_rev = %d，应为 1", st.BaseRev)
	}
}

func assertReplicaUntouched(t *testing.T, store *Store) {
	t.Helper()
	var records int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM records`).Scan(&records); err != nil {
		t.Fatalf("数副本: %v", err)
	}
	if records != 0 {
		t.Fatalf("拒绝合并却写了 %d 条副本", records)
	}
	st, err := store.LoadState(context.Background())
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if st.BaseRev != 0 {
		t.Fatalf("拒绝合并却把水位推到 %d", st.BaseRev)
	}
}

// 旧库没有 primary_instance_id 列：迁移要幂等补上，且不动已有内容。
func TestMigrateAddsPrimaryInstanceColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control_sync.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	if _, err := raw.Exec(`
CREATE TABLE state (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	instance_id TEXT NOT NULL DEFAULT '',
	mode TEXT NOT NULL DEFAULT '',
	base_rev INTEGER NOT NULL DEFAULT 0,
	last_primary_at TEXT NOT NULL DEFAULT '',
	last_error TEXT NOT NULL DEFAULT ''
);
INSERT INTO state(id, instance_id, mode, base_rev) VALUES(1, 'edge-01', 'offline-local', 7);`); err != nil {
		t.Fatalf("造旧库: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := t.Context()
	for i := 0; i < 2; i++ {
		if err := store.Migrate(ctx); err != nil {
			t.Fatalf("第 %d 次 Migrate: %v", i+1, err)
		}
	}
	st, err := store.LoadState(ctx)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if st.InstanceID != "edge-01" || st.Mode != string(ModeOfflineLocal) || st.BaseRev != 7 {
		t.Fatalf("补列动到了已有内容：%+v", st)
	}
	if st.PrimaryInstanceID != "" {
		t.Fatalf("旧行 primary_instance_id = %q，应为空", st.PrimaryInstanceID)
	}
	if err := store.SetPrimaryInstanceID(ctx, "primary-01"); err != nil {
		t.Fatalf("SetPrimaryInstanceID: %v", err)
	}
	if st, err = store.LoadState(ctx); err != nil {
		t.Fatalf("LoadState: %v", err)
	} else if st.PrimaryInstanceID != "primary-01" {
		t.Fatalf("补列后写不进去：%q", st.PrimaryInstanceID)
	}
	// 追平锁也是同一次迁移补的列，且旧行默认未锁。
	if st.BlockedReason != "" {
		t.Fatalf("旧行 blocked_reason = %q，应为空", st.BlockedReason)
	}
	if err := store.SetBlockedReason(ctx, "blocked for test"); err != nil {
		t.Fatalf("SetBlockedReason: %v", err)
	}
	if st, err = store.LoadState(ctx); err != nil {
		t.Fatalf("LoadState: %v", err)
	} else if st.BlockedReason != "blocked for test" {
		t.Fatalf("补列后写不进 blocked_reason：%q", st.BlockedReason)
	}
}

// 主节点真的被换掉了：拒绝合并只是第一步，还得有一条有记录的出路。
// 人工确认后丢掉按旧主节点建的副本（保留 pending 本地改动），从头追平新主节点。
func TestAdoptPrimaryRebasesReplicaOntoNewPrimary(t *testing.T) {
	primary := &fakePrimary{
		instanceID:    "primary-01",
		schemaVersion: SyncSchemaVersion,
		changes: []RemoteChange{
			remoteChange(t, 1, DomainPreferences, "agents", `{"v":1}`, false),
			remoteChange(t, 2, DomainPreferences, "theme", `{"v":"dark"}`, false),
		},
	}
	store, rep := newReplicatorFixture(t, primary)
	ctx := t.Context()
	if _, err := rep.SyncOnce(ctx); err != nil {
		t.Fatalf("首次 SyncOnce: %v", err)
	}
	// 边缘离线期间攒下的本地改动：采纳新主节点时必须留着交给冲突比对。
	if err := store.PutPending(ctx, PendingChange{
		ChangeID: "edge-test:preferences:agents:1", Domain: DomainPreferences,
		Key: "agents", Payload: []byte(`{"v":9}`), CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("PutPending: %v", err)
	}
	if _, err := store.InsertConflict(ctx, Conflict{
		ID: "pull:preferences:theme", Domain: DomainPreferences, Key: "theme",
		LocalHash: "h1", RemoteHash: "h2", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("InsertConflict: %v", err)
	}

	// 主节点换机：新身份、变更流从头编号。
	primary.mu.Lock()
	primary.instanceID = "primary-02"
	primary.changes = []RemoteChange{
		remoteChange(t, 1, DomainUsers, "alice", `{"role":"admin"}`, false),
	}
	primary.mu.Unlock()
	if _, err := rep.SyncOnce(ctx); !errors.Is(err, ErrReplicaOriginMismatch) {
		t.Fatalf("换主节点后 err = %v，应拒绝合并", err)
	}

	mc, err := NewModeController(ctx, store, &fakeProber{errs: []error{nil}})
	if err != nil {
		t.Fatalf("NewModeController: %v", err)
	}
	if _, err := mc.AdoptPrimary(ctx); err != nil {
		t.Fatalf("AdoptPrimary: %v", err)
	}
	st, err := store.LoadState(ctx)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if st.BaseRev != 0 || st.PrimaryInstanceID != "" || st.BlockedReason != "" {
		t.Fatalf("采纳后 state 没清干净：%+v", st)
	}
	var records, conflicts int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM records`).Scan(&records); err != nil {
		t.Fatalf("数副本: %v", err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM conflicts`).Scan(&conflicts); err != nil {
		t.Fatalf("数冲突: %v", err)
	}
	if records != 0 || conflicts != 0 {
		t.Fatalf("采纳后旧副本 records=%d conflicts=%d，应都清空", records, conflicts)
	}
	if n, err := store.CountPending(ctx); err != nil || n != 1 {
		t.Fatalf("pending = %d, %v，本地改动不能跟着副本一起丢", n, err)
	}

	// 重新追平：采纳新身份，从 rev 1 开始把新主节点的内容拉进来。
	if _, err := rep.SyncOnce(ctx); err != nil {
		t.Fatalf("采纳后 SyncOnce: %v", err)
	}
	st, err = store.LoadState(ctx)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if st.PrimaryInstanceID != "primary-02" || st.BaseRev != 1 {
		t.Fatalf("采纳后 state = %+v，应记下新主节点并追到 rev 1", st)
	}
	var key string
	if err := store.db.QueryRow(`SELECT key FROM records WHERE domain = ?`, DomainUsers).Scan(&key); err != nil {
		t.Fatalf("新主节点的记录没进副本: %v", err)
	}
	if key != "alice" {
		t.Fatalf("副本里的 key = %q，应为 alice", key)
	}
}

// 身份与 schema 都在 head 的 JSON 里：字段名是两端的线上契约，改名等于断连。
func TestHeadInfoJSONShape(t *testing.T) {
	raw, err := json.Marshal(HeadInfo{
		HeadRev: 5, InstanceID: "home", BuildVersion: "v1", SchemaVersion: 2,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"head_rev", "instance_id", "build_version", "schema_version"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("head 缺字段 %q：%s", key, raw)
		}
	}
}
