package feishu

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// retryableClickError stands in for the relay's typed remote-delivery error.
// It implements the same marker so the Service contract is testable here.
type retryableClickError struct{ message string }

func (e retryableClickError) Error() string         { return e.message }
func (e retryableClickError) RetryableAnswer() bool { return true }

func TestServiceRetryableAnswerErrorKeepsCardOpenAndRetryable(t *testing.T) {
	svc, st, _ := newTestService(t, Config{OwnerOpenID: "ou_owner"})
	ask := Notice{CardID: "ask-retry", Kind: KindAsk, RootID: "root", TaskID: "task-1",
		TaskTitle: "修复登录", Prompt: "确认继续", Options: []string{"好的"}}
	if err := svc.Notify(context.Background(), ask); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeliverOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	card, ok := st.Get("ask-retry")
	if !ok {
		t.Fatal("card missing after delivery")
	}

	reason := "节点 fn 暂不可达（已尝试 4 次）"
	svc.SetAnswerHandler(func(context.Context, Answer) error { return retryableClickError{message: reason} })
	first := Action{CardID: "ask-retry", Choice: 0, Text: "补充说明", OperatorID: "ou_owner", MessageID: card.MessageID}
	if err := svc.HandleAction(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	c, ok := st.Get("ask-retry")
	if !ok {
		t.Fatal("card disappeared")
	}
	if c.State != StateOpen {
		t.Fatalf("retryable delivery failure left state = %s, want open", c.State)
	}
	if !strings.Contains(c.LastError, reason) {
		t.Fatalf("LastError = %q, want readable reason %q", c.LastError, reason)
	}
	if c.Selected != 0 || c.Note != "补充说明" {
		t.Fatalf("answer not preserved: selected=%d note=%q", c.Selected, c.Note)
	}
	if c.Version < 3 {
		t.Fatalf("card version = %d, want a re-render after the retryable error", c.Version)
	}
	raw, err := RenderJSON(c)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, reason) {
		t.Fatalf("open card hides the retryable error: %s", raw)
	}
	if !strings.Contains(raw, `"callback"`) || !strings.Contains(raw, `"choice":0`) {
		t.Fatalf("open card lost its retry affordance: %s", raw)
	}

	applied := 0
	svc.SetAnswerHandler(func(context.Context, Answer) error { applied++; return nil })
	retry := Action{CardID: "ask-retry", Choice: 0, Text: "补充说明", OperatorID: "ou_owner", MessageID: card.MessageID}
	if err := svc.HandleAction(context.Background(), retry); err != nil {
		t.Fatal(err)
	}
	c, _ = st.Get("ask-retry")
	if applied != 1 || c.State != StateDone {
		t.Fatalf("retry: applied=%d state=%s, want 1 and done", applied, c.State)
	}
}

func TestServiceDeterministicAnswerErrorStillFailsCard(t *testing.T) {
	svc, st, _ := newTestService(t, Config{OwnerOpenID: "ou_owner"})
	if err := svc.Notify(context.Background(), gateNotice("gate-deterministic")); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeliverOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	card, _ := st.Get("gate-deterministic")
	svc.SetAnswerHandler(func(context.Context, Answer) error { return errors.New("未知节点 fn") })
	if err := svc.HandleAction(context.Background(), Action{
		CardID: "gate-deterministic", Choice: 0, OperatorID: "ou_owner", MessageID: card.MessageID,
	}); err != nil {
		t.Fatal(err)
	}
	c, _ := st.Get("gate-deterministic")
	if c.State != StateFailed || !strings.Contains(c.Summary, "没能执行") {
		t.Fatalf("deterministic failure state=%s summary=%q, want failed", c.State, c.Summary)
	}
}
