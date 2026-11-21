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
INSERT INTO changes(rev, domain, key, hash, payload, deleted, origin, created_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		rev, rec.Domain, rec.Key, rec.Hash, rec.Payload, boolInt(rec.Deleted), rec.Origin, now); err != nil {
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

// Conflict 是一条待裁决冲突的落库形态。推送侧 id 由 change_id 派生、拉取侧按
// (domain,key) 派生，两者重放都不会多出行。
type Conflict struct {
	ID            string
	Domain        string
	Key           string
	LocalPayload  []byte
	RemotePayload []byte
	LocalHash     string
	RemoteHash    string
	// LocalDeleted / RemoteDeleted 区分「这一侧删了记录」与「这一侧载荷为空」：
	// 裁决时前者要落墓碑，后者要写一个空对象。
	LocalDeleted  bool
	RemoteDeleted bool
	CreatedAt     time.Time
}

// LoadConflict 读一条冲突现场（含已裁决的）。
func (s *Store) LoadConflict(ctx context.Context, id string) (Conflict, string, string, bool, error) {
	if s == nil || s.db == nil {
		return Conflict{}, "", "", false, errors.New("controlsync: store closed")
	}
	var (
		c                      Conflict
		localPayload           []byte
		remotePayload          []byte
		localDeleted           int
		remoteDeleted          int
		createdAt              string
		resolvedAt, resolution string
	)
	err := s.db.QueryRowContext(ctx, `
SELECT domain, key, local_payload, remote_payload, local_hash, remote_hash,
       local_deleted, remote_deleted, created_at, resolved_at, resolution
FROM conflicts WHERE id = ?`, id).
		Scan(&c.Domain, &c.Key, &localPayload, &remotePayload, &c.LocalHash, &c.RemoteHash,
			&localDeleted, &remoteDeleted, &createdAt, &resolvedAt, &resolution)
	if errors.Is(err, sql.ErrNoRows) {
		return Conflict{}, "", "", false, nil
	}
	if err != nil {
		return Conflict{}, "", "", false, fmt.Errorf("controlsync: load conflict: %w", err)
	}
	c.ID = id
	c.LocalPayload = localPayload
	c.RemotePayload = remotePayload
	c.LocalDeleted = localDeleted != 0
	c.RemoteDeleted = remoteDeleted != 0
	if ts, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
		c.CreatedAt = ts
	}
	return c, resolvedAt, resolution, true, nil
}

// ClaimConflict 是裁决的 CAS 闸门：只有第一个把 `resolved_at` 从空改成时间的人赢，
// 并发的第二个拿到 0 行、报「已经裁决过」。先占坑再做副作用 —— 反过来两个不同选择
// 会同时落地，两边各写一遍。
func (s *Store) ClaimConflict(ctx context.Context, id, choice string) error {
	if s == nil || s.db == nil {
		return errors.New("controlsync: store closed")
	}
	res, err := s.db.ExecContext(ctx, `
UPDATE conflicts SET resolved_at = ?, resolution = ? WHERE id = ? AND resolved_at = ''`,
		s.now().UTC().Format(time.RFC3339Nano), choice, id)
	if err != nil {
		return fmt.Errorf("controlsync: claim conflict: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("controlsync: claim conflict: %w", err)
	}
	if n == 0 {
		return ErrConflictResolved
	}
	return nil
}

// ReleaseConflict 在副作用失败后把坑退回，让用户可以重试。
func (s *Store) ReleaseConflict(ctx context.Context, id string) {
	if s == nil || s.db == nil {
		return
	}
	_, _ = s.db.ExecContext(ctx,
		`UPDATE conflicts SET resolved_at = '', resolution = '' WHERE id = ?`, id)
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
INSERT INTO conflicts(id, domain, key, local_payload, remote_payload, local_hash, remote_hash,
	local_deleted, remote_deleted, created_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO NOTHING`,
		c.ID, c.Domain, c.Key, c.LocalPayload, c.RemotePayload, c.LocalHash, c.RemoteHash,
		boolInt(c.LocalDeleted), boolInt(c.RemoteDeleted),
		s.now().UTC().Format(time.RFC3339Nano)); err != nil {
		return "", fmt.Errorf("controlsync: insert conflict: %w", err)
	}
	return c.ID, nil
}

// ListOpenConflicts 返回未裁决的冲突，先来的在前（同一毫秒内按 id 稳定排序，
// 免得列表在两次轮询之间跳位）。
func (s *Store) ListOpenConflicts(ctx context.Context) ([]Conflict, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("controlsync: store closed")
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, domain, key, local_payload, remote_payload, local_hash, remote_hash,
       local_deleted, remote_deleted, created_at
FROM conflicts
WHERE resolved_at = ''
ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("controlsync: list conflicts: %w", err)
	}
	defer rows.Close()
	var out []Conflict
	for rows.Next() {
		var (
			c                           Conflict
			localPayload, remotePayload []byte
			localDeleted, remoteDel     int
			createdAt                   string
		)
		if err := rows.Scan(&c.ID, &c.Domain, &c.Key, &localPayload, &remotePayload,
			&c.LocalHash, &c.RemoteHash, &localDeleted, &remoteDel, &createdAt); err != nil {
			return nil, fmt.Errorf("controlsync: scan conflict: %w", err)
		}
		c.LocalPayload = localPayload
		c.RemotePayload = remotePayload
		c.LocalDeleted = localDeleted != 0
		c.RemoteDeleted = remoteDel != 0
		if ts, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
			c.CreatedAt = ts
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("controlsync: list conflicts: %w", err)
	}
	return out, nil
}
