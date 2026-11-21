package controlsync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// PendingChange 是一条「离线期间本地改过、还没被主节点确认」的记录（spec §4.3）。
//
// BaseHash 是这条记录在主节点上的**祖先值**（边缘最后一次见到的值），三方比对全靠它；
// ChangeID 是这次待推送变更的身份，重推时主节点按它做幂等。
type PendingChange struct {
	ChangeID  string
	Domain    string
	Key       string
	BaseHash  string
	Payload   []byte
	Deleted   bool
	CreatedAt time.Time
}

// PutPending 记下一条待推送变更。
//
// 同一 (domain,key) 离线期间改多次只保留最后一份载荷 —— 中间值没有意义，
// 也没人等着推送它。但 BaseHash 与 ChangeID 保持**第一次**那次的值：
// 祖先值不会因为用户又改了一版而变，ChangeID 变了则主节点会把同一批改动当新变更。
func (s *Store) PutPending(ctx context.Context, p PendingChange) error {
	if s == nil || s.db == nil {
		return errors.New("controlsync: store closed")
	}
	if err := ValidateRecord(Record{Domain: p.Domain, Key: p.Key}); err != nil {
		return err
	}
	if p.ChangeID == "" {
		return errors.New("controlsync: pending change_id required")
	}
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO pending(domain, key, change_id, base_hash, payload, deleted, created_at)
VALUES(?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(domain, key) DO UPDATE SET
	payload = excluded.payload,
	deleted = excluded.deleted`,
		p.Domain, p.Key, p.ChangeID, p.BaseHash, p.Payload, boolInt(p.Deleted),
		s.now().UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("controlsync: put pending: %w", err)
	}
	return nil
}

// PendingChange 读一条待推送变更；第二个返回值表示是否存在。
func (s *Store) PendingChange(ctx context.Context, domain, key string) (PendingChange, bool, error) {
	if s == nil || s.db == nil {
		return PendingChange{}, false, errors.New("controlsync: store closed")
	}
	var (
		p         PendingChange
		payload   []byte
		deleted   int
		createdAt string
	)
	err := s.db.QueryRowContext(ctx, `
SELECT change_id, domain, key, base_hash, payload, deleted, created_at
FROM pending WHERE domain = ? AND key = ?`, domain, key).
		Scan(&p.ChangeID, &p.Domain, &p.Key, &p.BaseHash, &payload, &deleted, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PendingChange{}, false, nil
	}
	if err != nil {
		return PendingChange{}, false, fmt.Errorf("controlsync: get pending: %w", err)
	}
	p.Deleted = deleted != 0
	if len(payload) > 0 {
		p.Payload = payload
	}
	if ts, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
		p.CreatedAt = ts
	}
	return p, true, nil
}

// DeletePending 清掉一条待推送变更（已收敛或已被裁决）。
func (s *Store) DeletePending(ctx context.Context, domain, key string) error {
	if s == nil || s.db == nil {
		return errors.New("controlsync: store closed")
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM pending WHERE domain = ? AND key = ?`, domain, key); err != nil {
		return fmt.Errorf("controlsync: delete pending: %w", err)
	}
	return nil
}

// CountPending 返回待推送变更条数（状态接口与测试用）。
func (s *Store) CountPending(ctx context.Context) (int, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("controlsync: store closed")
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pending`).Scan(&n); err != nil {
		return 0, fmt.Errorf("controlsync: count pending: %w", err)
	}
	return n, nil
}
