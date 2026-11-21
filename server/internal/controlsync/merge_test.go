package controlsync

import (
	"context"
	"encoding/json"
	"testing"
)

func newPushFixture(t *testing.T) (*Store, *memDomain, *Capturer, *Pusher) {
	t.Helper()
	store := openTestStore(t)
	domain := newMemDomain(DomainPreferences)
	registry, err := NewRegistry(domain)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	capturer, err := NewCapturer(store, registry, "home")
	if err != nil {
		t.Fatalf("NewCapturer: %v", err)
	}
	pusher, err := NewPusher(store, registry, capturer)
	if err != nil {
		t.Fatalf("NewPusher: %v", err)
	}
	return store, domain, capturer, pusher
}

func recordHash(t *testing.T, domain, key, payload string) string {
	t.Helper()
	hash, err := HashRecord(Record{Domain: domain, Key: key, Payload: json.RawMessage(payload)})
	if err != nil {
		t.Fatalf("HashRecord: %v", err)
	}
	return hash
}

func pushChange(changeID, domain, key, baseHash, payload string, deleted bool) PushChange {
	ch := PushChange{ChangeID: changeID, Domain: domain, Key: key, BaseHash: baseHash, Deleted: deleted}
	if payload != "" {
		ch.Payload = json.RawMessage(payload)
	}
	return ch
}

func countRows(t *testing.T, store *Store, table string) int {
	t.Helper()
	var n int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// base_hash 命中主节点当前值：直接应用，分配新 rev 并写变更流。
func TestPushAppliesWhenBaseHashMatches(t *testing.T) {
	store, domain, capturer, pusher := newPushFixture(t)
	ctx := t.Context()
	domain.set("agents", `{"v":1}`)
	if _, err := captureOnce(t, capturer, domain); err != nil {
		t.Fatalf("seed capture: %v", err)
	}
	headBefore, _ := store.HeadRev(ctx)

	batch, err := pusher.Push(ctx, PushRequest{InstanceID: "hasee", Changes: []PushChange{
		pushChange("ch-1", DomainPreferences, "agents", recordHash(t, DomainPreferences, "agents", `{"v":1}`), `{"v":2}`, false),
	}})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if len(batch.Results) != 1 || batch.Results[0].Status != PushApplied {
		t.Fatalf("结果 = %+v，应为 applied", batch.Results)
	}
	if string(domain.items["agents"]) != `{"v":2}` {
		t.Fatalf("权威存储没被更新：%s", domain.items["agents"])
	}
	rec, ok, err := store.GetRecord(ctx, DomainPreferences, "agents")
	if err != nil || !ok {
		t.Fatalf("读副本记录: ok=%v err=%v", ok, err)
	}
	if rec.Hash != recordHash(t, DomainPreferences, "agents", `{"v":2}`) || rec.Rev <= headBefore {
		t.Fatalf("records 没跟上：%+v（headBefore=%d）", rec, headBefore)
	}
	if batch.HeadRev != rec.Rev {
		t.Fatalf("head_rev = %d，应为新 rev %d", batch.HeadRev, rec.Rev)
	}
}

// 同一 change_id 重放只应用一次，rev 不再增长。
func TestPushIsIdempotentOnChangeID(t *testing.T) {
	store, domain, capturer, pusher := newPushFixture(t)
	ctx := t.Context()
	domain.set("agents", `{"v":1}`)
	if _, err := captureOnce(t, capturer, domain); err != nil {
		t.Fatalf("seed capture: %v", err)
	}
	change := pushChange("ch-1", DomainPreferences, "agents",
		recordHash(t, DomainPreferences, "agents", `{"v":1}`), `{"v":2}`, false)
	first, err := pusher.Push(ctx, PushRequest{InstanceID: "hasee", Changes: []PushChange{change}})
	if err != nil {
		t.Fatalf("首次 Push: %v", err)
	}
	second, err := pusher.Push(ctx, PushRequest{InstanceID: "hasee", Changes: []PushChange{change}})
	if err != nil {
		t.Fatalf("重放 Push: %v", err)
	}
	if second.Results[0].Status != PushApplied {
		t.Fatalf("重放状态 = %s，应仍是 applied", second.Results[0].Status)
	}
	if second.HeadRev != first.HeadRev {
		t.Fatalf("重放推进了 rev：%d → %d", first.HeadRev, second.HeadRev)
	}
	if n := countRows(t, store, "changes"); n != 2 {
		t.Fatalf("changes 行数 = %d，应为 2（种子的 1 条 + 推送的 1 条）", n)
	}
}

// 两侧改过同一记录：报 conflict，主节点记录与原值都不动，冲突现场落库。
func TestPushReportsConflictWhenBothSidesChanged(t *testing.T) {
	store, domain, capturer, pusher := newPushFixture(t)
	ctx := t.Context()
	domain.set("agents", `{"v":1}`)
	if _, err := captureOnce(t, capturer, domain); err != nil {
		t.Fatalf("seed capture: %v", err)
	}
	// 主节点本地又改成 v2（边缘看不见这一步）
	domain.set("agents", `{"v":2}`)
	if _, err := captureOnce(t, capturer, domain); err != nil {
		t.Fatalf("second capture: %v", err)
	}
	before, _, _ := store.GetRecord(ctx, DomainPreferences, "agents")
	changesBefore := countRows(t, store, "changes")

	batch, err := pusher.Push(ctx, PushRequest{InstanceID: "hasee", Changes: []PushChange{
		pushChange("ch-c", DomainPreferences, "agents",
			recordHash(t, DomainPreferences, "agents", `{"v":1}`), `{"v":3}`, false),
	}})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if batch.Results[0].Status != PushConflict {
		t.Fatalf("状态 = %s，应为 conflict", batch.Results[0].Status)
	}
	if batch.Results[0].ConflictID == "" {
		t.Fatalf("冲突没有 id，前端无法裁决")
	}
	if string(domain.items["agents"]) != `{"v":2}` {
		t.Fatalf("冲突时主节点记录被覆盖成：%s", domain.items["agents"])
	}
	after, _, _ := store.GetRecord(ctx, DomainPreferences, "agents")
	if after.Hash != before.Hash || after.Rev != before.Rev {
		t.Fatalf("冲突时副本记录变了：%+v → %+v", before, after)
	}
	if n := countRows(t, store, "changes"); n != changesBefore {
		t.Fatalf("冲突写进了变更流：%d → %d", changesBefore, n)
	}
	if n := countRows(t, store, "conflicts"); n != 1 {
		t.Fatalf("conflicts 行数 = %d，应为 1", n)
	}
}

// 主节点当前值已经等于推送载荷：收敛，不重复写变更流。
func TestPushConvergedWhenHashesEqual(t *testing.T) {
	store, domain, capturer, pusher := newPushFixture(t)
	ctx := t.Context()
	domain.set("agents", `{"v":1}`)
	if _, err := captureOnce(t, capturer, domain); err != nil {
		t.Fatalf("seed capture: %v", err)
	}
	headBefore, _ := store.HeadRev(ctx)
	batch, err := pusher.Push(ctx, PushRequest{InstanceID: "hasee", Changes: []PushChange{
		pushChange("ch-eq", DomainPreferences, "agents", "sha256:stale", `{"v":1}`, false),
	}})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if batch.Results[0].Status != PushApplied {
		t.Fatalf("状态 = %s，应收敛为 applied", batch.Results[0].Status)
	}
	if batch.HeadRev != headBefore {
		t.Fatalf("收敛也推进了 rev：%d → %d", headBefore, batch.HeadRev)
	}
	if n := countRows(t, store, "applied_changes"); n != 1 {
		t.Fatalf("收敛没记 applied_changes，重放会重复判定")
	}
}

// 删除：命中 base_hash 落墓碑并保留最后一次内容 hash；重放一次不新增 rev；
// 已被删掉的记录再删一次是幂等成功。
func TestPushDeleteIsIdempotent(t *testing.T) {
	store, domain, capturer, pusher := newPushFixture(t)
	ctx := t.Context()
	domain.set("agents", `{"v":1}`)
	if _, err := captureOnce(t, capturer, domain); err != nil {
		t.Fatalf("seed capture: %v", err)
	}
	baseHash := recordHash(t, DomainPreferences, "agents", `{"v":1}`)
	del := pushChange("ch-del", DomainPreferences, "agents", baseHash, "", true)
	batch, err := pusher.Push(ctx, PushRequest{InstanceID: "hasee", Changes: []PushChange{del}})
	if err != nil {
		t.Fatalf("Push delete: %v", err)
	}
	if batch.Results[0].Status != PushApplied {
		t.Fatalf("删除状态 = %s", batch.Results[0].Status)
	}
	if _, ok := domain.items["agents"]; ok {
		t.Fatalf("权威存储没删除")
	}
	rec, ok, err := store.GetRecord(ctx, DomainPreferences, "agents")
	if err != nil || !ok || !rec.Deleted || rec.Hash != baseHash {
		t.Fatalf("墓碑 = %+v（ok=%v err=%v），应 deleted 且保留旧 hash", rec, ok, err)
	}
	revAfter := rec.Rev
	again, err := pusher.Push(ctx, PushRequest{InstanceID: "hasee", Changes: []PushChange{del}})
	if err != nil || again.Results[0].Status != PushApplied || again.HeadRev != revAfter {
		t.Fatalf("删除重放：%+v（err=%v）rev 应停在 %d", again, err, revAfter)
	}
	// 另一条删除一个从来不存在（也没有 base）的记录：幂等成功。
	third, err := pusher.Push(ctx, PushRequest{InstanceID: "hasee", Changes: []PushChange{
		pushChange("ch-del-missing", DomainPreferences, "ghost", "", "", true),
	}})
	if err != nil || third.Results[0].Status != PushApplied {
		t.Fatalf("删除不存在记录：%+v（err=%v）", third, err)
	}
}

// 重放的判定必须先看 applied_changes：主节点在两次推送之间又改过这条记录时，
// 只靠 hash 比对会把一条早已应用过的变更误判成冲突。
func TestPushReplayAfterPrimaryEditStaysApplied(t *testing.T) {
	_, domain, capturer, pusher := newPushFixture(t)
	ctx := t.Context()
	domain.set("agents", `{"v":1}`)
	if _, err := captureOnce(t, capturer, domain); err != nil {
		t.Fatalf("seed capture: %v", err)
	}
	change := pushChange("ch-r", DomainPreferences, "agents",
		recordHash(t, DomainPreferences, "agents", `{"v":1}`), `{"v":2}`, false)
	if _, err := pusher.Push(ctx, PushRequest{InstanceID: "hasee", Changes: []PushChange{change}}); err != nil {
		t.Fatalf("首次 Push: %v", err)
	}
	// 主节点随后又改了一次（边缘不知情）。
	domain.set("agents", `{"v":3}`)
	if _, err := captureOnce(t, capturer, domain); err != nil {
		t.Fatalf("second capture: %v", err)
	}
	batch, err := pusher.Push(ctx, PushRequest{InstanceID: "hasee", Changes: []PushChange{change}})
	if err != nil {
		t.Fatalf("重放 Push: %v", err)
	}
	if batch.Results[0].Status != PushApplied {
		t.Fatalf("已应用变更重放成了 %s，应仍是 applied", batch.Results[0].Status)
	}
	if string(domain.items["agents"]) != `{"v":3}` {
		t.Fatalf("重放把主节点后续改动覆盖成：%s", domain.items["agents"])
	}
}

// 一批里混着 applied / conflict：每条各自落结论，好的那条不受坏的影响。
func TestPushMixedBatchKeepsPerChangeOutcomes(t *testing.T) {
	_, domain, capturer, pusher := newPushFixture(t)
	ctx := t.Context()
	domain.set("agents", `{"v":1}`)
	domain.set("other", `{"v":1}`)
	if _, err := captureOnce(t, capturer, domain); err != nil {
		t.Fatalf("seed capture: %v", err)
	}
	domain.set("other", `{"v":9}`) // 主节点本地改了 other
	if _, err := captureOnce(t, capturer, domain); err != nil {
		t.Fatalf("second capture: %v", err)
	}
	batch, err := pusher.Push(ctx, PushRequest{InstanceID: "hasee", Changes: []PushChange{
		pushChange("ch-a", DomainPreferences, "agents",
			recordHash(t, DomainPreferences, "agents", `{"v":1}`), `{"v":2}`, false),
		pushChange("ch-b", DomainPreferences, "other",
			recordHash(t, DomainPreferences, "other", `{"v":1}`), `{"v":8}`, false),
	}})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if len(batch.Results) != 2 || batch.Results[0].Status != PushApplied || batch.Results[1].Status != PushConflict {
		t.Fatalf("结果 = %+v，应为 [applied, conflict]", batch.Results)
	}
	if string(domain.items["agents"]) != `{"v":2}` || string(domain.items["other"]) != `{"v":9}` {
		t.Fatalf("混合批的落库不对：agents=%s other=%s", domain.items["agents"], domain.items["other"])
	}
}

// 不认识的域一律 rejected，别把拼错的域名静默落一条没人读的记录。
func TestPushRejectsUnknownDomain(t *testing.T) {
	_, _, _, pusher := newPushFixture(t)
	batch, err := pusher.Push(t.Context(), PushRequest{InstanceID: "hasee", Changes: []PushChange{
		pushChange("ch-x", "nonsense", "k", "", `{"v":1}`, false),
	}})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if batch.Results[0].Status != PushRejected || batch.Results[0].Reason == "" {
		t.Fatalf("结果 = %+v，应为 rejected 且说明原因", batch.Results[0])
	}
}

// captureOnce 用一次快照 diff 把主节点当前值写进索引（模拟本地写入后的采集）。
func captureOnce(t *testing.T, capturer *Capturer, domain *memDomain) (int, error) {
	t.Helper()
	return capturer.CaptureDomain(context.Background(), domain.Name())
}
