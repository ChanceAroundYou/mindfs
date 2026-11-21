package kanban

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
)

// W2 T2：节点级管理的服务侧实现（Router 的两个写动作）。
//
// 共同前置：任务未终态、本轮 Run 存在、节点是图里的顶层 agent 节点。
// 「运行中不切换」在这里再判一次 —— 工具层用的是当轮卡片，卡片到 apply 之间
// 节点可能已经跑起来了，所以以**行**为准（最新一次 attempt 还在 queued/running）。

// SetWorkflowNodeModelForRouter 把节点模型档写进本轮 Run 的图（params.model），
// 下一次 attempt 生效。失败 / 未跑过的节点允许改；运行中拒绝；已收尾的节点也
// 允许改 —— 改档本身不触发重跑，要重跑得再发 retry_node，或开新一轮。
func (s *Service) SetWorkflowNodeModelForRouter(ctx context.Context, rootID, taskID, nodeID, model, reason string) error {
	if s == nil {
		return errors.New("kanban service not configured")
	}
	store, task, err := s.loadForMove(ctx, rootID, taskID)
	if err != nil {
		return err
	}
	if isTerminalStatus(task.Status) {
		return fmt.Errorf("%w: task is %s", ErrWorkflowNodeRunNotActionable, task.Status)
	}
	nodeID, model = strings.TrimSpace(nodeID), strings.TrimSpace(model)
	if nodeID == "" || model == "" {
		return errors.New("node id and model required")
	}
	if len(model) > 64 {
		return fmt.Errorf("model name too long (max 64 bytes)")
	}
	run, found, err := s.workflowRunForTask(ctx, store, task.ID)
	if err != nil {
		return err
	}
	if !found || run == nil {
		return fmt.Errorf("%w: task has no workflow run", ErrWorkflowNodeRunNotActionable)
	}
	if _, err := requireAgentNode(run, nodeID); err != nil {
		return err
	}
	rows, err := store.ListWorkflowNodeRuns(ctx, run.ID)
	if err != nil {
		return err
	}
	if nodeRunInFlight(rows, nodeID) {
		return fmt.Errorf("%w: 节点 %s 正在运行，运行中不切换", ErrWorkflowNodeRunNotActionable, nodeID)
	}
	for attempt := 0; attempt < 5; attempt++ {
		setGraphNodeParam(&run.Graph, nodeID, "model", model)
		if err := store.SaveWorkflowRun(ctx, run); err != nil {
			if !errors.Is(err, ErrRunConflict) {
				return err
			}
			fresh, loadErr := store.WorkflowRunByID(ctx, run.ID)
			if loadErr != nil {
				return loadErr
			}
			run = fresh
			continue
		}
		log.Printf("[kanban/router] node model set task=%s run=%s node=%s model=%s reason=%q", task.ID, run.ID, nodeID, model, reason)
		return nil
	}
	return ErrRunConflict
}

// SkipWorkflowNodeForRouter 跳过一个还没跑过的节点：写一行 skipped、投影同步
// 收尾，然后唤醒 driver 让下游继续（与条件节点未选中时的跳过语义一致）。
func (s *Service) SkipWorkflowNodeForRouter(ctx context.Context, rootID, taskID, nodeID, reason string) error {
	if s == nil {
		return errors.New("kanban service not configured")
	}
	store, task, err := s.loadForMove(ctx, rootID, taskID)
	if err != nil {
		return err
	}
	if isTerminalStatus(task.Status) {
		return fmt.Errorf("%w: task is %s", ErrWorkflowNodeRunNotActionable, task.Status)
	}
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return errors.New("node id required")
	}
	run, found, err := s.workflowRunForTask(ctx, store, task.ID)
	if err != nil {
		return err
	}
	if !found || run == nil {
		return fmt.Errorf("%w: task has no workflow run", ErrWorkflowNodeRunNotActionable)
	}
	node, err := requireAgentNode(run, nodeID)
	if err != nil {
		return err
	}
	rows, err := store.ListWorkflowNodeRuns(ctx, run.ID)
	if err != nil {
		return err
	}
	if nodeRunInFlight(rows, nodeID) {
		return fmt.Errorf("%w: 节点 %s 正在运行，运行中不切换", ErrWorkflowNodeRunNotActionable, nodeID)
	}
	if nodeRunStarted(rows, nodeID) {
		return fmt.Errorf("%w: 节点 %s 已经跑过，跳过只用于还没开始执行的节点", ErrWorkflowNodeRunNotActionable, nodeID)
	}
	if state := strings.TrimSpace(run.NodeStates[nodeID]); state != "" && state != NodeRunPending {
		return fmt.Errorf("%w: 节点 %s 已经是 %s", ErrWorkflowNodeRunNotActionable, nodeID, state)
	}
	if _, err := s.commitSkippedWorkflowNode(ctx, store, run, node, reason); err != nil {
		return err
	}
	if err := s.WakeWorkflowRun(ctx, rootID, task.ID, "router_skip_node"); err != nil {
		// 投影已经写好了：唤醒失败只是这一拍没跑，下一次唤醒/启动对账会把下游捡起来。
		log.Printf("[kanban/router] skip node wake failed task=%s node=%s err=%v", task.ID, nodeID, err)
	}
	return nil
}

// requireAgentNode 解析顶层 agent 节点（Loop 内部节点不接受节点级管理）。
func requireAgentNode(run *WorkflowRun, nodeID string) (WorkflowNode, error) {
	for _, node := range run.Graph.Nodes {
		if node.ID != nodeID {
			continue
		}
		if node.Kind != NodeKindAgent {
			return WorkflowNode{}, fmt.Errorf("%w: 节点 %s 是 %s，节点级管理只支持 agent 节点", ErrWorkflowNodeRunNotActionable, nodeID, node.Kind)
		}
		return node, nil
	}
	return WorkflowNode{}, fmt.Errorf("%w: unknown node %q", ErrWorkflowNodeRunNotActionable, nodeID)
}

// setGraphNodeParam 改图里某个节点的参数（写时复制，避免与其它 Run 共享 map）。
func setGraphNodeParam(graph *WorkflowDefinition, nodeID, key, value string) {
	for i := range graph.Nodes {
		if graph.Nodes[i].ID != nodeID {
			continue
		}
		params := make(map[string]any, len(graph.Nodes[i].Params)+1)
		for k, v := range graph.Nodes[i].Params {
			params[k] = v
		}
		params[key] = value
		graph.Nodes[i].Params = params
		return
	}
}

// nodeRunInFlight 报告某节点最新一次尝试是否还在跑（运行中不切换的唯一依据）。
func nodeRunInFlight(rows []NodeRun, nodeID string) bool {
	latest, ok := latestTopLevelRow(rows, nodeID)
	return ok && (latest.Status == NodeRunQueued || latest.Status == NodeRunRunning)
}

// nodeRunStarted 报告某节点是否已经有任何一次尝试（跳过只用于真正没跑过的节点）。
func nodeRunStarted(rows []NodeRun, nodeID string) bool {
	_, ok := latestTopLevelRow(rows, nodeID)
	return ok
}

func latestTopLevelRow(rows []NodeRun, nodeID string) (NodeRun, bool) {
	var latest NodeRun
	found := false
	for _, row := range rows {
		if row.NodeID != nodeID || row.LoopPath != "" {
			continue
		}
		if !found || row.Attempt >= latest.Attempt {
			latest, found = row, true
		}
	}
	return latest, found
}
