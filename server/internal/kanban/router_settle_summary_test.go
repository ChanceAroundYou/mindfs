package kanban

import (
	"context"
	"strings"
	"testing"
	"time"
)

// 线上 task #99 与 llmux 两个待审核任务：Run 成功结算以后，任务卡上的
// result_summary 还是节点回复的原文（#99 甚至是六块黏在一起的长文），用户在
// 对话末尾也拿不到一段真正的总结。用户裁定「由 router 统一写」：结算回合的
// 汇报用 purpose=summary，写进任务结果摘要。
func TestRunSettledSummaryWritesTaskResultSummary(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.stopLoop(t)
	// #99 的现场：Run 四节点全成功，卡片摘要却是回复原文的截断。
	f.addRun(t, "task-1", "run-summary-1", "uact-run-summary-1",
		map[string]string{"agent-1": NodeRunSuccess, "gate-1": NodeRunSuccess, "agent-2": NodeRunSuccess})
	f.markOwned(t, "task-1", "run-summary-1", "decision-seed")
	f.setTaskStatus(t, "task-1", StatusSuccess)
	must(t, f.store.UpdateTaskResultSummary(ctx, "task-1",
		"Now let me write the gitview changes.Now update `mergeVanishedSource`."), "seed raw summary")

	summary := "结论：worktree 的合并目标已改到任务树里合并，w1/w2 两层都已验证。\n" +
		"证据：8 条 gitview 用例 + 6 条看板用例，go test 全绿。\n" +
		"未做：没有部署，也没有重启任何节点。"
	f.agent.calls = []scriptedRouterCall{{tool: "router_say", args: map[string]any{
		"body": summary, "purpose": "summary",
	}}}
	ev := f.addEvent(t, RouterEvent{
		Kind: RouterEventRunSettled, RunID: "run-summary-1",
		TurnID: "summary-1", UserActivityID: "uact-run-summary-1",
	})

	if _, err := f.router.ConsumeRouterEvents(ctx, now); err != nil {
		t.Fatalf("consume: %v", err)
	}
	f.waitEvent(t, ev.ID, RouterEventDone)

	task, err := f.store.GetTask(ctx, "task-1")
	must(t, err, "GetTask")
	if task.ResultSummary != summary {
		t.Fatalf("result_summary = %q, want the router summary", task.ResultSummary)
	}
	visible, err := NewActivityStore(f.store.db).List(ctx, "task-1")
	must(t, err, "List activities")
	var reply *TaskActivity
	for _, act := range visible {
		if act.RouterDecisionID != "" && act.Type == ActivityTypeAgentReply {
			reply = act
		}
	}
	if reply == nil || reply.Purpose != "summary" {
		t.Fatalf("settle reply = %#v, want purpose=summary", reply)
	}
	if reply.Body != summary {
		t.Fatalf("settle reply body = %q, want the summary text", reply.Body)
	}
}

// 线上补偿回合的现场（llmux #19，2026-11-21 重启后第一拍）：run_settled 事件被
// 补上重放，模型却把汇报写成了 purpose=progress —— 对话里有了像样的总结，任务卡
// 的 result_summary 仍是节点原文。结算回合没有显式 summary 时，最后一条 say 就是
// 这一轮的总结（用户裁定「最后要有一段长度合理的总结」），不能因为措辞降级丢掉。
func TestRunSettledProgressSayIsPromotedToSummary(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.stopLoop(t)
	f.addRun(t, "task-1", "run-summary-2", "uact-run-summary-2",
		map[string]string{"agent-1": NodeRunSuccess, "gate-1": NodeRunSuccess, "agent-2": NodeRunSuccess})
	f.markOwned(t, "task-1", "run-summary-2", "decision-seed")
	f.setTaskStatus(t, "task-1", StatusSuccess)
	must(t, f.store.UpdateTaskResultSummary(ctx, "task-1", "Now let me verify the diff."), "seed raw summary")

	body := "这轮工作流已收敛：方案节点完成；实现节点首次失败后重试成功，测试全绿。"
	f.agent.calls = []scriptedRouterCall{{tool: "router_say", args: map[string]any{
		"body": body, "purpose": "progress",
	}}}
	ev := f.addEvent(t, RouterEvent{
		Kind: RouterEventRunSettled, RunID: "run-summary-2",
		TurnID: "summary-progress", UserActivityID: "uact-run-summary-2",
	})

	if _, err := f.router.ConsumeRouterEvents(ctx, now); err != nil {
		t.Fatalf("consume: %v", err)
	}
	f.waitEvent(t, ev.ID, RouterEventDone)

	task, err := f.store.GetTask(ctx, "task-1")
	must(t, err, "GetTask")
	if task.ResultSummary != body {
		t.Fatalf("result_summary = %q, want the settle progress report promoted to summary", task.ResultSummary)
	}
	visible, err := NewActivityStore(f.store.db).List(ctx, "task-1")
	must(t, err, "List activities")
	var reply *TaskActivity
	for _, act := range visible {
		if act.RouterDecisionID != "" && act.Type == ActivityTypeAgentReply {
			reply = act
		}
	}
	if reply == nil || reply.Purpose != "summary" {
		t.Fatalf("settle reply = %#v, want purpose=summary", reply)
	}
}

// 显式写了 summary 时以它为准：同一回合同样允许先留一条进度汇报，最后的总结
// 由模型自己挑，不被「最后一条 say」的兜底规则改写。
func TestExplicitSettleSummaryWinsOverLastSay(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.stopLoop(t)
	f.addRun(t, "task-1", "run-summary-3", "uact-run-summary-3",
		map[string]string{"agent-1": NodeRunSuccess, "gate-1": NodeRunSuccess, "agent-2": NodeRunSuccess})
	f.markOwned(t, "task-1", "run-summary-3", "decision-seed")
	f.setTaskStatus(t, "task-1", StatusSuccess)
	f.agent.calls = []scriptedRouterCall{
		{tool: "router_say", args: map[string]any{"body": "先跑测试，稍后给结论。", "purpose": "progress"}},
		{tool: "router_say", args: map[string]any{"body": "结论：全部通过。", "purpose": "summary"}},
	}
	ev := f.addEvent(t, RouterEvent{
		Kind: RouterEventRunSettled, RunID: "run-summary-3",
		TurnID: "summary-explicit", UserActivityID: "uact-run-summary-3",
	})

	if _, err := f.router.ConsumeRouterEvents(ctx, now); err != nil {
		t.Fatalf("consume: %v", err)
	}
	f.waitEvent(t, ev.ID, RouterEventDone)

	task, err := f.store.GetTask(ctx, "task-1")
	must(t, err, "GetTask")
	if task.ResultSummary != "结论：全部通过。" {
		t.Fatalf("result_summary = %q, want the explicit summary", task.ResultSummary)
	}
	// 显式 summary 之前的进度汇报保持 progress：兜底规则只补缺失，不改写模型
	// 已经分好类的其它回复。
	visible, err := NewActivityStore(f.store.db).List(ctx, "task-1")
	must(t, err, "List activities")
	purposes := map[string]string{}
	for _, act := range visible {
		if act.RouterDecisionID != "" && act.Type == ActivityTypeAgentReply {
			purposes[act.Body] = act.Purpose
		}
	}
	if purposes["先跑测试，稍后给结论。"] != "progress" {
		t.Fatalf("progress reply purpose = %q, want progress (the fallback must not rewrite it)", purposes["先跑测试，稍后给结论。"])
	}
	if purposes["结论：全部通过。"] != "summary" {
		t.Fatalf("summary reply purpose = %q, want summary", purposes["结论：全部通过。"])
	}
}

// 失败回合的进度汇报不能被兜底规则提升成总结：只有 Run 成功结算的那一轮才写
// 任务结果摘要，否则一次失败说明会顶掉上一轮真正的结论。
func TestFailedEventProgressSayDoesNotBecomeSummary(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.stopLoop(t)
	f.addRun(t, "task-1", "run-summary-4", "uact-run-summary-4",
		map[string]string{"agent-1": NodeRunFail})
	f.markOwned(t, "task-1", "run-summary-4", "decision-seed")
	f.setTaskStatus(t, "task-1", StatusWaitingUser)
	must(t, f.store.UpdateTaskResultSummary(ctx, "task-1", "上一轮的结论。"), "seed summary")

	f.agent.calls = []scriptedRouterCall{{tool: "router_say", args: map[string]any{
		"body": "实现节点失败了，正在重试。", "purpose": "progress",
	}}}
	ev := f.addEvent(t, RouterEvent{
		Kind: RouterEventRunFailed, RunID: "run-summary-4",
		TurnID: "summary-failed", UserActivityID: "uact-run-summary-4",
	})

	if _, err := f.router.ConsumeRouterEvents(ctx, now); err != nil {
		t.Fatalf("consume: %v", err)
	}
	f.waitEvent(t, ev.ID, RouterEventDone)

	task, err := f.store.GetTask(ctx, "task-1")
	must(t, err, "GetTask")
	if task.ResultSummary != "上一轮的结论。" {
		t.Fatalf("result_summary = %q, a failed-run progress report must not overwrite it", task.ResultSummary)
	}
}

// 只有结算成功的那一轮才写总结：失败回合即便模型写了 summary 也降级成
// progress，不能把失败说明写成任务这一轮的结果摘要。
func TestFailureEventSayIsNeverASummary(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.stopLoop(t)
	f.addRun(t, "task-1", "run-failed-1", "uact-run-failed-1",
		map[string]string{"agent-1": NodeRunFail, "gate-1": NodeRunCancelled, "agent-2": NodeRunCancelled})
	f.markOwned(t, "task-1", "run-failed-1", "decision-seed")
	must(t, f.store.UpdateTaskResultSummary(ctx, "task-1", "上一轮的摘要"), "seed summary")
	f.agent.calls = []scriptedRouterCall{{tool: "router_say", args: map[string]any{
		"body": "这一轮失败了，我已经重试过节点。", "purpose": "summary",
	}}}
	ev := f.addEvent(t, RouterEvent{
		Kind: RouterEventRunFailed, RunID: "run-failed-1",
		TurnID: "failed-summary", UserActivityID: "uact-run-failed-1",
	})

	if _, err := f.router.ConsumeRouterEvents(ctx, now); err != nil {
		t.Fatalf("consume: %v", err)
	}
	f.waitEvent(t, ev.ID, RouterEventDone)

	task, err := f.store.GetTask(ctx, "task-1")
	must(t, err, "GetTask")
	if task.ResultSummary != "上一轮的摘要" {
		t.Fatalf("result_summary = %q, want the previous turn's summary untouched", task.ResultSummary)
	}
	visible, err := NewActivityStore(f.store.db).List(ctx, "task-1")
	must(t, err, "List activities")
	var reply *TaskActivity
	for _, act := range visible {
		if act.RouterDecisionID == "" || act.Type != ActivityTypeAgentReply {
			continue
		}
		if act.Purpose == "progress" {
			reply = act
		}
	}
	if reply == nil {
		t.Fatalf("failure reply missing; want a progress report, got %#v", visible)
	}
	if reply.Body != "这一轮失败了，我已经重试过节点。" {
		t.Fatalf("failure reply body = %q", reply.Body)
	}
}

// llmux task_260f5740cced704f 的现场：Run 最终成功，run_settled 汇报回合撞死线
// 落 failed，失败事件不会重放 —— 任务跑完了，对话最后一行还是那条出错重试提示。
// 补偿只针对「可重试原因 + 还没补偿过」的结算事件，且同一事件不会反复重排。
func TestFailedSettleEventIsRequeuedForSummary(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.stopLoop(t)
	f.addRun(t, "task-1", "run-summary-1", "uact-run-summary-1",
		map[string]string{"agent-1": NodeRunSuccess, "gate-1": NodeRunSuccess, "agent-2": NodeRunSuccess})
	f.markOwned(t, "task-1", "run-summary-1", "decision-seed")
	f.setTaskStatus(t, "task-1", StatusSuccess)
	ev := f.addEvent(t, RouterEvent{
		Kind: RouterEventRunSettled, RunID: "run-summary-1",
		TurnID: "lost-settle", UserActivityID: "uact-run-summary-1",
	})
	claimed, err := f.events.ClaimEvent(ctx, ev.ID, "router-old")
	must(t, err, "ClaimEvent")
	if !claimed {
		t.Fatalf("event not claimed")
	}
	must(t, f.events.CompleteEvent(ctx, ev.ID, RouterEventFailed,
		"persist turn blocks: context deadline exceeded"), "CompleteEvent")

	f.router.requeueFailedSettleEvents(ctx, now)
	requeued := f.eventByID(t, "task-1", ev.ID)
	if requeued.Status != RouterEventPending || requeued.Attempt != 0 {
		t.Fatalf("event = %s attempt=%d, want pending attempt=0", requeued.Status, requeued.Attempt)
	}
	if !strings.HasPrefix(requeued.TurnID, routerSettleCompensationTurnPrefix) {
		t.Fatalf("turn_id = %q, want the compensation prefix", requeued.TurnID)
	}

	summary := "结论：Codex 自动更新已落地，run_settled 汇报补发成功。\n证据：go test 全绿。\n未做：没有部署。"
	f.agent.calls = []scriptedRouterCall{{tool: "router_say", args: map[string]any{
		"body": summary, "purpose": "summary",
	}}}
	if _, err := f.router.ConsumeRouterEvents(ctx, now); err != nil {
		t.Fatalf("consume: %v", err)
	}
	f.waitEvent(t, ev.ID, RouterEventDone)
	task, err := f.store.GetTask(ctx, "task-1")
	must(t, err, "GetTask")
	if task.ResultSummary != summary {
		t.Fatalf("result_summary = %q, want the compensation summary", task.ResultSummary)
	}

	// 补偿过的事件再失败也不会被无限重排：turn_id 的补偿前缀就是那道闸。
	if _, err := f.store.db.ExecContext(ctx,
		`UPDATE router_events SET status = ?, last_error = ? WHERE id = ?`,
		RouterEventFailed, "context deadline exceeded", ev.ID); err != nil {
		t.Fatalf("force failed: %v", err)
	}
	f.router.requeueFailedSettleEvents(ctx, now)
	if again := f.eventByID(t, "task-1", ev.ID); again.Status != RouterEventFailed {
		t.Fatalf("event = %s, want it to stay failed after one compensation", again.Status)
	}
}
