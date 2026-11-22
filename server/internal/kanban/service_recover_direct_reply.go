package kanban

import (
	"context"
	"log"
)

// restartInterruptedReplySummary 是「重启把在飞回合带走」的统一说明。#86 的
// workflow 回复与直连回复共用同一句：用户看到的都是「上一轮没有结果，可以重来」。
const restartInterruptedReplySummary = "进程重启时这一轮还在执行，结果无法确定；可以直接发消息让我重开一轮。"

// settleOrphanDirectReplies 结算重启后无人推进的直连回复。
//
// 直连回合（Router `message_agent` / 纯对话）的收尾写在跑它的 goroutine 里，
// 进程重启把那条 goroutine 一起带走，于是活动永远停在 running/pending、任务
// 永远停在 running：卡片不落「待审核」，自动归档也永远轮不到它（#95 现场：
// 04:21 起转到次日）。workflow 侧有 settleWorkflowReplyUnknown，这条路当时没补。
//
// 判据分两层：
//   - run_id 为空 —— 绑定 Run 的回复归 Run 侧结算，两条路径互不重叠；
//   - 任务主会话此刻不在回复 —— 对账虽在装配期同步执行（此时进程内不可能有
//     回合），探针仍作为兜底：将来若把对账挪到后台或加第二次调用，绝不误杀
//     一轮活着的回合。
//
// 幂等：条件更新只吃 pending/running，已结算的行第二次启动不会再动。
func (s *Service) settleOrphanDirectReplies(ctx context.Context, store *TaskStore) {
	if s == nil || store == nil {
		return
	}
	activities := NewActivityStore(store.db)
	orphans, err := activities.ListOrphanDirectReplies(ctx)
	if err != nil {
		log.Printf("[kanban/recover] direct reply scan failed err=%v", err)
		return
	}
	sessionKeys := map[string]string{}
	settled := map[string]bool{}
	for _, activity := range orphans {
		key, cached := sessionKeys[activity.TaskID]
		if !cached {
			task, err := store.GetTask(ctx, activity.TaskID)
			if err == nil {
				key = task.MainSessionKey
			}
			sessionKeys[activity.TaskID] = key
		}
		if s.sessionRunning(key) {
			continue
		}
		updated, changed, err := activities.SettleOrphanReply(ctx, activity.ID, restartInterruptedReplySummary)
		if err != nil {
			log.Printf("[kanban/recover] settle direct reply failed id=%s err=%v", activity.ID, err)
			continue
		}
		if !changed {
			continue
		}
		s.broadcastActivityUpdated(updated, map[string]any{
			"status":  ActivityStatusCancelled,
			"summary": restartInterruptedReplySummary,
			"body":    "",
		})
		// 进度正文（"步骤 i/n"）属于运行中提示，这一轮已经结束，留着会被当成终态文案。
		_ = activities.ClearBody(ctx, updated.ID)
		// 结算只是把现场收干净；还要让 Router 知道「这一轮缺一个结果」，否则
		// 重启就等于安静地把活丢了（W3-3）。
		s.recordTurnInterruptedEvent(ctx, store, activities, updated)
		settled[updated.TaskID] = true
	}
	for taskID := range settled {
		s.resumeTaskAfterOrphanReply(ctx, store, activities, taskID)
	}
}

// resumeTaskAfterOrphanReply 把「唯一那一轮已经没了」的任务从 running 放回
// 待审核：否则卡片永远停在执行中，自动归档（只收终态）也永远轮不到它。
//
// 三道闸门缺一不可：任务必须确实停在 running（暂停/终态/待审核都是操作员
// 状态，不许覆盖）、会话此刻不在回复、任务上也没有别的活着回复（workflow
// 侧刚被唤醒那一轮仍挂在 running，不能顺手把任务拉走）。
func (s *Service) resumeTaskAfterOrphanReply(ctx context.Context, store *TaskStore, activities *ActivityStore, taskID string) {
	task, err := store.GetTask(ctx, taskID)
	if err != nil || task.Status != StatusRunning {
		return
	}
	if s.sessionRunning(task.MainSessionKey) {
		return
	}
	if live, err := activities.LiveReplyExists(ctx, taskID); err != nil || live {
		return
	}
	if err := store.UpdateTaskStatus(ctx, taskID, StatusWaitingUser, false); err != nil {
		log.Printf("[kanban/recover] resume task failed task=%s err=%v", taskID, err)
		return
	}
	s.broadcastTask(ctx, store, taskID)
}
