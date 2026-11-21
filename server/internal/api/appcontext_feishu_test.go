package api

import (
	"context"
	"strings"
	"testing"

	"mindfs/server/internal/channels/feishu"
)

// A click on a card that no longer exists must not be applied to whatever
// task happens to share an id: an unknown kind is a programming error, not a
// no-op.
func TestApplyFeishuAnswerRejectsUnknownKind(t *testing.T) {
	ctx := context.Background()
	err := (&AppContext{}).ApplyFeishuAnswer(ctx, feishu.Answer{CardID: "x", Kind: feishu.Kind("mystery")})
	if err == nil || !strings.Contains(err.Error(), "mystery") {
		t.Fatalf("err = %v", err)
	}
}

// A notice card has nothing to apply: its whole job is to be seen and
// acknowledged, which the channel already did before calling us. Returning an
// error here would turn "知道了" into a failed card.
func TestApplyFeishuAnswerNoticeIsNoop(t *testing.T) {
	if err := (&AppContext{}).ApplyFeishuAnswer(context.Background(), feishu.Answer{CardID: "x", Kind: feishu.KindNotice}); err != nil {
		t.Fatalf("notice answer = %v", err)
	}
}

// An empty answer is not an answer: writing it back would dispatch an empty
// user message and fail the task turn.
func TestApplyFeishuAnswerRejectsEmptyAskText(t *testing.T) {
	err := (&AppContext{}).ApplyFeishuAnswer(context.Background(), feishu.Answer{
		CardID: "x", Kind: feishu.KindAsk, TaskID: "task-1", QuestionID: "act_1", Text: "   ",
	})
	if err == nil {
		t.Fatal("empty ask answer was accepted")
	}
}

// A gate needs an explicit approve/reject; anything else is a stale or
// garbled click.
func TestApplyFeishuAnswerRejectsUnknownGateDecision(t *testing.T) {
	err := (&AppContext{}).ApplyFeishuAnswer(context.Background(), feishu.Answer{
		CardID: "x", Kind: feishu.KindGate, TaskID: "task-1", WorkflowNodeID: "gate", Decision: "later",
	})
	if err == nil {
		t.Fatal("unknown gate decision was accepted")
	}
}
