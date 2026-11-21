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
	// 也不能凭空给它造一条墓碑：其他节点从没听过这条记录，凭空广播一次删除
	// 会让它们的副本凭空多出一条「已删除」。
	if _, ok, err := store.GetRecord(ctx, DomainPreferences, "ghost"); err != nil || ok {
		t.Fatalf("删除不存在的记录留下了副本行：ok=%v err=%v", ok, err)
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

// 回执必须带上主节点处理完之后的记录：边缘要拿它建冲突卡（冲突卡要显示两侧内容，
// 只回 hash 不够）与给「推送期间又改过」的 pending 重定祖先。
func TestPushResultsCarryPrimaryRecord(t *testing.T) {
	_, domain, capturer, pusher := newPushFixture(t)
	ctx := t.Context()
	domain.set("agents", `{"v":1}`)
	if _, err := captureOnce(t, capturer, domain); err != nil {
		t.Fatalf("seed capture: %v", err)
	}
	domain.set("agents", `{"v":2}`)
	if _, err := captureOnce(t, capturer, domain); err != nil {
		t.Fatalf("second capture: %v", err)
	}

	batch, err := pusher.Push(ctx, PushRequest{InstanceID: "hasee", Changes: []PushChange{
		pushChange("ch-c", DomainPreferences, "agents",
			recordHash(t, DomainPreferences, "agents", `{"v":1}`), `{"v":3}`, false),
	}})
	if err != nil {
		t.Fatalf("冲突 Push: %v", err)
	}
	conflict := batch.Results[0]
	if conflict.Status != PushConflict {
		t.Fatalf("状态 = %s，应为 conflict", conflict.Status)
	}
	if !conflict.Exists || conflict.Deleted {
		t.Fatalf("冲突回执的记录状态 = exists:%v deleted:%v，主节点明明有一条未删的值",
			conflict.Exists, conflict.Deleted)
	}
	if conflict.Hash != recordHash(t, DomainPreferences, "agents", `{"v":2}`) {
		t.Fatalf("冲突回执 hash = %q，应为主节点当前值", conflict.Hash)
	}
	if string(conflict.Payload) != `{"v":2}` {
		t.Fatalf("冲突回执载荷 = %s，冲突卡要靠它显示主节点那一侧", conflict.Payload)
	}

	applied, err := pusher.Push(ctx, PushRequest{InstanceID: "hasee", Changes: []PushChange{
		pushChange("ch-ok", DomainPreferences, "agents",
			recordHash(t, DomainPreferences, "agents", `{"v":2}`), `{"v":4}`, false),
	}})
	if err != nil {
		t.Fatalf("应用 Push: %v", err)
	}
	done := applied.Results[0]
	if done.Status != PushApplied {
		t.Fatalf("状态 = %s，应已应用", done.Status)
	}
	if !done.Exists || done.Hash != recordHash(t, DomainPreferences, "agents", `{"v":4}`) {
		t.Fatalf("应用回执 = %+v，应带落地后的记录", done)
	}
	if len(done.Payload) != 0 {
		t.Fatalf("应用回执不该回传载荷（users 域里那是口令 hash）：%s", done.Payload)
	}
}

// 删除落地后回执要说清「主节点现在是墓碑」，hash 保留最后一次内容 ——
// 边缘靠它区分「主节点删了」与「主节点改过又删」。
func TestPushDeleteResultCarriesTombstone(t *testing.T) {
	_, domain, capturer, pusher := newPushFixture(t)
	ctx := t.Context()
	domain.set("agents", `{"v":1}`)
	if _, err := captureOnce(t, capturer, domain); err != nil {
		t.Fatalf("seed capture: %v", err)
	}
	baseHash := recordHash(t, DomainPreferences, "agents", `{"v":1}`)
	batch, err := pusher.Push(ctx, PushRequest{InstanceID: "hasee", Changes: []PushChange{
		pushChange("ch-del", DomainPreferences, "agents", baseHash, "", true),
	}})
	if err != nil {
		t.Fatalf("Push delete: %v", err)
	}
	res := batch.Results[0]
	if res.Status != PushApplied || !res.Exists || !res.Deleted {
		t.Fatalf("回执 = %+v，应为 applied 的墓碑", res)
	}
	if res.Hash != baseHash {
		t.Fatalf("墓碑 hash = %q，应保留最后一次内容 %q", res.Hash, baseHash)
	}
}

// 记录在主节点落定之后，之前那张冲突卡就是旧现场：不关掉会永远挂在列表里。
func TestPushSupersedesStaleConflictWhenRecordSettles(t *testing.T) {
	store, domain, capturer, pusher := newPushFixture(t)
	ctx := t.Context()
	domain.set("agents", `{"v":1}`)
	if _, err := captureOnce(t, capturer, domain); err != nil {
		t.Fatalf("seed capture: %v", err)
	}
	domain.set("agents", `{"v":2}`)
	if _, err := captureOnce(t, capturer, domain); err != nil {
		t.Fatalf("second capture: %v", err)
	}
	if _, err := pusher.Push(ctx, PushRequest{InstanceID: "hasee", Changes: []PushChange{
		pushChange("ch-c", DomainPreferences, "agents",
			recordHash(t, DomainPreferences, "agents", `{"v":1}`), `{"v":3}`, false),
	}}); err != nil {
		t.Fatalf("冲突 Push: %v", err)
	}
	open, err := store.ListOpenConflicts(ctx)
	if err != nil || len(open) != 1 {
		t.Fatalf("裁决前未裁决冲突 = %d, %v，应为 1", len(open), err)
	}

	// 边缘裁决后基于主节点现值重新推上来并落地。
	if _, err := pusher.Push(ctx, PushRequest{InstanceID: "hasee", Changes: []PushChange{
		pushChange("ch-merged", DomainPreferences, "agents",
			recordHash(t, DomainPreferences, "agents", `{"v":2}`), `{"v":"merged"}`, false),
	}}); err != nil {
		t.Fatalf("裁决后 Push: %v", err)
	}
	open, err = store.ListOpenConflicts(ctx)
	if err != nil || len(open) != 0 {
		t.Fatalf("落定后未裁决冲突 = %d, %v，应全部关掉", len(open), err)
	}
	var resolution string
	if err := store.db.QueryRow(
		`SELECT resolution FROM conflicts WHERE domain = ? AND key = ?`,
		DomainPreferences, "agents").Scan(&resolution); err != nil {
		t.Fatalf("读裁决记录: %v", err)
	}
	if resolution != "superseded" {
		t.Fatalf("resolution = %q，应记录为被取代", resolution)
	}
}

// captureOnce 用一次快照 diff 把主节点当前值写进索引（模拟本地写入后的采集）。
func captureOnce(t *testing.T, capturer *Capturer, domain *memDomain) (int, error) {
	t.Helper()
	return capturer.CaptureDomain(context.Background(), domain.Name())
}
