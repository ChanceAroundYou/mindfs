package kanban

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// W4：会话推进跟随。
//
// 两个缺口（线上 #68）：用户在会话里推进一轮、或在提问卡上答完继续，任务头
// 没有任何变化，Router 的 turn_settled 也看不出「这是个答复回合 / 这轮是用户
// 自己动手推的」。这里钉住三件事：
//   - 答过提问的回合，结算事件带 answered_question=true；
//   - 任务绑定会话的 out-of-band 回合，waiting_user → running → 结算回 waiting_user；
//   - main_session_key 为空时，「当前会话」回退到最近活跃的相关会话。

func turnSettledPayload(t *testing.T, f *turnSettledFixture, index int) map[string]any {
	t.Helper()
	events := f.events(t, RouterEventTurnSettled)
	if len(events) <= index {
		t.Fatalf("turn_settled events = %d, want > %d", len(events), index)
	}
	payload := map[string]any{}
	if err := json.Unmarshal([]byte(events[index].PayloadJSON), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	return payload
}

func TestAnsweredQuestionFlagsNextTurnSettlement(t *testing.T) {
	ctx := context.Background()
	f := newTurnSettledFixture(t, "task-answer-flag", "sess-main")

	// 作答之后紧接着的那一轮结算要带上 answered_question。
	f.svc.NoteSessionAnsweredQuestion(f.rootID, "sess-main")
	if _, err := f.svc.SettleSessionTurn(ctx, f.rootID, TurnSettledInput{
		SessionKey: "sess-main", TurnID: "turn-answer", Excerpt: "按你的选择继续完成了。",
	}); err != nil {
		t.Fatalf("SettleSessionTurn: %v", err)
	}
	if got := turnSettledPayload(t, f, 0)["answered_question"]; got != true {
		t.Fatalf("answered_question = %#v, want true", got)
	}

	// 标记是一次性的：下一轮没再答题，事件里不能继续声称答过。
	if _, err := f.svc.SettleSessionTurn(ctx, f.rootID, TurnSettledInput{
		SessionKey: "sess-main", TurnID: "turn-answer-next", Excerpt: "又补了一处。",
	}); err != nil {
		t.Fatalf("SettleSessionTurn(next): %v", err)
	}
	if got := turnSettledPayload(t, f, 1)["answered_question"]; got != false {
		t.Fatalf("answered_question (next turn) = %#v, want false", got)
	}
}

func TestOutOfBandTurnBumpsWaitingTaskAndSettlesBack(t *testing.T) {
	ctx := context.Background()
	f := newTurnSettledFixture(t, "task-oob", "sess-main")

	if !f.svc.BeginOutOfBandSessionTurn(ctx, f.rootID, "sess-main") {
		t.Fatal("main-session turn was not recognized as out-of-band")
	}
	task, err := f.store.GetTask(ctx, f.taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != StatusRunning {
		t.Fatalf("status during out-of-band turn = %q, want %q", task.Status, StatusRunning)
	}
	if _, err := f.svc.SettleSessionTurn(ctx, f.rootID, TurnSettledInput{
		SessionKey: "sess-main", TurnID: "turn-oob", Excerpt: "用户自己在会话里推进了一轮。",
	}); err != nil {
		t.Fatalf("SettleSessionTurn: %v", err)
	}
	if got := turnSettledPayload(t, f, 0)["out_of_band"]; got != true {
		t.Fatalf("out_of_band = %#v, want true", got)
	}
	task, err = f.store.GetTask(ctx, f.taskID)
	if err != nil {
		t.Fatalf("GetTask after settle: %v", err)
	}
	if task.Status != StatusWaitingUser {
		t.Fatalf("status after out-of-band turn = %q, want %q", task.Status, StatusWaitingUser)
	}

	// 节点会话同样生效：用户在任务某个节点的会话里动手，也算这一轮在推进任务。
	store := NewActivityStore(f.store.db)
	if err := store.Insert(ctx, &TaskActivity{
		ID: "act-node-reply", TaskID: f.taskID, RootID: f.rootID,
		Type: ActivityTypeAgentReply, Role: RoleAgent, Body: "节点先回一句",
		SessionKey: "sess-node", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("insert node reply: %v", err)
	}
	if !f.svc.BeginOutOfBandSessionTurn(ctx, f.rootID, "sess-node") {
		t.Fatal("node-session turn was not recognized as out-of-band")
	}
	if _, err := f.svc.SettleSessionTurn(ctx, f.rootID, TurnSettledInput{
		SessionKey: "sess-node", TurnID: "turn-oob-node", Excerpt: "节点会话继续跑完。",
	}); err != nil {
		t.Fatalf("SettleSessionTurn(node): %v", err)
	}
	if got := turnSettledPayload(t, f, 1)["out_of_band"]; got != true {
		t.Fatalf("out_of_band (node) = %#v, want true", got)
	}
	task, err = f.store.GetTask(ctx, f.taskID)
	if err != nil {
		t.Fatalf("GetTask after node settle: %v", err)
	}
	if task.Status != StatusWaitingUser {
		t.Fatalf("status after node turn = %q, want %q", task.Status, StatusWaitingUser)
	}

	// 与任务无关的会话不该碰任务头。
	if f.svc.BeginOutOfBandSessionTurn(ctx, f.rootID, "sess-unrelated") {
		t.Fatal("unrelated session was claimed as an out-of-band task turn")
	}
}

func TestTaskCurrentSessionFallsBackToLatestRelatedSession(t *testing.T) {
	ctx := context.Background()
	f := newTurnSettledFixture(t, "task-current-session", "")

	// 完全没有会话时仍然是空。
	task, err := f.store.GetTask(ctx, f.taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.MainSessionKey != "" {
		t.Fatalf("main_session_key without sessions = %q, want empty", task.MainSessionKey)
	}

	store := NewActivityStore(f.store.db)
	now := time.Now().UTC()
	for i, item := range []struct {
		id, session string
		at          time.Time
	}{
		{id: "act-old", session: "sess-old", at: now.Add(-time.Hour)},
		{id: "act-new", session: "sess-new", at: now},
	} {
		if err := store.Insert(ctx, &TaskActivity{
			ID: item.id, TaskID: f.taskID, RootID: f.rootID,
			Type: ActivityTypeAgentReply, Role: RoleAgent, Body: "回复",
			SessionKey: item.session, CreatedAt: item.at, UpdatedAt: item.at.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("insert %s: %v", item.id, err)
		}
	}

	// 最近活跃的相关会话就是「当前会话」，面板和轮询都有落点。
	task, err = f.store.GetTask(ctx, f.taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.MainSessionKey != "sess-new" {
		t.Fatalf("current session = %q, want sess-new", task.MainSessionKey)
	}
	list, err := f.store.ListTasks(ctx, ListTasksOptions{})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(list) != 1 || list[0].MainSessionKey != "sess-new" {
		t.Fatalf("ListTasks current session = %#v, want sess-new", list)
	}

	// 有主会话时回退不生效：显式绑定的那个才是当前会话。
	if err := f.svc.SetTaskMainSessionKey(ctx, f.rootID, f.taskID, "sess-main"); err != nil {
		t.Fatalf("SetTaskMainSessionKey: %v", err)
	}
	task, err = f.store.GetTask(ctx, f.taskID)
	if err != nil {
		t.Fatalf("GetTask with main: %v", err)
	}
	if task.MainSessionKey != "sess-main" {
		t.Fatalf("explicit main session = %q, want sess-main", task.MainSessionKey)
	}
}
