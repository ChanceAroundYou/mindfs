package session

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	rootfs "mindfs/server/internal/fs"
)

// 守 G-X「会话库的 pinned_at 列已停用」这条定制。
//
// 对应的坏法：某次改动把该列重新接回读写。后果不是「多存一个字段」这么轻 ——
// 置顶的权威是主节点的 pins 表（internal/pins，落 session_pins.json），
// 一旦会话库也存一份，置顶就回到「按机器分」的旧形状：
// 在这台机器上置顶的会话，到另一台机器上看就是没置顶的（CLAUDE.md 第 15 条）。
//
// 断言分三层，每层都要各自能被证伪：
//
//	SQL 面   —— 两条语句的字面量里就不许再出现 pinned_at（与结构体字段无关的一层）
//	读路径   —— 列里存着**残值**时，GetMeta 也不许把它读出来
//	写路径   —— 调用方把 PinnedAt 塞进结构体落盘，列也不许被写入
//
// 只测写路径会漏掉读路径的复活：列恒为 NULL 时，读回来当然是 nil，断言照样绿。
// 所以下面先往列里播一个残值 —— 这正是前端 services/sessionListMerge.ts 的
// applyPinnedSnapshotToSessions 在抹的那种历史数据（那段注释写着「会话库里那个
// 退役的 pinned_at 列可能还有残留值，留着会让置顶看起来在、点了又不动」）。
//
// 为什么还要第一层：上面两条绕不开 Session.PinnedAt 这个字段，而它并不是本定制的
// 产物 —— http_pins.go 的 pinnedSessionsForRoot 把置顶时间**写回**响应条目时也要
// 用它（那列退役后恒为空，前端要的置顶时间只能从 pins store 补上）。所以将来字段
// 若被改名/删掉，本测试会**编译不过**。编译不过虽然是响的，但那时最省事的反应是
// 把测试删掉，写路径的守卫就跟着一起没了。第一层只看 SQL 字面量，字段在不在都不
// 影响它 —— 字段真被删时，把那两层一并删掉就是了（它们断的是同一件事的另两个面），
// 这一层原样留下，不必重写。
func TestSessionMetaDoesNotPersistPinnedAt(t *testing.T) {
	// 0) SQL 面：两条语句都不许提这一列
	for name, sqlText := range map[string]string{
		"selectSessionSQL":     selectSessionSQL,
		"upsertSessionMetaSQL": upsertSessionMetaSQL,
	} {
		if strings.Contains(sqlText, "pinned_at") {
			t.Fatalf("%s 里又出现了 pinned_at —— 该列的读写都应断开，置顶的权威只能是 pins 表", name)
		}
	}

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
