package kanban

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// RunWorkflowNodeAttempt executes one claimed Agent NodeRun end to end:
// claim → running(+ledger) → session bind(+ledger) → external turn → terminal
// (+ledger) → task projection + correlated activity.
//
// The runner is never called before the claim/start transaction commits, and a
// lost claim returns the persisted row without dispatching a second turn.
func (s *Service) RunWorkflowNodeAttempt(ctx context.Context, store *TaskStore, run *WorkflowRun, node WorkflowNode, nodeRun NodeRun) (NodeRun, error) {
	if s == nil || store == nil || run == nil {
		return NodeRun{}, errors.New("workflow service, store and run required")
	}
	if node.Kind != NodeKindAgent {
		return NodeRun{}, fmt.Errorf("node %q is %s, not agent", node.ID, node.Kind)
	}
	if nodeRun.ID == "" || nodeRun.RunID != run.ID || nodeRun.NodeID != node.ID {
		return NodeRun{}, fmt.Errorf("node run %q does not belong to run/node %s/%s", nodeRun.ID, run.ID, node.ID)
	}
	if nodeRun.Attempt < 1 {
		return NodeRun{}, errors.New("workflow node run attempt must be >= 1")
	}
	profile := WorkflowNodeAgentProfileOf(node)
	nodeRun.Agent = profile.Agent
	nodeRun.Harness = profile.Harness
	nodeRun.Model = profile.Model
	nodeRun.Status = NodeRunQueued
	if nodeRun.Input == nil {
		nodeRun.Input = map[string]any{"prompt": node.Params["prompt"]}
	}

	claimed, won, err := store.ClaimWorkflowNodeRun(ctx, nodeRun)
	if err != nil {
		return NodeRun{}, err
	}
	if !won {
		// A persisted queued row is not "already executed": it is the restart or
		// retry path. Only queued rows may be begun; running/terminal rows are
		// somebody else's living attempt (or history) and are never re-dispatched.
		if claimed.Status != NodeRunQueued || !claimed.StartedAt.IsZero() {
			return claimed, nil
		}
	}
	nodeRun = claimed
	started := time.Now().UTC()
	if err := store.BeginWorkflowNodeRun(ctx, nodeRun, started); err != nil {
		if errors.Is(err, ErrWorkflowNodeRunNotQueued) {
			// Lost the queued→running race to a cancel/finish. Report the row's
			// current truth without starting an external turn.
			if current, rerr := store.WorkflowNodeRunByID(ctx, nodeRun.ID); rerr == nil {
				return current, nil
			}
			return nodeRun, nil
		}
		return nodeRun, err
	}
	nodeRun.Status = NodeRunRunning
	nodeRun.StartedAt = started

	// 先确定物理 workspace 再起外部回合：没有可得目录/写权限时绝不调用 Agent。
	// 独立树在这一步创建并由 NodeRun 持久认领；Task 2 阶段所有终态都保留树，
	// merge/close 由 Task 3 接管。
	lease, err := s.PrepareWorkflowNodeWorktree(ctx, store, run, node, nodeRun)
	if err != nil {
		status, eventType := NodeRunFail, WorkflowEventNodeFailed
		if errors.Is(err, ErrWorktreeOwnerConflict) || errors.Is(err, ErrWorktreeNeedsUser) {
			// owner/branch 冲突需要人处理，不是执行失败。
			status, eventType = NodeRunWaitingUser, WorkflowEventNodeBlocked
		}
		finished, ferr := s.finishWorkflowNodeAttempt(ctx, store, run, node, nodeRun, status, eventType,
			map[string]any{"error": err.Error()}, err.Error())
		if ferr != nil {
			return finished, ferr
		}
		return finished, err
	}
	// The lease is the physical writer gate for this node's workspace. It must
	// be released before ANY terminal path: finalize projects the result and may
	// deliver a queued comment into that same workspace, which re-acquires the
	// same non-reentrant gate. Holding the lease through finalize deadlocks the
	// driver and leaks the gate. Release is idempotent, so the defer stays as a
	// panic/early-return backstop.
	defer lease.Release()
	nodeRun = lease.NodeRun

	exec, err := s.prepareWorkflowNodeAt(ctx, store, run, node, nodeRun, lease.RootPath)
	if err != nil {
		lease.Release()
		return s.failWorkflowNodeAttempt(ctx, store, run, node, nodeRun, err, map[string]any{"error": err.Error()})
	}
	if key := strings.TrimSpace(exec.NodeRun.SessionKey); key != "" {
		if err := store.BindWorkflowNodeSession(ctx, nodeRun, key); err != nil {
			lease.Release()
			return s.failWorkflowNodeAttempt(ctx, store, run, node, nodeRun, err, map[string]any{"error": err.Error()})
		}
		nodeRun.SessionKey = key
	}

	result, err := s.RunPreparedWorkflowNode(ctx, exec)
	// The external turn is over: the workspace is no longer being written by
	// this node. Everything below is persistence/projection/merge, and comment
	// delivery must be able to take the gate itself.
	lease.Release()
	if err != nil {
		// Transport/send failure: no proof the agent did or did not act. Fail the
		// attempt (explicit, visible) and leave retry to a new attempt.
		return s.failWorkflowNodeAttempt(ctx, store, run, node, nodeRun, err, map[string]any{"error": err.Error()})
	}
	return s.finalizeWorkflowNodeResult(ctx, store, run, node, nodeRun, result)
}

// failWorkflowNodeAttempt persists the failure and still returns the cause: the
// caller (driver/HTTP) must see the failed attempt, not a silent nil.
func (s *Service) failWorkflowNodeAttempt(ctx context.Context, store *TaskStore, run *WorkflowRun, node WorkflowNode, nodeRun NodeRun, cause error, output map[string]any) (NodeRun, error) {
	finished, err := s.finishWorkflowNodeAttempt(ctx, store, run, node, nodeRun, NodeRunFail,
		WorkflowEventNodeFailed, output, cause.Error())
	if err != nil {
		return finished, err
	}
	return finished, cause
}

// workflowWorktreeOutput 把合并事实投影到 NodeRun output。没有独立树（零值）
// 时不写任何键，保持共享主树节点的输出形状不变。
func workflowWorktreeOutput(res WorkflowNodeWorktreeResult) map[string]any {
	if !res.Merged && res.Commit == "" && len(res.ConflictFiles) == 0 && res.CloseError == "" {
		return nil
	}
	out := map[string]any{"merged": res.Merged}
	if res.Commit != "" {
		out["commit"] = res.Commit
	}
	if len(res.ConflictFiles) > 0 {
		out["conflict_files"] = res.ConflictFiles
	}
	if res.CloseError != "" {
		out["close_error"] = res.CloseError
	}
	return out
}

// workflowDriverStopped 报告错误是否只是执行上下文被取消（操作员停止、
// 暂停/停机）。它不是节点执行结果，不能在已死上下文上写终态失败。
func workflowDriverStopped(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// workflowNeedsUserMergeError 区分「需要人处理」和「执行失败」：冲突与脏主树
// 都要求保留两边现场并等用户，不能笼统标成 fail。
func workflowNeedsUserMergeError(err error) bool {
	var conflict *MergeConflictError
	if errors.As(err, &conflict) {
		return true
	}
	var dirty *FinishWorktreeDirty
	if errors.As(err, &dirty) {
		return true
	}
	// 冲突已经解完、只差一次 commit 也是人要做的事；把它当执行失败会让任务
	// 直接进 fail 终态，而树里其实已经是可提交的成果。
	var userChanges *FinishWorktreeUserChanges
	if errors.As(err, &userChanges) {
		return true
	}
	return errors.Is(err, ErrWorktreeNeedsUser)
}

// finalizeWorkflowNodeWithWorktree 收尾一个已报告 done 的独立树节点。
//
// 锁的顺序就是正确性，但两把锁不能嵌套等待：
//  1. 收尾锁内重读 running 并写 agent-done 证据，然后**释放**收尾锁；
//  2. 在锁外等待主 checkout 的 writer gate（另一个写者可能在跑 agent 回合，
//     这里可能等很久，绝不能握着收尾锁等，否则操作员取消被无关写者阻塞）；
//  3. 拿到 gate 后重新取收尾锁、重读状态：取消/另一个收尾在这期间赢了就
//     直接返回，连 merge 都不发生；
//  4. 锁内完成 merge + 终态 CAS，然后释放两把锁再做投影/留言投递。
func (s *Service) finalizeWorkflowNodeWithWorktree(ctx context.Context, store *TaskStore, run *WorkflowRun, node WorkflowNode, nodeRun NodeRun, output map[string]any, reason string) (NodeRun, error) {
	unlock := s.lockWorkflowNodeFinish(nodeRun.ID)
	fresh, err := store.WorkflowNodeRunByID(ctx, nodeRun.ID)
	if err != nil {
		unlock()
		return nodeRun, err
	}
	if fresh.Status != NodeRunRunning {
		// 操作员取消/另一个收尾已经赢了：绝不在这里补 merge。
		unlock()
		return fresh, nil
	}
	nodeRun = fresh

	// 独立树节点在 merge 之前先把「agent 已返回」写成持久证据：进程可能
	// 正好死在 merge 前后，重启时靠它幂等续 merge 而不是把 agent 重跑一遍。
	// 重入的收尾不得重复写同一条证据（恢复正常路径也不依赖重复事件）。
	_, recorded, rerr := s.workflowNodeAgentDoneOutput(ctx, store, nodeRun)
	if rerr != nil {
		unlock()
		return nodeRun, rerr
	}
	if !recorded {
		if _, err := store.AppendWorkflowRunEvent(ctx, WorkflowRunEvent{
			RunID: run.ID, Type: WorkflowEventNodeAgentDone, NodeID: node.ID,
			NodeRunID: nodeRun.ID, Attempt: nodeRun.Attempt,
			Payload: map[string]any{"output": output, "reason": reason},
		}); err != nil {
			// 没写进证据就别继续 merge：宁可留 running 等恢复判 unknown，
			// 也不要制造一个重启后无法解释的合并。
			unlock()
			return nodeRun, err
		}
	}
	unlock()

	// 解析与等待 gate 都在收尾锁之外。独立的 NodeRun 工作树只有在节点真的
	// done 后才合并；失败/阻塞/unknown 保留现场（D7–D9）。合并目标是 Run
	// 创建时的快照，绝不由这里补默认值。
	plan, hasMerge, prepErr := s.prepareWorkflowNodeMerge(ctx, store, run, nodeRun)
	releaseGate := func() {}
	if prepErr == nil {
		releaseGate, prepErr = s.acquireWorkflowMergeGate(ctx, plan, hasMerge)
	}
	if prepErr != nil {
		releaseGate()
		if workflowDriverStopped(prepErr) {
			// 等待 gate 期间操作员停止了 driver（或进程在停机）。不能按执行
			// 失败落终态：取消可能已经赢了 CAS，而取消没赢时 agent-done 证据
			// 还在，重启恢复会续 merge，不会重跑 Agent。
			if current, rerr := store.WorkflowNodeRunByID(context.WithoutCancel(ctx), nodeRun.ID); rerr == nil {
				return current, prepErr
			}
			return nodeRun, prepErr
		}
		output["worktree_error"] = prepErr.Error()
		if workflowNeedsUserMergeError(prepErr) {
			return s.finishWorkflowNodeAttempt(ctx, store, run, node, nodeRun, NodeRunWaitingUser,
				WorkflowEventNodeBlocked, output, prepErr.Error())
		}
		return s.failWorkflowNodeAttempt(ctx, store, run, node, nodeRun, prepErr, output)
	}

	unlock = s.lockWorkflowNodeFinish(nodeRun.ID)
	fresh, err = store.WorkflowNodeRunByID(ctx, nodeRun.ID)
	if err != nil {
		unlock()
		releaseGate()
		return nodeRun, err
	}
	if fresh.Status != NodeRunRunning {
		// Gate 等待期间取消赢了：释放 gate，绝不补 merge。
		unlock()
		releaseGate()
		return fresh, nil
	}
	nodeRun = fresh

	var merged WorkflowNodeWorktreeResult
	var mergeErr error
	if hasMerge {
		merged, mergeErr = s.executeWorkflowNodeMerge(ctx, plan)
	}
	if worktree := workflowWorktreeOutput(merged); worktree != nil {
		output["worktree"] = worktree
	}
	if mergeErr != nil {
		unlock()
		releaseGate()
		reason := mergeErr.Error()
		output["worktree_error"] = reason
		if workflowNeedsUserMergeError(mergeErr) {
			// 冲突 / 脏主 checkout 都不是「节点执行失败」：两边现场都保留，
			// 需要人处理后再收敛。
			return s.finishWorkflowNodeAttempt(ctx, store, run, node, nodeRun, NodeRunWaitingUser,
				WorkflowEventNodeBlocked, output, reason)
		}
		return s.failWorkflowNodeAttempt(ctx, store, run, node, nodeRun, mergeErr, output)
	}
	finished, err := s.persistWorkflowNodeTerminal(ctx, store, nodeRun, NodeRunSuccess,
		WorkflowEventNodeSucceeded, output, reason)
	unlock()
	releaseGate()
	if err != nil {
		return finished, err
	}
	return s.finalizeWorkflowNodeTerminal(ctx, store, run, node, finished, NodeRunSuccess,
		WorkflowEventNodeSucceeded, output, reason)
}

func (s *Service) finalizeWorkflowNodeResult(ctx context.Context, store *TaskStore, run *WorkflowRun, node WorkflowNode, nodeRun NodeRun, result WorkflowNodeResult) (NodeRun, error) {
	output := result.Output
	if output == nil {
		output = map[string]any{}
	}
	output["outcome"] = string(result.Outcome)
	if reason := strings.TrimSpace(result.Reason); reason != "" {
		output["reason"] = reason
	}
	switch result.Outcome {
	case WorkflowNodeDone:
		if strings.TrimSpace(nodeRun.WorktreeID) != "" {
			return s.finalizeWorkflowNodeWithWorktree(ctx, store, run, node, nodeRun, output, result.Reason)
		}
		return s.finishWorkflowNodeAttempt(ctx, store, run, node, nodeRun, NodeRunSuccess,
			WorkflowEventNodeSucceeded, output, result.Reason)
	case WorkflowNodeBlocked:
		return s.finishWorkflowNodeAttempt(ctx, store, run, node, nodeRun, NodeRunWaitingUser,
			WorkflowEventNodeBlocked, output, result.Reason)
	case WorkflowNodeSilent:
		return s.finishWorkflowNodeAttempt(ctx, store, run, node, nodeRun, NodeRunWaitingUser,
			WorkflowEventNodeSilent, output, result.Reason)
	default:
		return s.finishWorkflowNodeAttempt(ctx, store, run, node, nodeRun, NodeRunUnknown,
			WorkflowEventNodeUnknown, output, result.Reason)
	}
}

func (s *Service) finishWorkflowNodeAttempt(ctx context.Context, store *TaskStore, run *WorkflowRun, node WorkflowNode, nodeRun NodeRun, status, eventType string, output map[string]any, reason string) (NodeRun, error) {
	finished, err := s.persistWorkflowNodeTerminal(ctx, store, nodeRun, status, eventType, output, reason)
	if err != nil {
		return finished, err
	}
	return s.finalizeWorkflowNodeTerminal(ctx, store, run, node, finished, status, eventType, output, reason)
}

// persistWorkflowNodeTerminal 只做「running → 终态」的 CAS + 账本事件。
// 与后续投影/活动/留言投递拆开，是为了让合并收尾能在锁内完成 CAS、锁外做
// 可能很慢的续聊投递（取消不必等一条 agent 回合）。
func (s *Service) persistWorkflowNodeTerminal(ctx context.Context, store *TaskStore, nodeRun NodeRun, status, eventType string, output map[string]any, reason string) (NodeRun, error) {
	finished := time.Now().UTC()
	payload := map[string]any{"status": status}
	if reason != "" {
		payload["reason"] = reason
	}
	if err := store.FinishWorkflowNodeRun(ctx, nodeRun, status, output, finished, eventType, payload); err != nil {
		return nodeRun, err
	}
	nodeRun.Status = status
	nodeRun.Output = output
	nodeRun.FinishedAt = finished
	return nodeRun, nil
}

func (s *Service) finalizeWorkflowNodeTerminal(ctx context.Context, store *TaskStore, run *WorkflowRun, node WorkflowNode, nodeRun NodeRun, status, eventType string, output map[string]any, reason string) (NodeRun, error) {
	// 每一步都尽量执行：行已经是终态，若中途出错就返回，留言会永远留在
	// queued（该 NodeRun 之后不会再有收尾回调）。先把能做的做完，再报第一个错误。
	var firstErr error
	record := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	// Inner loop nodes are projected per iteration by the loop driver, never
	// onto the run-level NodeStates/NodeOutputs. Persisting the outer projection
	// here would collide across iterations and shadow a same-id top-level node.
	if nodeRun.LoopPath == "" {
		record(s.saveWorkflowNodeProjection(ctx, store, run, node.ID, status, output))
	}

	activityType, author, role, body := workflowNodeActivity(status, reason, output)
	_, activityErr := store.AppendTaskActivity(ctx, TaskActivity{
		ID:        "wf:" + nodeRun.ID + ":" + eventType,
		TaskID:    run.TaskID,
		Type:      activityType,
		Author:    author,
		Role:      role,
		Body:      body,
		RunID:     run.ID,
		NodeRunID: nodeRun.ID,
	})
	record(activityErr)
	// D3：节点报告完成后，先把绑定到它的排队留言按 FIFO 续聊到同一 session，
	// 再让 driver 评估出边。失败/阻塞的节点不转发留言，直接进入 blocked。
	//
	// 顺序很关键：必须排在任务终态投影**之前**。单节点图完成时，投影会把任务
	// 直接写成 success；先投影再投递，reconcile 就会看到终态任务并把这条属于
	// 已完成节点的留言 block 掉，违反「同一 NodeRun 结束后 FIFO 发回同一 session」。
	record(s.reconcileWorkflowCommentsAfterNodeRun(ctx, store, run, nodeRun, status))
	if nodeRun.LoopPath == "" {
		// projectWorkflowTaskStatus refuses to overwrite pause/cancel; the
		// finishing callback must obey the same rule as the driver.
		record(s.projectWorkflowTaskStatus(ctx, store, run))
	}
	// 运行态变更必须同时到达看板（投影 + 广播），不能只落库。
	if detail, err := store.GetDetail(ctx, run.TaskID); err == nil && s.Runner != nil {
		s.Runner.TaskUpdated(detail.Task.RootID, detail)
	}
	return nodeRun, firstErr
}

// saveWorkflowNodeProjection persists one node's terminal state onto the run
// projection. The NodeRun terminal transition and the run projection are
// separate transactions (the ledger finish must not be widened), so a
// concurrent graph edit/loop advance can win the revision CAS between them.
// Reloading and reapplying only this node's keys preserves the other writer's
// fields instead of failing the finished agent turn and leaving the driver to
// re-run a node that already executed.
func (s *Service) saveWorkflowNodeProjection(ctx context.Context, store *TaskStore, run *WorkflowRun, nodeID, status string, output map[string]any) error {
	const attempts = 5
	for i := 0; i < attempts; i++ {
		if run.NodeStates == nil {
			run.NodeStates = map[string]string{}
		}
		if run.NodeOutputs == nil {
			run.NodeOutputs = map[string]map[string]any{}
		}
		run.NodeStates[nodeID] = status
		run.NodeOutputs[nodeID] = output
		err := store.SaveWorkflowRun(ctx, run)
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrRunConflict) {
			return err
		}
		fresh, loadErr := store.WorkflowRunByID(ctx, run.ID)
		if loadErr != nil {
			return loadErr
		}
		*run = *fresh
	}
	return fmt.Errorf("%w: node %s projection lost %d revision races", ErrRunConflict, nodeID, attempts)
}

func workflowNodeActivity(status, reason string, output map[string]any) (activityType, author, role, body string) {
	reason = strings.TrimSpace(reason)
	switch status {
	case NodeRunSuccess:
		summary, _ := output["summary"].(string)
		summary = strings.TrimSpace(summary)
		if summary == "" {
			if len(output) > 0 {
				if raw, err := marshalWorkflowJSON(output); err == nil {
					summary = raw
				}
			}
		}
		if summary == "" {
			summary = "节点已完成"
		}
		return ActivityTypeAgentReply, RoleAgent, RoleAgent, summary
	case NodeRunWaitingUser:
		if reason == "" {
			reason = "节点未回报完成"
		}
		return ActivityTypeAttention, "system", "system", "等待你处理：" + reason
	case NodeRunUnknown:
		if reason == "" {
			reason = "执行状态无法确定"
		}
		return ActivityTypeAttention, "system", "system", "执行状态未知：" + reason
	default:
		if reason == "" {
			reason = "节点执行失败"
		}
		return ActivityTypeAttention, "system", "system", "执行失败：" + reason
	}
}
