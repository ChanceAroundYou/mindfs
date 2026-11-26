package api

import (
	"testing"

	agenttypes "mindfs/server/internal/agent/types"
)

// Start 是 usecase 内部用来锚定思考位置的时序信号，没有内容。放上 WS 会先画出
// 一个空思考块，所以 updateToEvent 必须丢弃它；带内容的思考片段照常转发。
func TestUpdateToEventDropsThoughtStartMarker(t *testing.T) {
	marker := updateToEvent(agenttypes.Event{
		Type: agenttypes.EventTypeThoughtChunk,
		Data: agenttypes.ThoughtChunk{ID: "rs_1", Start: true},
	})
	if marker != nil {
		t.Fatalf("start marker leaked to WS: %#v", marker)
	}

	content := updateToEvent(agenttypes.Event{
		Type: agenttypes.EventTypeThoughtChunk,
		Data: agenttypes.ThoughtChunk{ID: "rs_1", Content: "思考"},
	})
	if content == nil || content.Type != "thought_chunk" {
		t.Fatalf("content event = %#v, want thought_chunk", content)
	}
}
