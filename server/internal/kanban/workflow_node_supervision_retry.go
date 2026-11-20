package kanban

import (
	"context"
	"strings"
)

// RetryFailedWorkflowNode 是主动监管的重试端口（ActivityNodeRetrier 的实现）。
// 它只做两件事：幂等检查 + 复用显式重试的既有语义（attempt+1、链接原行、写
// node_retried 账本、重置投影、唤醒 driver）。
//
// 幂等检查是必须的：CreateWorkflowNodeAttempt 内部用 Claim 让「同一 attempt」
// 只建一次行，但账本事件与唤醒是无条件追加的，重放会产生第二条 node_retried。
// 这里以「已经存在 PrevAttemptID = 原行」的下一尝试为判据直接返回。
func (s *Service) RetryFailedWorkflowNode(ctx context.Context, rootID, taskID, nodeRunID, reason string) (NodeRun, error) {
	store, _, err := s.loadForMove(ctx, rootID, taskID)
	if err != nil {
		return NodeRun{}, err
	}
	nodeRunID = strings.TrimSpace(nodeRunID)
	if existing, ok, err := supervisionNextAttempt(ctx, store, taskID, nodeRunID); err != nil {
		return NodeRun{}, err
	} else if ok {
		return existing, nil
	}
	if strings.TrimSpace(reason) == "" {
		reason = supervisionRetryReason
	}
	return s.CreateWorkflowNodeAttempt(ctx, rootID, taskID, nodeRunID, reason)
}

func supervisionNextAttempt(ctx context.Context, store *TaskStore, taskID, nodeRunID string) (NodeRun, bool, error) {
	if nodeRunID == "" {
		return NodeRun{}, false, nil
	}
	run, err := store.WorkflowRunForTask(ctx, taskID)
	if err != nil || run == nil {
		return NodeRun{}, false, err
	}
	runs, err := store.ListWorkflowNodeRuns(ctx, run.ID)
	if err != nil {
		return NodeRun{}, false, err
	}
	for _, nr := range runs {
		if nr.PrevAttemptID == nodeRunID {
			return nr, true, nil
		}
	}
	return NodeRun{}, false, nil
}
