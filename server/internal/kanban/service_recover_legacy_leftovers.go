package kanban

import (
	"context"
	"errors"
	"log"
	"time"
)

// legacyImportLeftoverSummary 是结案文案：任务早已结束，这一轮不会再继续，
// 但详情页要能解释「为什么这条回复是取消」。
const legacyImportLeftoverSummary = "历史任务迁移时留下的执行快照，任务已经结束；这一轮不会再继续。"

// settleLegacyImportLeftovers 收尾落在已结束任务上的历史迁移审计 Run。
//
// 这是一类单独的死角：legacy Run 不进 recoverWorkflowRunsOnStartup（按
// triggered_by 排除），它的回复 run_id 也不为空，直连孤儿结算同样收不到。
// 结果是迁移那一刻在跑的节点与回复永远显示「正在想」。任务非终态的 Run 不碰。
func (s *Service) settleLegacyImportLeftovers(ctx context.Context, store *TaskStore) {
	if s == nil || store == nil {
		return
	}
	refs, err := store.ListLegacyImportRunsOnTerminalTasks(ctx)
	if err != nil {
		log.Printf("[kanban/recover] legacy_import.list.error err=%v", err)
		return
	}
	if len(refs) == 0 {
		return
	}
	activities := NewActivityStore(store.db)
	now := time.Now().UTC()
	for _, ref := range refs {
		cancelled, err := settleLegacyImportRunWithRetry(ctx, store, ref.RunID, now)
		if err != nil {
			log.Printf("[kanban/recover] legacy_import.settle.error task=%s run=%s err=%v", ref.TaskID, ref.RunID, err)
			continue
		}
		replies, err := activities.ListLiveRepliesByRun(ctx, ref.RunID)
		if err != nil {
			log.Printf("[kanban/recover] legacy_import.replies.error task=%s run=%s err=%v", ref.TaskID, ref.RunID, err)
			continue
		}
		settled := 0
		for _, reply := range replies {
			updated, changed, err := activities.SettleOrphanReply(ctx, reply.ID, legacyImportLeftoverSummary)
			if err != nil {
				log.Printf("[kanban/recover] legacy_import.reply.error task=%s act=%s err=%v", ref.TaskID, reply.ID, err)
				continue
			}
			if !changed {
				continue
			}
			settled++
			s.broadcastActivityUpdated(updated, map[string]any{
				"status":  ActivityStatusCancelled,
				"summary": legacyImportLeftoverSummary,
			})
			// 进度正文属于运行中提示；历史快照已经结束，留着会被当成终态文案。
			_ = activities.ClearBody(ctx, reply.ID)
		}
		if len(cancelled) > 0 || settled > 0 {
			log.Printf("[kanban/recover] legacy_import.settled task=%s run=%s node_runs=%d replies=%d",
				ref.TaskID, ref.RunID, len(cancelled), settled)
		}
	}
}

// settleLegacyImportRunWithRetry 重试投影 CAS 冲突。启动对账期间写者很少，
// 但另一路（如用户同时操作）不能把这一条永久跳过：下次启动才会再扫到它。
func settleLegacyImportRunWithRetry(ctx context.Context, store *TaskStore, runID string, finishedAt time.Time) ([]string, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		cancelled, err := store.SettleLegacyImportRun(ctx, runID, finishedAt)
		if err == nil {
			return cancelled, nil
		}
		lastErr = err
		if !errors.Is(err, ErrRunConflict) {
			return nil, err
		}
	}
	return nil, lastErr
}
