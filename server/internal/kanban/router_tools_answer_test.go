package kanban

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// router_answer_waiting_node 是「答复真的落进节点」的写入工具：它把用户的
// 回答绑到那个停在 waiting_user 的 Agent 节点上，由服务建下一次尝试带着答复重跑。
//
// 闸门必须在**当轮**判掉。回合末的 validateRouterActions 拒绝会让整条决策
// rejected，随后 fallbackRoute 回落旧路径并 supersede 整条 Run —— 2026-11-19
// task_2ea72d70e560f8e3 的勘察结论就是这么被作废的（见 dad415bf）。

// seedBlockedNodeRun 造一个与线上同形的节点行：Agent 交回 blocked，节点行停在
// waiting_user，输出里带着它当时问用户的问题。
func seedBlockedNodeRun(t *testing.T, store *TaskStore, run *WorkflowRun, nodeID, reason string) NodeRun {
	t.Helper()
	node, ok := run.Graph.node(nodeID)
	if !ok {
		t.Fatalf("graph has no node %q", nodeID)
	}
	nr := workflowAttemptNodeRun(run, node)
	nr.Status = NodeRunWaitingUser
	nr.Output = map[string]any{"outcome": "blocked", "reason": reason}
	if _, _, err := store.CommitWorkflowNodeTransition(context.Background(), run, WorkflowNodeTransition{
		NodeRun: nr, EventType: WorkflowEventNodeBlocked,
		EventPayload: map[string]any{"outcome": "blocked", "reason": reason},
	}); err != nil {
		t.Fatalf("CommitWorkflowNodeTransition(%s): %v", nodeID, err)
	}
	return nr
}

func answerToolHandler(t *testing.T, env *routerToolEnv) func(nodeID string) error {
	t.Helper()
	var spec *RouterToolSpec
	for _, candidate := range env.tools() {
		if candidate.Name == "router_answer_waiting_node" {
			spec = &candidate
		}
	}
	if spec == nil {
		t.Fatal("router_answer_waiting_node is not exposed")
	}
	return func(nodeID string) error {
		args := map[string]any{"body": "我希望是A", "reason": "回答停住的节点"}
		if nodeID != "" {
			args["node_id"] = nodeID
		}
		raw, err := json.Marshal(args)
		if err != nil {
			t.Fatalf("marshal args: %v", err)
		}
		_, err = spec.Handler(context.Background(), raw)
		return err
	}
}

func TestRouterAnswerWaitingNodeToolGate(t *testing.T) {
	ctx := context.Background()

	t.Run("no_waiting_node_explains", func(t *testing.T) {
		store, _, run, _ := newWorkflowCommentFixture(t, "task-answer-none", "run-answer-none", workflowCommentTestGraph)
		brief := briefFor(t, store, run.TaskID)
		err := answerToolHandler(t, &routerToolEnv{store: store, brief: brief, collector: newRouterTurnCollector(RouterConfig{})})("")
		if err == nil || !strings.Contains(err.Error(), "没有停住") {
			t.Fatalf("err = %v, want an explanation that nothing is waiting", err)
		}
	})

	t.Run("human_gate_points_at_approval", func(t *testing.T) {
		store, svc, run, _ := newWorkflowCommentFixture(t, "task-answer-gate", "run-answer-gate", humanGateGraph())
		if err := svc.AdvanceWorkflowRun(ctx, store, run.TaskID); err != nil {
			t.Fatalf("AdvanceWorkflowRun: %v", err)
		}
		brief := briefFor(t, store, run.TaskID)
		err := answerToolHandler(t, &routerToolEnv{store: store, brief: brief, collector: newRouterTurnCollector(RouterConfig{})})("")
		if err == nil || !strings.Contains(err.Error(), "审批") {
			t.Fatalf("err = %v, want the approval-gate rejection", err)
		}
	})

	t.Run("several_waiting_nodes_need_a_target", func(t *testing.T) {
		store, _, run, _ := newWorkflowCommentFixture(t, "task-answer-two", "run-answer-two", workflowCommentTestGraph)
		seedBlockedNodeRun(t, store, run, "build", "等用户选 A 还是 B")
		review := seedBlockedNodeRun(t, store, run, "review", "等用户确认范围")
		if err := store.UpdateTaskStatus(ctx, run.TaskID, StatusWaitingUser, false); err != nil {
			t.Fatalf("UpdateTaskStatus: %v", err)
		}
		brief := briefFor(t, store, run.TaskID)
		env := &routerToolEnv{store: store, brief: brief, collector: newRouterTurnCollector(RouterConfig{})}
		err := answerToolHandler(t, env)("")
		if err == nil || !strings.Contains(err.Error(), "build") || !strings.Contains(err.Error(), "review") {
			t.Fatalf("err = %v, want both candidate node ids listed", err)
		}
		if err := answerToolHandler(t, env)("review"); err != nil {
			t.Fatalf("answering the explicitly named node: %v", err)
		}
		actions := env.collector.snapshot()
		if len(actions) != 1 || actions[0].Kind != RouterActionAnswerWaiting || actions[0].NodeRunID != review.ID {
			t.Fatalf("actions = %#v, want one answer bound to %s", actions, review.ID)
		}
	})

	t.Run("running_node_blocks_answering", func(t *testing.T) {
		store, _, run, _ := newWorkflowCommentFixture(t, "task-answer-busy", "run-answer-busy", workflowCommentTestGraph)
		seedBlockedNodeRun(t, store, run, "build", "等用户选 A 还是 B")
		startWorkflowAgentNode(t, store, run.ID, "review", "sess-review")
		if err := store.UpdateTaskStatus(ctx, run.TaskID, StatusRunning, false); err != nil {
			t.Fatalf("UpdateTaskStatus: %v", err)
		}
		brief := briefFor(t, store, run.TaskID)
		err := answerToolHandler(t, &routerToolEnv{store: store, brief: brief, collector: newRouterTurnCollector(RouterConfig{})})("")
		if err == nil || !strings.Contains(err.Error(), "正在执行") {
			t.Fatalf("err = %v, want the busy rejection", err)
		}
	})

	t.Run("single_waiting_node_is_recorded", func(t *testing.T) {
		store, _, run, _ := newWorkflowCommentFixture(t, "task-answer-one", "run-answer-one", workflowCommentTestGraph)
		blocked := seedBlockedNodeRun(t, store, run, "build", "等用户选 A 还是 B")
		if err := store.UpdateTaskStatus(ctx, run.TaskID, StatusWaitingUser, false); err != nil {
			t.Fatalf("UpdateTaskStatus: %v", err)
		}
		brief := briefFor(t, store, run.TaskID)
		env := &routerToolEnv{store: store, brief: brief, collector: newRouterTurnCollector(RouterConfig{})}
		if err := answerToolHandler(t, env)(""); err != nil {
			t.Fatalf("answer against the single waiting node: %v", err)
		}
		actions := env.collector.snapshot()
		if len(actions) != 1 || actions[0].Kind != RouterActionAnswerWaiting || actions[0].Body != "我希望是A" || actions[0].NodeRunID != blocked.ID {
			t.Fatalf("actions = %#v, want one answer bound to %s", actions, blocked.ID)
		}
	})
}
