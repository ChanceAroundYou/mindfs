package session

import (
	"encoding/json"

	agenttypes "mindfs/server/internal/agent/types"
)

// CUSTOM(G-BH): 块流是会话正文的**唯一**载体（方案 §3.1/§3.2，决策 D1）。
//
// 为什么必须保留：上游的 Exchange.Content 是「一轮正文 = 一段字符串」，于是
// 「只有 reasoning、没有 text 块」的轮次落库成空串 —— 流式时 thought_chunk 实时推看得见，
// 收尾落库后正文消失（实测 os 模型 ~15% 轮次）。块流把 reasoning / text / tool_call /
// todo / plan / compact 都当成一等公民，正文不再依赖「必须有 text 块」。
//
// 双写期（S2a）：块流与旧 Content/aux **并存**，读路径仍走旧格式，可随时整体回滚。
// S2b 起读路径改走块流，S2d 删除 Content。

// BlockType 是块的类型。序列化成小写蛇形，与 aux 的既有命名习惯一致。
type BlockType string

const (
	BlockTypeReasoning BlockType = "reasoning"
	BlockTypeText      BlockType = "text"
	BlockTypeToolCall  BlockType = "tool_call"
	BlockTypeTodo      BlockType = "todo"
	BlockTypePlan      BlockType = "plan"
	BlockTypeCompact   BlockType = "compact"
)

// Valid 报告是否是已知的块类型。未知类型不报错（读侧跳过），保证 agent 加新类型时不炸旧服务。
func (t BlockType) Valid() bool {
	switch t {
	case BlockTypeReasoning, BlockTypeText, BlockTypeToolCall, BlockTypeTodo, BlockTypePlan, BlockTypeCompact:
		return true
	default:
		return false
	}
}

// Block 是块流 `<key>.blocks.jsonl` 里的一行。
//
// 身份三级（方案 §3.3）：mindfs_id（MindFS 权威，全 agent 通用）+ step（agent 的
// 消息投递单元）+ pos（步内段序）。agent_id 是桥梁键，可空、可后补、fork 后可变更 ——
// 它**不是**权威身份（repoint.go:35 明载 fork 后 claude 产出全新 uuid）。
type Block struct {
	Seq      int       `json:"seq"`
	Pos      int       `json:"pos"`
	Revision int       `json:"revision"`
	Type     BlockType `json:"type"`

	// 正文载荷，按 Type 只用其中一个。
	Text     string                    `json:"text,omitempty"`
	ToolCall *agenttypes.ToolCall      `json:"toolcall,omitempty"`
	Todo     *agenttypes.TodoUpdate    `json:"todo,omitempty"`
	Plan     *agenttypes.PlanUpdate    `json:"plan,omitempty"`
	Compact  *agenttypes.CompactNotice `json:"compact,omitempty"`

	// Step 是步序。块在步收口前落盘时写哨兵 -1，收口时用 (seq,pos)+revision+1 补正。
	Step int `json:"step"`

	// MindFSID 是 MindFS 权威身份；AgentID 是 agent 侧桥梁键（可空）。
	MindFSID string `json:"mindfs_id,omitempty"`
	AgentID  string `json:"agent_id,omitempty"`

	// 子代理归属（方案 D16）。
	ParentToolUseID string `json:"parent_tool_use_id,omitempty"`
	TaskID          string `json:"task_id,omitempty"`
	SubagentType    string `json:"subagent_type,omitempty"`
}

// UnmarshalJSON 兼容块流里的旧键名：turn_id → MindFSID、native_id → AgentID。
//
// 没有这层，重命名之后读存量 39517 个块会全丢身份 —— 块与轮次挂不上，
// 「按轮次折叠 / 重放」之类全失效。写入侧只写新键名，旧键名只读不写。
func (b *Block) UnmarshalJSON(data []byte) error {
	type alias Block
	var raw struct {
		alias
		LegacyMindFSID string `json:"turn_id"`
		LegacyAgentID  string `json:"native_id"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*b = Block(raw.alias)
	if b.MindFSID == "" {
		b.MindFSID = raw.LegacyMindFSID
	}
	if b.AgentID == "" {
		b.AgentID = raw.LegacyAgentID
	}
	return nil
}

func newBlock(seq, pos int, blockType BlockType, step int, turnID, text string) Block {
	return Block{Seq: seq, Pos: pos, Type: blockType, Text: text, Step: step, MindFSID: turnID}
}
