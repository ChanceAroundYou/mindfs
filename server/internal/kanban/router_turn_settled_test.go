package kanban

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// W2：任务会话的回合结算事件。
//
// 问题：Router 只在「Run 结算 / 事件」时被唤醒。用户在任务详情里直接推进一次
// 会话（追问、委派、改口），那一轮结束没有任何事件，Router 既不总结也不跟进度
// （线上 #68 就是这样漂走的）。EndSessionTurn 是所有会话回合的唯一终结器，
// 由它发一条 turn_settled，Router 才有收拾这一轮的机会。

type turnSettledFixture struct {
	svc    *Service
	store  *TaskStore
	rootID string
	taskID string
	userID string
}

func newTurnSettledFixture(t *testing.T, taskID, mainSession string) *turnSettledFixture {
	t.Helper()
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("task store: %v", err)
	}
	if _, err := store.CreateTask(ctx,
		Task{ID: taskID, RootID: root.ID, Name: "会话跟进", Status: StatusWaitingUser, MainSessionKey: mainSession},
		TaskEvent{ID: taskID + "-created", TaskID: taskID, Type: "created"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	userID := taskID + "-user"
	user := &TaskActivity{
		ID: userID, TaskID: taskID, RootID: root.ID, Type: ActivityTypeUserMessage,
		Role: RoleUser, Body: "把 #68 那条会话的进度也跟上", Status: ActivityStatusCompleted,
		CreatedAt: time.Now().UTC().Add(-3 * time.Minute),
	}
	if err := NewActivityStore(store.db).Insert(ctx, user); err != nil {
		t.Fatalf("insert user activity: %v", err)
	}
	return &turnSettledFixture{svc: svc, store: store, rootID: root.ID, taskID: taskID, userID: userID}
}

func (f *turnSettledFixture) events(t *testing.T, kind RouterEventKind) []RouterEvent {
	t.Helper()
	events, err := NewRouterStore(f.store.db).ListEventsByTask(context.Background(), f.taskID)
	if err != nil {
		t.Fatalf("ListEventsByTask: %v", err)
	}
	var out []RouterEvent
	for _, ev := range events {
		if ev.Kind == kind {
			out = append(out, ev)
		}
	}
	return out
}

// 同一回合只产一条事件：EndSessionTurn 可能被重连、重放、重复调度命中多次。
func TestSettleSessionTurnEmitsOneEventPerTurn(t *testing.T) {
	ctx := context.Background()
	f := newTurnSettledFixture(t, "task-turn-once", "sess-main")
	in := TurnSettledInput{
		SessionKey: "sess-main",
		TurnID:     "turn-a",
		Excerpt:    "把会话列表的排序改成了按更新时间。",
		ToolCalls:  2,
	}
	inserted, err := f.svc.SettleSessionTurn(ctx, f.rootID, in)
	if err != nil {
		t.Fatalf("SettleSessionTurn: %v", err)
	}
	if !inserted {
		t.Fatal("first turn settlement did not insert an event")
	}
	inserted, err = f.svc.SettleSessionTurn(ctx, f.rootID, in)
	if err != nil {
		t.Fatalf("SettleSessionTurn (replay): %v", err)
	}
	if inserted {
		t.Fatal("replaying the same turn id inserted a second event")
	}
	events := f.events(t, RouterEventTurnSettled)
	if len(events) != 1 {
		t.Fatalf("turn_settled events = %d, want 1", len(events))
	}
	ev := events[0]
	if ev.UserActivityID != f.userID {
		t.Fatalf("event user activity = %q, want %q", ev.UserActivityID, f.userID)
	}
	if ev.TurnID == "" {
		t.Fatal("event has no deterministic turn id")
	}
	if got := routerEventPayloadString(ev.PayloadJSON, "excerpt"); !strings.Contains(got, "会话列表") {
		t.Fatalf("event excerpt = %q", got)
	}
	// tool_calls 是 Router 判断「这一轮有没有真干活」的唯一依据（turn_settled
	// 回合的最后一条 say 要靠它才允许升成任务结论），必须在载荷里。
	if got := routerEventPayloadInt(ev.PayloadJSON, "tool_calls"); got != 2 {
		t.Fatalf("event tool_calls = %d, want 2", got)
	}
}

// H2：失败回合的结算事件必须自带失败标记与原因。
//
// `has_output` 对失败回合也成立（错误说明本身就是输出），Router 拿到的旧载荷
// 与正常回合完全一样，于是把故障当成了「这一轮正常结束」。
func TestTurnSettledPayloadCarriesFailure(t *testing.T) {
	ctx := context.Background()
	f := newTurnSettledFixture(t, "task-turn-failed-payload", "sess-main")
	reason := strings.Repeat("远端在中途断开了这条 SSE 流。", 30) // 远超过上限，验证会截断
	inserted, err := f.svc.SettleSessionTurn(ctx, f.rootID, TurnSettledInput{
		SessionKey: "sess-main",
		TurnID:     "turn-failed-payload",
		Excerpt:    "Prompt is too long",
		Failed:     true,
		Error:      reason,
	})
	if err != nil || !inserted {
		t.Fatalf("SettleSessionTurn: inserted=%v err=%v", inserted, err)
	}
	events := f.events(t, RouterEventTurnSettled)
	if len(events) != 1 {
		t.Fatalf("turn_settled events = %d, want 1", len(events))
	}
	var payload struct {
		Failed *bool  `json:"failed"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal([]byte(events[0].PayloadJSON), &payload); err != nil {
		t.Fatalf("payload json: %v", err)
	}
	if payload.Failed == nil || !*payload.Failed {
		t.Fatalf("failed flag missing: %s", events[0].PayloadJSON)
	}
	if !strings.Contains(payload.Error, "远端在中途断开") {
		t.Fatalf("error text missing: %q", payload.Error)
	}
	if n := len([]rune(payload.Error)); n > turnSettledErrorRunes+1 {
		t.Fatalf("error keeps %d runes, want <= %d", n, turnSettledErrorRunes+1)
	}
	// brief 白名单要放行 failed，否则 Router 看到的是「载荷被截断」。
	brief := &SituationBrief{}
	event := events[0]
	if err := attachRouterEventBrief(ctx, f.store, brief, event); err != nil {
		t.Fatalf("attachRouterEventBrief: %v", err)
	}
	if got, _ := brief.Event.Payload["failed"].(bool); !got {
		t.Fatalf("brief dropped the failed flag: %#v", brief.Event.Payload)
	}
	if brief.Event.Truncated {
		t.Fatal("failed flag was treated as an unknown payload key")
	}
}

// 不是任务绑定的会话（普通聊天会话）不该产生任何任务事件。
func TestSettleSessionTurnSkipsNonTaskSession(t *testing.T) {
	ctx := context.Background()
	f := newTurnSettledFixture(t, "task-turn-other", "sess-main")
	inserted, err := f.svc.SettleSessionTurn(ctx, f.rootID, TurnSettledInput{
		SessionKey: "sess-unrelated", TurnID: "turn-x", Excerpt: "随便聊了两句。",
	})
	if err != nil {
		t.Fatalf("SettleSessionTurn: %v", err)
	}
	if inserted {
		t.Fatal("non-task session produced a task event")
	}
	if events := f.events(t, RouterEventTurnSettled); len(events) != 0 {
		t.Fatalf("turn_settled events = %d, want 0", len(events))
	}
}

// 任务绑定的会话不止主会话：节点会话、委派会话同样要能反查到任务。
func TestTaskIDForSessionFindsEveryBinding(t *testing.T) {
	ctx := context.Background()
	f := newTurnSettledFixture(t, "task-turn-bind", "sess-main")
	if got, err := f.store.TaskIDForSession(ctx, "sess-main"); err != nil || got != f.taskID {
		t.Fatalf("TaskIDForSession(main) = %q, %v", got, err)
	}
	act := &TaskActivity{
		ID: "act-node-session", TaskID: f.taskID, RootID: f.rootID, Type: ActivityTypeAgentReply,
		Role: RoleAgent, Body: "节点里干了一段活", Status: ActivityStatusCompleted,
		SessionKey: "sess-node", CreatedAt: time.Now().UTC(),
	}
	if err := NewActivityStore(f.store.db).Insert(ctx, act); err != nil {
		t.Fatalf("insert node activity: %v", err)
	}
	if got, err := f.store.TaskIDForSession(ctx, "sess-node"); err != nil || got != f.taskID {
		t.Fatalf("TaskIDForSession(node activity) = %q, %v", got, err)
	}
	if got, err := f.store.TaskIDForSession(ctx, "sess-nowhere"); err != nil || got != "" {
		t.Fatalf("TaskIDForSession(unknown) = %q, %v", got, err)
	}
}

// 结算回合不计入用户留言的 3 回合预算：否则用户问一句、会话答一轮，
// Router 就被自己的收尾动作挤掉了预算。
func TestTurnSettledExcludedFromUserTurnBudget(t *testing.T) {
	ctx := context.Background()
	f := newTurnSettledFixture(t, "task-turn-budget", "sess-main")
	decisions := NewRouterStore(f.store.db)
	for i, turn := range []string{"turn-1", "turn-2", "turn-3"} {
		if err := decisions.InsertDecision(ctx, &RouterDecision{
			ID:             "dec-" + turn,
			TaskID:         f.taskID,
			RootID:         f.rootID,
			TurnID:         turn,
			UserActivityID: f.userID,
			TriggerKind:    RouterTriggerTurnSettled,
			Status:         RouterDecisionApplied,
			CreatedAt:      time.Now().UTC().Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("InsertDecision(%s): %v", turn, err)
		}
	}
	used, err := countUserTurnDecisions(ctx, f.store.db, f.taskID, f.userID)
	if err != nil {
		t.Fatalf("countUserTurnDecisions: %v", err)
	}
	if used != 0 {
		t.Fatalf("turns used = %d, want 0 (settlement turns must not consume the user budget)", used)
	}
}

// 结算回合自己也要有上限：每个任务每小时最多 20 次，超了要能说出原因。
func TestSettlementTurnBudgetCapsPerHour(t *testing.T) {
	ctx := context.Background()
	f := newTurnSettledFixture(t, "task-turn-cap", "sess-main")
	now := time.Now().UTC()
	decisions := NewRouterStore(f.store.db)
	for i := 0; i < routerSettlementTurnsPerHour; i++ {
		turn := "turn-cap-" + string(rune('a'+i))
		if err := decisions.InsertDecision(ctx, &RouterDecision{
			ID:             "dec-" + turn,
			TaskID:         f.taskID,
			RootID:         f.rootID,
			TurnID:         turn,
			UserActivityID: f.userID,
			TriggerKind:    RouterTriggerTurnSettled,
			Status:         RouterDecisionApplied,
			CreatedAt:      now.Add(-time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatalf("InsertDecision(%s): %v", turn, err)
		}
	}
	allowed, reason, err := settlementTurnBudget(ctx, f.store, f.taskID, now)
	if err != nil {
		t.Fatalf("settlementTurnBudget: %v", err)
	}
	if allowed {
		t.Fatal("budget allowed a 21st settlement turn in the same hour")
	}
	if !strings.Contains(reason, "20") {
		t.Fatalf("skip reason does not name the limit: %q", reason)
	}
	// 一小时以外的旧回合不占本次额度。
	allowed, _, err = settlementTurnBudget(ctx, f.store, f.taskID, now.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("settlementTurnBudget(+2h): %v", err)
	}
	if !allowed {
		t.Fatal("old settlement turns still consume the hourly budget")
	}
}

// brief 要把这一轮的产出摘要交给 Router，同时不能给它一条「run 不存在」的
// 假未验证项 —— 会话回合本来就没有 Run。
func TestTurnSettledBriefCarriesExcerptWithoutRunNoise(t *testing.T) {
	ctx := context.Background()
	f := newTurnSettledFixture(t, "task-turn-brief", "sess-main")
	excerpt := strings.Repeat("这一轮把会话列表的排序改成了按更新时间。", 40)
	payload := `{"session_key":"sess-main","turn_id":"turn-b","duration_ms":42000,` +
		`"has_output":true,"out_of_band":false,"answered_question":false,"excerpt":` + quoteJSON(excerpt) + `}`
	event := RouterEvent{
		ID: "revt-turn-b", TaskID: f.taskID, RootID: f.rootID, Kind: RouterEventTurnSettled,
		UniqueKey: "turn-b", UserActivityID: f.userID, PayloadJSON: payload,
	}
	brief := &SituationBrief{}
	if err := attachRouterEventBrief(ctx, f.store, brief, event); err != nil {
		t.Fatalf("attachRouterEventBrief: %v", err)
	}
	if brief.Evidence == nil {
		t.Fatal("no evidence block")
	}
	if brief.Event.Excerpt == "" {
		t.Fatal("brief lost the turn excerpt")
	}
	if n := len([]rune(brief.Event.Excerpt)); n > routerEventBriefExcerptRunes {
		t.Fatalf("excerpt keeps %d runes, want <= %d", n, routerEventBriefExcerptRunes)
	}
	if brief.Evidence.RunStatus == "missing" {
		t.Fatal("turn settlement was reported as a missing run")
	}
	for _, item := range brief.Evidence.Unverified {
		if strings.Contains(item, "run") && strings.Contains(item, "not found") {
			t.Fatalf("misleading unverified item: %q", item)
		}
	}
	if got := routerEventPayloadValue(brief.Event.Payload, "duration_ms"); got == nil {
		t.Fatal("turn payload keys were dropped from the brief")
	}
}

// 结算回合可以汇报、提问、静默、通知用户；交出新执行仍然不允许。
func TestTurnSettledAllowsNotifyUserButRejectsHandoff(t *testing.T) {
	brief := &SituationBrief{Event: &RouterBriefEvent{Kind: string(RouterEventTurnSettled)}}
	allowed := []RouterAction{
		{Seq: 0, Kind: RouterActionSay, Purpose: "summary", Body: "本轮改完了三处。"},
		{Seq: 1, Kind: RouterActionNotifyUser, Body: "会话已停在一个失败上。"},
		{Seq: 2, Kind: RouterActionSilent},
	}
	if err := validateRuntimeEventActions(brief, allowed, RouterConfig{}); err != nil {
		t.Fatalf("settlement actions rejected: %v", err)
	}
	rejected := []RouterAction{{Seq: 0, Kind: RouterActionRunWorkflow}}
	if err := validateRuntimeEventActions(brief, rejected, RouterConfig{}); err == nil {
		t.Fatal("run_workflow was allowed for a settlement turn")
	}
}

func quoteJSON(text string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range text {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// routerEventPayloadValue 读 brief 里 payload 的某个键（只用于断言）。
func routerEventPayloadValue(payload map[string]any, key string) any {
	if payload == nil {
		return nil
	}
	return payload[key]
}

// 事件必须映射成一个 Router 认识的触发类型，否则 coordinate 会把它当未知 kind
// 直接 skipped，事件写得再准也没人处理。
func TestTurnSettledEventMapsToSettlementTrigger(t *testing.T) {
	event := RouterEvent{
		ID: "revt-map", TaskID: "task-map", RootID: "root", Kind: RouterEventTurnSettled,
		TurnID: "turn-map", UserActivityID: "act-map",
	}
	trigger, kind := routerTriggerForEvent(event)
	if kind != RouterTriggerTurnSettled {
		t.Fatalf("trigger kind = %q, want %q", kind, RouterTriggerTurnSettled)
	}
	if trigger.TriggerRef != "turn-map" {
		t.Fatalf("trigger ref = %q, want the turn id", trigger.TriggerRef)
	}
	if trigger.Event == nil || trigger.Event.Kind != RouterEventTurnSettled {
		t.Fatal("trigger lost the event body")
	}
}

// 用户留言的 3 回合额度用尽后，会话回合结算仍要能开：它花的是自己的每小时额度。
func TestSettlementTurnIgnoresExhaustedUserTurnBudget(t *testing.T) {
	ctx := context.Background()
	f := newTurnSettledFixture(t, "task-turn-gate", "sess-main")
	decisions := NewRouterStore(f.store.db)
	for _, turn := range []string{"u1", "u2", "u3"} {
		if err := decisions.InsertDecision(ctx, &RouterDecision{
			ID: "dec-" + turn, TaskID: f.taskID, RootID: f.rootID, TurnID: turn,
			UserActivityID: f.userID, TriggerKind: RouterTriggerUserMessage,
			Status: RouterDecisionApplied, CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("InsertDecision(%s): %v", turn, err)
		}
	}
	reason, err := eventTurnBudgetSkip(ctx, f.store, RouterEvent{
		Kind: RouterEventTurnSettled, TaskID: f.taskID,
	}, f.userID)
	if err != nil {
		t.Fatalf("eventTurnBudgetSkip: %v", err)
	}
	if reason != "" {
		t.Fatalf("settlement turn was skipped by the user budget: %q", reason)
	}
	// 对照：普通事件仍会被 3 回合额度挡住。
	reason, err = eventTurnBudgetSkip(ctx, f.store, RouterEvent{
		Kind: RouterEventRunFailed, TaskID: f.taskID,
	}, f.userID)
	if err != nil {
		t.Fatalf("eventTurnBudgetSkip(run_failed): %v", err)
	}
	if !strings.Contains(reason, "turn budget exhausted") {
		t.Fatalf("run_failed was not budget-limited: %q", reason)
	}
}
