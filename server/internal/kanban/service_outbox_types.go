package kanban

import (
	"context"
	"errors"
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
