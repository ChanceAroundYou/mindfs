package api

import (
	"testing"

	"mindfs/server/internal/agent/types"
)

// 线上 task #99：同一条回复被工具调用切成 6 个消息块，逐块直接相接会黏成
// 「changes.Now update ...」。块之间必须保留段落分隔。
func TestAppendTurnTextBlockSeparatesInterruptedBlocks(t *testing.T) {
	var text, last string
	steps := []struct {
		updateType types.EventType
		chunk      string
	}{
		{types.EventTypeMessageChunk, "Now let me write the gitview changes."},
		{types.EventTypeToolCall, ""},
		{types.EventTypeMessageChunk, "\nNow update `mergeVanishedSource`."},
		{types.EventTypeToolUpdate, ""},
		{types.EventTypeMessageChunk, "Now the auto-merge file."},
	}
	for _, step := range steps {
		text, last = appendTurnTextBlock(text, last, step.updateType, step.chunk)
	}
	want := "Now let me write the gitview changes.\n\nNow update `mergeVanishedSource`.\n\nNow the auto-merge file."
	if text != want {
		t.Fatalf("text = %q, want %q", text, want)
	}
}

// 连续的流式块必须逐字相接 —— 正文里的换行不能被补成空行。
func TestAppendTurnTextBlockKeepsStreamingChunksVerbatim(t *testing.T) {
	var text, last string
	for _, chunk := range []string{"第一段", "继续", "\n换行"} {
		text, last = appendTurnTextBlock(text, last, types.EventTypeMessageChunk, chunk)
	}
	if want := "第一段继续\n换行"; text != want {
		t.Fatalf("text = %q, want %q", text, want)
	}
}

// 思考/待办这类更新不是正文，但会把前后的正文块隔开。
func TestAppendTurnTextBlockIgnoresNonTextUpdates(t *testing.T) {
	text, last := appendTurnTextBlock("", "", types.EventTypeMessageChunk, "先看一眼")
	text, last = appendTurnTextBlock(text, last, types.EventTypeThoughtChunk, "thinking...")
	if text != "先看一眼" {
		t.Fatalf("non-text update leaked into body: %q", text)
	}
	text, _ = appendTurnTextBlock(text, last, types.EventTypeMessageChunk, "再动手")
	if want := "先看一眼\n\n再动手"; text != want {
		t.Fatalf("text = %q, want %q", text, want)
	}
}
