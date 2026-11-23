package controlsync

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func newEdgeStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "control_sync.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// TestObserveEdgeRegistersOnFirstRequest 钉「自动注册」：第一次观察到就落两列，
// 第二次只推进 last_seen_at，registered_at 必须保持不变（界面上「什么时候接进来的」
// 被每次心跳刷掉就失去意义了）。
func TestObserveEdgeRegistersOnFirstRequest(t *testing.T) {
	store := newEdgeStore(t)
	ctx := context.Background()
	first := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)

	edge, err := store.ObserveEdge(ctx, "hasee", first)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if edge.RegisteredAt != first || edge.LastSeenAt != first {
		t.Fatalf("edge = %+v，首次登记两列都该是 %s", edge, first)
	}

	second := first.Add(90 * time.Second)
	edge, err = store.ObserveEdge(ctx, " hasee ", second)
	if err != nil {
		t.Fatalf("observe again: %v", err)
	}
	if edge.RegisteredAt != first {
		t.Fatalf("registered_at = %s，被心跳刷掉了（应为 %s）", edge.RegisteredAt, first)
	}
	if edge.LastSeenAt != second {
		t.Fatalf("last_seen_at = %s，应推进到 %s", edge.LastSeenAt, second)
	}

	list, err := store.ListEdges(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].InstanceID != "hasee" {
		t.Fatalf("list = %+v，同一台边控端不该出现两行", list)
	}
}

// TestObserveEdgeRefusesRevoked 钉「踢人之后不会被下一次心跳悄悄解除」。
// 这是踢人唯一真正生效的地方：注册是自动的，若不在这里拦住，Revoke 只是一条
// 会被覆盖的标记。
func TestObserveEdgeRefusesRevoked(t *testing.T) {
	store := newEdgeStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)

	if _, err := store.ObserveEdge(ctx, "hasee", now); err != nil {
		t.Fatalf("observe: %v", err)
	}
	if _, err := store.RevokeEdge(ctx, "hasee", now.Add(time.Minute)); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	edge, err := store.ObserveEdge(ctx, "hasee", now.Add(2*time.Minute))
	if !errors.Is(err, ErrEdgeRevoked) {
		t.Fatalf("err = %v，被踢掉的边控端应回 ErrEdgeRevoked", err)
	}
	if !edge.Revoked() {
		t.Fatalf("edge = %+v，应带着 revoked_at", edge)
	}
	if edge.LastSeenAt != now {
		t.Fatalf("last_seen_at = %s，被拒绝的请求不该推进它（应为 %s）", edge.LastSeenAt, now)
	}
}

// TestRevokeAndRestoreEdge 钉恢复链路：踢了能撤回，撤回后重新同步。
func TestRevokeAndRestoreEdge(t *testing.T) {
	store := newEdgeStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	if _, err := store.ObserveEdge(ctx, "hasee", now); err != nil {
		t.Fatalf("observe: %v", err)
	}
	revoked, err := store.RevokeEdge(ctx, "hasee", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if !revoked.Revoked() {
		t.Fatalf("revoke 后 revoked_at 为空：%+v", revoked)
	}
	// 幂等：再踢一次保持原来的时间与结果。
	again, err := store.RevokeEdge(ctx, "hasee", now.Add(time.Hour))
	if err != nil {
		t.Fatalf("revoke again: %v", err)
	}
	if again.RevokedAt == nil || !again.RevokedAt.Equal(*revoked.RevokedAt) {
		t.Fatalf("二次踢人把 revoked_at 刷成了 %v（原 %v）", again.RevokedAt, revoked.RevokedAt)
	}

	restored, err := store.RestoreEdge(ctx, "hasee")
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if restored.Revoked() {
		t.Fatalf("restore 后仍带 revoked_at：%+v", restored)
	}
	if _, err := store.ObserveEdge(ctx, "hasee", now.Add(2*time.Minute)); err != nil {
		t.Fatalf("恢复后应能重新同步，got %v", err)
	}
}

// TestEdgeByIDMissing 钉「没登记过」与「登记过但被踢」在错误上是两件事：
// 前者是 errEdgeNotFound，后者返回行 + ErrEdgeRevoked。合成同一个错误会让
// 排障时分不清「这台机器从没连上」和「我把它踢了」。
func TestEdgeByIDMissing(t *testing.T) {
	store := newEdgeStore(t)
	_, err := store.EdgeByID(context.Background(), "ghost")
	if err == nil {
		t.Fatal("不存在的边控端应报错")
	}
	if errors.Is(err, ErrEdgeRevoked) {
		t.Fatalf("err = %v，不该是 revoked", err)
	}
}
