package kanban

import (
	"context"
	"strings"
	"time"
)

// 配额挂起（Plan 2026-11-20 Task 1）。撞到上游用量上限不是节点的错：立刻重试只会在
// 几百毫秒内再次 429，两次重试预算就此烧光，之后整轮只能停给用户。所以这类失败改为
// 把节点挂到重置时刻（NotBefore），由 coordinate 每拍扫描唤醒；挂起不消耗重试预算。

// nodeRunHoldUntil 报告这一行是否仍处在挂起窗口内。空值、非法时间、非 queued 行
// 都算「没有挂起」——已经跑过或已经失败的行不受影响。
func nodeRunHoldUntil(nr NodeRun) (time.Time, bool) {
	raw := strings.TrimSpace(nr.NotBefore)
	if raw == "" || nr.Status != NodeRunQueued {
		return time.Time{}, false
	}
	at, err := time.Parse(time.RFC3339, raw)
	if err != nil || !at.After(time.Now().UTC()) {
		return time.Time{}, false
	}
	return at, true
}

// quotaHoldUntil 从错误摘要里取上游给的重置时刻，取不到就挂一个固定时长。
// 结果必须落在 now+1m … now+7d：更早说明时间戳已经过去、更晚多半是把别的日期
// 当成了重置点，两种都退回 hold 本身，避免一挂几天或原地打转。
func quotaHoldUntil(summary string, now time.Time, hold time.Duration) time.Time {
	now = now.UTC()
	if hold <= 0 {
		hold = defaultSupervisionQuotaHold
	}
	fallback := now.Add(hold)
	lowered := strings.ToLower(summary)
	for _, mark := range []string{"resets at", "reset at", "resets on", "重置于", "重置时间"} {
		idx := strings.Index(lowered, mark)
		if idx < 0 {
			continue
		}
		rest := strings.TrimSpace(summary[idx+len(mark):])
		rest = strings.TrimLeft(rest, ":：")
		rest = strings.TrimSpace(rest)
		if len(rest) > 40 {
			rest = rest[:40]
		}
		if at, ok := parseQuotaResetTime(rest, now); ok && inQuotaHoldWindow(at, now) {
			return at
		}
	}
	return fallback
}

// parseQuotaResetTime 只认 RFC3339 与当日 HH:MM；其余格式宁可放弃也不猜。
func parseQuotaResetTime(text string, now time.Time) (time.Time, bool) {
	text = strings.TrimSpace(text)
	if at, err := time.Parse(time.RFC3339, text); err == nil {
		return at.UTC(), true
	}
	if len(text) > 0 {
		// 时间戳后面可能还跟着别的句子，取第一个 token 再试一次。
		token := text
		if i := strings.IndexAny(token, " \t,;，；。("); i > 0 {
			token = token[:i]
		}
		if at, err := time.Parse(time.RFC3339, token); err == nil {
			return at.UTC(), true
		}
		if len(token) == 5 {
			if at, err := time.Parse("15:04", token); err == nil {
				candidate := time.Date(now.Year(), now.Month(), now.Day(), at.Hour(), at.Minute(), 0, 0, time.UTC)
				if candidate.Before(now) {
					candidate = candidate.Add(24 * time.Hour)
				}
				return candidate, true
			}
		}
	}
	return time.Time{}, false
}

func inQuotaHoldWindow(at, now time.Time) bool {
	return !at.Before(now.Add(time.Minute)) && !at.After(now.Add(7*24*time.Hour))
}

// releaseDueNodeHolds 清掉到期的挂起并返回可以唤醒的 RunID。终态任务不唤醒：
// 用户已经取消或收尾的 Run 不能因为一行旧配额被拉回运行。调用方拿到 RunID 后
// 走 WakeWorkflowRun，driver 会把节点当普通 queued 行执行。
func (s *Service) releaseDueNodeHolds(ctx context.Context, store *TaskStore, now time.Time) ([]string, error) {
	rows, err := store.db.QueryContext(ctx, `
		SELECT nr.id, nr.run_id, t.status
		FROM workflow_node_runs nr
		JOIN workflow_runs wr ON wr.id = nr.run_id
		JOIN tasks t ON t.id = wr.task_id
		WHERE nr.not_before <> '' AND nr.status = ? AND nr.not_before <= ?
		ORDER BY nr.run_id, nr.id`, NodeRunQueued, now.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	// 先把结果全部读出来再关游标：SQLite 单连接下，边读游标边写会自锁死等。
	type heldRun struct{ id, runID, taskStatus string }
	var due []heldRun
	for rows.Next() {
		var row heldRun
		if err := rows.Scan(&row.id, &row.runID, &row.taskStatus); err != nil {
			return nil, err
		}
		due = append(due, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	var woken []string
	seen := map[string]bool{}
	for _, row := range due {
		if !isActiveTaskStatus(row.taskStatus) {
			continue
		}
		if _, err := store.db.ExecContext(ctx,
			`UPDATE workflow_node_runs SET not_before='', updated_at=? WHERE id=? AND not_before<>''`,
			now.UTC().Format(time.RFC3339Nano), row.id); err != nil {
			return nil, err
		}
		if !seen[row.runID] {
			seen[row.runID] = true
			woken = append(woken, row.runID)
		}
	}
	return woken, nil
}

// supervisionRetriesUsed 数这个节点真正用掉的重试预算。配额挂起产生的 attempt
// 不算：它们的前一行是按 quota 分类失败的，节点本身没跑过第二次。
func supervisionRetriesUsed(rows []NodeRun, nodeID string) int {
	byID := make(map[string]NodeRun, len(rows))
	for _, nr := range rows {
		byID[nr.ID] = nr
	}
	maxAttempt, quota := 0, 0
	for _, nr := range rows {
		if nr.NodeID != nodeID || nr.LoopPath != "" {
			continue
		}
		if nr.Attempt > maxAttempt {
			maxAttempt = nr.Attempt
		}
		if nr.PrevAttemptID == "" {
			continue
		}
		if prev, ok := byID[nr.PrevAttemptID]; ok {
			if kind, _ := classifySupervisionFailure(prev); kind == SupervisionFailureQuota {
				quota++
			}
		}
	}
	if used := maxAttempt - 1 - quota; used > 0 {
		return used
	}
	return 0
}
