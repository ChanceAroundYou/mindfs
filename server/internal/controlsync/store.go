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
	payload BLOB,
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
-- local_seq 给「本地这次改动」发号：change_id 必须随内容前进（见 PutPending），
-- 而主节点按它去重，所以序号只能一条道递增、不能复用。
CREATE TABLE IF NOT EXISTS local_seq (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	seq INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS conflicts (
	id TEXT PRIMARY KEY,
	domain TEXT NOT NULL,
	key TEXT NOT NULL,
	local_payload BLOB,
	remote_payload BLOB,
	local_hash TEXT NOT NULL DEFAULT '',
	remote_hash TEXT NOT NULL DEFAULT '',
	local_deleted INTEGER NOT NULL DEFAULT 0,
	remote_deleted INTEGER NOT NULL DEFAULT 0,
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
	// _txlock=immediate：事务一开始就拿写锁 —— rev 用 MAX(rev)+1 分配，
	// 延迟事务会让两个并发事务读到同一个 MAX，随后在 changes 主键上撞车。
	//
	// journal_mode 不放 DSN：SQLite 不为 journal_mode 切换调用 busy handler，
	// 撞上并发迁移会**立即**返回 SQLITE_BUSY，而 DSN 里的 pragma 在连接建立时执行、
	// 没有第二次机会。改为在 Migrate 里带重试地设一次（模式写在库头里，之后一直有效）。
	dsn := path + "?_pragma=busy_timeout(5000)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("controlsync: open db: %w", err)
	}
	return &Store{db: db, path: path, now: time.Now}, nil
}

// 迁移重试参数：单次等待远小于 busy_timeout，靠「多试几次」把 journal_mode
// 那种不排队的情形也覆盖掉。
const (
	migrateAttempts = 60
	migrateBackoff  = 25 * time.Millisecond
)

// Migrate 建表。幂等：所有语句都是 IF NOT EXISTS，可以每次启动都跑。
//
// 碰到 SQLITE_BUSY 会退避重试：这台库里同时只有一个同步实例在写，但开发/测试、
// 以及误起两个进程时会出现并发迁移，一次 BUSY 就让启动失败太脆。
func (s *Store) Migrate(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("controlsync: store closed")
	}
	var err error
	for attempt := 0; attempt < migrateAttempts; attempt++ {
		if err = s.migrateOnce(ctx); err == nil {
			return nil
		}
		if !isSQLiteBusy(err) {
			return fmt.Errorf("controlsync: migrate: %w", err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("controlsync: migrate: %w", ctx.Err())
		case <-time.After(migrateBackoff):
		}
	}
	return fmt.Errorf("controlsync: migrate: %w", err)
}

func (s *Store) migrateOnce(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, schemaDDL); err != nil {
		return err
	}
	if err := s.ensureConflictDeleteColumns(ctx); err != nil {
		return err
	}
	return s.ensureChangePayloadColumn(ctx)
}

// ensureChangePayloadColumn 给早期建的库补 changes.payload。
//
// 变更流要能独立供给拉取方：只存 hash 的话，handler 得回头 join records 取载荷，
// 而 records 只有「当前值」—— 一条旧 change 会读到后来被覆盖的载荷，追出来的副本
// 与主节点不一致。所以载荷在写变更时复制一份。
//
// 旧库补列之后逐行回填：能用 (domain,key,hash) 在当前 records 里对上的行可以补齐；
// 对不上的（记录已被再次修改）留空，由 ListChangesSince 跳过 —— 它们后面必有更新的
// 一条 change 带着正确载荷，副本照样能追平。
func (s *Store) ensureChangePayloadColumn(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(changes)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	have := false
	for rows.Next() {
		var (
			cid, notNull, pk int
			name, typ        string
			dflt             sql.NullString
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return err
		}
		if name == "payload" {
			have = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !have {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE changes ADD COLUMN payload BLOB`); err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, `
UPDATE changes SET payload = (
	SELECT r.payload FROM records r
	WHERE r.domain = changes.domain AND r.key = changes.key AND r.hash = changes.hash
) WHERE payload IS NULL AND deleted = 0`); err != nil {
			return err
		}
	}
	return nil
}

// ensureConflictDeleteColumns 给早期建的库补两列。
//
// conflict 现场必须能区分「这一侧把记录删了」与「这一侧载荷是空的」：裁决时前者要落
// 墓碑，后者要写空对象。`CREATE TABLE IF NOT EXISTS` 对已存在的表什么都不做，
// 所以旧库靠这里补 —— 幂等，加过的列跳过。
func (s *Store) ensureConflictDeleteColumns(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(conflicts)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	have := map[string]bool{}
	for rows.Next() {
		var (
			cid, notNull, pk int
			name, typ        string
			dflt             sql.NullString
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return err
		}
		have[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, col := range []string{"local_deleted", "remote_deleted"} {
		if have[col] {
			continue
		}
		if _, err := s.db.ExecContext(ctx,
			`ALTER TABLE conflicts ADD COLUMN `+col+` INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	return nil
}

func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "SQLITE_BUSY") ||
		strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "database table is locked")
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
	revs, err := s.WriteRecords(ctx, []Record{rec})
	if err != nil {
		return 0, err
	}
	return revs[0], nil
}

// WriteRecords 在一个事务里写入一批记录，逐条分配 rev。
//
// 一次采集的 diff 必须整批落地：只写一半时，边缘会拉到「部分变更」，
// 而采集器的基线已经按「全部写完」推进，剩下那半被跳过。
func (s *Store) WriteRecords(ctx context.Context, recs []Record) ([]int64, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("controlsync: store closed")
	}
	for _, rec := range recs {
		if err := ValidateRecord(rec); err != nil {
			return nil, err
		}
		if strings.TrimSpace(rec.Hash) == "" {
			return nil, errors.New("controlsync: record hash required")
		}
	}
	if len(recs) == 0 {
		return nil, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("controlsync: begin: %w", err)
	}
	revs := make([]int64, 0, len(recs))
	for _, rec := range recs {
		rev, err := writeRecordTx(ctx, tx, rec, s.now)
		if err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		revs = append(revs, rev)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("controlsync: commit: %w", err)
	}
	return revs, nil
}

// RecordState 是一条记录在索引库里的当前状态（不含载荷）。
type RecordState struct {
	Hash    string
	Deleted bool
	Rev     int64
}

// RecordStates 读出某域全部记录的当前状态。采集器用它恢复基线：进程重启后
// 不该把整库当成新数据重新发一遍。调用方只按 key 查表，所以返回 map。
func (s *Store) RecordStates(ctx context.Context, domain string) (map[string]RecordState, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("controlsync: store closed")
	}
	if !domainPattern.MatchString(domain) {
		return nil, fmt.Errorf("controlsync: invalid domain %q", domain)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT key, hash, deleted, rev FROM records WHERE domain = ?`, domain)
	if err != nil {
		return nil, fmt.Errorf("controlsync: read records: %w", err)
	}
	defer rows.Close()
	out := map[string]RecordState{}
	for rows.Next() {
		var key string
		var state RecordState
		var deleted int
		if err := rows.Scan(&key, &state.Hash, &deleted, &state.Rev); err != nil {
			return nil, fmt.Errorf("controlsync: scan record: %w", err)
		}
		state.Deleted = deleted != 0
		out[key] = state
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("controlsync: read records: %w", err)
	}
	return out, nil
}

// BaseRev 返回边缘副本的水位（主节点上已追到哪一条 change）。没有 state 行时为 0。
func (s *Store) BaseRev(ctx context.Context) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("controlsync: store closed")
	}
	var rev int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE((SELECT base_rev FROM state WHERE id = 1), 0)`).Scan(&rev); err != nil {
		return 0, fmt.Errorf("controlsync: read base_rev: %w", err)
	}
	return rev, nil
}

// ApplyRemoteChanges 在一个事务里把一页主节点变更写进本地副本并推进 base_rev。
//
// 三条约束，都是为了让「失败不丢变更」成立：
//   - rev 沿用主节点序号，不在本地重分配 —— 它是水位，不是本地自增；
//   - rev <= 当前水位的记录跳过，重放同一页不会写第二遍（幂等）；
//   - records 与 base_rev 同生共死：半个事务提交出去，边缘就会跳过没落地的记录。
func (s *Store) ApplyRemoteChanges(ctx context.Context, changes []RemoteChange) error {
	if s == nil || s.db == nil {
		return errors.New("controlsync: store closed")
	}
	for _, ch := range changes {
		if err := ValidateRecord(Record{Domain: ch.Domain, Key: ch.Key, Payload: ch.Payload}); err != nil {
			return err
		}
		if ch.Rev <= 0 {
			return fmt.Errorf("controlsync: invalid change rev %d for %s/%s", ch.Rev, ch.Domain, ch.Key)
		}
		if strings.TrimSpace(ch.Hash) == "" {
			return fmt.Errorf("controlsync: change hash required for %s/%s", ch.Domain, ch.Key)
		}
	}
	if len(changes) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("controlsync: begin: %w", err)
	}
	var baseRev int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE((SELECT base_rev FROM state WHERE id = 1), 0)`).Scan(&baseRev); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("controlsync: read base_rev: %w", err)
	}
	newRev := baseRev
	for _, ch := range changes {
		if ch.Rev <= baseRev {
			continue
		}
		updatedAt := ch.UpdatedAt
		if updatedAt.IsZero() {
			updatedAt = s.now()
		}
		var payload any
		if len(ch.Payload) > 0 {
			payload = []byte(ch.Payload)
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
			ch.Domain, ch.Key, ch.Rev, ch.Hash, payload, boolInt(ch.Deleted),
			updatedAt.UTC().Format(time.RFC3339Nano), ch.Origin); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("controlsync: upsert replica record: %w", err)
		}
		if ch.Rev > newRev {
			newRev = ch.Rev
		}
	}
	if newRev > baseRev {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO state(id, base_rev) VALUES(1, ?)
ON CONFLICT(id) DO UPDATE SET base_rev = excluded.base_rev`, newRev); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("controlsync: advance base_rev: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("controlsync: commit: %w", err)
	}
	return nil
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
INSERT INTO changes(rev, domain, key, hash, payload, deleted, origin, created_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		rev, rec.Domain, rec.Key, rec.Hash, payload, boolInt(rec.Deleted), rec.Origin,
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
