package types

import (
	"context"
	"time"
)

// Session is the interface for all agent sessions.
type Session interface {
	// SendMessage sends a message to the current session.
	SendMessage(ctx context.Context, content string) error

	// AnswerQuestion sends a response for a pending AskUserQuestion tool call.
	AnswerQuestion(ctx context.Context, answer AskUserAnswer) error

	// CurrentModel returns the model currently used by the runtime session.
	CurrentModel() string

	// SetModel updates the model used by the current session.
	SetModel(ctx context.Context, model string) error

	// ListModels returns the models visible to the current session/runtime.
	ListModels(ctx context.Context) (ModelList, error)

	// SetMode updates the mode used by the current session.
	SetMode(ctx context.Context, mode string) error

	// SetPlanMode updates the plan mode used by the current session.
	SetPlanMode(ctx context.Context, enabled bool) error

	// ListModes returns the modes visible to the current session/runtime.
	ListModes(ctx context.Context) (ModeList, error)

	// ListCommands returns the commands visible to the current session/runtime.
	ListCommands(ctx context.Context) (CommandList, error)

	// CancelCurrentTurn cancels the in-flight turn, if any.
	CancelCurrentTurn() error

	// OnUpdate registers a callback for streaming updates.
	OnUpdate(onUpdate func(Event))

	// SessionID returns the current session ID.
	SessionID() string

	// ContextWindow returns the latest known context window usage for the session.
	ContextWindow(ctx context.Context) (ContextWindow, error)

	// Close terminates the session (not the process).
	Close() error
}

type ForkPointKind string

const (
	ForkPointClaudeMessageUUID ForkPointKind = "claude_message_uuid"
	ForkPointCodexUserOrdinal  ForkPointKind = "codex_user_ordinal"
)

type ResolveForkPointInput struct {
	RootPath       string
	AgentSessionID string
	AgentTurnIndex int
	// TargetTimestamp 是 fork 目标轮次在 MindFS 侧的落盘时间。
	//
	// 为什么不能只靠 AgentTurnIndex：那是**按 MindFS 行**数出来的序号，而转录侧的
	// 回合切分是导入器自己的（相邻助手条目会被合并、CLI 注入的用户条目会被丢弃），
	// 两边并不一一对应。实测 2026-11-20 llmux 会话 seq=362：MindFS 侧 168 轮，
	// 转录侧只有 145 个 turn，按序号取必然越界。时间戳是两边都有的锚点，
	// 非零时解析器优先按它定位。
	TargetTimestamp time.Time
}

type ResolveForkPointOutput struct {
	Kind              ForkPointKind
	AgentSessionID    string
	ClaudeMessageUUID string
	CodexUserOrdinal  int
}

type ForkPointResolver interface {
	ResolveForkPointByAgentTurnIndex(ctx context.Context, in ResolveForkPointInput) (ResolveForkPointOutput, error)
}

type ForkSessionInput struct {
	SessionKey         string
	AgentName          string
	Model              string
	Mode               string
	Effort             string
	FastService        string
	PlanMode           bool
	RootPath           string
	SourceAgentSession string
	ForkPoint          ResolveForkPointOutput
}

type ForkSessionOutput struct {
	AgentSessionID string
}

type SessionForker interface {
	ForkSession(ctx context.Context, in ForkSessionInput) (ForkSessionOutput, error)
}

type ContextWindow struct {
	TotalTokens        int `json:"totalTokens"`
	ModelContextWindow int `json:"modelContextWindow"`
}

// TokenUsage is the normalized usage for one agent turn. InputTokens is the
// logical input size before cache discounts. CacheReadTokens is nil when the
// backend does not report cache telemetry.
type TokenUsage struct {
	InputTokens      int  `json:"inputTokens"`
	OutputTokens     int  `json:"outputTokens"`
	CacheReadTokens  *int `json:"cacheReadTokens,omitempty"`
	CacheWriteTokens *int `json:"cacheWriteTokens,omitempty"`
}

type OpenSessionInput struct {
	SessionKey            string
	AgentName             string
	Model                 string
	Mode                  string
	Effort                string
	FastService           string
	PlanMode              bool
	Probe                 bool
	RootPath              string
	DeveloperInstructions string
	AgentSessionID        string
	AgentCtxSeq           int
	ForkPoint             ResolveForkPointOutput
	// ConfigDir 非空时让 CLI 使用隔离的 settings/hooks/sessions 目录
	// （Task Router 专用）。普通会话留空，完全保持改造前行为。
	ConfigDir string
	// Tools 是进程内 MCP 工具；非空时只有 claude 协议支持。
	Tools []ToolSpec
	// ToolPolicy 是 deny-by-default 的工具闸门；nil 时沿用旧行为。
	ToolPolicy *ToolPolicy
}

type RuntimeDefaults struct {
	Model       string `json:"model,omitempty"`
	Effort      string `json:"effort,omitempty"`
	FastService string `json:"fast_service,omitempty"`
}

type DefaultsReader interface {
	RuntimeDefaults(ctx context.Context) (RuntimeDefaults, error)
}

type ThreadEventSubscriber interface {
	SubscribeThreadEvents(ctx context.Context) error
}

// SteerReceipt reports the outcome of a steer attempt. Only
// SteerReceiptConfirmed means the adapter protocol actually acknowledged
// delivery into the active turn. SteerReceiptSent has no such acknowledgement
// and must never be reported as read/confirmed. SteerReceiptUnknown means
// the outcome cannot be determined (for example a timeout after the message
// may have started) and must not be replayed automatically.
type SteerReceipt string

const (
	SteerReceiptConfirmed SteerReceipt = "confirmed"
	SteerReceiptSent      SteerReceipt = "sent"
	SteerReceiptRejected  SteerReceipt = "rejected"
	SteerReceiptUnknown   SteerReceipt = "unknown"
)

// SteerCapability is an explicit adapter capability. First-generation
// adapters do not implement it and therefore support no steer: callers must
// fall back to FIFO queuing. Ordinary SendMessage is not a steer and must
// never be presented as one.
type SteerCapability interface {
	SupportsSteer() bool
	// Steer attempts to inject msg into the active turn of sessionKey. The
	// message argument is a plain string per the Phase 1 constraint.
	Steer(ctx context.Context, sessionKey, msg string) (SteerReceipt, error)
}

// DefaultSteerSupport reports the Phase 1 default for existing adapters:
// no steer support, FIFO only.
func DefaultSteerSupport() bool { return false }

// SteerOf returns the SteerCapability implemented by s, if any.
func SteerOf(s Session) (SteerCapability, bool) {
	c, ok := s.(SteerCapability)
	return c, ok
}
