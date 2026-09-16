package types

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
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

type ExternalSessionSummary struct {
	Agent          string    `json:"agent"`
	AgentSessionID string    `json:"agent_session_id"`
	Cwd            string    `json:"cwd,omitempty"`
	Title          string    `json:"title,omitempty"`
	FirstUserText  string    `json:"-"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type ListExternalSessionsInput struct {
	RootPath    string
	Agent       string
	BeforeTime  time.Time
	AfterTime   time.Time
	Limit       int
	FilterBound bool
}

type ListExternalSessionsResult struct {
	Items []ExternalSessionSummary `json:"items"`
}

type ExternalSessionVisitFunc func(ExternalSessionSummary) (bool, error)

type ImportExternalSessionInput struct {
	RootPath       string
	Agent          string
	AgentSessionID string
	AfterTimestamp time.Time
	Cursor         ExternalSessionCursor
	// TimestampFloor 非零时，无论有没有字节游标，都只接受「时间戳晚于它」的条目。
	// 用途：MindFS 自己在驱动的会话（live-owned）平时不让导入器写，只在重启后兜底补齐一次；
	// 那一次的游标是**冻结已久**的，直接按游标读会把已经落过库的旧回合整段重导一遍
	// （实测 2026-09-16：BP 会话被重导 09-14 的内容，与实时路径写的行并排显示成重复）。
	// 兜底补齐真正要的只是「比库里最新一条还新」的尾轮，所以给它一道时间地板。
	TimestampFloor time.Time
	// ForceRead 为 true 时禁用「源文件未变即整个跳过导入」的快速路径，强制重新读取。
	// 有子代理会话的会话必须置 true：子代理转录可能在 root 文件不变的情况下增长，
	// 跳过会漏掉它们。注意它与 Cursor 是两件事——Cursor 提供增量起点（byte offset），
	// 二者不可混淆（曾因此让增量读失效、每次全量解析数十 MB 转录）。
	ForceRead bool
}

// TranscriptNoisePrefixes 是 Claude CLI 自己写进转录、比较时应忽略的标记（小写）。
//
// 它们与 <local-command-caveat> 那批同类：不是用户输入，但 isMeta 为 None（实测），
// 所以按 isMeta 过滤拦不住，只能按内容认。标记通常是**独立的一条 user 条目**，
// 紧跟着才是真人正文，而「相邻同角色合并」会把两者粘成 "[请求标记]\n\n正文" ——
// 正文此前已被实时路径写过一次，粘连版多 31 字前缀、与干净版既不相等也不构成前缀
// 关系，判重于是放行、落成第二条。实测 2026-09-14 17:21 的会话 1789190353：
// seq 136（干净正文）与 seq 138（标记+正文）并存，后者还排在助手回复之后。
//
// 导入侧用它判定「剥完什么都不剩 = 整条是噪声」，判重侧用它剥掉粘连的标记。
// 一侧定义、两侧共用，避免名单走散。
var TranscriptNoisePrefixes = []string{
	"[request interrupted by user]",
	"[request interrupted by user for tool use]",
	"[request interrupted for tool use]",
	"[your previous response had no visible output. please continue and produce a user-visible response.]",
}

// StripTranscriptNoisePrefixes 反复剥掉开头的已知标记（可叠加出现），
// 并去掉首尾空白。剥完为空说明整条就是标记。
func StripTranscriptNoisePrefixes(s string) string {
	for {
		trimmed := strings.TrimSpace(s)
		lower := strings.ToLower(trimmed)
		matched := ""
		for _, prefix := range TranscriptNoisePrefixes {
			if strings.HasPrefix(lower, prefix) {
				matched = prefix
				break
			}
		}
		if matched == "" {
			return trimmed
		}
		s = trimmed[len(matched):]
	}
}

// IsTranscriptNoiseEntry 判断整条内容是否只是 CLI 标记（剥完为空）。
func IsTranscriptNoiseEntry(s string) bool {
	return strings.TrimSpace(s) != "" && StripTranscriptNoisePrefixes(s) == ""
}

type ExternalSessionCursor struct {
	SourcePath string
	// Offset 是上次读取时的**文件长度**，只用于「文件没变就跳过」的判据。
	Offset          int64
	ModTimeUnixNano int64
	// CommittedOffset 是**已提交给 MindFS 的字节位置**，也是下次增量读的起点。
	// 它必须落在 item 边界上：轮次还在进行时不推进，避免把半成品当成一条 exchange 落库，
	// 也避免同一轮被反复当作「新内容」追加（旧实现用会被 tool_result 持续改写的 Timestamp
	// 当判据，实测同一轮落库 6 次）。
	CommittedOffset int64
}

type ImportedExchange struct {
	Role      string
	Content   string
	Timestamp time.Time
	Aux       []ImportedExchangeAux
}

// ImportedExchangeAux is structured historical content attached to an imported
// exchange. Seq is assigned by the session manager when the exchange is
// persisted; importers only need to preserve its position within the assistant
// text and the normalized payload.
type ImportedExchangeAux struct {
	Line     int
	ToolCall *ToolCall
	Plan     *PlanUpdate
}

type ImportedExternalSession struct {
	Agent          string
	AgentSessionID string
	Cwd            string
	Title          string
	Exchanges      []ImportedExchange
	Subagents      []ImportedSubagentSession
	Cursor         ExternalSessionCursor
}

// ImportedSubagentSession describes an externally persisted child agent session.
// ParentAgentSessionID is empty when the child belongs directly to the imported
// root session; otherwise it references another item in Subagents.
type ImportedSubagentSession struct {
	AgentSessionID       string
	ParentAgentSessionID string
	ParentToolCallID     string
	Title                string
	Model                string
	Exchanges            []ImportedExchange
}

type ExternalSessionImporter interface {
	AgentName() string
	ListExternalSessions(ctx context.Context, in ListExternalSessionsInput) (ListExternalSessionsResult, error)
	ImportExternalSession(ctx context.Context, in ImportExternalSessionInput) (ImportedExternalSession, error)
}

type StreamingExternalSessionImporter interface {
	ExternalSessionImporter
	ScanExternalSessions(ctx context.Context, in ListExternalSessionsInput, visit ExternalSessionVisitFunc) error
}

type ModelInfo struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Description   string   `json:"description,omitempty"`
	Hidden        bool     `json:"hidden,omitempty"`
	SupportEffort bool     `json:"supportEffort,omitempty"`
	Efforts       []string `json:"efforts,omitempty"`
}

type ModelList struct {
	CurrentModelID string      `json:"current_model_id,omitempty"`
	Models         []ModelInfo `json:"models,omitempty"`
}

type ModeInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type ModeList struct {
	CurrentModeID string     `json:"current_mode_id,omitempty"`
	Modes         []ModeInfo `json:"modes,omitempty"`
}

type CommandInfo struct {
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	ArgumentHint string `json:"argument_hint,omitempty"`
}

type CommandList struct {
	Commands []CommandInfo `json:"commands,omitempty"`
}

// EventType defines the type of a session event.
type EventType string

const (
	EventTypeMessageChunk EventType = "message_chunk"
	EventTypeThoughtChunk EventType = "thought_chunk"
	EventTypeToolCall     EventType = "tool_call"
	EventTypeToolUpdate   EventType = "tool_update"
	EventTypeTodoUpdate   EventType = "todo_update"
	EventTypePlanUpdate   EventType = "plan_update"
	EventTypeCompact      EventType = "compact_notice"
	EventTypeLogin        EventType = "login_notice"
	EventTypeMessageDone  EventType = "message_done"
	EventTypeRecovery     EventType = "recovery"
)

// Event is a normalized session update emitted by any agent backend.
type Event struct {
	Type      EventType
	SessionID string
	Data      any
}

type MessageChunk struct {
	Content         string `json:"content"`
	ParentToolUseID string `json:"parentToolUseId,omitempty"`
	TaskID          string `json:"taskId,omitempty"`
	SubagentType    string `json:"subagentType,omitempty"`
	TaskDescription string `json:"taskDescription,omitempty"`
}

type ThoughtChunk struct {
	ID              string `json:"id,omitempty"`
	Content         string `json:"content"`
	ParentToolUseID string `json:"parentToolUseId,omitempty"`
	TaskID          string `json:"taskId,omitempty"`
	SubagentType    string `json:"subagentType,omitempty"`
	TaskDescription string `json:"taskDescription,omitempty"`
}

type MessageDone struct {
	ContextWindow   ContextWindow `json:"contextWindow"`
	TokenUsage      *TokenUsage   `json:"tokenUsage,omitempty"`
	ParentToolUseID string        `json:"parentToolUseId,omitempty"`
	TaskID          string        `json:"taskId,omitempty"`
	SubagentType    string        `json:"subagentType,omitempty"`
	TaskDescription string        `json:"taskDescription,omitempty"`
}

type RecoveryStatus struct {
	Message string `json:"message"`
}

type TodoItem struct {
	Content    string `json:"content"`
	ActiveForm string `json:"activeForm,omitempty"`
	Status     string `json:"status"`
}

type TodoUpdate struct {
	Items []TodoItem `json:"items"`
}

type PlanUpdate struct {
	ID      string `json:"id,omitempty"`
	Content string `json:"content"`
	Delta   bool   `json:"delta,omitempty"`
}

type CompactNotice struct {
	ID      string `json:"id,omitempty"`
	Status  string `json:"status,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type LoginNotice struct {
	Status          string `json:"status"`
	LoginID         string `json:"loginId,omitempty"`
	VerificationURL string `json:"verificationUrl,omitempty"`
	UserCode        string `json:"userCode,omitempty"`
	Error           string `json:"error,omitempty"`
	AuthMode        string `json:"authMode,omitempty"`
	PlanType        string `json:"planType,omitempty"`
}

type AskUserQuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type AskUserQuestionItem struct {
	Question    string                  `json:"question"`
	Header      string                  `json:"header,omitempty"`
	Options     []AskUserQuestionOption `json:"options,omitempty"`
	MultiSelect bool                    `json:"multiSelect,omitempty"`
}

type AskUserAnswer struct {
	ToolUseID string            `json:"toolUseId"`
	Answers   map[string]string `json:"answers"`
}

type ToolKind string

const (
	ToolKindRead       ToolKind = "read"
	ToolKindEdit       ToolKind = "edit"
	ToolKindDelete     ToolKind = "delete"
	ToolKindMove       ToolKind = "move"
	ToolKindSearch     ToolKind = "search"
	ToolKindWebSearch  ToolKind = "web_search"
	ToolKindExecute    ToolKind = "execute"
	ToolKindThink      ToolKind = "think"
	ToolKindFetch      ToolKind = "fetch"
	ToolKindTask       ToolKind = "task"
	ToolKindAskUser    ToolKind = "ask_user"
	ToolKindTodo       ToolKind = "todo"
	ToolKindSwitchMode ToolKind = "switch_mode"
	ToolKindOther      ToolKind = "other"
)

type ToolCallLocation struct {
	Path string `json:"path"`
	Line *int   `json:"line,omitempty"`
}

type ToolCallContentItem struct {
	Type       string  `json:"type"`
	Text       string  `json:"text,omitempty"`
	Path       string  `json:"path,omitempty"`
	ChangeKind string  `json:"changeKind,omitempty"`
	OldText    *string `json:"oldText,omitempty"`
	NewText    string  `json:"newText,omitempty"`
}

type ToolCall struct {
	CallID    string                `json:"callId"`
	Title     string                `json:"title,omitempty"`
	Status    string                `json:"status"`
	Kind      ToolKind              `json:"kind"`
	Content   []ToolCallContentItem `json:"content,omitempty"`
	Locations []ToolCallLocation    `json:"locations,omitempty"`
	RawType   string                `json:"rawType,omitempty"`
	Meta      map[string]any        `json:"meta,omitempty"`
}

func (tc ToolCall) IsWriteOperation() bool {
	switch tc.Kind {
	case ToolKindEdit, ToolKindDelete, ToolKindMove:
		return true
	default:
		return false
	}
}

func (tc ToolCall) GetAffectedPaths() []string {
	paths := make([]string, 0, len(tc.Locations))
	for _, loc := range tc.Locations {
		if loc.Path != "" {
			paths = append(paths, loc.Path)
		}
	}
	return paths
}

type TurnCanceler struct {
	mu     sync.RWMutex
	cancel context.CancelFunc
	turnID uint64
}

func (t *TurnCanceler) Begin(parent context.Context) (context.Context, uint64) {
	turnCtx, cancel := context.WithCancel(parent)
	turnID := atomic.AddUint64(&t.turnID, 1)

	t.mu.Lock()
	t.cancel = cancel
	t.turnID = turnID
	t.mu.Unlock()

	return turnCtx, turnID
}

func (t *TurnCanceler) Cancel() {
	t.mu.RLock()
	cancel := t.cancel
	t.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
}

func (t *TurnCanceler) End(turnID uint64) {
	t.mu.Lock()
	if t.turnID == turnID {
		t.cancel = nil
	}
	t.mu.Unlock()
}
