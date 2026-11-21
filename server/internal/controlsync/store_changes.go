package controlsync

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ListChangesSince 读变更流的一页：rev >= since，按 rev 升序，最多 limit 条。
//
// 载荷取自 change 行自己的 `payload` 列（写变更时复制的快照），所以回放一条旧
// change 拿到的是当时的值，不会被 records 里的当前值污染。
//
// 早期库补列后可能残留「有 hash 没载荷」的活记录行（records 已被再次修改、回填
// 对不上）。这类行跳过不供：它们的键后面必有更新的 change 带着正确载荷，副本
// 靠后续行即可追平，而把空载荷发出去会让 ApplyRemoteChanges 直接失败。
func (s *Store) ListChangesSince(ctx context.Context, since int64, limit int) ([]RemoteChange, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("controlsync: store closed")
	}
	if since < 0 {
		since = 0
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT rev, domain, key, hash, payload, deleted, origin, created_at
FROM changes
WHERE rev >= ? AND (deleted = 1 OR payload IS NOT NULL)
ORDER BY rev
LIMIT ?`, since, limit)
	if err != nil {
		return nil, fmt.Errorf("controlsync: list changes: %w", err)
	}
	defer rows.Close()
	out := make([]RemoteChange, 0, limit)
	for rows.Next() {
		var (
			ch        RemoteChange
			payload   []byte
			deleted   int
			createdAt string
		)
		if err := rows.Scan(&ch.Rev, &ch.Domain, &ch.Key, &ch.Hash, &payload,
			&deleted, &ch.Origin, &createdAt); err != nil {
			return nil, fmt.Errorf("controlsync: scan change: %w", err)
		}
		ch.Deleted = deleted != 0
		if len(payload) > 0 {
			ch.Payload = payload
		}
		if ts, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
			ch.UpdatedAt = ts
		}
		out = append(out, ch)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("controlsync: list changes: %w", err)
	}
	return out, nil
}
