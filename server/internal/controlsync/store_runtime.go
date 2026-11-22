package controlsync

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ListRecords 返回某域副本里的全部记录（含载荷与墓碑），按 key 排序。
//
// 交接（边缘进入 offline-local）要按这份结果把副本整体写进权威存储：
// RecordStates 只有 hash，写不回去。
func (s *Store) ListRecords(ctx context.Context, domain string) ([]Record, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("controlsync: store closed")
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT domain, key, hash, payload, deleted, updated_at, origin
FROM records WHERE domain = ? ORDER BY key`, domain)
	if err != nil {
		return nil, fmt.Errorf("controlsync: list records: %w", err)
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		var (
			rec       Record
			payload   []byte
			deleted   int
			updatedAt string
		)
		if err := rows.Scan(&rec.Domain, &rec.Key, &rec.Hash, &payload, &deleted, &updatedAt, &rec.Origin); err != nil {
			return nil, fmt.Errorf("controlsync: scan record: %w", err)
		}
		if len(payload) > 0 {
			rec.Payload = payload
		}
		rec.Deleted = deleted != 0
		if ts, err := time.Parse(time.RFC3339Nano, updatedAt); err == nil {
			rec.UpdatedAt = ts
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("controlsync: list records: %w", err)
	}
	return out, nil
}

// PendingKeys 返回某域里还有待回传改动的 key 集合。
func (s *Store) PendingKeys(ctx context.Context, domain string) (map[string]bool, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("controlsync: store closed")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT key FROM pending WHERE domain = ?`, domain)
	if err != nil {
		return nil, fmt.Errorf("controlsync: pending keys: %w", err)
	}
	defer rows.Close()
	out := make(map[string]bool)
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("controlsync: scan pending key: %w", err)
		}
		out[key] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("controlsync: pending keys: %w", err)
	}
	return out, nil
}
