package kanban

import (
	"context"
	"strings"
	"testing"
	"time"
)

// W1 T2：监管预算两个窗口。用户裁定「每 Run 12 个、每小时 20 个」，用尽只通知
// 不取消。计数口径是一个决策里每个**有副作用的监管动作**计 1，say / ask_user 不计。

func TestSupervisionBudgetDefaultsPerRunAndHour(t *testing.T) {
	cfg := RouterConfig{}.Normalized().Supervision
	if cfg.MaxActionsPerRun != defaultSupervisionMaxActionsPerRun {
		t.Fatalf("default actions/run = %d, want %d", cfg.MaxActionsPerRun, defaultSupervisionMaxActionsPerRun)
	}
	if cfg.MaxActionsPerHour != defaultSupervisionMaxActionsPerHour {
		t.Fatalf("default actions/hour = %d, want %d", cfg.MaxActionsPerHour, defaultSupervisionMaxActionsPerHour)
	}
	if cfg.MaxActionsPerRun != 12 || cfg.MaxActionsPerHour != 20 {
		t.Fatalf("defaults = run %d / hour %d, want 12 / 20", cfg.MaxActionsPerRun, cfg.MaxActionsPerHour)
	}
}

// 行为：本 Run 的监管动作预算用尽后，下一个动作不执行，任务停 waiting_user 并留
// 可见系统说明；预算用尽绝不自动取消任何节点。
func TestSupervisionRunBudgetExhaustedStopsAtWaitingUser(t *testing.T) {
	f := stalledRunFixture(t, RouterSupervisionOn)
	f.svc.SetRouterConfig(RouterConfig{
		Mode:      RouterModeCoordinate,
		IdleClose: time.Hour,
		Supervision: RouterSupervisionConfig{
			Mode: RouterSupervisionOn, MaxActionsPerRun: 2, MaxActionsPerHour: 20,
		},
	})
	seedSupervisionActions(t, f, "run-1", RouterTriggerRunStalled, 2)
	f.agent.calls = []scriptedRouterCall{{
		tool: "router_notify_user",
		args: map[string]any{"body": "还在推进，稍等"},
	}}
	ev := f.runStalledEvent(t, "run-budget-turn")
	if _, err := f.router.ConsumeRouterEvents(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("ConsumeRouterEvents: %v", err)
	}
	got := f.waitEvent(t, ev.ID, RouterEventDone)
	if !strings.Contains(got.LastError, "supervision_budget_exhausted") {
		t.Fatalf("event reason = %q, want supervision_budget_exhausted", got.LastError)
	}
	if !strings.Contains(got.LastError, "this run") {
		t.Fatalf("event reason = %q, want it to name the per-run quota", got.LastError)
	}
	task := f.task(t, "task-1")
	if task.Status != StatusWaitingUser {
		t.Fatalf("task status = %q, want waiting_user", task.Status)
	}
	if isTerminalStatus(task.Status) {
		t.Fatal("budget exhaustion must not terminalize the task")
	}
	nr, err := f.store.WorkflowNodeRunByKey(context.Background(), "run-1", "agent-1", "", 0, 1)
	if err != nil {
		t.Fatalf("WorkflowNodeRunByKey: %v", err)
	}
	if nr.Status != NodeRunRunning {
		t.Fatalf("node run status = %q; budget exhaustion must not cancel anything", nr.Status)
	}
	if !hasVisibleSystemLine(t, f, "预算") {
		t.Fatal("budget exhaustion left no visible system line")
	}
}

// 行为：小时额度按**动作**计数，不按「决策回合」计数 —— 一个回合里两个副作用
// 动作要算两个。旧口径（每回合算 1）会让这里放过第三个动作。
func TestSupervisionHourlyBudgetCountsActionsNotTurns(t *testing.T) {
	f := supervisionFixture(t, RouterSupervisionOn)
	f.svc.SetRouterConfig(RouterConfig{
		Mode:      RouterModeCoordinate,
		IdleClose: time.Hour,
		Supervision: RouterSupervisionConfig{
			Mode: RouterSupervisionOn, MaxActionsPerHour: 2,
		},
	})
	// 一个已应用的决策，里面两个 notify_user：小时额度应已用掉 2/2。
	seedSupervisionActions(t, f, "run-1", RouterTriggerRunFailed, 2)
	f.agent.calls = []scriptedRouterCall{{
		tool: "router_notify_user",
		args: map[string]any{"body": "再说明一次"},
	}}
	ev := f.runFailedEvent(t, "hour-budget-turn")
	if _, err := f.router.ConsumeRouterEvents(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("ConsumeRouterEvents: %v", err)
	}
	got := f.waitEvent(t, ev.ID, RouterEventDone)
	if !strings.Contains(got.LastError, "supervision_budget_exhausted") {
		t.Fatalf("event reason = %q, want supervision_budget_exhausted", got.LastError)
	}
	if !strings.Contains(got.LastError, "last hour") {
		t.Fatalf("event reason = %q, want it to name the hourly quota", got.LastError)
	}
}

// 行为：say / ask_user / silent 不是副作用动作，预算满了也照常可用。
func TestSupervisionBudgetIgnoresSayAndAsk(t *testing.T) {
	brief := &SituationBrief{Supervision: &SupervisionObservation{
		Mode:            RouterSupervisionOn,
		ActionsThisRun:  defaultSupervisionMaxActionsPerRun,
		ActionsLastHour: defaultSupervisionMaxActionsPerHour,
	}}
	cfg := RouterConfig{Supervision: RouterSupervisionConfig{Mode: RouterSupervisionOn}}
	talk := []RouterAction{
		{Kind: RouterActionSay, Body: "进展：仍在执行"},
		{Kind: RouterActionAskUser, Body: "要继续等吗？"},
	}
	if err := validateSupervisionActions(brief, talk, cfg); err != nil {
		t.Fatalf("say/ask must not be budgeted: %v", err)
	}
	// 正对照：同样的观测下补一个有副作用的动作就必须被拒。
	write := append(append([]RouterAction{}, talk...), RouterAction{Kind: RouterActionNotifyUser, Body: "再提醒一次"})
	if err := validateSupervisionActions(brief, write, cfg); err == nil {
		t.Fatal("notify_user past the exhausted budget must be refused")
	}
}

// seedSupervisionActions 造一个已应用的决策，里面 n 个副作用动作 —— 刻意放在同
// 一个回合里，好让「按动作计数」与「按回合计数」两种口径给出不同的数字。
func seedSupervisionActions(t *testing.T, f *coordinateFixture, runID string, trigger RouterTriggerKind, n int) {
	t.Helper()
	actions := make([]RouterAction, 0, n)
	for i := 0; i < n; i++ {
		actions = append(actions, RouterAction{Kind: RouterActionNotifyUser, Body: "进展", Seq: i})
	}
	decision := &RouterDecision{
		ID:          "seed-" + runID,
		TaskID:      "task-1",
		RootID:      f.root.ID,
		TriggerKind: trigger,
		TriggerRef:  runID,
		TurnID:      "seed-turn-" + runID,
		Actions:     actions,
		Status:      RouterDecisionApplied,
		CreatedAt:   time.Now().UTC().Add(-time.Minute),
	}
	if err := NewRouterStore(f.store.db).InsertDecision(context.Background(), decision); err != nil {
		t.Fatalf("InsertDecision: %v", err)
	}
}

func hasVisibleSystemLine(t *testing.T, f *coordinateFixture, needle string) bool {
	t.Helper()
	acts, err := NewActivityStore(f.store.db).List(context.Background(), "task-1")
	must(t, err, "List activities")
	for _, act := range acts {
		if act.Type == ActivityTypeAttention && strings.Contains(act.Body, needle) {
			return true
		}
	}
	return false
}
