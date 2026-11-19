package kanban

import (
	"context"
)

// 末端 human_gate（gate 没有出边）是 2026-11-19 用户裁定要禁止的形态：审批
// 只能拦在执行中间。定义写入由 ValidateWorkflowDefinitionForAuthoring 拒绝，
// 但已发布的旧版本（workflow-new-feature:v1 的 verify）仍然要能建 Run，所以
// 运行期在这里统一按「通过」收尾：
//   - 还没物化的末端 gate：直接记 success，不落等待卡；
//   - 已经停在 waiting_user 的末端 gate（升级前留下的）：下一次推进时兼容收尾。

// workflowNodeIsTerminal reports whether nodeID has no outgoing edge.
func workflowNodeIsTerminal(graph WorkflowDefinition, nodeID string) bool {
	for _, e := range graph.Edges {
		if e.From == nodeID {
			return false
		}
	}
	return true
}

// autoPassTerminalGate commits the fresh-terminal-gate transition. It writes no
// gate activity: a card whose only buttons lead nowhere is exactly what the
// user asked to remove.
func (s *Service) autoPassTerminalGate(ctx context.Context, store *TaskStore, run *WorkflowRun, node WorkflowNode) (bool, error) {
	prompt, _ := node.Params["prompt"].(string)
	nr := workflowAttemptNodeRun(run, node)
	nr.Status = NodeRunSuccess
	nr.Output = map[string]any{"prompt": prompt, "auto_approved": true}
	if _, _, err := store.CommitWorkflowNodeTransition(ctx, run, WorkflowNodeTransition{
		NodeRun:      nr,
		EventType:    WorkflowEventNodeSucceeded,
		EventPayload: map[string]any{"auto_approved": true},
	}); err != nil {
		return false, err
	}
	return true, nil
}

// hasParkedTerminalHumanGate 供启动对账判断「这次重启要不要唤醒 driver」。
func hasParkedTerminalHumanGate(run *WorkflowRun) bool {
	if run == nil {
		return false
	}
	for _, node := range run.Graph.Nodes {
		if node.Kind == NodeKindHumanGate && workflowNodeIsTerminal(run.Graph, node.ID) &&
			run.NodeStates[node.ID] == NodeRunWaitingUser {
			return true
		}
	}
	return false
}

// healParkedTerminalGates releases a legacy terminal gate that is still parked
// in waiting_user. It runs before EngineReadyNodes: a waiting row is invisible
// to the engine, so without this scan the card would wait forever.
func (s *Service) healParkedTerminalGates(ctx context.Context, store *TaskStore, run *WorkflowRun) (bool, error) {
	for _, node := range run.Graph.Nodes {
		if node.Kind != NodeKindHumanGate || !workflowNodeIsTerminal(run.Graph, node.ID) {
			continue
		}
		if run.NodeStates[node.ID] != NodeRunWaitingUser {
			continue
		}
		nr, err := store.WorkflowNodeRunByKey(ctx, run.ID, node.ID, "", 0, 1)
		if err != nil {
			return false, err
		}
		_, changed, err := store.AutoApproveTerminalHumanGate(ctx, run, nr)
		if err != nil {
			return false, err
		}
		if !changed {
			return false, nil
		}
		if gate, gateErr := NewActivityStore(store.db).Get(ctx, "wf:"+nr.ID+":human_waiting"); gateErr == nil {
			s.broadcastWorkflowActivity(ctx, store, gate, false, map[string]any{
				"status":       gate.Status,
				"payload_json": gate.PayloadJSON,
			})
		}
		return true, nil
	}
	return false, nil
}
