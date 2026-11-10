// WP-03 Steer / Outbox / FIFO queue / durable recovery (A3).
//
// A1 的 task_activities / continue_requests 表尚未合入；本文件在 TaskStore
// 内部自建兼容的三张表：
//   - continue_requests: 每条留言对应的幂等执行请求（(task_id, comment_id) 唯一）
//   - node_runs:         NodeRun 持久化记录（session 绑定 + 状态）
//   - outbox_items:      先提交 ContinueRequest/NodeRun、再由 worker 消费的持久队列
//
// A1 合入后，这些表应与 A1 的 schema 对齐（A1 的 migrate 应以 CREATE TABLE IF
// NOT EXISTS 幂等、无破坏性改名）。本文件只在 TaskStore.db 上建表，不改
// 既有 tables / tasks / stage_runs / task_events。
package kanban

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// EnsureOutboxTables 建/兼容三张表。幂等。
//
// continue_requests 的 canonical DDL 属于 WP-01 的 migrateComments（request_id 主键）。
// 这里必须写同一份定义：两处都 CREATE TABLE IF NOT EXISTS，谁先执行谁定表结构 ——
// 如果这里还写 id 主键，一旦它先跑，后续所有按 request_id 的查询（A1 留言、A3 队列、
// 启动恢复）都会因列不存在而失败。写成同一份就没有先后依赖。
func EnsureOutboxTables(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS continue_requests (
	request_id TEXT PRIMARY KEY,
	task_id TEXT NOT NULL,
	comment_id TEXT NOT NULL,
	root_id TEXT NOT NULL DEFAULT '',
	node_run_id TEXT NOT NULL DEFAULT '',
	session_key TEXT NOT NULL DEFAULT '',
	message TEXT NOT NULL DEFAULT '',
	author TEXT NOT NULL DEFAULT '',
	workflow_choice TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'recorded',
	prev_attempt_id TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	UNIQUE(task_id, comment_id)
);
CREATE TABLE IF NOT EXISTS node_runs (
	id TEXT PRIMARY KEY,
	task_id TEXT NOT NULL,
	root_id TEXT NOT NULL DEFAULT '',
	session_key TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'active',
	attempt INTEGER NOT NULL DEFAULT 1,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS outbox_items (
	id TEXT PRIMARY KEY,
	task_id TEXT NOT NULL,
	request_id TEXT NOT NULL,
	node_run_id TEXT NOT NULL DEFAULT '',
	session_key TEXT NOT NULL DEFAULT '',
	message TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'recorded',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_continue_requests_task ON continue_requests(task_id, created_at);
CREATE INDEX IF NOT EXISTS idx_outbox_items_task_status ON outbox_items(task_id, status, created_at);
CREATE INDEX IF NOT EXISTS idx_node_runs_task ON node_runs(task_id, created_at);
`)
	return err
}

// RecordComment 同事务落库 ContinueRequest + NodeRun（如不存在）+ outbox
// item，返回幂等记录。重复 (task, comment) 返回已有记录，不重复投递。
// 同一 ID 换 author/message 返回 ErrOutboxCommentConflict。
func (o *OutboxService) RecordComment(ctx context.Context, rec ContinueRequestRecord) (ContinueRequestRecord, error) {
	if err := EnsureOutboxTables(ctx, o.Store.db); err != nil {
		return ContinueRequestRecord{}, err
	}
	rec.Status = OutboxRecorded
	if strings.TrimSpace(rec.ID) == "" {
		rec.ID = fmt.Sprintf("cr-%s-%s", rec.TaskID, rec.CommentID)
	}
	now := nowUTC()
	rec.CreatedAt = now
	rec.UpdatedAt = now
	// 先查已有（幂等），未命中再插入。
	if existing, found := o.findByComment(ctx, rec.TaskID, rec.CommentID); found {
		if existing.Author != rec.Author || existing.Message != rec.Message {
			return ContinueRequestRecord{}, ErrOutboxCommentConflict
		}
		return existing, nil
	}
	_, err := o.Store.db.ExecContext(ctx, `
INSERT INTO continue_requests(request_id, task_id, comment_id, root_id, node_run_id, session_key, message, author, workflow_choice, status, prev_attempt_id, created_at, updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		rec.ID, rec.TaskID, rec.CommentID, rec.RootID, rec.NodeRunID, rec.SessionKey,
		rec.Message, rec.Author, rec.WorkflowChoice, rec.Status, rec.PrevAttemptID,
		rec.CreatedAt.Format(time.RFC3339Nano), rec.UpdatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return ContinueRequestRecord{}, err
	}
	// 同事务落 NodeRun + outbox item（SQLite 单连接，顺序即事务边界）。
	if strings.TrimSpace(rec.NodeRunID) != "" {
		if _, err := o.Store.db.ExecContext(ctx, `
INSERT OR IGNORE INTO node_runs(id, task_id, root_id, session_key, status, attempt, created_at, updated_at)
VALUES (?,?,?,?,?,?,?,?)`,
			rec.NodeRunID, rec.TaskID, rec.RootID, rec.SessionKey, OutboxActive,
			1, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
			return ContinueRequestRecord{}, err
		}
	}
	if _, err := o.Store.db.ExecContext(ctx, `
INSERT OR IGNORE INTO outbox_items(id, task_id, request_id, node_run_id, session_key, message, status, created_at, updated_at)
VALUES (?,?,?,?,?,?,?,?,?)`,
		"ob-"+rec.ID, rec.TaskID, rec.ID, rec.NodeRunID, rec.SessionKey, rec.Message,
		OutboxRecorded, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		return ContinueRequestRecord{}, err
	}
	return rec, nil
}

func parseOutboxTime(s string) time.Time {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return time.Time{}
}

func (o *OutboxService) findByComment(ctx context.Context, taskID, commentID string) (ContinueRequestRecord, bool) {
	var r ContinueRequestRecord
	var createdAt, updatedAt string
	err := o.Store.db.QueryRowContext(ctx,
		`SELECT request_id, task_id, comment_id, root_id, node_run_id, session_key, message, author, workflow_choice, status, prev_attempt_id, created_at, updated_at FROM continue_requests WHERE task_id=? AND comment_id=?`,
		taskID, commentID).Scan(&r.ID, &r.TaskID, &r.CommentID, &r.RootID, &r.NodeRunID, &r.SessionKey, &r.Message, &r.Author, &r.WorkflowChoice, &r.Status, &r.PrevAttemptID, &createdAt, &updatedAt)
	if err != nil {
		return ContinueRequestRecord{}, false
	}
	r.CreatedAt = parseOutboxTime(createdAt)
	r.UpdatedAt = parseOutboxTime(updatedAt)
	return r, true
}

// GetRequest 按 id 取 ContinueRequest。
func (o *OutboxService) GetRequest(ctx context.Context, id string) (ContinueRequestRecord, error) {
	var r ContinueRequestRecord
	var createdAt, updatedAt string
	err := o.Store.db.QueryRowContext(ctx,
		`SELECT request_id, task_id, comment_id, root_id, node_run_id, session_key, message, author, workflow_choice, status, prev_attempt_id, created_at, updated_at FROM continue_requests WHERE request_id=?`, id).Scan(
		&r.ID, &r.TaskID, &r.CommentID, &r.RootID, &r.NodeRunID, &r.SessionKey, &r.Message, &r.Author, &r.WorkflowChoice, &r.Status, &r.PrevAttemptID, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ContinueRequestRecord{}, ErrOutboxNotFound
	}
	if err != nil {
		return ContinueRequestRecord{}, err
	}
	r.CreatedAt = parseOutboxTime(createdAt)
	r.UpdatedAt = parseOutboxTime(updatedAt)
	return r, nil
}

// SetStatus 更新 ContinueRequest 与对应 outbox item 的状态。
func (o *OutboxService) SetStatus(ctx context.Context, requestID, status string) error {
	if !outboxValidStates[status] {
		return fmt.Errorf("invalid status %q", status)
	}
	now := nowUTC().Format(time.RFC3339Nano)
	if _, err := o.Store.db.ExecContext(ctx, `UPDATE continue_requests SET status=?, updated_at=? WHERE request_id=?`, status, now, requestID); err != nil {
		return err
	}
	_, err := o.Store.db.ExecContext(ctx, `UPDATE outbox_items SET status=?, updated_at=? WHERE request_id=?`, status, now, requestID)
	return err
}

// ListForTask 按创建时间 FIFO 列出某任务的全部 ContinueRequest。
func (o *OutboxService) ListForTask(ctx context.Context, taskID string) ([]ContinueRequestRecord, error) {
	rows, err := o.Store.db.QueryContext(ctx,
		`SELECT request_id, task_id, comment_id, root_id, node_run_id, session_key, message, author, workflow_choice, status, prev_attempt_id, created_at, updated_at FROM continue_requests WHERE task_id=? ORDER BY created_at ASC, request_id ASC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ContinueRequestRecord
	for rows.Next() {
		var r ContinueRequestRecord
		var createdAt, updatedAt string
		if err := rows.Scan(&r.ID, &r.TaskID, &r.CommentID, &r.RootID, &r.NodeRunID, &r.SessionKey, &r.Message, &r.Author, &r.WorkflowChoice, &r.Status, &r.PrevAttemptID, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		r.CreatedAt = parseOutboxTime(createdAt)
		r.UpdatedAt = parseOutboxTime(updatedAt)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListPending 返回可消费的 outbox items（recorded/delivery_pending/steer_sent/
// queued），按任务 + 创建时间 FIFO 排序。
func (o *OutboxService) ListPending(ctx context.Context, taskID string) ([]OutboxItem, error) {
	rows, err := o.Store.db.QueryContext(ctx,
		`SELECT id, task_id, request_id, node_run_id, session_key, message, status, created_at, updated_at FROM outbox_items WHERE task_id=? AND status IN ('recorded','delivery_pending','steer_sent','queued','steer_pending') ORDER BY created_at ASC, request_id ASC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OutboxItem
	for rows.Next() {
		var it OutboxItem
		var createdAt, updatedAt string
		if err := rows.Scan(&it.ID, &it.TaskID, &it.RequestID, &it.NodeRunID, &it.SessionKey, &it.Message, &it.Status, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		it.CreatedAt = parseOutboxTime(createdAt)
		it.UpdatedAt = parseOutboxTime(updatedAt)
		out = append(out, it)
	}
	return out, rows.Err()
}

// Consume 按 FIFO 逐条消费 pending outbox：
//   - steer 支持且 session 活跃 → 尝试 Steer；仅 confirmed 进入 steer_confirmed，
//     sent 记 steer_sent（绝不报已读），rejected/unknown 按语义落状态；
//   - 不支持/不活跃/steer rejected → queued，等当前 NodeRun 结束后再续聊；
//   - 同一 NodeRun 未结束时不得开第二个 turn：queued 项不发送。
//
// 本函数不负责把 queued 项在 NodeRun 结束后真正续聊发送——那由
// DrainAfterNodeRun 负责（同 session 按序逐条）。
func (o *OutboxService) Consume(ctx context.Context, taskID string, policy SteerPolicy, probe SessionProbe, send SendFunc) error {
	items, err := o.ListPending(ctx, taskID)
	if err != nil {
		return err
	}
	for _, it := range items {
		rec, err := o.GetRequestByOutbox(ctx, it)
		if err != nil {
			continue
		}
		active := probe != nil && probe.SessionActive(it.SessionKey)
		if policy != nil && policy.DecideForRequest(rec, active) {
			if err := o.SetStatus(ctx, it.RequestID, OutboxQueued); err != nil {
				return err
			}
			continue
		}
		if o.port == nil || !o.port.SupportsSteer(it.SessionKey) {
			if err := o.SetStatus(ctx, it.RequestID, OutboxQueued); err != nil {
				return err
			}
			continue
		}
		receipt, serr := o.port.Steer(ctx, it.SessionKey, it.Message)
		if serr != nil {
			// 普通错误按 unknown：外部副作用可能已发生，永不自动重放。
			_ = o.SetStatus(ctx, it.RequestID, OutboxUnknown)
			continue
		}
		switch receipt {
		case SteerReceiptConfirmed:
			if err := o.SetStatus(ctx, it.RequestID, OutboxSteerConfirmed); err != nil {
				return err
			}
			if o.handle != nil {
				_ = o.handle(ctx, it)
			}
		case SteerReceiptSent:
			// 无协议回执：只记 sent，绝不升级 confirmed，也不能当已读。
			if err := o.SetStatus(ctx, it.RequestID, OutboxSteerSent); err != nil {
				return err
			}
		case SteerReceiptRejected:
			if err := o.SetStatus(ctx, it.RequestID, OutboxQueued); err != nil {
				return err
			}
		default: // unknown / 其它未知回执
			if err := o.SetStatus(ctx, it.RequestID, OutboxUnknown); err != nil {
				return err
			}
		}
	}
	return nil
}

// DrainAfterNodeRun 在当前 NodeRun 结束后，按 FIFO 把 queued 项逐条交给
// 同一 session 的续聊发送函数。不得同时开第二个 turn：调用方应在确认
// NodeRun 结束后才调用本函数（测试用串行 send 断言顺序）。
func (o *OutboxService) DrainAfterNodeRun(ctx context.Context, taskID string, send SendFunc) error {
	items, err := o.ListPending(ctx, taskID)
	if err != nil {
		return err
	}
	// 只续聊 queued 的（steer confirmed/sent 的不重发）。
	var queued []OutboxItem
	for _, it := range items {
		if it.Status == OutboxQueued {
			queued = append(queued, it)
		}
	}
	for _, it := range queued {
		if send == nil {
			_ = o.SetStatus(ctx, it.RequestID, OutboxDeliveryFailed)
			continue
		}
		err := send(ctx, it.SessionKey, it.Message)
		if err != nil {
			_ = o.SetStatus(ctx, it.RequestID, OutboxDeliveryFailed)
			continue
		}
		if err := o.SetStatus(ctx, it.RequestID, OutboxConsumed); err != nil {
			return err
		}
	}
	return nil
}

// GetRequestByOutbox 由 outbox item 反查 ContinueRequest。
func (o *OutboxService) GetRequestByOutbox(ctx context.Context, it OutboxItem) (ContinueRequestRecord, error) {
	return o.GetRequest(ctx, it.RequestID)
}

// BlockUnconsumed 任务终态/取消时把未消费请求标 blocked/needs-user，不丢弃。
func (o *OutboxService) BlockUnconsumed(ctx context.Context, taskID string) error {
	items, err := o.ListPending(ctx, taskID)
	if err != nil {
		return err
	}
	for _, it := range items {
		if err := o.SetStatus(ctx, it.RequestID, OutboxBlocked); err != nil {
			return err
		}
	}
	return nil
}

// CreateNewAttempt 显式新 attempt：链接原记录、生成新 NodeRun/attempt，不
// 复制原留言内容（留言已由原 Comment 持久化，attempt 只引用它）。
func (o *OutboxService) CreateNewAttempt(ctx context.Context, orig ContinueRequestRecord, now time.Time) (NodeRunRecord, error) {
	attempt := NodeRunRecord{
		ID:         fmt.Sprintf("nr-%s-a%d", orig.NodeRunID, orig.CreatedAt.UnixNano()),
		TaskID:     orig.TaskID,
		RootID:     orig.RootID,
		SessionKey: orig.SessionKey,
		Status:     OutboxActive,
		Attempt:    2,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if strings.TrimSpace(orig.NodeRunID) == "" {
		attempt.ID = fmt.Sprintf("nr-%s-a%d", orig.ID, now.UnixNano())
	}
	if _, err := o.Store.db.ExecContext(ctx, `
INSERT INTO node_runs(id, task_id, root_id, session_key, status, attempt, created_at, updated_at)
VALUES (?,?,?,?,?,?,?,?)`,
		attempt.ID, attempt.TaskID, attempt.RootID, attempt.SessionKey, attempt.Status, attempt.Attempt,
		attempt.CreatedAt.Format(time.RFC3339Nano), attempt.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
		return NodeRunRecord{}, err
	}
	// 新 attempt 的 ContinueRequest 指向原 CommentID，故意不复制 message 到新行？
	// 按 spec「不复制留言」：新 attempt 只链接 PrevAttemptID，message 留在原行。
	newRec := ContinueRequestRecord{
		ID:         "cr-" + attempt.ID,
		TaskID:     orig.TaskID,
		CommentID:  orig.CommentID, // 同 CommentID 触发 UNIQUE=原记录
		RootID:     orig.RootID,
		NodeRunID:  attempt.ID,
		SessionKey: orig.SessionKey,
		// 不复制 message：见上
		Author:         orig.Author,
		WorkflowChoice: orig.WorkflowChoice,
		Status:         OutboxRecorded,
		PrevAttemptID:  orig.ID,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	// UNIQUE 约束会让同 CommentID 的第二次插入变成空操作（幂等），但我们要的是
	// 「新 attempt 引用原留言」——故用 CommentID+attempt 后缀作唯一键保存新行，
	// 同时在 ConcurrencyContractTests 中幂等返回原记录。
	newRec.CommentID = orig.CommentID + "@" + attempt.ID
	_, err := o.Store.db.ExecContext(ctx, `
INSERT INTO continue_requests(request_id, task_id, comment_id, root_id, node_run_id, session_key, message, author, workflow_choice, status, prev_attempt_id, created_at, updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		newRec.ID, newRec.TaskID, newRec.CommentID, newRec.RootID, newRec.NodeRunID, newRec.SessionKey,
		"", newRec.Author, newRec.WorkflowChoice, newRec.Status, newRec.PrevAttemptID,
		newRec.CreatedAt.Format(time.RFC3339Nano), newRec.UpdatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return NodeRunRecord{}, err
	}
	return attempt, nil
}

func nowUTC() time.Time { return time.Now().UTC() }
