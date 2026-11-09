// WP-03 恢复（A3）：单节点重启只承诺原 Task 所属节点 + 本地 run + 原 session。

package kanban

import (
	"context"
	"database/sql"
	"strings"
)

// RecoveryPlan 是一次启动恢复的结论。
type RecoveryPlan struct {
	// ResumeSession 非空 = 可确定续跑原 session（可确定无重复时）。
	ResumeSession string
	// UnknownIDs 是无法判定执行状态、需要用户处理的请求 id 列表。
	UnknownIDs []string
	// NeedsUser 为 true 时任务进 needs-user，不得自动重发 prompt、
	// 不得自动建 attempt。
	NeedsUser bool
}

// SessionStateProbe 查询原 session/turn 状态：
//   - found=true,  resumable=true  → 可确定无重复续跑原 session
//   - found=true,  resumable=false → 会话在但无法确定，unknown/needs-user
//   - found=false                   → 会话不可达，unknown/needs-user
type SessionStateProbe interface {
	Probe(sessionKey string) (found bool, resumable bool)
}

// RecoverOnStartup 读取持久的 run/node/request 状态，再查 adapter
// session/turn 状态。可确定时续跑原 session；否则标 unknown/needs-user，
// 保留原 Comment/ContinueRequest/NodeRun/transcript 证据，不重发 prompt、
// 不自动建 attempt。
func RecoverOnStartup(ctx context.Context, store *TaskStore, probe SessionStateProbe) (RecoveryPlan, error) {
	if err := EnsureOutboxTables(ctx, store.db); err != nil {
		return RecoveryPlan{}, err
	}
	rows, err := store.db.QueryContext(ctx,
		`SELECT id, task_id, node_run_id, session_key, status FROM continue_requests WHERE status IN ('delivery_pending','steer_sent','steer_pending','steer_confirmed','recorded','queued') ORDER BY created_at ASC`)
	if err != nil {
		return RecoveryPlan{}, err
	}
	defer rows.Close()
	type pending struct {
		id, taskID, nodeRunID, sessionKey, status string
	}
	var pendings []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.taskID, &p.nodeRunID, &p.sessionKey, &p.status); err != nil {
			return RecoveryPlan{}, err
		}
		pendings = append(pendings, p)
	}
	if err := rows.Err(); err != nil {
		return RecoveryPlan{}, err
	}
	plan := RecoveryPlan{}
	for _, p := range pendings {
		if strings.TrimSpace(p.sessionKey) == "" {
			plan.UnknownIDs = append(plan.UnknownIDs, p.id)
			plan.NeedsUser = true
			_ = markUnknown(ctx, store, p.id)
			continue
		}
		if probe == nil {
			// 没有探针时无法证明可续接 → unknown，不得自动续跑。
			plan.UnknownIDs = append(plan.UnknownIDs, p.id)
			plan.NeedsUser = true
			_ = markUnknown(ctx, store, p.id)
			continue
		}
		found, resumable := probe.Probe(p.sessionKey)
		if found && resumable {
			// 可确定无重复 → 续跑原 session（Go 侧只记录结论，不真起 turn）。
			if plan.ResumeSession == "" {
				plan.ResumeSession = p.sessionKey
			}
			continue
		}
		plan.UnknownIDs = append(plan.UnknownIDs, p.id)
		plan.NeedsUser = true
		_ = markUnknown(ctx, store, p.id)
	}
	return plan, nil
}

func markUnknown(ctx context.Context, store *TaskStore, requestID string) error {
	_, err := store.db.ExecContext(ctx, `UPDATE continue_requests SET status=?, updated_at=? WHERE id=?`, OutboxUnknown, nowUTC().Format("2006-01-02T15:04:05.999999999Z"), requestID)
	if err != nil {
		return err
	}
	_, err = store.db.ExecContext(ctx, `UPDATE outbox_items SET status=?, updated_at=? WHERE request_id=?`, OutboxUnknown, nowUTC().Format("2006-01-02T15:04:05.999999999Z"), requestID)
	return err
}

// GetNodeRun 取 NodeRun 记录（供恢复/新 attempt 链路检查）。
func GetNodeRun(ctx context.Context, store *TaskStore, nodeRunID string) (NodeRunRecord, error) {
	var r NodeRunRecord
	var createdAt, updatedAt string
	err := store.db.QueryRowContext(ctx,
		`SELECT id, task_id, root_id, session_key, status, attempt, created_at, updated_at FROM node_runs WHERE id=?`, nodeRunID).Scan(
		&r.ID, &r.TaskID, &r.RootID, &r.SessionKey, &r.Status, &r.Attempt, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return NodeRunRecord{}, ErrOutboxNotFound
	}
	if err != nil {
		return NodeRunRecord{}, err
	}
	r.CreatedAt = parseOutboxTime(createdAt)
	r.UpdatedAt = parseOutboxTime(updatedAt)
	return r, nil
}
