package kanban

import (
	"context"
	"strings"
	"testing"
	"time"
)

// C1：巡检事件没有「来源用户留言」—— 这一轮停了（turn_stalled）、任务没人
// 接着做（task_orphaned）、回合被重启打断（turn_interrupted），用户什么都没发。
// 这类事件必须能回话，否则巡检发现的问题只会留在 router_events 里，用户什么
// 都看不到。口径（用户 2026-11-23 裁定）：只放开 say / ask_user；
// complete_task 仍然要求有来源留言。
func TestCoordinatorInspectionEventWithoutOriginCanSay(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

	for _, kind := range []RouterEventKind{
		RouterEventTurnStalled,
		RouterEventTaskOrphaned,
		RouterEventTurnInterrupted,
		RouterEventTurnSettled,
	} {
		t.Run(string(kind), func(t *testing.T) {
			f := newCoordinateFixture(t, RouterModeCoordinate)
			f.stopLoop(t)
			f.agent.calls = []scriptedRouterCall{{
				tool: "router_say",
				args: map[string]any{"body": "这一轮停了，我再试一次。"},
			}}
			turnID := "inspect-" + string(kind)
			ev := f.addEvent(t, RouterEvent{Kind: kind, TurnID: turnID})

			f.router.ConsumeRouterEvents(ctx, now)

			if got := f.waitEvent(t, ev.ID, RouterEventDone); got.Status != RouterEventDone {
				t.Fatalf("event status = %q, want done", got.Status)
			}
			decision := f.waitDecision(t, "task-1", turnID, RouterDecisionApplied)
			if decision.UserActivityID != "" {
				t.Fatalf("决策不该凭空长出用户留言: %q", decision.UserActivityID)
			}

			acts, err := NewActivityStore(f.store.db).List(ctx, "task-1")
			must(t, err, "List")
			if len(acts) != 1 {
				t.Fatalf("可见活动 = %d 条, want 1: %#v", len(acts), acts)
			}
			if acts[0].Body != "这一轮停了，我再试一次。" || acts[0].Purpose != "progress" {
				t.Fatalf("汇报 = %#v", acts[0])
			}
			if acts[0].ReplyToID != "" || acts[0].RouterDecisionID != decision.ID {
				t.Fatalf("回复绑定错误: reply_to_id=%q decision=%q", acts[0].ReplyToID, acts[0].RouterDecisionID)
			}
		})
	}
}

// 巡检回合同样只能「说」：complete_task 需要一条来源留言承担这轮收尾的证据，
// 没有它就不该被应用 —— 否则一个没有用户回合的任务会被巡检悄悄宣告完成。
//
// 但「拒绝」不等于「什么都不说」：上一轮就是这样，动作被拒、决策 failed、
// 用户侧零可见变化（现场 rdec_47d5992f117dc51b）。被拒的动作必须折成一条
// 可见系统说明，用户才知道 Router 想做什么以及为什么没做成。
func TestCoordinatorInspectionEventWithoutOriginRejectsCompleteTask(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.stopLoop(t)
	f.agent.calls = []scriptedRouterCall{{
		tool: "router_complete_task",
		args: map[string]any{"summary": "巡检认为已经做完。"},
	}}
	ev := f.addEvent(t, RouterEvent{Kind: RouterEventTurnStalled, TurnID: "inspect-complete"})

	f.router.ConsumeRouterEvents(ctx, now)

	got := f.waitEvent(t, ev.ID, RouterEventFailed)
	if got.Status != RouterEventFailed {
		t.Fatalf("event status = %q, want failed", got.Status)
	}
	decision := f.waitDecision(t, "task-1", "inspect-complete", RouterDecisionFailed)
	if !strings.Contains(decision.Error, "requires an originating user activity") {
		t.Fatalf("失败原因 = %q", decision.Error)
	}
	acts, err := NewActivityStore(f.store.db).List(ctx, "task-1")
	must(t, err, "List")
	if len(acts) != 1 {
		t.Fatalf("被拒的动作要留一条可见说明 = %d 条, want 1: %#v", len(acts), acts)
	}
	note := acts[0]
	if note.Type != ActivityTypeAttention || note.Role != "system" {
		t.Fatalf("说明行 = type %q role %q, want attention/system", note.Type, note.Role)
	}
	if !strings.Contains(note.Body, "complete_task") {
		t.Fatalf("说明行没写清被拒的动作: %q", note.Body)
	}
	// 它只是说明，不是对任务的结论，也不是对某条留言的回复。
	if note.Status != ActivityStatusCompleted || note.ReplyToID != "" {
		t.Fatalf("说明行状态/绑定 = %q/%q", note.Status, note.ReplyToID)
	}
}

// 闸门的准入集合就是「系统自己发现的现场」这四类；其它事件缺来源留言仍然跳过。
func TestRouterEventAllowsNoUserActivity(t *testing.T) {
	for kind, want := range map[RouterEventKind]bool{
		RouterEventTurnSettled:     true,
		RouterEventTurnStalled:     true,
		RouterEventTaskOrphaned:    true,
		RouterEventTurnInterrupted: true,
		RouterEventRunSettled:      false,
		RouterEventRunFailed:       false,
		RouterEventGateWaiting:     false,
		RouterEventDirectStalled:   false,
	} {
		if got := routerEventAllowsNoUserActivity(kind); got != want {
			t.Fatalf("routerEventAllowsNoUserActivity(%s) = %v, want %v", kind, got, want)
		}
	}
}
