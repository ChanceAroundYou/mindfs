package kanban

import (
	"context"
	"strings"
	"testing"
)

// WP0: ask_user 事件必须落成任务对话里的 question 卡，并且同一 tool call
// 只能有一张。工作流节点会话还要绑定当前活动的 NodeRun，便于运行视图对账。
func TestRecordTaskQuestionCreatesWaitingCard(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)

	run := &WorkflowRun{ID: "run-q", TaskID: "task-1", Revision: 1, Graph: wfDef(wfNode("agent-1", NodeKindAgent))}
	if err := f.tasks.CreateWorkflowRunAndSetEngine(ctx, run); err != nil {
		t.Fatalf("CreateWorkflowRunAndSetEngine: %v", err)
	}
	if err := f.tasks.UpsertWorkflowNodeRun(ctx, NodeRun{
		ID: "nr-q", RunID: "run-q", NodeID: "agent-1", Attempt: 1,
		Status: NodeRunRunning, SessionKey: "sess-q",
	}); err != nil {
		t.Fatalf("UpsertWorkflowNodeRun: %v", err)
	}

	payload := `{"questions":[{"question":"继续吗？","options":[{"label":"继续"},{"label":"停止"}]}],"agent":"claude"}`
	input := TaskQuestionInput{
		TaskID:      "task-1",
		SessionKey:  "sess-q",
		ToolUseID:   "tool-q",
		Body:        "需要确认：继续吗？",
		PayloadJSON: payload,
	}
	act, created, err := f.service.RecordTaskQuestion(ctx, input)
	if err != nil {
		t.Fatalf("RecordTaskQuestion: %v", err)
	}
	if !created {
		t.Fatal("first RecordTaskQuestion created = false, want true")
	}
	if act == nil || act.Type != ActivityTypeQuestion || act.Status != ActivityStatusWaitingUser {
		t.Fatalf("activity = %#v, want question/waiting_user", act)
	}
	if act.Role != RoleAgent || act.Author != "agent" {
		t.Fatalf("activity role/author = %q/%q", act.Role, act.Author)
	}
	if act.TaskID != "task-1" || act.SessionKey != "sess-q" || act.ToolUseID != "tool-q" {
		t.Fatalf("activity identity = %#v", act)
	}
	if act.RootID == "" || act.RootID != f.root.ID {
		t.Fatalf("activity root id = %q, want %q", act.RootID, f.root.ID)
	}
	if act.RunID != "run-q" || act.NodeRunID != "nr-q" {
		t.Fatalf("activity run binding = %q/%q, want run-q/nr-q", act.RunID, act.NodeRunID)
	}
	if act.PayloadJSON != payload || act.Body != input.Body {
		t.Fatalf("activity payload/body = %q/%q", act.PayloadJSON, act.Body)
	}

	got, err := NewActivityStore(f.tasks.db).QuestionByToolUseID(ctx, "sess-q", "tool-q")
	if err != nil || got.ID != act.ID {
		t.Fatalf("QuestionByToolUseID = %#v err=%v, want %q", got, err, act.ID)
	}

	again, createdAgain, err := f.service.RecordTaskQuestion(ctx, input)
	if err != nil {
		t.Fatalf("duplicate RecordTaskQuestion: %v", err)
	}
	if createdAgain {
		t.Fatal("duplicate RecordTaskQuestion created = true, want idempotent false")
	}
	if again == nil || again.ID != act.ID {
		t.Fatalf("duplicate activity = %#v, want %q", again, act.ID)
	}
	list, err := NewActivityStore(f.tasks.db).List(ctx, "task-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	questions := 0
	for _, item := range list {
		if item.Type == ActivityTypeQuestion {
			questions++
		}
	}
	if questions != 1 {
		t.Fatalf("question activities = %d, want 1", questions)
	}
	if created := f.broadcast.createdIDs(); len(created) != 1 || created[0] != act.ID {
		t.Fatalf("broadcast created = %v, want [%s]", created, act.ID)
	}

	// 直连会话没有活动 NodeRun：卡片照建，但不绑 run/node。
	chat, _, err := f.service.RecordTaskQuestion(ctx, TaskQuestionInput{
		TaskID: "task-1", SessionKey: "sess-chat", ToolUseID: "tool-chat",
		Body: "需要确认", PayloadJSON: `{"questions":[]}`,
	})
	if err != nil {
		t.Fatalf("RecordTaskQuestion direct: %v", err)
	}
	if chat.RunID != "" || chat.NodeRunID != "" {
		t.Fatalf("direct question binding = %q/%q, want empty", chat.RunID, chat.NodeRunID)
	}
}

func TestRecordTaskQuestionRequiresIdentity(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)
	for _, in := range []TaskQuestionInput{
		{SessionKey: "s", ToolUseID: "t"},
		{TaskID: "task-1", ToolUseID: "t"},
		{TaskID: "task-1", SessionKey: "s"},
	} {
		if _, _, err := f.service.RecordTaskQuestion(ctx, in); err == nil {
			t.Fatalf("RecordTaskQuestion(%#v) should reject missing identity", in)
		}
	}
}

// 回答可以从任务卡或会话查看器任一侧提交；无论从哪边来，任务卡都必须落成
// completed、把答案写回 payload，并且重复提交只生效一次。
func TestMarkTaskQuestionAnsweredCompletesCard(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)
	payload := `{"questions":[{"question":"继续吗？"}],"agent":"claude"}`
	act, _, err := f.service.RecordTaskQuestion(ctx, TaskQuestionInput{
		TaskID: "task-1", SessionKey: "sess-answer", ToolUseID: "tool-answer", Body: "需要确认", PayloadJSON: payload,
	})
	if err != nil {
		t.Fatalf("RecordTaskQuestion: %v", err)
	}

	answered, changed, err := f.service.MarkTaskQuestionAnswered(ctx, "sess-answer", "tool-answer", map[string]string{"q_0": "继续"})
	if err != nil {
		t.Fatalf("MarkTaskQuestionAnswered: %v", err)
	}
	if !changed {
		t.Fatal("first MarkTaskQuestionAnswered changed = false, want true")
	}
	if answered == nil || answered.ID != act.ID || answered.Status != ActivityStatusCompleted {
		t.Fatalf("answered = %#v, want completed %q", answered, act.ID)
	}
	if !strings.Contains(answered.PayloadJSON, `"q_0":"继续"`) {
		t.Fatalf("answered payload = %q, want answers q_0=继续", answered.PayloadJSON)
	}
	if !strings.Contains(answered.PayloadJSON, "继续吗？") {
		t.Fatalf("answered payload lost the original questions: %q", answered.PayloadJSON)
	}
	updated := f.broadcast.updatedIDs()
	if len(updated) != 1 || updated[0] != act.ID {
		t.Fatalf("broadcast updated = %v, want [%s]", updated, act.ID)
	}

	again, changedAgain, err := f.service.MarkTaskQuestionAnswered(ctx, "sess-answer", "tool-answer", map[string]string{"q_0": "停止"})
	if err != nil {
		t.Fatalf("duplicate MarkTaskQuestionAnswered: %v", err)
	}
	if changedAgain {
		t.Fatal("duplicate MarkTaskQuestionAnswered changed = true, want idempotent false")
	}
	if again == nil || again.Status != ActivityStatusCompleted || strings.Contains(again.PayloadJSON, "停止") {
		t.Fatalf("duplicate answer overwrote the first one: %#v", again)
	}
	if updated := f.broadcast.updatedIDs(); len(updated) != 1 {
		t.Fatalf("duplicate answer broadcast = %v, want no second update", updated)
	}

	missing, changed, err := f.service.MarkTaskQuestionAnswered(ctx, "sess-answer", "missing-tool", map[string]string{"q_0": "x"})
	if err != nil || changed || missing != nil {
		t.Fatalf("missing question = %#v/%v/%v, want nil/false/nil", missing, changed, err)
	}
}

// 会话侧的 tool_call_update complete 可能先到、且不带答案；这时卡先收成
// completed，随后 WS 提交的真实答案仍要能补进 payload，不能被幂等挡掉。
func TestMarkTaskQuestionAnsweredFillsAnswersAfterSilentCompletion(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)
	act, _, err := f.service.RecordTaskQuestion(ctx, TaskQuestionInput{
		TaskID: "task-1", SessionKey: "sess-race", ToolUseID: "tool-race", Body: "需要确认", PayloadJSON: `{"questions":[{"question":"继续吗？"}]}`,
	})
	if err != nil {
		t.Fatalf("RecordTaskQuestion: %v", err)
	}
	silent, changed, err := f.service.MarkTaskQuestionAnswered(ctx, "sess-race", "tool-race", nil)
	if err != nil || !changed {
		t.Fatalf("silent completion = %#v/%v/%v, want completed", silent, changed, err)
	}
	if silent.Status != ActivityStatusCompleted || strings.Contains(silent.PayloadJSON, `"answers"`) {
		t.Fatalf("silent completion = %#v, want completed without answers", silent)
	}

	filled, changed, err := f.service.MarkTaskQuestionAnswered(ctx, "sess-race", "tool-race", map[string]string{"q_0": "继续"})
	if err != nil || !changed {
		t.Fatalf("late answer = %#v/%v/%v, want filled", filled, changed, err)
	}
	if filled.ID != act.ID || !strings.Contains(filled.PayloadJSON, `"q_0":"继续"`) {
		t.Fatalf("late answer payload = %q, want q_0=继续", filled.PayloadJSON)
	}
}

// 回合结束/取消时，仍停在 waiting_user 的提问卡必须收口。任务对话在存在
// waiting 卡时会挡住输入框；不收起一张已经不可能回答的卡，用户既答不了也
// 发不了新消息，任务就永久卡死。
//
// 这里直接插行而不走 RecordTaskQuestion：后者现在会在同一会话插入新卡时把更早
// 的卡收口（见下一个用例），造不出「同一会话两张同时等待」的现场。而本用例要
// 钉的正是收口函数的筛选口径 —— 升级前遗留的多张等待卡必须都能被这一条路收掉。
func TestCloseOpenTaskQuestionsClosesOnlyWaitingCardsForSession(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)
	store := NewActivityStore(f.tasks.db)
	seed := func(id, session, tool, status string) *TaskActivity {
		act := &TaskActivity{
			ID: id, TaskID: "task-1", RootID: f.root.ID,
			Type: ActivityTypeQuestion, Author: "agent", Role: RoleAgent,
			Body: "需要确认", Status: status,
			SessionKey: session, ToolUseID: tool,
			PayloadJSON: `{"questions":[{"question":"继续吗？"}]}`,
		}
		if _, err := store.InsertOrIgnore(ctx, act); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
		return act
	}
	openA := seed("q-close-a", "sess-close", "tool-a", ActivityStatusWaitingUser)
	openB := seed("q-close-b", "sess-close", "tool-b", ActivityStatusWaitingUser)
	other := seed("q-close-other", "sess-other", "tool-c", ActivityStatusWaitingUser)
	answered := seed("q-close-done", "sess-close", "tool-d", ActivityStatusCompleted)

	closed, err := f.service.CloseOpenTaskQuestions(ctx, "sess-close", "回合已结束，提问未回答")
	if err != nil {
		t.Fatalf("CloseOpenTaskQuestions: %v", err)
	}
	if closed != 2 {
		t.Fatalf("closed = %d, want 2 (only waiting cards in this session)", closed)
	}
	for _, id := range []string{openA.ID, openB.ID} {
		got, err := store.Get(ctx, id)
		if err != nil {
			t.Fatalf("Get(%s): %v", id, err)
		}
		if got.Status != ActivityStatusCancelled {
			t.Fatalf("%s status = %q, want cancelled", id, got.Status)
		}
		if !strings.Contains(got.Summary, "提问未回答") {
			t.Fatalf("%s summary = %q, want the close reason", id, got.Summary)
		}
	}
	otherReload, err := store.Get(ctx, other.ID)
	if err != nil || otherReload.Status != ActivityStatusWaitingUser {
		t.Fatalf("other session card = %#v err=%v, want untouched waiting_user", otherReload, err)
	}
	answeredReload, err := store.Get(ctx, answered.ID)
	if err != nil || answeredReload.Status != ActivityStatusCompleted {
		t.Fatalf("answered card = %#v err=%v, want completed", answeredReload, err)
	}

	again, err := f.service.CloseOpenTaskQuestions(ctx, "sess-close", "回合已结束，提问未回答")
	if err != nil || again != 0 {
		t.Fatalf("second close = %d/%v, want idempotent 0", again, err)
	}
}

// W4-1：用户真的回答了提问卡时，任务对话里要留下一条 user_answer 用户活动。
// Router 的 brief 只读任务对话；没有这条，它只能看到「提问卡 completion」，
// 看不见用户答了什么、会话又继续了。
func TestMarkTaskQuestionAnsweredRecordsUserAnswerActivity(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)
	if _, _, err := f.service.RecordTaskQuestion(ctx, TaskQuestionInput{
		TaskID: "task-1", SessionKey: "sess-1", ToolUseID: "tool-1",
		Body: "需要确认", PayloadJSON: `{"questions":[{"question":"继续吗？"}]}`,
	}); err != nil {
		t.Fatalf("RecordTaskQuestion: %v", err)
	}
	if _, changed, err := f.service.MarkTaskQuestionAnswered(ctx, "sess-1", "tool-1",
		map[string]string{"继续吗？": "继续，按方案二改"}); err != nil {
		t.Fatalf("MarkTaskQuestionAnswered: %v", err)
	} else if !changed {
		t.Fatal("first answer did not change the question card")
	}

	list, err := NewActivityStore(f.tasks.db).List(ctx, "task-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var answers []*TaskActivity
	for _, item := range list {
		if item.Type == ActivityTypeUserAnswer {
			answers = append(answers, item)
		}
	}
	if len(answers) != 1 {
		t.Fatalf("user_answer activities = %d, want 1", len(answers))
	}
	got := answers[0]
	if got.Role != RoleUser {
		t.Fatalf("user_answer role = %q, want %q", got.Role, RoleUser)
	}
	if got.SessionKey != "sess-1" {
		t.Fatalf("user_answer session = %q, want sess-1", got.SessionKey)
	}
	if !strings.Contains(got.Body, "继续，按方案二改") {
		t.Fatalf("user_answer body = %q, want the answer text", got.Body)
	}

	// 重放同一个答案不能长出第二条：活动 id 由提问卡确定性派生。
	if _, changed, err := f.service.MarkTaskQuestionAnswered(ctx, "sess-1", "tool-1",
		map[string]string{"继续吗？": "继续，按方案二改"}); err != nil {
		t.Fatalf("replay: %v", err)
	} else if changed {
		t.Fatal("replayed answer reported a change")
	}
	list, err = NewActivityStore(f.tasks.db).List(ctx, "task-1")
	if err != nil {
		t.Fatalf("List after replay: %v", err)
	}
	replayed := 0
	for _, item := range list {
		if item.Type == ActivityTypeUserAnswer {
			replayed++
		}
	}
	if replayed != 1 {
		t.Fatalf("user_answer activities after replay = %d, want 1", replayed)
	}

	// 只收口（空答案）不是「用户答了」，不该留下 user_answer。
	if _, _, err := f.service.RecordTaskQuestion(ctx, TaskQuestionInput{
		TaskID: "task-1", SessionKey: "sess-1", ToolUseID: "tool-2",
		Body: "再确认", PayloadJSON: `{"questions":[{"question":"还好吗？"}]}`,
	}); err != nil {
		t.Fatalf("RecordTaskQuestion(2): %v", err)
	}
	if _, changed, err := f.service.MarkTaskQuestionAnswered(ctx, "sess-1", "tool-2", nil); err != nil {
		t.Fatalf("MarkTaskQuestionAnswered(empty): %v", err)
	} else if !changed {
		t.Fatal("empty answers did not close the card")
	}
	list, err = NewActivityStore(f.tasks.db).List(ctx, "task-1")
	if err != nil {
		t.Fatalf("List after empty answer: %v", err)
	}
	empty := 0
	for _, item := range list {
		if item.Type == ActivityTypeUserAnswer {
			empty++
		}
	}
	if empty != 1 {
		t.Fatalf("user_answer activities after empty close = %d, want 1", empty)
	}
}

// 同一会话里新插一张等待卡时，更早那张必须退出等待态。
//
// 现场 #104：Agent 用新的 tool_use_id 重问了一次，旧卡（q-84825203e4fe3902）
// 永远停在 waiting_user —— 去重键是 (session_key, tool_use_id)，换了 id 就是
// 新卡，而前端取的是**最早**那张等待卡来锁输入框。结果用户答了新卡，挡住
// 输入框的却是那张再也没人回答的旧卡。
func TestRecordTaskQuestionCancelsOlderWaitingCards(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)

	first, _, err := f.service.RecordTaskQuestion(ctx, TaskQuestionInput{
		TaskID: "task-1", SessionKey: "sess-q", ToolUseID: "tool-old",
		Body: "第一问", PayloadJSON: `{"questions":[{"question":"第一问"}]}`,
	})
	if err != nil {
		t.Fatalf("RecordTaskQuestion(first): %v", err)
	}
	second, _, err := f.service.RecordTaskQuestion(ctx, TaskQuestionInput{
		TaskID: "task-1", SessionKey: "sess-q", ToolUseID: "tool-new",
		Body: "第二问", PayloadJSON: `{"questions":[{"question":"第二问"}]}`,
	})
	if err != nil {
		t.Fatalf("RecordTaskQuestion(second): %v", err)
	}

	store := NewActivityStore(f.tasks.db)
	old, err := store.Get(ctx, first.ID)
	if err != nil {
		t.Fatalf("Get(old): %v", err)
	}
	if old.Status != ActivityStatusCancelled {
		t.Fatalf("older card status = %q, want cancelled", old.Status)
	}
	if !strings.Contains(old.Summary, "已被更新的提问取代") {
		t.Fatalf("older card summary = %q, want the superseded explanation", old.Summary)
	}
	latest, err := store.Get(ctx, second.ID)
	if err != nil {
		t.Fatalf("Get(new): %v", err)
	}
	if latest.Status != ActivityStatusWaitingUser {
		t.Fatalf("newest card status = %q, want waiting_user", latest.Status)
	}

	// 只留一张可回答的卡：这正是前端锁输入框时读到的集合。
	open, err := store.OpenQuestionsBySession(ctx, "sess-q")
	if err != nil {
		t.Fatalf("OpenQuestionsBySession: %v", err)
	}
	if len(open) != 1 || open[0].ID != second.ID {
		t.Fatalf("open questions = %v, want only the newest card", ids(open))
	}
}

// 换会话/换任务不算「被更新的提问取代」：别的任务或别的会话里的卡照旧等答案。
func TestRecordTaskQuestionOnlySupersedesSameSession(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)

	if _, _, err := f.service.RecordTaskQuestion(ctx, TaskQuestionInput{
		TaskID: "task-1", SessionKey: "sess-a", ToolUseID: "tool-a",
		Body: "会话 A", PayloadJSON: `{"questions":[{"question":"会话 A"}]}`,
	}); err != nil {
		t.Fatalf("RecordTaskQuestion(sess-a): %v", err)
	}
	if _, _, err := f.service.RecordTaskQuestion(ctx, TaskQuestionInput{
		TaskID: "task-1", SessionKey: "sess-b", ToolUseID: "tool-b",
		Body: "会话 B", PayloadJSON: `{"questions":[{"question":"会话 B"}]}`,
	}); err != nil {
		t.Fatalf("RecordTaskQuestion(sess-b): %v", err)
	}

	openA, err := NewActivityStore(f.tasks.db).OpenQuestionsBySession(ctx, "sess-a")
	if err != nil {
		t.Fatalf("OpenQuestionsBySession(sess-a): %v", err)
	}
	if len(openA) != 1 {
		t.Fatalf("session A open questions = %d, want 1 (other sessions must not close it)", len(openA))
	}
}

// ids 把卡列表压成 id 列表，失败信息里能看出是哪几张卡。
func ids(list []*TaskActivity) []string {
	out := make([]string, 0, len(list))
	for _, item := range list {
		out = append(out, item.ID)
	}
	return out
}
