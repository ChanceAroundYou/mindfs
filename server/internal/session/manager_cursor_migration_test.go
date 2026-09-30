package session

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	rootfs "mindfs/server/internal/fs"
)

// 老库升级：external_source_committed_offset 是后加的列，已有库必须靠 ALTER 补上。
// 漏了这一步，升级后读到的 committed offset 恒为 0，导入器会退回「提交到文件末尾」。
func TestAgentBindingSchemaMigratesCommittedOffset(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".mindfs", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbFile := filepath.Join(dir, "session-list.db")

	// 造一个「老版本」的库：绑定表没有 committed offset 列，并已有一行数据。
	legacy, err := sql.Open("sqlite", dbFile)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	if _, err := legacy.Exec(`CREATE TABLE session_agent_bindings (
		session_key TEXT NOT NULL,
		agent TEXT NOT NULL,
		agent_session_id TEXT NOT NULL,
		agent_ctx_seq INTEGER NOT NULL DEFAULT 0,
		external_source_path TEXT NOT NULL DEFAULT '',
		external_source_offset INTEGER NOT NULL DEFAULT 0,
		external_source_mtime_ns INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (session_key, agent)
	)`); err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	if _, err := legacy.Exec(
		`INSERT INTO session_agent_bindings (session_key, agent, agent_session_id, external_source_path, external_source_offset) VALUES (?,?,?,?,?)`,
		"old-session", "claude", "claude-session-1", "/tmp/old.jsonl", 1234,
	); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	legacy.Close()

	m := NewManager(rootfs.NewRootInfo("mindfs", "mindfs", root))
	ctx := context.Background()

	// 老行升级后：offset 保留，committed offset 为 0（导入器据此走时间戳引导）。
	old, err := m.GetAgentBinding(ctx, "old-session", "claude")
	if err != nil {
		t.Fatalf("get legacy binding after migration: %v", err)
	}
	if cursor := old.ExternalCursor(); cursor.Offset != 1234 || cursor.CommittedOffset != 0 {
		t.Fatalf("老游标迁移后 = %+v，want offset=1234 committed=0", cursor)
	}

	// 升级后的库要能写入 committed offset。
	if err := m.UpdateExternalSessionCursor(ctx, "old-session", "claude", old.ExternalCursor()); err != nil {
		t.Fatalf("update migrated cursor: %v", err)
	}
}
