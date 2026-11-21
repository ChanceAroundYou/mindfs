package controlsync

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
)

func newResolverFixture(t *testing.T) (*Store, *memDomain, *memDomain, *Resolver) {
	t.Helper()
	store := openTestStore(t)
	prefs := newMemDomain(DomainPreferences)
	users := newMemDomain(DomainUsers)
	registry, err := NewRegistry(prefs, users)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	capturer, err := NewCapturer(store, registry, "hasee")
	if err != nil {
		t.Fatalf("NewCapturer: %v", err)
	}
	resolver, err := NewResolver(store, registry, capturer)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	return store, prefs, users, resolver
}

// seedConflict 造一条冲突现场。删除的一侧没有载荷，hash 用祖先值（墓碑口径）。
func seedConflict(t *testing.T, store *Store, c Conflict) string {
	t.Helper()
	if c.LocalHash == "" {
		c.LocalHash = recordHash(t, c.Domain, c.Key, string(c.LocalPayload))
	}
	if c.RemoteHash == "" {
		c.RemoteHash = recordHash(t, c.Domain, c.Key, string(c.RemotePayload))
	}
	id, err := store.InsertConflict(context.Background(), c)
	if err != nil {
		t.Fatalf("InsertConflict: %v", err)
	}
	return id
}

func TestResolverListRedactsUsersPasswordHashes(t *testing.T) {
	store, _, _, resolver := newResolverFixture(t)
	ctx := t.Context()
	seedConflict(t, store, Conflict{
		ID:            "pull:preferences:agents",
		Domain:        DomainPreferences,
		Key:           "agents",
		LocalPayload:  json.RawMessage(`{"v":"local"}`),
		RemotePayload: json.RawMessage(`{"v":"remote"}`),
	})
	seedConflict(t, store, Conflict{
		ID:            "pull:users:u_abc",
		Domain:        DomainUsers,
		Key:           "u_abc",
		LocalPayload:  json.RawMessage(`{"username":"ann","password_hash":"$2a$10$secretA"}`),
		RemotePayload: json.RawMessage(`{"username":"ann","password_hash":"$2a$10$secretB"}`),
	})

	views, err := resolver.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(views) != 2 {
		t.Fatalf("冲突数 = %d，应为 2", len(views))
	}
	byDomain := map[string]ConflictView{}
	for _, v := range views {
		byDomain[v.Domain] = v
	}
	prefs := byDomain[DomainPreferences]
	if prefs.Redacted || string(prefs.Local) != `{"v":"local"}` || string(prefs.Remote) != `{"v":"remote"}` {
		t.Fatalf("preferences 视图 = %+v，应带两侧载荷", prefs)
	}
	users := byDomain[DomainUsers]
	if !users.Redacted {
		t.Fatalf("users 视图未标 redacted：%+v", users)
	}
	if len(users.Local) != 0 || len(users.Remote) != 0 {
		t.Fatalf("users 视图泄漏载荷：local=%s remote=%s", users.Local, users.Remote)
	}
	if users.LocalHash == "" || users.RemoteHash == "" {
		t.Fatalf("users 视图应保留 hash 供人区分：%+v", users)
	}
}

// 选 `local` 之后 pending 的共同祖先要重定到主节点当前值；这样再推一次就该
// 直接应用，而不是又判一次冲突。两端的每一步都断言，不只看出参。
func TestResolveLocalRebasesPendingThenPushConverges(t *testing.T) {
	store, prefs, _, resolver := newResolverFixture(t)
	ctx := t.Context()
	baseHash := recordHash(t, DomainPreferences, "agents", `{"v":"ancestor"}`)
	prefs.set("agents", `{"v":"local"}`)
	if err := store.PutPending(ctx, PendingChange{
		ChangeID: "ch-edge-1", Domain: DomainPreferences, Key: "agents",
		BaseHash: baseHash, Payload: json.RawMessage(`{"v":"local"}`),
	}); err != nil {
		t.Fatalf("PutPending: %v", err)
	}
	id := seedConflict(t, store, Conflict{
		ID:            "pull:preferences:agents",
		Domain:        DomainPreferences,
		Key:           "agents",
		LocalPayload:  json.RawMessage(`{"v":"local"}`),
		RemotePayload: json.RawMessage(`{"v":"remote"}`),
	})

	if err := resolver.Resolve(ctx, id, ConflictChoiceLocal, nil); err != nil {
		t.Fatalf("Resolve(local): %v", err)
	}
	if got := string(prefs.items["agents"]); got != `{"v":"local"}` {
		t.Fatalf("权威存储 = %s，应保留本地值", got)
	}
	pending, ok, err := store.PendingChange(ctx, DomainPreferences, "agents")
	if err != nil || !ok {
		t.Fatalf("pending 应保留：ok=%v err=%v", ok, err)
	}
	wantBase := recordHash(t, DomainPreferences, "agents", `{"v":"remote"}`)
	if pending.BaseHash != wantBase {
		t.Fatalf("pending.base_hash = %s，应重定到主节点当前值 %s", pending.BaseHash, wantBase)
	}
	if pending.ChangeID != "ch-edge-1" {
		t.Fatalf("change_id 变了 = %s，幂等身份应保留", pending.ChangeID)
	}

	// 主节点侧：这条记录现在就是 remote。
	pstore, pdomain, pcapturer, pusher := newPushFixture(t)
	pdomain.set("agents", `{"v":"remote"}`)
	if _, err := captureOnce(t, pcapturer, pdomain); err != nil {
		t.Fatalf("capture: %v", err)
	}
	batch, err := pusher.Push(ctx, PushRequest{
		InstanceID: "hasee",
		Changes: []PushChange{{
			ChangeID: pending.ChangeID, Domain: DomainPreferences, Key: "agents",
			BaseHash: pending.BaseHash, Payload: pending.Payload, Deleted: pending.Deleted,
		}},
	})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if len(batch.Results) != 1 || batch.Results[0].Status != PushApplied {
		t.Fatalf("推送结果 = %+v，应为 applied（重定过 base_hash 后不该再判冲突）", batch.Results)
	}
	if got := string(pdomain.items["agents"]); got != `{"v":"local"}` {
		t.Fatalf("主节点落地 = %s，应为本地值", got)
	}
	if n := insertConflictCount(t, pstore); n != 0 {
		t.Fatalf("主节点新增冲突 %d 条，应为 0", n)
	}
}

func TestResolvePrimaryAppliesRemoteAndClearsPending(t *testing.T) {
	store, prefs, _, resolver := newResolverFixture(t)
	ctx := t.Context()
	prefs.set("agents", `{"v":"local"}`)
	if err := store.PutPending(ctx, PendingChange{
		ChangeID: "ch-edge-2", Domain: DomainPreferences, Key: "agents",
		BaseHash: recordHash(t, DomainPreferences, "agents", `{"v":"ancestor"}`),
		Payload:  json.RawMessage(`{"v":"local"}`),
	}); err != nil {
		t.Fatalf("PutPending: %v", err)
	}
	id := seedConflict(t, store, Conflict{
		ID:            "pull:preferences:agents",
		Domain:        DomainPreferences,
		Key:           "agents",
		LocalPayload:  json.RawMessage(`{"v":"local"}`),
		RemotePayload: json.RawMessage(`{"v":"remote"}`),
	})

	if err := resolver.Resolve(ctx, id, ConflictChoicePrimary, nil); err != nil {
		t.Fatalf("Resolve(primary): %v", err)
	}
	if got := string(prefs.items["agents"]); got != `{"v":"remote"}` {
		t.Fatalf("权威存储 = %s，应换成主节点值", got)
	}
	if n := pendingCount(t, store); n != 0 {
		t.Fatalf("pending = %d，选主节点值后不该再推送", n)
	}
}

func TestResolvePayloadWritesMergedValueAndKeepsPush(t *testing.T) {
	store, prefs, _, resolver := newResolverFixture(t)
	ctx := t.Context()
	prefs.set("agents", `{"v":"local"}`)
	if err := store.PutPending(ctx, PendingChange{
		ChangeID: "ch-edge-3", Domain: DomainPreferences, Key: "agents",
		BaseHash: recordHash(t, DomainPreferences, "agents", `{"v":"ancestor"}`),
		Payload:  json.RawMessage(`{"v":"local"}`),
	}); err != nil {
		t.Fatalf("PutPending: %v", err)
	}
	id := seedConflict(t, store, Conflict{
		ID:            "pull:preferences:agents",
		Domain:        DomainPreferences,
		Key:           "agents",
		LocalPayload:  json.RawMessage(`{"v":"local"}`),
		RemotePayload: json.RawMessage(`{"v":"remote"}`),
	})

	merged := `{"v": "merged", "n": 1}`
	if err := resolver.Resolve(ctx, id, ConflictChoicePayload, []byte(merged)); err != nil {
		t.Fatalf("Resolve(payload): %v", err)
	}
	wantCanonical, err := CanonicalJSON(json.RawMessage(merged))
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	if got := string(prefs.items["agents"]); got != string(wantCanonical) {
		t.Fatalf("权威存储 = %s，应为规范化后的合并值 %s", got, wantCanonical)
	}
	pending, ok, err := store.PendingChange(ctx, DomainPreferences, "agents")
	if err != nil || !ok {
		t.Fatalf("pending 应保留：ok=%v err=%v", ok, err)
	}
	if string(pending.Payload) != string(wantCanonical) {
		t.Fatalf("pending.payload = %s，应为合并值", pending.Payload)
	}
	if pending.BaseHash != recordHash(t, DomainPreferences, "agents", `{"v":"remote"}`) {
		t.Fatalf("pending.base_hash = %s，应重定到主节点当前值", pending.BaseHash)
	}
}

// 本地这一侧是「删除」时，选 local 要落墓碑，而不是写一个空对象。
func TestResolveLocalDeletionWritesTombstone(t *testing.T) {
	store, prefs, _, resolver := newResolverFixture(t)
	ctx := t.Context()
	ancestor := recordHash(t, DomainPreferences, "agents", `{"v":"ancestor"}`)
	prefs.set("agents", `{"v":"ancestor"}`)
	if err := store.PutPending(ctx, PendingChange{
		ChangeID: "ch-edge-del", Domain: DomainPreferences, Key: "agents",
		BaseHash: ancestor, Deleted: true,
	}); err != nil {
		t.Fatalf("PutPending: %v", err)
	}
	id := seedConflict(t, store, Conflict{
		ID:            "pull:preferences:agents",
		Domain:        DomainPreferences,
		Key:           "agents",
		LocalDeleted:  true,
		LocalHash:     ancestor,
		RemotePayload: json.RawMessage(`{"v":"remote"}`),
	})

	if err := resolver.Resolve(ctx, id, ConflictChoiceLocal, nil); err != nil {
		t.Fatalf("Resolve(local): %v", err)
	}
	if _, ok := prefs.items["agents"]; ok {
		t.Fatalf("权威存储仍有这条记录，应被删掉")
	}
	pending, ok, err := store.PendingChange(ctx, DomainPreferences, "agents")
	if err != nil || !ok {
		t.Fatalf("pending 应保留：ok=%v err=%v", ok, err)
	}
	if !pending.Deleted {
		t.Fatalf("pending.deleted = false，应保留删除语义")
	}
	if pending.BaseHash != recordHash(t, DomainPreferences, "agents", `{"v":"remote"}`) {
		t.Fatalf("pending.base_hash = %s，应重定到主节点当前值", pending.BaseHash)
	}
}

func TestResolveRejectsBadRequests(t *testing.T) {
	store, _, _, resolver := newResolverFixture(t)
	ctx := t.Context()
	id := seedConflict(t, store, Conflict{
		ID:            "pull:preferences:agents",
		Domain:        DomainPreferences,
		Key:           "agents",
		LocalPayload:  json.RawMessage(`{"v":"local"}`),
		RemotePayload: json.RawMessage(`{"v":"remote"}`),
	})

	cases := []struct {
		name    string
		id      string
		choice  string
		payload []byte
		want    error
	}{
		{"未知 id", "pull:preferences:nope", ConflictChoiceLocal, nil, ErrConflictNotFound},
		{"非法选择", id, "merge", nil, ErrConflictChoice},
		{"payload 缺失", id, ConflictChoicePayload, nil, ErrConflictChoice},
		{"payload 是 null", id, ConflictChoicePayload, []byte("null"), ErrConflictChoice},
		{"payload 不是 json", id, ConflictChoicePayload, []byte("{oops"), ErrConflictChoice},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := resolver.Resolve(ctx, tc.id, tc.choice, tc.payload)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v，应为 %v", err, tc.want)
			}
		})
	}
	if n := insertConflictCount(t, store); n != 1 {
		t.Fatalf("非法请求不该消耗冲突卡：冲突数 = %d", n)
	}

	if err := resolver.Resolve(ctx, id, ConflictChoicePrimary, nil); err != nil {
		t.Fatalf("首次裁决: %v", err)
	}
	if err := resolver.Resolve(ctx, id, ConflictChoiceLocal, nil); !errors.Is(err, ErrConflictResolved) {
		t.Fatalf("重复裁决 err = %v，应为 ErrConflictResolved", err)
	}
}

// 两个界面同时点「用我的」和「用主节点的」：只能有一个生效，另一个必须被
// CAS 挡住，否则两端会各写一次、最后状态取决于谁后落盘。
func TestResolveConcurrentSingleWinner(t *testing.T) {
	store, prefs, _, resolver := newResolverFixture(t)
	ctx := t.Context()
	prefs.set("agents", `{"v":"local"}`)
	if err := store.PutPending(ctx, PendingChange{
		ChangeID: "ch-edge-4", Domain: DomainPreferences, Key: "agents",
		BaseHash: recordHash(t, DomainPreferences, "agents", `{"v":"ancestor"}`),
		Payload:  json.RawMessage(`{"v":"local"}`),
	}); err != nil {
		t.Fatalf("PutPending: %v", err)
	}
	id := seedConflict(t, store, Conflict{
		ID:            "pull:preferences:agents",
		Domain:        DomainPreferences,
		Key:           "agents",
		LocalPayload:  json.RawMessage(`{"v":"local"}`),
		RemotePayload: json.RawMessage(`{"v":"remote"}`),
	})

	choices := []string{ConflictChoiceLocal, ConflictChoicePrimary}
	errs := make([]error, len(choices))
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i, choice := range choices {
		wg.Add(1)
		go func(i int, choice string) {
			defer wg.Done()
			<-start
			errs[i] = resolver.Resolve(ctx, id, choice, nil)
		}(i, choice)
	}
	close(start)
	wg.Wait()

	wins := 0
	for i, err := range errs {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, ErrConflictResolved):
		default:
			t.Fatalf("choice %q 意外错误：%v", choices[i], err)
		}
	}
	if wins != 1 {
		t.Fatalf("成功裁决 %d 次，应恰好 1 次：errs=%v", wins, errs)
	}
	// 最终状态必须与唯一赢家的选择一致。
	want := `{"v":"remote"}`
	if errs[0] == nil {
		want = `{"v":"local"}`
	}
	if got := string(prefs.items["agents"]); got != want {
		t.Fatalf("权威存储 = %s，应与赢家选择一致 %s", got, want)
	}
}

// 从拉取冲突一路走到推送落地：reconcile 判冲突 → 裁决 → 推给主节点 → 两端一致。
func TestResolveAfterReconcileConflictConvergesAtPrimary(t *testing.T) {
	remote := remoteChange(t, 2, DomainPreferences, "agents", `{"v":"remote"}`, false)
	source := &fakePageSource{pageSize: 10, changes: []RemoteChange{remote}}
	store, domain, capturer, rec := newReconcileFixture(t, source)
	ctx := t.Context()
	seedReplica(t, store, 1, "agents", `{"v":"ancestor"}`)
	domain.set("agents", `{"v":"local"}`)
	if err := store.PutPending(ctx, PendingChange{
		ChangeID: "ch-edge-5", Domain: DomainPreferences, Key: "agents",
		BaseHash: recordHash(t, DomainPreferences, "agents", `{"v":"ancestor"}`),
		Payload:  json.RawMessage(`{"v":"local"}`),
	}); err != nil {
		t.Fatalf("PutPending: %v", err)
	}
	if res, err := rec.Reconcile(ctx); err != nil || res.Conflicts != 1 {
		t.Fatalf("Reconcile = %+v, %v，应判 1 条冲突", res, err)
	}

	registry, err := NewRegistry(domain)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	resolver, err := NewResolver(store, registry, capturer)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	views, err := resolver.List(ctx)
	if err != nil || len(views) != 1 {
		t.Fatalf("List = %d 条, %v，应 1 条", len(views), err)
	}
	if err := resolver.Resolve(ctx, views[0].ID, ConflictChoiceLocal, nil); err != nil {
		t.Fatalf("Resolve(local): %v", err)
	}

	pending, ok, err := store.PendingChange(ctx, DomainPreferences, "agents")
	if err != nil || !ok {
		t.Fatalf("pending 应保留：ok=%v err=%v", ok, err)
	}
	pstore, pdomain, pcapturer, pusher := newPushFixture(t)
	pdomain.set("agents", `{"v":"remote"}`)
	if _, err := captureOnce(t, pcapturer, pdomain); err != nil {
		t.Fatalf("capture: %v", err)
	}
	batch, err := pusher.Push(ctx, PushRequest{
		InstanceID: "hasee",
		Changes: []PushChange{{
			ChangeID: pending.ChangeID, Domain: pending.Domain, Key: pending.Key,
			BaseHash: pending.BaseHash, Payload: pending.Payload,
		}},
	})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if len(batch.Results) != 1 || batch.Results[0].Status != PushApplied {
		t.Fatalf("推送 = %+v，应为 applied", batch.Results)
	}
	if string(domain.items["agents"]) != string(pdomain.items["agents"]) {
		t.Fatalf("两端不一致：edge=%s primary=%s", domain.items["agents"], pdomain.items["agents"])
	}
	if n := insertConflictCount(t, pstore); n != 0 {
		t.Fatalf("主节点冲突数 = %d，应为 0", n)
	}
}

// 早期建的库没有这两列；CREATE TABLE IF NOT EXISTS 不会补，必须靠迁移补上，
// 否则升级后第一次读冲突卡就报 "no such column"。
func TestMigrateAddsConflictDeleteColumnsToExistingDB(t *testing.T) {
	store, prefs, _, resolver := newResolverFixture(t)
	ctx := t.Context()
	for _, col := range []string{"local_deleted", "remote_deleted"} {
		if _, err := store.db.ExecContext(ctx, "ALTER TABLE conflicts DROP COLUMN "+col); err != nil {
			t.Fatalf("模拟旧库（删 %s）: %v", col, err)
		}
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate 补列: %v", err)
	}
	ancestor := recordHash(t, DomainPreferences, "agents", `{"v":"ancestor"}`)
	prefs.set("agents", `{"v":"ancestor"}`)
	id := seedConflict(t, store, Conflict{
		ID:            "pull:preferences:agents",
		Domain:        DomainPreferences,
		Key:           "agents",
		LocalDeleted:  true,
		LocalHash:     ancestor,
		RemotePayload: json.RawMessage(`{"v":"remote"}`),
	})
	views, err := resolver.List(ctx)
	if err != nil || len(views) != 1 {
		t.Fatalf("List = %d 条, %v", len(views), err)
	}
	if !views[0].LocalDeleted || views[0].RemoteDeleted {
		t.Fatalf("删除标记没读回来：%+v", views[0])
	}
	if err := resolver.Resolve(ctx, id, ConflictChoiceLocal, nil); err != nil {
		t.Fatalf("Resolve(local): %v", err)
	}
	if _, ok := prefs.items["agents"]; ok {
		t.Fatalf("选 local 删除后权威存储仍留着记录")
	}
}

// 选了合并值就是「内容变了」：change_id 必须换。主节点按 applied_changes 去重，
// 沿用旧 id 会让这份合并值被当成已经应用过的变更而静默丢掉。
func TestResolvePayloadFromEdgeMintsNewChangeID(t *testing.T) {
	store, prefs, _, resolver := newResolverFixture(t)
	resolver.WithPendingIDs("edge-pc")
	ctx := t.Context()
	prefs.set("agents", `{"v":"local"}`)
	if err := store.PutPending(ctx, PendingChange{
		ChangeID: "edge-pc:preferences:agents:41", Domain: DomainPreferences, Key: "agents",
		BaseHash: recordHash(t, DomainPreferences, "agents", `{"v":"ancestor"}`),
		Payload:  json.RawMessage(`{"v":"local"}`),
	}); err != nil {
		t.Fatalf("PutPending: %v", err)
	}
	id := seedConflict(t, store, Conflict{
		ID:            "pull:preferences:agents",
		Domain:        DomainPreferences,
		Key:           "agents",
		LocalPayload:  json.RawMessage(`{"v":"local"}`),
		RemotePayload: json.RawMessage(`{"v":"remote"}`),
	})
	if err := resolver.Resolve(ctx, id, ConflictChoicePayload, []byte(`{"v":"merged"}`)); err != nil {
		t.Fatalf("Resolve(payload): %v", err)
	}
	pending, ok, err := store.PendingChange(ctx, DomainPreferences, "agents")
	if err != nil || !ok {
		t.Fatalf("pending 应保留：ok=%v err=%v", ok, err)
	}
	if pending.ChangeID == "edge-pc:preferences:agents:41" || pending.ChangeID == "" {
		t.Fatalf("change_id = %q，合并值必须换新身份", pending.ChangeID)
	}
	if !strings.HasPrefix(pending.ChangeID, "edge-pc:preferences:agents:") {
		t.Fatalf("change_id = %q，应带本机身份前缀", pending.ChangeID)
	}
}

// 选 local 推的还是本地那一版，身份不该动 —— 换了号等于告诉主节点这是新变更，
// 幂等保护就失效了。
func TestResolveLocalFromEdgeKeepsChangeID(t *testing.T) {
	store, prefs, _, resolver := newResolverFixture(t)
	resolver.WithPendingIDs("edge-pc")
	ctx := t.Context()
	prefs.set("agents", `{"v":"local"}`)
	if err := store.PutPending(ctx, PendingChange{
		ChangeID: "edge-pc:preferences:agents:41", Domain: DomainPreferences, Key: "agents",
		BaseHash: recordHash(t, DomainPreferences, "agents", `{"v":"ancestor"}`),
		Payload:  json.RawMessage(`{"v":"local"}`),
	}); err != nil {
		t.Fatalf("PutPending: %v", err)
	}
	id := seedConflict(t, store, Conflict{
		ID:            "pull:preferences:agents",
		Domain:        DomainPreferences,
		Key:           "agents",
		LocalPayload:  json.RawMessage(`{"v":"local"}`),
		RemotePayload: json.RawMessage(`{"v":"remote"}`),
	})
	if err := resolver.Resolve(ctx, id, ConflictChoiceLocal, nil); err != nil {
		t.Fatalf("Resolve(local): %v", err)
	}
	pending, ok, err := store.PendingChange(ctx, DomainPreferences, "agents")
	if err != nil || !ok {
		t.Fatalf("pending 应保留：ok=%v err=%v", ok, err)
	}
	if pending.ChangeID != "edge-pc:preferences:agents:41" {
		t.Fatalf("change_id = %q，选 local 时不该换号", pending.ChangeID)
	}
}
