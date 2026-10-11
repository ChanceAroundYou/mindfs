package session

import (
	"testing"

	agenttypes "mindfs/server/internal/agent/types"
)

// 压缩**只能**去掉「展开时才需要、折叠卡片渲染不需要」的字段。
// reasoning 不是那一类：前端把 `reasoning` 块渲染成灰显的思考文本，窗口少给它一块，
// 用户看到的就是「回复的时候好好的，回复完（重新读）就消失了」。
//
// 这不是理论问题：实测存量 aux 里有 **10826 条 `thought` 条目**，而
// CompactExchangeAux 有两条路把它们扔掉 ——
//   ① 只有 thought、没有 toolcall → 整条返回 false，条目直接消失；
//   ② toolcall 与 thought 同时存在 → `aux.Thought = ""`，思考被抹掉。
// 流式期 thought_chunk 是实时推的，所以用户「看着有」，关掉重开就没了 ——
// 正是本项目最初报的「回复完瞬间消失」。

func TestCompactExchangeAuxKeepsReasoningOnlyEntries(t *testing.T) {
	got, ok := CompactExchangeAux(ExchangeAux{Seq: 1, Line: 1, Thought: "先想想"})
	if !ok {
		t.Fatal("只有 reasoning 的 aux 被整条丢弃了")
	}
	if got.Thought != "先想想" {
		t.Fatalf("Thought = %q, want %q", got.Thought, "先想想")
	}
}

func TestCompactExchangeAuxKeepsReasoningAlongsideToolCall(t *testing.T) {
	tool := agenttypes.ToolCall{
		CallID: "t1", Title: "读文件", Status: "completed", Kind: agenttypes.ToolKindRead,
		Content: []agenttypes.ToolCallContentItem{{Type: "text", Text: "很长的内容…"}},
	}
	got, ok := CompactExchangeAux(ExchangeAux{Seq: 1, Line: 1, Thought: "先读文件", ToolCall: &tool})
	if !ok {
		t.Fatal("条目被丢弃了")
	}
	if got.Thought != "先读文件" {
		t.Fatalf("Thought = %q, want %q", got.Thought, "先读文件")
	}
	if got.ToolCall == nil {
		t.Fatal("ToolCall 被整条丢了")
	}
}

// 窗口路径走的是更激进的 Light 变体。它连 edit/read/execute 的 content 都清空，
// 但 reasoning 同样一个字都不能少 —— 那不是「展开才需要」的字段。
func TestCompactExchangeAuxLightKeepsReasoning(t *testing.T) {
	tool := agenttypes.ToolCall{
		CallID: "e1", Title: "改文件", Status: "completed", Kind: agenttypes.ToolKindEdit,
		Content: []agenttypes.ToolCallContentItem{{Type: "text", OldText: strPtr("a"), NewText: "b"}},
	}
	got, ok := CompactExchangeAuxLight(ExchangeAux{Seq: 1, Line: 1, Thought: "改这里", ToolCall: &tool})
	if !ok {
		t.Fatal("条目被丢弃了")
	}
	if got.Thought != "改这里" {
		t.Fatalf("Thought = %q, want %q", got.Thought, "改这里")
	}
	if len(got.ToolCall.Content) != 0 {
		t.Fatalf("Light 压缩本该清掉 edit 的 content: %+v", got.ToolCall.Content)
	}
}

// 反过来：压缩该做的事仍然要做。没有这条，上面的修复就可能被顺手"修"成不压缩。
func TestCompactExchangeAuxStillStripsToolCallContent(t *testing.T) {
	tool := agenttypes.ToolCall{
		CallID: "r1", Title: "读文件", Status: "completed", Kind: agenttypes.ToolKindRead,
		Content: []agenttypes.ToolCallContentItem{{Type: "text", Text: "很长的内容…"}},
	}
	got, ok := CompactExchangeAux(ExchangeAux{Seq: 1, Line: 1, ToolCall: &tool})
	if !ok {
		t.Fatal("条目被丢弃了")
	}
	if len(got.ToolCall.Content) != 0 {
		t.Fatalf("压缩没干活: %+v", got.ToolCall.Content)
	}
}
