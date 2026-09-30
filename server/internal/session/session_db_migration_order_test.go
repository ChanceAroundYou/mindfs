package session

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	rootfs "mindfs/server/internal/fs"
)

// 真实故障回归：task-11 引入归档功能时，CREATE INDEX idx_sessions_archived_at 建在
// ADD COLUMN archived_at 之前。旧库里没有 archived_at 列，索引创建报 "no such column"，
// openSessionMetaDB 整个失败，ensureSessionMetaDBUnsafe 随即静默回退到 ~/.config/mindfs 下的
// 空库并写 .link —— 界面上表现为"所有历史会话消失"，而数据其实完好躺在旧库里。
func TestLegacyDBMissingArchivedAtKeepsSessions(t *testing.T) {
	rootDir := t.TempDir()
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)

	// 造一个"旧库"：完整 schema，唯独缺 pinned_at / archived_at 两列
	// （与生产上那批旧库一致 —— 这两列都是后来加的，而索引早就按它们建好了）。
	metaDir := root.MetaDir()
	legacyDB := filepath.Join(metaDir, "sessions", "session-list.db")
	if err := os.MkdirAll(filepath.Dir(legacyDB), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", legacyDB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE sessions (
		key TEXT PRIMARY KEY,
		type TEXT NOT NULL,
		parent_session_key TEXT NOT NULL DEFAULT '',
		parent_tool_call_id TEXT NOT NULL DEFAULT '',
		source TEXT NOT NULL DEFAULT '',
		task_id TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		shell TEXT NOT NULL DEFAULT '',
		plan_mode INTEGER NOT NULL DEFAULT 0,
		name TEXT NOT NULL,
		related_files_json TEXT NOT NULL,
		related_worktree_json TEXT NOT NULL DEFAULT '',
		last_context_window_total_tokens INTEGER NOT NULL DEFAULT 0,
		last_context_window_model_context_window INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		closed_at TEXT
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sessions
		(key, type, name, related_files_json, created_at, updated_at)
		VALUES ('old-1','chat','历史会话','[]','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if cols := tableColumns(t, db, "sessions"); cols["archived_at"] || cols["pinned_at"] {
		t.Fatal("fixture must NOT have archived_at/pinned_at, otherwise the test proves nothing")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	manager := NewManager(root)
	if _, err := manager.List(context.Background(), ListOptions{}); err != nil {
		t.Fatalf("opening a legacy db missing archived_at must succeed after migration, got: %v", err)
	}

	// 历史行必须还在：空库回退会让这里拿到 0 行。
	rows, err := manager.List(context.Background(), ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rows {
		if r.Key == "old-1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("legacy session old-1 vanished (rows=%d) — db fell back to an empty store", len(rows))
	}
}

// 旧库存在但打不开时，必须报错退出，而不是静默换成空库。
func TestExistingLegacyDBFailureDoesNotFallBackToEmpty(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	legacyDB := filepath.Join(root.MetaDir(), "sessions", "session-list.db")
	if err := os.MkdirAll(filepath.Dir(legacyDB), 0o755); err != nil {
		t.Fatal(err)
	}
	// 不是 SQLite 文件：openSessionMetaDB 必然失败，但文件确实存在。
	if err := os.WriteFile(legacyDB, []byte("not a sqlite database"), 0o644); err != nil {
		t.Fatal(err)
	}

	manager := NewManager(root)
	if _, err := manager.List(context.Background(), ListOptions{}); err == nil {
		t.Fatal("expected an error for an unreadable existing legacy db; falling back to an empty db hides data loss")
	}
}

func tableColumns(t *testing.T, db *sql.DB, table string) map[string]bool {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		out[name] = true
	}
	return out
}
