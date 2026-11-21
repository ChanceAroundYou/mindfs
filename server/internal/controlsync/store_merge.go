package controlsync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// GetRecord 读一条记录快照；第二个返回值表示记录是否存在（含墓碑）。
func (s *Store) GetRecord(ctx context.Context, domain, key string) (Record, bool, error) {
	if s == nil || s.db == nil {
		return Record{}, false, errors.New("controlsync: store closed")
	}
	var (
		rec       Record
		payload   []byte
		updatedAt string
		deleted   int
	)
	err := s.db.QueryRowContext(ctx, `
SELECT domain, key, rev, hash, payload, deleted, updated_at, origin
FROM records WHERE domain = ? AND key = ?`, domain, key).
		Scan(&rec.Domain, &rec.Key, &rec.Rev, &rec.Hash, &payload, &deleted, &updatedAt, &rec.Origin)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, fmt.Errorf("controlsync: get record: %w", err)
	}
	rec.Deleted = deleted != 0
	if len(payload) > 0 {
		rec.Payload = payload
	}
	if ts, err := time.Parse(time.RFC3339Nano, updatedAt); err == nil {
		rec.UpdatedAt = ts
	}
	return rec, true, nil
}

// HeadRev 返回变更流最大 rev（没有变更时为 0）。
func (s *Store) HeadRev(ctx context.Context) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("controlsync: store closed")
	}
	var rev int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE((SELECT MAX(rev) FROM changes), 0)`).Scan(&rev); err != nil {
		return 0, fmt.Errorf("controlsync: head rev: %w", err)
	}
	return rev, nil
}

// ChangeApplied 查 change_id 是否已经被应用过（重放幂等的唯一依据）。
func (s *Store) ChangeApplied(ctx context.Context, changeID string) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("controlsync: store closed")
	}
	var one int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM applied_changes WHERE change_id = ?`, changeID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("controlsync: check applied change: %w", err)
	}
	return true, nil
}

// MarkChangeApplied 把一条不产生新变更的结论（已收敛 / 幂等删除）记进 applied_changes。
func (s *Store) MarkChangeApplied(ctx context.Context, changeID string, rev int64) error {
	if s == nil || s.db == nil {
		return errors.New("controlsync: store closed")
	}
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO applied_changes(change_id, rev, applied_at) VALUES(?, ?, ?)
ON CONFLICT(change_id) DO NOTHING`,
		changeID, rev, s.now().UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("controlsync: mark applied: %w", err)
	}
	return nil
}

// PushedRecord 是一次推送落地需要的全部字段。
type PushedRecord struct {
	ChangeID string
	Domain   string
	Key      string
	Hash     string
	Payload  []byte
	Deleted  bool
	Origin   string
}

// ApplyPushedChange 在一个事务里完成「分配新 rev → 写记录 → 写变更流 → 记 applied_changes」。
//
// 四步必须同生共死：只写 records 不写 changes，其他节点永远看不到这次变更；
// 只写 changes 不写 applied_changes，边缘重连后会再推一遍，rev 无意义地增长。
func (s *Store) ApplyPushedChange(ctx context.Context, rec PushedRecord) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("controlsync: store closed")
	}
	if err := ValidateRecord(Record{Domain: rec.Domain, Key: rec.Key, Payload: rec.Payload}); err != nil {
		return 0, err
	}
	if rec.ChangeID == "" {
		return 0, errors.New("controlsync: change_id required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("controlsync: begin: %w", err)
	}
	var rev int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(rev), 0) + 1 FROM changes`).Scan(&rev); err != nil {
		_ = tx.Rollback()
		return 0, fmt.Errorf("controlsync: alloc rev: %w", err)
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
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
		rec.Domain, rec.Key, rev, rec.Hash, rec.Payload, boolInt(rec.Deleted), now, rec.Origin); err != nil {
		_ = tx.Rollback()
		return 0, fmt.Errorf("controlsync: upsert record: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO changes(rev, domain, key, hash, deleted, origin, created_at) VALUES(?, ?, ?, ?, ?, ?, ?)`,
		rev, rec.Domain, rec.Key, rec.Hash, boolInt(rec.Deleted), rec.Origin, now); err != nil {
		_ = tx.Rollback()
		return 0, fmt.Errorf("controlsync: append change: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO applied_changes(change_id, rev, applied_at) VALUES(?, ?, ?)
ON CONFLICT(change_id) DO NOTHING`, rec.ChangeID, rev, now); err != nil {
		_ = tx.Rollback()
		return 0, fmt.Errorf("controlsync: mark applied: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("controlsync: commit: %w", err)
	}
	return rev, nil
}

// Conflict 是一条待裁决冲突的落库形态。id 由推送方 change_id 派生，重放同一批不会多出行。
type Conflict struct {
	ID            string
	Domain        string
	Key           string
	LocalPayload  []byte
	RemotePayload []byte
	LocalHash     string
	RemoteHash    string
}

// InsertConflict 幂等写冲突现场（同一 change_id 重放不新增）。返回冲突 id。
func (s *Store) InsertConflict(ctx context.Context, c Conflict) (string, error) {
	if s == nil || s.db == nil {
		return "", errors.New("controlsync: store closed")
	}
	if c.ID == "" {
		return "", errors.New("controlsync: conflict id required")
	}
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO conflicts(id, domain, key, local_payload, remote_payload, local_hash, remote_hash, created_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO NOTHING`,
		c.ID, c.Domain, c.Key, c.LocalPayload, c.RemotePayload, c.LocalHash, c.RemoteHash,
		s.now().UTC().Format(time.RFC3339Nano)); err != nil {
		return "", fmt.Errorf("controlsync: insert conflict: %w", err)
	}
	return c.ID, nil
}
