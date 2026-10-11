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
	// NativeID 是这条行在 agent 侧的身份：claude 转录条目的 `uuid`、ACP 的
	// `messageId`（user 行取 `UserMessageChunk.MessageId`，agent 行取
	// `AgentMessageChunk.MessageId`）。
	//
	// 为什么必须带：导入器会把**相邻同角色**条目合并成一条（`appendMerged…`），
	// 合并后的 Content 与实时路径写的行**逐字不同**，于是「内容相等 + ±5 秒」
	// 判重必然漏判，同一轮被两个写入者各落一行（实测 2026-10-07：某会话 9 对
	// live/import 重复，导入侧时间戳还是 `time.Now()` 伪造的）。有了 native id，
	// 判重就是「同一条 agent 原生消息」的铁证，与内容无关（方案 §6 / D10）。
	//
	// 空串 = 这条行没有 agent 侧身份（codex 未实证、S2c 之前的历史数据）。
	// 此时判重退回内容比对，并打一条日志 —— 那是「还在猜」的可见信号，
	// 不是可以默默接受的常态。
	NativeID string
	// SynthesizedAt 非空表示 Timestamp **不是**那一轮真实发生的时间，而是导入时
	// 的观测时刻。目前只有 ACP 会置它：ACP 的 `SessionUpdate` 协议里根本没有
	// 时间字段（SDK `SessionUpdate` 无 timestamp），重放转录得不到真实时间。
	//
	// 为什么必须标：ACP 于是**无法**用时间地板挡住重放（`TimestampFloor` 对 ACP
	// 恒不成立），它的护栏只能是 native id 判重。把「这个时间是假的」写进数据里，
	// 下游就不会拿它当权威时间去做窗口裁剪。
	SynthesizedAt time.Time
}

// ImportedExchangeAux is structured historical content attached to an imported
// exchange. Seq is assigned by the session manager when the exchange is
// persisted; importers only need to preserve its position within the assistant
// text and the normalized payload.
type ImportedExchangeAux struct {
	Line int
	// Thought = reasoning 文本。**必须用这个字段，不要用 kind=think 的 ToolCall**：
	// 前端把 `reasoning` 块渲染成灰显可折叠的思考文本，而把 think 工具卡渲染成
	// 带💭 图标的工具卡片 —— 两者外观不同。实时路径的 thought_chunk 走的正是
	// Thought，导入侧也必须走同一个字段，否则「实时看得到、导入后变样」。
	Thought  string
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
	// UsesPromptMessageID 报告该 agent 的 prompt 是否带 `messageId` 这层身份
	// （ACP 协议有，claude/codex 没有）。
	//
	// 为什么需要：没有这层身份的 agent，实时路径写的 user 行拿不到 agent 侧 id，
	// 连 `native_prompt = mindfs_id` 这种自引用的假身份都不该写 —— 那是
	// 「同一个值在两个字段里互相引用」的空转，两端各写一半、永远对不上。
	// 有了 mindfs_id → agent_id 的确定性派生（UUIDv5），实时侧与导入侧
	// 才能自然对上，不需要这种自引用桥。
	UsesPromptMessageID() bool
	// HasTranscriptTimestamps 报告该 agent 的转录条目是否带**真实**时间戳。
	//
	// 为什么需要：`TimestampFloor` 的意义是「只补比库里最新一条更新的尾段」，它只有在
	// 导入器能给出行真实发生时间时才成立。ACP 的 `SessionUpdate` 协议里**根本没有
	// 时间戳字段**，它的时间只能是读到那一刻的 `time.Now()` —— 拿这种时间去比地板，
	// 重放出来的旧回合永远「比库里新」，地板等于没有（实测 2026-10-07 某会话 9 对
	// live/import 重复就是这么来的）。所以没有真时间的 agent 不设地板，改由身份判重兜。
	HasTranscriptTimestamps() bool
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
	// MessageID 是 ACP `AgentMessageChunk.MessageId` —— 「这条文本属于哪一条 agent
	// 原生消息」。同一轮的文本可能分属多条消息（dsh 一轮常有多条 `assistant/message`
	// 记录，各自带独立 `messageId`），所以它是**步级**身份，由实时路径随回合结束
	// 写进 `agent_id`，导入侧据此认出「这条已经写过了」（D2/D3/D7）。
	//
	// 只对 ACP 协议有意义；claude 不暴露 assistant message id，恒为空。
	MessageID string `json:"messageId,omitempty"`
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

// promptTurnIDContextKey 是「本轮要递给 ACP 的 prompt messageId」的 context 键。
// 用私有类型，避免与别的 context 值撞键。
type promptTurnIDContextKey struct{}

// WithPromptTurnID 把本轮的 MindFS mindfs_id 放进 context，供 ACP 适配器当作
// `PromptRequest.MessageId` 递出去（D3：一个逻辑 id 一种表示）。
//
// 为什么走 context 而不是给 `Session.SendMessage` 加参数：`SendMessage` 是三个
// agent 共用的接口，加参数要动三处实现 + 所有调用方；而 mindfs_id 本来就是
// 「这一轮」的上下文，与 `WithExchangeSource` / `WithExchangeIdentity` 同一层。
func WithPromptTurnID(ctx context.Context, turnID string) context.Context {
	return context.WithValue(ctx, promptTurnIDContextKey{}, turnID)
}

// PromptTurnIDFromContext 取出 WithPromptTurnID 放的 mindfs_id；没有则空串。
func PromptTurnIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	turnID, _ := ctx.Value(promptTurnIDContextKey{}).(string)
	return strings.TrimSpace(turnID)
}
