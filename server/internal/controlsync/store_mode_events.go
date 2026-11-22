package controlsync

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ModeEvent 是一次模式迁移留下的可见事件（spec §7：降级/升级都要留痕）。
//
// 它同时是横幅的数据源：用户看到的「什么时候变的、为什么」来自这张表，而不是
// 前端的本地记忆 —— 换台浏览器、刷新页面之后仍然是同一份历史。
type ModeEvent struct {
	ID     string    `json:"id"`
	From   Mode      `json:"from"`
	To     Mode      `json:"to"`
	Reason string    `json:"reason,omitempty"`
	At     time.Time `json:"at"`
}

// ModeEventID 由迁移本身决定事件 id：同一对 (from,to,at) 永远得到同一个 id，
// 于是「重放同一次迁移」不会在界面上变成两条通知（见 RecordModeEvent 的 INSERT OR IGNORE）。
//
// 时间截到毫秒：状态机每次迁移都要经过一次探测循环，同毫秒内重复同一对不现实。
func ModeEventID(from, to Mode, at time.Time) string {
	return fmt.Sprintf("mode:%s->%s:%d", from, to, at.UTC().UnixMilli())
}

// RecordModeEvent 落一条迁移事件，返回是否真的新写入（重复即 false）。
//
// 幂等靠 id + INSERT OR IGNORE 在库层保证，不靠调用方自觉：崩溃重启、并发收尾
// 都可能把同一次迁移再报一遍，界面上「又断线了」重复弹比漏报更伤信任。
func (s *Store) RecordModeEvent(ctx context.Context, ev ModeEvent) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("controlsync: store closed")
	}
	res, err := s.db.ExecContext(ctx, `
INSERT OR IGNORE INTO mode_events(id, from_mode, to_mode, reason, at) VALUES(?, ?, ?, ?, ?)`,
		ModeEventID(ev.From, ev.To, ev.At), string(ev.From), string(ev.To), ev.Reason,
		ev.At.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return false, fmt.Errorf("controlsync: record mode event: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		// 驱动不报影响行数时按「已写入」处理：宁可多认一条，也不把成功写成失败。
		return true, nil
	}
	return n > 0, nil
}

// ListModeEvents 按时间倒序返回最近 limit 条迁移事件（最新在前）。
func (s *Store) ListModeEvents(ctx context.Context, limit int) ([]ModeEvent, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("controlsync: store closed")
	}
	if limit <= 0 || limit > 50 {
		limit = 5
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, from_mode, to_mode, reason, at FROM mode_events
ORDER BY at DESC, rowid DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("controlsync: list mode events: %w", err)
	}
	defer rows.Close()
	var out []ModeEvent
	for rows.Next() {
		var (
			ev   ModeEvent
			from string
			to   string
			at   string
		)
		if err := rows.Scan(&ev.ID, &from, &to, &ev.Reason, &at); err != nil {
			return nil, fmt.Errorf("controlsync: scan mode event: %w", err)
		}
		ev.From, ev.To = Mode(from), Mode(to)
		if ts, err := time.Parse(time.RFC3339Nano, at); err == nil {
			ev.At = ts
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("controlsync: list mode events: %w", err)
	}
	return out, nil
}
