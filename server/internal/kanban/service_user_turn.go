package kanban

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// 「新留言开始新一轮」的守卫与取代语义（D3 的执行入口）。
//
// 为什么必须在这里而不仅靠 CreateWorkflowRunAndSetEngine 的绑定守卫：
//   - 绑定守卫只会拒绝（ErrTaskAlreadyWorkflow），用户看到的是「task already
//     migrated」这种实现细节，而不是「等这一轮结束」；
//   - 任务处于 waiting_user / 终态时，新建 Run 根本不会报错，driver 也会在
//     AdvanceWorkflowRun 的终态闸门直接返回 —— 结果是一条永远 pending 的僵尸
//     Run，用户等不到任何回复（2026-11-14 隔离实例实测）。
var (
	// ErrWorkflowTurnActive 表示同一任务已有活动执行。D3：每任务至多一个非终态
	// Run / 一个活动执行；留言必须等这一轮结束后再发。
	ErrWorkflowTurnActive = errors.New("当前任务还在执行中，请等这一轮结束后再发")
	// ErrWorkflowWaitingApproval 表示工作流停在 HumanGate。批准/驳回是专用决定
	// （D4），普通留言不能绕过它，也不能借此新开一条 Run。
	ErrWorkflowWaitingApproval = errors.New("当前工作流在等待你审批，请先批准或驳回")
	// ErrTaskClosed 表示任务已进入终态。继续对话需要新建任务；这里显式拒绝，
	// 不建一条 driver 永远不会推进的 Run。
	ErrTaskClosed = errors.New("任务已结束，要开始新一轮请新建任务")
	// ErrTaskPaused 表示任务被暂停。暂停是操作员状态，不能靠发消息隐式恢复。
	ErrTaskPaused = errors.New("任务已暂停，请先点「继续」")
)

// PrepareNewUserTurn implements ActivityRunLifecycle.
//
// 顺序：先判任务级状态，再判活动执行；只有 supersede=true 的 workflow 回合才会
// 冻结停住的旧 Run。先停 driver 再重扫一次，是为了覆盖「检查与停止之间刚好
// claim 了下一个节点」的窗口（与 CancelWorkflowRun 相同的做法）。
func (s *Service) PrepareNewUserTurn(ctx context.Context, task Task, supersede bool) error {
	if s == nil {
		return nil
	}
	store, err := s.taskStore(task.RootID)
	if err != nil {
		return err
	}
	current, err := store.GetTask(ctx, task.ID)
	if err != nil {
		return err
	}
	switch {
	case isTerminalStatus(current.Status):
		return ErrTaskClosed
	case current.Status == StatusPaused:
		return ErrTaskPaused
	}
	run, found, err := s.workflowRunForTask(ctx, store, current.ID)
	if err != nil || !found || run == nil {
		return err
	}
	if err := rejectRunningWorkflowNodeRuns(ctx, store, run.ID); err != nil {
		return err
	}
	if !supersede {
		// 直连回合只守「没有活动执行」，不冻结停在 waiting_user 的旧 Run：
		// 用户可能只是想先聊一句，再回去审批。
		return nil
	}
	if gate, ok := waitingHumanGate(run); ok {
		return fmt.Errorf("%w（%s）", ErrWorkflowWaitingApproval, gate)
	}
	s.stopWorkflowDriver(current.RootID, current.ID)
	if err := rejectRunningWorkflowNodeRuns(ctx, store, run.ID); err != nil {
		return err
	}
	if err := s.cancelQueuedWorkflowNodeRuns(ctx, store, run.ID, "superseded by a new user message"); err != nil {
		return err
	}
	if err := s.markWorkflowRunSuperseded(ctx, store, run.ID); err != nil {
		return err
	}
	if err := store.UpdateTaskStatus(ctx, current.ID, StatusPending, false); err != nil {
		return err
	}
	s.settleSupersededReply(ctx, store, run)
	s.broadcastTask(ctx, store, current.ID)
	return nil
}

// rejectRunningWorkflowNodeRuns fails closed the moment an Agent turn is live.
// Queued rows are allowed here: they never started, so the supersede path can
// cancel them without losing an external side effect.
func rejectRunningWorkflowNodeRuns(ctx context.Context, store *TaskStore, runID string) error {
	open, err := store.ListOpenWorkflowNodeRuns(ctx, runID)
	if err != nil {
		return err
	}
	for _, nr := range open {
		if nr.Status == NodeRunRunning {
			return ErrWorkflowTurnActive
		}
	}
	return nil
}

// cancelQueuedWorkflowNodeRuns closes rows that were claimed but never sent.
// They are safe to cancel: no Agent turn exists behind them. The finish lock is
// shared with merge finalize so a row that is somehow still finishing is not
// rewritten under the operator's feet.
func (s *Service) cancelQueuedWorkflowNodeRuns(ctx context.Context, store *TaskStore, runID, reason string) error {
	open, err := store.ListOpenWorkflowNodeRuns(ctx, runID)
	if err != nil {
		return err
	}
	for _, nr := range open {
		unlock := s.lockWorkflowNodeFinish(nr.ID)
		_, cerr := store.CancelWorkflowNodeRun(ctx, nr, reason)
		unlock()
		if cerr != nil {
			return cerr
		}
	}
	return nil
}

// waitingHumanGate reports the gate a run is waiting on. A comment must not be
// treated as an approval (D4), and a new run must not silently abandon it.
func waitingHumanGate(run *WorkflowRun) (string, bool) {
	if run == nil {
		return "", false
	}
	for _, node := range run.Graph.Nodes {
		if node.Kind != NodeKindHumanGate || run.NodeStates[node.ID] != NodeRunWaitingUser {
			continue
		}
		prompt := ""
		if output := run.NodeOutputs[node.ID]; output != nil {
			prompt, _ = output["prompt"].(string)
		}
		for _, candidate := range []string{prompt, node.Name, node.ID} {
			if trimmed := strings.TrimSpace(candidate); trimmed != "" {
				return trimmed, true
			}
		}
		return node.ID, true
	}
	return "", false
}

// markWorkflowRunSuperseded freezes the run projection so it is terminally
// cancelled while keeping every executed node's evidence intact. NodeRun rows
// are not touched: they remain the audit trail of what actually ran.
func (s *Service) markWorkflowRunSuperseded(ctx context.Context, store *TaskStore, runID string) error {
	for attempt := 0; attempt < 5; attempt++ {
		run, err := store.WorkflowRunByID(ctx, runID)
		if err != nil {
			return err
		}
		if run.NodeStates == nil {
			run.NodeStates = map[string]string{}
		}
		for _, node := range run.Graph.Nodes {
			if !workflowNodeStateTerminal(run.NodeStates[node.ID]) {
				run.NodeStates[node.ID] = NodeRunCancelled
			}
		}
		for nodeID, state := range run.NodeStates {
			if !workflowNodeStateTerminal(state) {
				run.NodeStates[nodeID] = NodeRunCancelled
			}
		}
		err = store.SaveWorkflowRunWithEvent(ctx, run, WorkflowRunEvent{
			Type: WorkflowEventRunSuperseded, Payload: map[string]any{"reason": "superseded by a new user message"},
		})
		if errors.Is(err, ErrRunConflict) {
			continue
		}
		return err
	}
	return ErrRunConflict
}

func workflowNodeStateTerminal(state string) bool {
	switch state {
	case NodeRunSuccess, NodeRunFail, NodeRunSkipped, NodeRunCancelled:
		return true
	default:
		// unknown / waiting_user / running / queued / pending 都还不是终态：
		// 被取代时必须冻结成 cancelled，否则 ProjectStatusFromRun 仍会报
		// waiting_user / running，绑定守卫继续拒绝下一条留言。
		return false
	}
}

// settleSupersededReply keeps the old turn visible but no longer "waiting": the
// user already moved on with the next message.
func (s *Service) settleSupersededReply(ctx context.Context, store *TaskStore, run *WorkflowRun) {
	if s == nil || store == nil || run == nil {
		return
	}
	activities := NewActivityStore(store.db)
	activity, err := activities.ActivityByRunID(ctx, run.ID)
	if err != nil || activity == nil {
		return
	}
	summary := workflowReplyReason(ctx, store, run)
	if err := activities.UpdateStatus(ctx, activity.ID, ActivityStatusCancelled, &summary); err != nil {
		return
	}
	activity.Status = ActivityStatusCancelled
	activity.Summary = summary
	s.broadcastActivityUpdated(activity, map[string]any{"status": ActivityStatusCancelled, "summary": summary})
	// 进度正文（"步骤 i/n"）属于运行中提示；旧轮已经结束，留着会被当成终态文案。
	_ = activities.ClearBody(ctx, activity.ID)
	activity.Body = ""
}
