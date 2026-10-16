package session

import (
	"strings"

	agenttypes "mindfs/server/internal/agent/types"
)

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
