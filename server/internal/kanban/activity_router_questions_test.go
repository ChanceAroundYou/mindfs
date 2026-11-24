package kanban

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// Router 的 ask_user 必须落成**真的**提问卡（type=question）：按钮、输入框锁、
// 作答入口都挂在这个类型上。等待态的 agent 回复在前端只是一条没说完的话 ——
// 不锁输入框、没有按钮，答案也回不到 Router（#68 的四条待拍板事项就是这么
// 漂走的）。这一组用例钉住三件事：卡片的形状、新卡取代旧卡、作答走留言。
func TestRouteAskUserLandsRealQuestionCard(t *testing.T) {
	h := newAskHarness(t)
	if h.question.Type != ActivityTypeQuestion {
		t.Fatalf("ask_user activity type = %q, want %q", h.question.Type, ActivityTypeQuestion)
	}
	var payload struct {
		Source    string               `json:"source"`
		Questions []routerQuestionItem `json:"questions"`
	}
	if err := json.Unmarshal([]byte(h.question.PayloadJSON), &payload); err != nil {
		t.Fatalf("payload json: %v", err)
	}
	if payload.Source != routerQuestionSource {
		t.Fatalf("payload source = %q, want %q", payload.Source, routerQuestionSource)
	}
	if len(payload.Questions) != 1 {
		t.Fatalf("questions = %d, want 1", len(payload.Questions))
	}
	if got := payload.Questions[0].Question; got != "先做哪个？" {
		t.Fatalf("question = %q", got)
	}
	// 选项必须以卡片按钮的结构出现，而不是只拼在正文里。
	if !strings.Contains(h.question.PayloadJSON, `"label":"A"`) ||
		!strings.Contains(h.question.PayloadJSON, `"label":"B"`) {
		t.Fatalf("options missing from payload: %s", h.question.PayloadJSON)
	}
	// 正文仍然保留问题与选项：飞书通知与纯文本客户端读的是它。
	if !strings.Contains(h.question.Body, "先做哪个？") || !strings.Contains(h.question.Body, "A") {
		t.Fatalf("body lost the readable question: %q", h.question.Body)
	}
}

// 新提问落地时必须收口旧的 Router 提问卡：前端锁输入框读的是**最早**那张
// waiting 卡（pendingTaskPrompt），旧卡留着用户就会点到过时的问题，而 Router
// 等的是新问题 —— #104 里问答对不上的形态。
func TestRouteAskUserSupersedesEarlierWaitingCard(t *testing.T) {
	ctx := context.Background()
	f := newRouteFixture(t, RouterModeOff)
	userAct, err := f.activity.QueueUserActivity(ctx, "task-1", "帮我定一下", CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		t.Fatalf("QueueUserActivity: %v", err)
	}
	task, err := f.store.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	firstDecision := &RouterDecision{
		ID: "dec-ask-1", TaskID: "task-1", RootID: f.rootID, TurnID: "msg:1",
		UserActivityID: userAct.ID, TriggerKind: RouterTriggerUserMessage,
	}
	if err := f.activity.applyRouteAskUser(ctx, firstDecision, task, userAct, &routeApplyState{},
		RouterAction{Seq: 0, Kind: RouterActionAskUser, Question: "先做哪个？", Options: []string{"A", "B"}}); err != nil {
		t.Fatalf("applyRouteAskUser(first): %v", err)
	}
	first := openRouterQuestion(t, f, "先做哪个？")
	secondDecision := &RouterDecision{
		ID: "dec-ask-2", TaskID: "task-1", RootID: f.rootID, TurnID: "msg:2",
		UserActivityID: userAct.ID, TriggerKind: RouterTriggerUserMessage,
	}
	if err := f.activity.applyRouteAskUser(ctx, secondDecision, task, userAct, &routeApplyState{},
		RouterAction{Seq: 0, Kind: RouterActionAskUser, Question: "改口了：先做哪个？", Options: []string{"A", "B"}}); err != nil {
		t.Fatalf("applyRouteAskUser(second): %v", err)
	}
	closed := reloadTestActivity(t, f.store, first.ID)
	if closed.Status != ActivityStatusCancelled {
		t.Fatalf("superseded card status = %q, want cancelled", closed.Status)
	}
	if !strings.Contains(closed.Summary, "取代") {
		t.Fatalf("superseded card summary = %q, want the reason", closed.Summary)
	}
	open := openRouterQuestions(t, f)
	if len(open) != 1 || !strings.Contains(open[0].Body, "改口了") {
		t.Fatalf("open questions = %d, want only the newer one", len(open))
	}
}

// 作答 = 一条绑定的用户留言 + 卡片收口。Router 读到的必须是普通留言：它不在
// 任何会话里，没有 tool call 可回填。
func TestAnswerQuestionCardCreatesUserReplyAndClosesCard(t *testing.T) {
	ctx := context.Background()
	h := newAskHarness(t)
	userAct, _, err := h.f.activity.AnswerQuestionCard(ctx, "task-1", h.question.ID,
		map[string]string{"q_0": "A"})
	if err != nil {
		t.Fatalf("AnswerQuestionCard: %v", err)
	}
	if userAct.Body != "先做哪个？ → A" {
		t.Fatalf("answer body = %q, want the question paired with the choice", userAct.Body)
	}
	if userAct.ReplyToID != h.question.ID {
		t.Fatalf("answer reply_to = %q, want the question card", userAct.ReplyToID)
	}
	card := reloadTestActivity(t, h.f.store, h.question.ID)
	if card.Status != ActivityStatusCompleted {
		t.Fatalf("card status = %q, want completed", card.Status)
	}
	var payload struct {
		Source  string            `json:"source"`
		Answers map[string]string `json:"answers"`
	}
	if err := json.Unmarshal([]byte(card.PayloadJSON), &payload); err != nil {
		t.Fatalf("card payload: %v", err)
	}
	if payload.Answers["q_0"] != "A" {
		t.Fatalf("card answers = %#v, want the submitted choice", payload.Answers)
	}
	if payload.Source != routerQuestionSource {
		t.Fatalf("answering dropped the card source: %q", payload.Source)
	}
	// 再点一次：卡已收口，不能写出第二条留言。
	if _, _, err := h.f.activity.AnswerQuestionCard(ctx, "task-1", h.question.ID,
		map[string]string{"q_0": "B"}); !errors.Is(err, ErrQuestionAlreadyAnswered) {
		t.Fatalf("second answer err = %v, want ErrQuestionAlreadyAnswered", err)
	}
}

// 会话里的提问卡不能走这条路径：答案属于那个会话的 tool call，当成任务留言
// 派发会让等在那里的 Agent 永远收不到结果。
func TestAnswerQuestionCardRejectsSessionBoundCard(t *testing.T) {
	ctx := context.Background()
	f := newRouteFixture(t, RouterModeOff)
	act := &TaskActivity{
		ID: "act-session-question", TaskID: "task-1", RootID: f.rootID,
		Type: ActivityTypeQuestion, Role: RoleAgent, Body: "会话里的提问",
		Status: ActivityStatusWaitingUser, SessionKey: "sess-1", ToolUseID: "tool-1",
		PayloadJSON: `{"questions":[{"question":"选哪个？","options":[{"label":"A"}]}]}`,
		CreatedAt:   time.Now().UTC(),
	}
	if err := NewActivityStore(f.store.db).Insert(ctx, act); err != nil {
		t.Fatalf("insert question: %v", err)
	}
	_, _, err := f.activity.AnswerQuestionCard(ctx, "task-1", act.ID, map[string]string{"q_0": "A"})
	if !errors.Is(err, ErrQuestionSessionBound) {
		t.Fatalf("session card answer err = %v, want ErrQuestionSessionBound", err)
	}
}

// openRouterQuestion 按问题正文找当前唯一一张等待中的 Router 提问卡。
func openRouterQuestion(t *testing.T, f *routeFixture, want string) *TaskActivity {
	t.Helper()
	for _, act := range openRouterQuestions(t, f) {
		if strings.Contains(act.Body, want) {
			return act
		}
	}
	t.Fatalf("no open router question containing %q", want)
	return nil
}

func openRouterQuestions(t *testing.T, f *routeFixture) []*TaskActivity {
	t.Helper()
	open, err := NewActivityStore(f.store.db).OpenRouterQuestionsByTask(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("OpenRouterQuestionsByTask: %v", err)
	}
	return open
}
