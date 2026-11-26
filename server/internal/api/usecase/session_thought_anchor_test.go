package usecase

import (
	"strings"
	"testing"

	"mindfs/server/internal/session"
)

// 复刻 2026-11-26 的真实事故（HBSN/latex seq 667）：思考在正文之前开始，
// summary 却在正文 93 行流完之后才到。锚点必须是 0，不是 93。
func TestThoughtAnchorKeepsStartLineWhenSummaryArrivesLate(t *testing.T) {
	var a thoughtAnchor
	a.note("rs_1", 0)

	content := strings.Repeat("正文行\n", 93)
	if lines := len(strings.Split(content, "\n")) - 1; lines != 93 {
		t.Fatalf("fixture lines = %d, want 93", lines)
	}
	a.add("rs_1", "思考", 93)

	text, line, id, ok := a.take()
	if !ok || text != "思考" || id == "" {
		t.Fatalf("take = %q / %q / %v", text, id, ok)
	}
	if line != 0 {
		t.Fatalf("line = %d, want 0（思考真实起点，不是 flush 时的正文行数）", line)
	}

	// 端到端：line=0 的 aux 必须排在正文之前，复刻 blocks.jsonl 的事故形态
	// （改前是 text pos=0 / reasoning pos=1）。
	blocks := session.BuildTurnBlocks(667, 1, "turn-1", content,
		[]session.ExchangeAux{{Line: line, Thought: text, ThoughtID: id}})
	if len(blocks) != 2 || blocks[0].Type != session.BlockTypeReasoning || blocks[1].Type != session.BlockTypeText {
		t.Fatalf("blocks = %#v, want reasoning before text", blocks)
	}
}

// 对照组：没有任何「开始」信号、首个片段确实在正文之后到达时，思考仍应排在正文后面。
// 这条钉住「不许把 reasoning 一律排到开头」。
func TestThoughtAnchorKeepsLateThoughtAtEnd(t *testing.T) {
	var a thoughtAnchor
	a.add("rs_late", "补充思考", 93)

	_, line, _, ok := a.take()
	if !ok || line != 93 {
		t.Fatalf("line = %d / ok=%v, want 93（思考确实发生在正文之后）", line, ok)
	}

	content := strings.Repeat("正文行\n", 93)
	blocks := session.BuildTurnBlocks(667, 1, "turn-1", content,
		[]session.ExchangeAux{{Line: line, Thought: "补充思考", ThoughtID: "thought-late"}})
	if len(blocks) != 2 || blocks[0].Type != session.BlockTypeText || blocks[1].Type != session.BlockTypeReasoning {
		t.Fatalf("blocks = %#v, want text before reasoning", blocks)
	}
}

// 空缓冲上 flush（例如正文首个 MessageChunk 触发 flush）不能抹掉已到达的起点。
func TestThoughtAnchorTakeKeepsAnchorWhenNoContentYet(t *testing.T) {
	var a thoughtAnchor
	a.note("rs_1", 0)
	if _, _, _, ok := a.take(); ok {
		t.Fatal("空内容不应被 take")
	}
	a.add("rs_1", "迟到的 summary", 93)
	_, line, _, ok := a.take()
	if !ok || line != 0 {
		t.Fatalf("line = %d / ok=%v, want 0", line, ok)
	}
}

// 同一 item 的重复 Start 不改锚；不同 item 在空缓冲上重新锚定。
func TestThoughtAnchorRebindsOnlyForNewItemOnEmptyBuffer(t *testing.T) {
	var a thoughtAnchor
	a.note("rs_1", 0)
	a.note("rs_1", 40)
	a.note("rs_2", 50)
	a.add("rs_2", "第二段", 50)
	_, line, _, ok := a.take()
	if !ok || line != 50 {
		t.Fatalf("line = %d / ok=%v, want 50（空缓冲上允许新 item 重锚）", line, ok)
	}
}
