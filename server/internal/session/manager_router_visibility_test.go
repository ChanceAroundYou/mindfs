package session

import (
	"context"
	"testing"
	"time"

	rootfs "mindfs/server/internal/fs"
)

// Router 会话是隐藏控制面：列表/计数/元数据/搜索/最新时间都不应看见它，
// 但按 key 的 Get 必须仍然可读（「调度记录」靠它）。
func TestRouterSessionHiddenFromListSearchCountLatest(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	manager := NewManager(root, WithClock(func() time.Time { return now }))
	ctx := context.Background()

	visible, err := manager.Create(ctx, CreateInput{Type: TypeChat, Name: "普通会话"})
	if err != nil {
		t.Fatalf("create visible: %v", err)
	}
	manager.now = func() time.Time { return now.Add(time.Hour) }
	router, err := manager.Create(ctx, CreateInput{Type: TypeRouter, Name: "调度记录", TaskID: "task-1"})
	if err != nil {
		t.Fatalf("create router: %v", err)
	}

	items, err := manager.List(ctx, ListOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 || items[0].Key != visible.Key {
		t.Fatalf("List = %#v, want only %q", items, visible.Key)
	}

	count, err := manager.Count(ctx, ListOptions{})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("Count = %d, want 1", count)
	}

	metas, err := manager.ListMetas(ctx)
	if err != nil {
		t.Fatalf("list metas: %v", err)
	}
	if len(metas) != 1 || metas[0].Key != visible.Key {
		t.Fatalf("ListMetas = %#v, want only %q", metas, visible.Key)
	}

	latest, err := manager.LatestSessionUpdatedAt(ctx)
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if !latest.Equal(visible.UpdatedAt) {
		t.Fatalf("latest = %v, want visible %v (router must not surface)", latest, visible.UpdatedAt)
	}

	hits, err := manager.Search(ctx, SearchOptions{Query: "调度", Limit: 20})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	for _, hit := range hits {
		if hit.Key == router.Key {
			t.Fatalf("router session leaked into search: %#v", hit)
		}
	}

	// 已归档视图同样不能因为归档模式而漏出。
	if _, err := manager.SetArchived(ctx, router.Key, true); err != nil {
		t.Fatalf("archive router: %v", err)
	}
	archived, err := manager.List(ctx, ListOptions{ArchivedMode: "only"})
	if err != nil {
		t.Fatalf("list archived: %v", err)
	}
	if len(archived) != 0 {
		t.Fatalf("archived router leaked: %#v", archived)
	}

	// Get 是「调度记录」的读取通道，不隐藏。
	loaded, err := manager.Get(ctx, router.Key, 0)
	if err != nil {
		t.Fatalf("get router: %v", err)
	}
	if loaded == nil || loaded.Type != TypeRouter || loaded.TaskID != "task-1" {
		t.Fatalf("Get router = %#v", loaded)
	}
}