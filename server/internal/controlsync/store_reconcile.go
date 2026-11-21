package controlsync

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// 一页合并落库需要的全部内容。整页一个事务：records 更新、pending 清理、冲突落库、
// 水位推进同生共死 —— 只推进水位不写记录，那一页的远端内容就永远丢了；
// 只写记录不推进水位，下一页会从同一位置重来。
type ReconcilePage struct {
	// Records 是要写进 records 的远端记录（applied 与 converged 两种结论）。
	Records []RemoteChange
	// ClearPending 是已收敛、可以清掉的本地待推送记录。
	ClearPending []PendingRef
	// Conflicts 是两侧都改过的记录（保留两边，等用户裁决）。
	Conflicts []Conflict
	// BaseRev 是本页最大 rev；0 表示不推进水位。
	BaseRev int64
}

// PendingRef 是 pending 表的主键。
type PendingRef struct {
	Domain string
	Key    string
}

// ApplyReconcilePage 把一页合并结论落库。已有水位之后的记录才会被写，避免服务端
// 重发旧页时把已经处理过的内容再写一遍。
func (s *Store) ApplyReconcilePage(ctx context.Context, page ReconcilePage) error {
	if s == nil || s.db == nil {
		return errors.New("controlsync: store closed")
	}
	if len(page.Records) == 0 && len(page.ClearPending) == 0 && len(page.Conflicts) == 0 && page.BaseRev == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("controlsync: begin: %w", err)
	}
	var baseRev int64
	if err := tx.QueryRowContext(ctx, `
SELECT COALESCE((SELECT base_rev FROM state WHERE id = 1), 0)`).Scan(&baseRev); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("controlsync: read base rev: %w", err)
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	for _, rec := range page.Records {
		if rec.Rev <= baseRev {
			continue
		}
		if err := ValidateRecord(Record{Domain: rec.Domain, Key: rec.Key, Payload: rec.Payload}); err != nil {
			_ = tx.Rollback()
			return err
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
			rec.Domain, rec.Key, rec.Rev, rec.Hash, rec.Payload, boolInt(rec.Deleted), now, rec.Origin); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("controlsync: reconcile upsert record: %w", err)
		}
	}
	for _, ref := range page.ClearPending {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM pending WHERE domain = ? AND key = ?`, ref.Domain, ref.Key); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("controlsync: reconcile clear pending: %w", err)
		}
	}
	for _, c := range page.Conflicts {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO conflicts(id, domain, key, local_payload, remote_payload, local_hash, remote_hash, created_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	remote_payload = excluded.remote_payload,
	remote_hash = excluded.remote_hash`,
			c.ID, c.Domain, c.Key, c.LocalPayload, c.RemotePayload, c.LocalHash, c.RemoteHash, now); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("controlsync: reconcile upsert conflict: %w", err)
		}
	}
	if page.BaseRev > baseRev {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO state(id, base_rev) VALUES(1, ?)
ON CONFLICT(id) DO UPDATE SET base_rev = excluded.base_rev`, page.BaseRev); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("controlsync: reconcile advance base rev: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("controlsync: commit: %w", err)
	}
	return nil
}

// PullConflictID 是拉取侧冲突的稳定 id：同一 (domain,key) 反复拉到的冲突只占一行，
// 只刷新「主节点现在是什么」那一侧 —— 若按 rev 生成 id，一次断网重连能堆出一串
// 指向同一对值的冲突卡。
func PullConflictID(domain, key string) string {
	return "pull:" + domain + ":" + key
}
