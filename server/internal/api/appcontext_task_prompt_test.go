package api

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/api/usecase"
	"mindfs/server/internal/kanban"
	"mindfs/server/internal/session"
)

// WP0: ask_user 是会话级事件，但用户要在任务对话里看到并回答。广播路径必须
// 把它同步成任务活动；重复广播（SDK 两条路径/重放）只能有一张卡。
func TestBroadcastSessionUpdateRecordsTaskQuestion(t *testing.T) {
	ctx := context.Background()
	app, rootID := newWorkflowAdapterTestApp(t)
	root, err := app.GetRoot(rootID)
	if err != nil {
		t.Fatalf("GetRoot: %v", err)
	}
	store, err := kanban.NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateTask(ctx,
		kanban.Task{ID: "task-q", RootID: rootID, TaskNumber: 1, Name: "提问任务", Status: kanban.StatusPending},
		kanban.TaskEvent{ID: "ev-q", TaskID: "task-q", Type: "created"},
	); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	app.Kanban = kanban.NewService(app)

	created, err := (&usecase.Service{Registry: app}).CreateSession(ctx, usecase.CreateSessionInput{
		RootID: rootID,
		Input: session.CreateInput{
			Type: session.TypeChat, Agent: "claude", Name: "提问会话", TaskID: "task-q",
		},
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	toolCall := agenttypes.ToolCall{
		CallID: "tool-q", Kind: agenttypes.ToolKindAskUser, Status: "running",
		Meta: map[string]any{
			"questions": []agenttypes.AskUserQuestionItem{{
				Question: "继续执行吗？",
				Options:  []agenttypes.AskUserQuestionOption{{Label: "继续"}, {Label: "停止"}},
			}},
		},
	}
	event := agenttypes.Event{Type: agenttypes.EventTypeToolCall, Data: toolCall}
	app.BroadcastSessionUpdate(rootID, created.Key, event)
	app.BroadcastSessionUpdate(rootID, created.Key, event)

	svc, err := app.GetKanbanService()
	if err != nil {
		t.Fatalf("GetKanbanService: %v", err)
	}
	activities, err := svc.ListTaskActivities(ctx, rootID, "task-q")
	if err != nil {
		t.Fatalf("ListTaskActivities: %v", err)
	}
	questions := make([]*kanban.TaskActivity, 0, 1)
	for _, activity := range activities {
		if activity.Type == kanban.ActivityTypeQuestion {
			questions = append(questions, activity)
		}
	}
	if len(questions) != 1 {
		t.Fatalf("question activities = %d, want exactly 1", len(questions))
	}
	question := questions[0]
	if question.Status != kanban.ActivityStatusWaitingUser {
		t.Fatalf("question status = %q, want waiting_user", question.Status)
	}
	if question.SessionKey != created.Key || question.ToolUseID != "tool-q" {
		t.Fatalf("question identity = session %q tool %q", question.SessionKey, question.ToolUseID)
	}
	if !strings.Contains(question.PayloadJSON, "继续执行吗？") {
		t.Fatalf("question payload = %q, want the question text", question.PayloadJSON)
	}
	if question.Body == "" {
		t.Fatal("question body is empty; task conversation would render a blank card")
	}
}

// 非 ask_user 的工具调用不能生成提问卡；否则任务对话会被普通工具卡灌满。
func TestBroadcastSessionUpdateIgnoresNonAskUserTools(t *testing.T) {
	ctx := context.Background()
	app, rootID := newWorkflowAdapterTestApp(t)
	root, err := app.GetRoot(rootID)
	if err != nil {
		t.Fatalf("GetRoot: %v", err)
	}
	store, err := kanban.NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateTask(ctx,
		kanban.Task{ID: "task-plain", RootID: rootID, TaskNumber: 1, Name: "普通任务", Status: kanban.StatusPending},
		kanban.TaskEvent{ID: "ev-plain", TaskID: "task-plain", Type: "created"},
	); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	app.Kanban = kanban.NewService(app)
	created, err := (&usecase.Service{Registry: app}).CreateSession(ctx, usecase.CreateSessionInput{
		RootID: rootID,
		Input: session.CreateInput{
			Type: session.TypeChat, Agent: "claude", Name: "普通会话", TaskID: "task-plain",
		},
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	app.BroadcastSessionUpdate(rootID, created.Key, agenttypes.Event{
		Type: agenttypes.EventTypeToolCall,
		Data: agenttypes.ToolCall{CallID: "tool-read", Kind: agenttypes.ToolKindRead, Status: "running"},
	})
	svc, _ := app.GetKanbanService()
	activities, err := svc.ListTaskActivities(ctx, rootID, "task-plain")
	if err != nil {
		t.Fatalf("ListTaskActivities: %v", err)
	}
	for _, activity := range activities {
		if activity.Type == kanban.ActivityTypeQuestion {
			t.Fatalf("non-ask tool created question activity %#v", activity)
		}
	}
}

// 会话侧的 tool_call_update complete 可能先于用户答复到达（或这一轮被中断）。
// 任务卡必须跟着收成 completed，但不能写 answers:null —— 迟到的真实答案还要能补。
func TestBroadcastSessionUpdateCompletesTaskQuestionOnToolComplete(t *testing.T) {
	ctx := context.Background()
	app, rootID := newWorkflowAdapterTestApp(t)
	root, err := app.GetRoot(rootID)
	if err != nil {
		t.Fatalf("GetRoot: %v", err)
	}
	store, err := kanban.NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateTask(ctx,
		kanban.Task{ID: "task-done", RootID: rootID, TaskNumber: 1, Name: "完成会话", Status: kanban.StatusPending},
		kanban.TaskEvent{ID: "ev-done", TaskID: "task-done", Type: "created"},
	); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	app.Kanban = kanban.NewService(app)
	created, err := (&usecase.Service{Registry: app}).CreateSession(ctx, usecase.CreateSessionInput{
		RootID: rootID,
		Input: session.CreateInput{
			Type: session.TypeChat, Agent: "claude", Name: "完成会话", TaskID: "task-done",
		},
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	ask := agenttypes.ToolCall{
		CallID: "tool-done", Kind: agenttypes.ToolKindAskUser, Status: "running",
		Meta: map[string]any{
			"questions": []agenttypes.AskUserQuestionItem{{Question: "继续执行吗？"}},
		},
	}
	app.BroadcastSessionUpdate(rootID, created.Key, agenttypes.Event{Type: agenttypes.EventTypeToolCall, Data: ask})
	app.BroadcastSessionUpdate(rootID, created.Key, agenttypes.Event{
		Type: agenttypes.EventTypeToolUpdate,
		Data: agenttypes.ToolCall{CallID: "tool-done", Kind: agenttypes.ToolKindAskUser, Status: "complete"},
	})

	svc, err := app.GetKanbanService()
	if err != nil {
		t.Fatalf("GetKanbanService: %v", err)
	}
	activities, err := svc.ListTaskActivities(ctx, rootID, "task-done")
	if err != nil {
		t.Fatalf("ListTaskActivities: %v", err)
	}
	var question *kanban.TaskActivity
	for _, activity := range activities {
		if activity.Type == kanban.ActivityTypeQuestion {
			question = activity
		}
	}
	if question == nil {
		t.Fatal("question activity missing")
	}
	if question.Status != kanban.ActivityStatusCompleted {
		t.Fatalf("question status = %q, want completed", question.Status)
	}
	if strings.Contains(question.PayloadJSON, `"answers"`) {
		t.Fatalf("silent completion wrote answers: %q", question.PayloadJSON)
	}
}

// 任务卡/会话查看器提交答案时，任务对话里的同一张卡必须写回答案。
func TestMarkTaskQuestionAnsweredUpdatesTaskCard(t *testing.T) {
	ctx := context.Background()
	app, rootID := newWorkflowAdapterTestApp(t)
	root, err := app.GetRoot(rootID)
	if err != nil {
		t.Fatalf("GetRoot: %v", err)
	}
	store, err := kanban.NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateTask(ctx,
		kanban.Task{ID: "task-answer", RootID: rootID, TaskNumber: 1, Name: "回答任务", Status: kanban.StatusPending},
		kanban.TaskEvent{ID: "ev-answer", TaskID: "task-answer", Type: "created"},
	); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	app.Kanban = kanban.NewService(app)
	created, err := (&usecase.Service{Registry: app}).CreateSession(ctx, usecase.CreateSessionInput{
		RootID: rootID,
		Input: session.CreateInput{
			Type: session.TypeChat, Agent: "claude", Name: "回答会话", TaskID: "task-answer",
		},
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	ask := agenttypes.ToolCall{
		CallID: "tool-answer", Kind: agenttypes.ToolKindAskUser, Status: "running",
		Meta: map[string]any{
			"questions": []agenttypes.AskUserQuestionItem{{Question: "继续执行吗？"}},
		},
	}
	app.BroadcastSessionUpdate(rootID, created.Key, agenttypes.Event{Type: agenttypes.EventTypeToolCall, Data: ask})
	app.markTaskQuestionAnswered(ctx, rootID, created.Key, "tool-answer", map[string]string{"q_0": "继续"})

	svc, err := app.GetKanbanService()
	if err != nil {
		t.Fatalf("GetKanbanService: %v", err)
	}
	activities, err := svc.ListTaskActivities(ctx, rootID, "task-answer")
	if err != nil {
		t.Fatalf("ListTaskActivities: %v", err)
	}
	var question *kanban.TaskActivity
	for _, activity := range activities {
		if activity.Type == kanban.ActivityTypeQuestion {
			question = activity
		}
	}
	if question == nil {
		t.Fatal("question activity missing")
	}
	if question.Status != kanban.ActivityStatusCompleted || !strings.Contains(question.PayloadJSON, `"q_0":"继续"`) {
		t.Fatalf("answered question = status %q payload %q", question.Status, question.PayloadJSON)
	}

	// W4-1：答案也要在任务对话里留一条 user_answer 用户行，并让随后的一轮结算
	// 带上 answered_question —— Router 才知道「用户答复了，会话继续了」。
	answerRows := 0
	for _, activity := range activities {
		if activity.Type == kanban.ActivityTypeUserAnswer {
			answerRows++
			if activity.Role != kanban.RoleUser || !strings.Contains(activity.Body, "继续") {
				t.Fatalf("user_answer activity = role %q body %q", activity.Role, activity.Body)
			}
		}
	}
	if answerRows != 1 {
		t.Fatalf("user_answer activities = %d, want 1", answerRows)
	}
	if _, err := svc.SettleSessionTurn(ctx, rootID, kanban.TurnSettledInput{
		SessionKey: created.Key, TurnID: "turn-answer", Excerpt: "按你的选择继续完成了。",
	}); err != nil {
		t.Fatalf("SettleSessionTurn: %v", err)
	}
	events, err := svc.ListRouterEventsForTask(ctx, rootID, "task-answer")
	if err != nil {
		t.Fatalf("ListRouterEvents: %v", err)
	}
	found := false
	for _, event := range events {
		if event.Kind == kanban.RouterEventTurnSettled {
			found = strings.Contains(event.PayloadJSON, `"answered_question":true`)
		}
	}
	if !found {
		t.Fatalf("turn_settled did not carry answered_question=true: %#v", events)
	}
}

// WS 回答成功后必须把同一张任务卡写回答案；漏掉这一句，任务对话会永远停在
// waiting_user（会话侧已经答完）。成功分支需要活着的 agent 会话，沿用
// session_aux_flags_broadcast_test 的源码守卫做法。
func TestAnswerQuestionHandlerMarksTaskQuestionAnswered(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	src, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "ws.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	start := strings.Index(text, "func (h *WSHandler) handleSessionAnswerQuestion(")
	if start < 0 {
		t.Fatal("handleSessionAnswerQuestion not found in ws.go")
	}
	end := strings.Index(text[start:], "\nfunc ")
	if end < 0 {
		end = len(text) - start
	}
	body := text[start : start+end]
	if !strings.Contains(body, "markTaskQuestionAnswered(") {
		t.Fatalf("handleSessionAnswerQuestion 没有把答案写回任务提问卡：\n%s", body)
	}
}

// 提问卡所在回合被取消/工具失败（tool_call_update failed）时，这张卡必须
// 立刻退出 waiting_user：它已经不可能再被回答，继续挡着输入框只会让任务死锁。
func TestBroadcastSessionUpdateClosesTaskQuestionOnFailedTool(t *testing.T) {
	app, rootID, sessionKey := newTaskQuestionTestApp(t, "task-failed")
	app.BroadcastSessionUpdate(rootID, sessionKey, agenttypes.Event{
		Type: agenttypes.EventTypeToolCall,
		Data: agenttypes.ToolCall{
			CallID: "tool-failed", Kind: agenttypes.ToolKindAskUser, Status: "running",
			Meta: map[string]any{"questions": []agenttypes.AskUserQuestionItem{{Question: "继续执行吗？"}}},
		},
	})
	app.BroadcastSessionUpdate(rootID, sessionKey, agenttypes.Event{
		Type: agenttypes.EventTypeToolUpdate,
		Data: agenttypes.ToolCall{CallID: "tool-failed", Kind: agenttypes.ToolKindAskUser, Status: "failed"},
	})

	svc, err := app.GetKanbanService()
	if err != nil {
		t.Fatalf("GetKanbanService: %v", err)
	}
	activities, err := svc.ListTaskActivities(context.Background(), rootID, "task-failed")
	if err != nil {
		t.Fatalf("ListTaskActivities: %v", err)
	}
	question := findQuestionActivity(t, activities)
	if question.Status != kanban.ActivityStatusCancelled {
		t.Fatalf("question status = %q, want cancelled after a failed ask", question.Status)
	}
}

// 回合终结是最后一道兜底：harness 取消回合时可能完全不发 tool update
// （Codex 当前就是如此），只靠事件收口会留下永远 waiting 的卡。
func TestEndSessionTurnClosesStaleTaskQuestions(t *testing.T) {
	app, rootID, sessionKey := newTaskQuestionTestApp(t, "task-turn-end")
	app.BroadcastSessionUpdate(rootID, sessionKey, agenttypes.Event{
		Type: agenttypes.EventTypeToolCall,
		Data: agenttypes.ToolCall{
			CallID: "tool-stale", Kind: agenttypes.ToolKindAskUser, Status: "running",
			Meta: map[string]any{"questions": []agenttypes.AskUserQuestionItem{{Question: "继续执行吗？"}}},
		},
	})
	turnID := app.SetSessionPendingReply(rootID, sessionKey, "提问会话", "turn-stale")
	if turnID == "" {
		t.Fatal("SetSessionPendingReply returned an empty turn id")
	}
	app.EndSessionTurn(rootID, sessionKey, "", turnID)

	svc, err := app.GetKanbanService()
	if err != nil {
		t.Fatalf("GetKanbanService: %v", err)
	}
	activities, err := svc.ListTaskActivities(context.Background(), rootID, "task-turn-end")
	if err != nil {
		t.Fatalf("ListTaskActivities: %v", err)
	}
	question := findQuestionActivity(t, activities)
	if question.Status != kanban.ActivityStatusCancelled {
		t.Fatalf("question status = %q, want cancelled after the turn ended", question.Status)
	}
}

func newTaskQuestionTestApp(t *testing.T, taskID string) (*AppContext, string, string) {
	t.Helper()
	ctx := context.Background()
	app, rootID := newWorkflowAdapterTestApp(t)
	root, err := app.GetRoot(rootID)
	if err != nil {
		t.Fatalf("GetRoot: %v", err)
	}
	store, err := kanban.NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateTask(ctx,
		kanban.Task{ID: taskID, RootID: rootID, TaskNumber: 1, Name: "提问任务", Status: kanban.StatusPending},
		kanban.TaskEvent{ID: "ev-" + taskID, TaskID: taskID, Type: "created"},
	); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	app.Kanban = kanban.NewService(app)
	created, err := (&usecase.Service{Registry: app}).CreateSession(ctx, usecase.CreateSessionInput{
		RootID: rootID,
		Input: session.CreateInput{
			Type: session.TypeChat, Agent: "claude", Name: "提问会话", TaskID: taskID,
		},
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return app, rootID, created.Key
}

func findQuestionActivity(t *testing.T, activities []*kanban.TaskActivity) *kanban.TaskActivity {
	t.Helper()
	for _, activity := range activities {
		if activity.Type == kanban.ActivityTypeQuestion {
			return activity
		}
	}
	t.Fatal("question activity missing")
	return nil
}

// W4-2 的 API 接缝：用户在会话视图里发消息（ws_session_message）时，必须把这一轮
// 认领成任务绑定的 out-of-band 回合；没有这一句，任务头整段时间都停在 waiting_user。
// 真跑 WS 需要活的 agent 会话，这里沿用本文件既有的源码守卫做法钉住接线。
func TestSessionSendClaimsOutOfBandTaskTurn(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	src, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "ws_session_message.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "BeginOutOfBandSessionTurn(") {
		t.Fatal("ws_session_message.go 没有在回合开始时认领 out-of-band 任务回合")
	}
}
