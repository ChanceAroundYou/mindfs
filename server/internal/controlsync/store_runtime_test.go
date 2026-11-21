package controlsync

import (
	"testing"
	"time"
)

// 交接（进入 offline-local）要把副本整体写进权威存储，所以必须能读到载荷与墓碑；
// RecordStates 只有 hash，不够。
func TestListRecordsReturnsPayloadAndTombstones(t *testing.T) {
	store := openTestStore(t)
	ctx := t.Context()
	live := testRecord(t, DomainPreferences, "agents", `{"v":1}`)
	if _, err := store.WriteRecord(ctx, live); err != nil {
		t.Fatalf("WriteRecord live: %v", err)
	}
	tomb := Record{
		Domain: DomainPreferences, Key: "ghost", Hash: live.Hash,
		Deleted: true, UpdatedAt: time.Unix(1700000001, 0).UTC(),
	}
	if _, err := store.WriteRecord(ctx, tomb); err != nil {
		t.Fatalf("WriteRecord tombstone: %v", err)
	}
	recs, err := store.ListRecords(ctx, DomainPreferences)
	if err != nil {
		t.Fatalf("ListRecords: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("记录数 = %d，应为 2", len(recs))
	}
	if recs[0].Key != "agents" || recs[1].Key != "ghost" {
		t.Fatalf("顺序 = %q,%q，应按 key 稳定排序", recs[0].Key, recs[1].Key)
	}
	if string(recs[0].Payload) != `{"v":1}` || recs[0].Hash != live.Hash || recs[0].Deleted {
		t.Fatalf("活记录 = %+v", recs[0])
	}
	if !recs[1].Deleted || recs[1].Hash != live.Hash || len(recs[1].Payload) != 0 {
		t.Fatalf("墓碑 = %+v，应保留最后一次内容 hash 且无载荷", recs[1])
	}
}

func TestListRecordsIsScopedToDomain(t *testing.T) {
	store := openTestStore(t)
	ctx := t.Context()
	if _, err := store.WriteRecord(ctx, testRecord(t, DomainPreferences, "agents", `{"v":1}`)); err != nil {
		t.Fatalf("seed preferences: %v", err)
	}
	if _, err := store.WriteRecord(ctx, testRecord(t, DomainPins, "s1", `{"pinned":true}`)); err != nil {
		t.Fatalf("seed pins: %v", err)
	}
	recs, err := store.ListRecords(ctx, DomainPins)
	if err != nil {
		t.Fatalf("ListRecords: %v", err)
	}
	if len(recs) != 1 || recs[0].Domain != DomainPins || recs[0].Key != "s1" {
		t.Fatalf("记录 = %+v，应只含 pins 域", recs)
	}
}

// 恢复条件用的是「还有没有未裁决的卡」：已裁决（含被取代）的不算。
func TestCountOpenConflictsCountsOnlyUnresolved(t *testing.T) {
	store := openTestStore(t)
	ctx := t.Context()
	for _, key := range []string{"a", "b", "c"} {
		if _, err := store.InsertConflict(ctx, Conflict{
			ID:         PullConflictID(DomainPins, key),
			Domain:     DomainPins,
			Key:        key,
			LocalHash:  "sha256:local-" + key,
			RemoteHash: "sha256:remote-" + key,
		}); err != nil {
			t.Fatalf("InsertConflict %s: %v", key, err)
		}
	}
	if n, err := store.CountOpenConflicts(ctx); err != nil || n != 3 {
		t.Fatalf("未裁决 = %d, %v，应为 3", n, err)
	}
	if _, err := store.SupersedeConflicts(ctx, DomainPins, "a", "superseded"); err != nil {
		t.Fatalf("SupersedeConflicts: %v", err)
	}
	if n, err := store.CountOpenConflicts(ctx); err != nil || n != 2 {
		t.Fatalf("被取代后未裁决 = %d, %v，应为 2", n, err)
	}
	if err := store.ClaimConflict(ctx, PullConflictID(DomainPins, "b"), ConflictChoicePrimary); err != nil {
		t.Fatalf("ClaimConflict: %v", err)
	}
	// 占坑即从待裁决列表消失（用户点了按钮，前端不该再让他点一次）。
	// 裁决的副作用与回传还没落定时靠 pending 兜住：Runtime 的恢复闸门是
	// 「没有未裁决的卡」且「没有待回传」两条同时成立。
	if n, err := store.CountOpenConflicts(ctx); err != nil || n != 1 {
		t.Fatalf("裁决进行中未裁决 = %d, %v，应为 1", n, err)
	}
}

// 交接时按 key 跳过「本地已有待回传改动」的记录：副本那一版已经被本地改动
// 取代了，再写进权威存储就是拿旧值盖掉用户的编辑。
func TestPendingKeysIsScopedToDomain(t *testing.T) {
	store := openTestStore(t)
	ctx := t.Context()
	for _, row := range []struct{ domain, key string }{
		{DomainPreferences, "agents"},
		{DomainPreferences, "session_naming"},
		{DomainPins, "agents"},
	} {
		if err := store.PutPending(ctx, PendingChange{
			ChangeID: "edge:" + row.domain + ":" + row.key + ":1",
			Domain:   row.domain, Key: row.key,
			Payload: []byte(`{"v":1}`),
		}); err != nil {
			t.Fatalf("PutPending %s/%s: %v", row.domain, row.key, err)
		}
	}
	keys, err := store.PendingKeys(ctx, DomainPreferences)
	if err != nil {
		t.Fatalf("PendingKeys: %v", err)
	}
	if len(keys) != 2 || !keys["agents"] || !keys["session_naming"] {
		t.Fatalf("pending keys = %v，应只有 preferences 域的两条", keys)
	}
}
