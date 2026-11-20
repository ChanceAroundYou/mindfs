package kanban

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

// WorkflowNodeCanceller stops the Agent turn behind one running NodeRun. It is
// separate from the Runner so a test/legacy wiring can omit it: cancellation
// still persists the terminal state and never waits unbounded for the adapter.
type WorkflowNodeCanceller interface {
	CancelWorkflowNode(ctx context.Context, nodeRun NodeRun) error
}

// workflowDrive is the per-task single-flight token. A driver is one goroutine
// that repeatedly reloads the authoritative run; duplicate triggers set pending
// instead of starting a second concurrent turn.
type workflowDrive struct {
	pending bool
	cancel  context.CancelFunc
	done    chan struct{}
}

func (s *Service) SetWorkflowNodeCanceller(c WorkflowNodeCanceller) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.WorkflowNodeCancellerPort = c
	s.mu.Unlock()
}

// SetWorkflowNodeCancellerForRoot binds a root-aware adapter. A NodeRun has no
// root column, and cancelling a session needs the root's session manager; the
// per-root map keeps that attribution without adding a persisted field.
func (s *Service) SetWorkflowNodeCancellerForRoot(rootID string, c WorkflowNodeCanceller) {
	if s == nil || strings.TrimSpace(rootID) == "" {
		return
	}
	s.mu.Lock()
	if s.workflowCancellers == nil {
		s.workflowCancellers = map[string]WorkflowNodeCanceller{}
	}
	s.workflowCancellers[strings.TrimSpace(rootID)] = c
	s.mu.Unlock()
}

func (s *Service) workflowCancellerFor(rootID string) WorkflowNodeCanceller {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if c := s.workflowCancellers[strings.TrimSpace(rootID)]; c != nil {
		return c
	}
	return s.WorkflowNodeCancellerPort
}

func workflowDriveKey(rootID, taskID string) string {
	return strings.TrimSpace(rootID) + "\x00" + strings.TrimSpace(taskID)
}

// WakeWorkflowRun is the Router's write-side entry: every approved trigger
// (comment, migration, RunNow, resume, startup reconciliation, agent report)
// funnels here. Exactly one driver runs per task; a duplicate wake marks the
// current driver to re-read state once more instead of racing a second turn.
func (s *Service) WakeWorkflowRun(ctx context.Context, rootID, taskID, cause string) error {
	if s == nil {
		return errors.New("kanban service not configured")
	}
	rootID = strings.TrimSpace(rootID)
	taskID = strings.TrimSpace(taskID)
	if rootID == "" || taskID == "" {
		return errors.New("root id and task id required")
	}
	key := workflowDriveKey(rootID, taskID)
	s.mu.Lock()
	if s.workflowDrives == nil {
		s.workflowDrives = map[string]*workflowDrive{}
	}
	if d := s.workflowDrives[key]; d != nil {
		d.pending = true
		s.mu.Unlock()
		return nil
	}
	// The driver outlives the inbound request: an HTTP handler returning or a
	// websocket read loop moving on must not cancel an in-flight workflow step.
	driveCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	d := &workflowDrive{cancel: cancel, done: make(chan struct{})}
	s.workflowDrives[key] = d
	s.mu.Unlock()
	go s.driveWorkflowRun(driveCtx, key, rootID, taskID, cause)
	return nil
}

// WaitWorkflowRunIdle waits until no driver is running for the task. It exists
// for deterministic tests and operator flows that need to observe a quiescent
// run; it never starts work itself.
func (s *Service) WaitWorkflowRunIdle(ctx context.Context, rootID, taskID string) error {
	key := workflowDriveKey(rootID, taskID)
	for {
		s.mu.Lock()
		d := s.workflowDrives[key]
		s.mu.Unlock()
		if d == nil {
			return nil
		}
		select {
		case <-d.done:
			// A new trigger may have installed a new driver right after this one
			// closed; loop until the registry is genuinely empty.
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *Service) driveWorkflowRun(ctx context.Context, key, rootID, taskID, cause string) {
	for {
		if ctx.Err() == nil {
			store, err := s.taskStore(rootID)
			if err != nil {
				log.Printf("[kanban/workflow] drive.store root=%s task=%s cause=%s err=%v", rootID, taskID, cause, err)
			} else if err := s.AdvanceWorkflowRun(ctx, store, taskID); err != nil && ctx.Err() == nil {
				log.Printf("[kanban/workflow] drive.advance root=%s task=%s cause=%s err=%v", rootID, taskID, cause, err)
			}
		}
		s.mu.Lock()
		d := s.workflowDrives[key]
		if ctx.Err() == nil && d != nil && d.pending {
			d.pending = false
			s.mu.Unlock()
			continue
		}
		if d != nil {
			delete(s.workflowDrives, key)
			close(d.done)
		}
		s.mu.Unlock()
		return
	}
}

// PauseWorkflowRun stops new claims but deliberately lets the in-flight node
// reach its terminal state. It never cancels the driver context: killing a turn
// here would turn a pause into a lost attempt.
func (s *Service) PauseWorkflowRun(ctx context.Context, rootID, taskID, reason string) error {
	store, task, err := s.loadForMove(ctx, rootID, taskID)
	if err != nil {
		return err
	}
	if isTerminalStatus(task.Status) {
		return nil
	}
	if _, err := s.setTaskStatus(ctx, rootID, task.ID, StatusPaused, "paused", reason, false); err != nil {
		return err
	}
	if run, found, rerr := s.workflowRunForTask(ctx, store, task.ID); rerr == nil && found {
		s.settleWorkflowReply(ctx, store, run)
	}
	s.mu.Lock()
	if d := s.workflowDrives[workflowDriveKey(rootID, task.ID)]; d != nil {
		d.pending = false
	}
	s.mu.Unlock()
	s.appendRunStateEvent(ctx, store, task.ID, WorkflowEventRunPaused, reason)
	return nil
}

// ResumeWorkflowRun requires a non-terminal task, marks it running and wakes
// the same run. The driver reloads the run and continues from persisted state.
func (s *Service) ResumeWorkflowRun(ctx context.Context, rootID, taskID, reason string) error {
	store, task, err := s.loadForMove(ctx, rootID, taskID)
	if err != nil {
		return err
	}
	if isTerminalStatus(task.Status) {
		return fmt.Errorf("%w: task is terminal (%s)", ErrWorkflowNodeRunNotActionable, task.Status)
	}
	if _, err := s.setTaskStatus(ctx, rootID, task.ID, StatusRunning, "resumed", reason, false); err != nil {
		return err
	}
	s.resumeWorkflowReply(ctx, store, task.ID)
	s.appendRunStateEvent(ctx, store, task.ID, WorkflowEventRunResumed, reason)
	return s.WakeWorkflowRun(ctx, rootID, task.ID, "resume")
}

// CancelWorkflowRun is the operator stop. Ordering matters:
//  1. ask the adapter to cancel each in-flight turn (bounded);
//  2. mark open rows cancelled while they are still observable as open, so the
//     finishing callback cannot rewrite a cancelled row into a different result;
//  3. cancel the driver context so no further node is claimed;
//  4. project the run + task, block unconsumed comments, and leave the worktree
//     alone (lifecycle cleanup is a separate decision).
func (s *Service) CancelWorkflowRun(ctx context.Context, rootID, taskID, reason string) error {
	store, task, err := s.loadForMove(ctx, rootID, taskID)
	if err != nil {
		return err
	}
	if isTerminalStatus(task.Status) {
		return nil
	}
	run, found, err := s.workflowRunForTask(ctx, store, task.ID)
	if err != nil {
		return err
	}
	if !found {
		// The binding says legacy (or is inconsistent); still honour the stop.
		if err := store.UpdateTaskStatus(ctx, task.ID, StatusCancelled, true); err != nil {
			return err
		}
		_ = store.BlockWorkflowCommentQueue(ctx, task.ID)
		s.broadcastTask(ctx, store, task.ID)
		return nil
	}
	if err := s.cancelOpenWorkflowNodeRuns(ctx, rootID, store, run.ID, reason); err != nil {
		return err
	}
	s.stopWorkflowDriver(rootID, task.ID)
	// Re-scan after cancelling the driver: a step that committed its claim in
	// the window between the first list and ctx cancellation is not in the
	// first snapshot. It is now observable and must be cancelled while open,
	// otherwise a late callback could rewrite it after the operator stopped.
	if err := s.cancelOpenWorkflowNodeRuns(ctx, rootID, store, run.ID, reason); err != nil {
		return err
	}
	if err := s.projectRunCancelled(ctx, store, run.ID, reason); err != nil {
		return err
	}
	if err := store.UpdateTaskStatus(ctx, task.ID, StatusCancelled, true); err != nil {
		return err
	}
	// projectRunCancelled 在另一次加载里写回新的 NodeStates，这里的 run 仍是
	// 取消前的快照。settleWorkflowReply 用 run 判断「节点是否全部终态」，过期
	// 快照会让它以为还有 running 节点而直接返回，绑定回复就永远停在 running。
	// 投影与任务状态落库之后重读一次再结算。
	if fresh, ok, err := s.workflowRunForTask(ctx, store, task.ID); err == nil && ok {
		run = fresh
	}
	s.settleWorkflowReply(ctx, store, run)
	_ = store.AddEvent(ctx, TaskEvent{
		ID: newID("event"), TaskID: task.ID, Type: "cancelled",
		Payload: eventPayload(map[string]any{"reason": strings.TrimSpace(reason)}), CreatedAt: time.Now().UTC(),
	})
	if err := store.BlockWorkflowCommentQueue(ctx, task.ID); err != nil {
		return err
	}
	s.broadcastTask(ctx, store, task.ID)
	return nil
}

// finishWorkflowRunByOperator implements D7's per-verb mapping for Complete and
// Fail. Both are human assertions about the task, not agent results, so they
// stop open Agent turns and freeze the run the same way cancel does, while the
// persisted terminal outcome is success/fail rather than cancelled. The run's
// historical NodeRuns are never rewritten, and the worktree is left alone.
func (s *Service) finishWorkflowRunByOperator(ctx context.Context, rootID, taskID, status, taskEvent, runEvent, reason string) (TaskDetail, error) {
	store, task, err := s.loadForMove(ctx, rootID, taskID)
	if err != nil {
		return TaskDetail{}, err
	}
	if isTerminalStatus(task.Status) {
		return store.GetDetail(ctx, task.ID)
	}
	run, found, err := s.workflowRunForTask(ctx, store, task.ID)
	if err != nil {
		return TaskDetail{}, err
	}
	if !found {
		return TaskDetail{}, fmt.Errorf("%w: task has no workflow run", ErrWorkflowNodeRunNotActionable)
	}
	if err := s.cancelOpenWorkflowNodeRuns(ctx, rootID, store, run.ID, reason); err != nil {
		return TaskDetail{}, err
	}
	s.stopWorkflowDriver(rootID, task.ID)
	// Same post-stop re-scan as CancelWorkflowRun: a claim that committed in the
	// cancellation window must not survive the operator's terminal outcome.
	if err := s.cancelOpenWorkflowNodeRuns(ctx, rootID, store, run.ID, reason); err != nil {
		return TaskDetail{}, err
	}
	if _, err := store.AppendWorkflowRunEvent(ctx, WorkflowRunEvent{
		RunID: run.ID, Type: runEvent,
		Payload: map[string]any{"reason": strings.TrimSpace(reason)},
	}); err != nil {
		return TaskDetail{}, err
	}
	if err := store.BlockWorkflowCommentQueue(ctx, task.ID); err != nil {
		return TaskDetail{}, err
	}
	detail, err := s.setTaskStatus(ctx, rootID, task.ID, status, taskEvent, reason, true)
	if err != nil {
		return TaskDetail{}, err
	}
	// 操作员终态不重写 Run 投影，事件按操作员给出的任务终态产生；事件写失败
	// 不回滚已经落库的决定，下一次扫描会补齐。
	s.emitOperatorOutcomeRouterEvent(ctx, store, run.ID, status)
	return detail, nil
}

// projectRunCancelled rewrites the run projection for rows that cancellation
// actually moved. A CAS conflict means a concurrent driver step won; reload and
// reapply rather than silently leaving the projection disagreeing with the rows.
func (s *Service) projectRunCancelled(ctx context.Context, store *TaskStore, runID, reason string) error {
	for attempt := 0; attempt < 5; attempt++ {
		run, err := store.WorkflowRunByID(ctx, runID)
		if err != nil {
			return err
		}
		rows, err := store.ListWorkflowNodeRuns(ctx, run.ID)
		if err != nil {
			return err
		}
		if run.NodeStates == nil {
			run.NodeStates = map[string]string{}
		}
		for _, nr := range rows {
			if nr.LoopPath != "" || nr.Status != NodeRunCancelled {
				continue
			}
			run.NodeStates[nr.NodeID] = NodeRunCancelled
		}
		err = store.SaveWorkflowRunWithEvent(ctx, run, WorkflowRunEvent{
			Type: WorkflowEventRunCancelled, Payload: map[string]any{"reason": strings.TrimSpace(reason)},
		})
		if errors.Is(err, ErrRunConflict) {
			continue
		}
		return err
	}
	return ErrRunConflict
}

// CreateWorkflowNodeAttempt is the explicit retry verb (D9: only with evidence).
// It creates attempt+1 linked to the failed original and never mutates the
// original row, its output, or a comment's frozen binding.
func (s *Service) CreateWorkflowNodeAttempt(ctx context.Context, rootID, taskID, nodeRunID, reason string) (NodeRun, error) {
	return s.createWorkflowNodeAttempt(ctx, rootID, taskID, nodeRunID, reason, "")
}

// createWorkflowNodeAttempt 是显式重试的唯一实现；notBefore 非空时新行一建好就带
// 配额挂起，driver 醒来也只会跳过它，等 coordinate 到点唤醒。
func (s *Service) createWorkflowNodeAttempt(ctx context.Context, rootID, taskID, nodeRunID, reason, notBefore string) (NodeRun, error) {
	store, task, err := s.loadForMove(ctx, rootID, taskID)
	if err != nil {
		return NodeRun{}, err
	}
	// 显式重试是用户对失败/未知证据的处理动作，可以重开一个已进入 fail/cancelled
	// 的终态任务；可重试性由原 NodeRun 的状态决定，而不是任务当前的终态。
	run, found, err := s.workflowRunForTask(ctx, store, task.ID)
	if err != nil {
		return NodeRun{}, err
	}
	if !found {
		return NodeRun{}, fmt.Errorf("%w: task has no workflow run", ErrWorkflowNodeRunNotActionable)
	}
	original, err := store.WorkflowNodeRunByID(ctx, strings.TrimSpace(nodeRunID))
	if err != nil {
		return NodeRun{}, err
	}
	if original.RunID != run.ID || original.LoopPath != "" {
		return NodeRun{}, fmt.Errorf("%w: only a top-level node of this run can be retried", ErrWorkflowNodeRunNotActionable)
	}
	switch original.Status {
	case NodeRunFail, NodeRunUnknown, NodeRunWaitingUser:
	default:
		return NodeRun{}, fmt.Errorf("%w: node run %s is %s", ErrWorkflowNodeRunNotActionable, original.ID, original.Status)
	}
	open, err := store.ListOpenWorkflowNodeRuns(ctx, run.ID)
	if err != nil {
		return NodeRun{}, err
	}
	for _, nr := range open {
		if nr.NodeID == original.NodeID && nr.LoopPath == original.LoopPath {
			return NodeRun{}, fmt.Errorf("%w: node %s already has an open attempt", ErrWorkflowNodeRunNotActionable, original.NodeID)
		}
	}
	retried, err := store.CreateRetryWorkflowNodeRun(ctx, run, original)
	if err != nil {
		return NodeRun{}, err
	}
	if notBefore != "" {
		if err := store.SetWorkflowNodeRunNotBefore(ctx, retried.ID, notBefore); err != nil {
			return NodeRun{}, err
		}
		retried.NotBefore = notBefore
	}
	if _, err := store.AppendWorkflowRunEvent(ctx, WorkflowRunEvent{
		RunID: run.ID, Type: WorkflowEventNodeRetried, NodeID: original.NodeID, NodeRunID: retried.ID,
		Attempt: retried.Attempt, Payload: map[string]any{"prev_attempt_id": original.ID, "reason": strings.TrimSpace(reason)},
	}); err != nil {
		return NodeRun{}, err
	}
	if err := s.resetWorkflowNodeProjection(ctx, store, run.ID, original.NodeID); err != nil {
		return NodeRun{}, err
	}
	if err := store.UpdateTaskStatus(ctx, task.ID, StatusRunning, false); err != nil {
		return NodeRun{}, err
	}
	_ = store.AddEvent(ctx, TaskEvent{
		ID: newID("event"), TaskID: task.ID, Type: "workflow_node_retried",
		Payload:   eventPayload(map[string]any{"node_id": original.NodeID, "node_run_id": retried.ID, "prev_attempt_id": original.ID, "reason": strings.TrimSpace(reason)}),
		CreatedAt: time.Now().UTC(),
	})
	if err := s.WakeWorkflowRun(ctx, rootID, task.ID, "retry"); err != nil {
		return NodeRun{}, err
	}
	return retried, nil
}

func (s *Service) resetWorkflowNodeProjection(ctx context.Context, store *TaskStore, runID, nodeID string) error {
	for attempt := 0; attempt < 5; attempt++ {
		run, err := store.WorkflowRunByID(ctx, runID)
		if err != nil {
			return err
		}
		if run.NodeStates == nil {
			run.NodeStates = map[string]string{}
		}
		run.NodeStates[nodeID] = NodeRunPending
		delete(run.NodeOutputs, nodeID)
		err = store.SaveWorkflowRun(ctx, run)
		if errors.Is(err, ErrRunConflict) {
			continue
		}
		return err
	}
	return ErrRunConflict
}

func (s *Service) workflowRunForTask(ctx context.Context, store *TaskStore, taskID string) (*WorkflowRun, bool, error) {
	engine, err := taskEngineOf(ctx, store.db, taskID)
	if err != nil {
		return nil, false, err
	}
	if engine.Engine != EngineWorkflow {
		return nil, false, nil
	}
	if strings.TrimSpace(engine.WorkflowRunID) == "" {
		run, err := store.WorkflowRunForTask(ctx, taskID)
		if errors.Is(err, ErrWorkflowRunNotFound) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		return run, true, nil
	}
	run, err := store.WorkflowRunByID(ctx, engine.WorkflowRunID)
	if err != nil {
		return nil, false, err
	}
	return run, true, nil
}

func (s *Service) appendRunStateEvent(ctx context.Context, store *TaskStore, taskID, eventType, reason string) {
	if store == nil {
		return
	}
	run, found, err := s.workflowRunForTask(ctx, store, taskID)
	if err != nil || !found || run == nil {
		return
	}
	_, _ = store.AppendWorkflowRunEvent(ctx, WorkflowRunEvent{
		RunID: run.ID, Type: eventType, Payload: map[string]any{"reason": strings.TrimSpace(reason)},
	})
}
