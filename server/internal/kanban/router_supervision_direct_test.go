package kanban

import (
	"context"
	"strings"
	"testing"
	"time"
)

// S3-3 直连回合监管：直连回合没有 Run，收尾写在跑它的 goroutine 里，进程活着
// 也可能因为回合被吞掉而永远停在 running。扫描从「run_id 为空、仍停在
// pending/running 的回复行」产出 direct_stalled，监管回合可以结算旧回复并重开
// 一轮；同一轮直连只重开一次，第二次走预算停手路径。

// directStalledFixture 造一条停住的直连回合：用户留言 + 两分钟前创建、仍在
// running 的回复行（run_id 为空），任务主会话键 sess-main。
func directStalledFixture(t *testing.T, mode RouterSupervisionMode) (*coordinateFixture, time.Time) {
	t.Helper()
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.svc.SetRouterConfig(RouterConfig{
		Mode:        RouterModeCoordinate,
		IdleClose:   time.Hour,
		Supervision: RouterSupervisionConfig{Mode: mode},
	})
	f.stopLoop(t)
	f.svc.SetSessionRunningProbe(func(key string) bool { return key == "sess-busy" })
	ctx := context.Background()
	must(t, f.svc.SetTaskMainSessionKey(ctx, f.root.ID, "task-1", "sess-main"), "SetTaskMainSessionKey")
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	acts := NewActivityStore(f.store.db)
	must(t, acts.Insert(ctx, &TaskActivity{
		ID: "uact-direct", TaskID: "task-1", RootID: f.root.ID,
		Type: ActivityTypeUserMessage, Author: "user", Role: RoleUser, Body: "修复登录",
		CreatedAt: now.Add(-3 * time.Minute),
	}), "insert user activity")
	must(t, acts.Insert(ctx, &TaskActivity{
		ID: "act-stalled", TaskID: "task-1", RootID: f.root.ID,
		Type: ActivityTypeAgentReply, Role: RoleAgent, Status: ActivityStatusRunning,
		ReplyToID: "uact-direct", CreatedAt: now.Add(-2 * time.Minute),
	}), "insert stalled reply")
	return f, now
}

// insertDirectStalledEvent 直接落一条 direct_stalled 事件（不走 addEvent：那
// 个 helper 会给空 RunID 补一个假 Run）。
func (f *coordinateFixture) insertDirectStalledEvent(t *testing.T, turnID string) RouterEvent {
	t.Helper()
	ev, _, err := f.events.InsertEvent(context.Background(), RouterEvent{
		RootID: f.root.ID, TaskID: "task-1", Kind: RouterEventDirectStalled,
		InstanceKey: "act-stalled:" + turnID, TurnID: turnID,
		UserActivityID: "uact-direct",
		PayloadJSON:    `{"task_id":"task-1","activity_id":"act-stalled","status":"running"}`,
	})
	must(t, err, "InsertEvent(direct_stalled)")
	return ev
}

func (f *coordinateFixture) consumeEvents(t *testing.T) {
	t.Helper()
	if _, err := f.router.ConsumeRouterEvents(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("ConsumeRouterEvents: %v", err)
	}
}

func (f *coordinateFixture) activityByID(t *testing.T, id string) *TaskActivity {
	t.Helper()
	act, err := NewActivityStore(f.store.db).Get(context.Background(), id)
	must(t, err, "load activity %s", id)
	return act
}

// 行为：一次 inactive 观测不够；相隔 ≥30s 的第二次观测才产出 direct_stalled，
// 且同一 5 分钟桶内重复扫描不再新增。事件不带 Run，但必须带原始留言与回复行。
func TestDirectStalledScanRequiresTwoIdleObservations(t *testing.T) {
	f, now := directStalledFixture(t, RouterSupervisionOn)

	if got := countKind(f.scan(t, now).Produced, RouterEventDirectStalled); got != 0 {
		t.Fatalf("first observation produced %d direct_stalled, want 0", got)
	}
	if got := countKind(f.scan(t, now.Add(31*time.Second)).Produced, RouterEventDirectStalled); got != 1 {
		t.Fatalf("second observation produced %d direct_stalled, want 1", got)
	}
	f.scan(t, now.Add(32*time.Second))

	events := f.list(t, "task-1")
	if got := countKind(events, RouterEventDirectStalled); got != 1 {
		t.Fatalf("direct_stalled rows = %d, want 1 (bucket dedupe)", got)
	}
	var ev RouterEvent
	for _, candidate := range events {
		if candidate.Kind == RouterEventDirectStalled {
			ev = candidate
		}
	}
	if ev.RunID != "" {
		t.Fatalf("direct_stalled run id = %q, want empty", ev.RunID)
	}
	if ev.UserActivityID != "uact-direct" {
		t.Fatalf("direct_stalled user activity = %q, want uact-direct", ev.UserActivityID)
	}
	for _, want := range []string{`"activity_id":"act-stalled"`, `"supervision"`} {
		if !strings.Contains(ev.PayloadJSON, want) {
			t.Fatalf("payload %s missing %s", ev.PayloadJSON, want)
		}
	}
	// 事件载荷的既有约束：不泄漏会话键/转录。
	if strings.Contains(strings.ToLower(ev.PayloadJSON), "session") {
		t.Fatalf("payload leaks a session key: %s", ev.PayloadJSON)
	}
}

// 行为：主会话还在回复时不算停摆 —— 观测被清掉，等它空闲再从头数两轮。
func TestDirectStalledScanSkipsReplyingSession(t *testing.T) {
	f, now := directStalledFixture(t, RouterSupervisionOn)
	f.svc.SetSessionRunningProbe(func(key string) bool { return key == "sess-main" })

	f.scan(t, now)
	if got := countKind(f.scan(t, now.Add(31*time.Second)).Produced, RouterEventDirectStalled); got != 0 {
		t.Fatalf("replying session produced %d direct_stalled, want 0", got)
	}
	if got := countKind(f.list(t, "task-1"), RouterEventDirectStalled); got != 0 {
		t.Fatalf("direct_stalled rows = %d, want 0", got)
	}
}

// 行为：on 下 retry_direct 结算旧回复（cancelled）并用确定性新行重开一轮；
// 新回合拿到的是原始用户留言。
func TestSupervisionRetryDirectRestartsTurn(t *testing.T) {
	f, _ := directStalledFixture(t, RouterSupervisionOn)
	f.agent.calls = []scriptedRouterCall{
		{tool: "router_retry_direct", args: map[string]any{"reason": "会话已空闲"}},
	}
	ev := f.insertDirectStalledEvent(t, "direct-retry-turn")
	f.consumeEvents(t)
	f.waitEvent(t, ev.ID, RouterEventDone)

	decision, err := NewRouterStore(f.store.db).DecisionByTurn(context.Background(), "task-1", "direct-retry-turn")
	must(t, err, "DecisionByTurn")
	if decision.Status != RouterDecisionApplied {
		t.Fatalf("decision status = %q, want applied (err=%s)", decision.Status, decision.Error)
	}
	old := f.activityByID(t, "act-stalled")
	if old.Status != ActivityStatusCancelled {
		t.Fatalf("stalled reply status = %q, want cancelled", old.Status)
	}
	if strings.TrimSpace(old.Summary) == "" {
		t.Fatal("stalled reply settled without a readable summary")
	}

	retryID := "direct-retry:" + decision.ID
	var retry *TaskActivity
	waitForCondition(t, func() bool {
		act, loadErr := NewActivityStore(f.store.db).Get(context.Background(), retryID)
		if loadErr != nil {
			return false
		}
		retry = act
		return act.Status == ActivityStatusCompleted
	})
	if retry.RunID != "" {
		t.Fatalf("retry reply run id = %q, want empty (direct turn)", retry.RunID)
	}
	if retry.Body != "直连回复" {
		t.Fatalf("retry reply body = %q, want the direct executor reply", retry.Body)
	}
	select {
	case <-f.direct.done:
	default:
		t.Fatal("direct executor was never invoked")
	}
	if f.direct.got != "修复登录" {
		t.Fatalf("retry ran with body %q, want the original user message", f.direct.got)
	}
	waitRuntimeIdle(t, f.svc, f.root.ID, "task-1")
}

// 行为：决策时会话又忙起来了 —— 校验拒绝，旧回复不动，不产生新回合。
func TestSupervisionRetryDirectRefusedWhileReplying(t *testing.T) {
	f, _ := directStalledFixture(t, RouterSupervisionOn)
	f.svc.SetSessionRunningProbe(func(key string) bool { return key == "sess-main" })
	f.agent.calls = []scriptedRouterCall{
		{tool: "router_retry_direct", args: map[string]any{"reason": "试试"}},
	}
	ev := f.insertDirectStalledEvent(t, "direct-busy-turn")
	f.consumeEvents(t)
	got := f.waitEvent(t, ev.ID, RouterEventFailed)
	if !strings.Contains(got.LastError, "replying") {
		t.Fatalf("event reason = %q, want mention of replying", got.LastError)
	}
	if old := f.activityByID(t, "act-stalled"); old.Status != ActivityStatusRunning {
		t.Fatalf("stalled reply status = %q, want untouched running", old.Status)
	}
	select {
	case <-f.direct.done:
		t.Fatal("direct executor ran despite the busy session")
	default:
	}
}

// 行为：同一轮直连已经重开过一次 → 预算用尽不是模型犯错，任务停在
// waiting_user 并留一条可见系统说明，不再产生第二次重开。
func TestSupervisionRetryDirectBudgetExhaustedStopsAtWaitingUser(t *testing.T) {
	f, _ := directStalledFixture(t, RouterSupervisionOn)
	must(t, NewRouterStore(f.store.db).InsertDecision(context.Background(), &RouterDecision{
		ID: "rdec-prior", TaskID: "task-1", RootID: f.root.ID, Scope: RouterScopeTask, OwnerID: "task-1",
		UserActivityID: "uact-direct", TriggerKind: RouterTriggerDirectStalled, TurnID: "prior-direct-retry",
		Actions: []RouterAction{{Seq: 0, Kind: RouterActionRetryDirect, NodeRunID: "act-stalled"}},
		Status:  RouterDecisionApplied,
	}), "InsertDecision(prior retry)")
	f.agent.calls = []scriptedRouterCall{
		{tool: "router_retry_direct", args: map[string]any{"reason": "再试一次"}},
	}
	ev := f.insertDirectStalledEvent(t, "direct-budget-turn")
	f.consumeEvents(t)
	got := f.waitEvent(t, ev.ID, RouterEventDone)
	if !strings.Contains(got.LastError, "supervision_budget_exhausted") {
		t.Fatalf("event reason = %q, want supervision_budget_exhausted", got.LastError)
	}
	decision, err := NewRouterStore(f.store.db).DecisionByTurn(context.Background(), "task-1", "direct-budget-turn")
	must(t, err, "DecisionByTurn")
	if _, loadErr := NewActivityStore(f.store.db).Get(context.Background(), "direct-retry:"+decision.ID); loadErr == nil {
		t.Fatal("budget-exhausted turn still opened a retry reply")
	}
	if old := f.activityByID(t, "act-stalled"); old.Status != ActivityStatusRunning {
		t.Fatalf("stalled reply status = %q, want untouched running", old.Status)
	}
	if task := f.task(t, "task-1"); task.Status != StatusWaitingUser {
		t.Fatalf("task status = %q, want waiting_user", task.Status)
	}
	acts, err := NewActivityStore(f.store.db).List(context.Background(), "task-1")
	must(t, err, "List activities")
	found := false
	for _, act := range acts {
		if act.Role == "system" && strings.Contains(act.Body, "监管") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no visible system row explaining the exhausted budget: %+v", acts)
	}
	select {
	case <-f.direct.done:
		t.Fatal("budget-exhausted turn still ran a direct turn")
	default:
	}
}

// 行为：工具按观测块的形状分组 —— 直连回合只给 retry_direct，Run 回合只给
// retry_node；两者都有 notify/escalate。retry_direct 记下的是回复行 ID。
func TestSupervisionDirectToolsOnlyWithDirectBlock(t *testing.T) {
	names := func(env *routerToolEnv) map[string]bool {
		out := map[string]bool{}
		for _, tool := range env.tools() {
			out[tool.Name] = true
		}
		return out
	}
	runTurn := names(&routerToolEnv{brief: &SituationBrief{
		Task:        RouterBriefTask{ID: "task-1"},
		Supervision: &SupervisionObservation{Mode: RouterSupervisionOn, RunID: "run-1"},
	}})
	if runTurn["router_retry_direct"] {
		t.Fatalf("run turn exposes router_retry_direct: %v", runTurn)
	}
	if !runTurn["router_retry_node"] || !runTurn["router_notify_user"] || !runTurn["router_escalate"] {
		t.Fatalf("run turn tools = %v", runTurn)
	}

	direct := &routerToolEnv{
		brief: &SituationBrief{
			Task: RouterBriefTask{ID: "task-1"},
			Supervision: &SupervisionObservation{Mode: RouterSupervisionOn, Direct: &SupervisionDirectState{
				ActivityID: "act-stalled", Status: ActivityStatusRunning, RetryAllowed: true,
			}},
		},
		collector: newRouterTurnCollector(RouterConfig{MaxActions: 6}),
	}
	directNames := names(direct)
	if !directNames["router_retry_direct"] || !directNames["router_notify_user"] || !directNames["router_escalate"] {
		t.Fatalf("direct turn tools = %v", directNames)
	}
	if directNames["router_retry_node"] || directNames["router_resume_run"] {
		t.Fatalf("direct turn exposes run-only tools: %v", directNames)
	}
	for _, tool := range direct.tools() {
		if tool.Name != "router_retry_direct" {
			continue
		}
		if _, err := tool.Handler(context.Background(), []byte(`{"reason":"空闲"}`)); err != nil {
			t.Fatalf("router_retry_direct handler: %v", err)
		}
	}
	actions := direct.collector.snapshot()
	if len(actions) != 1 {
		t.Fatalf("recorded actions = %+v, want one", actions)
	}
	if actions[0].Kind != RouterActionRetryDirect || actions[0].NodeRunID != "act-stalled" {
		t.Fatalf("recorded action = %+v, want retry_direct on act-stalled", actions[0])
	}
}

// 行为：事件是排队时的事实。轮到消费时旧回复已经被别的路径结算（例如启动对账
// 或用户手动重开），就不能再重开一轮——事件标 skipped，不调模型。
func TestDirectStalledEventSkippedWhenReplyAlreadySettled(t *testing.T) {
	f, _ := directStalledFixture(t, RouterSupervisionOn)
	must(t, NewActivityStore(f.store.db).UpdateStatus(context.Background(), "act-stalled", ActivityStatusCompleted, nil),
		"settle reply")
	ev := f.insertDirectStalledEvent(t, "direct-stale-turn")
	f.consumeEvents(t)
	got := f.waitEvent(t, ev.ID, RouterEventSkipped)
	if !strings.Contains(got.LastError, "no longer stalled") {
		t.Fatalf("event reason = %q, want no longer stalled", got.LastError)
	}
	if len(f.agent.calls) != 0 {
		t.Fatalf("router model was called for a stale event: %+v", f.agent.calls)
	}
}

// 行为：诊断快照透出监管模式与最近用量——「机器会不会自己动手」不该靠推断。
func TestRouterStatusExposesSupervisionUsage(t *testing.T) {
	f, _ := directStalledFixture(t, RouterSupervisionOn)
	now := time.Now().UTC()
	insert := func(id string, kind RouterTriggerKind, at time.Time, actions []RouterAction) {
		t.Helper()
		must(t, NewRouterStore(f.store.db).InsertDecision(context.Background(), &RouterDecision{
			ID: id, TaskID: "task-1", RootID: f.root.ID, Scope: RouterScopeTask, OwnerID: "task-1",
			TriggerKind: kind, TurnID: "turn-" + id, Actions: actions,
			Status: RouterDecisionApplied, CreatedAt: at,
		}), "InsertDecision(%s)", id)
	}
	insert("rdec-now", RouterTriggerDirectStalled, now.Add(-time.Minute),
		[]RouterAction{{Seq: 0, Kind: RouterActionRetryDirect, NodeRunID: "act-stalled"}})
	insert("rdec-old", RouterTriggerRunFailed, now.Add(-3*time.Hour), nil)

	status := f.svc.RouterStatusForRoot(f.root.ID)
	if status.SupervisionMode != RouterSupervisionOn {
		t.Fatalf("supervision mode = %q, want on", status.SupervisionMode)
	}
	if status.SupervisionActionsLastHour != 1 {
		t.Fatalf("actions last hour = %d, want 1", status.SupervisionActionsLastHour)
	}
	if status.SupervisionDecisionCount != 2 {
		t.Fatalf("recent supervision decisions = %d, want 2", status.SupervisionDecisionCount)
	}
}
