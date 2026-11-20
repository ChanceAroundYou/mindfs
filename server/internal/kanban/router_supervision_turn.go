package kanban

import (
	"context"
	"log"
	"strings"
)

// stopSupervisionForUser 是「监管预算用尽」的收场动作（2026-11-20 用户裁定：
// 停 waiting_user，由用户接手）。它做三件事，缺一不可：
//   - 决策标 rejected 并带上原因（账本里能查到为什么没重试）；
//   - 写一条可见系统行，让用户看到「机器已经停手」而不是任务无声停在失败态；
//   - 把仍停在失败态的任务放回 waiting_user（取消/已完成不复活）。
//
// 事件标 done 而不是 failed：预算用尽是一次正常的收尾，不是执行失败；failed
// 会让启动对账/重扫把它当成「这一轮没处理完」再拉起来。
func (rs *RouterService) stopSupervisionForUser(ctx context.Context, decision *RouterDecision, eventID, reason string) {
	if rs == nil || rs.store == nil || decision == nil {
		return
	}
	if err := rs.store.MarkDecisionStatus(ctx, decision.ID, RouterDecisionRejected, reason); err != nil {
		log.Printf("[router/coordinate] decision.fail.error id=%s err=%v", decision.ID, err)
	}
	decision.Status = RouterDecisionRejected
	decision.Error = reason
	rs.broadcast(decision)

	reason = strings.TrimSpace(reason)
	activity := &TaskActivity{
		ID: "supervision-budget:" + decision.ID, TaskID: decision.TaskID, RootID: decision.RootID,
		Type: ActivityTypeAttention, Author: "system", Role: "system",
		Status: ActivityStatusCompleted,
		Body:   "监管动作预算已用尽（" + reason + "）。这一轮不再自动重试，任务停在待审核；确认后可以手动重试节点或结束任务。",
	}
	store := rs.taskStore()
	if store != nil {
		created, err := NewActivityStore(store.db).InsertOrIgnore(ctx, activity)
		if err != nil {
			log.Printf("[router/coordinate] supervision.notice.error task=%s err=%v", decision.TaskID, err)
		} else if created {
			rs.service.broadcastWorkflowActivity(ctx, store, activity, true, nil)
		}
		if task, err := store.GetTask(ctx, decision.TaskID); err == nil {
			switch task.Status {
			case StatusWaitingUser, StatusCancelled, StatusSuccess:
				// 已是待审核 / 终态：不改任务行。
			default:
				if err := store.UpdateTaskStatus(ctx, decision.TaskID, StatusWaitingUser, false); err != nil {
					log.Printf("[router/coordinate] supervision.waiting_user.error task=%s err=%v", decision.TaskID, err)
				}
			}
		}
	}
	rs.completeCoordinatedEvent(ctx, eventID, RouterEventDone, "supervision_budget_exhausted: "+reason)
}
