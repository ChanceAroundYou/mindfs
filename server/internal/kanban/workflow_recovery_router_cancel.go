package kanban

import (
	"context"
	"errors"
	"log"
)

// W1 T4：取消类动作的启动补口。
//
// Router 的取消分两档，崩溃窗口不一样：
//
//   - cancel_node：行改 cancelled → 唤醒 driver 继续跑下游。进程死在两者之间时
//     这一轮并没有被放弃，重启要做的只是唤醒，绝不能把整轮冻成「已取消」。
//   - cancel_run / cancel_task：行 → run_cancelled 事件 → 投影冻结 → 结算回复 →
//     任务放回 waiting_user。进程死在中间任何一步都会留下「账本说取消了、界面
//     还在转」的半写状态，启动时按 run_cancelled 幂等重放剩下的收尾。
//
// 幂等来自两处：run_cancelled 只落一次（WorkflowRunHasEvent），重放后投影全部
// 收尾（workflowRunHasOpenNodes 为 false），第二次启动不写任何事件；任务状态
// 只在还停在 running 时才回落到 waiting_user。

// reconcileRouterCancelledRun 是启动对账的取消补口。返回 true 表示需要唤醒
// driver（只取消了一个节点的那种情况）。
func (s *Service) reconcileRouterCancelledRun(ctx context.Context, store *TaskStore, taskID, runID, taskStatus string) (bool, error) {
	runCancelled, err := store.WorkflowRunHasEvent(ctx, runID, WorkflowEventRunCancelled)
	if err != nil {
		return false, err
	}
	if !runCancelled {
		// 节点级取消的崩溃窗口：行已 cancelled、投影还没写。不补投影的话 driver
		// 醒来会把这个节点当成「没跑过」反复派发（空转到 1000 步上限），下游永远
		// 不动 —— 所以先补齐投影，再决定唤醒。
		repaired, err := s.repairCancelledNodeProjections(ctx, store, runID)
		if err != nil {
			return false, err
		}
		nodeCancelled, err := workflowRunHasCancelledNode(ctx, store, runID)
		if err != nil {
			return false, err
		}
		// 没有 run_cancelled 就不是「整轮不要了」：只有任务还在跑才需要唤醒，
		// 停在 waiting_user / paused 的任务等人或操作员，不能替他们继续。
		return (repaired || nodeCancelled) && taskStatus == StatusRunning, nil
	}
	run, err := store.WorkflowRunByID(ctx, runID)
	if err != nil {
		return false, err
	}
	nodeRuns, err := store.ListWorkflowNodeRuns(ctx, runID)
	if err != nil {
		return false, err
	}
	if workflowRunHasOpenNodes(run, nodeRuns) {
		if err := s.replayRouterRunCancelled(ctx, store, run); err != nil {
			// 投影被别的写者动过：下一次启动再来（重放本身幂等）。
			if errors.Is(err, ErrRunConflict) {
				return false, nil
			}
			return false, err
		}
		if err := s.emitRouterEvent(ctx, store, runID, RouterEventStartupReconcile, "", "", "", "", 0,
			map[string]any{"cancel_replayed": true}); err != nil {
			log.Printf("[kanban/recover] router event failed run=%s err=%v", runID, err)
		}
	}
	s.settleWorkflowReplyUnknown(ctx, store, runID)
	if taskStatus == StatusRunning {
		if err := store.UpdateTaskStatus(ctx, taskID, StatusWaitingUser, false); err != nil {
			return false, err
		}
	}
	return false, nil
}

// replayRouterRunCancelled 把这一轮里还没收尾的节点冻结成 cancelled 并补一条
// run_cancelled 事件（reason 标明是启动重放，不是第二次取消）。
func (s *Service) replayRouterRunCancelled(ctx context.Context, store *TaskStore, run *WorkflowRun) error {
	if run.NodeStates == nil {
		run.NodeStates = map[string]string{}
	}
	frozen := make([]string, 0, len(run.Graph.Nodes))
	for _, node := range run.Graph.Nodes {
		if workflowNodeStateSettled(run.NodeStates[node.ID]) {
			continue
		}
		run.NodeStates[node.ID] = NodeRunCancelled
		frozen = append(frozen, node.ID)
	}
	if len(frozen) == 0 {
		return nil
	}
	if err := store.SaveWorkflowRunWithEvent(ctx, run, WorkflowRunEvent{
		Type: WorkflowEventRunCancelled,
		Payload: map[string]any{
			"reason":          "startup_cancel_replay",
			"cancelled_nodes": frozen,
		},
	}); err != nil {
		return err
	}
	log.Printf("[kanban/recover] router cancel replayed run=%s nodes=%v", run.ID, frozen)
	return nil
}

// workflowRunHasCancelledNode 报告这一轮是否已经有节点被取消（节点级取消的
// 证据）。整轮取消另看 run_cancelled 账本，两者语义不同。
func workflowRunHasCancelledNode(ctx context.Context, store *TaskStore, runID string) (bool, error) {
	rows, err := store.ListWorkflowNodeRuns(ctx, runID)
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		if row.Status == NodeRunCancelled {
			return true, nil
		}
	}
	return false, nil
}

// repairCancelledNodeProjections 把「行已 cancelled、投影还没写」的节点补进这一轮
// 的投影（每节点只看最新一次尝试，重试成功的节点不会被旧取消覆盖）。返回是否
// 真的改了投影。
func (s *Service) repairCancelledNodeProjections(ctx context.Context, store *TaskStore, runID string) (bool, error) {
	errConflict := ErrRunConflict
	for attempt := 0; attempt < 5; attempt++ {
		run, err := store.WorkflowRunByID(ctx, runID)
		if err != nil {
			return false, err
		}
		rows, err := store.ListWorkflowNodeRuns(ctx, runID)
		if err != nil {
			return false, err
		}
		latest := map[string]NodeRun{}
		for _, row := range rows {
			if row.LoopPath != "" {
				continue
			}
			if prev, ok := latest[row.NodeID]; ok && prev.Attempt >= row.Attempt {
				continue
			}
			latest[row.NodeID] = row
		}
		if run.NodeStates == nil {
			run.NodeStates = map[string]string{}
		}
		changed := false
		for nodeID, row := range latest {
			if row.Status != NodeRunCancelled || run.NodeStates[nodeID] == NodeRunCancelled {
				continue
			}
			run.NodeStates[nodeID] = NodeRunCancelled
			changed = true
		}
		if !changed {
			return false, nil
		}
		if err := store.SaveWorkflowRun(ctx, run); err != nil {
			if errors.Is(err, errConflict) {
				continue
			}
			return false, err
		}
		log.Printf("[kanban/recover] cancelled node projection repaired run=%s", runID)
		return true, nil
	}
	return false, ErrRunConflict
}
