package kanban

import (
	"context"
	"strings"
	"testing"
	"time"
)

// S2：整轮校验失败时保留纯文本。
//
// 现场（只读诊断报告 R4）：5 条 `ask_user must be the only action` 都是没有
// 来源留言的结算/巡检回合，旧路径无处回落，模型写下的结论跟着动作一起消失，
// 用户侧零可见。这里钉住两件事：结论要按正常回复落成可见活动，且必须有一条
// 确定 ID 的系统说明解释「为什么这一轮只留下结论」。
func TestCoordinatorRejectedTurnKeepsVisibleSay(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.stopLoop(t)
	f.agent.calls = []scriptedRouterCall{
		{tool: "router_say", args: map[string]any{"body": "结论：两个节点都跑完了，只差最后一步合并。"}},
		// 白名单外：运行期事件回合不允许它，于是整轮校验失败 —— 这正是要保留文本的场景。
		{tool: "router_deliver_to_active_run", args: map[string]any{"body": "转交给节点"}},
	}
	f.addUserActivity(t, "uact-salvage")
	ev := f.addEvent(t, RouterEvent{Kind: RouterEventRunSettled, TurnID: "salvage-1", UserActivityID: "uact-salvage"})
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
	decision := decisions[0]
	if runs := f.runs(t); len(runs) != 0 {
		t.Fatalf("rejected turn created runs: %d", len(runs))
	}
	select {
	case <-f.direct.done:
		t.Fatal("rejected turn started a direct agent turn")
	default:
	}

	activities, err := NewActivityStore(f.store.db).List(ctx, "task-1")
	must(t, err, "List activities")
	var say, notice *TaskActivity
	for _, act := range activities {
		switch {
		case act.RouterDecisionID == decision.ID:
			say = act
		case act.ID == "route-rejected:"+decision.ID+":1":
			notice = act
		}
	}
	if say == nil {
		t.Fatalf("the rejected turn dropped its text; activities = %+v", activities)
	}
	if say.Body != "结论：两个节点都跑完了，只差最后一步合并。" || say.Status != ActivityStatusCompleted {
		t.Fatalf("salvaged say = status %q body %q", say.Status, say.Body)
	}
	// S2 的批准口径是「按 summary 应用」：只进流不占摘要所有权的话，卡片会继续
	// 停在节点原文上，被拒回合的结论仍然到不了用户眼前。
	if say.Purpose != "summary" {
		t.Fatalf("salvaged say purpose = %q, want summary", say.Purpose)
	}
	if task := f.task(t, "task-1"); task.ResultSummary != "结论：两个节点都跑完了，只差最后一步合并。" {
		t.Fatalf("task result summary = %q, want the salvaged text", task.ResultSummary)
	}
	if notice == nil {
		t.Fatalf("missing route-rejected notice; activities = %+v", activities)
	}
	if notice.Type != ActivityTypeAttention || notice.Role != "system" || notice.Status != ActivityStatusCompleted {
		t.Fatalf("notice = type %q role %q status %q", notice.Type, notice.Role, notice.Status)
	}
	if !strings.Contains(notice.Body, "is not allowed for runtime event turns") {
		t.Fatalf("notice body = %q, want the rejection reason", notice.Body)
	}
	if !strings.Contains(notice.Body, "结论单独保留") {
		t.Fatalf("notice body = %q, want it to point at the kept reply", notice.Body)
	}
}

// 没有可保留文本时也要留说明：否则整轮 rejected 在用户侧仍是「什么都没发生」，
// 只是这次说明会明确写「没有可保留的纯文本结论」。
func TestCoordinatorRejectedTurnWithoutTextStillNotices(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.stopLoop(t)
	f.agent.calls = []scriptedRouterCall{
		{tool: "router_deliver_to_active_run", args: map[string]any{"body": "转交给节点"}},
	}
	f.addUserActivity(t, "uact-empty")
	ev := f.addEvent(t, RouterEvent{Kind: RouterEventRunSettled, TurnID: "salvage-2", UserActivityID: "uact-empty"})
	if _, err := f.router.ConsumeRouterEvents(ctx, now); err != nil {
		t.Fatalf("ConsumeRouterEvents: %v", err)
	}
	f.waitEvent(t, ev.ID, RouterEventFailed)
	decisions := f.decisions(t, "task-1")
	if len(decisions) != 1 || decisions[0].Status != RouterDecisionRejected {
		t.Fatalf("decisions = %+v, want one rejected decision", decisions)
	}
	activities, err := NewActivityStore(f.store.db).List(ctx, "task-1")
	must(t, err, "List activities")
	var notice *TaskActivity
	for _, act := range activities {
		if act.ID == "route-rejected:"+decisions[0].ID+":0" {
			notice = act
		}
	}
	if notice == nil {
		t.Fatalf("missing route-rejected notice; activities = %+v", activities)
	}
	if !strings.Contains(notice.Body, "没有可保留的纯文本结论") {
		t.Fatalf("notice body = %q", notice.Body)
	}
}

// 判据是触发类不是留言 ID：运行期事件决策同样带 UserActivityID（线上 20 条
// rejected 全部如此），按 ID 判空会把生产主路径整条漏掉；而用户留言回合有
// FallbackRoute 可落，两条路径都写会让同一段结论出现两次。
func TestShouldSalvageRejectedTurnKeysOnTriggerKind(t *testing.T) {
	cases := []struct {
		name       string
		decision   *RouterDecision
		wantSalved bool
	}{
		{name: "nil", decision: nil, wantSalved: false},
		{name: "user message", decision: &RouterDecision{TriggerKind: RouterTriggerUserMessage, UserActivityID: "uact-1"}, wantSalved: false},
		{name: "turn settled with user activity", decision: &RouterDecision{TriggerKind: RouterTriggerTurnSettled, UserActivityID: "uact-1"}, wantSalved: true},
		{name: "task orphaned", decision: &RouterDecision{TriggerKind: RouterTriggerTaskOrphaned}, wantSalved: true},
	}
	for _, tc := range cases {
		if got := shouldSalvageRejectedTurn(tc.decision); got != tc.wantSalved {
			t.Fatalf("%s: shouldSalvageRejectedTurn = %v, want %v", tc.name, got, tc.wantSalved)
		}
	}
}
