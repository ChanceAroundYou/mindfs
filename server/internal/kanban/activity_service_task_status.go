package kanban

import "context"

// beginTurnTaskStatus 把「这一轮已经开始」投影到任务头，并返回直连回合的恢复
// 函数。workflow 回合不需要恢复：终态由 projectWorkflowTaskStatus 按节点投影。
func (s *ActivityService) beginTurnTaskStatus(ctx context.Context, taskID string) func() {
	if s == nil || s.tasks == nil {
		return func() {}
	}
	task, err := s.tasks.GetTask(ctx, taskID)
	if err != nil {
		return func() {}
	}
	previous := task.Status
	// 暂停与终态是操作者的状态，不能被一个回合同步复活；直连回合只服务
	// 仍在推进（pending / waiting_user / running）的任务。
	if previous == StatusPaused || isTerminalStatus(previous) {
		return func() {}
	}
	if previous != StatusRunning {
		if err := s.tasks.UpdateTaskStatus(ctx, taskID, StatusRunning, false); err != nil {
			return func() {}
		}
	}
	return func() {
		// 本来就在 running 的任务不归这一轮回滚，避免直连回合把别人的执行
		// 状态写成 pending。
		if previous == StatusRunning {
			return
		}
		current, err := s.tasks.GetTask(ctx, taskID)
		if err != nil || current.Status != StatusRunning {
			return
		}
		next := previous
		if next == "" || next == StatusPaused || isTerminalStatus(next) {
			next = StatusPending
		}
		_ = s.tasks.UpdateTaskStatus(ctx, taskID, next, false)
	}
}

// markTurnTaskRunning 把任务推入 running，供不需要恢复的 workflow 回合使用。
func (s *ActivityService) markTurnTaskRunning(ctx context.Context, taskID string) {
	if s == nil || s.tasks == nil {
		return
	}
	task, err := s.tasks.GetTask(ctx, taskID)
	if err != nil || task.Status == StatusRunning || task.Status == StatusPaused || isTerminalStatus(task.Status) {
		return
	}
	_ = s.tasks.UpdateTaskStatus(ctx, taskID, StatusRunning, false)
}
