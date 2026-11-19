package kanban

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// deliver 工具必须**在当轮**拒绝没有唯一活动节点的投递，而不是无脑回
// {"status":"recorded"} 等回合结束后再把整条决策 rejected。
//
// 线上动机（2026-11-19 task_2ea72d70e560f8e3）：模型答的是「我希望是A」，
// 上游节点 `input` 停在 waiting_user；deliver 被 recorded，回合末尾
// validateRouterActions 以 "deliver requires exactly one active node, got 0"
// 拒绝整条决策 → fallbackRoute 回落旧路径 → 旧路径把整条 Run 冻结成
// cancelled（`run-be0aa7f7228a67a7` 四个节点全 cancelled，勘察结论全丢）。
//
// 工具错误会直接回给模型（cli 侧 ErrorResult 分支），模型当轮就能改口成
// say/ask_user。所以闸门的判据是「这一轮有没有可投递目标」，而不是「模型
// 有没有权限调这个工具」。

// briefFor 取**真实任务行**建 brief：活动执行段只在任务处于活动状态时才挂，
// 手搓一个 Status 为空的 Task 会让 ActiveExecution 恒 nil，测出的东西没意义。
func briefFor(t *testing.T, store *TaskStore, taskID string) *SituationBrief {
	t.Helper()
	task, err := store.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	brief, err := buildSituationBrief(context.Background(), store, nil, RouterConfig{}.Normalized(), task, RouterTriggerUserMessage, "")
	if err != nil {
		t.Fatalf("buildSituationBrief: %v", err)
	}
	return brief
}

func TestRouterDeliverToolRejectsWithoutSingleActiveNode(t *testing.T) {
	ctx := context.Background()
	handlerOf := func(t *testing.T, env *routerToolEnv) func() error {
		t.Helper()
		var spec *RouterToolSpec
		for _, candidate := range env.tools() {
			if candidate.Name == "router_deliver_to_active_run" {
				spec = &candidate
			}
		}
		if spec == nil {
			t.Fatal("router_deliver_to_active_run is not exposed")
		}
		return func() error {
			raw, err := json.Marshal(map[string]any{"body": "我希望是A", "reason": "回答停住的输入节点"})
			if err != nil {
				t.Fatalf("marshal args: %v", err)
			}
			_, err = spec.Handler(ctx, raw)
			return err
		}
	}

	t.Run("blocked_agent_node_explains_retry", func(t *testing.T) {
		store, _, run, _ := newWorkflowCommentFixture(t, "task-deliver-blocked", "run-deliver-blocked", workflowCommentTestGraph)
		// 与线上同形：`build` 交出 blocked，节点行停在 waiting_user。
		blocked := workflowAttemptNodeRun(run, run.Graph.Nodes[0])
		blocked.Status = NodeRunWaitingUser
		blocked.Output = map[string]any{"outcome": "blocked", "reason": "等用户选 A 还是 B"}
		if _, _, err := store.CommitWorkflowNodeTransition(ctx, run, WorkflowNodeTransition{
			NodeRun: blocked, EventType: WorkflowEventNodeBlocked,
			EventPayload: map[string]any{"outcome": "blocked", "reason": "等用户选 A 还是 B"},
		}); err != nil {
			t.Fatalf("CommitWorkflowNodeTransition: %v", err)
		}
		if err := store.UpdateTaskStatus(ctx, run.TaskID, StatusWaitingUser, false); err != nil {
			t.Fatalf("UpdateTaskStatus: %v", err)
		}
		brief := briefFor(t, store, run.TaskID)
		if brief.ActiveExecution == nil || len(brief.ActiveExecution.CurrentNodes) != 0 {
			t.Fatalf("fixture is not the zero-active shape: %#v", brief.ActiveExecution)
		}
		err := handlerOf(t, &routerToolEnv{store: store, brief: brief, collector: newRouterTurnCollector(RouterConfig{})})()
		if err == nil {
			t.Fatal("deliver against a stopped agent node was recorded instead of rejected")
		}
		msg := err.Error()
		if !explainsUndeliverable(msg) || !strings.Contains(msg, "router_answer_waiting_node") {
			t.Fatalf("rejection %q must explain why and point at the verb that does deliver", msg)
		}
	})

	t.Run("human_gate_explains_approval", func(t *testing.T) {
		store, svc, run, _ := newWorkflowCommentFixture(t, "task-deliver-gate", "run-deliver-gate", humanGateGraph())
		if err := svc.AdvanceWorkflowRun(ctx, store, run.TaskID); err != nil {
			t.Fatalf("AdvanceWorkflowRun: %v", err)
		}
		brief := briefFor(t, store, run.TaskID)
		err := handlerOf(t, &routerToolEnv{store: store, brief: brief, collector: newRouterTurnCollector(RouterConfig{})})()
		if err == nil {
			t.Fatal("deliver during HumanGate was recorded instead of rejected")
		}
		if !strings.Contains(err.Error(), "审批") {
			t.Fatalf("rejection %q must point at the approval gate", err.Error())
		}
	})

	t.Run("single_active_node_is_recorded", func(t *testing.T) {
		store, _, run, _ := newWorkflowCommentFixture(t, "task-deliver-one", "run-deliver-one", workflowCommentTestGraph)
		startWorkflowAgentNode(t, store, run.ID, "build", "sess-build")
		if err := store.UpdateTaskStatus(ctx, run.TaskID, StatusRunning, false); err != nil {
			t.Fatalf("UpdateTaskStatus: %v", err)
		}
		brief := briefFor(t, store, run.TaskID)
		collector := newRouterTurnCollector(RouterConfig{})
		if err := handlerOf(t, &routerToolEnv{store: store, brief: brief, collector: collector})(); err != nil {
			t.Fatalf("deliver against the single active node: %v", err)
		}
		actions := collector.snapshot()
		if len(actions) != 1 || actions[0].Kind != RouterActionDeliver {
			t.Fatalf("actions = %#v, want one recorded deliver", actions)
		}
	})

	t.Run("several_active_nodes_are_not_fanned_out", func(t *testing.T) {
		store, _, run, _ := newWorkflowCommentFixture(t, "task-deliver-two", "run-deliver-two", workflowCommentTestGraph)
		startWorkflowAgentNode(t, store, run.ID, "build", "sess-build")
		startWorkflowAgentNode(t, store, run.ID, "review", "sess-review")
		if err := store.UpdateTaskStatus(ctx, run.TaskID, StatusRunning, false); err != nil {
			t.Fatalf("UpdateTaskStatus: %v", err)
		}
		brief := briefFor(t, store, run.TaskID)
		err := handlerOf(t, &routerToolEnv{store: store, brief: brief, collector: newRouterTurnCollector(RouterConfig{})})()
		if err == nil || !strings.Contains(err.Error(), "2 个活动 Agent 节点") {
			t.Fatalf("err = %v, want the fan-out rejection", err)
		}
	})
}
