package kanban

// 把留言交出去的两个动作：deliver 与 answer_waiting。动作分发与其它动作实现
// 在 activity_route_apply.go；从该文件拆出（硬线 500，2026-11-23）。

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// applyRouteDeliver 把用户留言绑到当前唯一的活动 Agent NodeRun，并隐藏占位
// 回复：答复会出现在节点会话里，任务对话流不需要一条空洞的「已转交」。
func (s *ActivityService) applyRouteDeliver(
	ctx context.Context,
	decision *RouterDecision,
	task Task,
	userAct *TaskActivity,
	state *routeApplyState,
	action RouterAction,
) error {
	// 校验阶段已经保证这里最多一个活动执行；没有活动节点时 give up，
	// 让决策落 failed 而不是把留言静默留在 pending_phase。
	if _, err := s.tasks.DeliverActivityToActiveNodeRun(ctx, task.ID, userAct.ID, ""); err != nil {
		return err
	}
	if err := s.hideRouteActivity(ctx, state, nil, "delivered"); err != nil {
		return err
	}
	updated, err := s.activities.Get(ctx, userAct.ID)
	if err == nil {
		s.broadcastUpdated(updated, map[string]any{"node_run_id": updated.NodeRunID, "run_id": updated.RunID})
	}
	return nil
}

// applyRouteAnswerWaiting 把用户答复真的交回那个停住的节点：服务重开一次带答复
// 的尝试，节点回到 running。
//
// 与 deliver 的差别就在「可见性」：deliver 把留言排进正在跑的节点会话，占位回复
// 会被隐藏（用户看不到任何动作）；这里必须留一句事实回执，否则用户会以为又什么
// 都没发生 —— 那正是 2026-11-19 的报障。
func (s *ActivityService) applyRouteAnswerWaiting(
	ctx context.Context,
	decision *RouterDecision,
	task Task,
	userAct *TaskActivity,
	state *routeApplyState,
	action RouterAction,
) error {
	body := strings.TrimSpace(action.Body)
	if body == "" {
		return errors.New("answer_waiting_node body required")
	}
	if s.nodeAnswerer == nil {
		return errors.New("node answer port not configured")
	}
	answered, err := s.nodeAnswerer.AnswerWaitingWorkflowNode(ctx, task.RootID, task.ID, strings.TrimSpace(action.NodeRunID), body, userAct.ID)
	if err != nil {
		return err
	}
	notice := fmt.Sprintf("已把你的答复交给「%s」节点，正在带着答复重跑。", answered.NodeID)
	act := state.take()
	if act == nil {
		act = s.newAgentActivity(task, CommentWorkflowChoiceEmpty)
		act.Body = notice
		act.Status = ActivityStatusCompleted
		if err := s.insertRouteReply(ctx, act, false); err != nil {
			return err
		}
	} else {
		if err := s.activities.UpdateBody(ctx, act.ID, notice); err != nil {
			return err
		}
		if err := s.activities.UpdateStatus(ctx, act.ID, ActivityStatusCompleted, nil); err != nil {
			return err
		}
		act.Body = notice
		act.Status = ActivityStatusCompleted
	}
	fields, err := s.markRouteReply(ctx, decision, userAct, act, action)
	if err != nil {
		return err
	}
	s.broadcastUpdated(act, map[string]any{
		"body": notice, "status": ActivityStatusCompleted,
		"purpose": fields["purpose"], "ordinal": action.Seq,
		"reply_to_id": routeUserActivityID(userAct), "router_decision_id": decision.ID,
	})
	return nil
}
