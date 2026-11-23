package kanban

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"strings"
)

// 任务头状态与「真的有人在跑」对齐。
//
// 历史上任务状态只由两条路径写：直连回合的 beginTurnTaskStatus（开工 running、
// 收尾还原）与 workflow driver 的 projectWorkflowTaskStatus（按 Run 投影）。凡是
// 绕过这两条入口的回合 —— 用户直接给会话发消息、回答提问卡、节点被 claim 后在
// 自己会话里干活 —— 任务头都不会跟着变成运行中，反而会停在上一轮留下的
// waiting_user，界面看起来「待处理」而会话明明在跑。
//
// 这里给出唯一判定 taskRuntimeLive 与两个动作：开工抬到 running（只从
// waiting_user/pending 抬，不碰 paused/终态/归档），收尾按同一判定回落。

// TaskHasRunningWorkflowNode 报告任务当前 Run 是否有节点行真的在跑。
//
// 不看 NodeStates：投影只写终态（见 router_brief_open_nodes.go），运行中的节点
// 在投影里仍是 pending/空，用它判「有没有在跑」恒为假。行状态是执行器 claim 时
// 落的，才是真相。
func (s *TaskStore) TaskHasRunningWorkflowNode(ctx context.Context, taskID string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `
SELECT 1 FROM workflow_node_runs n
JOIN workflow_runs r ON r.id = n.run_id
WHERE r.task_id = ? AND n.status = ?
LIMIT 1`,
		strings.TrimSpace(taskID), NodeRunRunning).Scan(&one)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// taskRuntimeLive 是「任务此刻是否有人在跑」的唯一判定：任一绑定会话正在回话，
// 或当前 Run 有节点行在跑。excludeSessionKey 用于回合收尾：结算中的那条会话已经
// 不再算「还在跑」，否则它自己会把自己永远挡在 running。
func (s *Service) taskRuntimeLive(ctx context.Context, store *TaskStore, taskID, excludeSessionKey string) bool {
	if s == nil || store == nil || strings.TrimSpace(taskID) == "" {
		return false
	}
	keys, err := store.TaskRuntimeSessionKeys(ctx, taskID)
	if err != nil {
		// 读不到绑定关系时按「在跑」处理：宁可多留一个 running，也不要把真正
		// 在干活的会话误标成待处理（与 stale OOB 恢复的 fail-closed 同一口径）。
		log.Printf("[kanban/runtime] session keys task=%s err=%v", taskID, err)
		return true
	}
	exclude := strings.TrimSpace(excludeSessionKey)
	for _, key := range keys {
		if exclude != "" && key == exclude {
			continue
		}
		if s.sessionRunning(key) {
			return true
		}
	}
	running, err := store.TaskHasRunningWorkflowNode(ctx, taskID)
	if err != nil {
		log.Printf("[kanban/runtime] running nodes task=%s err=%v", taskID, err)
		return true
	}
	return running
}

// markTaskRuntimeRunning 把「有人开始干活」抬到 running。只从等待类状态抬，
// paused 是操作员的显式决定、终态是结论、归档卡在界面上不该再亮灯，三者都不碰。
func (s *Service) markTaskRuntimeRunning(ctx context.Context, store *TaskStore, taskID string) {
	if s == nil || store == nil {
		return
	}
	task, err := store.GetTask(ctx, strings.TrimSpace(taskID))
	if err != nil {
		log.Printf("[kanban/runtime] load task %s: %v", taskID, err)
		return
	}
	if task.ArchivedAt != "" {
		return
	}
	switch task.Status {
	case StatusWaitingUser, StatusPending:
	default:
		return
	}
	if err := store.UpdateTaskStatus(ctx, task.ID, StatusRunning, false); err != nil {
		log.Printf("[kanban/runtime] mark running task=%s: %v", task.ID, err)
		return
	}
	s.broadcastTask(ctx, store, task.ID)
}

// reconcileTaskRuntimeStatus 在回合收尾时把 running 放回 waiting_user —— 只有确实
// 没人跑了才落。工作流 Run 还有未终态节点时状态归 driver 投影，这里不抢先改。
func (s *Service) reconcileTaskRuntimeStatus(ctx context.Context, store *TaskStore, taskID, excludeSessionKey string) {
	if s == nil || store == nil {
		return
	}
	task, err := store.GetTask(ctx, strings.TrimSpace(taskID))
	if err != nil || task.Status != StatusRunning {
		return
	}
	open, err := taskHasOpenNodes(ctx, store, task.ID)
	if err != nil {
		log.Printf("[kanban/runtime] open nodes task=%s: %v", task.ID, err)
		return
	}
	if open {
		return
	}
	if s.taskRuntimeLive(ctx, store, task.ID, excludeSessionKey) {
		return
	}
	if err := store.UpdateTaskStatus(ctx, task.ID, StatusWaitingUser, false); err != nil {
		log.Printf("[kanban/runtime] reconcile task=%s: %v", task.ID, err)
		return
	}
	s.broadcastTask(ctx, store, task.ID)
}
