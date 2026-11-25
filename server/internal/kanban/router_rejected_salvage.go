package kanban

import (
	"context"
	"log"
	"strconv"
	"strings"
)

// 校验失败时的文字保留（S2）。
//
// 整轮 rejected 的判断没错：半套动作比没有动作更危险，执行/监管类动作必须一起
// 拒。但**纯文本**没有副作用 —— 模型写下的结论完全可以留下。线上 5 条
// `ask_user must be the only action` 全是没有来源留言的结算回合，旧路径无处
// 回落，用户侧因此零可见：模型写得再准也跟着动作一起消失。
//
// 调用边界：用户留言回合仍走 fallbackRoute（按留言重新路由，同一条回复不能被两
// 条路径各写一遍），只有「没有留言可回落」的回合才调这里 —— 结算/巡检回合在
// route 模式下没有占位可复用，运行期事件回合则干脆没有旧路径。

// shouldSalvageRejectedTurn 判定被拒回合走「保留正文」还是「回落旧路径」。
// 判据是触发类而不是 UserActivityID：运行期事件（turn_settled 等）也会带上它
// 记录的那条留言 ID（线上 20 条 rejected 全是这样），按 ID 是否为空判断会把
// 生产上的主路径漏掉。用户留言回合才有「那条留言」可供 FallbackRoute 重新路由。
func shouldSalvageRejectedTurn(decision *RouterDecision) bool {
	return decision != nil && decision.TriggerKind != RouterTriggerUserMessage
}

// salvageRejectedTurnText 把被整轮拒绝的决策里那段纯文本 say 按正常回复应用，
// 再写一条确定 ID 的系统说明解释「为什么这一轮只留下结论」。
//
// 应用走 ApplyRouteActions 而不是直接写库：可见回复、purpose 归属、任务摘要
// 所有权（只有结算类触发才写卡片）都沿用既有语义，不新开一条旁路。
func (rs *RouterService) salvageRejectedTurnText(
	ctx context.Context,
	decision *RouterDecision,
	actions []RouterAction,
	reason string,
) {
	if rs == nil || rs.service == nil || decision == nil {
		return
	}
	say, salvaged := salvagedRejectedSay(actions)
	svc, err := rs.service.ActivityServiceForRoot(rs.rootID)
	if err != nil {
		log.Printf("[router] reject.salvage.unavailable id=%s task=%s err=%v", decision.ID, decision.TaskID, err)
		return
	}
	if salvaged {
		// 副本：落库的仍是整轮 rejected 的原始动作，救回的只有这一条 say。
		// 来源留言与占位一并清空 —— 这条 say 是独立的新回复，不该改写别人的行。
		rescue := *decision
		rescue.Actions = []RouterAction{say}
		rescue.UserActivityID = ""
		rescue.PlaceholderActivityID = ""
		if err := svc.ApplyRouteActions(ctx, &rescue); err != nil {
			log.Printf("[router] reject.salvage.apply.error id=%s task=%s err=%v", decision.ID, decision.TaskID, err)
		}
	}
	if err := svc.recordRejectedTurnNotice(ctx, decision, salvagedRejectedSeq(actions), reason, salvaged); err != nil {
		log.Printf("[router] reject.salvage.notice.error id=%s task=%s err=%v", decision.ID, decision.TaskID, err)
	}
}

// salvagedRejectedSay 挑出可以保留的那条纯文本：只看 say，正文非空且不超单条
// 上限（正是超限被拒时不能再把它原样塞回去）。多条时取最后一条 —— 模型习惯把
// 结论写在最后。
//
// 救回的那条一律按 summary 应用（S2 的批准口径）：同一决策里带执行/监管动作时
// 模型写下的 say 通常就是这一轮的结论，而 progress/chat 只进流、不承担摘要所有权，
// 卡片会继续停在节点原文上。按 summary 落库后它既进可见流，也覆盖任务摘要。
func salvagedRejectedSay(actions []RouterAction) (RouterAction, bool) {
	var picked RouterAction
	found := false
	for _, action := range actions {
		if action.Kind != RouterActionSay {
			continue
		}
		if strings.TrimSpace(action.Body) == "" || len(action.Body) > routerActionBodyMaxBytes {
			continue
		}
		picked = action
		found = true
	}
	if found {
		picked.Purpose = "summary"
	}
	return picked, found
}

// salvagedRejectedSeq 是拒绝说明挂的序号：取第一个非 say 动作（真正被拒的那条），
// 没有就落到 0。ID 由决策 + 序号确定，重放只命中同一行。
func salvagedRejectedSeq(actions []RouterAction) int {
	for _, action := range actions {
		if action.Kind != RouterActionSay {
			return action.Seq
		}
	}
	return 0
}

// recordRejectedTurnNotice 写一条可见系统说明。没有它，整轮 rejected 在用户侧
// 只剩「什么都没发生」—— 现场 5 条决策就是这样，日志里有原因、界面上零变化。
func (s *ActivityService) recordRejectedTurnNotice(
	ctx context.Context,
	decision *RouterDecision,
	seq int,
	reason string,
	salvaged bool,
) error {
	if s == nil || s.activities == nil || decision == nil {
		return nil
	}
	act := &TaskActivity{
		ID:     "route-rejected:" + decision.ID + ":" + strconv.Itoa(seq),
		TaskID: decision.TaskID, RootID: decision.RootID,
		Type: ActivityTypeAttention, Author: "system", Role: "system",
		Status: ActivityStatusCompleted,
		// 整轮决策被拒、动作一个都没执行：这是这一轮没做成，标 error。
		Severity: ActivitySeverityError,
		Body:     rejectedTurnNoticeBody(reason, salvaged),
	}
	created, err := s.activities.InsertOrIgnore(ctx, act)
	if err != nil {
		return err
	}
	if created {
		s.broadcastCreated(act)
	}
	return nil
}

func rejectedTurnNoticeBody(reason string, salvaged bool) string {
	var b strings.Builder
	b.WriteString("这一轮决策没有整轮执行：")
	b.WriteString(reason)
	b.WriteString("。有副作用的动作一个都没有执行")
	if salvaged {
		b.WriteString("；模型已经写下的结论单独保留在上面那条回复里")
	} else {
		b.WriteString("；这一轮也没有可保留的纯文本结论")
	}
	b.WriteString("。需要推进请在任务里说一句，或让 Router 用提问卡征得你的意见。")
	return b.String()
}
