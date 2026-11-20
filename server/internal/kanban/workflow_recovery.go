package kanban

import (
	"context"
	"errors"
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
		 JOIN workflow_runs r ON r.id=e.workflow_run_id
		 WHERE e.engine=? AND e.workflow_run_id<>'' AND r.triggered_by<>? ORDER BY e.task_id`,
		EngineWorkflow, WorkflowTriggeredByLegacyImport)
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
		resumedBefore, unknownBefore := len(plan.ResumeNodeRunIDs), len(plan.UnknownIDs)
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
		if len(open) > 0 {
			// 这一轮已经彻底停下（没有节点被重排）：补齐半写的投影并结算回复活动，
			// 否则时间线永远 0 个节点、回复永远转圈（#86）。
			if len(plan.UnknownIDs) > unknownBefore && !wake && !paused {
				if err := s.projectWorkflowRunStopped(ctx, store, b.runID); err != nil {
					return plan, err
				}
			}
			if len(plan.UnknownIDs) > unknownBefore && !wake {
				s.settleWorkflowReplyUnknown(ctx, store, b.runID)
			}
			// 启动对账也是一次权威状态落库：把「这个 Run 重启后被怎么处理」记成
			// 一条 startup_reconcile，供 Router 在下一个回合看到。InstanceKey 是
			// RunID，重复启动不会重复产事件。
			if err := s.emitRouterEvent(ctx, store, b.runID, RouterEventStartupReconcile, "", "", "", "", 0,
				map[string]any{
					"open_node_runs": len(open),
					"resumed":        len(plan.ResumeNodeRunIDs) - resumedBefore,
					"unknown":        len(plan.UnknownIDs) - unknownBefore,
				}); err != nil {
				log.Printf("[kanban/recover] router event failed root=%s run=%s err=%v", rootID, b.runID, err)
			}
		}
		// 升级前留下的末端 human_gate 停在 waiting_user，没有 queued/running 行，
		// 上面的对账看不到它。必须唤醒 driver，让兼容收尾把它放行；否则任务会
		// 卡在一张永远没人需要批的审批卡上。
		if !wake && !paused {
			if run, err := store.WorkflowRunByID(ctx, b.runID); err == nil && hasParkedTerminalHumanGate(run) {
				wake = true
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

// projectWorkflowRunStopped 把「重启后彻底停下的一轮」补成完整投影。
//
// 投影只在节点终态时写，重启把 running 行标成 unknown 时投影还是空的（#86：
// 时间线 0 个节点），而下游从没 claim 过的节点会被当成「还会动」，Router 于是
// 永远被挡在「有活动执行」外面。unknown 行已经不在跑、也没人会再推进这一轮，
// 所以：
//   - unknown 行 → 投影 unknown（保留证据，不删 NodeOutputs）；
//   - 其余还没收尾的节点 → cancelled（这一轮放弃了它们；与取代时的冻结一致，
//     但 unknown 保持 unknown，不覆盖「副作用不明」这个事实）。
func (s *Service) projectWorkflowRunStopped(ctx context.Context, store *TaskStore, runID string) error {
	for attempt := 0; attempt < 5; attempt++ {
		run, err := store.WorkflowRunByID(ctx, runID)
		if err != nil {
			return err
		}
		if run.NodeStates == nil {
			run.NodeStates = map[string]string{}
		}
		nodeRuns, err := store.ListWorkflowNodeRuns(ctx, runID)
		if err != nil {
			return err
		}
		unknown := make([]string, 0, len(nodeRuns))
		for _, nr := range nodeRuns {
			if nr.Status != NodeRunUnknown {
				continue
			}
			run.NodeStates[nr.NodeID] = NodeRunUnknown
			unknown = append(unknown, nr.NodeID)
		}
		if len(unknown) == 0 {
			return nil
		}
		frozen := make([]string, 0, len(run.Graph.Nodes))
		for _, node := range run.Graph.Nodes {
			if workflowNodeStateSettled(run.NodeStates[node.ID]) {
				continue
			}
			run.NodeStates[node.ID] = NodeRunCancelled
			frozen = append(frozen, node.ID)
		}
		err = store.SaveWorkflowRunWithEvent(ctx, run, WorkflowRunEvent{
			Type: WorkflowEventNodeUnknown,
			Payload: map[string]any{
				"reason":          "startup_ambiguous_side_effect",
				"unknown_nodes":   unknown,
				"cancelled_nodes": frozen,
			},
		})
		if errors.Is(err, ErrRunConflict) {
			continue
		}
		return err
	}
	return ErrRunConflict
}

// settleWorkflowReplyUnknown 结算挂在已停下那一轮上的回复活动。
//
// 回合的收尾写在 agent 那一侧，进程重启把它带走了：活动停在 running，前端一直
// 转圈（#86 实测 `act_bde1960c77dbe52e` 从 18:29 转到次日）。结果不明不等于成功，
// 所以结算成 cancelled 并留一句可读说明，让用户知道可以继续发消息重开一轮。
func (s *Service) settleWorkflowReplyUnknown(ctx context.Context, store *TaskStore, runID string) {
	activities := NewActivityStore(store.db)
	activity, err := activities.ActivityByRunID(ctx, runID)
	if err != nil || activity == nil {
		return
	}
	if activity.Status != ActivityStatusRunning && activity.Status != ActivityStatusPending {
		return
	}
	summary := "进程重启时这一轮还在执行，结果无法确定；可以直接发消息让我重开一轮。"
	if err := activities.UpdateStatus(ctx, activity.ID, ActivityStatusCancelled, &summary); err != nil {
		return
	}
	activity.Status = ActivityStatusCancelled
	activity.Summary = summary
	s.broadcastActivityUpdated(activity, map[string]any{"status": ActivityStatusCancelled, "summary": summary})
	// 进度正文（"步骤 i/n"）属于运行中提示，这一轮已经结束，留着会被当成终态文案。
	_ = activities.ClearBody(ctx, activity.ID)
	activity.Body = ""
}
