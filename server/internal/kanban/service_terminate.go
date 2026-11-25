package kanban

import "context"

// Terminate 是「终止」这个操作员动作：停掉正在跑的东西（工作流 Run 或直连回合），
// 但任务回到「待处理」而不是取消 —— 用户可以接着说话，旧现场（Run 投影、回复行、
// 会话转录）原样保留为证据。
//
// 与 Cancel 的分工：Cancel 把任务收成终态 cancelled，卡片落「已结束」；Terminate
// 只停当前这轮活，卡片留在「待处理」。两者都要停 Run / 打断回合，区别只在最后
// 写哪个状态，所以「停 Run」那段与 Router 的 cancel_run 共用同一个实现。
func (s *Service) Terminate(ctx context.Context, in MoveInput) (TaskDetail, error) {
	store, task, err := s.loadForMove(ctx, in.RootID, in.TaskID)
	if err != nil {
		return TaskDetail{}, err
	}
	if isTerminalStatus(task.Status) {
		return store.GetDetail(ctx, task.ID)
	}
	if _, wf, werr := s.workflowRunForTask(ctx, store, task.ID); werr != nil {
		return TaskDetail{}, werr
	} else if wf {
		if err := s.stopWorkflowRunToWaitingUser(ctx, in.RootID, task.ID, in.Reason, "task_terminated"); err != nil {
			return TaskDetail{}, err
		}
		return store.GetDetail(ctx, task.ID)
	}
	// 直连（无 Run）：打断主会话的当前回合，把在飞回复结算成 cancelled；任务头
	// 由这里统一写回待处理，ActivityService 只负责回合与回复行。
	if activitySvc, svcErr := s.ActivityServiceForRoot(task.RootID); svcErr == nil && activitySvc != nil {
		activitySvc.TerminateDirectTurn(ctx, task, in.Reason)
	}
	return s.setTaskStatus(ctx, in.RootID, task.ID, StatusWaitingUser, "task_terminated", in.Reason, false)
}

// SetSessionTurnCanceller 装配「按会话打断当前回合」的端口。装配期调用一次。
func (s *Service) SetSessionTurnCanceller(fn func(ctx context.Context, rootID, sessionKey string) error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.sessionTurnCanceller = fn
	s.mu.Unlock()
}

func (s *Service) sessionTurnCancellerFn() func(ctx context.Context, rootID, sessionKey string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionTurnCanceller
}

// turnTerminationsRegistry 返回跨 ActivityService 实例共享的终止登记簿。
func (s *Service) turnTerminationsRegistry() *TurnTerminations {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.turnTerminations == nil {
		s.turnTerminations = newTurnTerminations()
	}
	return s.turnTerminations
}

// inspectionDedupeRegistry 返回跨 ActivityService 实例共享的巡检去重登记簿（S5）。
// 与 turnTerminationsRegistry 同构：进程内一份，懒建（零值 Service 也能用）。
func (s *Service) inspectionDedupeRegistry() *InspectionDedupe {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inspectionDedupe == nil {
		s.inspectionDedupe = newInspectionDedupe()
	}
	return s.inspectionDedupe
}
