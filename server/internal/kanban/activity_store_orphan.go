package kanban

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// 这一组查询只服务「重启后没人推进的直连回复」：run_id 为空、仍停在
// pending/running 的 agent 回复。绑定到 workflow run 的回复走 Run 侧结算
// （settleWorkflowReplyUnknown），两条路径按 run_id 分工，互不重叠。

// ListOrphanDirectReplies 返回所有 run_id 为空、仍停在 pending/running 的回复行。
// 是否真的无人推进要由调用方用会话活跃位判定 —— 这里只回答「谁是候选」。
func (s *ActivityStore) ListOrphanDirectReplies(ctx context.Context) ([]*TaskActivity, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+activitySelectColumns+` FROM task_activities
		 WHERE type = ? AND status IN (?, ?) AND COALESCE(run_id, '') = ''
		 ORDER BY created_at ASC, rowid ASC`,
		ActivityTypeAgentReply, ActivityStatusPending, ActivityStatusRunning)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []*TaskActivity{}
	for rows.Next() {
		activity, err := scanActivityRecord(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, activity)
	}
	return items, rows.Err()
}

// ListLiveDirectReplies 返回某任务上 run_id 为空、仍停在 pending/running 的回复行。
// 判据与 ListOrphanDirectReplies 相同，只是把范围收窄到一个任务：人工「终止」只
// 结算这一任务自己的在飞回复，不能顺手把别的任务的活也收掉。
func (s *ActivityStore) ListLiveDirectReplies(ctx context.Context, taskID string) ([]*TaskActivity, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+activitySelectColumns+` FROM task_activities
		 WHERE task_id = ? AND type = ? AND status IN (?, ?) AND COALESCE(run_id, '') = ''
		 ORDER BY created_at ASC, rowid ASC`,
		strings.TrimSpace(taskID), ActivityTypeAgentReply, ActivityStatusPending, ActivityStatusRunning)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []*TaskActivity{}
	for rows.Next() {
		activity, err := scanActivityRecord(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, activity)
	}
	return items, rows.Err()
}

// ListLiveRepliesByRun 返回绑在某条 Run 上、仍停在 pending/running 的回复行。
// 用于 legacy 审计 Run 的收尾：那类回复 run_id 不为空，直连孤儿查询按设计收不到。
func (s *ActivityStore) ListLiveRepliesByRun(ctx context.Context, runID string) ([]*TaskActivity, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+activitySelectColumns+` FROM task_activities
		 WHERE type = ? AND status IN (?, ?) AND run_id = ?
		 ORDER BY created_at ASC, rowid ASC`,
		ActivityTypeAgentReply, ActivityStatusPending, ActivityStatusRunning, strings.TrimSpace(runID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []*TaskActivity{}
	for rows.Next() {
		activity, err := scanActivityRecord(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, activity)
	}
	return items, rows.Err()
}

// LiveReplyExists 报这个任务上还有没有停在 pending/running 的回复行。用于判断
// 「结算掉一条陈旧直连回复之后，任务是不是真的空闲了」。
func (s *ActivityStore) LiveReplyExists(ctx context.Context, taskID string) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM task_activities
		 WHERE task_id = ? AND type = ? AND status IN (?, ?) LIMIT 1`,
		strings.TrimSpace(taskID), ActivityTypeAgentReply, ActivityStatusPending, ActivityStatusRunning).Scan(&exists)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// SettleOrphanReply 把停在 pending/running 的回复收成 cancelled，并写入可读说明。
// 条件更新（status 必须是 pending/running）保证它不会覆盖期间完成的回合；返回
// changed=false 表示这一行已经被别的路径结算过，调用方不得再广播或改任务状态。
func (s *ActivityStore) SettleOrphanReply(ctx context.Context, id, summary string) (*TaskActivity, bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, false, errors.New("activity id required")
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE task_activities SET status = ?, summary = ?, updated_at = ?
		 WHERE id = ? AND status IN (?, ?)`,
		ActivityStatusCancelled, summary, time.Now().UTC().Format(time.RFC3339Nano),
		id, ActivityStatusPending, ActivityStatusRunning)
	if err != nil {
		return nil, false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, false, err
	}
	if affected == 0 {
		return nil, false, nil
	}
	activity, err := s.Get(ctx, id)
	if err != nil {
		return nil, false, err
	}
	return activity, true, nil
}
