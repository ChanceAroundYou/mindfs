package controlsync

import (
	"bytes"
	"encoding/json"
	"testing"
)

func rec(domain, key, payload string) Record {
	return Record{Domain: domain, Key: key, Payload: json.RawMessage(payload)}
}

func hashOf(t *testing.T, r Record) string {
	t.Helper()
	h, err := HashRecord(r)
	if err != nil {
		t.Fatalf("HashRecord: %v", err)
	}
	return h
}

// 变更流必须自带载荷：只存 hash 的话，回放一条旧 change 只能去 records 取
// 「当前值」，副本会追到一个从未存在过的中间状态。
func TestListChangesSinceServesRecordedPayloads(t *testing.T) {
	store := openTestStore(t)
	ctx := t.Context()

	first := rec(DomainPreferences, "agents", `{"v":1}`)
	first.Hash = hashOf(t, first)
	revs, err := store.WriteRecords(ctx, []Record{first})
	if err != nil {
		t.Fatalf("WriteRecords: %v", err)
	}
	r1 := revs[0]
	second := rec(DomainPreferences, "agents", `{"v":2}`)
	second.Hash = hashOf(t, second)
	if _, err := store.WriteRecords(ctx, []Record{second}); err != nil {
		t.Fatalf("WriteRecords: %v", err)
	}
	del := Record{Domain: DomainPreferences, Key: "agents", Deleted: true, Hash: hashOf(t, second)}
	if _, err := store.WriteRecords(ctx, []Record{del}); err != nil {
		t.Fatalf("WriteRecords(delete): %v", err)
	}

	all, err := store.ListChangesSince(ctx, 0, 100)
	if err != nil {
		t.Fatalf("ListChangesSince: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("变更数 = %d，应为 3", len(all))
	}
	if string(all[0].Payload) != `{"v":1}` {
		t.Fatalf("第一条载荷 = %s，应为写入时的快照 {\"v\":1}", all[0].Payload)
	}
	if string(all[1].Payload) != `{"v":2}` {
		t.Fatalf("第二条载荷 = %s", all[1].Payload)
	}
	if !all[2].Deleted || len(all[2].Payload) != 0 {
		t.Fatalf("墓碑 = %+v，应只有 hash、没有载荷", all[2])
	}
	if all[2].Hash != del.Hash {
		t.Fatalf("墓碑 hash = %s，应为最后内容 hash %s", all[2].Hash, del.Hash)
	}

	// since 是闭区间水位：从第二条的 rev 起读，应只跳过第一条。
	page, err := store.ListChangesSince(ctx, r1+1, 100)
	if err != nil {
		t.Fatalf("ListChangesSince(since): %v", err)
	}
	if len(page) != 2 || page[0].Rev != r1+1 {
		t.Fatalf("水位分页 = %+v，应从 rev %d 起两行", page, r1+1)
	}

	// limit 生效且不跳号。
	limited, err := store.ListChangesSince(ctx, 0, 2)
	if err != nil || len(limited) != 2 {
		t.Fatalf("limit=2 得到 %d 条, %v", len(limited), err)
	}
	if limited[1].Rev != all[1].Rev {
		t.Fatalf("limit 分页顺序不对：%+v", limited)
	}
}

// 推送落地的记录同样要进变更流，否则边缘之间互相看不到对方的写入。
func TestApplyPushedChangeAppearsInChangeStream(t *testing.T) {
	store := openTestStore(t)
	ctx := t.Context()
	payload := json.RawMessage(`{"v":"from-edge"}`)
	h := hashOf(t, Record{Domain: DomainPreferences, Key: "agents", Payload: payload})
	rev, err := store.ApplyPushedChange(ctx, PushedRecord{
		ChangeID: "ch-1", Domain: DomainPreferences, Key: "agents",
		Hash: h, Payload: payload, Origin: "hasee",
	})
	if err != nil {
		t.Fatalf("ApplyPushedChange: %v", err)
	}
	changes, err := store.ListChangesSince(ctx, 0, 10)
	if err != nil || len(changes) != 1 {
		t.Fatalf("变更流 = %d 条, %v", len(changes), err)
	}
	got := changes[0]
	if got.Rev != rev || got.Origin != "hasee" || !bytes.Equal(got.Payload, payload) {
		t.Fatalf("变更行 = %+v，应带推送载荷与 origin", got)
	}
}

// 早期库的 changes 没有 payload 列；补列之后能对上的行回填，对不上的跳过，
// 绝不能把「有 hash 没载荷」的活记录发出去（副本会直接拒收）。
func TestMigrateBackfillsChangePayloadAndSkipsUnbackfillable(t *testing.T) {
	store := openTestStore(t)
	ctx := t.Context()

	ok := rec(DomainPreferences, "agents", `{"v":"kept"}`)
	ok.Hash = hashOf(t, ok)
	okRevs, err := store.WriteRecords(ctx, []Record{ok})
	if err != nil {
		t.Fatalf("WriteRecords: %v", err)
	}
	okRev := okRevs[0]
	forgotten := rec(DomainPreferences, "pins", `{"v":"old"}`)
	forgotten.Hash = hashOf(t, forgotten)
	oldRevs, err := store.WriteRecords(ctx, []Record{forgotten})
	if err != nil {
		t.Fatalf("WriteRecords: %v", err)
	}
	oldRev := oldRevs[0]
	// 记录被再次修改：这条历史 change 的载荷已经无从还原。
	newer := rec(DomainPreferences, "pins", `{"v":"new"}`)
	newer.Hash = hashOf(t, newer)
	if _, err := store.WriteRecords(ctx, []Record{newer}); err != nil {
		t.Fatalf("WriteRecords(new): %v", err)
	}

	if _, err := store.db.ExecContext(ctx, `ALTER TABLE changes DROP COLUMN payload`); err != nil {
		t.Fatalf("模拟旧库: %v", err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	changes, err := store.ListChangesSince(ctx, 0, 100)
	if err != nil {
		t.Fatalf("ListChangesSince: %v", err)
	}
	seen := map[int64]RemoteChange{}
	for _, ch := range changes {
		seen[ch.Rev] = ch
	}
	if got, ok := seen[okRev]; !ok || string(got.Payload) != `{"v":"kept"}` {
		t.Fatalf("回填成功的那条没带载荷：%+v", got)
	}
	if _, ok := seen[oldRev]; ok {
		t.Fatalf("无法回填的历史行被发出去了（rev %d）", oldRev)
	}
	if len(seen) != 2 {
		t.Fatalf("可供给变更 = %d 条，应为 2（回填行 + 墓碑前的当前行）", len(seen))
	}
}
