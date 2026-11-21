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
	// SubSession 表示这条 pending 属于**子会话**（Claude Code Task / 看板节点 attach 出来的
	// 子代理），不是用户正看着的那条会话。子会话的完成不该弹提醒：父会话那一轮还在跑，
	// 用户要的是「活干完了」，不是「其中一个子任务回来了」。
	//
	// 由 SetPendingReply 置位 —— 目前所有调用点都是 OnSubAgentAttached，也就是子会话。
	// 顶层的用户留言走 SetPendingUserAt，永远是 false。
	SubSession bool
	// SilentCompletion 表示这一轮的完成**不对外提醒**（看板工作流节点等任务子步骤）。
	// 与 SubSession 的区别：子会话是「父回合里派生出来的会话」，这里是一条正常会话，
	// 只是这一轮属于任务的中间步骤，用户要的是任务收尾那一条提醒。
	//
	// 由 executeWorkflowTurn 在节点回合开始时显式置位；顶层对话与直连回合保持 false。
	SilentCompletion bool
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
	// SubSession 标记这条属于子会话（见 SessionPendingState.SubSession）。
	// 客户端（安卓后台轮询、Web Push 消费端）据此跳过完成提醒。
	SubSession bool `json:"subSession,omitempty"`
	// Silent 标记这一轮属于任务子步骤（见 SessionPendingState.SilentCompletion）。
	// 消费端同样只跳过完成提醒，不影响「正在回复」指示灯。
	Silent bool `json:"silent,omitempty"`
}

type PendingSessionSnapshot struct {
	RootID       string
	SessionTitle string
	Summary      string
	UpdatedAt    time.Time
	// SubSession 与 SessionPendingState.SubSession 同义；Web Push 用它跳过子会话完成。
	SubSession bool
	// SilentCompletion 与 SessionPendingState.SilentCompletion 同义；Web Push 用它跳过
	// 任务子步骤（工作流节点）的完成提醒。
	SilentCompletion bool
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
