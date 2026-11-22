package controlsync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

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
	if err := s.ensureColumns(ctx, "conflicts", map[string]string{
		"local_deleted":  "local_deleted INTEGER NOT NULL DEFAULT 0",
		"remote_deleted": "remote_deleted INTEGER NOT NULL DEFAULT 0",
	}); err != nil {
		return err
	}
	if err := s.ensureColumns(ctx, "state", map[string]string{
		// 对面主节点是谁：没有它，主节点被重装/换机后边缘会把一台空库当成
		// 「主节点把记录全删了」照单全收。旧库补齐后为空串，下次追平自动记上。
		"primary_instance_id": "primary_instance_id TEXT NOT NULL DEFAULT ''",
		// 追平被拒的原因（非空 = 锁住）。重启后必须读回来：全量重拉一遍再撞上
		// 同一个身份不符，只会重复失败。
		"blocked_reason": "blocked_reason TEXT NOT NULL DEFAULT ''",
	}); err != nil {
		return err
	}
	return s.ensureChangePayloadColumn(ctx)
}

// ensureColumns 给已存在的表补列：`CREATE TABLE IF NOT EXISTS` 对旧表什么都不做，
// 新增列只能在这里 ALTER。幂等，已存在的列跳过。
func (s *Store) ensureColumns(ctx context.Context, table string, want map[string]string) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
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
	for name, ddl := range want {
		if have[name] {
			continue
		}
		if _, err := s.db.ExecContext(ctx,
			`ALTER TABLE `+table+` ADD COLUMN `+ddl); err != nil {
			return err
		}
	}
	return nil
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

func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "SQLITE_BUSY") ||
		strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "database table is locked")
}
