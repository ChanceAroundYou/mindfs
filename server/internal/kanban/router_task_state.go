package kanban

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// out-of-band 标记的持久化。原先它只在 Service 的进程内 map 里
// （service_session_follow.go），进程一重启就丢：用户在会话视图里推的那一轮
// 永远不会再触发收尾，任务卡停在 running 转圈（C2，用户 2026-11-23 裁定）。
//
// 这里只负责这一行状态，不判断该不该恢复 —— 恢复与事件在
// service_recover_out_of_band.go。

// outOfBandState 是一条 out-of-band 标记的读回形状。
type outOfBandState struct {
	Active     bool
	SessionKey string
	StartedAt  time.Time
}

// MarkOutOfBand 记下「这个任务正被一轮 out-of-band 会话回合推进」，并刷新
// 开始时间。同一个任务只有一轮 OOB 回合（任务停在 waiting_user 时才会被
// 认领），所以后写覆盖前写是安全的。
func (s *RouterStore) MarkOutOfBand(ctx context.Context, rootID, taskID, sessionKey string, startedAt time.Time) error {
	if s == nil || s.db == nil {
		return nil
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil
	}
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	now := formatRouterTime(startedAt)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO router_task_state (task_id, root_id, out_of_band, out_of_band_started_at, out_of_band_session_key, updated_at)
VALUES (?, ?, 1, ?, ?, ?)
ON CONFLICT(task_id) DO UPDATE SET
  root_id = excluded.root_id,
  out_of_band = 1,
  out_of_band_started_at = excluded.out_of_band_started_at,
  out_of_band_session_key = excluded.out_of_band_session_key,
  updated_at = excluded.updated_at`,
		taskID, strings.TrimSpace(rootID), now, strings.TrimSpace(sessionKey), now)
	return err
}

// ClearOutOfBand 清掉标记。回合结束、任务已经不在 running、或恢复完成都要调。
func (s *RouterStore) ClearOutOfBand(ctx context.Context, taskID string) error {
	if s == nil || s.db == nil {
		return nil
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
UPDATE router_task_state
SET out_of_band = 0, out_of_band_started_at = '', out_of_band_session_key = '', updated_at = ?
WHERE task_id = ?`, formatRouterTime(time.Now().UTC()), taskID)
	return err
}

// LoadOutOfBand 读回标记。没有这一行就是「没有进行中的 OOB 回合」，不是错误。
func (s *RouterStore) LoadOutOfBand(ctx context.Context, taskID string) (outOfBandState, error) {
	if s == nil || s.db == nil {
		return outOfBandState{}, nil
	}
	var active int
	var sessionKey, startedAt string
	err := s.db.QueryRowContext(ctx, `
SELECT out_of_band, out_of_band_session_key, out_of_band_started_at
FROM router_task_state
WHERE task_id = ?`, strings.TrimSpace(taskID)).Scan(&active, &sessionKey, &startedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return outOfBandState{}, nil
		}
		return outOfBandState{}, err
	}
	state := outOfBandState{Active: active != 0, SessionKey: sessionKey}
	if parsed, parseErr := time.Parse(routerTimeLayout, startedAt); parseErr == nil {
		state.StartedAt = parsed.UTC()
	}
	return state, nil
}

// ListStaleOutOfBand 列出 started_at 不晚于 before 的进行中 OOB 标记。按
// started_at 升序，先处理最老的。
func (s *RouterStore) ListStaleOutOfBand(ctx context.Context, rootID string, before time.Time) ([]outOfBandTask, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT task_id, out_of_band_session_key, out_of_band_started_at
FROM router_task_state
WHERE root_id = ? AND out_of_band = 1 AND out_of_band_started_at <> '' AND out_of_band_started_at <= ?
ORDER BY out_of_band_started_at ASC`, strings.TrimSpace(rootID), formatRouterTime(before))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []outOfBandTask
	for rows.Next() {
		var item outOfBandTask
		var startedAt string
		if err := rows.Scan(&item.TaskID, &item.SessionKey, &startedAt); err != nil {
			return nil, err
		}
		if parsed, parseErr := time.Parse(routerTimeLayout, startedAt); parseErr == nil {
			item.StartedAt = parsed.UTC()
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// outOfBandTask 是一条待恢复的 OOB 标记。
type outOfBandTask struct {
	TaskID     string
	SessionKey string
	StartedAt  time.Time
}
