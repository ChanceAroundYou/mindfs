package kanban

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// askReplyHarness 是「任务停在一条 Router 提问上」的最小现场：一条等待回答的
// 提问活动（waiting_user），加上能让回答重新走一轮路由的 service。
type askReplyHarness struct {
	f        *routeFixture
	task     Task
	question *TaskActivity
}

func newAskHarness(t *testing.T) *askReplyHarness {
	t.Helper()
	f := newRouteFixture(t, RouterModeRoute)
	f.agent.calls = []scriptedRouterCall{{
		tool: "router_ask_user",
		args: map[string]any{"question": "先做哪个？", "options": []any{"A", "B"}},
	}}
	ctx := context.Background()
	_, placeholder, err := f.activity.CreateUserActivity(ctx, "task-1", "帮我排个优先级", CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	f.waitDecision(t, RouterDecisionApplied)
	question, err := NewActivityStore(f.store.db).Get(ctx, placeholder.ID)
	if err != nil {
		t.Fatalf("load question: %v", err)
	}
	if question.Status != ActivityStatusWaitingUser {
		t.Fatalf("question status = %q, want waiting_user", question.Status)
	}
	task, err := f.store.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	return &askReplyHarness{f: f, task: task, question: question}
}

// answerQuestion 模拟「网页上已经答过这张卡」：提问卡收成 completed 并带答案。
func (h *askReplyHarness) answerQuestion(t *testing.T) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"answers": map[string]any{h.question.ID: "A"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := NewActivityStore(h.f.store.db).ResolvePrompt(
		context.Background(), h.question.ID, ActivityStatusCompleted, string(payload), nil,
	); err != nil {
		t.Fatalf("ResolvePrompt: %v", err)
	}
}

// A card answer must become a real user message bound to the question, and it
// must re-enter the same dispatch path a typed message takes — otherwise the
// answer is recorded but nothing continues.
func TestCreateUserReplyBindsQuestionAndDispatches(t *testing.T) {
	h := newAskHarness(t)
	userAct, agentAct, err := h.f.activity.CreateUserReply(context.Background(), "task-1", "先做 A", h.question.ID)
	if err != nil {
		t.Fatalf("CreateUserReply: %v", err)
	}
	if userAct.ReplyToID != h.question.ID || userAct.Body != "先做 A" {
		t.Fatalf("user = %+v", userAct)
	}
	if agentAct == nil {
		t.Fatal("dispatch did not create a reply")
	}
	h.f.waitDecision(t, RouterDecisionApplied)
}

// The same click delivered twice is one answer: the second call returns the
// activity the first one created instead of adding a second message. Once the
// question is settled (answered in the web UI first), the card is stale and
// must change nothing.
func TestCreateUserReplyIsIdempotentAndRejectsAnsweredQuestion(t *testing.T) {
	ctx := context.Background()
	h := newAskHarness(t)
	first, _, err := h.f.activity.CreateUserReply(ctx, "task-1", "A", h.question.ID)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, _, err := h.f.activity.CreateUserReply(ctx, "task-1", "A", h.question.ID)
	if err != nil || second.ID != first.ID {
		t.Fatalf("second = %+v err = %v", second, err)
	}
	h.answerQuestion(t)
	if _, _, err := h.f.activity.CreateUserReply(ctx, "task-1", "B", h.question.ID); !errors.Is(err, ErrQuestionAlreadyAnswered) {
		t.Fatalf("err = %v, want ErrQuestionAlreadyAnswered", err)
	}
}

// A card can outlive the wait it was made for: the question may belong to a
// different task, or not exist at all. Neither may write a message into a task.
func TestCreateUserReplyRejectsUnknownOrForeignQuestion(t *testing.T) {
	ctx := context.Background()
	h := newAskHarness(t)
	if _, _, err := h.f.activity.CreateUserReply(ctx, "task-1", "A", "act_missing"); err == nil {
		t.Fatal("missing question was accepted")
	}
	if _, _, err := h.f.activity.CreateUserReply(ctx, "task-other", "A", h.question.ID); err == nil {
		t.Fatal("question from another task was accepted")
	}
}
