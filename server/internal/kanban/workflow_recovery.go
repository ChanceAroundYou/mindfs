package kanban

import (
	"context"
	"fmt"
	"log"
	"strings"
)

// workflowAttemptForReadyNode picks the attempt row a ready Agent node should
// execute. Normal first runs have no row yet (attempt 1 is claimed here);
// recovery/retry rows are already persisted as queued and must be executed
// instead of pretending the node was never claimed.
func (s *Service) workflowAttemptForReadyNode(ctx context.Context, store *TaskStore, run *WorkflowRun, node WorkflowNode) (NodeRun, error) {
	runs, err := store.ListWorkflowNodeRuns(ctx, run.ID)
	if err != nil {
		return NodeRun{}, err
	}
	var latest NodeRun
	for _, nr := range runs {
		if nr.NodeID != node.ID || nr.LoopPath != "" {
			continue
		}
		if latest.ID == "" || nr.Attempt > latest.Attempt {
			latest = nr
		}
	}
	if latest.ID == "" {
		return workflowAttemptNodeRun(run, node), nil
	}
	if latest.Status == NodeRunQueued && latest.Input == nil {
		latest.Input = map[string]any{"prompt": node.Params["prompt"]}
	}
	return latest, nil
}

// workflowNodeAgentDoneOutput 读取 NodeRun 的持久 agent-done 证据。有这条事件
// 表示外部回合已经返回，重启时只允许续做 merge/收尾，绝不能再次调用 agent。
func (s *Service) workflowNodeAgentDoneOutput(ctx context.Context, store *TaskStore, nr NodeRun) (map[string]any, bool, error) {
	events, err := store.ListWorkflowRunEvents(ctx, nr.RunID)
	if err != nil {
		return nil, false, err
	}
	var payload map[string]any
	found := false
	for _, ev := range events {
		if ev.Type == WorkflowEventNodeAgentDone && ev.NodeRunID == nr.ID {
			payload = ev.Payload
			found = true
		}
	}
	if !found {
		return nil, false, nil
	}
	output, _ := payload["output"].(map[string]any)
	return output, true, nil
}

// resumeWorkflowNodeMergeAfterRestart 完成「agent 已返回、merge 尚未收尾」的
// NodeRun：merge 自身幂等（已合过不会产生第二个合并提交），冲突/脏主树转
// waiting_user，其它 merge 失败转 unknown 保留现场。此路径永远不调用 agent。
func (s *Service) resumeWorkflowNodeMergeAfterRestart(ctx context.Context, store *TaskStore, runID string, nr NodeRun, output map[string]any) (bool, error) {
	run, err := store.WorkflowRunByID(ctx, runID)
	if err != nil {
		return false, err
	}
	// loop 内部节点不在顶层 Graph.Nodes 里，必须用递归查找，否则重启恢复
	// 会在查不到节点时中断整个 root 的对账。
	node, ok := workflowNodeByID(run.Graph, nr.NodeID)
	if !ok {
		return false, fmt.Errorf("workflow run %s has no node %q for node run %s", runID, nr.NodeID, nr.ID)
	}
	if output == nil {
		output = map[string]any{}
	}
	output["resumed_after_restart"] = true

	// 与取消路径共用收尾锁，但**不能握着它等主 checkout gate**：另一个写者
	// 可能正在跑长回合，握着收尾锁等会让操作员取消无限期阻塞。先在锁外解析
	// 并取得 gate，再锁内重读状态；取消若已赢，直接放弃 merge。
	plan, hasMerge, prepErr := s.prepareWorkflowNodeMerge(ctx, store, run, nr)
	releaseGate := func() {}
	if prepErr == nil {
		releaseGate, prepErr = s.acquireWorkflowMergeGate(ctx, plan, hasMerge)
	}
	if prepErr != nil {
		releaseGate()
		if workflowDriverStopped(prepErr) {
			// 启动对账被取消（进程停机）：不写终态、不重排，让下一次启动
			// 继续依据 agent-done 证据续 merge。
			return true, prepErr
		}
		output["worktree_error"] = prepErr.Error()
		status, eventType := NodeRunUnknown, WorkflowEventNodeUnknown
		if workflowNeedsUserMergeError(prepErr) {
			status, eventType = NodeRunWaitingUser, WorkflowEventNodeBlocked
		}
		_, err := s.finishWorkflowNodeAttempt(ctx, store, run, node, nr, status, eventType, output, prepErr.Error())
		return true, err
	}

	unlock := s.lockWorkflowNodeFinish(nr.ID)
	fresh, err := store.WorkflowNodeRunByID(ctx, nr.ID)
	if err != nil {
		unlock()
		releaseGate()
		return false, err
	}
	if fresh.Status != NodeRunRunning {
		unlock()
		releaseGate()
		return true, nil
	}
	nr = fresh

	var res WorkflowNodeWorktreeResult
	var mergeErr error
	if hasMerge {
		res, mergeErr = s.executeWorkflowNodeMerge(ctx, plan)
	}
	if worktree := workflowWorktreeOutput(res); worktree != nil {
		output["worktree"] = worktree
	}
	if mergeErr != nil {
		unlock()
		releaseGate()
		output["worktree_error"] = mergeErr.Error()
		status, eventType := NodeRunUnknown, WorkflowEventNodeUnknown
		if workflowNeedsUserMergeError(mergeErr) {
			status, eventType = NodeRunWaitingUser, WorkflowEventNodeBlocked
		}
		_, err := s.finishWorkflowNodeAttempt(ctx, store, run, node, nr, status, eventType, output, mergeErr.Error())
		return true, err
	}
	finished, err := s.persistWorkflowNodeTerminal(ctx, store, nr, NodeRunSuccess,
		WorkflowEventNodeSucceeded, output, "restart: agent done, merge resumed")
	unlock()
	releaseGate()
	if err != nil {
		return true, err
	}
	_, err = s.finalizeWorkflowNodeTerminal(ctx, store, run, node, finished, NodeRunSuccess,
		WorkflowEventNodeSucceeded, output, "restart: agent done, merge resumed")
	return true, err
}

// recoverWorkflowRunsOnStartup applies D8/D9 to persisted NodeRuns:
//   - queued-not-sent is safe to resume;
//   - running without a bound session cannot have sent a turn yet → safe;
//   - running with a bound session resumes only when the adapter proves the
//     session is reachable and resumable; otherwise unknown/needs-user.
func (s *Service) recoverWorkflowRunsOnStartup(ctx context.Context, rootID string, store *TaskStore, probe SessionStateProbe) (RecoveryPlan, error) {
	plan := RecoveryPlan{}
	rows, err := store.db.QueryContext(ctx,
		`SELECT e.task_id, e.workflow_run_id, t.status FROM task_execution_engines e
		 JOIN tasks t ON t.id=e.task_id
		 WHERE e.engine=? AND e.workflow_run_id<>'' ORDER BY e.task_id`,
		EngineWorkflow)
	if err != nil {
		return plan, err
	}
	defer rows.Close()
	type bound struct{ taskID, runID, status string }
	var bounds []bound
	for rows.Next() {
		var b bound
		if err := rows.Scan(&b.taskID, &b.runID, &b.status); err != nil {
			return plan, err
		}
		bounds = append(bounds, b)
	}
	if err := rows.Err(); err != nil {
		return plan, err
	}
	for _, b := range bounds {
		if isTerminalStatus(b.status) {
			continue
		}
		// Paused tasks are reconciled too: a process restart can leave an open
		// row that pause's "let the in-flight node finish" contract cannot
		// complete. Requeue/unknown still applies, but we neither wake the
		// driver nor overwrite the operator's paused status.
		paused := b.status == StatusPaused
		open, err := store.ListOpenWorkflowNodeRuns(ctx, b.runID)
		if err != nil {
			return plan, err
		}
		wake := false
		for _, nr := range open {
			// agent-done 证据优先于所有「副作用不明」判断：回合已经返回，重启
			// 只许续 merge，不许把同一个回合再发一遍。
			if nr.Status == NodeRunRunning && strings.TrimSpace(nr.WorktreeID) != "" {
				output, recorded, err := s.workflowNodeAgentDoneOutput(ctx, store, nr)
				if err != nil {
					return plan, err
				}
				if recorded {
					resumed, err := s.resumeWorkflowNodeMergeAfterRestart(ctx, store, b.runID, nr, output)
					if err != nil {
						return plan, err
					}
					if resumed {
						plan.ResumeNodeRunIDs = append(plan.ResumeNodeRunIDs, nr.ID)
						// merge 收尾把 NodeRun 推进到终态，但启动恢复里没有正在
						// 自循环的 driver。不唤醒的话，下游节点会永远停在未开始。
						if !paused {
							wake = true
						}
						continue
					}
				}
			}
			reason := ""
			switch {
			case nr.Status == NodeRunQueued:
				reason = "startup_queued_not_sent"
			case nr.Status == NodeRunRunning && strings.TrimSpace(nr.SessionKey) == "":
				reason = "startup_started_without_session"
			case nr.Status == NodeRunRunning && probe != nil:
				if found, resumable := probe.Probe(nr.SessionKey); found && resumable {
					reason = "startup_session_resumable"
				}
			}
			if reason != "" {
				changed, err := store.RequeueWorkflowNodeRun(ctx, nr, reason)
				if err != nil {
					return plan, err
				}
				if changed {
					plan.ResumeNodeRunIDs = append(plan.ResumeNodeRunIDs, nr.ID)
					if nr.LoopPath == "" {
						if err := s.resetWorkflowNodeProjection(ctx, store, b.runID, nr.NodeID); err != nil {
							return plan, err
						}
					}
					wake = true
				}
				continue
			}
			// Running with a session the probe cannot vouch for (or no probe at
			// all): never replay the side effect. Keep the row as unknown evidence.
			changed, err := store.MarkWorkflowNodeRunUnknown(ctx, nr, "startup_ambiguous_side_effect")
			if err != nil {
				return plan, err
			}
			if changed {
				plan.UnknownIDs = append(plan.UnknownIDs, nr.ID)
				plan.NeedsUser = true
				if !paused {
					_ = store.UpdateTaskStatus(ctx, b.taskID, StatusWaitingUser, false)
				}
			}
		}
		if wake && !paused {
			if err := s.WakeWorkflowRun(ctx, rootID, b.taskID, "startup_reconcile"); err != nil {
				log.Printf("[kanban/recover] workflow wake failed root=%s task=%s err=%v", rootID, b.taskID, err)
			}
		}
	}
	return plan, nil
}
