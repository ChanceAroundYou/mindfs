package kanban

import (
	"context"
	"testing"
)

func (r *fakeRunner) lastTaskUpdate() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.taskUpdates) == 0 {
		return ""
	}
	return r.taskUpdates[len(r.taskUpdates)-1]
}

// 看板只认 task.updated；提问卡的生命周期只广播 task.activity.*。缺了这条通知，
// Router 卡上的「等你回答」要等下一次无关事件才出现/消失（2026-11-27 线上）。
func TestRouteAskUserNotifiesTaskCard(t *testing.T) {
	f := newRouteFixture(t, RouterModeRoute)
	f.agent.calls = []scriptedRouterCall{{
		tool: "router_ask_user",
		args: map[string]any{"question": "先做哪个？", "options": []any{"A", "B"}},
	}}
	runner, ok := f.svc.Runner.(*fakeRunner)
	if !ok {
		t.Fatalf("runner type = %T", f.svc.Runner)
	}
	before := runner.updateCount()

	ctx := context.Background()
	if _, _, err := f.activity.CreateUserActivity(ctx, "task-1", "先做哪个？", CommentWorkflowChoiceEmpty, nil); err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	f.waitDecision(t, RouterDecisionApplied)

	if got := runner.updateCount(); got <= before {
		t.Fatalf("task.updated count = %d, want > %d after router ask applies", got, before)
	}
	if last := runner.lastTaskUpdate(); last != "task-1" {
		t.Fatalf("last task.updated = %q, want task-1", last)
	}
}

func TestAnswerQuestionCardNotifiesTaskCard(t *testing.T) {
	h := newAskHarness(t)
	notified := make(chan string, 4)
	h.f.activity.SetTaskCardNotifier(func(_ context.Context, rootID, taskID string) {
		notified <- rootID + "/" + taskID
	})

	ctx := context.Background()
	if _, _, err := h.f.activity.AnswerQuestionCard(ctx, "task-1", h.question.ID, map[string]string{"q_0": "A"}); err != nil {
		t.Fatalf("AnswerQuestionCard: %v", err)
	}

	select {
	case got := <-notified:
		if got != h.f.rootID+"/task-1" {
			t.Fatalf("notify = %q, want %s/task-1", got, h.f.rootID)
		}
	default:
		t.Fatal("AnswerQuestionCard did not notify the task card")
	}
}

func TestCloseOpenTaskQuestionsNotifiesTaskCard(t *testing.T) {
	f := setupActivityService(t)
	notified := make(chan string, 4)
	f.service.SetTaskCardNotifier(func(_ context.Context, rootID, taskID string) {
		notified <- rootID + "/" + taskID
	})

	ctx := context.Background()
	if _, _, err := f.service.RecordTaskQuestion(ctx, TaskQuestionInput{
		TaskID:      "task-1",
		SessionKey:  "sess-1",
		ToolUseID:   "tool-1",
		Body:        "继续执行吗？",
		PayloadJSON: `{"questions":[{"question":"继续执行吗？"}]}`,
	}); err != nil {
		t.Fatalf("RecordTaskQuestion: %v", err)
	}

	closed, err := f.service.CloseOpenTaskQuestions(ctx, "sess-1", "回合已结束，提问未回答")
	if err != nil {
		t.Fatalf("CloseOpenTaskQuestions: %v", err)
	}
	if closed != 1 {
		t.Fatalf("closed = %d, want 1", closed)
	}
	select {
	case got := <-notified:
		if got != f.root.ID+"/task-1" {
			t.Fatalf("notify = %q, want %s/task-1", got, f.root.ID)
		}
	default:
		t.Fatal("CloseOpenTaskQuestions did not notify the task card")
	}
}
