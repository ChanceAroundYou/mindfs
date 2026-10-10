package session

import (
	"testing"

	agenttypes "mindfs/server/internal/agent/types"
)

// BuildTurnBlocks 把「一轮的正文 + aux」编译成块序列，顺序必须与前端
// buildAssistantTimeline 的既有渲染顺序逐条一致 —— S2b 起块序就是渲染序，
// 若这里与渲染器不一致，切换读路径的瞬间正文顺序会变（用户可见的回归）。

func TestBuildTurnBlocksTextOnly(t *testing.T) {
	blocks := BuildTurnBlocks(3, 3, "turn-1", "hello\nworld", nil)
	if len(blocks) != 1 {
		t.Fatalf("blocks = %#v, want one text block", blocks)
	}
	if blocks[0].Type != BlockTypeText || blocks[0].Text != "hello\nworld" || blocks[0].Pos != 0 {
		t.Fatalf("block = %#v", blocks[0])
	}
	if blocks[0].Seq != 3 || blocks[0].Step != 3 || blocks[0].TurnID != "turn-1" {
		t.Fatalf("block identity = %#v", blocks[0])
	}
}

// 本 bug 的核心场景：**只有 reasoning、没有任何 text 块**。
// 旧路径把这一轮落库成 content:""，于是渲染成空；块流必须给出 reasoning 块。
func TestBuildTurnBlocksReasoningOnlyTurnStillProducesBlocks(t *testing.T) {
	blocks := BuildTurnBlocks(5, 5, "turn-2", "", []ExchangeAux{
		{Seq: 5, Line: 0, Thought: "let me think"},
	})
	if len(blocks) != 1 {
		t.Fatalf("blocks = %#v, want one reasoning block", blocks)
	}
	if blocks[0].Type != BlockTypeReasoning || blocks[0].Text != "let me think" {
		t.Fatalf("block = %#v, want reasoning", blocks[0])
	}
}

func TestBuildTurnBlocksEmptyTurnProducesNoBlocks(t *testing.T) {
	if got := BuildTurnBlocks(5, 5, "turn-3", "", nil); len(got) != 0 {
		t.Fatalf("blocks = %#v, want empty", got)
	}
	// 只有空白正文也不算有正文。
	if got := BuildTurnBlocks(5, 5, "turn-4", "  \n ", nil); len(got) != 0 {
		t.Fatalf("blocks = %#v, want empty", got)
	}
}

// 交错顺序：渲染器按 (line, 到达序) 稳定排序后把 aux 插进正文段之间。
func TestBuildTurnBlocksInterleavesAuxByLine(t *testing.T) {
	blocks := BuildTurnBlocks(7, 7, "turn-5", "l0\nl1\nl2\nl3", []ExchangeAux{
		// 故意乱序传入，且带越界 line。
		{Seq: 7, Line: 99, ToolCall: &agenttypes.ToolCall{CallID: "c-late", Title: "t3"}},
		{Seq: 7, Line: 0, Thought: "think-first"},
		{Seq: 7, Line: 2, ToolCall: &agenttypes.ToolCall{CallID: "c-mid", Title: "t2"}},
		{Seq: 7, Line: 2, Plan: &agenttypes.PlanUpdate{Content: "plan"}},
	})
	want := []BlockType{BlockTypeReasoning, BlockTypeText, BlockTypeToolCall, BlockTypePlan, BlockTypeText, BlockTypeToolCall}
	if len(blocks) != len(want) {
		t.Fatalf("blocks = %#v, want %d blocks", blocks, len(want))
	}
	for i, wantType := range want {
		if blocks[i].Type != wantType {
			t.Fatalf("blocks[%d].Type = %s, want %s (%#v)", i, blocks[i].Type, wantType, blocks)
		}
		if blocks[i].Pos != i {
			t.Fatalf("blocks[%d].Pos = %d, want %d", i, blocks[i].Pos, i)
		}
	}
	// 空正文段必须被跳过（渲染器同样跳过），否则 pos 会跳号。
	if blocks[1].Text != "l0\nl1" {
		t.Fatalf("first text segment = %q, want %q", blocks[1].Text, "l0\nl1")
	}
	if blocks[4].Text != "l2\nl3" {
		t.Fatalf("second text segment = %q, want %q", blocks[4].Text, "l2\nl3")
	}
}

// 同一 line 上多个 aux：保持到达序（渲染器的 auxIndex 规则）。
// 正文段仍按 line 边界切开：lines[0:1] 在前，lines[1:] 在后。
func TestBuildTurnBlocksSameLineKeepsArrivalOrder(t *testing.T) {
	blocks := BuildTurnBlocks(9, 9, "turn-6", "a\nb", []ExchangeAux{
		{Seq: 9, Line: 1, Thought: "first"},
		{Seq: 9, Line: 1, Thought: "second"},
	})
	want := []BlockType{BlockTypeText, BlockTypeReasoning, BlockTypeReasoning, BlockTypeText}
	if len(blocks) != len(want) {
		t.Fatalf("blocks = %#v, want %d", blocks, len(want))
	}
	for i, wantType := range want {
		if blocks[i].Type != wantType {
			t.Fatalf("blocks[%d].Type = %s, want %s (%#v)", i, blocks[i].Type, wantType, blocks)
		}
	}
	if blocks[0].Text != "a" || blocks[1].Text != "first" || blocks[2].Text != "second" || blocks[3].Text != "b" {
		t.Fatalf("same-line order not preserved: %#v", blocks)
	}
}

func TestBuildTurnBlocksToolCallAndTodoKinds(t *testing.T) {
	blocks := BuildTurnBlocks(11, 11, "turn-7", "body", []ExchangeAux{
		{Seq: 11, Line: 0, Todo: &agenttypes.TodoUpdate{Items: []agenttypes.TodoItem{{Content: "x"}}}},
		{Seq: 11, Line: 1, Compact: &agenttypes.CompactNotice{ID: "cmp"}},
	})
	if len(blocks) != 3 {
		t.Fatalf("blocks = %#v, want 3", blocks)
	}
	if blocks[0].Type != BlockTypeTodo || blocks[0].Todo == nil {
		t.Fatalf("blocks[0] = %#v, want todo", blocks[0])
	}
	if blocks[2].Type != BlockTypeCompact || blocks[2].Compact == nil {
		t.Fatalf("blocks[2] = %#v, want compact", blocks[2])
	}
	if blocks[1].Type != BlockTypeText || blocks[1].Text != "body" {
		t.Fatalf("blocks[1] = %#v, want text body", blocks[1])
	}
}
