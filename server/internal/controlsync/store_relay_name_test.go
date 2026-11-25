package controlsync

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// relay_name 是从「旧库没有这一列」补上来的。补列后旧记录必须是空串（没名字），
// 而不是 NULL 或某台机器的名字 —— 否则升级后每台边控端会抢同一个地址。
func TestMigrateAddsRelayNameColumnToOldEdgesTable(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control_sync.db")

	// 造一个「升级前」的库：表结构里没有 relay_name。
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := raw.ExecContext(ctx, `CREATE TABLE edges (
		instance_id TEXT PRIMARY KEY,
		registered_at TEXT NOT NULL,
		last_seen_at TEXT NOT NULL,
		revoked_at TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatalf("create old table: %v", err)
	}
	if _, err := raw.ExecContext(ctx,
		`INSERT INTO edges(instance_id, registered_at, last_seen_at) VALUES (?,?,?)`,
		"legacy", now, now); err != nil {
		t.Fatalf("seed old edge: %v", err)
	}
	_ = raw.Close()

	// 用新版本打开并迁移。
	store, err := Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	edge, err := store.EdgeByID(ctx, "legacy")
	if err != nil {
		t.Fatalf("EdgeByID: %v", err)
	}
	if edge.RelayName != "" {
		t.Fatalf("旧记录的 relay_name = %q，want 空串（不能自动分到别人的名字）", edge.RelayName)
	}
}

// 名字是主控分配的、要落库：重启主控后其他设备仍能按同一个地址访问。
func TestSetRelayNamePersistsAndResolves(t *testing.T) {
	store := newEdgeStore(t)
	ctx := context.Background()
	if _, err := store.ObserveEdge(ctx, "hasee", time.Now()); err != nil {
		t.Fatalf("observe: %v", err)
	}
	if err := store.SetRelayName(ctx, "hasee", "hasee-office"); err != nil {
		t.Fatalf("SetRelayName: %v", err)
	}
	edge, err := store.EdgeByRelayName(ctx, "hasee-office")
	if err != nil {
		t.Fatalf("EdgeByRelayName: %v", err)
	}
	if edge.InstanceID != "hasee" {
		t.Fatalf("按名字解析出 %q，want hasee", edge.InstanceID)
	}
	// 再按 id 读回，名字也要在。
	byID, err := store.EdgeByID(ctx, "hasee")
	if err != nil {
		t.Fatalf("EdgeByID: %v", err)
	}
	if byID.RelayName != "hasee-office" {
		t.Fatalf("EdgeByID().RelayName = %q，want hasee-office", byID.RelayName)
	}
}

// 名字必须唯一：两台不同的机器不能解析到同一个地址。
func TestSetRelayNameRejectsDuplicate(t *testing.T) {
	store := newEdgeStore(t)
	ctx := context.Background()
	if _, err := store.ObserveEdge(ctx, "a", time.Now()); err != nil {
		t.Fatalf("observe a: %v", err)
	}
	if _, err := store.ObserveEdge(ctx, "b", time.Now()); err != nil {
		t.Fatalf("observe b: %v", err)
	}
	if err := store.SetRelayName(ctx, "a", "shared"); err != nil {
		t.Fatalf("SetRelayName(a): %v", err)
	}
	if err := store.SetRelayName(ctx, "b", "shared"); err == nil {
		t.Fatal("两台机器拿到了同一个 relay 名字")
	}
}

// 重复设置同一个名字必须幂等（重连时每拍都会走一次）。
func TestSetRelayNameIdempotent(t *testing.T) {
	store := newEdgeStore(t)
	ctx := context.Background()
	if _, err := store.ObserveEdge(ctx, "hasee", time.Now()); err != nil {
		t.Fatalf("observe: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := store.SetRelayName(ctx, "hasee", "hasee"); err != nil {
			t.Fatalf("第 %d 次 SetRelayName: %v", i, err)
		}
	}
}

// 未登记的 edge 不能赋名字：否则会写出一个无处可解析的地址。
func TestSetRelayNameUnknownEdge(t *testing.T) {
	store := newEdgeStore(t)
	if err := store.SetRelayName(context.Background(), "ghost", "ghost"); !errors.Is(err, ErrEdgeNotFound) {
		t.Fatalf("err = %v，want ErrEdgeNotFound", err)
	}
}

// RelayNames 返回 name→instanceID 全量映射，供分配逻辑判断冲突。
func TestRelayNamesMap(t *testing.T) {
	store := newEdgeStore(t)
	ctx := context.Background()
	for _, id := range []string{"a", "b"} {
		if _, err := store.ObserveEdge(ctx, id, time.Now()); err != nil {
			t.Fatalf("observe %s: %v", id, err)
		}
	}
	if err := store.SetRelayName(ctx, "a", "alpha"); err != nil {
		t.Fatalf("set a: %v", err)
	}
	names, err := store.RelayNames(ctx)
	if err != nil {
		t.Fatalf("RelayNames: %v", err)
	}
	if names["alpha"] != "a" {
		t.Fatalf("names = %v，want alpha→a", names)
	}
	// 没有名字的 edge 不应出现在映射里。
	if _, ok := names["b"]; ok {
		t.Fatalf("未命名的 edge 也进了映射：%v", names)
	}
}

// 空名字解析必须直接失败，不能返回「第一台」。
func TestEdgeByRelayNameEmptyIsNotFound(t *testing.T) {
	store := newEdgeStore(t)
	ctx := context.Background()
	if _, err := store.ObserveEdge(ctx, "hasee", time.Now()); err != nil {
		t.Fatalf("observe: %v", err)
	}
	if _, err := store.EdgeByRelayName(ctx, ""); !errors.Is(err, ErrEdgeNotFound) {
		t.Fatalf("err = %v，want ErrEdgeNotFound", err)
	}
	if _, err := store.EdgeByRelayName(ctx, "nobody"); !errors.Is(err, ErrEdgeNotFound) {
		t.Fatalf("err = %v，want ErrEdgeNotFound", err)
	}
}

// 重连 + 名字被别人占着：AssignRelayName 返回的候选名可能仍是别人的，
// SetRelayName 必须明确失败（ErrRelayNameTaken），让调用方看到冲突，
// 而不是静默把地址从别人手里抢过来。
func TestSetRelayNameConflictIsTyped(t *testing.T) {
	store := newEdgeStore(t)
	ctx := context.Background()
	for _, id := range []string{"a", "b"} {
		if _, err := store.ObserveEdge(ctx, id, time.Now()); err != nil {
			t.Fatalf("observe %s: %v", id, err)
		}
	}
	if err := store.SetRelayName(ctx, "a", "office"); err != nil {
		t.Fatalf("set a: %v", err)
	}
	err := store.SetRelayName(ctx, "b", "office")
	if !errors.Is(err, ErrRelayNameTaken) {
		t.Fatalf("err = %v，want ErrRelayNameTaken", err)
	}
	// 冲突时原归属不能被改掉。
	edge, err := store.EdgeByRelayName(ctx, "office")
	if err != nil || edge.InstanceID != "a" {
		t.Fatalf("冲突后归属被改了：edge=%+v err=%v", edge, err)
	}
}
