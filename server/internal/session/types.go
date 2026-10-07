package session

import (
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
// 用途见 usecase 的投影：同一轮被两个写入者各写一行时，投影保留 live、隐藏 import。
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
	Seq              int                    `json:"seq"`
	Role             string                 `json:"role"`
	Source           string                 `json:"source,omitempty"`
	Agent            string                 `json:"agent,omitempty"`
	Model            string                 `json:"model,omitempty"`
	ModelDisplayName string                 `json:"model_display_name,omitempty"`
	Mode             string                 `json:"mode,omitempty"`
	Effort           string                 `json:"effort,omitempty"`
	FastService      string                 `json:"fast_service,omitempty"`
	Content          string                 `json:"content"`
	TokenUsage       *agenttypes.TokenUsage `json:"token_usage,omitempty"`
	Timestamp        time.Time              `json:"timestamp"`
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

func CompactExchangeAux(aux ExchangeAux) (ExchangeAux, bool) {
	if aux.ToolCall == nil {
		if aux.Todo != nil || aux.Plan != nil || aux.Compact != nil {
			return aux, true
		}
		return ExchangeAux{}, false
	}

	toolCall := CompactToolCall(*aux.ToolCall)
	aux.ToolCall = &toolCall
	aux.Thought = ""
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
