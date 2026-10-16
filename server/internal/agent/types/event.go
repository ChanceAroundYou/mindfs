package types

// EventType defines the type of a session event.
type EventType string

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
