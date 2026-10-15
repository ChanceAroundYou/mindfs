package session

import (
	"encoding/json"
	"os"
	"strings"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
)

const (
	TypeChat    = "chat"
	TypeView    = "view"
	TypeCommand = "command"
)

// Exchange.Source 取值：谁写的这一行。
// 实时路径（MindFS 自己在驱动会话）写 live，转录导入器写 import；老数据为空。
// 写入来源用于会话所有权与同步逻辑；它不改变 GET 返回的持久化行。
const (
	ExchangeSourceLive   = "live"
	ExchangeSourceImport = "import"
)

// ExchangeHasLiveSignature 报告这一行是否带「实时路径」独有字段。
// 老数据（2026-09 之前）没有 source 标注，只能靠这些字段认：转录导入器写行时
// mode / effort / fast_service 恒为空串、model_display_name 与 token_usage 也不填，
// 而实时路径会带上。
func ExchangeHasLiveSignature(exchange Exchange) bool {
	return strings.TrimSpace(exchange.ModelDisplayName) != "" ||
		exchange.TokenUsage != nil ||
		strings.TrimSpace(exchange.Mode) != "" ||
		strings.TrimSpace(exchange.Effort) != "" ||
		strings.TrimSpace(exchange.FastService) != ""
}

// SessionIsLiveOwned 报告这个会话的持久化是否归实时路径（"谁驱动，谁落盘"）。
//
// 判据是**推导**出来的，不落字段：会话里存在实时路径写的行（Source=live，老数据用
// ExchangeHasLiveSignature 兜底）就算被 MindFS 驱动过。
// 推导而不是存字段的好处：所有权可以随"第一次从 MindFS 发消息"自动翻转，不需要回填、
// 也不会与 Session.Source（fork 用来存来源描述）冲突。
func SessionIsLiveOwned(exchanges []Exchange) bool {
	for _, exchange := range exchanges {
		if exchange.Source == ExchangeSourceLive {
			return true
		}
		if exchange.Source != "" {
			continue
		}
		if ExchangeHasLiveSignature(exchange) {
			return true
		}
	}
	return false
}

type Session struct {
	Key               string                   `json:"key"`
	Type              string                   `json:"type"`
	ParentSessionKey  string                   `json:"parent_session_key,omitempty"`
	ParentToolCallID  string                   `json:"parent_tool_call_id,omitempty"`
	Source            string                   `json:"source,omitempty"`
	TaskID            string                   `json:"task_id,omitempty"`
	AgentCtxSeq       map[string]int           `json:"agent_ctx_seq,omitempty"`
	Model             string                   `json:"model,omitempty"`
	Shell             string                   `json:"shell,omitempty"`
	PlanMode          bool                     `json:"plan_mode,omitempty"`
	Name              string                   `json:"name"`
	Exchanges         []Exchange               `json:"exchanges"`
	RelatedFiles      []RelatedFile            `json:"related_files"`
	RelatedWorktree   *RelatedWorktree         `json:"related_worktree,omitempty"`
	// Aux 是会话行的内容徽标数据源（G-AD）：ask_user/plan/todo/task/error。
	// 与任务行的 kanban.TaskAuxFlags 同名同义，但按会话落盘 —— 无 task_id 的
	// 普通聊天会话也有这份数据（任务行 aux 只写给有 task_id 的会话）。
	Aux               SessionAuxFlags           `json:"aux_flags"`
	LastContextWindow agenttypes.ContextWindow `json:"last_context_window,omitempty"`
	PinnedAt          *time.Time               `json:"pinned_at,omitempty"`
	// ArchivedAt 非空 = 已归档。归档只是从主面板隐去，正文/链接都还在（能深链接打开、
	// 搜索搜得到），只有删除才真正清内容。与 PinnedAt 同构：NULL = 未归档。
	ArchivedAt *time.Time `json:"archived_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	ClosedAt   *time.Time `json:"closed_at,omitempty"`
}

type Exchange struct {
	Seq              int    `json:"seq"`
	Role             string `json:"role"`
	Source           string `json:"source,omitempty"`
	Agent            string `json:"agent,omitempty"`
	Model            string `json:"model,omitempty"`
	ModelDisplayName string `json:"model_display_name,omitempty"`
	Mode             string `json:"mode,omitempty"`
	Effort           string `json:"effort,omitempty"`
	FastService      string `json:"fast_service,omitempty"`
	// LegacyContent 是 **S2d-1 之后主日志里的唯一残留正文**，只作回填输入。
	//
	// json tag 刻意**不改名**：老会话文件里的 `"content"` 必须还能解析，否则回填
	// 之前的历史正文会被当成空白丢掉 —— 而它正是那份正文此刻唯一的副本。
	// 新写入的行一律为空（见 `AddExchangeForAgentAt` 的各调用点）。
	//
	// 唯一允许读它的地方是 `backfillTurnsFromWindow`。任何其它读取都是「两份真相」：
	// 方案 D1 定的是块流是唯一正文载体，而两份真相迟早分叉（实测已分叉过一次 ——
	// 见 `docs/session-blocks-plan.md` §1）。
	//
	// 删除时机：全网回填验证完成后（每台机器的每个会话都被读过至少一次）。
	LegacyContent string                 `json:"content"`
	TokenUsage    *agenttypes.TokenUsage `json:"token_usage,omitempty"`
	Timestamp     time.Time              `json:"timestamp"`

	// ── 轮次元数据（CUSTOM(G-BH)，方案 §3.1）────────────────────────────
	// 正文搬去块流之后，主日志一轮一行只留身份与展示字段。
	//
	// MindFSID 是 **MindFS 权威身份**（MindFS mint 的唯一 id，系统内实际使用）：
	// 落盘即定、永不改。它是 deterministically derived from AgentID when available
	// (UUIDv5), otherwise from sessionKey+role+seq (UUIDv5 fallback).
	// 全系统只有两类 id：MindFSID（MindFS 铸，权威）与 AgentID（agent 返回，桥梁）。
	MindFSID string `json:"mindfs_id,omitempty"`
	// TurnSeq 是「打开该轮的 user 行 seq」，排序 / 人读用；与 MindFSID 一一对应。
	TurnSeq int `json:"turn_seq,omitempty"`
	// AgentID 是 agent 侧的身份（可空、可后补）。各 agent 模式不同：
	// claude = 转录条目 `uuid`，ACP = `messageId`，codex = 尚未实证。
	//
	// 它**不是** MindFS 的身份 —— 只是一个桥梁键：恢复 / repoint / 跨写入者对齐时
	// 靠它认出「agent 侧的同一条消息」。判重优先级：AgentID > MindFSID > 内容兜底。
	AgentID string `json:"agent_id,omitempty"`
	// Partial 标记「本轮未收尾」：崩溃 / 中断丢缓冲区时靠 import 补齐（方案 D13）。
	Partial bool `json:"partial,omitempty"`
}

// exchangeLegacyAlias 处理老文件里的旧字段名，重命名不丢数据。
//
// json tag 改名后，老 JSONL 里写的是旧键名 —— 没有这层，133 个存量会话读出来
// MindFSID / AgentID 全空，等于把回填过的 39517 块的身份丢了。读取侧兼容即可，
// 写入侧只写新键名。
type exchangeLegacyAlias struct {
	LegacyTurnID   string `json:"turn_id"`     // → MindFSID
	LegacyNativeID string `json:"native_id"`   // → AgentID (Block)
	LegacyNatTurn  string `json:"native_turn"` // → AgentID
	LegacyNatPrmpt string `json:"native_prompt"`
}

// UnmarshalJSON 兼容老键名：旧字段读进来，新键名优先。
func (e *Exchange) UnmarshalJSON(data []byte) error {
	type alias Exchange // 避免递归
	var raw struct {
		alias
		exchangeLegacyAlias
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*e = Exchange(raw.alias)
	if e.MindFSID == "" {
		e.MindFSID = raw.LegacyTurnID
	}
	if e.AgentID == "" {
		// 老数据 user 行在 native_prompt、agent 行在 native_turn —— 合并成一个键。
		e.AgentID = strings.TrimSpace(firstNonEmptyRaw(raw.LegacyNatTurn, raw.LegacyNatPrmpt))
	}
	return nil
}

func firstNonEmptyRaw(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

type ExchangeAux struct {
	Seq       int                       `json:"seq"`
	Line      int                       `json:"line"`
	ToolCall  *agenttypes.ToolCall      `json:"toolcall,omitempty"`
	Thought   string                    `json:"thought,omitempty"`
	ThoughtID string                    `json:"thought_id,omitempty"`
	Todo      *agenttypes.TodoUpdate    `json:"todo,omitempty"`
	Plan      *agenttypes.PlanUpdate    `json:"plan,omitempty"`
	Compact   *agenttypes.CompactNotice `json:"compact,omitempty"`
}

// CompactExchangeAux 去掉「展开时才需要、折叠卡片渲染用不到」的字段。
//
// **压缩对象只有 toolcall 一类。** `Thought` / `Todo` / `Plan` / `Compact` 全部原样保留：
// 前端把 reasoning 块渲染成灰显的思考文本，窗口少给一块，用户看到的就是
// 「回复的时候好好的，回复完（重新读）就消失了」。实测存量 aux 里有 10826 条
// `thought` 条目，全在这条路径上。
//
// 原先这里是 `aux.Thought = ""` + 「非 toolcall 且非 todo/plan/compact 就整条丢弃」，
// 两条路都会把 reasoning 扔掉。改成 `auxBlock` 之后 reasoning 升格成块类型，
// 这里必须跟上 —— 判据统一用「这条 aux 有没有任何可渲染内容」。
func CompactExchangeAux(aux ExchangeAux) (ExchangeAux, bool) {
	if aux.ToolCall != nil {
		toolCall := CompactToolCall(*aux.ToolCall)
		aux.ToolCall = &toolCall
	}
	if aux.ToolCall == nil && aux.Todo == nil && aux.Plan == nil && aux.Compact == nil &&
		strings.TrimSpace(aux.Thought) == "" {
		return ExchangeAux{}, false
	}
	return aux, true
}

// CUSTOM(G-AT): 上游没有这个「窗口专用」的更轻压缩。窗口响应里 exchange_aux 占 87%，
// 其中 edit 的 content 一块就是 101 KB；卡片展开时本就走懒加载，所以窗口只需要
// 能渲染折叠卡片的最小字段。全量 sync / 重锚定仍走 CompactExchangeAux（保留 content）。
// CompactExchangeAuxLight 是窗口专用的更激进压缩：在 CompactExchangeAux 基础上，
// 把「详情只在展开时需要」的 kind（edit/read/execute）的 content 也清空。
//
// 为什么安全：这些 kind 的折叠卡片只需要 kind/title/status/locations；展开时
// ToolCallCard 的 needsRemoteDetails（edit/read 走 !hasContent、execute 走
// !hasExecuteOutput）会触发 GET /api/sessions/{key}/toolcalls/{callID} 懒加载，
// 拿回完整详情。窗口因此只下发能渲染折叠卡片的最小字段 —— 实测一个会话 65 个
// edit 的 content 占 101 KB，是 exchange_aux 里最大的一块。
//
// 为什么只对窗口用：全量 sync / 重锚定路径仍走 CompactExchangeAux，保留完整
// content，避免「重锚定后卡片内容闪一下又变了」。
func CompactExchangeAuxLight(aux ExchangeAux) (ExchangeAux, bool) {
	compacted, ok := CompactExchangeAux(aux)
	if !ok {
		return ExchangeAux{}, false
	}
	if compacted.ToolCall != nil {
		lightCompactToolCall(compacted.ToolCall)
	}
	return compacted, true
}

func lightCompactToolCall(toolCall *agenttypes.ToolCall) {
	switch toolCall.Kind {
	case agenttypes.ToolKindEdit, agenttypes.ToolKindRead, agenttypes.ToolKindExecute:
		toolCall.Content = nil
	}
}

func CompactToolCall(toolCall agenttypes.ToolCall) agenttypes.ToolCall {
	preserveContent := PreserveToolCallContent(toolCall.Kind)
	switch {
	case preserveContent:
	case PreserveCommandExecutionContent(toolCall):
		toolCall.Content = truncateToolCallContent(toolCall.Content, maxExecToolCallContentBytes)
	default:
		toolCall.Content = nil
	}
	if !preserveContent {
		toolCall.Meta = compactToolCallMeta(toolCall.Meta)
	} else if toolCall.Kind == agenttypes.ToolKindEdit && len(toolCall.Content) > 0 {
		// CUSTOM(G-AT): 上游保留 edit 的 meta.input/output，而它们与 content 是同一份
		// diff 的两种形状（实测占 130 KB）。前端只在 content 为空时才回退到 meta.input。
		// edit 的 content 是格式化后的 diff，meta.input/output 是同一份 old/new
		// 文本的未格式化副本。前端只在 content 为空时才拿 meta.input 当 fallback
		// （见 ToolCallCard 的 detailSections 回退），content 非空时这两个字段是
		// 纯冗余 —— 实测一个会话 65 个 edit 的 meta.input 占 109 KB。
		// 只对 edit 生效：ask_user 的 meta.input 是 questions 的 fallback、
		// todo 的 meta.input 是待办列表本身，都不能丢。
		toolCall.Meta = dropEditRedundantMeta(toolCall.Meta)
	}
	return toolCall
}

// dropEditRedundantMeta 丢掉 edit 工具调用里与 content 重复的 input/output。
// 保留其余字段（如 filePath）—— 它们参与前端渲染与缓存键。
func dropEditRedundantMeta(meta map[string]any) map[string]any {
	if len(meta) == 0 {
		return meta
	}
	out := make(map[string]any, len(meta))
	for key, value := range meta {
		if key == "input" || key == "output" {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func compactToolCallMeta(meta map[string]any) map[string]any {
	if len(meta) == 0 {
		return meta
	}
	out := make(map[string]any, len(meta))
	for key, value := range meta {
		switch key {
		case "output":
			continue
		default:
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func PreserveToolCallContent(kind agenttypes.ToolKind) bool {
	switch kind {
	case agenttypes.ToolKindEdit,
		agenttypes.ToolKindDelete,
		agenttypes.ToolKindMove,
		agenttypes.ToolKindAskUser,
		agenttypes.ToolKindTodo,
		agenttypes.ToolKindTask:
		return true
	default:
		return false
	}
}

const maxExecToolCallContentBytes = 128 * 1024
const truncationMarker = "\n...(truncated)"

func PreserveCommandExecutionContent(toolCall agenttypes.ToolCall) bool {
	if toolCall.Kind != agenttypes.ToolKindExecute || toolCall.RawType != "commandExecution" {
		return false
	}
	if toolCall.Meta == nil {
		return false
	}
	source, _ := toolCall.Meta["source"].(string)
	return strings.EqualFold(strings.TrimSpace(source), "userShell")
}

func InferCommandShellFromAux(aux map[int][]ExchangeAux) string {
	bestSeq := 0
	shell := ""
	for seq, items := range aux {
		if seq < bestSeq {
			continue
		}
		for _, item := range items {
			if item.ToolCall == nil || item.ToolCall.Meta == nil {
				continue
			}
			toolCall := item.ToolCall
			if toolCall.Kind != agenttypes.ToolKindExecute || toolCall.RawType != "commandExecution" {
				continue
			}
			source, _ := toolCall.Meta["source"].(string)
			phase, _ := toolCall.Meta["phase"].(string)
			value, _ := toolCall.Meta["shell"].(string)
			value = strings.TrimSpace(value)
			if !strings.EqualFold(strings.TrimSpace(source), "userShell") || strings.TrimSpace(phase) != "final" || value == "" {
				continue
			}
			bestSeq = seq
			shell = value
		}
	}
	return shell
}

func truncateToolCallContent(items []agenttypes.ToolCallContentItem, maxBytes int) []agenttypes.ToolCallContentItem {
	if maxBytes <= 0 || len(items) == 0 {
		return nil
	}
	out := make([]agenttypes.ToolCallContentItem, 0, len(items))
	remaining := maxBytes
	for _, item := range items {
		if remaining <= 0 {
			break
		}
		if item.Type == "text" {
			limit := remaining
			if len(item.Text) > limit {
				if remaining <= len(truncationMarker) {
					item.Text = truncationMarker[:remaining]
					out = append(out, item)
					break
				}
				limit -= len(truncationMarker)
			}
			text, used, truncated := truncateStringBytes(item.Text, limit)
			item.Text = text
			remaining -= used
			if truncated {
				item.Text += truncationMarker
				out = append(out, item)
				break
			}
		}
		out = append(out, item)
	}
	return out
}

func truncateStringBytes(value string, maxBytes int) (string, int, bool) {
	if maxBytes <= 0 {
		return "", 0, value != ""
	}
	if len(value) <= maxBytes {
		return value, len(value), false
	}
	end := maxBytes
	for end > 0 && (value[end]&0xC0) == 0x80 {
		end--
	}
	if end == 0 {
		return "", 0, true
	}
	return value[:end], end, true
}

type RelatedFile struct {
	RootID           string `json:"root_id,omitempty"`
	RepoPath         string `json:"repo_path,omitempty"`
	RepoName         string `json:"repo_name,omitempty"`
	RepoKind         string `json:"repo_kind,omitempty"`
	Path             string `json:"path"`
	Head             string `json:"head,omitempty"`
	Relation         string `json:"relation"`
	CreatedBySession bool   `json:"created_by_session"`
}

type RelatedWorktree struct {
	RootID    string    `json:"root_id"`
	Path      string    `json:"path"`
	Branch    string    `json:"branch,omitempty"`
	Head      string    `json:"head,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SessionAuxFlags 是会话行的内容徽标数据源（G-AD）：ask_user/plan/todo/task/error。
// 与任务行的 kanban.TaskAuxFlags 同名同义，但按会话落盘 —— 无 task_id 的普通聊天
// 会话也有这份数据（任务行 aux 只写给有 task_id 的会话，所以不能复用那张表）。
type SessionAuxFlags struct {
	AskUserWaiting bool   `json:"ask_user_waiting"`
	HasPlan        bool   `json:"has_plan"`
	HasTodos       bool   `json:"has_todos"`
	HasTask        bool   `json:"has_task"`
	SessionError   string `json:"session_error,omitempty"`
}

// SessionAuxFlagsPatch 是 UpdateSessionAuxFlags 的部分更新：nil = 不动。
// 与 kanban.TaskAuxFlagsPatch 同构（跨域同义不同包，usecase 禁直调别域存储，
// 所以镜像一份而不是 import kanban —— 见 layering.md 分层铁律）。
type SessionAuxFlagsPatch struct {
	AskUserWaiting *bool
	HasPlan        *bool
	HasTodos       *bool
	HasTask        *bool
	SessionError   *string
}

// AuxFlagsPayload 是 session.Aux 的列表/广播 payload 形状（G-AD）。
// 三个构造点（sessionListResponse + 两处 session.meta.updated）共用它，
// 字段增减只改一处 —— 以前 related_worktree 三处各写一遍，漏一处就是静默不一致。
func (f SessionAuxFlags) AuxFlagsPayload() map[string]any {
	return map[string]any{
		"ask_user_waiting": f.AskUserWaiting,
		"has_plan":         f.HasPlan,
		"has_todos":        f.HasTodos,
		"has_task":         f.HasTask,
		"session_error":    f.SessionError,
	}
}

// WorktreeMissing 报告会话的 worktree 归属是否已失效（目录被删了）。
//
// 归属路径会事后消失——DELETE /api/git/worktrees、wt-finish.sh cleanup、用户手工 rm，
// 都不是 mindfs 干的，所以没有任何代码清这个字段。失效的后果是会话在 mindfs 里
// 读得到、却发不了消息（agent cwd 指向不存在的目录）。
//
// 刻意不复用 gitview.IsWorktree：它答的是「这是不是 git linked worktree」，
// 主 checkout / 普通目录 / 不存在的路径都返回 false，语义不对。
//
// 这是**派生状态**，不落库：目录重建后下一次调用自动转 false，不需要迁移。
func (wt *RelatedWorktree) WorktreeMissing() bool {
	if wt == nil {
		return false
	}
	p := strings.TrimSpace(wt.Path)
	if p == "" {
		return false
	}
	// 非目录（路径存在但指向文件）同样算失效：agent 的 cwd 必须是目录。
	info, err := os.Stat(p)
	return err != nil || !info.IsDir()
}

type SearchOptions struct {
	Query string
	Limit int
}

type SearchHit struct {
	Key              string     `json:"key"`
	Type             string     `json:"type"`
	ParentSessionKey string     `json:"parent_session_key,omitempty"`
	ParentToolCallID string     `json:"parent_tool_call_id,omitempty"`
	Agent            string     `json:"agent,omitempty"`
	Model            string     `json:"model,omitempty"`
	Shell            string     `json:"shell,omitempty"`
	Name             string     `json:"name"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	ClosedAt         *time.Time `json:"closed_at,omitempty"`
	MatchType        string     `json:"match_type"`
	MatchScore       int        `json:"match_score"`
	Seq              int        `json:"seq"`
	Snippet          string     `json:"snippet,omitempty"`
}

// InferAgentFromSession derives the display agent from session data.
//
// 优先看最后一轮 exchange 的 agent（最准）。列表路径不加载 exchanges，于是回落到
// 绑定表回填出来的 AgentCtxSeq —— **多绑定时取 ctx_seq 最大的那个**：ctx_seq 是会话
// 的行数游标（UpdateAgentState 写的是 len(Exchanges)），谁最大谁最后写过这一串
// exchange，也就是最近在用的 agent。
//
// 以前这里只处理 len==1，多绑定（中途换过 agent 的会话）直接返回空串，徽标落回
// 「AI」文字占位 —— 2026-10-07 实测「大多数用 dsh 的任务没有 agent 徽标」就是它：
// dsh 恰恰都是在已有 claude 会话里中途切过去的，必然是双绑定。
func InferAgentFromSession(s *Session) string {
	if s == nil {
		return ""
	}
	for i := len(s.Exchanges) - 1; i >= 0; i-- {
		if agent := strings.TrimSpace(s.Exchanges[i].Agent); agent != "" {
			return agent
		}
	}
	best := ""
	bestSeq := -1
	for agent, seq := range s.AgentCtxSeq {
		agent = strings.TrimSpace(agent)
		if agent == "" {
			continue
		}
		// 平局（含都还是 0 的新绑定）按名字定序，保证同一个会话每次报同一个 agent，
		// 否则 map 遍历顺序会让徽标在两次列表之间来回跳。
		if seq > bestSeq || (seq == bestSeq && agent < best) {
			best, bestSeq = agent, seq
		}
	}
	return best
}

// InferEffortFromSession derives the latest non-empty effort from session data.
func InferEffortFromSession(s *Session) string {
	if s == nil || len(s.Exchanges) == 0 {
		return ""
	}
	return strings.TrimSpace(s.Exchanges[len(s.Exchanges)-1].Effort)
}

// InferFastServiceFromSession derives the latest fast-service setting from session data.
func InferFastServiceFromSession(s *Session) string {
	if s == nil || len(s.Exchanges) == 0 {
		return ""
	}
	return strings.TrimSpace(s.Exchanges[len(s.Exchanges)-1].FastService)
}

// InferModeFromSession derives the latest non-empty mode from session data.
func InferModeFromSession(s *Session) string {
	if s == nil || len(s.Exchanges) == 0 {
		return ""
	}
	return strings.TrimSpace(s.Exchanges[len(s.Exchanges)-1].Mode)
}
