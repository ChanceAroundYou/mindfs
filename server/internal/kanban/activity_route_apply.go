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
	}
	userAct, err := s.activities.Get(ctx, strings.TrimSpace(decision.UserActivityID))
	if err != nil {
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
	act := state.take()
	if act == nil {
		effectID := routeEffectActivityID(decision, action)
		act = s.newAgentActivity(task, CommentWorkflowChoiceEmpty)
		if effectID != "" {
			act.ID = effectID
		}
		act.Body = body
		act.Status = ActivityStatusCompleted
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
	// purpose=summary 是 Router 对这一轮结果写下的总结：它同时成为任务结果摘要，
	// 看板卡片/任务列表读的就是这一栏。线上 #99 的卡片上那段「changes.Now update…」
	// 是节点回复原文的直接截断，只有结算回合的总结能覆盖它。
	// 与下面「用户回合正文即摘要」的兜底不同，这条走无条件写入：任务可能已经
	// 是 success/waiting_user，recordReplySummaryIfActive 在终态上不写。
	if act.Purpose == "summary" {
		return s.tasks.UpdateTaskResultSummary(ctx, task.ID, body)
	}
	// 纯回复没有 Run 可投影任务结果，正文就是这一轮的结果摘要。运行期事件的
	// 汇报（trigger != user_message）不得覆盖用户回合留下的任务结果摘要。
	if act.RunID == "" && decision.TriggerKind == RouterTriggerUserMessage {
		// 这一轮已经回了话、又没有 Run 可投影任务头：任务必须停在「待审核」，
		// 不能退回「未开始」（用户实测：#87 回复完成后卡片仍是未开始）。
		s.markTurnTaskWaitingUser(ctx, task.ID)
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
		"reply_to_id": userAct.ID, "router_decision_id": decision.ID,
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
	begun, err := s.beginWorkflowRunVersion(ctx, task, version, userAct.ID, act, action.SessionMode)
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
		"reply_to_id": userAct.ID, "router_decision_id": decision.ID,
	})
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

// routeEffectActivityID 给出运行期事件回合的确定活动 ID。用户留言保持既有语义：
// 复用同步占位，或每次新起随机 ID 的活动。
func routeEffectActivityID(decision *RouterDecision, action RouterAction) string {
	if decision == nil || decision.TriggerKind == RouterTriggerUserMessage {
		return ""
	}
	return routerEventEffectID(decision.ID, action.Seq)
}

// insertRouteReply 插入一条回复活动。确定 ID 路径用 InsertOrIgnore：已存在同一行
// 时只更新正文/状态（可广播 updated），不插第二行、不广播 created。
func (s *ActivityService) insertRouteReply(ctx context.Context, act *TaskActivity, deterministic bool) error {
	if !deterministic {
		if err := s.activities.Insert(ctx, act); err != nil {
			return err
		}
		s.broadcastCreated(act)
		return nil
	}
	created, err := s.activities.InsertOrIgnore(ctx, act)
	if err != nil {
		return err
	}
	if created {
		s.broadcastCreated(act)
		return nil
	}
	if err := s.activities.UpdateBody(ctx, act.ID, act.Body); err != nil {
		return err
	}
	return s.activities.UpdateStatus(ctx, act.ID, act.Status, nil)
}

// settledRunSucceeded 判定这条 run_settled 决策背后是不是一轮真的成功结算的
// Run。运行期事件里也有 kind=run_settled 但任务根本没有 Run 的合成回合（T8
// 的运行时汇报），那些只是进度说明，不能顶掉任务结果摘要。
func (s *ActivityService) settledRunSucceeded(ctx context.Context, decision *RouterDecision) bool {
	if s.tasks == nil || decision == nil || decision.TriggerKind != RouterTriggerRunSettled {
		return false
	}
	runID := strings.TrimSpace(decision.TriggerRef)
	if runID == "" {
		return false
	}
	run, err := s.tasks.WorkflowRunByID(ctx, runID)
	if err != nil || run == nil {
		return false
	}
	return ProjectStatusFromRun(run) == StatusSuccess
}

// settleSummaryActionSeq 决定结算回合里哪一条 say 承担总结：模型显式写了
// purpose=summary 就用它；只写了 progress/chat（线上补偿回合的实测形态）时
// 取最后一条 say。没有 say 返回 -1。
func settleSummaryActionSeq(decision *RouterDecision) int {
	if decision == nil {
		return -1
	}
	last := -1
	for _, action := range decision.Actions {
		if action.Kind != RouterActionSay {
			continue
		}
		if strings.TrimSpace(action.Purpose) == "summary" {
			return action.Seq
		}
		last = action.Seq
	}
	return last
}

// markRouteReply 写 Router 归属字段（决策/回复对象/用途/顺序）。运行期事件的
// 汇报默认 purpose=progress；用户留言仍是 reply 或调用方指定值。
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
		if decision.TriggerKind != RouterTriggerUserMessage {
			purpose = "progress"
		}
	}
	// purpose=summary 只对「Run 成功结算」这一轮成立。模型在别的回合写了 summary
	// 也不该让整轮失败（那会把一条汇报连同回合一起丢掉）：降级成 progress，正文
	// 照常发给用户，只是不写任务结果摘要。
	if purpose == "summary" && decision.TriggerKind != RouterTriggerRunSettled {
		purpose = "progress"
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

// routeOptionLabels 是卡片用的选项标签：与正文同源（同样去空、同样顺序），
// 但不带正文里的项目符号 —— 卡片按钮自己就是图形，再叠一个「· 」只是噪声。
func routeOptionLabels(options []string) []string {
	out := make([]string, 0, len(options))
	for _, option := range options {
		if trimmed := strings.TrimSpace(option); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
