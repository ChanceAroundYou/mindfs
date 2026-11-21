package controlsync

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
)

// fakePageSource 按 rev 升序喂页；calls 记录每次请求的 since，便于断言续传位置。
type fakePageSource struct {
	mu         sync.Mutex
	changes    []RemoteChange
	pageSize   int
	calls      []int64
	failOnCall int
	// ignoreSince 模拟不守规矩/有竞态的服务端：请求 since 之后仍把旧 rev 混在页里。
	ignoreSince bool
}

func (f *fakePageSource) FetchPage(_ context.Context, since int64) ([]RemoteChange, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, since)
	if f.failOnCall > 0 && len(f.calls) == f.failOnCall {
		return nil, errors.New("主节点又断了")
	}
	out := []RemoteChange{}
	for _, ch := range f.changes {
		if ch.Rev < since && !f.ignoreSince {
			continue
		}
		out = append(out, ch)
		if len(out) >= f.pageSize {
			break
		}
	}
	return out, nil
}

// flakyDomain 在指定 key 的 Apply 上失败，用来验证「应用失败不能推进水位」。
type flakyDomain struct {
	*memDomain
	failKey string
}

func (d *flakyDomain) Apply(ctx context.Context, rec Record) error {
	if rec.Key == d.failKey {
		return errors.New("磁盘满了")
	}
	return d.memDomain.Apply(ctx, rec)
}

func newReconcileFixture(t *testing.T, source PageSource) (*Store, *memDomain, *Capturer, *Reconciler) {
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
	rec, err := NewReconciler(store, registry, source, capturer)
	if err != nil {
		t.Fatalf("NewReconciler: %v", err)
	}
	return store, domain, capturer, rec
}

// seedReplica 把「边缘离线前看到的远端内容」写进副本，作为 pending 的祖先来源。
func seedReplica(t *testing.T, store *Store, rev int64, key, payload string) {
	t.Helper()
	if err := store.ApplyRemoteChanges(context.Background(), []RemoteChange{
		remoteChange(t, rev, DomainPreferences, key, payload, false),
	}); err != nil {
		t.Fatalf("seed replica: %v", err)
	}
}

func pendingCount(t *testing.T, store *Store) int {
	t.Helper()
	n, err := store.CountPending(context.Background())
	if err != nil {
		t.Fatalf("CountPending: %v", err)
	}
	return n
}

func baseRev(t *testing.T, store *Store) int64 {
	t.Helper()
	rev, err := store.BaseRev(context.Background())
	if err != nil {
		t.Fatalf("BaseRev: %v", err)
	}
	return rev
}

func insertConflictCount(t *testing.T, store *Store) int {
	t.Helper()
	return countRows(t, store, "conflicts")
}

// 分支一：本地没有 pending —— 远端值直接落地。
func TestReconcileAppliesWhenNoPending(t *testing.T) {
	source := &fakePageSource{pageSize: 10, changes: []RemoteChange{
		remoteChange(t, 1, DomainPreferences, "agents", `{"v":"remote"}`, false),
	}}
	store, domain, _, rec := newReconcileFixture(t, source)

	res, err := rec.Reconcile(t.Context())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.Applied != 1 || res.Conflicts != 0 {
		t.Fatalf("结果 = %+v", res)
	}
	if string(domain.items["agents"]) != `{"v":"remote"}` {
		t.Fatalf("权威存储 = %s", domain.items["agents"])
	}
	row, ok, err := store.GetRecord(t.Context(), DomainPreferences, "agents")
	if err != nil || !ok || row.Rev != 1 {
		t.Fatalf("副本 = %+v（ok=%v err=%v）", row, ok, err)
	}
	if got := baseRev(t, store); got != 1 {
		t.Fatalf("水位 = %d，应为 1", got)
	}
}

// 分支二：远端的这一版就是边缘离线前看到的祖先值 —— 保留 pending，别把本地改动冲掉。
func TestReconcileKeepsPendingWhenRemoteIsAncestor(t *testing.T) {
	source := &fakePageSource{pageSize: 10, changes: []RemoteChange{
		remoteChange(t, 2, DomainPreferences, "agents", `{"v":"ancestor"}`, false),
	}}
	store, domain, _, rec := newReconcileFixture(t, source)
	seedReplica(t, store, 1, "agents", `{"v":"ancestor"}`)
	domain.set("agents", `{"v":"local"}`)
	if err := store.PutPending(t.Context(), PendingChange{
		ChangeID: "ch-local", Domain: DomainPreferences, Key: "agents", BaseHash: recordHash(t, DomainPreferences, "agents", `{"v":"ancestor"}`),
		Payload: json.RawMessage(`{"v":"local"}`),
	}); err != nil {
		t.Fatalf("PutPending: %v", err)
	}

	res, err := rec.Reconcile(t.Context())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.Kept != 1 || res.Applied != 0 || res.Conflicts != 0 {
		t.Fatalf("结果 = %+v", res)
	}
	if string(domain.items["agents"]) != `{"v":"local"}` {
		t.Fatalf("本地改动被冲掉了：%s", domain.items["agents"])
	}
	if pendingCount(t, store) != 1 {
		t.Fatalf("pending 被清掉，本地改动会丢")
	}
	if got := baseRev(t, store); got != 2 {
		t.Fatalf("水位 = %d，应为 2（远端这一页仍然要带过）", got)
	}
}

// 分支三：远端值与本地改动是同一个内容 —— 收敛，清 pending。
func TestReconcileConvergesWhenBothSidesEqual(t *testing.T) {
	source := &fakePageSource{pageSize: 10, changes: []RemoteChange{
		remoteChange(t, 2, DomainPreferences, "agents", `{"v":"same"}`, false),
	}}
	store, domain, _, rec := newReconcileFixture(t, source)
	seedReplica(t, store, 1, "agents", `{"v":"ancestor"}`)
	domain.set("agents", `{"v":"same"}`)
	if err := store.PutPending(t.Context(), PendingChange{
		ChangeID: "ch-local", Domain: DomainPreferences, Key: "agents", BaseHash: recordHash(t, DomainPreferences, "agents", `{"v":"ancestor"}`),
		Payload: json.RawMessage(`{"v":"same"}`),
	}); err != nil {
		t.Fatalf("PutPending: %v", err)
	}

	res, err := rec.Reconcile(t.Context())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.Converged != 1 || res.Conflicts != 0 {
		t.Fatalf("结果 = %+v", res)
	}
	if pendingCount(t, store) != 0 {
		t.Fatalf("已收敛但 pending 没清")
	}
	row, _, _ := store.GetRecord(t.Context(), DomainPreferences, "agents")
	if row.Rev != 2 || row.Hash != recordHash(t, DomainPreferences, "agents", `{"v":"same"}`) {
		t.Fatalf("副本没跟到远端：%+v", row)
	}
}

// 分支四：两侧都改过 —— 冲突落库，两边都保留，pending 不清。
func TestReconcileRecordsConflictAndKeepsPending(t *testing.T) {
	remote := remoteChange(t, 2, DomainPreferences, "agents", `{"v":"remote"}`, false)
	source := &fakePageSource{pageSize: 10, changes: []RemoteChange{remote}}
	store, domain, _, rec := newReconcileFixture(t, source)
	seedReplica(t, store, 1, "agents", `{"v":"ancestor"}`)
	domain.set("agents", `{"v":"local"}`)
	if err := store.PutPending(t.Context(), PendingChange{
		ChangeID: "ch-local", Domain: DomainPreferences, Key: "agents", BaseHash: recordHash(t, DomainPreferences, "agents", `{"v":"ancestor"}`),
		Payload: json.RawMessage(`{"v":"local"}`),
	}); err != nil {
		t.Fatalf("PutPending: %v", err)
	}

	res, err := rec.Reconcile(t.Context())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.Conflicts != 1 || res.Applied != 0 {
		t.Fatalf("结果 = %+v", res)
	}
	if string(domain.items["agents"]) != `{"v":"local"}` {
		t.Fatalf("冲突时本地被覆盖成：%s", domain.items["agents"])
	}
	if pendingCount(t, store) != 1 {
		t.Fatalf("冲突时 pending 被清掉，本地改动会丢")
	}
	var localPayload, remotePayload []byte
	if err := store.db.QueryRow(`
SELECT local_payload, remote_payload FROM conflicts WHERE domain = ? AND key = ?`,
		DomainPreferences, "agents").Scan(&localPayload, &remotePayload); err != nil {
		t.Fatalf("读冲突: %v", err)
	}
	if string(localPayload) != `{"v":"local"}` || string(remotePayload) != `{"v":"remote"}` {
		t.Fatalf("冲突两边没保留：local=%s remote=%s", localPayload, remotePayload)
	}
	if got := baseRev(t, store); got != 2 {
		t.Fatalf("水位 = %d，应为 2（冲突也要带过，否则永远卡在同一页）", got)
	}
}

// 删除分支：两边都删 = 收敛；远端还是祖先 = 保留本地删除；远端改过 = 冲突。
func TestReconcileDeleteBranches(t *testing.T) {
	t.Run("两边都删", func(t *testing.T) {
		source := &fakePageSource{pageSize: 10, changes: []RemoteChange{
			remoteChange(t, 2, DomainPreferences, "agents", `{"v":"ancestor"}`, true),
		}}
		store, _, _, rec := newReconcileFixture(t, source)
		seedReplica(t, store, 1, "agents", `{"v":"ancestor"}`)
		putDeletePending(t, store, `{"v":"ancestor"}`)
		res, err := rec.Reconcile(t.Context())
		if err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		if res.Converged != 1 || pendingCount(t, store) != 0 {
			t.Fatalf("结果 = %+v，pending = %d", res, pendingCount(t, store))
		}
	})
	t.Run("远端还是祖先", func(t *testing.T) {
		source := &fakePageSource{pageSize: 10, changes: []RemoteChange{
			remoteChange(t, 2, DomainPreferences, "agents", `{"v":"ancestor"}`, false),
		}}
		store, _, _, rec := newReconcileFixture(t, source)
		seedReplica(t, store, 1, "agents", `{"v":"ancestor"}`)
		putDeletePending(t, store, `{"v":"ancestor"}`)
		res, err := rec.Reconcile(t.Context())
		if err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		if res.Kept != 1 || pendingCount(t, store) != 1 {
			t.Fatalf("结果 = %+v，pending = %d", res, pendingCount(t, store))
		}
	})
	t.Run("远端改过", func(t *testing.T) {
		source := &fakePageSource{pageSize: 10, changes: []RemoteChange{
			remoteChange(t, 2, DomainPreferences, "agents", `{"v":"remote"}`, false),
		}}
		store, domain, _, rec := newReconcileFixture(t, source)
		seedReplica(t, store, 1, "agents", `{"v":"ancestor"}`)
		domain.remove("agents")
		putDeletePending(t, store, `{"v":"ancestor"}`)
		res, err := rec.Reconcile(t.Context())
		if err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		if res.Conflicts != 1 || pendingCount(t, store) != 1 {
			t.Fatalf("结果 = %+v，pending = %d", res, pendingCount(t, store))
		}
		if _, ok := domain.items["agents"]; ok {
			t.Fatalf("冲突时把远端值写进了权威存储")
		}
	})
}

func putDeletePending(t *testing.T, store *Store, lastContent string) {
	t.Helper()
	if err := store.PutPending(t.Context(), PendingChange{
		ChangeID: "ch-del", Domain: DomainPreferences, Key: "agents",
		BaseHash: recordHash(t, DomainPreferences, "agents", lastContent), Deleted: true,
	}); err != nil {
		t.Fatalf("PutPending: %v", err)
	}
}

// 分页：一页提交完再取下一页，since 逐页前进。
func TestReconcileAdvancesPageByPage(t *testing.T) {
	changes := []RemoteChange{
		remoteChange(t, 1, DomainPreferences, "a", `{"v":1}`, false),
		remoteChange(t, 2, DomainPreferences, "b", `{"v":1}`, false),
		remoteChange(t, 3, DomainPreferences, "c", `{"v":1}`, false),
		remoteChange(t, 4, DomainPreferences, "d", `{"v":1}`, false),
		remoteChange(t, 5, DomainPreferences, "e", `{"v":1}`, false),
	}
	source := &fakePageSource{pageSize: 2, changes: changes}
	store, domain, _, rec := newReconcileFixture(t, source)

	res, err := rec.Reconcile(t.Context())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.Applied != 5 {
		t.Fatalf("结果 = %+v", res)
	}
	want := []int64{1, 3, 5, 6}
	if len(source.calls) != len(want) {
		t.Fatalf("拉取 since 序列 = %v，应为 %v", source.calls, want)
	}
	for i := range want {
		if source.calls[i] != want[i] {
			t.Fatalf("拉取 since 序列 = %v，应为 %v", source.calls, want)
		}
	}
	if len(domain.items) != 5 {
		t.Fatalf("落地 %d 条，应为 5", len(domain.items))
	}
	if got := baseRev(t, store); got != 5 {
		t.Fatalf("水位 = %d，应为 5", got)
	}
}

// 中途断掉：已提交的页生效，下一轮从水位续传，不重放也不跳过。
func TestReconcileResumesFromWatermarkAfterFailure(t *testing.T) {
	source := &fakePageSource{pageSize: 1, changes: []RemoteChange{
		remoteChange(t, 1, DomainPreferences, "a", `{"v":1}`, false),
		remoteChange(t, 2, DomainPreferences, "b", `{"v":1}`, false),
		remoteChange(t, 3, DomainPreferences, "c", `{"v":1}`, false),
	}, failOnCall: 3}
	store, domain, _, rec := newReconcileFixture(t, source)

	res, err := rec.Reconcile(t.Context())
	if err == nil {
		t.Fatalf("应当把主节点断线报出来")
	}
	if res.Applied != 2 {
		t.Fatalf("断线前已应用 %d 条，应为 2", res.Applied)
	}
	if got := baseRev(t, store); got != 2 {
		t.Fatalf("水位 = %d，应为 2", got)
	}

	source.mu.Lock()
	source.failOnCall = 0
	source.mu.Unlock()
	res, err = rec.Reconcile(t.Context())
	if err != nil {
		t.Fatalf("恢复后 Reconcile: %v", err)
	}
	if res.Applied != 1 {
		t.Fatalf("恢复后应用 %d 条，应为 1（只补第 3 条）", res.Applied)
	}
	if len(domain.items) != 3 || baseRev(t, store) != 3 {
		t.Fatalf("最终 items=%d 水位=%d", len(domain.items), baseRev(t, store))
	}
}

// 应用远端记录失败时，水位必须停在这一页之前 —— 否则那条内容再也不会被拉到。
func TestReconcileKeepsWatermarkWhenApplyFails(t *testing.T) {
	source := &fakePageSource{pageSize: 10, changes: []RemoteChange{
		remoteChange(t, 1, DomainPreferences, "a", `{"v":1}`, false),
		remoteChange(t, 2, DomainPreferences, "boom", `{"v":1}`, false),
	}}
	store := openTestStore(t)
	domain := &flakyDomain{memDomain: newMemDomain(DomainPreferences), failKey: "boom"}
	registry, err := NewRegistry(domain)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	capturer, err := NewCapturer(store, registry, "hasee")
	if err != nil {
		t.Fatalf("NewCapturer: %v", err)
	}
	rec, err := NewReconciler(store, registry, source, capturer)
	if err != nil {
		t.Fatalf("NewReconciler: %v", err)
	}

	if _, err := rec.Reconcile(t.Context()); err == nil {
		t.Fatalf("应当报应用失败")
	}
	if got := baseRev(t, store); got != 0 {
		t.Fatalf("应用失败却把水位推到 %d", got)
	}
}

// 服务端重发旧页时跳过，不重复计数也不倒退水位。
func TestReconcileSkipsReplayedOldRevs(t *testing.T) {
	source := &fakePageSource{pageSize: 10, ignoreSince: true, changes: []RemoteChange{
		remoteChange(t, 1, DomainPreferences, "a", `{"v":1}`, false),
		remoteChange(t, 2, DomainPreferences, "b", `{"v":1}`, false),
	}}
	store, _, _, rec := newReconcileFixture(t, source)
	seedReplica(t, store, 1, "a", `{"v":1}`)

	res, err := rec.Reconcile(t.Context())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	// 第二轮整页都是旧 rev（服务端没守 since），跳过计数会继续加，但循环必须停下：
	// 水位不动、应用条数只有新增的那一条。
	if res.Applied != 1 || res.Skipped < 1 {
		t.Fatalf("结果 = %+v", res)
	}
	if len(source.calls) > 3 {
		t.Fatalf("旧页在原地空转了 %d 次", len(source.calls))
	}
	if got := baseRev(t, store); got != 2 {
		t.Fatalf("水位 = %d，应为 2", got)
	}
}

// 删除的 pending 没有载荷：冲突里「本地 hash」用祖先值，别拿空载荷算出一个假 hash。
func TestReconcileDeleteConflictCarriesAncestorHash(t *testing.T) {
	source := &fakePageSource{pageSize: 10, changes: []RemoteChange{
		remoteChange(t, 2, DomainPreferences, "agents", `{"v":"remote"}`, false),
	}}
	store, _, _, rec := newReconcileFixture(t, source)
	seedReplica(t, store, 1, "agents", `{"v":"ancestor"}`)
	putDeletePending(t, store, `{"v":"ancestor"}`)
	if _, err := rec.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var localHash string
	if err := store.db.QueryRow(`SELECT local_hash FROM conflicts WHERE id = ?`,
		PullConflictID(DomainPreferences, "agents")).Scan(&localHash); err != nil {
		t.Fatalf("读冲突: %v", err)
	}
	if localHash != recordHash(t, DomainPreferences, "agents", `{"v":"ancestor"}`) {
		t.Fatalf("本地 hash = %q，应为祖先值 hash", localHash)
	}
	if insertConflictCount(t, store) != 1 {
		t.Fatalf("冲突行数 = %d", insertConflictCount(t, store))
	}
}
