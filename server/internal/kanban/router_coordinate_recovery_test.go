package kanban

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// 并发与重启故障矩阵（Plan 2026-11-18，R1 T9）。
//
// 这里只覆盖 T7 已经钉住的三个窗口之外的恢复状态：
//   - 待领取事件跨重启存活（不丢事件、不重复汇报）；
//   - 已领取未执行的事件在重启路径上被回收（claimed 不会永久卡死）；
//   - 可见效果已写、完成游标未推进（确定 effect ID 是幂等闸门）；
//   - 同一事件并发领取只产生一次效果（CAS 承重）；
//   - 旧 Run 的迟到事件不改写当前 Run；
//   - 暂停/终态任务仍可收到汇报但不执行；
//   - 回合预算来自持久账本（不是内存计数）；
//   - 无法重放的决策不盲目重跑。
//
// 「进程重启」统一用同一份 SQLite 上的新 RouterService 实例模拟：构造本身就是
// 重启点（先 RecoverClaimedEvents，再起循环并立即跑一拍）。所以断言只写最终状态，
// 绝不断言「构造后事件仍是 pending」。构造期那一拍可能已经消费过事件。

// recoveryRouterAgent 是可计数、可阻塞的 Router agent：计数在锁内，阻塞由通道
// 控制。计数必须自带互斥——背景循环与手动消费并存时，裸读 scriptedRouterAgent
// 的切片会被 -race 抓到。
type recoveryRouterAgent struct {
	*scriptedRouterAgent
	mu      sync.Mutex
	calls   int
	release chan struct{} // 非 nil 时：进入回合后阻塞到关闭或 ctx 到期
}

func newRecoveryRouterAgent(script ...scriptedRouterCall) *recoveryRouterAgent {
	return &recoveryRouterAgent{scriptedRouterAgent: &scriptedRouterAgent{calls: script}}
}

func (a *recoveryRouterAgent) RunRouterTurn(ctx context.Context, req RouterAgentRequest) (RouterAgentResult, error) {
	a.mu.Lock()
	a.calls++
	release := a.release
	a.mu.Unlock()
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return RouterAgentResult{}, ctx.Err()
		}
	}
	return a.scriptedRouterAgent.RunRouterTurn(ctx, req)
}

func (a *recoveryRouterAgent) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

// restartRouter 模拟一次进程重启：不能走 svc.RouterServiceForRoot，它缓存旧实例。
// 构造期先回收上个进程遗留的 claimed 行，再起 30s 循环并立即跑一拍；立即 Close
// 是为了收回消费权，但那一拍可能已经消费过事件，所以调用方只断言最终状态。
func (f *coordinateFixture) restartRouter(t *testing.T) *RouterService {
	t.Helper()
	fresh := newRouterService(f.svc, f.root.ID, f.svc.RouterConfig(), f.store)
	if fresh == nil {
		t.Fatal("restart produced no router runtime")
	}
	fresh.Close()
	waitForCondition(t, func() bool { return !fresh.coordinateLoopActive() })
	t.Cleanup(fresh.Close)
	return fresh
}

// consumeEvents 手动驱动一拍消费。构造期那一拍已经消费过时它是无害的空转。
func consumeEvents(t *testing.T, router *RouterService) {
	t.Helper()
	if _, err := router.ConsumeRouterEvents(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("ConsumeRouterEvents: %v", err)
	}
}

// awaitEvent 等事件到达期望状态；超时时报出实际状态而不是一句 timeout——
// 恢复矩阵里的失败几乎都是「事件卡在 claimed/pending」，这条信息才是证据。
func awaitEvent(t *testing.T, f *coordinateFixture, eventID, want string) RouterEvent {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		ev := f.eventByID(t, "task-1", eventID)
		if ev.Status == want {
			return ev
		}
		if time.Now().After(deadline) {
			t.Fatalf("event %s stuck at status %q (claimed_by=%q last_error=%q), want %q",
				eventID, ev.Status, ev.ClaimedBy, ev.LastError, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// visibleReplies 数某条决策的可见汇报（默认列表已过滤 hidden 行）。
func visibleReplies(t *testing.T, f *coordinateFixture, decisionID string) []*TaskActivity {
	t.Helper()
	acts, err := NewActivityStore(f.store.db).List(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("List activities: %v", err)
	}
	out := make([]*TaskActivity, 0, 1)
	for _, act := range acts {
		if act.RouterDecisionID == decisionID {
			out = append(out, act)
		}
	}
	return out
}

func (f *coordinateFixture) runByID(t *testing.T, runID string) *WorkflowRun {
	t.Helper()
	run, err := f.store.WorkflowRunByID(context.Background(), runID)
	if err != nil {
		t.Fatalf("WorkflowRunByID(%s): %v", runID, err)
	}
	return run
}

// 行为 1：待领取事件跨重启存活。重启不丢 pending 事件，也不重复汇报。
func TestRecoveryPendingEventSurvivesRestart(t *testing.T) {
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.stopLoop(t)
	agent := newRecoveryRouterAgent(scriptedRouterCall{tool: "router_say", args: map[string]any{"body": "流程已完成，汇报一次"}})
	f.svc.SetRouterAgent(agent)
	user := f.addUserActivity(t, "uact-pending")
	ev := f.addEvent(t, RouterEvent{Kind: RouterEventRunSettled, TurnID: "pending-turn", UserActivityID: user})
	if got := f.eventByID(t, "task-1", ev.ID).Status; got != RouterEventPending {
		t.Fatalf("event status before restart = %q, want pending", got)
	}

	router := f.restartRouter(t)
	consumeEvents(t, router)
	awaitEvent(t, f, ev.ID, RouterEventDone)
	decision := f.waitDecision(t, "task-1", "pending-turn", RouterDecisionApplied)

	if calls := agent.count(); calls != 1 {
		t.Fatalf("model calls = %d, want exactly 1 across the restart", calls)
	}
	if replies := visibleReplies(t, f, decision.ID); len(replies) != 1 {
		t.Fatalf("visible reports = %d, want exactly 1: %#v", len(replies), replies)
	}
	if decisions := f.decisions(t, "task-1"); len(decisions) != 1 {
		t.Fatalf("decisions = %d, want exactly 1", len(decisions))
	}
}

// 行为 2：已领取未执行的事件跨重启回收。claimed 不会永久卡死，且只跑一次。
func TestRecoveryClaimedEventRequeuedAfterRestart(t *testing.T) {
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.stopLoop(t)
	agent := newRecoveryRouterAgent(scriptedRouterCall{tool: "router_say", args: map[string]any{"body": "重启后补上的汇报"}})
	f.svc.SetRouterAgent(agent)
	user := f.addUserActivity(t, "uact-claimed")
	ev := f.addEvent(t, RouterEvent{
		Kind: RouterEventRunSettled, TurnID: "claimed-turn", UserActivityID: user,
		Status: RouterEventClaimed, ClaimedBy: "dead-process", ClaimedAt: time.Now().UTC(),
	})

	router := f.restartRouter(t)
	consumeEvents(t, router)
	awaitEvent(t, f, ev.ID, RouterEventDone)
	decision := f.waitDecision(t, "task-1", "claimed-turn", RouterDecisionApplied)

	if calls := agent.count(); calls != 1 {
		t.Fatalf("model calls = %d, want exactly 1 after recovering the dead claim", calls)
	}
	if replies := visibleReplies(t, f, decision.ID); len(replies) != 1 {
		t.Fatalf("visible reports = %d, want exactly 1: %#v", len(replies), replies)
	}
	if decisions := f.decisions(t, "task-1"); len(decisions) != 1 {
		t.Fatalf("decisions = %d, want exactly 1", len(decisions))
	}
	// done 行保留「谁最后完成它」的审计，但绝不能还挂着上个死进程的领取者。
	if done := f.eventByID(t, "task-1", ev.ID); done.ClaimedBy == "dead-process" {
		t.Fatalf("event still claimed by the dead process: %+v", done)
	}
}

// 行为 3：可见效果已写、完成游标未推进。确定 effect ID 是幂等闸门：效果先写
// 不会出现第二行，游标在效果之后推进。
func TestRecoveryEffectBeforeCursorDoesNotDuplicate(t *testing.T) {
	ctx := context.Background()
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.stopLoop(t)
	// 零脚本 agent：这条路径只要调模型就是缺陷。
	agent := newRecoveryRouterAgent()
	f.svc.SetRouterAgent(agent)
	user := f.addUserActivity(t, "uact-effect-recovery")
	const decisionID = "rdec-effect-recovery"
	const body = "上一进程已经写好的汇报"
	must(t, NewRouterStore(f.store.db).InsertDecision(ctx, &RouterDecision{
		ID: decisionID, TaskID: "task-1", RootID: f.root.ID, UserActivityID: user,
		TriggerKind: RouterTriggerRunSettled, TriggerRef: "run-effect", TurnID: "effect-turn",
		Status: RouterDecisionApplying, RouterSessionKey: "router-session-effect",
		Actions: []RouterAction{{Seq: 0, Kind: RouterActionSay, Body: body, Purpose: "progress"}},
	}), "InsertDecision")
	effectID := wantRouterEffectID(decisionID, 0)
	must(t, NewActivityStore(f.store.db).Insert(ctx, &TaskActivity{
		ID: effectID, TaskID: "task-1", RootID: f.root.ID, Type: ActivityTypeAgentReply, Role: RoleAgent,
		Body: body, Status: ActivityStatusCompleted, Purpose: "progress",
		ReplyToID: user, RouterDecisionID: decisionID,
	}), "insert effect activity")
	before, err := NewActivityStore(f.store.db).ListIncludingHidden(ctx, "task-1")
	must(t, err, "ListIncludingHidden before")
	ev := f.addEvent(t, RouterEvent{
		Kind: RouterEventRunSettled, RunID: "run-effect", TurnID: "effect-turn", UserActivityID: user,
		Status: RouterEventClaimed, ClaimedBy: "dead-process", ClaimedAt: time.Now().UTC(),
	})

	router := f.restartRouter(t)
	consumeEvents(t, router)
	done := awaitEvent(t, f, ev.ID, RouterEventDone)
	if done.LastError != "replayed" {
		t.Fatalf("event reason = %q, want replayed", done.LastError)
	}
	if calls := agent.count(); calls != 0 {
		t.Fatalf("model calls = %d, want 0 on replay", calls)
	}
	after, err := NewActivityStore(f.store.db).ListIncludingHidden(ctx, "task-1")
	must(t, err, "ListIncludingHidden after")
	if len(after) != len(before) {
		t.Fatalf("activities before=%d after=%d, replay must not add rows", len(before), len(after))
	}
	rows := 0
	for _, act := range after {
		if act.ID != effectID {
			continue
		}
		rows++
		if act.Body != body {
			t.Fatalf("effect body = %q, want the already-written %q", act.Body, body)
		}
	}
	if rows != 1 {
		t.Fatalf("rows carrying the deterministic effect id = %d, want exactly 1", rows)
	}
	if applied := f.waitDecision(t, "task-1", "effect-turn", RouterDecisionApplied); applied.AppliedSeq != 1 {
		t.Fatalf("applied_seq = %d, want 1 (cursor advances after the effect)", applied.AppliedSeq)
	}
}

// 行为 4：同一事件并发领取只产生一次效果。CAS 是唯一的闸门——直接对两个实例
// Enqueue 同一个事件，不走 pending 列表过滤。
func TestRecoveryConcurrentClaimSingleEffect(t *testing.T) {
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.stopLoop(t)
	agent := newRecoveryRouterAgent(scriptedRouterCall{tool: "router_say", args: map[string]any{"body": "只应汇报一次"}})
	agent.release = make(chan struct{})
	f.svc.SetRouterAgent(agent)

	// 两个实例共享一个 DB；先构造再插事件，避免构造期那一拍把事件抢走。
	instances := make([]*RouterService, 0, 2)
	for i := 0; i < 2; i++ {
		router := newRouterService(f.svc, f.root.ID, f.svc.RouterConfig(), f.store)
		if router == nil {
			t.Fatal("coordinate root must build a router runtime")
		}
		router.Close()
		waitForCondition(t, func() bool { return !router.coordinateLoopActive() })
		t.Cleanup(router.Close)
		instances = append(instances, router)
	}

	user := f.addUserActivity(t, "uact-concurrent")
	ev := f.addEvent(t, RouterEvent{Kind: RouterEventRunSettled, TurnID: "concurrent-turn", UserActivityID: user})
	trigger := RouterTrigger{
		Kind: RouterTriggerRunSettled, TaskID: "task-1", RootID: f.root.ID,
		UserActivityID: user, TriggerRef: ev.RunID, TurnID: ev.TurnID,
		Mode: RouterModeCoordinate, Event: &ev,
	}

	instances[0].Enqueue(trigger)
	// A 已经领到事件并进入模型回合（count==1 时 claim 早已完成，事件是 claimed）。
	waitForCondition(t, func() bool { return agent.count() == 1 })
	instances[1].Enqueue(trigger)
	// B 的争夺必须发生在 A 还阻塞的时候：CAS 正确时 B 空手退出 lane；去掉
	// status 条件后 B 会领到同一事件并进入模型回合，lane 一直挂到放行。
	claimLost := false
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if instances[1].activeLanes() == 0 {
			claimLost = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(agent.release)
	waitForCondition(t, func() bool {
		return instances[0].activeLanes() == 0 && instances[1].activeLanes() == 0
	})

	if !claimLost {
		t.Fatal("the second instance entered a model turn instead of losing the claim CAS")
	}
	if calls := agent.count(); calls != 1 {
		t.Fatalf("model calls = %d, want exactly 1: the second claim must lose the CAS", calls)
	}
	decisions := f.decisions(t, "task-1")
	if len(decisions) != 1 {
		t.Fatalf("decisions = %d, want exactly 1", len(decisions))
	}
	if decisions[0].Status != RouterDecisionApplied {
		t.Fatalf("decision status = %q (%s), want applied", decisions[0].Status, decisions[0].Error)
	}
	if replies := visibleReplies(t, f, decisions[0].ID); len(replies) != 1 {
		t.Fatalf("visible reports = %d, want exactly 1: %#v", len(replies), replies)
	}
	if got := f.eventByID(t, "task-1", ev.ID).Status; got != RouterEventDone {
		t.Fatalf("event status = %q, want done: the losing instance must not fail it", got)
	}
	if replies := visibleReplies(t, f, decisions[0].ID); len(replies) != 1 {
		t.Fatalf("visible reports after both lanes drained = %d, want exactly 1", len(replies))
	}
}

// 行为 5：旧 Run 的迟到事件只汇报自己的 Run，不改写当前 Run。
func TestRecoveryDelayedOldRunEventKeepsNewRun(t *testing.T) {
	ctx := context.Background()
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.stopLoop(t)
	agent := newRecoveryRouterAgent(scriptedRouterCall{tool: "router_say", args: map[string]any{"body": "旧 Run 的迟到汇报"}})
	f.svc.SetRouterAgent(agent)
	f.addRun(t, "task-1", "run-old", WorkflowTriggeredByUser, map[string]string{
		"agent-1": NodeRunSuccess, "gate-1": NodeRunSuccess, "agent-2": NodeRunSuccess,
	})
	// 早于扫描窗口：有界扫描不会替它补事件，本用例只关心迟到事件的消费。
	if _, err := f.store.db.ExecContext(ctx, `UPDATE workflow_runs SET updated_at = ? WHERE id = ?`,
		time.Now().UTC().Add(-2*time.Hour).Format(routerTimeLayout), "run-old"); err != nil {
		t.Fatalf("backdate run-old: %v", err)
	}
	f.addRun(t, "task-1", "run-new", WorkflowTriggeredByUser, map[string]string{
		"agent-1": NodeRunSuccess, "gate-1": NodeRunRunning,
	})
	f.setTaskStatus(t, "task-1", StatusRunning)
	user := f.addUserActivity(t, "uact-old-run")
	before := f.runByID(t, "run-new").NodeStates
	runsBefore := len(f.runs(t))
	ev := f.addEvent(t, RouterEvent{
		Kind: RouterEventRunSettled, RunID: "run-old", TurnID: "old-run-turn", UserActivityID: user,
		PayloadJSON: `{"run_id":"run-old","status":"success"}`,
	})

	router := f.restartRouter(t)
	consumeEvents(t, router)
	awaitEvent(t, f, ev.ID, RouterEventDone)
	decision := f.waitDecision(t, "task-1", "old-run-turn", RouterDecisionApplied)

	runNew := f.runByID(t, "run-new")
	if len(runNew.NodeStates) != len(before) {
		t.Fatalf("run-new node states = %v, want unchanged %v", runNew.NodeStates, before)
	}
	for nodeID, want := range before {
		if got := runNew.NodeStates[nodeID]; got != want {
			t.Fatalf("run-new node %s = %q, want %q", nodeID, got, want)
		}
	}
	if runs := len(f.runs(t)); runs != runsBefore {
		t.Fatalf("runs = %d, want %d (a runtime event must not start execution)", runs, runsBefore)
	}
	if task := f.task(t, "task-1"); task.Status != StatusRunning {
		t.Fatalf("task status = %q, want running: a delayed event must not rewrite the task", task.Status)
	}
	select {
	case <-f.direct.done:
		t.Fatal("delayed runtime event started a direct agent turn")
	default:
	}
	replies := visibleReplies(t, f, decision.ID)
	if len(replies) != 1 || replies[0].ReplyToID != user || replies[0].RunID != "" {
		t.Fatalf("reply = %#v, want exactly one report in the original message thread", replies)
	}
}

// 行为 6：暂停/终态任务仍可收到汇报，但不执行任何东西。运行期事件不是执行指令，
// 终态闸门只约束用户留言。
func TestRecoveryPausedAndTerminalTaskStillReports(t *testing.T) {
	for _, status := range []string{StatusPaused, StatusSuccess, StatusCancelled} {
		t.Run(status, func(t *testing.T) {
			f := newCoordinateFixture(t, RouterModeCoordinate)
			f.stopLoop(t)
			agent := newRecoveryRouterAgent(scriptedRouterCall{tool: "router_say", args: map[string]any{"body": "任务已结束，补一条汇报"}})
			f.svc.SetRouterAgent(agent)
			f.setTaskStatus(t, "task-1", status)
			user := f.addUserActivity(t, "uact-status")
			ev := f.addEvent(t, RouterEvent{Kind: RouterEventRunSettled, TurnID: "status-turn", UserActivityID: user})

			router := f.restartRouter(t)
			consumeEvents(t, router)
			awaitEvent(t, f, ev.ID, RouterEventDone)
			decision := f.waitDecision(t, "task-1", "status-turn", RouterDecisionApplied)

			if replies := visibleReplies(t, f, decision.ID); len(replies) != 1 {
				t.Fatalf("visible reports = %d, want exactly 1", len(replies))
			}
			if task := f.task(t, "task-1"); task.Status != status {
				t.Fatalf("task status = %q, want %q unchanged", task.Status, status)
			}
			if runs := f.runs(t); len(runs) != 0 {
				t.Fatalf("runs = %d, want 0: a runtime report must not start execution", len(runs))
			}
			select {
			case <-f.direct.done:
				t.Fatalf("runtime report on a %s task started a direct agent turn", status)
			default:
			}
		})
	}
}

// 行为 7：回合预算跨重启仍在。预算来自持久账本，不是内存计数。
func TestRecoveryBudgetSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.stopLoop(t)
	agent := newRecoveryRouterAgent(scriptedRouterCall{tool: "router_say", args: map[string]any{"body": "预算已耗尽仍应该汇报"}})
	f.svc.SetRouterAgent(agent)
	user := f.addUserActivity(t, "uact-budget-recovery")
	routerStore := NewRouterStore(f.store.db)
	for _, turnID := range []string{"budget-recovery-0", "budget-recovery-1", "budget-recovery-2"} {
		must(t, routerStore.InsertDecision(ctx, &RouterDecision{
			ID: "dec-" + turnID, TaskID: "task-1", RootID: f.root.ID, UserActivityID: user,
			TriggerKind: RouterTriggerUserMessage, TurnID: turnID, Status: RouterDecisionApplied,
		}), "InsertDecision(%s)", turnID)
	}
	ev := f.addEvent(t, RouterEvent{Kind: RouterEventRunSettled, TurnID: "budget-over", UserActivityID: user})

	router := f.restartRouter(t)
	consumeEvents(t, router)
	got := awaitEvent(t, f, ev.ID, RouterEventSkipped)
	if !strings.Contains(got.LastError, "turn budget exhausted") {
		t.Fatalf("skipped reason = %q, want turn budget exhausted", got.LastError)
	}
	if calls := agent.count(); calls != 0 {
		t.Fatalf("model calls = %d, want 0 on an exhausted budget", calls)
	}
	if decisions := f.decisions(t, "task-1"); len(decisions) != 3 {
		t.Fatalf("decisions = %d, want the 3 persisted turns only", len(decisions))
	}
}

// 行为 8：无法重放的决策不盲目重跑。只有「零动作 + 游标未动 + 无会话键」才允许
// 删行重跑；其它状态必须落终态。
func TestRecoveryUnreplayableDecisionFailsWithoutModelCall(t *testing.T) {
	ctx := context.Background()
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.stopLoop(t)
	agent := newRecoveryRouterAgent(scriptedRouterCall{tool: "router_say", args: map[string]any{"body": "不该出现的重跑汇报"}})
	f.svc.SetRouterAgent(agent)
	user := f.addUserActivity(t, "uact-unreplayable")
	const failure = "模型回合已验证失败"
	must(t, NewRouterStore(f.store.db).InsertDecision(ctx, &RouterDecision{
		ID: "rdec-unreplayable", TaskID: "task-1", RootID: f.root.ID, UserActivityID: user,
		TriggerKind: RouterTriggerRunSettled, TriggerRef: "run-unreplayable", TurnID: "unreplayable-turn",
		Status: RouterDecisionFailed, Error: failure,
	}), "InsertDecision")
	ev := f.addEvent(t, RouterEvent{
		Kind: RouterEventRunSettled, RunID: "run-unreplayable", TurnID: "unreplayable-turn", UserActivityID: user,
		Status: RouterEventClaimed, ClaimedBy: "dead-process", ClaimedAt: time.Now().UTC(),
	})

	router := f.restartRouter(t)
	consumeEvents(t, router)
	got := awaitEvent(t, f, ev.ID, RouterEventFailed)
	if !strings.Contains(got.LastError, failure) {
		t.Fatalf("event reason = %q, want the original decision failure %q", got.LastError, failure)
	}
	if calls := agent.count(); calls != 0 {
		t.Fatalf("model calls = %d, want 0: a failed decision must not be replayed", calls)
	}
	acts, err := NewActivityStore(f.store.db).List(ctx, "task-1")
	must(t, err, "List activities")
	for _, act := range acts {
		if act.Type == ActivityTypeAgentReply {
			t.Fatalf("unreplayable decision produced a visible activity: %#v", act)
		}
	}
	decision, err := NewRouterStore(f.store.db).DecisionByTurn(ctx, "task-1", "unreplayable-turn")
	must(t, err, "DecisionByTurn")
	if decision.Status != RouterDecisionFailed {
		t.Fatalf("decision status = %q, want failed (terminal states stay terminal)", decision.Status)
	}
}
