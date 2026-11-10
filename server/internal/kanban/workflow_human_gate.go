package kanban

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// workflowGateSyncWait bounds the approval request's wait for the driver. The
// decision is already durable before this wait; a downstream agent turn can
// take minutes, so an HTTP/WS caller must not stay blocked on it. Progress
// still reaches clients through the normal task broadcasts.
const workflowGateSyncWait = 5 * time.Second

// ResolveWorkflowHumanGate applies the explicit approve/reject decision (D4) and,
// on approval, immediately drives the downstream graph.
//
// A normal comment never reaches this method. Repeating the same decision is
// idempotent; the opposite decision is rejected by the store. Rejection ends the
// run as cancelled and blocks every undelivered comment instead of forwarding it
// to a later node.
func (s *Service) ResolveWorkflowHumanGate(ctx context.Context, rootID, taskID, nodeID, decision, commentID string) (NodeRun, error) {
	if s == nil {
		return NodeRun{}, errors.New("kanban service not configured")
	}
	store, err := s.taskStore(rootID)
	if err != nil {
		return NodeRun{}, err
	}
	taskID = strings.TrimSpace(taskID)
	nodeID = strings.TrimSpace(nodeID)
	run, err := store.WorkflowRunForTask(ctx, taskID)
	if err != nil {
		return NodeRun{}, err
	}
	node, ok := run.Graph.node(nodeID)
	if !ok {
		return NodeRun{}, fmt.Errorf("workflow node %q not found in run %s", nodeID, run.ID)
	}
	if node.Kind != NodeKindHumanGate {
		return NodeRun{}, fmt.Errorf("workflow node %q is %s, not human_gate", nodeID, node.Kind)
	}
	nodeRun, err := store.WorkflowNodeRunByKey(ctx, run.ID, nodeID, "", 0, 1)
	if err != nil {
		return NodeRun{}, err
	}
	if nodeRun.Status != NodeRunWaitingUser {
		// Let the store produce the precise idempotent/decided error.
		return s.decideExistingHumanGate(ctx, store, run, nodeRun, decision, commentID)
	}
	// An undecided gate on an already-finished task must not resurrect the run.
	// A decision that was recorded before completion still follows the
	// idempotent path above, so history remains readable.
	task, err := store.GetTask(ctx, taskID)
	if err != nil {
		return NodeRun{}, err
	}
	if isTerminalStatus(task.Status) {
		return NodeRun{}, fmt.Errorf("%w: task is terminal (%s)", ErrWorkflowNodeRunNotActionable, task.Status)
	}
	updated, changed, err := store.DecideWorkflowHumanGate(ctx, run, nodeRun, decision, commentID)
	if err != nil {
		return NodeRun{}, err
	}
	if !changed {
		return updated, nil
	}
	if updated.Status == NodeRunCancelled {
		if err := store.UpdateTaskStatus(ctx, run.TaskID, StatusCancelled, true); err != nil {
			return updated, err
		}
		if err := store.BlockWorkflowCommentQueue(ctx, run.TaskID); err != nil {
			return updated, err
		}
		s.broadcastTask(ctx, store, run.TaskID)
		return updated, nil
	}
	if err := s.WakeWorkflowRun(ctx, rootID, run.TaskID, "human_gate_approved"); err != nil {
		return updated, err
	}
	// approve 的调用方拿到的 NodeRun 必须已经反映下游推进结果（HTTP/WS 响应
	// 与旧同步语义一致）。实际推进仍由 per-task driver 单飞执行。
	//
	// 但等待必须有界：审批本身已落库并唤醒 driver，下游若是一个几分钟的 Agent
	// 回合，请求不能替它等完。超时只表示响应里暂时看不到下游结果，不是失败。
	waitCtx, cancel := context.WithTimeout(ctx, workflowGateSyncWait)
	defer cancel()
	if err := s.WaitWorkflowRunIdle(waitCtx, rootID, run.TaskID); err != nil {
		if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			return updated, err
		}
	}
	return updated, nil
}

// decideExistingHumanGate keeps duplicate-call semantics in one place: a repeat
// of the already-recorded decision is a no-op, anything else is an error.
func (s *Service) decideExistingHumanGate(ctx context.Context, store *TaskStore, run *WorkflowRun, nodeRun NodeRun, decision, commentID string) (NodeRun, error) {
	desired := NodeRunSuccess
	if strings.TrimSpace(decision) == "reject" {
		desired = NodeRunCancelled
	}
	if nodeRun.Status == desired {
		return nodeRun, nil
	}
	if nodeRun.Status == NodeRunSuccess || nodeRun.Status == NodeRunCancelled {
		return NodeRun{}, ErrWorkflowHumanGateAlreadyDecided
	}
	return NodeRun{}, ErrWorkflowHumanGateNotWaiting
}

func (s *Service) broadcastTask(ctx context.Context, store *TaskStore, taskID string) {
	if s.Runner == nil {
		return
	}
	if detail, err := store.GetDetail(ctx, taskID); err == nil {
		s.Runner.TaskUpdated(detail.Task.RootID, detail)
	}
}
