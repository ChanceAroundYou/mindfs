package claude

import (
	"encoding/json"
	"testing"

	types "mindfs/server/internal/agent/types"

	claudeagent "github.com/roasbeef/claude-agent-sdk-go"
)

// WP-00 Task 4：SDK 给了归属就必须原样带到事件上，没给就保持为空。
//
// 这两个方向都不能错：
//   - 有 parentToolUseId / subagentType 却被丢掉 → 子代理正文在 router 里没有归属，
//     只能落进父会话（用户报的串流形态）；
//   - 没给却补一个 → 父正文被凭空认领成子代理，父回合丢字。
func TestClaudePartialDeltaPreservesSubagentAttribution(t *testing.T) {
	var events []types.Event
	s := &session{onUpdate: func(event types.Event) { events = append(events, event) }}
	raw := json.RawMessage(`{"type":"content_block_delta","delta":{"type":"text_delta","text":"child text"}}`)

	s.handlePartialAssistantMessage(raw, subagentMeta{ParentToolUseID: "tool-child-1"})

	if len(events) != 1 || events[0].Type != types.EventTypeMessageChunk {
		t.Fatalf("events = %#v, want one message chunk", events)
	}
	chunk, ok := events[0].Data.(types.MessageChunk)
	if !ok {
		t.Fatalf("data = %T, want MessageChunk", events[0].Data)
	}
	if chunk.Content != "child text" || chunk.ParentToolUseID != "tool-child-1" {
		t.Fatalf("chunk = %#v, want content with parent tool use id", chunk)
	}
}

func TestClaudeAssistantMessagePreservesSubagentAttribution(t *testing.T) {
	var events []types.Event
	s := &session{onUpdate: func(event types.Event) { events = append(events, event) }}
	var msg claudeagent.AssistantMessage
	if err := json.Unmarshal([]byte(`{
		"type":"assistant",
		"uuid":"uuid-child-1",
		"parent_tool_use_id":"tool-child-1",
		"subagent_type":"Explore",
		"task_description":"Inspect files",
		"message":{"role":"assistant","content":[{"type":"text","text":"child text"}]}
	}`), &msg); err != nil {
		t.Fatalf("unmarshal assistant message: %v", err)
	}

	s.handleAssistantMessage(msg, false)

	if len(events) != 1 || events[0].Type != types.EventTypeMessageChunk {
		t.Fatalf("events = %#v, want one message chunk", events)
	}
	chunk, ok := events[0].Data.(types.MessageChunk)
	if !ok {
		t.Fatalf("data = %T, want MessageChunk", events[0].Data)
	}
	if chunk.ParentToolUseID != "tool-child-1" || chunk.SubagentType != "Explore" ||
		chunk.TaskDescription != "Inspect files" || chunk.MessageID != "uuid-child-1" {
		t.Fatalf("chunk = %#v, want SDK attribution preserved", chunk)
	}
}

func TestClaudeMetadataFreeDeltaInventsNoAttribution(t *testing.T) {
	var events []types.Event
	s := &session{onUpdate: func(event types.Event) { events = append(events, event) }}
	raw := json.RawMessage(`{"type":"content_block_delta","delta":{"type":"text_delta","text":"parent text"}}`)

	s.handlePartialAssistantMessage(raw, subagentMeta{})
	// 无归属增量走合并缓冲，由回合边界（或达到阈值）统一冲刷。
	s.flushAllDeltas()

	if len(events) != 1 {
		t.Fatalf("events = %#v, want one message chunk", events)
	}
	chunk, ok := events[0].Data.(types.MessageChunk)
	if !ok {
		t.Fatalf("data = %T, want MessageChunk", events[0].Data)
	}
	if chunk.ParentToolUseID != "" || chunk.TaskID != "" || chunk.SubagentType != "" {
		t.Fatalf("chunk = %#v, want no fabricated attribution", chunk)
	}
}

func TestClaudeToolProgressPreservesTaskAndParentAttribution(t *testing.T) {
	parentID := "tool-child-1"
	taskID := "task-child-1"
	meta := subagentMetaFromToolProgress(claudeagent.ToolProgressMessage{
		ParentToolUseID: &parentID,
		TaskID:          &taskID,
	})
	if meta.ParentToolUseID != parentID || meta.TaskID != taskID {
		t.Fatalf("meta = %#v, want progress attribution preserved", meta)
	}
}
