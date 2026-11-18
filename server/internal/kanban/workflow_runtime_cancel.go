package kanban

import (
	"context"
	"strings"
	"time"
)

// 取消/中断的分步收尾（Plan 2026-11-18 集成拆分：原在 workflow_runtime.go，
// 文件超过 500 行硬线后按职责拆出；仅移动代码，语义不变）。

// cancelOpenWorkflowNodeRuns snapshots open rows, asks the adapter to stop
// their live turns, then marks them cancelled while they are still open. Doing
// the marking before the driver context is cancelled is deliberate: a finishing
// callback must lose the CAS and preserve the operator's terminal state.
func (s *Service) cancelOpenWorkflowNodeRuns(ctx context.Context, rootID string, store *TaskStore, runID, reason string) error {
	open, err := store.ListOpenWorkflowNodeRuns(ctx, runID)
	if err != nil {
		return err
	}
	s.interruptWorkflowNodeRuns(ctx, rootID, open)
	for _, nr := range open {
		// 与合并收尾共用锁：agent 已返回、正在 merge 的行不能被这里改成
		// cancelled；等收尾 CAS 成功后，这次取消自然 CAS 失败（changed=false）。
		unlock := s.lockWorkflowNodeFinish(nr.ID)
		_, cerr := store.CancelWorkflowNodeRun(ctx, nr, reason)
		unlock()
		if cerr != nil {
			return cerr
		}
	}
	return nil
}

// interruptWorkflowNodeRuns asks the adapter to stop the live turn behind each
// running row. It is bounded per row: an SDK that never acknowledges must not
// hold an operator verb hostage. Terminal state is persisted by the caller
// afterwards, so a late adapter reply cannot rewrite it.
func (s *Service) interruptWorkflowNodeRuns(ctx context.Context, rootID string, open []NodeRun) {
	canceller := s.workflowCancellerFor(rootID)
	timeout := s.workflowCancelTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	for _, nr := range open {
		if nr.Status != NodeRunRunning || strings.TrimSpace(nr.SessionKey) == "" || canceller == nil {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, timeout)
		_ = canceller.CancelWorkflowNode(cctx, nr)
		cancel()
	}
}

// stopWorkflowDriver cancels the single-flight driver so it cannot claim the
// next node. The driver itself only exits after its current step returns; the
// caller has already made the observable rows terminal, so that step's late
// write is a CAS no-op.
func (s *Service) stopWorkflowDriver(rootID, taskID string) {
	s.mu.Lock()
	if d := s.workflowDrives[workflowDriveKey(rootID, taskID)]; d != nil {
		d.pending = false
		d.cancel()
	}
	s.mu.Unlock()
}
