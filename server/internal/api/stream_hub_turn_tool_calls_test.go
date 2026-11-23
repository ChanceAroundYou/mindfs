package api

import (
	"testing"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
)

// turn_settled 载荷里的 tool_calls 是 Router 判断「这一轮有没有真干活」的唯一依据：
// Summary / has_output 区分不了「读文件后给出四条待决策项」和「回了一句好的」。
// 它必须按轮计数：上一轮的工具调用不能算到下一轮头上。
func TestAppendReplyEventCountsToolCallsPerTurn(t *testing.T) {
	hub := NewStreamHub()
	if _, turnID := hub.SetPendingUserAt("root", "sess-1", "title", "claude", "", "", "", "", "", false, "第一轮", time.Now(), 1); turnID == "" {
		t.Fatal("SetPendingUserAt 没有返回 turnID")
	}

	hub.AppendReplyEvent("sess-1", StreamEvent{
		Type: string(agenttypes.EventTypeToolCall),
		Data: agenttypes.ToolCall{CallID: "call-1"},
	})
	hub.AppendReplyEvent("sess-1", StreamEvent{
		Type: string(agenttypes.EventTypeMessageChunk),
		Data: agenttypes.MessageChunk{Content: "读完了。"},
	})
	hub.AppendReplyEvent("sess-1", StreamEvent{
		Type: string(agenttypes.EventTypeToolCall),
		Data: agenttypes.ToolCall{CallID: "call-2"},
	})

	snapshot, ok := hub.PendingSessionSnapshotFound("sess-1")
	if !ok {
		t.Fatal("回合进行中，快照必须存在")
	}
	if snapshot.ToolCalls != 2 {
		t.Fatalf("tool_calls = %d, want 2", snapshot.ToolCalls)
	}

	// 新的一轮从 0 开始：否则第一轮干过活，之后每一轮的纯闲聊都会被算成执行。
	hub.SetPendingUserAt("root", "sess-1", "title", "claude", "", "", "", "", "", false, "第二轮", time.Now(), 2)
	snapshot, ok = hub.PendingSessionSnapshotFound("sess-1")
	if !ok {
		t.Fatal("第二轮开始后快照必须存在")
	}
	if snapshot.ToolCalls != 0 {
		t.Fatalf("新一轮 tool_calls = %d, want 0", snapshot.ToolCalls)
	}
}
