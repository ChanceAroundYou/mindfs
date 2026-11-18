package kanban

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// routeApplyState 在一条决策的动作序列内复用同一份占位回复：第一条回复类动作
// 占用它，后续动作用新活动或隐藏它。
type routeApplyState struct {
	placeholder *TaskActivity
	used        bool
}

func (st *routeApplyState) take() *TaskActivity {
	if st == nil || st.used || st.placeholder == nil {
		return nil
	}
	st.used = true
	return st.placeholder
}

// ApplyRouteActions 按 seq 顺序应用 Router 决策。每条动作先 ClaimAppliedSeq，
// 抢占成功才执行 —— 进程重启或重复投递时，已经应用过的动作必须跳过，不能
// 重复建 Run / 插活动。
func (s *ActivityService) ApplyRouteActions(ctx context.Context, decision *RouterDecision) (applyErr error) {
	if s == nil || s.tasks == nil || s.activities == nil {
		return errors.New("activity service not configured")
	}
	if decision == nil {
		return errors.New("router decision required")
	}
	state := &routeApplyState{}
	if id := strings.TrimSpace(decision.PlaceholderActivityID); id != "" {
		if act, loadErr := s.activities.Get(ctx, id); loadErr == nil {
			state.placeholder = act
		}
	}
	defer func() {
		if applyErr != nil && !state.used {
			if err := s.failUnusedRoutePlaceholder(ctx, decision, applyErr.Error()); err != nil {
				applyErr = errors.Join(applyErr, fmt.Errorf("finish router placeholder: %w", err))
			}
		}
	}()
	userAct, err := s.activities.Get(ctx, strings.TrimSpace(decision.UserActivityID))
	if err != nil {
		return err
	}
	store := NewRouterStore(s.tasks.db)
	for _, action := range decision.Actions {
		claimed, err := store.ClaimAppliedSeq(ctx, decision.ID, action.Seq)
		if err != nil {
			return fmt.Errorf("claim seq %d: %w", action.Seq, err)
		}
		if !claimed {
			continue
		}
		// The model and previous actions may have yielded to an operator decision.
		task, err := s.tasks.GetTask(ctx, decision.TaskID)
		if err != nil {
			return err
		}
		// 运行期事件回合是「汇报」，不是对任务的再操作：Run 结算后任务本来就该
		// 是终态，同一个终态闸门会把汇报本身挡成失败。用户留言保持原语义。
		if decision.TriggerKind == RouterTriggerUserMessage {
			if err := routeTaskStatusError(task.Status); err != nil {
				return err
			}
		}
		if err := s.applyRouteAction(ctx, decision, task, userAct, state, action); err != nil {
			return fmt.Errorf("apply %s(seq=%d): %w", action.Kind, action.Seq, err)
		}
	}
	return nil
}

// Only close the original, still-empty reply. A retried decision may load a reply
// already claimed by a worker or filled by an earlier action.
func (s *ActivityService) failUnusedRoutePlaceholder(ctx context.Context, decision *RouterDecision, reason string) error {
	id := strings.TrimSpace(decision.PlaceholderActivityID)
	if id == "" {
		return nil
	}
	result, err := s.activities.db.ExecContext(ctx, `
UPDATE task_activities SET status=?, summary=?, updated_at=?
WHERE id=? AND task_id=? AND type=? AND role=? AND status=?
  AND hidden=0 AND run_id='' AND router_decision_id='' AND body='' AND summary=''`,
		ActivityStatusFailed, reason, time.Now().UTC().Format(time.RFC3339Nano),
		id, decision.TaskID, ActivityTypeAgentReply, RoleAgent, ActivityStatusPending)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err == nil && updated > 0 {
		s.broadcastUpdatedByID(ctx, id, map[string]any{"status": ActivityStatusFailed, "summary": reason})
	}
	return err
}

func (s *ActivityService) applyRouteAction(
	ctx context.Context,
	decision *RouterDecision,
	task Task,
	userAct *TaskActivity,
	state *routeApplyState,
	action RouterAction,
) error {
	switch action.Kind {
	case RouterActionSay:
		return s.applyRouteSay(ctx, decision, task, userAct, state, action)
	case RouterActionAskUser:
		return s.applyRouteAskUser(ctx, decision, task, userAct, state, action)
	case RouterActionMessageAgent:
		return s.applyRouteMessageAgent(ctx, decision, task, userAct, state, action)
	case RouterActionRunWorkflow:
		return s.applyRouteRunWorkflow(ctx, decision, task, userAct, state, action)
	case RouterActionDeliver:
		return s.applyRouteDeliver(ctx, decision, task, userAct, state, action)
	case RouterActionSilent:
		return s.hideRouteActivity(ctx, state, nil, "silent")
	default:
		return fmt.Errorf("unsupported action %q", action.Kind)
	}
}

// applyRouteSay 渲染 Router 的直接回复。第一条 say 复用同步占位（用户已经
// 看到「正在想」的那条），之后每条新起一条活动，Ordinal 保序。
func (s *ActivityService) applyRouteSay(
	ctx context.Context,
	decision *RouterDecision,
	task Task,
	userAct *TaskActivity,
	state *routeApplyState,
	action RouterAction,
) error {
	body := strings.TrimSpace(action.Body)
	if body == "" {
		return errors.New("say body required")
	}
	act := state.take()
	if act == nil {
		act = s.newAgentActivity(task, CommentWorkflowChoiceEmpty)
		act.Body = body
		act.Status = ActivityStatusCompleted
		if err := s.activities.Insert(ctx, act); err != nil {
			return err
		}
		s.broadcastCreated(act)
	} else {
		if err := s.activities.UpdateBody(ctx, act.ID, body); err != nil {
			return err
		}
		if err := s.activities.UpdateStatus(ctx, act.ID, ActivityStatusCompleted, nil); err != nil {
			return err
		}
		act.Body = body
		act.Status = ActivityStatusCompleted
	}
	fields, err := s.markRouteReply(ctx, decision, userAct, act, action)
	if err != nil {
		return err
	}
	changes := map[string]any{
		"body": body, "status": ActivityStatusCompleted,
		"purpose": fields["purpose"], "ordinal": action.Seq,
		"reply_to_id": userAct.ID, "router_decision_id": decision.ID,
	}
	s.broadcastUpdated(act, changes)
	// 纯回复没有 Run 可投影任务结果，正文就是这一轮的结果摘要。运行期事件的
	// 汇报（trigger != user_message）不得覆盖用户回合留下的任务结果摘要。
	if act.RunID == "" && decision.TriggerKind == RouterTriggerUserMessage {
		return s.tasks.recordReplySummaryIfActive(ctx, task.ID, body)
	}
	return nil
}

// applyRouteAskUser 把提问渲染成一条 waiting_user 的 agent 回复。任务对话流的
// 提问卡绑定会话 tool call，Router 不在那个会话里，所以用正文承载问题与选项。
func (s *ActivityService) applyRouteAskUser(
	ctx context.Context,
	decision *RouterDecision,
	task Task,
	userAct *TaskActivity,
	state *routeApplyState,
	action RouterAction,
) error {
	question := strings.TrimSpace(action.Question)
	if question == "" {
		question = strings.TrimSpace(action.Body)
	}
	if question == "" {
		return errors.New("ask_user question required")
	}
	body := question
	if options := cleanRouteOptions(action.Options); len(options) > 0 {
		body += "\n\n" + strings.Join(options, "\n")
	}
	act := state.take()
	if act == nil {
		act = s.newAgentActivity(task, CommentWorkflowChoiceEmpty)
		act.Body = body
		act.Status = ActivityStatusWaitingUser
		if err := s.activities.Insert(ctx, act); err != nil {
			return err
		}
		s.broadcastCreated(act)
	} else {
		if err := s.activities.UpdateBody(ctx, act.ID, body); err != nil {
			return err
		}
		if err := s.activities.UpdateStatus(ctx, act.ID, ActivityStatusWaitingUser, nil); err != nil {
			return err
		}
		act.Body = body
		act.Status = ActivityStatusWaitingUser
	}
	fields, err := s.markRouteReply(ctx, decision, userAct, act, action)
	if err != nil {
		return err
	}
	s.broadcastUpdated(act, map[string]any{
		"body": body, "status": ActivityStatusWaitingUser,
		"purpose": fields["purpose"], "ordinal": action.Seq,
		"reply_to_id": userAct.ID, "router_decision_id": decision.ID,
	})
	return nil
}

// applyRouteMessageAgent 把 Router 改写后的任务描述交给直连 Agent。用户原话
// 已经作为留言显示在对话流里，Agent 收到的是 Router 结构化的 brief 正文。
func (s *ActivityService) applyRouteMessageAgent(
	ctx context.Context,
	decision *RouterDecision,
	task Task,
	userAct *TaskActivity,
	state *routeApplyState,
	action RouterAction,
) error {
	body := strings.TrimSpace(action.Body)
	if body == "" {
		return errors.New("message_agent body required")
	}
	act, err := s.ensureAgentActivity(ctx, task, CommentWorkflowChoiceDisabled, state.take())
	if err != nil {
		return err
	}
	if _, err := s.markRouteReply(ctx, decision, userAct, act, action); err != nil {
		return err
	}
	begun, err := s.beginDirectAgent(ctx, task, body, act, CommentWorkflowChoiceDisabled)
	if err != nil {
		return err
	}
	return routeTurnStarted(begun, "message_agent")
}

// applyRouteRunWorkflow 用 Router 指定的 workflow/版本起 Run，不再经过
// recommender —— Router 已经做过选型，再挑一次会覆盖它的决定。
func (s *ActivityService) applyRouteRunWorkflow(
	ctx context.Context,
	decision *RouterDecision,
	task Task,
	userAct *TaskActivity,
	state *routeApplyState,
	action RouterAction,
) error {
	version, err := s.resolveRouteWorkflowVersion(ctx, action)
	if err != nil {
		return err
	}
	act, err := s.ensureAgentActivity(ctx, task, CommentWorkflowChoiceEmpty, state.take())
	if err != nil {
		return err
	}
	if _, err := s.markRouteReply(ctx, decision, userAct, act, action); err != nil {
		return err
	}
	begun, err := s.beginWorkflowRunVersion(ctx, task, version, userAct.ID, act)
	if err != nil {
		return err
	}
	return routeTurnStarted(begun, "run_workflow")
}

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

// hideRouteActivity 把占位回复从用户可见活动流里摘掉。Row 保留（审计/幂等
// 都需要它），只置 hidden=1 并广播 hidden 让前端移除。
func (s *ActivityService) hideRouteActivity(ctx context.Context, state *routeApplyState, act *TaskActivity, reason string) error {
	if act == nil {
		act = state.take()
	}
	if act == nil {
		return nil
	}
	if err := s.activities.SetHidden(ctx, act.ID, true); err != nil {
		return err
	}
	act.Hidden = true
	s.broadcastUpdated(act, map[string]any{"hidden": true, "hidden_reason": reason})
	return nil
}

// markRouteReply 写 Router 归属字段（决策/回复对象/用途/顺序）。
func (s *ActivityService) markRouteReply(
	ctx context.Context,
	decision *RouterDecision,
	userAct *TaskActivity,
	act *TaskActivity,
	action RouterAction,
) (map[string]any, error) {
	purpose := strings.TrimSpace(action.Purpose)
	if purpose == "" {
		purpose = "reply"
	}
	if err := s.activities.SetRouteFields(ctx, act.ID, decision.ID, userAct.ID, purpose, action.Seq); err != nil {
		return nil, err
	}
	act.RouterDecisionID = decision.ID
	act.ReplyToID = userAct.ID
	act.Purpose = purpose
	act.Ordinal = action.Seq
	return map[string]any{"purpose": purpose}, nil
}

// resolveRouteWorkflowVersion 解析 Router 给的版本：显式 version_id 优先，
// 否则取该 workflow 的最新发布版本。两者都不存在是决策失败，不是回落。
func (s *ActivityService) resolveRouteWorkflowVersion(ctx context.Context, action RouterAction) (WorkflowVersion, error) {
	if s.workflows == nil {
		return WorkflowVersion{}, errors.New("workflow control-plane store unavailable")
	}
	if id := strings.TrimSpace(action.VersionID); id != "" {
		if version, err := s.workflows.GetVersion(ctx, id); err == nil {
			return version, nil
		}
	}
	workflowID := strings.TrimSpace(action.WorkflowID)
	if workflowID == "" {
		return WorkflowVersion{}, errors.New("run_workflow requires workflow_id or version_id")
	}
	latest, ok := s.latestVersion(ctx, workflowID)
	if !ok {
		return WorkflowVersion{}, fmt.Errorf("workflow %s has no published version", workflowID)
	}
	return s.workflows.GetVersion(ctx, latest)
}

// routeTurnStarted 把「起跑守卫拒绝」翻译成决策失败：占位回复已经显示原因，
// 这里只负责让 router_decisions 不假装 applied。
func routeTurnStarted(act *TaskActivity, kind string) error {
	if act != nil && act.Status == ActivityStatusFailed {
		summary := strings.TrimSpace(act.Summary)
		if summary == "" {
			summary = "turn rejected"
		}
		return fmt.Errorf("%s: %s", kind, summary)
	}
	return nil
}

func cleanRouteOptions(options []string) []string {
	out := make([]string, 0, len(options))
	for _, option := range options {
		if trimmed := strings.TrimSpace(option); trimmed != "" {
			out = append(out, "· "+trimmed)
		}
	}
	return out
}
