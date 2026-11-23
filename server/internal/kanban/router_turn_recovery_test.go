package kanban

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// W3-3：重启恢复与停滞的监管动作（Plan 2026-11-22）。
//
// W3-2 的启动对账与巡检只负责把现场写成事件；这一层决定 Router 能不能在上面
// 动手。用户裁定「重启后可以重试至多 3 次」：一次重启常常只是把好回合打断，
// 值得多给几次，但必须封顶，否则模型反复重开会变成死循环。

// turnInterruptedFixture 造一条「上一进程死在回合中间」的现场：用户留言 + 旧回复
// 仍停在 running，任务 running，没有任何活会话。
//
// 锚点取调用时刻而不是固定日期：重启重开的判据里有「中断后 24 小时内才允许重试」，
// 而决策时重算监管块的时钟是真实的 time.Now()。固定成 2026-11-22T12:00Z 时，
// 现场在 2026-11-23T12:00Z 之后对代码来说就已经「过期 26 小时」，retry_allowed
// 永远 false —— 两条用例正是这样在第二天集体变红的。
func turnInterruptedFixture(t *testing.T, mode RouterSupervisionMode) (*coordinateFixture, time.Time) {
	t.Helper()
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.svc.SetRouterConfig(RouterConfig{
		Mode:        RouterModeCoordinate,
		IdleClose:   time.Hour,
		Supervision: RouterSupervisionConfig{Mode: mode},
	})
	f.stopLoop(t)
	f.svc.SetSessionRunningProbe(func(string) bool { return false })
	ctx := context.Background()
	now := time.Now().UTC()
	acts := NewActivityStore(f.store.db)
	must(t, acts.Insert(ctx, &TaskActivity{
		ID: "uact-direct", TaskID: "task-1", RootID: f.root.ID,
		Type: ActivityTypeUserMessage, Author: "user", Role: RoleUser, Body: "修复登录",
		CreatedAt: now.Add(-40 * time.Minute),
	}), "insert user activity")
	must(t, acts.Insert(ctx, &TaskActivity{
		ID: "act-stalled", TaskID: "task-1", RootID: f.root.ID,
		Type: ActivityTypeAgentReply, Role: RoleAgent, Status: ActivityStatusRunning,
		ReplyToID: "uact-direct", SessionKey: "sess-main", CreatedAt: now.Add(-30 * time.Minute),
	}), "insert stalled reply")
	return f, now
}

func turnInterruptedEvents(t *testing.T, f *coordinateFixture) []RouterEvent {
	t.Helper()
	events, err := f.events.ListEventsByTask(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("ListEventsByTask: %v", err)
	}
	var out []RouterEvent
	for _, ev := range events {
		if ev.Kind == RouterEventTurnInterrupted {
			out = append(out, ev)
		}
	}
	return out
}

// 行为：启动对账结算被重启带走的直连回复时，必须同时落一条 turn_interrupted。
// 只结算不告诉 Router，等于安静地把这一轮丢了 —— 任务回到 waiting_user，用户
// 看到的是「跑了一半就没了」，而没有任何东西会去补。
func TestSettleOrphanDirectReplyEmitsTurnInterruptedEvent(t *testing.T) {
	f, _ := turnInterruptedFixture(t, RouterSupervisionOn)
	ctx := context.Background()

	f.svc.settleOrphanDirectReplies(ctx, f.store)

	old, err := NewActivityStore(f.store.db).Get(ctx, "act-stalled")
	if err != nil || old == nil {
		t.Fatalf("read settled reply: %v", err)
	}
	if old.Status != ActivityStatusCancelled {
		t.Fatalf("old reply status = %q, want cancelled", old.Status)
	}
	events := turnInterruptedEvents(t, f)
	if len(events) != 1 {
		t.Fatalf("turn_interrupted events = %d, want 1", len(events))
	}
	ev := events[0]
	if ev.UserActivityID != "uact-direct" {
		t.Fatalf("user activity = %q, want the message that started the turn", ev.UserActivityID)
	}
	for _, want := range []string{`"activity_id":"act-stalled"`, `"status":"cancelled"`} {
		if !strings.Contains(ev.PayloadJSON, want) {
			t.Fatalf("payload %s missing %s", ev.PayloadJSON, want)
		}
	}

	// 同一个现场再对账一次（第二次启动不会再结算任何行）：实例键幂等，不重复落。
	f.svc.settleOrphanDirectReplies(ctx, f.store)
	if got := len(turnInterruptedEvents(t, f)); got != 1 {
		t.Fatalf("turn_interrupted events after second reconcile = %d, want 1", got)
	}
}

// 行为：一条 turn_interrupted 走完整条链路 —— 领取、决策、重开。新回合拿到的
// 是原始用户留言，旧回复保持 cancelled。这正是「重启后不会主动恢复」要补的那一环。
func TestTurnInterruptedEventRestartsTurn(t *testing.T) {
	f, now := turnInterruptedFixture(t, RouterSupervisionOn)
	ctx := context.Background()
	f.svc.settleOrphanDirectReplies(ctx, f.store)
	events := turnInterruptedEvents(t, f)
	if len(events) != 1 {
		t.Fatalf("turn_interrupted events = %d, want 1", len(events))
	}
	f.agent.calls = []scriptedRouterCall{
		{tool: "router_retry_direct", args: map[string]any{"reason": "重启把这一轮带走了"}},
	}
	f.consumeEventsAt(t, now)
	f.waitEvent(t, events[0].ID, RouterEventDone)

	decision, err := NewRouterStore(f.store.db).DecisionByTurn(ctx, "task-1", events[0].TurnID)
	must(t, err, "DecisionByTurn")
	if decision.Status != RouterDecisionApplied {
		t.Fatalf("decision status = %q, want applied (err=%s)", decision.Status, decision.Error)
	}
	retryID := "direct-retry:" + decision.ID
	var retry *TaskActivity
	waitForCondition(t, func() bool {
		act, loadErr := NewActivityStore(f.store.db).Get(ctx, retryID)
		if loadErr != nil {
			return false
		}
		retry = act
		return act.Status == ActivityStatusCompleted
	})
	if retry.RunID != "" {
		t.Fatalf("retry reply run id = %q, want empty (direct turn)", retry.RunID)
	}
	if f.direct.got != "修复登录" {
		t.Fatalf("retry ran with body %q, want the original user message", f.direct.got)
	}
	if old := f.activityByID(t, "act-stalled"); old.Status != ActivityStatusCancelled {
		t.Fatalf("old reply status = %q, want cancelled", old.Status)
	}
	waitRuntimeIdle(t, f.svc, f.root.ID, "task-1")
}

// 行为：turn_interrupted 的监管块认得出「这一轮已经被重启结算」，于是重开不需要
// 「旧回复仍停在 pending/running」这个前提 —— 那条回复永远不会再有进展了。
func TestHealthSupervisionInterruptedAllowsRetryOfSettledReply(t *testing.T) {
	f, now := turnInterruptedFixture(t, RouterSupervisionOn)
	ctx := context.Background()
	f.svc.settleOrphanDirectReplies(ctx, f.store)
	events := turnInterruptedEvents(t, f)
	if len(events) != 1 {
		t.Fatalf("turn_interrupted events = %d, want 1", len(events))
	}

	task, err := f.store.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	obs := f.svc.healthEventSupervision(ctx, f.store, task, events[0], now)
	if obs == nil || obs.Direct == nil {
		t.Fatalf("observation = %+v, want a direct supervision block", obs)
	}
	if !obs.Direct.Interrupted {
		t.Fatal("observation did not mark the turn as interrupted")
	}
	if !obs.Direct.RetryAllowed {
		t.Fatalf("retry_allowed = false right after the first interruption: %+v", obs.Direct)
	}
	if obs.Direct.MaxRetries != supervisionInterruptedMaxRetries {
		t.Fatalf("max_retries = %d, want %d", obs.Direct.MaxRetries, supervisionInterruptedMaxRetries)
	}

	brief := &SituationBrief{Task: RouterBriefTask{ID: "task-1", Status: StatusWaitingUser}, Supervision: obs}
	action := RouterAction{Kind: RouterActionRetryDirect, NodeRunID: "act-stalled"}
	if err := validateSupervisionActions(brief, []RouterAction{action}, RouterConfig{Supervision: RouterSupervisionConfig{Mode: RouterSupervisionOn}}); err != nil {
		t.Fatalf("retry_direct rejected on an interrupted turn: %v", err)
	}
}

// 行为：自动重开有上限 —— 用户裁定「重启后可以重试至多 3 次」。第 3 次之后
// 观测必须自己把 retry_allowed 关掉，校验也必须拒绝，不能靠模型自觉。
func TestInterruptedRetryStopsAfterThree(t *testing.T) {
	f, now := turnInterruptedFixture(t, RouterSupervisionOn)
	ctx := context.Background()
	f.svc.settleOrphanDirectReplies(ctx, f.store)
	events := turnInterruptedEvents(t, f)
	if len(events) != 1 {
		t.Fatalf("turn_interrupted events = %d, want 1", len(events))
	}
	task, err := f.store.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	config := RouterConfig{Supervision: RouterSupervisionConfig{Mode: RouterSupervisionOn}}
	brief := func() *SituationBrief {
		obs := f.svc.healthEventSupervision(ctx, f.store, task, events[0], now)
		return &SituationBrief{Task: RouterBriefTask{ID: "task-1", Status: StatusWaitingUser}, Supervision: obs}
	}
	action := RouterAction{Kind: RouterActionRetryDirect, NodeRunID: "act-stalled"}

	for i := 0; i < supervisionInterruptedMaxRetries; i++ {
		if err := validateSupervisionActions(brief(), []RouterAction{action}, config); err != nil {
			t.Fatalf("retry %d rejected with only %d used: %v", i+1, i, err)
		}
		insertInterruptedRetryDecision(t, f, i)
	}
	obs := brief().Supervision
	if obs.Direct.RetriesUsed != supervisionInterruptedMaxRetries {
		t.Fatalf("retries_used = %d, want %d", obs.Direct.RetriesUsed, supervisionInterruptedMaxRetries)
	}
	if obs.Direct.RetryAllowed {
		t.Fatalf("retry_allowed still true after %d retries", obs.Direct.RetriesUsed)
	}
	err = validateSupervisionActions(brief(), []RouterAction{action}, config)
	var budget *supervisionBudgetError
	if !errors.As(err, &budget) {
		t.Fatalf("4th retry error = %v, want a budget error", err)
	}
}

// insertInterruptedRetryDecision 直接落一条已应用的 turn_interrupted 决策（只带
// retry_direct），模拟前几次自动重开已经发生。
func insertInterruptedRetryDecision(t *testing.T, f *coordinateFixture, seq int) {
	t.Helper()
	ctx := context.Background()
	raw := `[{"kind":"retry_direct","node_run_id":"act-stalled"}]`
	if _, err := f.store.db.ExecContext(ctx, `
INSERT INTO router_decisions (id, task_id, root_id, turn_id, user_activity_id, trigger_kind, status, actions_json, created_at, updated_at)
VALUES (?, 'task-1', ?, ?, 'uact-direct', ?, ?, ?, ?, ?)`,
		"dec-interrupt-"+string(rune('a'+seq)), f.root.ID, "turn-interrupt-"+string(rune('a'+seq)),
		string(RouterTriggerTurnInterrupted), string(RouterDecisionApplied), raw,
		"2026-11-22T11:00:00Z", "2026-11-22T11:00:00Z"); err != nil {
		t.Fatalf("insert interrupted decision: %v", err)
	}
}

// 行为：turn_stalled 的停滞证据来自输出水位，探针可能仍报「在回复」。这种时候
// 重开必须被允许 —— 探针能证明进程活着，不能证明有进展。
func TestHealthSupervisionStalledAllowsRetryWhileProbeReplies(t *testing.T) {
	f := newTaskHealthFixture(t, "task-stall", StatusRunning, "sess-stall")
	ctx := context.Background()
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	f.insertRunningReply(t, "act-stall", "sess-stall", now.Add(-30*time.Minute))
	f.svc.NoteSessionTurnOutput(ctx, f.rootID, "sess-stall", now.Add(-12*time.Minute))
	f.svc.SetRouterConfig(RouterConfig{Supervision: RouterSupervisionConfig{Mode: RouterSupervisionOn}})
	f.svc.SetSessionRunningProbe(func(key string) bool { return key == "sess-stall" })

	produced, err := f.svc.taskHealthSweep(ctx, f.rootID, now)
	if err != nil {
		t.Fatalf("taskHealthSweep: %v", err)
	}
	var stalled *RouterEvent
	for i := range produced {
		if produced[i].Kind == RouterEventTurnStalled {
			stalled = &produced[i]
		}
	}
	if stalled == nil {
		t.Fatalf("sweep produced %+v, want a turn_stalled event", produced)
	}
	task, err := f.store.GetTask(ctx, "task-stall")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	obs := f.svc.healthEventSupervision(ctx, f.store, task, *stalled, now)
	if obs == nil || obs.Direct == nil {
		t.Fatalf("observation = %+v, want a direct supervision block", obs)
	}
	if !obs.Direct.StalledByWatermark {
		t.Fatal("observation did not mark the stall as watermark-based")
	}
	if !obs.Direct.Replying {
		t.Fatal("fixture expected the probe to still report the session as replying")
	}
	brief := &SituationBrief{Task: RouterBriefTask{ID: "task-stall", Status: StatusRunning}, Supervision: obs}
	action := RouterAction{Kind: RouterActionRetryDirect, NodeRunID: "act-stall"}
	if err := validateSupervisionActions(brief, []RouterAction{action}, RouterConfig{Supervision: RouterSupervisionConfig{Mode: RouterSupervisionOn}}); err != nil {
		t.Fatalf("watermark stall cannot be retried while the probe still says replying: %v", err)
	}
}

// 行为：孤儿任务下面什么都没有，没有可重开的回复。可以通知/升级，不能 retry_direct。
func TestHealthSupervisionOrphanedAllowsNotifyOnly(t *testing.T) {
	f := newTaskHealthFixture(t, "task-orphan", StatusRunning, "sess-gone")
	ctx := context.Background()
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	f.svc.SetRouterConfig(RouterConfig{Supervision: RouterSupervisionConfig{Mode: RouterSupervisionOn}})

	produced, err := f.svc.taskHealthSweep(ctx, f.rootID, now)
	if err != nil {
		t.Fatalf("taskHealthSweep: %v", err)
	}
	var orphaned *RouterEvent
	for i := range produced {
		if produced[i].Kind == RouterEventTaskOrphaned {
			orphaned = &produced[i]
		}
	}
	if orphaned == nil {
		t.Fatalf("sweep produced %+v, want a task_orphaned event", produced)
	}
	task, err := f.store.GetTask(ctx, "task-orphan")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	obs := f.svc.healthEventSupervision(ctx, f.store, task, *orphaned, now)
	if obs == nil {
		t.Fatal("orphaned event got no supervision block: Router could only watch")
	}
	if obs.Direct != nil {
		t.Fatalf("orphaned observation carries a direct block: %+v", obs.Direct)
	}
	brief := &SituationBrief{Task: RouterBriefTask{ID: "task-orphan", Status: StatusRunning}, Supervision: obs}
	config := RouterConfig{Supervision: RouterSupervisionConfig{Mode: RouterSupervisionOn}}
	notify := RouterAction{Kind: RouterActionNotifyUser, Body: "任务写着在跑，但底下什么都没有了"}
	if err := validateSupervisionActions(brief, []RouterAction{notify}, config); err != nil {
		t.Fatalf("notify_user rejected on an orphaned task: %v", err)
	}
	retry := RouterAction{Kind: RouterActionRetryDirect, NodeRunID: "act-none"}
	if err := validateSupervisionActions(brief, []RouterAction{retry}, config); err == nil {
		t.Fatal("retry_direct accepted on an orphaned task with no reply row")
	}
}

// 行为：监管/恢复回合有自己的预算（重开次数 + 每小时动作数 + 链深度），不挤占
// 用户留言的 3 回合额度 —— 否则「入口 1 轮 + 3 次自动重开」第 3 次就被静默跳过。
func TestHealthEventsSkipUserTurnBudget(t *testing.T) {
	f := newTaskHealthFixture(t, "task-budget", StatusWaitingUser, "sess-budget")
	ctx := context.Background()
	for i := 0; i < RouterCoordinateMaxTurnsPerUserActivity; i++ {
		if _, err := f.store.db.ExecContext(ctx, `
INSERT INTO router_decisions (id, task_id, root_id, turn_id, trigger_kind, user_activity_id, status, actions_json, created_at, updated_at)
VALUES (?, 'task-budget', ?, ?, ?, 'uact-budget', ?, '[]', '2026-11-22T11:00:00Z', '2026-11-22T11:00:00Z')`,
			"dec-user-"+string(rune('a'+i)), f.rootID, "turn-user-"+string(rune('a'+i)), string(RouterTriggerUserMessage),
			string(RouterDecisionApplied)); err != nil {
			t.Fatalf("insert user decision: %v", err)
		}
	}
	for _, kind := range []RouterEventKind{RouterEventTurnInterrupted, RouterEventTurnStalled, RouterEventTaskOrphaned} {
		reason, err := eventTurnBudgetSkip(ctx, f.store,
			RouterEvent{Kind: kind, TaskID: "task-budget"}, "uact-budget")
		if err != nil {
			t.Fatalf("eventTurnBudgetSkip(%s): %v", kind, err)
		}
		if reason != "" {
			t.Fatalf("%s skipped by the user turn budget: %s", kind, reason)
		}
	}
}
