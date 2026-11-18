package kanban

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// coordinate 的扫描窗口、stall 重查与失败终态（R1 T7）。

// 行为 8：run_stalled 消费前必须重查证据；NodeRun 已终态 → skipped、零模型调用。
func TestCoordinatorStallRecheck(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		name       string
		status     string
		wantEvent  string
		wantCalls  int
		wantReason string
	}{
		{name: "terminal node skipped", status: NodeRunSuccess, wantEvent: RouterEventSkipped, wantCalls: 0, wantReason: "no longer stalled"},
		{name: "running node processed", status: NodeRunRunning, wantEvent: RouterEventDone, wantCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCoordinateFixture(t, RouterModeCoordinate)
			f.stopLoop(t)
			f.agent.calls = []scriptedRouterCall{{tool: "router_say", args: map[string]any{"body": "停摆汇报"}}}
			f.addUserActivity(t, "uact-stall")
			f.addNodeRun(t, NodeRun{ID: "nr-stall", RunID: "run-stall", NodeID: "agent-1", Attempt: 1, Status: tc.status})
			ev := f.addEvent(t, RouterEvent{
				Kind: RouterEventRunStalled, RunID: "run-stall", TurnID: "stall-1",
				UserActivityID: "uact-stall", InstanceKey: "nr-stall:1",
				PayloadJSON: `{"node_run_id":"nr-stall","run_id":"run-stall"}`,
			})
			if _, err := f.router.ConsumeRouterEvents(ctx, now); err != nil {
				t.Fatalf("ConsumeRouterEvents: %v", err)
			}
			got := f.waitEvent(t, ev.ID, tc.wantEvent)
			if tc.wantReason != "" && !strings.Contains(got.LastError, tc.wantReason) {
				t.Fatalf("event reason = %q, want %q", got.LastError, tc.wantReason)
			}
			if calls := len(f.agent.sessions); calls != tc.wantCalls {
				t.Fatalf("model calls = %d, want %d", calls, tc.wantCalls)
			}
		})
	}
}

// 行为 11：有界扫描 = 1 小时窗口 ∪ 仍有 open NodeRun 的 Run。窗口外的终态 Run
// 不产事件（钩子是主路径，扫描只是兜底）。
func TestCoordinatorBoundedScanWindow(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.stopLoop(t)

	// 每任务只能绑定一个 Run（CreateWorkflowRunAndSetEngine 的迁移守卫），
	// 所以三条 Run 各挂一个任务。
	settle := func(taskID, runID string, updatedAt time.Time) {
		if taskID != "task-1" {
			f.addTask(t, taskID)
		}
		f.addRun(t, taskID, runID, WorkflowTriggeredByUser, map[string]string{"agent-1": NodeRunSuccess})
		f.markOwned(t, taskID, runID, "decision-"+runID)
		// 结算事实：T6 的终态判定优先看 Run 投影，投影没跑完时按「任务仍绑定该
		// Run 且任务已终态」补解释，这里就是那条路径。
		f.setTaskStatus(t, taskID, StatusSuccess)
		if _, err := f.store.db.ExecContext(ctx, `UPDATE workflow_runs SET updated_at = ? WHERE id = ?`,
			updatedAt.UTC().Format(routerTimeLayout), runID); err != nil {
			t.Fatalf("backdate run %s: %v", runID, err)
		}
	}
	settle("task-1", "run-old-terminal", now.Add(-2*time.Hour))
	settle("task-2", "run-recent-terminal", now.Add(-30*time.Minute))

	f.addTask(t, "task-3")
	f.addRun(t, "task-3", "run-old-open", WorkflowTriggeredByUser, map[string]string{})
	f.markOwned(t, "task-3", "run-old-open", "decision-run-old-open")
	f.addNodeRun(t, NodeRun{ID: "nr-gate", RunID: "run-old-open", NodeID: "gate-1", Attempt: 1, Status: NodeRunWaitingUser})
	if _, err := f.store.db.ExecContext(ctx, `UPDATE workflow_runs SET updated_at = ? WHERE id = ?`,
		now.Add(-2*time.Hour).Format(routerTimeLayout), "run-old-open"); err != nil {
		t.Fatalf("backdate run-old-open: %v", err)
	}

	res, err := f.svc.scanRouterEventSourcesBounded(ctx, f.root.ID, now, routerCoordinateScanWindow)
	must(t, err, "scanRouterEventSourcesBounded")
	settled := 0
	for _, ev := range res.Produced {
		switch {
		case ev.Kind == RouterEventRunSettled && ev.RunID == "run-recent-terminal":
			settled++
		case ev.Kind == RouterEventRunSettled && ev.RunID == "run-old-terminal":
			t.Fatalf("terminal run older than the window produced an event: %+v", ev)
		case ev.Kind == RouterEventGateWaiting && ev.RunID == "run-old-open":
			// 窗口外但仍有 open NodeRun：必须继续被扫描。
		default:
			t.Fatalf("unexpected event: %+v", ev)
		}
	}
	if settled != 1 {
		t.Fatalf("recent settled events = %d, want 1 (produced=%+v notes=%v)", settled, res.Produced, res.Notes)
	}
	again, err := f.svc.scanRouterEventSourcesBounded(ctx, f.root.ID, now, routerCoordinateScanWindow)
	must(t, err, "second bounded scan")
	if len(again.Produced) != 0 {
		t.Fatalf("repeat scan produced %d events, want 0", len(again.Produced))
	}
	gate := 0
	for _, ev := range f.list(t, "task-3") {
		if ev.Kind == RouterEventGateWaiting && ev.RunID == "run-old-open" {
			gate++
		}
	}
	if gate != 1 {
		t.Fatalf("gate_waiting events for the old open run = %d, want 1", gate)
	}
}

// modelFailureAgent 让运行期回合稳定报错，用于钉住「失败即终态」。
type modelFailureAgent struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (a *modelFailureAgent) RunRouterTurn(context.Context, RouterAgentRequest) (RouterAgentResult, error) {
	a.mu.Lock()
	a.calls++
	err := a.err
	a.mu.Unlock()
	if err == nil {
		err = errors.New("model failed")
	}
	return RouterAgentResult{}, err
}

func (a *modelFailureAgent) CloseRouterSession(context.Context, string, string) error { return nil }

// 行为 9：模型失败 → 决策与事件都落 failed；再消费不调模型、事件不留在 pending。
func TestCoordinatorModelFailureIsTerminal(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.stopLoop(t)
	agent := &modelFailureAgent{err: errors.New("router cli crashed")}
	f.svc.SetRouterAgent(agent)
	f.addUserActivity(t, "uact-fail")
	ev := f.addEvent(t, RouterEvent{Kind: RouterEventRunSettled, TurnID: "fail-1", UserActivityID: "uact-fail"})
	if _, err := f.router.ConsumeRouterEvents(ctx, now); err != nil {
		t.Fatalf("ConsumeRouterEvents: %v", err)
	}
	got := f.waitEvent(t, ev.ID, RouterEventFailed)
	if !strings.Contains(got.LastError, "router cli crashed") {
		t.Fatalf("event error = %q", got.LastError)
	}
	decisions := f.decisions(t, "task-1")
	if len(decisions) != 1 || decisions[0].Status != RouterDecisionFailed {
		t.Fatalf("decisions = %+v, want one failed decision", decisions)
	}
	if _, err := f.router.ConsumeRouterEvents(ctx, now); err != nil {
		t.Fatalf("second ConsumeRouterEvents: %v", err)
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if agent.calls != 1 {
		t.Fatalf("model calls = %d, want 1 (failed events must not be retried)", agent.calls)
	}
	if got := f.eventByID(t, "task-1", ev.ID).Status; got != RouterEventFailed {
		t.Fatalf("event status = %q, want failed", got)
	}
}
