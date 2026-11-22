package api

import (
	"context"
	"strings"
	"testing"

	"mindfs/server/internal/kanban"
)

// W2：会话回合结算事件的生产端必须挂在 EndSessionTurn 上。
//
// 这是整条链的唯一接缝：EndSessionTurn 是回合的终结器（成功 / 出错 / 取消都经过
// 它）。少了这一处，Router 永远不知道用户在任务详情里推进过会话 —— 线上 #68 的
// 进度就是这样整段丢失的。
func TestEndSessionTurnEmitsTurnSettledEvent(t *testing.T) {
	ctx := context.Background()
	app, rootID, sessionKey := newTaskQuestionTestApp(t, "task-turn-settled")
	if err := app.Kanban.SetTaskMainSessionKey(ctx, rootID, "task-turn-settled", sessionKey); err != nil {
		t.Fatalf("SetTaskMainSessionKey: %v", err)
	}
	turnID := app.SetSessionPendingReply(rootID, sessionKey, "提问会话", "turn-settled-1")
	if turnID == "" {
		t.Fatal("SetSessionPendingReply returned an empty turn id")
	}
	app.EndSessionTurn(rootID, sessionKey, "", turnID)

	svc, err := app.GetKanbanService()
	if err != nil {
		t.Fatalf("GetKanbanService: %v", err)
	}
	events, err := svc.ListRouterEventsForTask(ctx, rootID, "task-turn-settled")
	if err != nil {
		t.Fatalf("ListRouterEventsForTask: %v", err)
	}
	var settled []kanban.RouterEvent
	for _, ev := range events {
		if ev.Kind == kanban.RouterEventTurnSettled {
			settled = append(settled, ev)
		}
	}
	if len(settled) != 1 {
		t.Fatalf("turn_settled events = %d, want 1", len(settled))
	}
	if !strings.Contains(settled[0].PayloadJSON, "session_key") {
		t.Fatalf("event payload lost the session: %s", settled[0].PayloadJSON)
	}
}

// 非任务会话不该产生任务事件：普通聊天会话每轮都落一条也只是噪音。
func TestEndSessionTurnIgnoresNonTaskSession(t *testing.T) {
	ctx := context.Background()
	app, rootID, _ := newTaskQuestionTestApp(t, "task-turn-other")
	other := app.SetSessionPendingReply(rootID, "sess-plain-chat", "普通会话", "turn-plain-1")
	app.EndSessionTurn(rootID, "sess-plain-chat", "", other)

	svc, err := app.GetKanbanService()
	if err != nil {
		t.Fatalf("GetKanbanService: %v", err)
	}
	events, err := svc.ListRouterEventsForTask(ctx, rootID, "task-turn-other")
	if err != nil {
		t.Fatalf("ListRouterEventsForTask: %v", err)
	}
	for _, ev := range events {
		if ev.Kind == kanban.RouterEventTurnSettled {
			t.Fatalf("non-task session produced %s", ev.PayloadJSON)
		}
	}
}
