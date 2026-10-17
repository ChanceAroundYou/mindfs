package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
	rootfs "mindfs/server/internal/fs"
	"mindfs/server/internal/session"
)

// seedPendingAskUser 造一张「已登记、在等回答」的提问卡，返回 manager 与 callID。
func seedPendingAskUser(t *testing.T) (*session.Manager, string, string) {
	t.Helper()
	manager := session.NewManager(rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir()))
	created, err := manager.Create(context.Background(), session.CreateInput{
		Type: session.TypeChat,
		Name: "Chat",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	callID := "call-answer"
	if err := manager.UpsertPendingExchangeAux(context.Background(), created.Key, session.ExchangeAux{
		Seq:  2,
		Line: 0,
		ToolCall: &agenttypes.ToolCall{
			CallID: callID,
			Title:  "ask user",
			Status: "running",
			Kind:   agenttypes.ToolKindAskUser,
			Meta: map[string]any{
				"toolUseId": callID,
				"questions": []agenttypes.AskUserQuestionItem{{Question: "Pick one"}},
			},
		},
	}); err != nil {
		t.Fatalf("upsert pending ask user: %v", err)
	}
	return manager, created.Key, callID
}

func answerFor(callID string) agenttypes.AskUserAnswer {
	return agenttypes.AskUserAnswer{
		ToolUseID: callID,
		Answers:   map[string]string{"Pick one": "crimson"},
	}
}

// 投递失败时「已回答」标记必须撤回：留着它，卡片显示已回答而 agent 仍在等这次权限应答，
// 该轮永久卡死且用户没有第二次机会。
func TestDeliverAskUserAnswerRollsBackMarkWhenDeliveryFails(t *testing.T) {
	ctx := context.Background()
	manager, sessionKey, callID := seedPendingAskUser(t)
	answer := answerFor(callID)

	answeredAt := time.Now()
	if err := manager.MarkPendingAskUserAnswered(ctx, sessionKey, callID, answer.Answers, answeredAt); err != nil {
		t.Fatalf("mark answered: %v", err)
	}

	sess := &fakeUsecaseAgentSession{answerErr: errors.New("question is not pending: " + callID)}
	if err := deliverAskUserAnswer(ctx, manager, sess, sessionKey, answer, answeredAt); err == nil {
		t.Fatal("投递失败必须把错误传出去（WS 要回 session.error）")
	}

	toolCall, err := manager.GetFullToolCall(ctx, sessionKey, callID)
	if err != nil {
		t.Fatalf("get full toolcall: %v", err)
	}
	if toolCall.Status != "running" {
		t.Fatalf("status = %q, want running（卡片要回到可回答）", toolCall.Status)
	}
	if _, ok := toolCall.Meta["answers"]; ok {
		t.Fatalf("投递失败却留下了 answers：%#v", toolCall.Meta)
	}
}

// 投递成功时标记要保留：刷新/切换会话后卡片仍显示「已回答」，不会被要求再答一次。
func TestDeliverAskUserAnswerKeepsMarkWhenDeliverySucceeds(t *testing.T) {
	ctx := context.Background()
	manager, sessionKey, callID := seedPendingAskUser(t)
	answer := answerFor(callID)

	answeredAt := time.Now()
	if err := manager.MarkPendingAskUserAnswered(ctx, sessionKey, callID, answer.Answers, answeredAt); err != nil {
		t.Fatalf("mark answered: %v", err)
	}
	if err := deliverAskUserAnswer(ctx, manager, &fakeUsecaseAgentSession{}, sessionKey, answer, answeredAt); err != nil {
		t.Fatalf("投递应成功：%v", err)
	}

	toolCall, err := manager.GetFullToolCall(ctx, sessionKey, callID)
	if err != nil {
		t.Fatalf("get full toolcall: %v", err)
	}
	answers, ok := toolCall.Meta["answers"].(map[string]string)
	if !ok || answers["Pick one"] != "crimson" {
		t.Fatalf("answers = %#v, want Pick one=crimson", toolCall.Meta["answers"])
	}
}
