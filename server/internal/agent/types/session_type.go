package types

import (
	"context"
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
