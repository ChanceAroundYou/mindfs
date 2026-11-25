package kanban

import (
	"context"
	"log"
	"strings"
	"time"
)

// 自动收尾：任务进入终态（或被归档）后，把还留着的工作树收掉。
//
// 分两档是为了不白烧一个模型回合：树已经干净且分支已并入主干时直接清场；有活没提交
// 时才起收尾回合让 agent 按显式路径 commit + merge。commit 永远不单独做成按钮 ——
// 「该提交哪些文件、哪些是半成品」只有 agents 自己知道，这个判断留在回合提示词里。

// SetAutoFinishEnabled 打开/关闭自动收尾。零值 = 关（单测与未装配的调用方不产生副作用），
// 生产装配按 config.json 的 worktree.auto_finish 注入（缺省 true）。
func (s *Service) SetAutoFinishEnabled(enabled bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.autoFinishWorktree = enabled
	s.mu.Unlock()
}

func (s *Service) autoFinishEnabled() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.autoFinishWorktree
}

type autoFinishContextKey struct{}

func autoFinishFromContext(ctx context.Context) bool {
	on, _ := ctx.Value(autoFinishContextKey{}).(bool)
	return on
}

// maybeAutoFinishWorktree 在终态/归档时调用。重复调用只会排一次：判据是任务事件
// finish_auto_queued，和原先 AddEvent 的幂等 id 不同（那个 id 每次都是新的）。
func (s *Service) maybeAutoFinishWorktree(ctx context.Context, rootID, taskID string) {
	if !s.autoFinishEnabled() || s.directExecutor == nil {
		return
	}
	rootID, taskID = strings.TrimSpace(rootID), strings.TrimSpace(taskID)
	if rootID == "" || taskID == "" || s.Finishing(rootID, taskID) {
		return
	}
	store, err := s.taskStore(rootID)
	if err != nil {
		return
	}
	task, err := store.GetTask(ctx, taskID)
	if err != nil || !task.CreateWorktree || !worktreeDirUsable(task.WorktreePath) {
		return
	}
	if s.sessionRunningProbe != nil && s.sessionRunningProbe(task.MainSessionKey) {
		return
	}
	// 任务终态不等于 Run 收敛：引擎会在节点落定后为下游节点建树
	// （PrepareWorkflowNodeWorktree / snapshotRunIntegrationBranch），中间有一拍
	// 「上游 success、下游还没 claim」。这一拍拆树，引擎立刻又建一棵，就是
	// 「清了又建」。判据复用 workflowRunHasOpenNodes —— 它是本仓「Run 还会不会
	// 自己动」的唯一口径；只看 task.Status 或末节点是否在跑都会漏掉这一拍。
	if s.autoFinishRunStillOpen(ctx, store, taskID) {
		return
	}
	events, err := store.ListEvents(ctx, taskID)
	if err != nil {
		return
	}
	var queued, failed *TaskEvent
	for i := range events {
		switch events[i].Type {
		case "finish_auto_queued":
			queued = &events[i]
		case "finish_auto_failed":
			failed = &events[i]
		}
	}
	// 失败事件晚于（或等于）占坑事件时允许重试；否则一次暂时性错误会永久占位。
	if queued != nil && (failed == nil || failed.CreatedAt.Before(queued.CreatedAt)) {
		return
	}
	// 占坑必须先于任何异步动作，否则同一拍里两次终态事件会排两轮。
	if err := store.AddEvent(ctx, TaskEvent{
		ID: newID("event"), TaskID: taskID, Type: "finish_auto_queued",
		Payload:   eventPayload(map[string]any{"worktree": task.WorktreePath}),
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		return
	}
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("[kanban/auto-finish] panic root=%s task=%s: %v", rootID, taskID, recovered)
			}
		}()
		bgCtx := context.WithValue(context.WithoutCancel(ctx), autoFinishContextKey{}, true)
		// PlanFinishWorktree 只读，放在异步段里：终态事件本身不能被 git 探测拖住。
		plan, err := s.PlanFinishWorktree(bgCtx, rootID, taskID)
		if err != nil {
			s.failAutoFinish(bgCtx, rootID, taskID, "判定收尾方式失败", err)
			return
		}
		if plan.Merged {
			if _, err := s.FinishTaskWorktree(bgCtx, FinishWorktreeInput{
				RootID: rootID, TaskID: taskID, DeleteBranch: true, PruneOrphans: true,
			}); err != nil {
				s.failAutoFinish(bgCtx, rootID, taskID, "自动清场失败", err)
			}
			return
		}
		if _, err := s.BeginFinishWorktree(bgCtx, BeginFinishInput{RootID: rootID, TaskID: taskID}); err != nil {
			s.failAutoFinish(bgCtx, rootID, taskID, "自动收尾回合启动失败", err)
		}
	}()
}

// autoFinishRunStillOpen 报告任务的 workflow Run 是否还有会自己动的节点。
//
// 用它替代「task.Status 已终态」和「末节点不在跑」这两个更弱的判据：引擎在上游
// 节点 success 之后才会为下游建树（PrepareWorkflowNodeWorktree /
// snapshotRunIntegrationBranch），这一拍 mask 在两个弱判据之外，却是拆树后
// 立刻重建的唯一窗口。
//
//   - 非 workflow 任务、读不到绑定 → 放行（没有 Run 可动）；
//   - legacy_import 的 Run 是转换时留的审计快照，引擎永远不会再驱动，放行；
//   - 读 Run / 读行失败 → 当作 open（fail-closed：状态不明时不拆树，晚一拍由
//     finalizeWorkflowNodeTerminal 的重试点补上）。
//
// 代价：waiting_user 行、或图里永远不会被 claim 的下游节点，会让 Run 一直算
// open，自动收尾被推迟；树保留，手工「收尾」按钮仍是逃生口。这个取舍是刻意的：
// 树多留一会儿，好过拆了又建。
func (s *Service) autoFinishRunStillOpen(ctx context.Context, store *TaskStore, taskID string) bool {
	run, found, err := s.workflowRunForTask(ctx, store, taskID)
	if err != nil {
		log.Printf("[kanban/auto-finish] read run failed task=%s: %v", taskID, err)
		return true
	}
	if !found || run == nil {
		return false
	}
	if strings.TrimSpace(run.TriggeredBy) == WorkflowTriggeredByLegacyImport {
		return false
	}
	rows, err := store.ListWorkflowNodeRuns(ctx, run.ID)
	if err != nil {
		log.Printf("[kanban/auto-finish] read node runs failed task=%s run=%s: %v", taskID, run.ID, err)
		return true
	}
	return workflowRunHasOpenNodes(run, rows)
}

// failAutoFinish 不静默吞掉后台失败：任务退回 waiting_user 并把原因写到 session_error。
// 归档任务只记录错误，不靠状态回退把人刚归档的卡复活。
func (s *Service) failAutoFinish(ctx context.Context, rootID, taskID, prefix string, cause error) {
	message := prefix + "：" + cause.Error()
	log.Printf("[kanban/auto-finish] %s root=%s task=%s: %v", prefix, rootID, taskID, cause)
	store, err := s.taskStore(rootID)
	if err != nil {
		return
	}
	task, err := store.GetTask(ctx, taskID)
	if err != nil {
		return
	}
	s.recordAutoFinishFailureEvent(ctx, rootID, taskID, message)
	if strings.TrimSpace(task.ArchivedAt) != "" {
		_ = s.recordTaskError(ctx, store, task, message)
		return
	}
	if isTerminalStatus(task.Status) {
		if _, err := s.setTaskStatus(ctx, rootID, taskID, StatusWaitingUser, "auto_finish_failed", message, false); err != nil {
			log.Printf("[kanban/auto-finish] revert status failed root=%s task=%s: %v", rootID, taskID, err)
		}
	}
	_ = s.recordTaskError(ctx, store, task, message)
}

// recordAutoFinishFailureEvent 只负责解掉 finish_auto_queued 占坑，使下一次终态
// 可以重试；状态与可见错误由调用方按自己的上下文写。
func (s *Service) recordAutoFinishFailureEvent(ctx context.Context, rootID, taskID, message string) {
	store, err := s.taskStore(rootID)
	if err != nil {
		log.Printf("[kanban/auto-finish] store for failure event failed root=%s task=%s: %v", rootID, taskID, err)
		return
	}
	if err := store.AddEvent(ctx, TaskEvent{
		ID: newID("event"), TaskID: taskID, Type: "finish_auto_failed",
		Payload:   eventPayload(map[string]any{"message": message}),
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		log.Printf("[kanban/auto-finish] record failure event failed root=%s task=%s: %v", rootID, taskID, err)
	}
}
