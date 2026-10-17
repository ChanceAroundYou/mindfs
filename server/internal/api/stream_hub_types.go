package api

import (
	"strings"
	"time"

	"mindfs/server/internal/api/usecase"
)

type PendingUserMessage struct {
	Agent            string    `json:"agent,omitempty"`
	Model            string    `json:"model,omitempty"`
	ModelDisplayName string    `json:"model_display_name,omitempty"`
	Mode             string    `json:"mode,omitempty"`
	Effort           string    `json:"effort,omitempty"`
	FastService      string    `json:"fast_service,omitempty"`
	PlanMode         bool      `json:"plan_mode,omitempty"`
	Content          string    `json:"content"`
	Timestamp        time.Time `json:"timestamp"`
}

type QueuedUserMessage struct {
	ID string `json:"id"`
	PendingUserMessage
	ClientCtx usecase.ClientContext `json:"-"`
}

type SessionPendingState struct {
	RootID          string
	SessionTitle    string
	Active          bool
	QueueFrozen     bool
	User            *PendingUserMessage
	Queue           []QueuedUserMessage
	ReplyingList    []StreamEvent
	BaseExchangeSeq int
	NextEventSeq    uint64
	Summary         string
	UpdatedAt       time.Time
	// MindFSID 是本条目所属回合的身份（每次 SetPending* 刷新为一个新 mindfs_id）。
	// 清理时带上同一个 id 才生效 —— 见 ClearSessionPending。
	// G-BI 起它沿用主日志行的 mindfs_id 表示（确定性 UUIDv5），不再有第二套编号。
	MindFSID string
	// AwaitingAnswer 表示「有一个 ask_user 提问还没被回答」。
	//
	// 它必须**活过回合结束**：AskUserQuestion 把一个逻辑任务切成两轮，提问轮 done
	// 之后才是它该显示的时候（Agent 在等用户，不是任务结束）。所以回合结束时它
	// 不是被删掉，而是把条目降级成「非 Active 但保留」——见 ClearSessionPending。
	// 与之配套：它**不**参与 Active，于是 IsSessionReplying 为假、不进
	// /api/replying-sessions（那是实心灯），只经 sessionListResponse 的
	// awaiting_answer 字段表达成空心灯。
	AwaitingAnswer bool
	AwaitingCallID string
}

type ClientStreamStatus string

const (
	ClientStreamStatusReplay ClientStreamStatus = "replay"
	ClientStreamStatusLive   ClientStreamStatus = "live"
)

type ClientReplayState struct {
	Status       ClientStreamStatus
	LastEventSeq uint64
}

type ReplyingSessionState struct {
	RootID       string    `json:"rootId"`
	SessionKey   string    `json:"sessionKey"`
	SessionTitle string    `json:"sessionTitle"`
	Status       string    `json:"status"`
	Summary      string    `json:"summary"`
	UpdatedAt    time.Time `json:"updatedAt"`
	// AwaitingAnswer 表示「该会话有一个 ask_user 提问尚未回答」（空心灯）。
	// 与 Status="replying"（实心灯）互斥表达：一个会话可以 awaiting 但不 replying。
	// 随 /api/replying-sessions 一并下发，避免为这一位单独开第二条状态通道。
	AwaitingAnswer bool `json:"awaiting_answer,omitempty"`
}

type PendingSessionSnapshot struct {
	RootID       string
	SessionTitle string
	Summary      string
	UpdatedAt    time.Time
}

type replayStep struct {
	events []StreamEvent
	live   bool
}

func blank(value string) bool {
	return strings.TrimSpace(value) == ""
}

func cloneEvent(ev StreamEvent) StreamEvent {
	// Timestamp 必须一起搬。重放路径（ReplyingList → collectReplayStep）只走 cloneEvent，
	// 漏掉它客户端就只能回退到「恢复页面的接收时刻」，回复时长把整段后台时间算进去。
	return StreamEvent{Type: ev.Type, Data: ev.Data, EventCursor: ev.EventCursor, Timestamp: ev.Timestamp, MindFSID: ev.MindFSID}
}
