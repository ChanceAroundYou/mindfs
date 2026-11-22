package controlsync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 变更流不能无限长：spec §4.2 要求按 90 天 / 50 万行清理。清理必须**只删过期**，
// 并且在删空之后仍然不许复用 rev —— rev 是边缘的水位，复用等于让落后节点静默跳过新变更。
func writeTestChange(t *testing.T, store *Store, key string, deleted bool) int64 {
	t.Helper()
	r := rec(DomainPreferences, key, `{"v":"`+key+`"}`)
	if deleted {
		r = Record{Domain: DomainPreferences, Key: key, Deleted: true}
	}
	r.Hash = hashOf(t, r)
	rev, err := store.WriteRecord(context.Background(), r)
	if err != nil {
		t.Fatalf("WriteRecord(%s): %v", key, err)
	}
	return rev
}

// backdateChange 把一条 change 的 created_at 挪到过去：清理按时间判定，
// 只有直接改库才造得出「90 天前」的样本。
func backdateChange(t *testing.T, store *Store, rev int64, at time.Time) {
	t.Helper()
	if _, err := store.db.ExecContext(context.Background(),
		`UPDATE changes SET created_at = ? WHERE rev = ?`,
		at.UTC().Format(time.RFC3339Nano), rev); err != nil {
		t.Fatalf("backdate rev %d: %v", rev, err)
	}
}

func changeRevs(t *testing.T, store *Store) []int64 {
	t.Helper()
	rows, err := store.db.QueryContext(context.Background(), `SELECT rev FROM changes ORDER BY rev`)
	if err != nil {
		t.Fatalf("read changes: %v", err)
	}
	defer rows.Close()
	var revs []int64
	for rows.Next() {
		var rev int64
		if err := rows.Scan(&rev); err != nil {
			t.Fatalf("scan rev: %v", err)
		}
		revs = append(revs, rev)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read changes: %v", err)
	}
	return revs
}

// pruneAt 只给时间，其余全走零值：用例测的就是生产默认值，
// 把保留期/上限在用例里再抄一遍，改坏默认值时一个都不会红。
func pruneAt(now time.Time) PruneOptions {
	return PruneOptions{Now: now}
}

// 只删过期：90 天前的活记录与被删记录都清掉，保留期内的（活的和墓碑）一条不动。
// 墓碑不是可回收垃圾 —— 它本身也在保留期内，删早了落后节点就看不见那次删除。
func TestPruneChangesDeletesOnlyExpired(t *testing.T) {
	if ChangeRetention != 90*24*time.Hour || ChangeMaxRows != 500000 {
		t.Fatalf("保留策略 = %v / %d 行，与 spec §4.2 的 90 天 / 50 万行不符", ChangeRetention, ChangeMaxRows)
	}
	store := openTestStore(t)
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

	expiredLive := writeTestChange(t, store, "expired-live", false)
	expiredTombstone := writeTestChange(t, store, "expired-tombstone", true)
	freshLive := writeTestChange(t, store, "fresh-live", false)
	freshTombstone := writeTestChange(t, store, "fresh-tombstone", true)

	backdateChange(t, store, expiredLive, now.Add(-100*24*time.Hour))
	backdateChange(t, store, expiredTombstone, now.Add(-91*24*time.Hour))

	n, err := store.PruneChanges(context.Background(), pruneAt(now))
	if err != nil {
		t.Fatalf("PruneChanges: %v", err)
	}
	if n != 2 {
		t.Fatalf("删除 %d 条，应只删过期的 2 条", n)
	}
	got := changeRevs(t, store)
	want := []int64{freshLive, freshTombstone}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("剩余 rev = %v，应为 %v", got, want)
	}
}

// 单次清理必须有上限：一次删掉几十万行会把主节点锁在写事务里，
// 期间既不能采集也不能回应边缘。超出的部分留给下一拍。
func TestPruneChangesHonorsBatchBudget(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	for _, key := range []string{"a", "b", "c", "d", "e"} {
		writeTestChange(t, store, key, false)
	}
	opts := pruneAt(time.Now())
	opts.MaxRows = 2
	opts.Batch = 2

	n, err := store.PruneChanges(ctx, opts)
	if err != nil {
		t.Fatalf("PruneChanges: %v", err)
	}
	if n != 2 {
		t.Fatalf("首次删除 %d 条，批量上限为 2", n)
	}
	if left := len(changeRevs(t, store)); left != 3 {
		t.Fatalf("剩余 %d 条，应余 3 条", left)
	}
	// 5 条、上限 2 条：第二次只需再删 1 条就回到上限内，不该继续删满一批。
	n, err = store.PruneChanges(ctx, opts)
	if err != nil {
		t.Fatalf("PruneChanges: %v", err)
	}
	if n != 1 {
		t.Fatalf("第二次删除 %d 条，应只删超出上限的 1 条", n)
	}
	n, err = store.PruneChanges(ctx, opts)
	if err != nil {
		t.Fatalf("PruneChanges: %v", err)
	}
	if n != 0 {
		t.Fatalf("第三次删除 %d 条，已在行数上限内不该再删", n)
	}
}

// 删空之后 rev 必须继续往上发：复用 1、2、3 会让水位停在旧值的边缘
// 把新变更当成「已应用过」全部跳过，而水位那一侧看起来毫无异常。
func TestPruneKeepsRevMonotonicAfterEverythingDeleted(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Now()
	var last int64
	for _, key := range []string{"a", "b", "c"} {
		last = writeTestChange(t, store, key, false)
		backdateChange(t, store, last, now.Add(-100*24*time.Hour))
	}

	if _, err := store.PruneChanges(ctx, pruneAt(now)); err != nil {
		t.Fatalf("PruneChanges: %v", err)
	}
	if revs := changeRevs(t, store); len(revs) != 0 {
		t.Fatalf("剩余 rev = %v，应清空", revs)
	}
	if head, err := store.HeadRev(ctx); err != nil || head != last {
		t.Fatalf("清空后 HeadRev = %d, %v；应保持 %d", head, err, last)
	}

	next := writeTestChange(t, store, "after-prune", false)
	if next <= last {
		t.Fatalf("清理后新 rev = %d，不得复用 ≤ %d 的旧序号", next, last)
	}
	if head, err := store.HeadRev(ctx); err != nil || head != next {
		t.Fatalf("HeadRev = %d, %v；应为 %d", head, err, next)
	}
}

// 落后太多（要的 rev 已经被清理）必须显式报错，不能返回一页「看起来正常」的变更：
// 静默跳过等于让副本永久缺一段控制面，而界面不会有任何提示。
func TestChangeFeedRejectsRetentionGap(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Now()
	var expired []int64
	for _, key := range []string{"a", "b"} {
		rev := writeTestChange(t, store, key, false)
		backdateChange(t, store, rev, now.Add(-100*24*time.Hour))
		expired = append(expired, rev)
	}
	fresh := writeTestChange(t, store, "c", false)
	if _, err := store.PruneChanges(ctx, pruneAt(now)); err != nil {
		t.Fatalf("PruneChanges: %v", err)
	}
	feed, err := NewChangeFeed(store)
	if err != nil {
		t.Fatalf("NewChangeFeed: %v", err)
	}

	// since 落在已被清理的区间（含水位正好等于最后一条被删的 rev）。
	for _, since := range []int64{0, expired[0], expired[len(expired)-1]} {
		if _, _, err := feed.Page(ctx, since, 10); !errors.Is(err, ErrRetentionGap) {
			t.Fatalf("since=%d 应报 ErrRetentionGap，得到 %v", since, err)
		}
	}
	// 还在保留窗口内的起点照常供给。
	changes, head, err := feed.Page(ctx, fresh, 10)
	if err != nil {
		t.Fatalf("since=%d 不该报错: %v", fresh, err)
	}
	if len(changes) != 1 || changes[0].Rev != fresh {
		t.Fatalf("changes = %+v，应只含 rev %d", changes, fresh)
	}
	if head != fresh {
		t.Fatalf("head = %d，应为 %d", head, fresh)
	}
}

// 库里存着含口令 hash 的整份控制面，文件权限就是最后的边界。
func TestMigrateHardensStoredFilePermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sync")
	path := filepath.Join(dir, "control_sync.db")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// 旧库：先按默认权限造出来，Migrate 必须收紧。
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("seed db: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	assertMode(t, path, 0o600)
	// WAL 与 SHM 会跟着库文件一起被读：口径必须一致。
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(path + suffix); err == nil {
			assertMode(t, path+suffix, 0o600)
		}
	}

	// 新库与新建目录：一开始就不该有任何一段是可读的。
	freshDir := filepath.Join(t.TempDir(), "fresh")
	freshPath := filepath.Join(freshDir, "control_sync.db")
	fresh, err := Open(freshPath)
	if err != nil {
		t.Fatalf("Open fresh: %v", err)
	}
	t.Cleanup(func() { _ = fresh.Close() })
	if err := fresh.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate fresh: %v", err)
	}
	assertMode(t, freshDir, 0o700)
	assertMode(t, freshPath, 0o600)
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s 权限 = %04o，应为 %04o", filepath.Base(path), got, want)
	}
}
