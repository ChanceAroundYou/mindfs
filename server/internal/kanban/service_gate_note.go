package kanban

import (
	"context"
	"log"
)

// dispatchQueuedGateNote 在审批这一轮成功收尾后，把审批附言交给下一轮。
//
// 附言由 ResolveWorkflowHumanGate 以排队用户留言的形式落库（见
// QueueUserActivity），这里只负责在「这一轮真的跑完了」时把它派发出去：
// route 模式下 dispatchUserActivity 会把它交给 Router 决策。
//
// 只认 StatusSuccess：失败/取消的回合没有“下一轮”，附言留在队列里不假装执行。
// DispatchQueuedUserActivity 自己按队列领取，重复调用是幂等空操作。
func (s *Service) dispatchQueuedGateNote(ctx context.Context, store *TaskStore, run *WorkflowRun) {
	if s == nil || store == nil || run == nil {
		return
	}
	if ProjectStatusFromRun(run) != StatusSuccess {
		return
	}
	activitySvc, err := s.ActivityServiceForRoot(store.root.ID)
	if err != nil {
		log.Printf("[kanban/human-gate] note dispatch skipped task=%s err=%v", run.TaskID, err)
		return
	}
	if _, _, err := activitySvc.DispatchQueuedUserActivity(ctx, run.TaskID); err != nil {
		log.Printf("[kanban/human-gate] note dispatch failed task=%s err=%v", run.TaskID, err)
	}
}
