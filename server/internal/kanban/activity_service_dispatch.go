package kanban

import (
	"context"
	"errors"
	"strings"
)

// dispatchUserActivity 把已落库的用户留言派发出去：route 模式交给 Router 决策；
// shadow 与旧路径（off）按 workflow_choice 起 Run 或直连回一轮，shadow 额外给
// Router 记一份观察样本。
func (s *ActivityService) dispatchUserActivity(
	ctx context.Context,
	task Task,
	userAct *TaskActivity,
	workflowVersionID *string,
) (*TaskActivity, *TaskActivity, error) {
	choice := userAct.WorkflowChoice

	// route 模式只接管「没指定 workflow」的留言。用户在界面上显式选了某个
	// workflow 或「纯对话」时，那是明确指令，不能再用 Router 猜一遍。
	if s.router != nil {
		mode := s.router.service.RouterConfig().ModeForRoot(task.RootID)
		if mode == RouterModeRoute && choice == CommentWorkflowChoiceEmpty {
			return s.dispatchRoutedUserActivity(ctx, task, userAct)
		}
	}

	// shadow 的 Situation Brief 必须在旧路径分发之前取：旧路径一动手，active
	// execution / run 状态就变了，之后再取到的是「旧路径已经改变过的世界」。
	// 这里只做只读快照，任何失败都只丢观察样本，绝不影响用户留言的处理。
	var brief *SituationBrief
	router := s.router
	if router != nil {
		config := router.service.RouterConfig()
		if config.ModeForRoot(task.RootID) == RouterModeShadow {
			if snapshot, briefErr := buildSituationBrief(ctx, s.tasks, s.workflows, config, task, RouterTriggerUserMessage, userAct.ID); briefErr == nil {
				brief = snapshot
			}
		} else {
			router = nil
		}
	}

	agentAct, err := s.dispatchLegacyActivity(ctx, task, userAct, workflowVersionID, nil)
	var legacy *RouterLegacyAction
	if err == nil {
		legacy = legacyActionOf(task, userAct, agentAct)
	} else {
		legacy = &RouterLegacyAction{Kind: "failed", Error: strings.TrimSpace(err.Error())}
	}
	if router != nil && brief != nil {
		router.Enqueue(RouterTrigger{
			Kind:           RouterTriggerUserMessage,
			TaskID:         task.ID,
			RootID:         task.RootID,
			UserActivityID: userAct.ID,
			PlaceholderID:  activityIDOf(agentAct),
			TurnID:         "msg:" + userAct.ID,
			Mode:           RouterModeShadow,
			Brief:          brief,
			LegacyAction:   legacy,
		})
	}
	return userAct, agentAct, err
}

// dispatchRoutedUserActivity 同步留下可见占位，允许活动回合接收协调留言。
func (s *ActivityService) dispatchRoutedUserActivity(ctx context.Context, task Task, userAct *TaskActivity) (*TaskActivity, *TaskActivity, error) {
	placeholder := s.newAgentActivity(task, CommentWorkflowChoiceEmpty)
	if err := s.activities.Insert(ctx, placeholder); err != nil {
		return userAct, nil, err
	}
	s.broadcastCreated(placeholder)
	if err := s.prepareRouterTurn(ctx, task, placeholder); err != nil {
		return userAct, placeholder, nil
	}
	s.router.Enqueue(RouterTrigger{
		Kind:           RouterTriggerUserMessage,
		TaskID:         task.ID,
		RootID:         task.RootID,
		UserActivityID: userAct.ID,
		PlaceholderID:  placeholder.ID,
		TurnID:         "msg:" + userAct.ID,
		Mode:           RouterModeRoute,
	})
	return userAct, placeholder, nil
}

// dispatchLegacyActivity 是改造前的分发逻辑。placeholder 非空时复用它，
// 不再新插一条 agent 回复（Router 回落路径必须复用同步占位）。
func (s *ActivityService) dispatchLegacyActivity(
	ctx context.Context,
	task Task,
	userAct *TaskActivity,
	workflowVersionID *string,
	placeholder *TaskActivity,
) (*TaskActivity, error) {
	if userAct.WorkflowChoice == CommentWorkflowChoiceDisabled {
		return s.beginDirectAgent(ctx, task, userAct.Body, placeholder, CommentWorkflowChoiceDisabled)
	}
	return s.beginWorkflowRun(ctx, task, userAct.Body, userAct.WorkflowChoice, workflowVersionID, userAct.ID, placeholder)
}

func legacyActionOf(task Task, userAct *TaskActivity, agentAct *TaskActivity) *RouterLegacyAction {
	if agentAct == nil {
		return &RouterLegacyAction{Kind: "failed"}
	}
	if agentAct.Status == ActivityStatusFailed {
		return &RouterLegacyAction{Kind: "failed", Error: strings.TrimSpace(agentAct.Summary)}
	}
	if userAct != nil && userAct.WorkflowChoice == CommentWorkflowChoiceDisabled {
		return &RouterLegacyAction{Kind: "direct"}
	}
	return &RouterLegacyAction{Kind: "workflow", RunID: agentAct.RunID}
}

// FallbackRoute 在 Router 失败或拒绝时把这一轮交回旧路径，并复用 Router 同步
// 落下的占位回复。已经应用的决策不会走这里（那会重复执行）。
func (s *ActivityService) FallbackRoute(ctx context.Context, taskID, userActivityID, placeholderID string) error {
	if s == nil || s.tasks == nil || s.activities == nil {
		return errors.New("activity service not configured")
	}
	task, err := s.tasks.GetTask(ctx, strings.TrimSpace(taskID))
	if err != nil {
		return err
	}
	userAct, err := s.activities.Get(ctx, strings.TrimSpace(userActivityID))
	if err != nil {
		return err
	}
	var placeholder *TaskActivity
	if id := strings.TrimSpace(placeholderID); id != "" {
		if act, loadErr := s.activities.Get(ctx, id); loadErr == nil {
			placeholder = act
		}
	}
	_, err = s.dispatchFallbackForPlaceholder(ctx, task, userAct, placeholder)
	return err
}

func (s *ActivityService) dispatchFallbackForPlaceholder(ctx context.Context, task Task, userAct *TaskActivity, placeholder *TaskActivity) (*TaskActivity, error) {
	agentAct, err := s.dispatchLegacyActivity(ctx, task, userAct, nil, placeholder)
	if err != nil {
		if agentAct != nil {
			s.failActivity(ctx, agentAct, err)
		}
		return agentAct, err
	}
	return agentAct, nil
}

func activityIDOf(activity *TaskActivity) string {
	if activity == nil {
		return ""
	}
	return activity.ID
}
