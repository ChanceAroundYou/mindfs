package kanban

import (
	"context"
	"log"
	"strings"
)

// TerminateDirectTurn 叫停任务主会话里正在跑的直连回合，并把在飞回复结算成
// cancelled。任务头由调用方（Service.Terminate）写回 waiting_user —— 这里只管
// 回合与回复行，避免两条路各写一次状态。
//
// 顺序：先登记「被终止」，再打断会话。反过来（先打断、后登记）回合可能在标记
// 落下之前就收尾成 failed，用户主动叫停就显示成一条红色失败。
func (s *ActivityService) TerminateDirectTurn(ctx context.Context, task Task, reason string) {
	if s == nil || s.activities == nil {
		return
	}
	lives, err := s.activities.ListLiveDirectReplies(ctx, task.ID)
	if err != nil {
		log.Printf("[kanban/terminate] list live replies failed task=%s err=%v", task.ID, err)
		return
	}
	ids := make([]string, 0, len(lives))
	for _, activity := range lives {
		ids = append(ids, activity.ID)
	}
	s.terminations.Mark(ids...)
	if fn := s.sessionTurnCanceller; fn != nil {
		key := strings.TrimSpace(task.MainSessionKey)
		if key != "" {
			if err := fn(ctx, task.RootID, key); err != nil {
				log.Printf("[kanban/terminate] cancel session failed task=%s session=%s reason=%q err=%v", task.ID, key, reason, err)
			}
		}
	}
	for _, activity := range lives {
		updated, changed, err := s.activities.SettleOrphanReply(ctx, activity.ID, terminatedTurnSummary)
		if err != nil {
			log.Printf("[kanban/terminate] settle reply failed id=%s err=%v", activity.ID, err)
			continue
		}
		if !changed {
			continue
		}
		s.broadcastUpdated(updated, map[string]any{
			"status":  ActivityStatusCancelled,
			"summary": terminatedTurnSummary,
			"body":    "",
		})
		// 进度正文（"步骤 i/n"）属于运行中提示，这一轮已经结束，留着会被当成终态文案。
		_ = s.activities.ClearBody(ctx, updated.ID)
	}
}
