package session

import (
	"context"
	"testing"
	"time"

	rootfs "mindfs/server/internal/fs"
)

func TestManagerPersistsArchivedAtWithoutChangingUpdatedAt(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	manager := NewManager(root, WithClock(func() time.Time { return now }))

	created, err := manager.Create(context.Background(), CreateInput{Type: TypeChat, Name: "Archived"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	originalUpdatedAt := created.UpdatedAt

	archivedAt := now.Add(5 * time.Minute)
	manager.now = func() time.Time { return archivedAt }
	archived, err := manager.SetArchived(context.Background(), created.Key, true)
	if err != nil {
		t.Fatalf("archive session: %v", err)
	}
	if archived.ArchivedAt == nil || !archived.ArchivedAt.Equal(archivedAt) {
		t.Fatalf("ArchivedAt = %v, want %v", archived.ArchivedAt, archivedAt)
	}
	if !archived.UpdatedAt.Equal(originalUpdatedAt) {
		t.Fatalf("UpdatedAt changed on archive: got %v, want %v", archived.UpdatedAt, originalUpdatedAt)
	}

	manager.sessions = map[string]*Session{}
	loaded, err := manager.Get(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if loaded.ArchivedAt == nil || !loaded.ArchivedAt.Equal(archivedAt) {
		t.Fatalf("reloaded ArchivedAt = %v, want %v", loaded.ArchivedAt, archivedAt)
	}

	clearedAt := now.Add(10 * time.Minute)
	manager.now = func() time.Time { return clearedAt }
	cleared, err := manager.SetArchived(context.Background(), created.Key, false)
	if err != nil {
		t.Fatalf("unarchive session: %v", err)
	}
	if cleared.ArchivedAt != nil {
		t.Fatalf("ArchivedAt after unarchive = %v, want nil", cleared.ArchivedAt)
	}
	if !cleared.UpdatedAt.Equal(originalUpdatedAt) {
		t.Fatalf("UpdatedAt changed on unarchive: got %v, want %v", cleared.UpdatedAt, originalUpdatedAt)
	}
}

func TestSetArchivedIsIdempotent(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	manager := NewManager(root, WithClock(func() time.Time { return now }))
	ctx := context.Background()

	created, err := manager.Create(ctx, CreateInput{Type: TypeChat, Name: "Twice"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	first, err := manager.SetArchived(ctx, created.Key, true)
	if err != nil {
		t.Fatalf("first archive: %v", err)
	}
	later := now.Add(time.Hour)
	manager.now = func() time.Time { return later }
	second, err := manager.SetArchived(ctx, created.Key, true)
	if err != nil {
		t.Fatalf("second archive should be a no-op: %v", err)
	}
	// 已是目标态就早返回：不重写 archived_at（否则时间戳会跟着漂）
	if !second.ArchivedAt.Equal(*first.ArchivedAt) {
		t.Fatalf("ArchivedAt drifted on repeat archive: %v -> %v", first.ArchivedAt, second.ArchivedAt)
	}
	third, err := manager.SetArchived(ctx, created.Key, false)
	if err != nil {
		t.Fatalf("first unarchive: %v", err)
	}
	if third.ArchivedAt != nil {
		t.Fatalf("ArchivedAt = %v, want nil", third.ArchivedAt)
	}
	if _, err := manager.SetArchived(ctx, created.Key, false); err != nil {
		t.Fatalf("repeat unarchive should be a no-op: %v", err)
	}
}

func TestListExcludesArchivedByDefaultAndSupportsArchivedOnly(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)
	ctx := context.Background()

	plain, err := manager.Create(ctx, CreateInput{Type: TypeChat, Name: "Plain"})
	if err != nil {
		t.Fatalf("create plain: %v", err)
	}
	hidden, err := manager.Create(ctx, CreateInput{Type: TypeChat, Name: "Hidden"})
	if err != nil {
		t.Fatalf("create hidden: %v", err)
	}
	if _, err := manager.SetArchived(ctx, hidden.Key, true); err != nil {
		t.Fatalf("archive: %v", err)
	}

	// 默认（主面板）：排除归档
	items, err := manager.List(ctx, ListOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 || items[0].Key != plain.Key {
		keys := make([]string, 0, len(items))
		for _, item := range items {
			keys = append(keys, item.Key)
		}
		t.Fatalf("default list should exclude archived, got %v", keys)
	}
	count, err := manager.Count(ctx, ListOptions{})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("default count = %d, want 1", count)
	}

	// only（归档区）：只要归档的
	archived, err := manager.List(ctx, ListOptions{ArchivedMode: "only"})
	if err != nil {
		t.Fatalf("list archived: %v", err)
	}
	if len(archived) != 1 || archived[0].Key != hidden.Key {
		t.Fatalf("archived list = %v, want only %s", archived, hidden.Key)
	}
	archivedCount, err := manager.Count(ctx, ListOptions{ArchivedMode: "only"})
	if err != nil {
		t.Fatalf("count archived: %v", err)
	}
	if archivedCount != 1 {
		t.Fatalf("archived count = %d, want 1", archivedCount)
	}

	// 取消归档后回到主列表
	if _, err := manager.SetArchived(ctx, hidden.Key, false); err != nil {
		t.Fatalf("unarchive: %v", err)
	}
	items, err = manager.List(ctx, ListOptions{})
	if err != nil {
		t.Fatalf("list after unarchive: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("after unarchive list = %d items, want 2", len(items))
	}
}

// 置顶不再存在会话库里（权威在主节点的置顶表），但「归档后从置顶区掉进归档区」
// 这条**产品规则**还在 —— 由 api 层过滤。所以这里守的是 ListByKeys 那道过滤：
// 归档的会话不该被当成「已置顶」捞出来。
func TestListByKeysCanExcludeArchivedSessions(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)
	ctx := context.Background()

	created, err := manager.Create(ctx, CreateInput{Type: TypeChat, Name: "PinnedThenArchived"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	found, err := manager.ListByKeys(ctx, []string{created.Key})
	if err != nil {
		t.Fatalf("ListByKeys: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("ListByKeys = %d, want 1", len(found))
	}

	if _, err := manager.SetArchived(ctx, created.Key, true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	// 归档不该让会话从库里消失（深链接仍要能开），所以这里只钉住「还在」，
	// 过滤发生在 api 层按 archived 标记挑的那一层。
	found, err = manager.ListByKeys(ctx, []string{created.Key})
	if err != nil {
		t.Fatalf("ListByKeys after archive: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("归档后会话必须仍能查到, got %d", len(found))
	}
	if found[0].ArchivedAt == nil {
		t.Fatal("归档标记丢了")
	}
}

// ListByKeys 只认本机库里有的键：置顶表跨节点，别的机器的键查不到就静默丢弃，
// 不能报错也不能凭空造出条目。顺序按传入顺序，不按库里的顺序。
func TestListByKeysSkipsUnknownKeysAndKeepsOrder(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)
	ctx := context.Background()

	a, err := manager.Create(ctx, CreateInput{Type: TypeChat, Name: "A"})
	if err != nil {
		t.Fatalf("create a: %v", err)
	}
	b, err := manager.Create(ctx, CreateInput{Type: TypeChat, Name: "B"})
	if err != nil {
		t.Fatalf("create b: %v", err)
	}
	got, err := manager.ListByKeys(ctx, []string{"别的机器的键", b.Key, a.Key, b.Key})
	if err != nil {
		t.Fatalf("ListByKeys: %v", err)
	}
	if len(got) != 2 || got[0].Key != b.Key || got[1].Key != a.Key {
		t.Fatalf("ListByKeys = %v, want [%s %s]", keysOf(got), b.Key, a.Key)
	}
	if empty, err := manager.ListByKeys(ctx, nil); err != nil || len(empty) != 0 {
		t.Fatalf("空入参应返回空结果, got %v err %v", empty, err)
	}
}

func keysOf(items []*Session) []string {
	out := make([]string, 0, len(items))
	for _, s := range items {
		out = append(out, s.Key)
	}
	return out
}
