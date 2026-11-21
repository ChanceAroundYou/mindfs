package controlsync

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "control_sync.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return store
}

func TestMigrateIsIdempotent(t *testing.T) {
	store := openTestStore(t)
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("二次 Migrate: %v", err)
	}
	want := []string{"applied_changes", "changes", "conflicts", "pending", "records", "state"}
	rows, err := store.db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatalf("query tables: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		got = append(got, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate tables: %v", err)
	}
	sort.Strings(got)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("tables = %v, want %v", got, want)
	}
	for _, table := range want {
		var n int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Fatalf("%s = %d 行，新库应为空", table, n)
		}
	}
}

// 并发打开同一路径：各自 Open + Migrate 都必须成功，且分配到的 rev 互不重复。
// rev 用 MAX(rev)+1 分配，如果事务不是 immediate，两个并发事务会读到同一个 MAX
// 然后在 changes 主键上撞车 —— 这条用例就是钉住那个写锁语义。
func TestConcurrentOpenMigratesAndAllocatesDistinctRevs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control_sync.db")
	const workers = 8
	revs := make([]int64, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			store, err := Open(path)
			if err != nil {
				errs[idx] = err
				return
			}
			defer store.Close()
			ctx := context.Background()
			if err := store.Migrate(ctx); err != nil {
				errs[idx] = err
				return
			}
			rec := Record{
				Domain:    "preferences",
				Key:       fmt.Sprintf("field-%d", idx),
				UpdatedAt: time.Unix(int64(1700000000+idx), 0).UTC(),
				Origin:    "home",
				Payload:   json.RawMessage(`{"value":true}`),
			}
			if rec.Hash, err = HashRecord(rec); err != nil {
				errs[idx] = err
				return
			}
			revs[idx], errs[idx] = store.WriteRecord(ctx, rec)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: %v", i, err)
		}
	}
	seen := map[int64]bool{}
	for i, rev := range revs {
		if rev <= 0 {
			t.Fatalf("worker %d 没拿到 rev: %d", i, rev)
		}
		if seen[rev] {
			t.Fatalf("rev %d 被分配了两次：%v", rev, revs)
		}
		seen[rev] = true
	}
}

func TestWriteRecordAllocatesMonotonicRevAndUpserts(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	first, err := store.WriteRecord(ctx, testRecord(t, "preferences", "agents", `{"value":1}`))
	if err != nil {
		t.Fatalf("第一次写入: %v", err)
	}
	second, err := store.WriteRecord(ctx, testRecord(t, "preferences", "agents", `{"value":2}`))
	if err != nil {
		t.Fatalf("第二次写入: %v", err)
	}
	if second != first+1 {
		t.Fatalf("rev = %d → %d，应逐次 +1", first, second)
	}
	var rows int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM records WHERE domain = 'preferences' AND key = 'agents'`).Scan(&rows); err != nil {
		t.Fatalf("count records: %v", err)
	}
	if rows != 1 {
		t.Fatalf("records 行数 = %d，同一 (domain,key) 必须覆盖而不是追加", rows)
	}
	var payload string
	if err := store.db.QueryRow(`SELECT payload FROM records WHERE domain = 'preferences' AND key = 'agents'`).Scan(&payload); err != nil {
		t.Fatalf("read payload: %v", err)
	}
	if payload != `{"value":2}` {
		t.Fatalf("payload = %s，应保留最后一次写入", payload)
	}
	var changes int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM changes`).Scan(&changes); err != nil {
		t.Fatalf("count changes: %v", err)
	}
	if changes != 2 {
		t.Fatalf("changes = %d，每次写入都应留一条变更", changes)
	}
}

// 事务回滚不留半条 change：records 与 changes 必须同生共死。
func TestWriteRecordRollsBackWholeChange(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	if _, err := writeRecordTx(ctx, tx, testRecord(t, "users", "alice", `{"username":"alice"}`), store.now); err != nil {
		t.Fatalf("writeRecordTx: %v", err)
	}
	// 事务内再制造一个必然失败的语句，模拟「写完 change，后续步骤出错」。
	if _, err := tx.ExecContext(ctx, `INSERT INTO records(domain, key, rev, hash, updated_at) VALUES(NULL, 'x', 99, 'h', 'now')`); err == nil {
		t.Fatalf("故意写坏的行居然成功了，用例失去意义")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	for _, table := range []string{"records", "changes"} {
		var n int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Fatalf("%s 回滚后残留 %d 行：change 写了一半", table, n)
		}
	}
}

func testRecord(t *testing.T, domain, key, payload string) Record {
	t.Helper()
	rec := Record{
		Domain:    domain,
		Key:       key,
		Origin:    "home",
		UpdatedAt: time.Unix(1700000000, 0).UTC(),
		Payload:   json.RawMessage(payload),
	}
	hash, err := HashRecord(rec)
	if err != nil {
		t.Fatalf("HashRecord: %v", err)
	}
	rec.Hash = hash
	return rec
}
