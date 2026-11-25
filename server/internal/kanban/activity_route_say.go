package kanban

import (
	"context"
	"errors"
	"strings"
)

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
	// 「实质变化」才覆盖卡片（S4）：零工具的系统结算回合把上一段总结换个说法
	// 再写一遍时，降级成 progress —— 正文照常可见，卡片保留上一条实质结论。
	if action.Purpose == "summary" && demoteRepeatedSettlementSummary(decision, task, body) {
		action.Purpose = "progress"
	}
	// 巡检零变化不重复出声（S5）：同一任务、同一触发类、同一任务状态、同一段
	// 正文 30 分钟内只留第一条可见，其余落库但标 hidden。被拒动作走的是另一条
	// 可见说明通道（S2），不会被这里吞掉。
	hiddenReason := ""
	if inspectionDedupeApplies(decision.TriggerKind) &&
		s.inspectionDedupeFn().Repeated(task.ID, string(decision.TriggerKind), task.Status, body) {
		hiddenReason = "duplicate_inspection"
	}
	// 收尾回合里总结之后的进度汇报不再进可见流（M1）：它只是把刚写下的总结再
	// 念一遍，用户看到的就是「进度和汇报老是重复」。正文照旧落库（审计/回看要
	// 原文），只是标成 hidden —— 也不能并进总结，那会让总结超过存储上限。
	if hiddenReason == "" && state != nil && state.settleTurn && state.settleSummarySeen &&
		action.Purpose == "progress" {
		hiddenReason = "redundant_progress"
	}
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
		act.Hidden = hiddenReason != ""
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
		if hiddenReason != "" {
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
	if hiddenReason != "" {
		changes["hidden"] = true
		changes["hidden_reason"] = hiddenReason
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
