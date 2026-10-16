package session

import (
	"strings"

	agenttypes "mindfs/server/internal/agent/types"
)

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
