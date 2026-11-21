package kanban

import (
	"context"
	"errors"
	"strconv"
	"strings"
)

// W2 T2 的节点级管理动作如何在决策被应用时落地。
//
// 与取消一样走端口：ActivityService 不认识 kanban.Service，端口由装配期接上。
// 两个动作都不改动任务状态、不结算回复 —— 它们是「把这一轮继续下去」的手段，
// 不是收尾。

// ActivityNodeAdmin 是节点级管理端口（改档 / 跳过）。
type ActivityNodeAdmin interface {
	SetWorkflowNodeModelForRouter(ctx context.Context, rootID, taskID, nodeID, model, reason string) error
	SkipWorkflowNodeForRouter(ctx context.Context, rootID, taskID, nodeID, reason string) error
}

// SetNodeAdmin 装配节点级管理端口。装配期调用一次。
func (s *ActivityService) SetNodeAdmin(a ActivityNodeAdmin) {
	s.nodeAdmin = a
}

func (s *ActivityService) applyRouteSetNodeModel(ctx context.Context, decision *RouterDecision, task Task, action RouterAction) error {
	if s.nodeAdmin == nil {
		return errors.New("set_node_model is not available: node admin not configured")
	}
	nodeID := strings.TrimSpace(action.NodeID)
	model := strings.TrimSpace(action.Model)
	if nodeID == "" || model == "" {
		return errors.New("set_node_model: node_id and model required")
	}
	if err := s.nodeAdmin.SetWorkflowNodeModelForRouter(ctx, task.RootID, decision.TaskID, nodeID, model, action.Reason); err != nil {
		return err
	}
	return s.recordNodeAdminNote(ctx, decision, task, "route-model:"+decision.ID+":"+strconv.Itoa(action.Seq),
		"已把节点「"+nodeID+"」的模型档改为 "+model+"，下一次重跑生效。")
}

func (s *ActivityService) applyRouteSkipNode(ctx context.Context, decision *RouterDecision, task Task, action RouterAction) error {
	if s.nodeAdmin == nil {
		return errors.New("skip_node is not available: node admin not configured")
	}
	nodeID := strings.TrimSpace(action.NodeID)
	if nodeID == "" {
		return errors.New("skip_node: node_id required")
	}
	if err := s.nodeAdmin.SkipWorkflowNodeForRouter(ctx, task.RootID, decision.TaskID, nodeID, action.Reason); err != nil {
		return err
	}
	return s.recordNodeAdminNote(ctx, decision, task, "route-skip:"+decision.ID+":"+strconv.Itoa(action.Seq),
		"已跳过节点「"+nodeID+"」，下游继续执行。")
}

// recordNodeAdminNote 落一条确定性的系统行：节点级动作改的是执行路线，用户要在
// 任务详情里看得见「谁动了什么」，而不是只在日志里。
func (s *ActivityService) recordNodeAdminNote(ctx context.Context, decision *RouterDecision, task Task, id, body string) error {
	act := &TaskActivity{
		ID:     id,
		TaskID: decision.TaskID, RootID: task.RootID,
		Type: ActivityTypeAttention, Author: "system", Role: "system",
		Status: ActivityStatusCompleted,
		Body:   body,
	}
	_, err := s.activities.InsertOrIgnore(ctx, act)
	return err
}
