package kanban

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Router 主动监管 S1/S2：影子只记录、不改状态；on 允许「重试失败节点」与
// 「通知用户」，预算用尽或证据不确定时停手。

func supervisionFixture(t *testing.T, mode RouterSupervisionMode) *coordinateFixture {
	t.Helper()
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.svc.SetRouterConfig(RouterConfig{
		Mode:        RouterModeCoordinate,
		IdleClose:   time.Hour,
		Supervision: RouterSupervisionConfig{Mode: mode},
	})
	f.stopLoop(t)
	// markOwned 会建出 Router-owned 判据所需的用户留言（uact-run-1）与回复行，
	// 不能再单独插一条同 ID 的留言。
	f.addRun(t, "task-1", "run-1", WorkflowTriggeredByUser, map[string]string{"agent-1": NodeRunFail})
	f.markOwned(t, "task-1", "run-1", "decision-1")
	f.setTaskStatus(t, "task-1", StatusFail)
	f.addNodeRun(t, NodeRun{ID: "nr-1", RunID: "run-1", NodeID: "agent-1", Status: NodeRunFail,
		Attempt: 1, StartedAt: time.Now().Add(-3 * time.Minute), FinishedAt: time.Now().Add(-2 * time.Minute),
		Output: map[string]any{"error": "503 Service Unavailable"}})
	return f
}

func (f *coordinateFixture) runFailedEvent(t *testing.T, turnID string) RouterEvent {
	t.Helper()
	return f.addEvent(t, RouterEvent{
		Kind: RouterEventRunFailed, RunID: "run-1", TurnID: turnID,
		UserActivityID: "uact-run-1", InstanceKey: turnID,
		PayloadJSON: `{"run_id":"run-1","task_id":"task-1"}`,
	})
}

// 行为：shadow 下两个新动作都只落决策，没有重试行、没有系统通知行，
// 事件标 done 且理由写明是影子；已有的汇报类动作照常应用。
func TestSupervisionShadowRecordsWithoutSideEffects(t *testing.T) {
	f := supervisionFixture(t, RouterSupervisionShadow)
	f.agent.calls = []scriptedRouterCall{
		{tool: "router_retry_node", args: map[string]any{"node_id": "agent-1", "reason": "上游 503"}},
		{tool: "router_notify_user", args: map[string]any{"body": "agent-1 失败，建议重试"}},
	}
	ev := f.runFailedEvent(t, "shadow-turn")
	if _, err := f.router.ConsumeRouterEvents(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("ConsumeRouterEvents: %v", err)
	}
	got := f.waitEvent(t, ev.ID, RouterEventDone)
	if !strings.Contains(got.LastError, "supervision_shadow") {
		t.Fatalf("event reason = %q, want supervision_shadow", got.LastError)
	}
	decision, err := NewRouterStore(f.store.db).DecisionByTurn(context.Background(), "task-1", "shadow-turn")
	if err != nil {
		t.Fatalf("DecisionByTurn: %v", err)
	}
	if decision.Status != RouterDecisionShadow {
		t.Fatalf("decision status = %q, want shadow", decision.Status)
	}
	if len(decision.Actions) != 2 {
		t.Fatalf("recorded actions = %+v, want both supervision actions", decision.Actions)
	}
	if _, err := f.store.WorkflowNodeRunByKey(context.Background(), "run-1", "agent-1", "", 0, 2); err == nil {
		t.Fatal("shadow created a retry NodeRun")
	}
	acts, err := NewActivityStore(f.store.db).List(context.Background(), "task-1")
	must(t, err, "List activities")
	for _, act := range acts {
		if act.Role == "system" {
			t.Fatalf("shadow wrote a visible system row: %+v", act)
		}
	}
}

// 行为：on 下 retry_node 真的建 attempt+1（链接原行、理由 supervisor_retry），
// 成功调用端口前先校验节点确实可重试。
func TestSupervisionOnRetriesFailedNode(t *testing.T) {
	f := supervisionFixture(t, RouterSupervisionOn)
	f.svc.SetWorkflowNodeRunner(newRuntimeRunner(-1))
	f.agent.calls = []scriptedRouterCall{
		{tool: "router_retry_node", args: map[string]any{"node_id": "agent-1", "reason": "上游 503"}},
	}
	ev := f.runFailedEvent(t, "retry-turn")
	if _, err := f.router.ConsumeRouterEvents(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("ConsumeRouterEvents: %v", err)
	}
	f.waitEvent(t, ev.ID, RouterEventDone)
	retried, err := f.store.WorkflowNodeRunByKey(context.Background(), "run-1", "agent-1", "", 0, 2)
	must(t, err, "retry NodeRun")
	if retried.PrevAttemptID != "nr-1" {
		t.Fatalf("prev attempt = %q, want nr-1", retried.PrevAttemptID)
	}
	var reason string
	err = f.store.db.QueryRowContext(context.Background(),
		`SELECT json_extract(payload_json,'$.reason') FROM workflow_run_events WHERE run_id=? AND type=? AND node_id=? ORDER BY seq DESC LIMIT 1`,
		"run-1", WorkflowEventNodeRetried, "agent-1").Scan(&reason)
	must(t, err, "node_retried ledger")
	if reason != "supervisor_retry" {
		t.Fatalf("ledger reason = %q, want supervisor_retry", reason)
	}
	task := f.task(t, "task-1")
	if task.Status != StatusRunning {
		t.Fatalf("task status = %q, want running after retry", task.Status)
	}
	decision, err := NewRouterStore(f.store.db).DecisionByTurn(context.Background(), "task-1", "retry-turn")
	must(t, err, "DecisionByTurn")
	if decision.Status != RouterDecisionApplied {
		t.Fatalf("decision status = %q, want applied", decision.Status)
	}
	waitRuntimeIdle(t, f.svc, f.root.ID, "task-1")
}

// 行为：证据不确定（unknown + worktree_error）一律拒绝自动重试。
func TestSupervisionOnRefusesRetryForUncertainFailure(t *testing.T) {
	f := supervisionFixture(t, RouterSupervisionOn)
	f.saveStates(t, "run-1", map[string]string{"agent-1": NodeRunUnknown})
	f.addNodeRun(t, NodeRun{ID: "nr-unknown", RunID: "run-1", NodeID: "agent-1", Status: NodeRunUnknown,
		Attempt: 2, PrevAttemptID: "nr-1", Output: map[string]any{"worktree_error": "merge conflict while closing"}})
	f.agent.calls = []scriptedRouterCall{
		{tool: "router_retry_node", args: map[string]any{"node_id": "agent-1", "reason": "试试"}},
	}
	ev := f.runFailedEvent(t, "uncertain-turn")
	if _, err := f.router.ConsumeRouterEvents(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("ConsumeRouterEvents: %v", err)
	}
	got := f.waitEvent(t, ev.ID, RouterEventFailed)
	if !strings.Contains(got.LastError, "uncertain") {
		t.Fatalf("event reason = %q, want mention of uncertain", got.LastError)
	}
	// attempt 2 是本用例自己造的 unknown 行；重试会再开 attempt 3。
	if _, err := f.store.WorkflowNodeRunByKey(context.Background(), "run-1", "agent-1", "", 0, 3); err == nil {
		t.Fatal("uncertain failure was retried")
	}
}

// 行为：预算用尽不再重试，任务停在 waiting_user 并留一条可见系统说明。
func TestSupervisionOnBudgetExhaustedStopsAtWaitingUser(t *testing.T) {
	f := supervisionFixture(t, RouterSupervisionOn)
	f.addNodeRun(t, NodeRun{ID: "nr-2", RunID: "run-1", NodeID: "agent-1", Status: NodeRunFail,
		Attempt: 2, PrevAttemptID: "nr-1", Output: map[string]any{"error": "503 Service Unavailable"}})
	f.addNodeRun(t, NodeRun{ID: "nr-3", RunID: "run-1", NodeID: "agent-1", Status: NodeRunFail,
		Attempt: 3, PrevAttemptID: "nr-2", Output: map[string]any{"error": "503 Service Unavailable"}})
	f.agent.calls = []scriptedRouterCall{
		{tool: "router_retry_node", args: map[string]any{"node_id": "agent-1", "reason": "再试一次"}},
	}
	ev := f.runFailedEvent(t, "budget-turn")
	if _, err := f.router.ConsumeRouterEvents(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("ConsumeRouterEvents: %v", err)
	}
	got := f.waitEvent(t, ev.ID, RouterEventDone)
	if !strings.Contains(got.LastError, "supervision_budget_exhausted") {
		t.Fatalf("event reason = %q, want supervision_budget_exhausted", got.LastError)
	}
	if _, err := f.store.WorkflowNodeRunByKey(context.Background(), "run-1", "agent-1", "", 0, 4); err == nil {
		t.Fatal("budget-exhausted turn still created attempt 4")
	}
	task := f.task(t, "task-1")
	if task.Status != StatusWaitingUser {
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
}

// 行为：on 下 notify_user 落一条可见系统行；同一决策重放不会多出第二行。
func TestSupervisionOnNotifyUserIsIdempotent(t *testing.T) {
	f := supervisionFixture(t, RouterSupervisionOn)
	f.agent.calls = []scriptedRouterCall{
		{tool: "router_notify_user", args: map[string]any{"body": "agent-1 连续失败，建议人工确认"}},
	}
	ev := f.runFailedEvent(t, "notify-turn")
	if _, err := f.router.ConsumeRouterEvents(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("ConsumeRouterEvents: %v", err)
	}
	f.waitEvent(t, ev.ID, RouterEventDone)
	count := func() int {
		acts, err := NewActivityStore(f.store.db).List(context.Background(), "task-1")
		must(t, err, "List activities")
		n := 0
		for _, act := range acts {
			if act.Role == "system" && strings.Contains(act.Body, "建议人工确认") {
				n++
			}
		}
		return n
	}
	if got := count(); got != 1 {
		t.Fatalf("system rows = %d, want 1", got)
	}
	decision, err := NewRouterStore(f.store.db).DecisionByTurn(context.Background(), "task-1", "notify-turn")
	must(t, err, "DecisionByTurn")
	// 运行期事件回合「先写效果、后推游标」：崩在中间就会重放同一个决策。
	must(t, f.activity.ApplyRouteActions(context.Background(), decision), "replay ApplyRouteActions")
	if got := count(); got != 1 {
		t.Fatalf("system rows after replay = %d, want 1", got)
	}
}

// 行为：同一个失败行被重复要求重试时只开一个 attempt、只写一条账本。
func TestSupervisionRetryIsIdempotent(t *testing.T) {
	f := supervisionFixture(t, RouterSupervisionOn)
	first, err := f.svc.RetryFailedWorkflowNode(context.Background(), f.root.ID, "task-1", "nr-1", supervisionRetryReason)
	must(t, err, "first retry")
	second, err := f.svc.RetryFailedWorkflowNode(context.Background(), f.root.ID, "task-1", "nr-1", supervisionRetryReason)
	must(t, err, "second retry")
	if second.ID != first.ID {
		t.Fatalf("second retry created %s, want existing %s", second.ID, first.ID)
	}
	events, err := f.store.ListWorkflowRunEvents(context.Background(), "run-1")
	must(t, err, "ListWorkflowRunEvents")
	ledger := 0
	for _, ev := range events {
		if ev.Type == WorkflowEventNodeRetried && ev.NodeID == "agent-1" {
			ledger++
		}
	}
	if ledger != 1 {
		t.Fatalf("node_retried ledger rows = %d, want 1", ledger)
	}
}

// 行为：监管工具只在带 supervision 块的事件回合暴露；普通留言回合看不到。
func TestSupervisionToolsOnlyOnSupervisedEventTurns(t *testing.T) {
	plain := &routerToolEnv{brief: &SituationBrief{Task: RouterBriefTask{ID: "task-1"}}}
	for _, tool := range plain.tools() {
		if tool.Name == "router_retry_node" || tool.Name == "router_notify_user" {
			t.Fatalf("plain turn exposes %s", tool.Name)
		}
	}
	supervised := &routerToolEnv{brief: &SituationBrief{
		Task:        RouterBriefTask{ID: "task-1"},
		Supervision: &SupervisionObservation{Mode: RouterSupervisionOn, RunID: "run-1"},
	}}
	names := map[string]bool{}
	for _, tool := range supervised.tools() {
		names[tool.Name] = true
	}
	if !names["router_retry_node"] || !names["router_notify_user"] {
		t.Fatalf("supervised turn tools = %v", names)
	}
}
