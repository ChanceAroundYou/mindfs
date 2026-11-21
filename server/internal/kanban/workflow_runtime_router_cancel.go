package kanban

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Router 的取消三档（2026-11-21 用户裁定「解禁 cancel」）。最小够用原则：
// 能停一个节点就不停整轮，能停一轮就不取消任务。三者都保留现场 —— worktree、
// 会话、节点尝试记录与输出一律不删。

// CancelWorkflowNodeForRouter 取消单个节点。节点行落 cancelled 后唤醒 driver，
// 让下游按跳过语义收敛；任务状态不因此进入终态。
func (s *Service) CancelWorkflowNodeForRouter(ctx context.Context, rootID, taskID, nodeRunID, reason string) error {
	store, task, err := s.loadForMove(ctx, rootID, taskID)
	if err != nil {
		return err
	}
	if isTerminalStatus(task.Status) {
		return fmt.Errorf("%w: task is %s", ErrWorkflowNodeRunNotActionable, task.Status)
	}
	nr, err := store.WorkflowNodeRunByID(ctx, nodeRunID)
	if err != nil {
		return fmt.Errorf("load node run %s: %w", nodeRunID, err)
	}
	run, found, err := s.workflowRunForTask(ctx, store, task.ID)
	if err != nil {
		return err
	}
	if !found || run.ID != nr.RunID {
		return fmt.Errorf("%w: node run %s does not belong to the task's current run", ErrWorkflowNodeRunNotActionable, nodeRunID)
	}
	switch nr.Status {
	case NodeRunSuccess, NodeRunFail, NodeRunCancelled, NodeRunSkipped:
		// 已经收敛的行：取消是幂等的，不重写尝试记录。
		return nil
	case NodeRunWaitingUser:
		// 用户在等这条节点给答复/审批：不能替用户取消。
		return fmt.Errorf("%w: node %s is waiting for the user", ErrWorkflowNodeRunNotActionable, nr.NodeID)
	}
	s.interruptWorkflowNodeRuns(ctx, rootID, []NodeRun{nr})
	// 与合并收尾共用锁：agent 已返回、正在 merge 的行不能被改成 cancelled。
	unlock := s.lockWorkflowNodeFinish(nr.ID)
	changed, cerr := store.CancelWorkflowNodeRun(ctx, nr, reason)
	unlock()
	if cerr != nil {
		return cerr
	}
	if changed {
		if err := store.AddEvent(ctx, TaskEvent{
			ID: newID("event"), TaskID: task.ID, Type: "node_cancelled_by_router",
			Payload: eventPayload(map[string]any{
				"node_id": nr.NodeID, "node_run_id": nr.ID, "reason": strings.TrimSpace(reason),
			}),
			CreatedAt: time.Now().UTC(),
		}); err != nil {
			return err
		}
		// driver 可能正空闲等下一拍；唤醒后它按新投影决定下游 skip/join。
		if err := s.WakeWorkflowRun(ctx, rootID, task.ID, "router_cancel_node"); err != nil {
			return err
		}
	}
	s.broadcastTask(ctx, store, task.ID)
	return nil
}

// CancelWorkflowRunForRouter 停掉这一轮 Run，但把任务放回 waiting_user 交给用户；
// 取消任务本身是另一档（CancelTaskForRouter）。
func (s *Service) CancelWorkflowRunForRouter(ctx context.Context, rootID, taskID, reason string) error {
	store, task, err := s.loadForMove(ctx, rootID, taskID)
	if err != nil {
		return err
	}
	if isTerminalStatus(task.Status) {
		return fmt.Errorf("%w: task is %s", ErrWorkflowNodeRunNotActionable, task.Status)
	}
	run, found, err := s.workflowRunForTask(ctx, store, task.ID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: task has no workflow run", ErrWorkflowNodeRunNotActionable)
	}
	if err := s.cancelOpenWorkflowNodeRuns(ctx, rootID, store, run.ID, reason); err != nil {
		return err
	}
	s.stopWorkflowDriver(rootID, task.ID)
	// 取消 driver 之后再扫一次：窗口内刚提交的 claim 也必须停在 open 时被取消。
	if err := s.cancelOpenWorkflowNodeRuns(ctx, rootID, store, run.ID, reason); err != nil {
		return err
	}
	if err := s.projectRunCancelled(ctx, store, run.ID, reason); err != nil {
		return err
	}
	if _, err := s.setTaskStatus(ctx, rootID, task.ID, StatusWaitingUser, "run_cancelled_by_router", reason, false); err != nil {
		return err
	}
	// 投影写回之后再读一次，settleWorkflowReply 才会看到「全部终态」。
	if fresh, ok, err := s.workflowRunForTask(ctx, store, task.ID); err == nil && ok {
		run = fresh
	}
	s.settleWorkflowReply(ctx, store, run)
	_ = store.BlockWorkflowCommentQueue(ctx, task.ID)
	s.broadcastTask(ctx, store, task.ID)
	return nil
}

// CancelTaskForRouter 把任务收成 cancelled（终态），沿用人工取消的同一条路径。
func (s *Service) CancelTaskForRouter(ctx context.Context, rootID, taskID, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return errors.New("cancel_task reason required")
	}
	// Service.Cancel 对终态任务是幂等的（返回详情、不报错）；监管这一侧要明确
	// 拒绝 —— 已收尾的任务没有可取消的东西，静默成功会让 Router 误以为收了尾。
	_, task, err := s.loadForMove(ctx, rootID, taskID)
	if err != nil {
		return err
	}
	if isTerminalStatus(task.Status) {
		return fmt.Errorf("%w: task is %s", ErrWorkflowNodeRunNotActionable, task.Status)
	}
	_, err = s.Cancel(ctx, MoveInput{RootID: rootID, TaskID: taskID, Reason: reason})
	return err
}
