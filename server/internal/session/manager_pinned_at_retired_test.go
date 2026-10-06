package session

import (
	"context"
	"database/sql"
	"testing"
	"time"

	rootfs "mindfs/server/internal/fs"
)

// 守 G-AK「会话库的 pinned_at 列已停用」这条定制。
//
// 对应的坏法：某次改动把该列重新接回读写。后果不是「多存一个字段」这么轻 ——
// 置顶的权威是主节点的 pins 表（internal/pins，落 session_pins.json），
// 一旦会话库也存一份，置顶就回到「按机器分」的旧形状：
// 在这台机器上置顶的会话，到另一台机器上看就是没置顶的（CLAUDE.md 第 15 条）。
//
// 断言分两层，且两层要各自能被证伪：
//
//	读路径 —— 列里存着**残值**时，GetMeta 也不许把它读出来
//	写路径 —— 调用方把 PinnedAt 塞进结构体落盘，列也不许被写入
//
// 只测写路径会漏掉读路径的复活：列恒为 NULL 时，读回来当然是 nil，断言照样绿。
// 所以下面先往列里播一个残值 —— 这正是前端 sessionListMerge.ts 在抹的那种历史数据。
func TestSessionMetaDoesNotPersistPinnedAt(t *testing.T) {
	root := rootfs.NewRootInfo("test", "test", t.TempDir())
	manager := NewManager(root)
	ctx := context.Background()

	created, err := manager.Create(ctx, CreateInput{Type: "chat", Name: "pin probe"})
	if err != nil {
		t.Fatal(err)
	}
	db, err := manager.ensureSessionMetaDBUnsafe()
	if err != nil {
		t.Fatal(err)
	}

	// 1) 读路径：拿一个**带着历史残值**的会话（旧版本写进去的，用户升级前留下的）
	residual := "2026-09-01T00:00:00Z"
	if _, err := db.Exec(`UPDATE sessions SET pinned_at = ? WHERE key = ?`, residual, created.Key); err != nil {
		t.Fatal(err)
	}
	reloaded, err := manager.GetMeta(ctx, created.Key)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.PinnedAt != nil {
		t.Fatalf("列里的残值被读回来了（读路径重新接上了）: %v；置顶的权威只能是 pins 表", reloaded.PinnedAt)
	}

	// 2) 写路径：另起一个**干净**会话（列里没有残值），模拟调用方把 PinnedAt 塞进
	//    结构体后落盘（http_pins.go 给响应条目盖章就是这个形状）。落盘不得把它写进列。
	//    不复用上面那个会话：它带着残值，断言「列必须为 NULL」会被残值本身绊倒。
	clean, err := manager.Create(ctx, CreateInput{Type: "chat", Name: "pin probe 2"})
	if err != nil {
		t.Fatal(err)
	}
	pinned := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	clean.PinnedAt = &pinned
	if err := manager.upsertSessionMetaUnsafe(clean); err != nil {
		t.Fatal(err)
	}
	var raw sql.NullString
	if err := db.QueryRow(`SELECT pinned_at FROM sessions WHERE key = ?`, clean.Key).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw.Valid {
		t.Fatalf("sessions.pinned_at 被写入了 %q，该列应恒为 NULL（写路径重新接上了）", raw.String)
	}
}
