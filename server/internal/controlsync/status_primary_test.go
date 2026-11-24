package controlsync

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func newPrimaryStatusStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "control_sync.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return store
}

// 主节点摘要要能回答「水位到哪、几台边控端在跟、最近一次心跳、有没有待裁决冲突」。
// 四条各自独立取数，任一条错都不能拿别的凑数。
func TestPrimaryStatusSummarizesWatermarkEdgesAndConflicts(t *testing.T) {
	store := newPrimaryStatusStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 24, 11, 30, 0, 0, time.UTC)

	early := now.Add(-2 * time.Hour)
	late := now.Add(-10 * time.Minute)
	if _, err := store.ObserveEdge(ctx, "hasee", early); err != nil {
		t.Fatalf("observe hasee: %v", err)
	}
	if _, err := store.ObserveEdge(ctx, "laptop", late); err != nil {
		t.Fatalf("observe laptop: %v", err)
	}
	// 被踢掉的边控端仍留在名单里（运维索引），但不该算进 active，也不该贡献心跳时间。
	if _, err := store.ObserveEdge(ctx, "retired", now); err != nil {
		t.Fatalf("observe retired: %v", err)
	}
	if _, err := store.RevokeEdge(ctx, "retired", now); err != nil {
		t.Fatalf("revoke retired: %v", err)
	}
	if _, err := store.InsertConflict(ctx, Conflict{ID: "c1", Domain: "preferences", Key: "agents"}); err != nil {
		t.Fatalf("insert conflict: %v", err)
	}

	got, err := store.PrimaryStatus(ctx, now)
	if err != nil {
		t.Fatalf("primary status: %v", err)
	}
	if got.Role != "primary" {
		t.Fatalf("role = %q，应为 primary", got.Role)
	}
	if got.SchemaVersion != SyncSchemaVersion {
		t.Fatalf("schema_version = %d，应为 %d", got.SchemaVersion, SyncSchemaVersion)
	}
	if got.EdgeCount != 3 {
		t.Fatalf("edge_count = %d，应为 3（含已踢掉的）", got.EdgeCount)
	}
	if got.ActiveEdgeCount != 2 {
		t.Fatalf("active_edge_count = %d，应为 2", got.ActiveEdgeCount)
	}
	// 心跳取活跃边缘里的最大值：已踢掉的 retired 更晚，但不能盖住真实心跳。
	if got.LastEdgeSeenAt == nil || !got.LastEdgeSeenAt.Equal(late) {
		t.Fatalf("last_edge_seen_at = %v，应为 %v", got.LastEdgeSeenAt, late)
	}
	if got.ConflictCount != 1 {
		t.Fatalf("conflict_count = %d，应为 1", got.ConflictCount)
	}
	if !got.CheckedAt.Equal(now) {
		t.Fatalf("checked_at = %v，应为 %v（UTC）", got.CheckedAt, now)
	}
}

// 一条边控端都没有时是「清水位」而不是错误：刚起的主节点本来就还没有人来同步。
func TestPrimaryStatusWithoutEdgesIsEmptyNotError(t *testing.T) {
	store := newPrimaryStatusStore(t)
	now := time.Date(2026, 11, 24, 11, 30, 0, 0, time.UTC)
	got, err := store.PrimaryStatus(context.Background(), now)
	if err != nil {
		t.Fatalf("primary status: %v", err)
	}
	if got.HeadRev != 0 || got.EdgeCount != 0 || got.ActiveEdgeCount != 0 || got.ConflictCount != 0 {
		t.Fatalf("空库摘要 = %+v", got)
	}
	if got.LastEdgeSeenAt != nil {
		t.Fatalf("无边控端时 last_edge_seen_at 应为空，实际 %v", got.LastEdgeSeenAt)
	}
}

// 关掉的 store 必须报错，不能返回零值被读成「水位 0、一切正常」。
func TestPrimaryStatusOnClosedStoreFails(t *testing.T) {
	store := newPrimaryStatusStore(t)
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := store.PrimaryStatus(context.Background(), time.Now()); err == nil {
		t.Fatalf("关掉 store 后应报错")
	}
}
