package kanban

import (
	"context"
	"strings"
	"testing"
	"time"
)

// 运行期事件回合（R1 T7）：汇报落在线程里、动作白名单、崩溃 replay 与摘要隔离。

func eventByIDIn(t *testing.T, f *routerEventFixture, eventID string) RouterEvent {
	t.Helper()
	for _, ev := range f.list(t, "task-1") {
		if ev.ID == eventID {
			return ev
		}
	}
	t.Fatalf("event %s not found", eventID)
	return RouterEvent{}
}

func waitEventIn(t *testing.T, f *routerEventFixture, eventID, status string) RouterEvent {
	t.Helper()
	var got RouterEvent
	waitForCondition(t, func() bool {
		got = eventByIDIn(t, f, eventID)
		return got.Status == status
	})
	return got
}

// 行为 3：run_settled → say applied、事件 done；决策三元组与事件行一致，回复
// 指向原始用户活动所在线程。
func TestCoordinatorSettledEventReportsToOriginThread(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.stopLoop(t)
	f.agent.calls = []scriptedRouterCall{{tool: "router_say", args: map[string]any{"body": "流程已完成"}}}

	f.addRun(t, "task-1", "run-1", WorkflowTriggeredByUser, map[string]string{"agent-1": NodeRunSuccess})
	f.markOwned(t, "task-1", "run-1", "decision-1")
	// Run 结算后任务本来就是终态：运行期汇报不能被终态闸门挡成失败。
	f.setTaskStatus(t, "task-1", StatusSuccess)
	res := f.scan(t, now)
	if len(res.Produced) != 1 || res.Produced[0].Kind != RouterEventRunSettled {
		t.Fatalf("produced = %+v, want one run_settled", res.Produced)
	}
	event := res.Produced[0]
	if _, err := f.router.ConsumeRouterEvents(ctx, now); err != nil {
		t.Fatalf("ConsumeRouterEvents: %v", err)
	}
	done := f.waitEvent(t, event.ID, RouterEventDone)
	if done.LastError != "" {
		t.Fatalf("completed event error = %q", done.LastError)
	}
	decision := f.waitDecision(t, "task-1", event.TurnID, RouterDecisionApplied)
	if decision.TurnID != event.TurnID || decision.TriggerKind != RouterTriggerRunSettled || decision.TriggerRef != "run-1" {
		t.Fatalf("decision trigger = turn %q kind %q ref %q", decision.TurnID, decision.TriggerKind, decision.TriggerRef)
	}
	activities, err := NewActivityStore(f.store.db).List(ctx, "task-1")
	must(t, err, "List activities")
	var reply *TaskActivity
	for _, act := range activities {
		if act.RouterDecisionID == decision.ID {
			reply = act
		}
	}
	if reply == nil {
		t.Fatal("runtime say did not create a visible activity")
	}
	if reply.ReplyToID != "uact-run-1" || reply.Status != ActivityStatusCompleted || reply.Body != "流程已完成" {
		t.Fatalf("reply = to %q status %q body %q", reply.ReplyToID, reply.Status, reply.Body)
	}
}

// 行为 7：运行期事件只允许 say / ask_user / silent。三类执行动作各自被拒绝，
// 零新 Run、零直连回合、事件 failed、无 fallback。
func TestCoordinatorRuntimeEventActionWhitelist(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		call scriptedRouterCall
	}{
		{"run_workflow", scriptedRouterCall{tool: "router_run_workflow", args: map[string]any{"workflow_id": "workflow-debug"}}},
		{"message_agent", scriptedRouterCall{tool: "router_message_agent", args: map[string]any{"body": "转交给 agent"}}},
		{"deliver_to_active_run", scriptedRouterCall{tool: "router_deliver_to_active_run", args: map[string]any{"body": "转交给节点"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newCoordinateFixture(t, RouterModeCoordinate)
			f.stopLoop(t)
			f.publishWorkflow(t, "workflow-debug")
			f.agent.calls = []scriptedRouterCall{tc.call}
			f.addUserActivity(t, "uact-perm")
			ev := f.addEvent(t, RouterEvent{Kind: RouterEventRunSettled, TurnID: "perm-" + tc.name, UserActivityID: "uact-perm"})
			if _, err := f.router.ConsumeRouterEvents(ctx, now); err != nil {
				t.Fatalf("ConsumeRouterEvents: %v", err)
			}
			got := f.waitEvent(t, ev.ID, RouterEventFailed)
			if !strings.Contains(got.LastError, "is not allowed for runtime event turns") {
				t.Fatalf("event error = %q, want the runtime whitelist rejection", got.LastError)
			}
			decisions := f.decisions(t, "task-1")
			if len(decisions) != 1 || decisions[0].Status != RouterDecisionRejected {
				t.Fatalf("decisions = %+v, want one rejected decision", decisions)
			}
			if runs := f.runs(t); len(runs) != 0 {
				t.Fatalf("runtime action created runs: %d", len(runs))
			}
			select {
			case <-f.direct.done:
				t.Fatal("runtime action started a direct agent turn")
			default:
			}
		})
	}
}

// 行为 10：决策已 applied、事件仍 claimed（进程死在两者之间）→ 构造期回收 +
// 消费后事件 done，零模型调用、零新增可见活动。
func TestCoordinatorCrashReplayCompletesWithoutModelCall(t *testing.T) {
	ctx := context.Background()
	f := newRouterEventFixture(t)
	// 先在 off 配置下取 ActivityService，避免提前构造 Router（构造即回收）。
	if _, err := f.svc.ActivityServiceForRoot(f.root.ID); err != nil {
		t.Fatalf("ActivityServiceForRoot: %v", err)
	}
	f.svc.SetRouterConfig(RouterConfig{Mode: RouterModeCoordinate, IdleClose: time.Hour})
	agent := &scriptedRouterAgent{}
	f.svc.SetRouterAgent(agent)
	must(t, NewActivityStore(f.store.db).Insert(ctx, &TaskActivity{
		ID: "uact-crash", TaskID: "task-1", RootID: f.root.ID,
		Type: ActivityTypeUserMessage, Author: "user", Role: "user", Body: "原始需求",
	}), "insert user activity")

	routerStore := NewRouterStore(f.store.db)
	must(t, routerStore.InsertDecision(ctx, &RouterDecision{
		ID: "dec-crash", TaskID: "task-1", RootID: f.root.ID, UserActivityID: "uact-crash",
		TriggerKind: RouterTriggerRunSettled, TriggerRef: "run-crash", TurnID: "crash-turn",
		Status:  RouterDecisionApplied,
		Actions: []RouterAction{{Seq: 0, Kind: RouterActionSay, Body: "已汇报", Purpose: "chat"}},
	}), "InsertDecision")
	event, _, err := f.events.InsertEvent(ctx, RouterEvent{
		RootID: f.root.ID, TaskID: "task-1", RunID: "run-crash", Kind: RouterEventRunSettled,
		InstanceKey: "run-crash", UserActivityID: "uact-crash", TurnID: "crash-turn",
		Status: RouterEventClaimed, ClaimedBy: "dead-process",
	})
	must(t, err, "InsertEvent")
	before, err := NewActivityStore(f.store.db).ListIncludingHidden(ctx, "task-1")
	must(t, err, "ListIncludingHidden before")

	router := f.svc.RouterServiceForRoot(f.root.ID)
	if router == nil {
		t.Fatal("coordinate root must build a router runtime")
	}
	// 手动驱动消费：先让 30s 循环退场，避免它抢走待消费的事件。
	router.Close()
	waitForCondition(t, func() bool { return !router.coordinateLoopActive() })
	t.Cleanup(router.Close)
	if got := eventByIDIn(t, f, event.ID).Status; got != RouterEventPending {
		t.Fatalf("recovered event status = %q, want pending", got)
	}
	if _, err := router.ConsumeRouterEvents(ctx, time.Now().UTC()); err != nil {
		t.Fatalf("ConsumeRouterEvents: %v", err)
	}
	done := waitEventIn(t, f, event.ID, RouterEventDone)
	if done.LastError != "replayed" {
		t.Fatalf("replay reason = %q, want replayed", done.LastError)
	}
	if calls := len(agent.sessions); calls != 0 {
		t.Fatalf("model calls = %d, want 0 on replay", calls)
	}
	after, err := NewActivityStore(f.store.db).ListIncludingHidden(ctx, "task-1")
	must(t, err, "ListIncludingHidden after")
	if len(after) != len(before) {
		t.Fatalf("activities before=%d after=%d, replay must not add visible activities", len(before), len(after))
	}
}

// 行为 12：运行期 say 不覆盖任务结果摘要（用户留言的 say 仍写摘要，见行为 2）。
func TestCoordinatorRuntimeSayKeepsTaskSummary(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.stopLoop(t)
	f.agent.calls = []scriptedRouterCall{{tool: "router_say", args: map[string]any{"body": "运行期汇报"}}}
	f.addUserActivity(t, "uact-summary")
	must(t, f.store.UpdateTaskResultSummary(ctx, "task-1", "旧摘要"), "UpdateTaskResultSummary")
	ev := f.addEvent(t, RouterEvent{Kind: RouterEventRunSettled, TurnID: "summary-1", UserActivityID: "uact-summary"})
	if _, err := f.router.ConsumeRouterEvents(ctx, now); err != nil {
		t.Fatalf("ConsumeRouterEvents: %v", err)
	}
	f.waitEvent(t, ev.ID, RouterEventDone)
	if task := f.task(t, "task-1"); task.ResultSummary != "旧摘要" {
		t.Fatalf("result summary = %q, runtime say must not overwrite it", task.ResultSummary)
	}
	activities, err := NewActivityStore(f.store.db).List(ctx, "task-1")
	must(t, err, "List activities")
	found := false
	for _, act := range activities {
		if act.Body == "运行期汇报" && act.ReplyToID == "uact-summary" {
			found = true
		}
	}
	if !found {
		t.Fatal("runtime say was not visible in the origin thread")
	}
}
