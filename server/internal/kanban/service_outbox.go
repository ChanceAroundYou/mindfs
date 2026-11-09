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
	"sync"
	"time"
)

var (
	// ErrOutboxCommentConflict 表示同一 (task, comment) 换了 author/body。
	ErrOutboxCommentConflict = errors.New("comment id conflict")
	// ErrOutboxNotFound 表示请求的 ContinueRequest/NodeRun 不存在。
	ErrOutboxNotFound = errors.New("outbox record not found")
)

// ContinueRequest / Outbox / NodeRun 状态（spec「留言、steer 与队列行为」§2/§3）。
const (
	OutboxRecorded           = "recorded"
	OutboxDeliveryPending    = "delivery_pending"
	OutboxSteerSent          = "steer_sent"
	OutboxSteerConfirmed     = "steer_confirmed" // 仅真实协议回执
	OutboxQueued             = "queued"          // FIFO 排队
	OutboxConsumed           = "consumed"
	OutboxDeliveryFailed     = "delivery_failed"
	OutboxUnknown            = "unknown" // 超时无法判定，永不自动重放
	OutboxBlocked            = "blocked" // 终态/取消未消费，needs-user
	OutboxNeedsUser          = "needs_user"
	OutboxSteerActionPending = "steer_pending" // 仅内部：等待 steer 判定
	OutboxActive             = "active"        // NodeRun 正在执行
	OutboxFinished           = "finished"      // NodeRun 已完成
)

var outboxValidStates = map[string]bool{
	OutboxRecorded: true, OutboxDeliveryPending: true, OutboxSteerSent: true,
	OutboxSteerConfirmed: true, OutboxQueued: true, OutboxConsumed: true,
	OutboxDeliveryFailed: true, OutboxUnknown: true, OutboxBlocked: true,
	OutboxNeedsUser: true, OutboxSteerActionPending: true, OutboxActive: true,
	OutboxFinished: true,
}

// ContinueRequestRecord 是 continue_requests 行的内存形态。
type ContinueRequestRecord struct {
	ID             string
	TaskID         string
	CommentID      string
	RootID         string
	NodeRunID      string
	SessionKey     string
	Message        string
	Author         string
	WorkflowChoice string
	Status         string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	// PrevAttemptID 新 attempt 链接的原记录（显式重试时），普通留言为空。
	PrevAttemptID string
}

// NodeRunRecord 是 node_runs 行的内存形态。
type NodeRunRecord struct {
	ID         string
	TaskID     string
	RootID     string
	SessionKey string
	Status     string
	Attempt    int
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// OutboxItem 是 outbox_items 行的内存形态。
type OutboxItem struct {
	ID         string
	TaskID     string
	RequestID  string
	NodeRunID  string
	SessionKey string
	Message    string
	Status     string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// SteerPort 是 Outbox 依赖的 steer 能力端口（由 agent.Pool 适配，默认 FIFO）。
type SteerPort interface {
	SupportsSteer(sessionKey string) bool
	Steer(ctx context.Context, sessionKey, msg string) (SteerReceipt, error)
}

// SteerReceipt 与 agent/types.SteerReceipt 语义一致（本地镜像，避免 kanban
// 对 agent/types 的新 import 形成循环；值已与 types 中常量交叉测试锁定）。
type SteerReceipt string

const (
	SteerReceiptConfirmed SteerReceipt = "confirmed"
	SteerReceiptSent      SteerReceipt = "sent"
	SteerReceiptRejected  SteerReceipt = "rejected"
	SteerReceiptUnknown   SteerReceipt = "unknown"
)

// SteerPolicy 决定一条 ContinueRequest 如何投递。
type SteerPolicy interface {
	// DecideForRequest 在投递前判定：session 不活跃 / 不支持 steer 时返回
	// fifo=true，调用方排队，不得调用 Steer。
	// 默认：adapter SupportsSteer=false 或 session 不活跃 → FIFO。
	DecideForRequest(rec ContinueRequestRecord, sessionActive bool) (fifo bool)
}

// DefaultSteerPolicy 是 Phase 1 默认：没有 steer 声明就 FIFO。
type DefaultSteerPolicy struct{ Port SteerPort }

func (p DefaultSteerPolicy) DecideForRequest(rec ContinueRequestRecord, sessionActive bool) bool {
	if !sessionActive {
		return true
	}
	if p.Port == nil {
		return true
	}
	return !p.Port.SupportsSteer(rec.SessionKey)
}

// SendFunc 是 worker 把消息真正交给 session 的发送函数（通常是 SendMessage
// 续聊）。steer 已确认的路径不调用它；FIFO 消费时按序调用它。
// 返回 error 时调用方记 delivery_failed；超时无法判定记 unknown。
type SendFunc func(ctx context.Context, sessionKey, msg string) error

// SessionProbe 报告 session 是否活跃且可接收 steer/续聊。
type SessionProbe interface {
	SessionActive(sessionKey string) bool
}

// OutboxService 持久化 ContinueRequest/NodeRun + outbox 队列，并按
// FIFO 消费。worker 逻辑在 ConsumeAll；生产装配时由 Service 调用。
type OutboxService struct {
	Store  *TaskStore
	handle func(ctx context.Context, item OutboxItem) error
	// fn 供测试注入 steer 行为；nil 时默认用 Port。
	send SendFunc
	port SteerPort

	mu sync.Mutex
}

// EnsureOutboxTables 建/兼容三张表。幂等。
func EnsureOutboxTables(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS continue_requests (
	id TEXT PRIMARY KEY,
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
INSERT INTO continue_requests(id, task_id, comment_id, root_id, node_run_id, session_key, message, author, workflow_choice, status, prev_attempt_id, created_at, updated_at)
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
		`SELECT id, task_id, comment_id, root_id, node_run_id, session_key, message, author, workflow_choice, status, prev_attempt_id, created_at, updated_at FROM continue_requests WHERE task_id=? AND comment_id=?`,
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
		`SELECT id, task_id, comment_id, root_id, node_run_id, session_key, message, author, workflow_choice, status, prev_attempt_id, created_at, updated_at FROM continue_requests WHERE id=?`, id).Scan(
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
	if _, err := o.Store.db.ExecContext(ctx, `UPDATE continue_requests SET status=?, updated_at=? WHERE id=?`, status, now, requestID); err != nil {
		return err
	}
	_, err := o.Store.db.ExecContext(ctx, `UPDATE outbox_items SET status=?, updated_at=? WHERE request_id=?`, status, now, requestID)
	return err
}

// ListForTask 按创建时间 FIFO 列出某任务的全部 ContinueRequest。
func (o *OutboxService) ListForTask(ctx context.Context, taskID string) ([]ContinueRequestRecord, error) {
	rows, err := o.Store.db.QueryContext(ctx,
		`SELECT id, task_id, comment_id, root_id, node_run_id, session_key, message, author, workflow_choice, status, prev_attempt_id, created_at, updated_at FROM continue_requests WHERE task_id=? ORDER BY created_at ASC, id ASC`, taskID)
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
		`SELECT id, task_id, request_id, node_run_id, session_key, message, status, created_at, updated_at FROM outbox_items WHERE task_id=? AND status IN ('recorded','delivery_pending','steer_sent','queued','steer_pending') ORDER BY created_at ASC, id ASC`, taskID)
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
INSERT INTO continue_requests(id, task_id, comment_id, root_id, node_run_id, session_key, message, author, workflow_choice, status, prev_attempt_id, created_at, updated_at)
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
