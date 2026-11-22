package api

import (
	"testing"

	agenttypes "mindfs/server/internal/agent/types"
)

// W3-1：输出水位的生产端必须挂在 BroadcastSessionUpdate 上。
//
// 它是所有 harness 帧的必经之路。少了这一处，「这一轮真的在产出」就只剩会话探针
// 那一条证据 —— 而挂死的回合探针照样报「在跑」，停滞永远判不出来。
func TestBroadcastSessionUpdateAdvancesTurnWatermark(t *testing.T) {
	app, rootID, _ := newTaskQuestionTestApp(t, "task-watermark")
	svc, err := app.GetKanbanService()
	if err != nil {
		t.Fatalf("GetKanbanService: %v", err)
	}
	const sessionKey = "sess-watermark"
	if _, ok := svc.SessionTurnWatermark(rootID, sessionKey); ok {
		t.Fatal("watermark known before any frame")
	}
	app.BroadcastSessionUpdate(rootID, sessionKey, agenttypes.Event{
		Type: agenttypes.EventTypeMessageChunk,
		Data: agenttypes.MessageChunk{Content: "hi"},
	})
	if _, ok := svc.SessionTurnWatermark(rootID, sessionKey); !ok {
		t.Fatal("a broadcast message frame did not advance the turn watermark")
	}
	// 回合结束清零：下一轮重新从零判断。
	app.clearSessionTurnWatermark(rootID, sessionKey)
	if _, ok := svc.SessionTurnWatermark(rootID, sessionKey); ok {
		t.Fatal("watermark survived turn end")
	}
}
