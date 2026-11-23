package kanban

import (
	"context"
	"log"
)

// closeSupersededQuestionsOnStartup 收掉升级前遗留的孤儿等待卡：同一会话里
// Agent 已经用新的 tool_use_id 重问过，更早那张卡既不会被去重命中，也不会被
// 任何收尾路径碰到，却一直挡着任务对话的输入框（现场 #104）。
//
// 运行期由 RecordTaskQuestion 在插入新卡时同步收口；这里补的是那之前已经
// 落库的存量。只动 waiting_user 的提问卡，且必须存在更晚的提问卡才收 ——
// 同会话只问了一次、还在等人回答的卡不受影响。
func (s *Service) closeSupersededQuestionsOnStartup(ctx context.Context, store *TaskStore) {
	if s == nil || store == nil {
		return
	}
	acts := NewActivityStore(store.db)
	stale, err := acts.ListSupersededOpenQuestions(ctx)
	if err != nil {
		log.Printf("[kanban/recover] superseded questions: %v", err)
		return
	}
	reason := supersededByNewerQuestion
	closed := 0
	for _, card := range stale {
		updated, changed, err := acts.ResolvePrompt(ctx, card.ID, ActivityStatusCancelled, card.PayloadJSON, &reason)
		if err != nil {
			log.Printf("[kanban/recover] close superseded question %s: %v", card.ID, err)
			continue
		}
		if !changed {
			// 并行收口（回合终结 / 用户答题）先到，这里什么都不用做。
			continue
		}
		closed++
		s.broadcastActivityUpdated(updated, map[string]any{
			"status":  ActivityStatusCancelled,
			"summary": reason,
		})
	}
	if closed > 0 {
		log.Printf("[kanban/recover] superseded questions closed=%d", closed)
	}
}
