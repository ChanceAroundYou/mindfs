package kanban

import (
	"context"
	"strings"
	"time"
)

// 任务级动作：暂停 / 恢复 / 完成 / 取消 / 失败 / 删除。
//
// 任务没有阶段，所以这里不再有「推进到下一段」之类的动词 —— 一轮对话由
// Router（ActivityService）在用户留言时发起，任务状态只是这一轮运行的投影。

func (s *Service) Pause(ctx context.Context, in MoveInput) (TaskDetail, error) {
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
		if err := s.PauseWorkflowRun(ctx, in.RootID, task.ID, in.Reason); err != nil {
			return TaskDetail{}, err
		}
		return store.GetDetail(ctx, task.ID)
	}
	return s.setTaskStatus(ctx, in.RootID, in.TaskID, StatusPaused, "paused", in.Reason, false)
}

func (s *Service) Resume(ctx context.Context, in MoveInput) (TaskDetail, error) {
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
		if err := s.ResumeWorkflowRun(ctx, in.RootID, task.ID, in.Reason); err != nil {
			return TaskDetail{}, err
		}
		return store.GetDetail(ctx, task.ID)
	}
	if _, err := s.setTaskStatus(ctx, in.RootID, in.TaskID, StatusRunning, "resumed", in.Reason, false); err != nil {
		return TaskDetail{}, err
	}
	// 暂停的也可能是「排队中」的任务：恢复同样要把排队留言交给 Router，
	// 否则状态回到 running 但这一轮永远不开始。
	if activitySvc, svcErr := s.ActivityServiceForRoot(task.RootID); svcErr == nil {
		if _, _, err := activitySvc.DispatchQueuedUserActivity(ctx, task.ID); err != nil {
			return TaskDetail{}, err
		}
	}
	return store.GetDetail(ctx, task.ID)
}

// RunNow：没有工作流运行的任务只需要把暂停拨回运行；纯对话任务的回合由
// 用户留言驱动，按钮本身不另起一轮。已有工作流时走唤醒/恢复。
//
// 例外是「新建任务时勾掉了立即执行」：首条留言以 queued 落库、一直没人派发。
// 这里的「立即执行」就是把它交给 Router —— 先来先跑，只派发最早一条。
func (s *Service) RunNow(ctx context.Context, in MoveInput) (TaskDetail, error) {
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
		if task.Status == StatusPaused {
			if err := s.ResumeWorkflowRun(ctx, in.RootID, task.ID, in.Reason); err != nil {
				return TaskDetail{}, err
			}
		} else if err := s.WakeWorkflowRun(ctx, in.RootID, task.ID, "run_now"); err != nil {
			return TaskDetail{}, err
		}
		return store.GetDetail(ctx, task.ID)
	}
	if task.Status == StatusPaused {
		return s.Resume(ctx, in)
	}
	if activitySvc, svcErr := s.ActivityServiceForRoot(task.RootID); svcErr == nil {
		if _, _, err := activitySvc.DispatchQueuedUserActivity(ctx, task.ID); err != nil {
			return TaskDetail{}, err
		}
	}
	return store.GetDetail(ctx, task.ID)
}

func (s *Service) Fail(ctx context.Context, in MoveInput) (TaskDetail, error) {
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
		return s.finishWorkflowRunByOperator(ctx, in.RootID, task.ID, StatusFail, "task_failed", WorkflowEventRunFailed, in.Reason)
	}
	return s.setTaskStatus(ctx, in.RootID, in.TaskID, StatusFail, "task_failed", in.Reason, true)
}

func (s *Service) Cancel(ctx context.Context, in MoveInput) (TaskDetail, error) {
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
		if err := s.CancelWorkflowRun(ctx, in.RootID, task.ID, in.Reason); err != nil {
			return TaskDetail{}, err
		}
		return store.GetDetail(ctx, task.ID)
	}
	return s.setTaskStatus(ctx, in.RootID, in.TaskID, StatusCancelled, "cancelled", in.Reason, true)
}

// Reopen：把终态任务（完成 / 失败 / 取消）放回「待审核」，让这条对话继续。
//
// 终态是封板，封错了得有出路：用户点「完成」点早了、任务被一次偶发失败带进
// 已结束、取消之后又改主意，以前只能删卡重建（2026-11-19 用户要求补这个按钮）。
//
// 只改任务行：上一轮的 Run、回复与结果摘要是证据，原样保留。下一轮留言由
// PrepareNewUserTurn 按「终态 Run 不算取代」的语义新开一条，不会改写旧结论。
// 非终态任务是幂等的（返回当前详情、不写事件、不广播）。
func (s *Service) Reopen(ctx context.Context, in MoveInput) (TaskDetail, error) {
	store, task, err := s.loadForMove(ctx, in.RootID, in.TaskID)
	if err != nil {
		return TaskDetail{}, err
	}
	if !isTerminalStatus(task.Status) {
		return store.GetDetail(ctx, task.ID)
	}
	previous := task.Status
	if err := store.ReopenTask(ctx, task.ID); err != nil {
		return TaskDetail{}, err
	}
	_ = store.AddEvent(ctx, TaskEvent{
		ID:        newID("event"),
		TaskID:    task.ID,
		Type:      "reopened",
		Payload:   eventPayload(map[string]any{"from": previous, "reason": strings.TrimSpace(in.Reason)}),
		CreatedAt: time.Now().UTC(),
	})
	detail, err := store.GetDetail(ctx, task.ID)
	if err == nil && s.Runner != nil {
		s.Runner.TaskUpdated(in.RootID, detail)
	}
	return detail, err
}

// Complete：任何非终态任务都可一键完成。
// 不看会话/worktree 是否还在——会话被删、worktree 丢了，任务状态照样能人工收尾，
// 否则这些任务会永远卡在 running 没法推进。
func (s *Service) Complete(ctx context.Context, in MoveInput) (TaskDetail, error) {
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
		// D7: Complete is a human assertion, not a node result. It freezes open
		// work like cancel but persists success and records run_completed.
		return s.finishWorkflowRunByOperator(ctx, in.RootID, task.ID, StatusSuccess, "completed", WorkflowEventRunCompleted, in.Reason)
	}
	return s.setTaskStatus(ctx, in.RootID, in.TaskID, StatusSuccess, "completed", in.Reason, true)
}

func (s *Service) Status(ctx context.Context, rootID, taskID string) (TaskDetail, error) {
	return s.GetTask(ctx, rootID, taskID)
}

// DeleteTask 删掉一张任务卡 —— 只有卡片，worktree / 分支 / 会话一律不碰。
//
// 为什么要有它：看板上的卡只会累积。终态任务留着是为了看历史，看完得能清掉，
// 而「取消」只把状态改成 cancelled，卡片照旧留在板上。
//
// **只在任务没在跑时允许**：agent 还活着时删卡，随后的写入会落空（UPDATE 打到
// 不存在的行上），或者把卡又插回来 —— 与「删掉」相反。判据复用收尾那条
// assertNotRunning（会话在不在回复是金标准，见 worktree_finish.go）。
func (s *Service) DeleteTask(ctx context.Context, in MoveInput) error {
	store, task, err := s.loadForMove(ctx, in.RootID, in.TaskID)
	if err != nil {
		return err
	}
	if err := s.assertNotRunning(ctx, store, task); err != nil {
		return err
	}
	// 先关进程内 lane 再删库：卡片消失之后不能还有回合在跑，也不能留一堆
	// 排队回合去拿一张已不存在的任务调模型。lane 是每任务一条，关它不影响
	// 同 root 的其他任务。
	s.CloseRouterTask(ctx, in.RootID, task.ID)
	return store.DeleteTask(ctx, task.ID)
}
