package session

import (
	"encoding/json"
	"strings"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
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
