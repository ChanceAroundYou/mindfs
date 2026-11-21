package controlsync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// 主节点与边缘共用同一份 DDL。
//
// 教训来自 kanban 的 EnsureOutboxTables：两处各写一份表定义、谁先跑谁定结构，
// 后来按新结构写的查询会在旧结构上因缺列而全挂。这里只有一份，角色差异只体现在
// 「用哪几张表」：主节点用 records/changes/applied_changes，边缘用 records（自身副本）
// 与 pending/state；conflicts 两边同构，UI 只写一套读取逻辑。
//
// 表名与 spec §4 的对应：spec §4.3 里的 edge 表 replica 与 §4.2 的 records 列完全相同，
// 这里合并成 records 一张 —— 名字不同、内容逐字重复，正是要避免的那类漂移。
const schemaDDL = `
CREATE TABLE IF NOT EXISTS records (
	domain TEXT NOT NULL,
	key TEXT NOT NULL,
	rev INTEGER NOT NULL,
	hash TEXT NOT NULL,
	payload BLOB,
	deleted INTEGER NOT NULL DEFAULT 0,
	updated_at TEXT NOT NULL,
	origin TEXT NOT NULL DEFAULT '',
	PRIMARY KEY(domain, key)
);
CREATE TABLE IF NOT EXISTS changes (
	rev INTEGER PRIMARY KEY,
	domain TEXT NOT NULL,
	key TEXT NOT NULL,
	hash TEXT NOT NULL,
	deleted INTEGER NOT NULL DEFAULT 0,
	origin TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS changes_created_at ON changes(created_at);
CREATE TABLE IF NOT EXISTS applied_changes (
	change_id TEXT PRIMARY KEY,
	rev INTEGER NOT NULL,
	applied_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS pending (
	domain TEXT NOT NULL,
	key TEXT NOT NULL,
	change_id TEXT NOT NULL,
	base_hash TEXT NOT NULL DEFAULT '',
	payload BLOB,
	deleted INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	PRIMARY KEY(domain, key)
);
CREATE TABLE IF NOT EXISTS state (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	instance_id TEXT NOT NULL DEFAULT '',
	mode TEXT NOT NULL DEFAULT '',
	base_rev INTEGER NOT NULL DEFAULT 0,
	last_primary_at TEXT NOT NULL DEFAULT '',
	last_error TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS conflicts (
	id TEXT PRIMARY KEY,
	domain TEXT NOT NULL,
	key TEXT NOT NULL,
	local_payload BLOB,
	remote_payload BLOB,
	local_hash TEXT NOT NULL DEFAULT '',
	remote_hash TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	resolved_at TEXT NOT NULL DEFAULT '',
	resolution TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS conflicts_open ON conflicts(domain, key) WHERE resolved_at = '';
`

// Store 是同步索引库的句柄。它只存「记录快照、变更日志、待回传、冲突现场」，
// 业务数据仍在各自的 JSON / store 里。
type Store struct {
	db   *sql.DB
	path string
	now  func() time.Time
}

// Open 打开（或新建）同步索引库。不做迁移 —— 调用方显式跑 Migrate，方便测试与
// 启动顺序控制。同步实例在每台机器上应只有一个，多开会让 rev 分配与 pending 归属打架。
func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("controlsync: db path required")
	}
	// 路径拼进 DSN 查询串，带 ? 的真实路径会被当参数拆开。与其做转义，不如直接拒绝：
	// 配置错到这一步，静默连到另一个文件更糟。
	if strings.ContainsAny(path, "?#") {
		return nil, fmt.Errorf("controlsync: db path contains '?' or '#': %q", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("controlsync: create db dir: %w", err)
	}
	// busy_timeout：并发写排队而不是立刻报 "database is locked"。
	// journal_mode=WAL：读写不互相阻塞。
	// _txlock=immediate：事务一开始就拿写锁 —— rev 用 MAX(rev)+1 分配，
	// 延迟事务会让两个并发事务读到同一个 MAX，随后在 changes 主键上撞车。
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("controlsync: open db: %w", err)
	}
	return &Store{db: db, path: path, now: time.Now}, nil
}

// Migrate 建表。幂等：所有语句都是 IF NOT EXISTS，可以每次启动都跑。
func (s *Store) Migrate(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("controlsync: store closed")
	}
	if _, err := s.db.ExecContext(ctx, schemaDDL); err != nil {
		return fmt.Errorf("controlsync: migrate: %w", err)
	}
	return nil
}

// Close 关闭句柄。可重复调用。
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

// WriteRecord 写入一条记录的当前快照，并在同一事务里追加变更日志，返回分配的 rev。
//
// 两条写必须同生共死：只有 change 没有 record，边缘拉到的增量无处落地；
// 只有 record 没有 change，边缘永远收不到这次改动。
func (s *Store) WriteRecord(ctx context.Context, rec Record) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("controlsync: store closed")
	}
	if err := ValidateRecord(rec); err != nil {
		return 0, err
	}
	if strings.TrimSpace(rec.Hash) == "" {
		return 0, errors.New("controlsync: record hash required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("controlsync: begin: %w", err)
	}
	rev, err := writeRecordTx(ctx, tx, rec, s.now)
	if err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("controlsync: commit: %w", err)
	}
	return rev, nil
}

// writeRecordTx 是 WriteRecord 的事务内核，单独拎出来是为了能用外层事务驱动，
// 测试也靠它验证「回滚不留半条 change」。
func writeRecordTx(ctx context.Context, tx *sql.Tx, rec Record, now func() time.Time) (int64, error) {
	var rev int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(rev), 0) + 1 FROM changes`).Scan(&rev); err != nil {
		return 0, fmt.Errorf("controlsync: allocate rev: %w", err)
	}
	updatedAt := rec.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = now()
	}
	updatedAt = updatedAt.UTC()
	var payload any
	if len(rec.Payload) > 0 {
		payload = []byte(rec.Payload)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO records(domain, key, rev, hash, payload, deleted, updated_at, origin)
VALUES(?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(domain, key) DO UPDATE SET
	rev = excluded.rev,
	hash = excluded.hash,
	payload = excluded.payload,
	deleted = excluded.deleted,
	updated_at = excluded.updated_at,
	origin = excluded.origin`,
		rec.Domain, rec.Key, rev, rec.Hash, payload, boolInt(rec.Deleted),
		updatedAt.Format(time.RFC3339Nano), rec.Origin); err != nil {
		return 0, fmt.Errorf("controlsync: upsert record: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO changes(rev, domain, key, hash, deleted, origin, created_at)
VALUES(?, ?, ?, ?, ?, ?, ?)`,
		rev, rec.Domain, rec.Key, rec.Hash, boolInt(rec.Deleted), rec.Origin,
		now().UTC().Format(time.RFC3339Nano)); err != nil {
		return 0, fmt.Errorf("controlsync: append change: %w", err)
	}
	return rev, nil
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
