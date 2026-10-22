package session

import (
	"testing"

	agenttypes "mindfs/server/internal/agent/types"
)

func ptrTool(callID string) *agenttypes.ToolCall {
	return &agenttypes.ToolCall{CallID: callID, Kind: agenttypes.ToolKindRead, Status: "complete"}
}

func dedupTestTodo() *agenttypes.TodoUpdate {
	return &agenttypes.TodoUpdate{}
}

// DropAuxThoughtsCoveredByBlocks 的契约。
//
// 背景：窗口回包里 aux 的 thought 与同 seq 的 reasoning 块是同一段文本的两份拷贝
// （实测一页 9.36 MB 里 2.84 MB 是这份重复）。去重只能按**逐字相同**判定 ——
// 「这个 seq 有 reasoning 块就全丢」会在「块只落了一半」时把剩下内容永久删掉。
func TestDropAuxThoughtsCoveredByBlocks(t *testing.T) {
	aux := map[int][]ExchangeAux{
		1: {
			{Seq: 1, Thought: "alpha"},
			{Seq: 1, Thought: "beta"},  // 块里没有
			{Seq: 1, ToolCall: ptrTool("call-1")},
		},
		2: {
			{Seq: 2, Thought: "gamma"}, // 该 seq 没有任何块
			{Seq: 2, Todo: dedupTestTodo()},
		},
	}
	blocks := map[int][]Block{
		1: {
			{Seq: 1, Type: BlockTypeReasoning, Text: "alpha"},
			{Seq: 1, Type: BlockTypeToolCall, ToolCall: ptrTool("call-1")},
		},
	}
	got := DropAuxThoughtsCoveredByBlocks(aux, blocks)

	if len(got[1]) != 2 {
		t.Fatalf("seq=1 应只剩 beta 与 toolcall 两条，实际 %d 条: %+v", len(got[1]), got[1])
	}
	if got[1][0].Thought != "beta" {
		t.Fatalf("seq=1 留下的必须是块里没有的那条，实际 %q", got[1][0].Thought)
	}
	if got[1][1].ToolCall == nil || got[1][1].ToolCall.CallID != "call-1" {
		t.Fatalf("工具卡 aux 不能被去重（懒加载与卡片元数据都靠它）")
	}
	if len(got[2]) != 2 {
		t.Fatalf("没有块的 seq 必须原样保留（旧数据只靠 aux 渲染），实际 %+v", got[2])
	}
	// 入参不得被修改。
	if len(aux[1]) != 3 {
		t.Fatalf("入参被就地修改了：%+v", aux[1])
	}
}

// 空白差异不算相同：块与 aux 的文本来源不同，TrimSpace 后比较是放宽，但不能放宽到
// 「只比前缀」或「只比长度」—— 那会把不同轮的思考误删。
func TestDropAuxThoughtsCoveredByBlocksRequiresExactText(t *testing.T) {
	aux := map[int][]ExchangeAux{1: {{Seq: 1, Thought: "alpha plus more"}}}
	blocks := map[int][]Block{1: {{Seq: 1, Type: BlockTypeReasoning, Text: "alpha"}}}
	got := DropAuxThoughtsCoveredByBlocks(aux, blocks)
	if len(got[1]) != 1 {
		t.Fatalf("前缀相同不算覆盖，必须保留，实际 %+v", got[1])
	}
}

// 只有一部分块落了盘：块侧只有 alpha，aux 有 alpha+beta。
// 这是「按 seq 一刀切」会静默删数据的那个场景 —— beta 必须留下。
func TestDropAuxThoughtsKeepsUncoveredWhenBlocksArePartial(t *testing.T) {
	aux := map[int][]ExchangeAux{1: {
		{Seq: 1, Thought: "alpha"},
		{Seq: 1, Thought: "beta"},
	}}
	blocks := map[int][]Block{1: {{Seq: 1, Type: BlockTypeReasoning, Text: "alpha"}}}
	got := DropAuxThoughtsCoveredByBlocks(aux, blocks)
	if len(got[1]) != 1 || got[1][0].Thought != "beta" {
		t.Fatalf("块只落了一半时，未覆盖的 thought 必须保留，实际 %+v", got[1])
	}
}

// 只有 remark：带 toolcall 的条目即使也带 thought 也不动 —— 它还要给卡片提供身份。
func TestDropAuxThoughtsKeepsCompositeEntries(t *testing.T) {
	aux := map[int][]ExchangeAux{1: {{Seq: 1, Thought: "alpha", ToolCall: ptrTool("call-1")}}}
	blocks := map[int][]Block{1: {
		{Seq: 1, Type: BlockTypeReasoning, Text: "alpha"},
		{Seq: 1, Type: BlockTypeToolCall, ToolCall: ptrTool("call-1")},
	}}
	got := DropAuxThoughtsCoveredByBlocks(aux, blocks)
	if len(got[1]) != 1 || got[1][0].ToolCall == nil {
		t.Fatalf("带 toolcall 的复合条目必须保留，实际 %+v", got[1])
	}
}
