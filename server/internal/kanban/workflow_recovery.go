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
	// 本轮真正还会动的 Run：被重排/续合并（wake）或有 open 行的。它们的回复
	// 活动属于正在继续的一轮，历史回填绝不能结算。
	liveRuns := map[string]bool{}
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
		// 取消类补口（W1 T4）：整轮取消写到一半 → 按账本重放收尾；只取消了一个
		// 节点 → 唤醒 driver 让下游继续。见 workflow_recovery_router_cancel.go。
		if !wake && !paused && len(open) == 0 {
			cancelWake, err := s.reconcileRouterCancelledRun(ctx, store, b.taskID, b.runID, b.status)
			if err != nil {
				log.Printf("[kanban/recover] router cancel reconcile failed root=%s task=%s err=%v", rootID, b.taskID, err)
			} else if cancelWake {
				wake = true
			}
		}
		if len(open) > 0 || wake {
			liveRuns[b.runID] = true
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
			if err := s.emitStartupReconcileRouterEvent(ctx, store, b.runID,
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
	// 历史回填（用户裁定 2026-11-20）：上面那圈只看得到 queued/running 行，还要求
	// 任务未终态。#86 那种「已经停下、任务后来还收尾/归档了」的 Run 永远扫不到，
	// 投影停在 0 个节点、回复一直转圈。按 unknown 行反查所有 Run，补缺失的投影键
	// 并结算陈旧的回复活动；正在继续的那一轮（liveRuns）不碰。
	unknownRunIDs, err := store.ListWorkflowRunIDsWithUnknownNodes(ctx)
	if err != nil {
		return plan, err
	}
	for _, runID := range unknownRunIDs {
		if liveRuns[runID] {
			continue
		}
		if err := s.backfillWorkflowRunStopped(ctx, store, runID); err != nil {
			log.Printf("[kanban/recover] projection backfill failed root=%s run=%s err=%v", rootID, runID, err)
		}
	}
	return plan, nil
}

// backfillWorkflowRunStopped 给「已经停下、但投影没写完」的历史 Run 补投影。
//
// 与 projectWorkflowRunStopped 的分工：那个只在本次启动刚把在飞行标成 unknown
// 时跑；这里按 unknown 行反查所有历史 Run（含任务早已收尾/归档的），只补**缺失**
// 的键，绝不覆盖已有终态证据：
//   - 最新尝试行是终态 / waiting_user → 用行状态（unknown 仍是 unknown，
//     真在等答复的节点也保持 waiting_user，见「操作员完成时不冻结」）；
//   - 行还停在 queued/running（这一轮已经没人推进）→ cancelled；
//   - 从没跑过的节点 → cancelled（这一轮放弃了它们）；
//   - 已有投影的节点 → 原样保留。
//
// 投影补完再结算还挂在 running/pending 的回复活动：那是 #86 里转了十几个小时的
// 那一行。幂等 —— 投影齐了就不再写事件、活动已终态就不再改。
func (s *Service) backfillWorkflowRunStopped(ctx context.Context, store *TaskStore, runID string) error {
	for attempt := 0; attempt < 3; attempt++ {
		run, err := store.WorkflowRunByID(ctx, runID)
		if err != nil {
			return err
		}
		// 历史阶段任务迁移出来的 Run 不在这里补：它们的收尾走迁移语义。
		if len(run.Graph.Nodes) == 0 || run.TriggeredBy == WorkflowTriggeredByLegacyImport {
			return nil
		}
		nodeRuns, err := store.ListWorkflowNodeRuns(ctx, runID)
		if err != nil {
			return err
		}
		latest := workflowLatestTopLevelNodeRuns(nodeRuns)
		if run.NodeStates == nil {
			run.NodeStates = map[string]string{}
		}
		unknown := []string{}
		filled := []string{}
		frozen := []string{}
		for _, node := range run.Graph.Nodes {
			if row, ok := latest[node.ID]; ok && row.Status == NodeRunUnknown {
				unknown = append(unknown, node.ID)
			}
			if _, ok := run.NodeStates[node.ID]; ok {
				continue
			}
			status := NodeRunCancelled
			if row, ok := latest[node.ID]; ok && (workflowNodeStateSettled(row.Status) || row.Status == NodeRunWaitingUser) {
				status = row.Status
			}
			run.NodeStates[node.ID] = status
			if status == NodeRunCancelled {
				frozen = append(frozen, node.ID)
			} else {
				filled = append(filled, node.ID)
			}
		}
		if len(filled)+len(frozen) > 0 {
			err := store.SaveWorkflowRunWithEvent(ctx, run, WorkflowRunEvent{
				Type: WorkflowEventNodeUnknown,
				Payload: map[string]any{
					"reason":          "startup_projection_backfill",
					"unknown_nodes":   unknown,
					"filled_nodes":    filled,
					"cancelled_nodes": frozen,
				},
			})
			if errors.Is(err, ErrRunConflict) {
				continue
			}
			if err != nil {
				return err
			}
		}
		s.settleWorkflowReplyUnknown(ctx, store, runID)
		return nil
	}
	return ErrRunConflict
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
	summary := restartInterruptedReplySummary
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
