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
	// 认定），但它没有 Run 可判定「真的跑成了」，所以只用来识别「本回合已有总结」，
	// 不做「最后一条 say 提升成总结」的兜底 —— 每轮会话都结算，那样会把普通进度
	// 顶成卡片结论。
	if decision.TriggerKind == RouterTriggerTurnSettled {
		state.settleTurn = true
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
	// 没有来源留言的巡检回合只能回话。收尾、起 Run、投递、监管写入都需要一条
	// 留言当锚点（回复绑定、证据、投递目标），没有它就不是「巡检发现问题」，
	// 而是「凭空操作任务」。只对巡检类触发判定：直接调用的应用测试与用户留言
	// 回合没有留言是另一回事，不在这里改变它们的语义。
	if userAct == nil && routerTurnAllowsNoUserActivity(string(decision.TriggerKind)) &&
		action.Kind != RouterActionSay && action.Kind != RouterActionAskUser {
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

// applyRouteSay 渲染 Router 的直接回复。第一条 say 复用同步占位（用户已经
// 看到「正在想」的那条），之后每条新起一条活动，Ordinal 保序。
//
// 运行期事件回合没有占位，用确定 effect ID 插入并 InsertOrIgnore：同一决策/动作
// 重复应用必须落成同一行，绝不出现第二条可见汇报。
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
	// 结算回合的最后一条 say 承担总结：模型显式写 summary 时用它的 seq，只写了
	// progress/chat 时退到最后一条。线上补偿回合（llmux #19）就是后者 —— 汇报
	// 看着像总结，任务摘要却停在节点原文。
	if state != nil && state.settleSummarySeq == action.Seq && action.Purpose != "summary" {
		action.Purpose = "summary"
	}
	// 收尾回合里总结之后的进度汇报不再进可见流（M1）：它只是把刚写下的总结再
	// 念一遍，用户看到的就是「进度和汇报老是重复」。正文照旧落库（审计/回看要
	// 原文），只是标成 hidden —— 也不能并进总结，那会让总结超过 600 字上限。
	redundantProgress := state != nil && state.settleTurn && state.settleSummarySeen &&
		action.Purpose == "progress"
	if action.Purpose == "summary" && state != nil {
		state.settleSummarySeen = true
	}
	// 转交口信默认不发（Plan 2026-11-22，W1-5）：同一决策里已经有执行动作时，
	// 「已交给执行会话，完成后我会汇总」这类 progress 只是把占位重复一遍，
	// 线上 #100 的对话流里 19/45 条就是它。占位留给执行会话自己的回复。
	if action.Purpose == "progress" && decision.TriggerKind == RouterTriggerUserMessage && decisionHasHandoff(decision) {
		return nil
	}
	act := state.take()
	if act == nil {
		effectID := routeEffectActivityID(decision, action)
		act = s.newAgentActivity(task, CommentWorkflowChoiceEmpty)
		if effectID != "" {
			act.ID = effectID
		}
		act.Body = body
		act.Status = ActivityStatusCompleted
		// 落库前就带上 hidden：插入时会一并写 hidden 列，activity.created 帧
		// 因此不会让前端先渲染一帧再删掉。
		act.Hidden = redundantProgress
		if err := s.insertRouteReply(ctx, act, effectID != ""); err != nil {
			return err
		}
	} else {
		if err := s.activities.UpdateBody(ctx, act.ID, body); err != nil {
			return err
		}
		if err := s.activities.UpdateStatus(ctx, act.ID, ActivityStatusCompleted, nil); err != nil {
			return err
		}
		act.Body = body
		act.Status = ActivityStatusCompleted
		if redundantProgress {
			if err := s.activities.SetHidden(ctx, act.ID, true); err != nil {
				return err
			}
			act.Hidden = true
		}
	}
	fields, err := s.markRouteReply(ctx, decision, userAct, act, action)
	if err != nil {
		return err
	}
	changes := map[string]any{
		"body": body, "status": ActivityStatusCompleted,
		"purpose": fields["purpose"], "ordinal": action.Seq,
		"reply_to_id": routeUserActivityID(userAct), "router_decision_id": decision.ID,
	}
	if redundantProgress {
		changes["hidden"] = true
		changes["hidden_reason"] = "redundant_progress"
	}
	s.broadcastUpdated(act, changes)
	// purpose=summary 是 Router 对这一轮结果写下的总结：它同时成为任务结果摘要，
	// 看板卡片/任务列表读的就是这一栏。线上 #99 的卡片上那段「changes.Now update…」
	// 是节点回复原文的直接截断，只有结算回合的总结能覆盖它。
	// 与下面「用户回合正文即摘要」的兜底不同，这条走无条件写入：任务可能已经
	// 是 success/waiting_user，recordReplySummaryIfActive 在终态上不写。
	if act.Purpose == "summary" {
		return s.tasks.UpdateTaskResultSummary(ctx, task.ID, body)
	}
	// 纯回复没有 Run 可投影任务结果。只有这一轮**没有执行动作**的 chat 回答才算
	// 结果摘要：progress 是过程口信，带执行动作的 chat 是开场白，两者都没有总结
	// 所有权（W1-1）。任务头仍然要停到「待审核」。
	if act.RunID == "" && decision.TriggerKind == RouterTriggerUserMessage {
		// 这一轮已经回了话、又没有 Run 可投影任务头：任务必须停在「待审核」，
		// 不能退回「未开始」（用户实测：#87 回复完成后卡片仍是未开始）。
		s.markTurnTaskWaitingUser(ctx, task.ID)
		if act.Purpose == "chat" && !decisionHasHandoff(decision) {
			return s.tasks.recordReplySummaryIfActive(ctx, task.ID, body)
		}
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
	fields, err := s.markRouteReply(ctx, decision, userAct, act, action)
	if err != nil {
		return err
	}
	s.broadcastUpdated(act, map[string]any{
		"body": body, "status": ActivityStatusWaitingUser,
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
