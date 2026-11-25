package kanban

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

// routeApplyState 在一条决策的动作序列内复用同一份占位回复：第一条回复类动作
// 占用它，后续动作用新活动或隐藏它。
type routeApplyState struct {
	placeholder *TaskActivity
	used        bool
	// settleSummarySeq 是结算回合里承担总结的那条 say（-1 = 不适用）。
	settleSummarySeq int
	// settleTurn 表示这是一轮收尾（Run 成功结算 / 会话回合结算）。
	settleTurn bool
	// settleSummarySeen 表示本回合已经出现过承担总结的那条 say。之后的
	// progress 只是把总结再念一遍，落库但不进可见流。
	settleSummarySeen bool
}

func (st *routeApplyState) take() *TaskActivity {
	if st == nil || st.used || st.placeholder == nil {
		return nil
	}
	st.used = true
	return st.placeholder
}

// ApplyRouteActions 按 seq 顺序应用 Router 决策。用户留言的每条动作先
// ClaimAppliedSeq，抢占成功才执行 —— 进程重启或重复投递时，已经应用过的动作
// 必须跳过，不能重复建 Run / 插活动。
//
// 运行期事件相反：可见汇报用确定 effect ID（routeEffectActivityID），重复应用
// 只会改写同一行。所以它先写效果、后推游标。倒过来一旦崩在两者之间，重放就会
// 把这条当成已应用，用户永远看不到这次汇报。
func (s *ActivityService) ApplyRouteActions(ctx context.Context, decision *RouterDecision) (applyErr error) {
	if s == nil || s.tasks == nil || s.activities == nil {
		return errors.New("activity service not configured")
	}
	if decision == nil {
		return errors.New("router decision required")
	}
	state := &routeApplyState{settleSummarySeq: -1}
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
	if s.settledRunSucceeded(ctx, decision) {
		state.settleSummarySeq = settleSummaryActionSeq(decision)
		state.settleTurn = true
	}
	// 会话回合结算同样是收尾回合（prompt 规则 12 与 summaryTriggerAllowed 都这么
	// 认定），但它没有 Run 可判定「真的跑成了」，所以判据换成「这一轮有没有真实
	// 执行结果」：会话里跑过工具（decision.HadToolCalls）才把最后一条 say 提升成
	// 总结；纯文本闲聊不提升 —— 每轮会话都结算，全升会把普通进度顶成卡片结论。
	// 线上 #68 正是「Router 说对了、说了四条待决策项，但写成 progress，永远不落
	// 任务摘要」，卡片上只剩压缩器那段短句。
	if decision.TriggerKind == RouterTriggerTurnSettled {
		state.settleTurn = true
		if decision.HadToolCalls {
			state.settleSummarySeq = settleSummaryActionSeq(decision)
		}
	}
	// 巡检回合（回合结算 / 停滞 / 孤儿 / 打断）没有来源用户留言：它们由系统
	// 发起，证据在事件载荷里。此时 userAct 为 nil，只允许回话类动作（见
	// applyRouteAction），任何需要「回复某条留言」的动作都会被拒绝。
	userAct, err := s.activities.Get(ctx, strings.TrimSpace(decision.UserActivityID))
	if err != nil && !(strings.TrimSpace(decision.UserActivityID) == "" && errors.Is(err, ErrActivityNotFound)) {
		return err
	}
	store := NewRouterStore(s.tasks.db)
	runtimeEvent := decision.TriggerKind != RouterTriggerUserMessage
	for _, action := range decision.Actions {
		if !runtimeEvent {
			claimed, err := store.ClaimAppliedSeq(ctx, decision.ID, action.Seq)
			if err != nil {
				return fmt.Errorf("claim seq %d: %w", action.Seq, err)
			}
			if !claimed {
				continue
			}
		}
		if err := s.applyCheckedRouteAction(ctx, decision, userAct, state, action); err != nil {
			return err
		}
		if runtimeEvent {
			if err := store.AdvanceAppliedSeq(ctx, decision.ID, action.Seq); err != nil {
				return fmt.Errorf("advance seq %d: %w", action.Seq, err)
			}
		}
	}
	return nil
}

// applyCheckedRouteAction 取任务、做用户留言的状态闸门，再执行动作。运行期
// 事件只在 Run 结算后汇报，任务本来就是终态，所以闸门只属于用户留言。
func (s *ActivityService) applyCheckedRouteAction(
	ctx context.Context,
	decision *RouterDecision,
	userAct *TaskActivity,
	state *routeApplyState,
	action RouterAction,
) error {
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
	// 没有来源留言的巡检回合只能回话，或写一条不依赖留言锚点的监管说明/升级卡。
	// 收尾、起 Run、投递等动作都需要留言当锚点（回复绑定、证据、投递目标），
	// 没有它就不是「巡检发现问题」，而是「凭空操作任务」。notify_user / escalate
	// 的 applier 本来就不接收 userAct；本现场的上层 validateRuntimeEventActions
	// 也已经确认过监管观测块。这里放行它们，真正修掉巡检发现问题却只留下拒绝说明的现场。
	// silent 也放行（S5）：巡检零变化时本来就该什么都不说 —— 它没有副作用，
	// 拒掉只会把「没变化」逼成一条被拒说明，比沉默更吵。
	// 只对巡检类触发判定：直接调用的应用测试与用户留言回合没有留言是另一回事，
	// 不在这里改变它们的语义。
	if userAct == nil && routerTurnAllowsNoUserActivity(string(decision.TriggerKind)) &&
		action.Kind != RouterActionSay && action.Kind != RouterActionAskUser &&
		action.Kind != RouterActionSilent &&
		action.Kind != RouterActionNotifyUser && action.Kind != RouterActionEscalate {
		// 拒掉之后必须留一条可见说明。否则整轮 failed 只在日志里，用户侧
		// 零变化 —— 现场就是这样：巡检发现了问题、动作被拒、谁也不知道
		// （rdec_47d5992f117dc51b）。
		if noteErr := s.recordRejectedInspectionAction(ctx, decision, task, action); noteErr != nil {
			log.Printf("[router] reject.notice.error id=%s seq=%d err=%v", decision.ID, action.Seq, noteErr)
		}
		return fmt.Errorf("action %q requires an originating user activity", action.Kind)
	}
	switch action.Kind {
	case RouterActionSay:
		return s.applyRouteSay(ctx, decision, task, userAct, state, action)
	case RouterActionAskUser:
		return s.applyRouteAskUser(ctx, decision, task, userAct, state, action)
	case RouterActionMessageAgent:
		return s.applyRouteMessageAgent(ctx, decision, task, userAct, state, action)
	case RouterActionRunWorkflow:
		return s.applyRouteRunWorkflow(ctx, decision, task, userAct, state, action)
	case RouterActionComplete:
		return s.applyRouteComplete(ctx, decision, task, action)
	case RouterActionRecordAcceptance:
		return s.applyRouteRecordAcceptance(ctx, decision, task, action)
	case RouterActionDeliver:
		return s.applyRouteDeliver(ctx, decision, task, userAct, state, action)
	case RouterActionAnswerWaiting:
		return s.applyRouteAnswerWaiting(ctx, decision, task, userAct, state, action)
	case RouterActionSilent:
		return s.hideRouteActivity(ctx, state, nil, "silent")
	case RouterActionRetryNode:
		return s.applyRouteRetryNode(ctx, decision, action)
	case RouterActionNotifyUser:
		return s.applyRouteNotifyUser(ctx, decision, task, action)
	case RouterActionResumeRun:
		return s.applyRouteResumeRun(ctx, decision, action)
	case RouterActionEscalate:
		return s.applyRouteEscalate(ctx, decision, task, action)
	case RouterActionRetryDirect:
		return s.applyRouteRetryDirect(ctx, decision, task, userAct, action)
	case RouterActionCancelNode:
		return s.applyRouteCancelNode(ctx, decision, task, action)
	case RouterActionCancelRun:
		return s.applyRouteCancelRun(ctx, decision, task, action)
	case RouterActionCancelTask:
		return s.applyRouteCancelTask(ctx, decision, task, action)
	case RouterActionSetNodeModel:
		return s.applyRouteSetNodeModel(ctx, decision, task, action)
	case RouterActionSkipNode:
		return s.applyRouteSkipNode(ctx, decision, task, action)
	case RouterActionSaveWorkflow:
		return s.applyRouteSaveWorkflow(ctx, decision, task, action)
	default:
		return fmt.Errorf("unsupported action %q", action.Kind)
	}
}

// applyRouteComplete 把 Router 的收尾意图交给服务层求值：证据成立才收尾，
// 不成立只写缺口、任务原地不动。收尾要停 driver、冻结 Run，所以走端口而不是
// 在这里直接改库。
func (s *ActivityService) applyRouteComplete(ctx context.Context, decision *RouterDecision, task Task, action RouterAction) error {
	if s.completion == nil {
		return errors.New("complete_task is not available: completion applier not configured")
	}
	if s.tasks == nil {
		return errors.New("complete_task: task store missing")
	}
	return s.completion.CompleteTaskFromRouter(ctx, s.tasks, task, *decision, action)
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
		effectID := routeEffectActivityID(decision, action)
		act = s.newAgentActivity(task, CommentWorkflowChoiceEmpty)
		if effectID != "" {
			act.ID = effectID
		}
		act.Body = body
		act.Status = ActivityStatusWaitingUser
		if err := s.insertRouteReply(ctx, act, effectID != ""); err != nil {
			return err
		}
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
	// Router 的提问落成真正的提问卡：按钮、输入框锁、作答入口都挂在 type=question
	// 上。正文保留（问题 + 选项各一行），飞书通知与纯文本客户端读的是它。
	payloadJSON, err := routerAskUserQuestionPayload(question, action.Options, decision.ID)
	if err != nil {
		return err
	}
	if err := s.activities.SetQuestionCard(ctx, act.ID, payloadJSON); err != nil {
		return err
	}
	act.Type = ActivityTypeQuestion
	act.PayloadJSON = payloadJSON
	// 新卡落地前先收口旧的 Router 提问卡：前端锁输入框读的是最早那张，旧卡留着
	// 用户就会点到过时的问题（#104）。
	if err := s.closeRouterQuestionsBefore(ctx, task.ID, act.ID); err != nil {
		return err
	}
	fields, err := s.markRouteReply(ctx, decision, userAct, act, action)
	if err != nil {
		return err
	}
	s.broadcastUpdated(act, map[string]any{
		"body": body, "status": ActivityStatusWaitingUser,
		"type": ActivityTypeQuestion, "payload_json": payloadJSON,
		"purpose": fields["purpose"], "ordinal": action.Seq,
		"reply_to_id": routeUserActivityID(userAct), "router_decision_id": decision.ID,
	})
	// 提问就是「等人回话」：任务头与活动状态一致，停在待审核。
	if decision.TriggerKind == RouterTriggerUserMessage && act.RunID == "" {
		s.markTurnTaskWaitingUser(ctx, task.ID)
	}
	if s.cardNotifier != nil {
		notice := CardNotice{
			ID: "ask-" + act.ID, Kind: CardKindAsk,
			RootID: task.RootID, TaskID: task.ID, TaskTitle: taskTitle(task), TaskNumber: task.TaskNumber,
			QuestionID: act.ID, Prompt: question, Options: routeOptionLabels(action.Options),
		}
		if err := s.cardNotifier.NotifyCard(ctx, notice); err != nil {
			log.Printf("[kanban/cards] ask %s: %v", notice.ID, err)
		}
	}
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
	version, err := s.resolveRouteWorkflowVersion(ctx, task, action)
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
	begun, err := s.beginWorkflowRunVersion(ctx, task, version, userAct.ID, act, action.SessionMode)
	if err != nil {
		return err
	}
	return routeTurnStarted(begun, "run_workflow")
}
