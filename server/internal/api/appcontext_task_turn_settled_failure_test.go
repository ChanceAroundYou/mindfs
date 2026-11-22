package api

import (
	"context"
	"strings"
	"testing"

	"mindfs/server/internal/kanban"
)

// H2：失败回合在结算事件里没有任何失败标记。
//
// `turn_settled` 的载荷只有 has_output，而失败回合也有输出（错误说明本身就是
// 输出），所以 Router 读到的每一条结算都是「正常结束」。线上 #100 的两轮回复以
// `Prompt is too long` 开头仍然记 completed，就是在这一层丢的：真正的失败信号
// 只写在会话行 aux（前端徽标用），Router 永远看不到。
//
// 唯一的失败写入口是 AppContext.BroadcastSessionError → 会话行 Aux.SessionError，
// 而回合开始时 ClearTaskAuxFlagsForSession 会把它清空 —— 所以结算时读到非空，
// 就一定是**本轮内**真的报过错。
func TestEndSessionTurnMarksFailedTurnSettled(t *testing.T) {
	ctx := context.Background()
	app, rootID, sessionKey := newTaskQuestionTestApp(t, "task-turn-failed")
	if err := app.Kanban.SetTaskMainSessionKey(ctx, rootID, "task-turn-failed", sessionKey); err != nil {
		t.Fatalf("SetTaskMainSessionKey: %v", err)
	}
	turnID := app.SetSessionPendingReply(rootID, sessionKey, "提问会话", "turn-failed-1")
	if turnID == "" {
		t.Fatal("SetSessionPendingReply returned an empty turn id")
	}
	app.UpdateTaskSessionErrorForSession(rootID, sessionKey, "response stream ended unexpectedly")
	app.EndSessionTurn(rootID, sessionKey, "", turnID)

	settled := findTurnSettled(t, app, rootID, "task-turn-failed")
	if !strings.Contains(settled.PayloadJSON, `"failed":true`) {
		t.Fatalf("失败回合的结算事件没有失败标记: %s", settled.PayloadJSON)
	}
	if !strings.Contains(settled.PayloadJSON, "response stream ended unexpectedly") {
		t.Fatalf("结算事件丢了失败原因: %s", settled.PayloadJSON)
	}
}

// 反向守卫：正常回合绝不能被标成失败 —— 否则 Router 会把每一条结算都当故障。
func TestEndSessionTurnKeepsHealthyTurnUnfailed(t *testing.T) {
	ctx := context.Background()
	app, rootID, sessionKey := newTaskQuestionTestApp(t, "task-turn-healthy")
	if err := app.Kanban.SetTaskMainSessionKey(ctx, rootID, "task-turn-healthy", sessionKey); err != nil {
		t.Fatalf("SetTaskMainSessionKey: %v", err)
	}
	turnID := app.SetSessionPendingReply(rootID, sessionKey, "提问会话", "turn-healthy-1")
	app.EndSessionTurn(rootID, sessionKey, "", turnID)

	settled := findTurnSettled(t, app, rootID, "task-turn-healthy")
	if strings.Contains(settled.PayloadJSON, `"failed":true`) {
		t.Fatalf("正常回合被标成失败: %s", settled.PayloadJSON)
	}
}

// 上一轮的错误不能在下一轮结算里复活：新回合的 OnStart 会清空会话行 aux。
func TestEndSessionTurnClearsPreviousTurnFailure(t *testing.T) {
	ctx := context.Background()
	app, rootID, sessionKey := newTaskQuestionTestApp(t, "task-turn-recover")
	if err := app.Kanban.SetTaskMainSessionKey(ctx, rootID, "task-turn-recover", sessionKey); err != nil {
		t.Fatalf("SetTaskMainSessionKey: %v", err)
	}
	app.UpdateTaskSessionErrorForSession(rootID, sessionKey, "old failure")
	app.ClearTaskAuxFlagsForSession(rootID, sessionKey)
	turnID := app.SetSessionPendingReply(rootID, sessionKey, "提问会话", "turn-recover-1")
	app.EndSessionTurn(rootID, sessionKey, "", turnID)

	settled := findTurnSettled(t, app, rootID, "task-turn-recover")
	if strings.Contains(settled.PayloadJSON, `"failed":true`) {
		t.Fatalf("上一轮的失败标记泄漏到了下一轮: %s", settled.PayloadJSON)
	}
}

func findTurnSettled(t *testing.T, app *AppContext, rootID, taskID string) kanban.RouterEvent {
	t.Helper()
	svc, err := app.GetKanbanService()
	if err != nil {
		t.Fatalf("GetKanbanService: %v", err)
	}
	events, err := svc.ListRouterEventsForTask(context.Background(), rootID, taskID)
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
	return settled[0]
}
