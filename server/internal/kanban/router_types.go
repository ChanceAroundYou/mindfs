package kanban

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// Task Router (Plan 2026-11-14). P0 only records decisions (shadow); applying
// actions is P1. The types here are the shared surface between the Router
// service, the per-account AppContext runtime adapter and the HTTP layer.

// ---- 配置 ----

type RouterMode string

const (
	RouterModeOff        RouterMode = "off"
	RouterModeShadow     RouterMode = "shadow"
	RouterModeRoute      RouterMode = "route"
	RouterModeCoordinate RouterMode = "coordinate"
)

const (
	defaultRouterAgent  = "claude"
	defaultRouterEffort = "low"
	// Router 回合的实测量级：冷进程 + 读 brief 常常 30–45s，20s 会把正常回合
	// 掐成 fallback（2026-11-14 shadow 26 条样本 p50 34.3s / p95 41.7s）。
	defaultRouterTurnTimeout = 90 * time.Second
	defaultRouterIdleClose   = 30 * time.Minute
	defaultRouterMaxSay      = 3
	defaultRouterMaxActions  = 6
	defaultRouterMaxBytes    = 8192
)

// RouterConfig 是运行节点本地配置（config.json 的 router 段 + flags）。
// 它不是控制面偏好：同一账户在不同节点可以有不同的 Router 开关。
type RouterConfig struct {
	Mode        RouterMode
	Agent       string
	Model       string
	Effort      string
	TurnTimeout time.Duration
	IdleClose   time.Duration
	MaxSay      int
	MaxActions  int
	MaxBytes    int
	// RootModes 按 root ID 覆盖全局 Mode。root ID 是项目 basename，跨节点稳定。
	RootModes map[string]RouterMode
	// Supervision 是主动监管开关与预算（S0 观测 / S1 影子 / S2 有限执行）。
	Supervision RouterSupervisionConfig
}

// Normalized 把所有空串/未知值收敛成可执行语义：
//   - Mode 未知 -> off（未配置与显式关闭行为一致）；
//   - Agent/Effort 空 -> 默认值；
//   - 时长/上限 <=0 -> 默认值；
//   - RootModes 里的未知值 -> off，空键丢弃。
func (c RouterConfig) Normalized() RouterConfig {
	c.Mode = normalizeRouterMode(c.Mode)
	if c.Agent = strings.TrimSpace(c.Agent); c.Agent == "" {
		c.Agent = defaultRouterAgent
	}
	c.Model = strings.TrimSpace(c.Model)
	if c.Effort = strings.TrimSpace(c.Effort); c.Effort == "" {
		c.Effort = defaultRouterEffort
	}
	if c.TurnTimeout <= 0 {
		c.TurnTimeout = defaultRouterTurnTimeout
	}
	if c.IdleClose <= 0 {
		c.IdleClose = defaultRouterIdleClose
	}
	if c.MaxSay <= 0 {
		c.MaxSay = defaultRouterMaxSay
	}
	if c.MaxActions <= 0 {
		c.MaxActions = defaultRouterMaxActions
	}
	if c.MaxBytes <= 0 {
		c.MaxBytes = defaultRouterMaxBytes
	}
	if len(c.RootModes) > 0 {
		roots := make(map[string]RouterMode, len(c.RootModes))
		for rootID, mode := range c.RootModes {
			rootID = strings.TrimSpace(rootID)
			if rootID == "" {
				continue
			}
			roots[rootID] = normalizeRouterMode(mode)
		}
		c.RootModes = roots
	} else {
		c.RootModes = nil
	}
	c.Supervision = c.Supervision.Normalized()
	return c
}

// ModeForRoot 先查 RootModes，再回落 Mode，最后回落 off。
func (c RouterConfig) ModeForRoot(rootID string) RouterMode {
	rootID = strings.TrimSpace(rootID)
	if rootID != "" && c.RootModes != nil {
		if mode, ok := c.RootModes[rootID]; ok {
			return normalizeRouterMode(mode)
		}
	}
	return normalizeRouterMode(c.Mode)
}

func normalizeRouterMode(mode RouterMode) RouterMode {
	switch RouterMode(strings.ToLower(strings.TrimSpace(string(mode)))) {
	case RouterModeShadow:
		return RouterModeShadow
	case RouterModeRoute:
		return RouterModeRoute
	case RouterModeCoordinate:
		return RouterModeCoordinate
	default:
		return RouterModeOff
	}
}

// ---- 作用域（P1 只实现 task；全局 Router 预留） ----

type RouterScope string

const (
	RouterScopeTask    RouterScope = "task"
	RouterScopeProject RouterScope = "project"
	RouterScopeGlobal  RouterScope = "global"
)

type RouterBinding struct {
	Scope      RouterScope
	OwnerID    string // task_id / root_id / ""
	SessionKey string
}

type RouterResolver interface {
	Resolve(ctx context.Context, task Task) (RouterBinding, error)
}

// ---- 触发与决策 ----

type RouterTriggerKind string

const (
	RouterTriggerUserMessage RouterTriggerKind = "user_message"
	RouterTriggerRunSettled  RouterTriggerKind = "run_settled"  // P2
	RouterTriggerRunFailed   RouterTriggerKind = "run_failed"   // P2
	RouterTriggerGateWaiting RouterTriggerKind = "gate_waiting" // P2
	RouterTriggerRunStalled  RouterTriggerKind = "run_stalled"  // P2
	// S3：直连回合没有 Run，回复行停在 running 而主会话已空闲时用它。
	RouterTriggerDirectStalled RouterTriggerKind = "direct_stalled"
	RouterTriggerStartup       RouterTriggerKind = "startup_reconcile" // P1
)

type RouterTrigger struct {
	Kind           RouterTriggerKind
	TaskID         string
	RootID         string
	UserActivityID string
	PlaceholderID  string
	TriggerRef     string // run_id / node_run_id / ""
	// TurnID 为空时由 RouterService 生成；恢复/重试时调用方复用已落库的值。
	TurnID string
	// Mode 是入队时的 Router 模式（shadow 只观察，route 真正应用动作）。
	// 为空时按 shadow 处理，保持老调用方语义。
	Mode         RouterMode
	Brief        *SituationBrief     // shadow: legacy 分发前快照；route 在消费时重新构建
	LegacyAction *RouterLegacyAction // shadow only
	// Event 非 nil 即 coordinate 运行期事件回合；用户留言恒为 nil。
	Event *RouterEvent
}

type RouterLegacyAction struct {
	Kind            string `json:"kind"` // workflow|direct|none|failed
	WorkflowID      string `json:"workflow_id,omitempty"`
	WorkflowVersion string `json:"workflow_version,omitempty"`
	RunID           string `json:"run_id,omitempty"`
	Error           string `json:"error,omitempty"`
}

type RouterActionKind string

const (
	RouterActionSay           RouterActionKind = "say"
	RouterActionRunWorkflow   RouterActionKind = "run_workflow"
	RouterActionMessageAgent  RouterActionKind = "message_agent"
	RouterActionAskUser       RouterActionKind = "ask_user"
	RouterActionSilent        RouterActionKind = "silent"
	RouterActionDeliver       RouterActionKind = "deliver_to_active_run"
	RouterActionAnswerWaiting RouterActionKind = "answer_waiting_node"
	RouterActionSuggestFinish RouterActionKind = "suggest_finish" // P2
	// 主动监管（S1/S2）：重试一个已判定为「可重试」的失败节点，或把情况
	// 直接告诉用户。两者都只在带 supervision 块的事件回合可用。
	RouterActionRetryNode  RouterActionKind = "retry_node"
	RouterActionNotifyUser RouterActionKind = "notify_user"
	// S3：唤醒一个还有 open 节点、但没有回合在跑的 Run；把机器停手交还给用户。
	RouterActionResumeRun RouterActionKind = "resume_run"
	RouterActionEscalate  RouterActionKind = "escalate"
	// S3：直连回合停摆——结算卡住的回复行，再用原始留言重开一轮。
	RouterActionRetryDirect RouterActionKind = "retry_direct"
)

// run_workflow 的会话模式（2026-11-20 用户决策）：默认新开一段会话；
// continue 接续任务最近实际在用的那条会话。
const (
	RouterSessionModeNew      = "new"
	RouterSessionModeContinue = "continue"
)

type RouterAction struct {
	Seq        int              `json:"seq"`
	Kind       RouterActionKind `json:"kind"`
	Body       string           `json:"body,omitempty"`    // say / message_agent / deliver / answer
	Purpose    string           `json:"purpose,omitempty"` // chat|progress
	WorkflowID string           `json:"workflow_id,omitempty"`
	VersionID  string           `json:"version_id,omitempty"`
	Question   string           `json:"question,omitempty"`
	Options    []string         `json:"options,omitempty"`
	RunID      string           `json:"run_id,omitempty"`
	// NodeRunID 是 answer_waiting_node / retry_node / retry_direct 的目标：工具在
	// 当轮就把它解析成具体行（retry_direct 带的是回复活动 ID），apply 阶段不再
	// 重新判定「哪个节点在等我答复 / 该重试哪一行 / 哪条回复卡住了」。
	NodeRunID string `json:"node_run_id,omitempty"`
	// SessionMode 只对 run_workflow 有意义：new（默认）= 这一轮开一段新会话，
	// continue = 接续任务最近实际在用的那条会话。由 Router 跨工作流决定。
	SessionMode string `json:"session_mode,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// IsExecutionAction 报告动作是否会启动/继续一次执行。用于跨动作校验与
// shadow 的 agreement 比较。答复停住的节点会让它重跑，也算执行类动作。
func (a RouterAction) IsExecutionAction() bool {
	return a.Kind == RouterActionRunWorkflow || a.Kind == RouterActionMessageAgent || a.Kind == RouterActionAnswerWaiting
}

// routerActionKinds 把动作种类压成一行日志（say,deliver...）。只用于观测：
// 决策问题首先是「模型到底做了什么」，这比完整动作体省好几行日志。
func routerActionKinds(actions []RouterAction) string {
	if len(actions) == 0 {
		return "none"
	}
	kinds := make([]string, 0, len(actions))
	for _, action := range actions {
		kinds = append(kinds, string(action.Kind))
	}
	return strings.Join(kinds, ",")
}

type RouterDecisionStatus string

const (
	RouterDecisionShadow   RouterDecisionStatus = "shadow"
	RouterDecisionApplying RouterDecisionStatus = "applying"
	RouterDecisionApplied  RouterDecisionStatus = "applied"
	RouterDecisionRejected RouterDecisionStatus = "rejected"
	RouterDecisionFailed   RouterDecisionStatus = "failed"
	RouterDecisionFallback RouterDecisionStatus = "fallback"
	RouterDecisionSkipped  RouterDecisionStatus = "skipped"
	RouterDecisionBypassed RouterDecisionStatus = "bypassed"
)

const (
	RouterAgreementMatch    = "match"
	RouterAgreementMismatch = "mismatch"
	RouterAgreementUnknown  = "unknown"
	RouterAgreementNoRouter = "no_router"
)

type RouterDecision struct {
	ID                    string
	TaskID                string
	RootID                string
	Scope                 RouterScope
	OwnerID               string
	UserActivityID        string
	PlaceholderActivityID string
	TriggerKind           RouterTriggerKind
	TriggerRef            string
	RouterSessionKey      string
	TurnID                string
	BriefHash             string
	BriefJSON             string
	Actions               []RouterAction
	Status                RouterDecisionStatus
	AppliedSeq            int
	LegacyAction          *RouterLegacyAction
	Agreement             string
	Error                 string
	LatencyMS             int
	PromptTokens          int
	CompletionTokens      int
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// ---- Agent 接口（由 api.AppContext 实现；kanban 不 import api） ----

type RouterTurnMeta struct {
	Model            string
	PromptHash       string
	FinalText        string
	LatencyMS        int
	PromptTokens     int
	CompletionTokens int
}

type RouterAgentRequest struct {
	RootID                string
	TaskID                string
	SessionKey            string
	Cwd                   string
	ConfigDir             string
	Agent                 string
	Model                 string
	Effort                string
	Prompt                string // brief JSON
	DeveloperInstructions string
	PromptHash            string
	Tools                 []RouterToolSpec
	ToolPolicy            RouterToolPolicy
	Timeout               time.Duration
}

type RouterAgentResult struct {
	SessionKey string
	FinalText  string
	Meta       RouterTurnMeta
	// ConfigDir/Cwd 由 agent 适配层回填，供 router_sessions 诊断；
	// kanban 层不知道账户目录布局。
	ConfigDir string
	Cwd       string
}

type RouterAgent interface {
	RunRouterTurn(ctx context.Context, req RouterAgentRequest) (RouterAgentResult, error)
	CloseRouterSession(ctx context.Context, rootID, sessionKey string) error
}

// ---- Tool ----

type RouterToolClass string

const (
	RouterToolRead  RouterToolClass = "read"
	RouterToolWrite RouterToolClass = "write"
)

type RouterToolSpec struct {
	Name        string
	Description string
	InputSchema map[string]any
	Class       RouterToolClass
	// Handler 必须对 read 工具直接执行、对 write 工具只记录动作并返回
	// {"status":"recorded","seq":N}。handler 不依赖 SDK 类型。
	Handler func(ctx context.Context, args json.RawMessage) (string, error)
}

type RouterToolPolicy struct {
	AllowedTools    []string
	DisallowedTools []string
	DenyByDefault   bool
}

// RouterNamedToolPrefix 是进程内 MCP server 暴露给 CLI 的工具名前缀。
const RouterNamedToolPrefix = "mcp__mindfs_router__"

// DefaultRouterToolPolicy 是 Router 会话的唯一工具白名单：只允许进程内
// Router MCP 工具，其它一切（文件、Shell、网络、子代理、原生提问）默认拒绝。
func DefaultRouterToolPolicy() RouterToolPolicy {
	return RouterToolPolicy{
		AllowedTools: []string{RouterNamedToolPrefix + "*"},
		DisallowedTools: []string{
			"Bash", "Read", "Write", "Edit", "MultiEdit", "Glob", "Grep",
			"WebFetch", "WebSearch", "Task", "AskUserQuestion", "TodoWrite",
			"NotebookEdit",
		},
		DenyByDefault: true,
	}
}

// ---- ModelPolicy（预留，P1 不实现） ----

type ModelPolicy interface {
	SelectRouterModel(ctx context.Context, task Task, trigger RouterTrigger) (string, error)
}
