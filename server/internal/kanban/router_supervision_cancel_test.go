package kanban

import (
	"context"
	"strings"
	"testing"
	"time"
)

// 用户裁定「解禁 cancel」：监管不再只会重试与汇报，可以把卡死的节点、这一轮 Run、
// 或整个任务停下来。取消是最小够用原则 —— 能停一个节点就不要停整轮。

// 行为：on 模式下 cancel_node 停掉卡住的节点，节点行落 cancelled，任务不因此
// 被判终态（用户还要看到现场并决定下一步）。
func TestSupervisionCancelNodeCancelsRunningNode(t *testing.T) {
	f := stalledRunFixture(t, RouterSupervisionOn)
	f.agent.calls = []scriptedRouterCall{{
		tool: "router_cancel_node",
		args: map[string]any{"node_id": "agent-1", "reason": "卡死十分钟"},
	}}
	ev := f.runStalledEvent(t, "cancel-node-turn")
	if _, err := f.router.ConsumeRouterEvents(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("ConsumeRouterEvents: %v", err)
	}
	f.waitEvent(t, ev.ID, RouterEventDone)

	nr, err := f.store.WorkflowNodeRunByKey(context.Background(), "run-1", "agent-1", "", 0, 1)
	if err != nil {
		t.Fatalf("WorkflowNodeRunByKey: %v", err)
	}
	if nr.Status != NodeRunCancelled {
		t.Fatalf("node run status = %q, want cancelled", nr.Status)
	}
	decision, err := NewRouterStore(f.store.db).DecisionByTurn(context.Background(), "task-1", "cancel-node-turn")
	if err != nil {
		t.Fatalf("DecisionByTurn: %v", err)
	}
	if decision.Status != RouterDecisionApplied {
		t.Fatalf("decision status = %q, want applied (%s)", decision.Status, decision.Error)
	}
	task, err := f.store.GetTask(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if isTerminalStatus(task.Status) {
		t.Fatalf("task status = %q, cancelling one node must not terminalize the task", task.Status)
	}
}

// 行为：cancel_run 停掉这一轮并冻结 Run，但任务回到 waiting_user 交给用户，
// 不是直接取消任务 —— 取消任务需要显式的 cancel_task。
func TestSupervisionCancelRunParksTaskAtWaitingUser(t *testing.T) {
	f := stalledRunFixture(t, RouterSupervisionOn)
	f.agent.calls = []scriptedRouterCall{{
		tool: "router_cancel_run",
		args: map[string]any{"reason": "节点卡死且没有可重试的路径"},
	}}
	ev := f.runStalledEvent(t, "cancel-run-turn")
	if _, err := f.router.ConsumeRouterEvents(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("ConsumeRouterEvents: %v", err)
	}
	f.waitEvent(t, ev.ID, RouterEventDone)

	run := f.runByID(t, "run-1")
	if run == nil {
		t.Fatal("run missing after cancel_run")
	}
	if state := run.NodeStates["agent-1"]; state != NodeRunCancelled {
		t.Fatalf("run projection agent-1 = %q, want cancelled", state)
	}
	task, err := f.store.GetTask(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != StatusWaitingUser {
		t.Fatalf("task status = %q, want waiting_user after cancel_run", task.Status)
	}
}

// 行为：审批等待中的 gate 节点不允许被 cancel_node 收掉 —— 用户裁定「不自动取消
// 等待审批的节点」，审批只能由人批准或驳回。
func TestSupervisionRefusesCancelOfGateWaitingNode(t *testing.T) {
	brief := &SituationBrief{Supervision: &SupervisionObservation{
		Mode: RouterSupervisionOn,
		OpenNodes: []SupervisionNodeState{{
			NodeID: "gate-1", NodeRunID: "nr-gate", NodeKind: NodeKindHumanGate,
			Status: NodeRunWaitingUser,
		}},
	}}
	err := validateSupervisionActions(brief, []RouterAction{{
		Kind: RouterActionCancelNode, NodeRunID: "nr-gate", Reason: "不想等了",
	}}, RouterConfig{Supervision: RouterSupervisionConfig{Mode: RouterSupervisionOn}})
	if err == nil {
		t.Fatal("cancel_node on a gate waiting for approval must be refused")
	}
	if !strings.Contains(err.Error(), "approval") && !strings.Contains(err.Error(), "human_gate") {
		t.Fatalf("error = %v, want it to name the approval gate", err)
	}
}

// 行为：取消是收口动作，不能与重试/开工同轮 —— 一轮里既取消又开工，等于把
// 没解释清楚的现场留给下一条执行。
func TestSupervisionRejectsCancelCombinedWithOtherWrites(t *testing.T) {
	brief := &SituationBrief{Supervision: &SupervisionObservation{
		Mode: RouterSupervisionOn,
		OpenNodes: []SupervisionNodeState{{
			NodeID: "agent-1", NodeRunID: "nr-1", NodeKind: NodeKindAgent, Status: NodeRunRunning,
		}},
	}}
	actions := []RouterAction{
		{Kind: RouterActionCancelNode, NodeRunID: "nr-1", Reason: "卡死", Seq: 0},
		{Kind: RouterActionResumeRun, Reason: "再推一把", Seq: 1},
	}
	err := validateSupervisionActions(brief, actions, RouterConfig{Supervision: RouterSupervisionConfig{Mode: RouterSupervisionOn}})
	if err == nil {
		t.Fatal("cancel_node combined with resume_run must be refused")
	}
	if !strings.Contains(err.Error(), "cancel") {
		t.Fatalf("error = %v, want it to name the cancel conflict", err)
	}
}

// 行为：终态任务拒绝 cancel_task —— 已经收尾的任务不需要取消，取消动作也不该
// 成为改写终态的旁路。
func TestSupervisionCancelTaskOnTerminalTaskIsRefused(t *testing.T) {
	f := stalledRunFixture(t, RouterSupervisionOn)
	f.setTaskStatus(t, "task-1", StatusSuccess)
	if err := f.svc.CancelTaskForRouter(context.Background(), f.root.ID, "task-1", "不跑了"); err == nil {
		t.Fatal("cancel_task on a success task must be refused")
	}
	task, err := f.store.GetTask(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != StatusSuccess {
		t.Fatalf("task status = %q, want it untouched", task.Status)
	}
}

// 行为：取消保留现场 —— 节点行与它的会话绑定都还在，只是状态变成 cancelled。
func TestSupervisionCancelKeepsNodeRunEvidence(t *testing.T) {
	f := stalledRunFixture(t, RouterSupervisionOn)
	f.agent.calls = []scriptedRouterCall{{
		tool: "router_cancel_run",
		args: map[string]any{"reason": "卡死，先停这一轮"},
	}}
	ev := f.runStalledEvent(t, "cancel-keep-turn")
	if _, err := f.router.ConsumeRouterEvents(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("ConsumeRouterEvents: %v", err)
	}
	f.waitEvent(t, ev.ID, RouterEventDone)

	nr, err := f.store.WorkflowNodeRunByKey(context.Background(), "run-1", "agent-1", "", 0, 1)
	if err != nil {
		t.Fatalf("WorkflowNodeRunByKey: %v", err)
	}
	if nr.Status != NodeRunCancelled {
		t.Fatalf("node run status = %q, want cancelled", nr.Status)
	}
	if nr.SessionKey != "sess-idle" {
		t.Fatalf("session key = %q, cancel must keep the session binding", nr.SessionKey)
	}
	if nr.StartedAt.IsZero() {
		t.Fatal("started_at was cleared; cancel must keep the attempt record")
	}
}
