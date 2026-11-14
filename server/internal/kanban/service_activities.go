package kanban

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Task 6 (Phase 2): per-root entry points for the conversation activity API.
//
// The plan's handler holds a single ActivityService/ActivityStore, but mindfs is
// multi-account and every task node lives in a per-root SQLite store. These
// helpers keep the HTTP layer free of store plumbing and give the (future)
// WebSocket wiring a single place to attach a broadcaster.

// ActivityServiceForRoot builds the ActivityService bound to one data-plane
// root. s.Workflows is the control-plane workflow store; the recommender
// degrades to the first available workflow when no LLM client is configured.
func (s *Service) ActivityServiceForRoot(rootID string) (*ActivityService, error) {
	store, err := s.taskStore(rootID)
	if err != nil {
		return nil, err
	}
	svc := NewActivityService(store, s.Workflows, NewWorkflowRecommender(s.Workflows, nil))
	s.mu.Lock()
	broadcaster := s.activityBroadcaster
	s.mu.Unlock()
	svc.SetBroadcaster(broadcaster)
	// 装配 WorkflowRun 执行端口：通过 Service.WakeWorkflowRun 触发实际执行
	svc.SetWorkflowExecutor(s)
	s.mu.Lock()
	direct := s.directExecutor
	s.mu.Unlock()
	svc.SetDirectExecutor(direct)
	// 装配合并分支解析器
	svc.SetBranchResolver(func(ctx context.Context, task Task, graph WorkflowDefinition) (string, error) {
		return s.snapshotRunIntegrationBranch(ctx, task, graph)
	})
	// Task Router P0：mode=off 返回 nil，ActivityService 连一次额外读取都不做。
	svc.SetRouter(s.RouterServiceForRoot(rootID))
	return svc, nil
}

// SetActivityBroadcaster 装配对话式活动流的广播端口（Task 7）。装配期调用一次；
// nil 表示不广播（单测/老装配行为不变）。
func (s *Service) SetActivityBroadcaster(b ActivityBroadcaster) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.activityBroadcaster = b
	s.mu.Unlock()
}

// SetDirectExecutor 装配「纯对话模式」的执行端口（普通 Agent 回合）。
func (s *Service) SetDirectExecutor(e ActivityDirectExecutor) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.directExecutor = e
	s.mu.Unlock()
}

// CreateUserActivity is the HTTP-facing entry point: it resolves the root's
// task store, then delegates to ActivityService.CreateUserActivity.
func (s *Service) CreateUserActivity(
	ctx context.Context,
	rootID, taskID, body, workflowChoice string,
	workflowVersionID *string,
) (*TaskActivity, *TaskActivity, error) {
	activityService, err := s.ActivityServiceForRoot(rootID)
	if err != nil {
		return nil, nil, err
	}
	return activityService.CreateUserActivity(ctx, taskID, body, workflowChoice, workflowVersionID)
}

// ListTaskActivities returns the conversation stream for one task. ActivityStore
// (not the legacy ListActivities projection) is used so status/summary survive.
func (s *Service) ListTaskActivities(ctx context.Context, rootID, taskID string) ([]*TaskActivity, error) {
	return s.ListTaskActivitiesWithHidden(ctx, rootID, taskID, false)
}

// ListTaskActivitiesWithHidden 在 includeHidden=true 时返回含隐藏行的完整流。
// 默认过滤由 ActivityStore 负责，这里只做任务归属校验。
func (s *Service) ListTaskActivitiesWithHidden(ctx context.Context, rootID, taskID string, includeHidden bool) ([]*TaskActivity, error) {
	store, err := s.taskStore(rootID)
	if err != nil {
		return nil, err
	}
	if _, err := store.GetTask(ctx, taskID); err != nil {
		return nil, err
	}
	activities := NewActivityStore(store.db)
	if includeHidden {
		return activities.ListIncludingHidden(ctx, taskID)
	}
	return activities.List(ctx, taskID)
}

// GetTaskActivity loads one activity and verifies it belongs to taskID. The id
// alone is not a capability: crossing tasks within a root must still 404.
func (s *Service) GetTaskActivity(ctx context.Context, rootID, taskID, activityID string) (*TaskActivity, error) {
	store, err := s.taskStore(rootID)
	if err != nil {
		return nil, err
	}
	if _, err := store.GetTask(ctx, taskID); err != nil {
		return nil, err
	}
	activity, err := NewActivityStore(store.db).Get(ctx, activityID)
	if err != nil {
		return nil, err
	}
	if activity.TaskID != strings.TrimSpace(taskID) {
		return nil, ErrActivityNotFound
	}
	return activity, nil
}

// SetTaskMainSessionKey 把纯对话模式建立的主会话记回任务。已有会话时幂等。
func (s *Service) SetTaskMainSessionKey(ctx context.Context, rootID, taskID, key string) error {
	store, err := s.taskStore(rootID)
	if err != nil {
		return err
	}
	task, err := store.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	key = strings.TrimSpace(key)
	if key == "" || task.MainSessionKey == key {
		return nil
	}
	task.MainSessionKey = key
	task.UpdatedAt = time.Now().UTC()
	return store.UpdateTask(ctx, task)
}

// ExecuteActivityWorkflow implements ActivityWorkflowExecutor by triggering
// the workflow runtime driver (WakeWorkflowRun). 它只说「开始」，结算由
// projectWorkflowTaskStatus → settleWorkflowReply 在真实终态时完成。
func (s *Service) ExecuteActivityWorkflow(ctx context.Context, task Task, run *WorkflowRun) error {
	return s.WakeWorkflowRun(ctx, task.RootID, task.ID, "activity_created")
}

// updateWorkflowReplyProgress 把节点完成进度写到这一轮的 Agent 回复上
// （"步骤 i/n: 名称"）。进度是提示性的：任何失败都静默，不能影响运行。
func (s *Service) updateWorkflowReplyProgress(ctx context.Context, store *TaskStore, run *WorkflowRun, nodeID string) {
	if s == nil || store == nil || run == nil {
		return
	}
	index, total, name, ok := workflowProgressNode(run, nodeID)
	if !ok {
		return
	}
	activity, err := NewActivityStore(store.db).ActivityByRunID(ctx, run.ID)
	if err != nil || activity == nil {
		return
	}
	if activity.Status != ActivityStatusRunning {
		return
	}
	body := fmt.Sprintf("步骤 %d/%d: %s", index, total, name)
	if err := NewActivityStore(store.db).UpdateBody(ctx, activity.ID, body); err != nil {
		return
	}
	activity.Body = body
	s.broadcastActivityUpdated(activity, map[string]any{"body": body})
}

// settleWorkflowReply 把一轮运行的真实终态写回 Router 的占位回复。
//
// 触发点：任务状态投影（节点终态、HumanGate 之后的继续）与取消。只有任务
// 进入终态或 waiting_user 时才落；运行中保持 running。
func (s *Service) settleWorkflowReply(ctx context.Context, store *TaskStore, run *WorkflowRun) {
	if s == nil || store == nil || run == nil {
		return
	}
	task, err := store.GetTask(ctx, run.TaskID)
	if err != nil {
		return
	}
	activities := NewActivityStore(store.db)
	activity, err := activities.ActivityByRunID(ctx, run.ID)
	if err != nil || activity == nil {
		return
	}
	status, summary := workflowReplyOutcome(ctx, store, run, task)
	if status == "" || status == activity.Status {
		if status == activity.Status && summary != "" && summary != activity.Summary {
			_ = activities.UpdateStatus(ctx, activity.ID, status, &summary)
			activity.Summary = summary
			s.broadcastActivityUpdated(activity, map[string]any{"status": status, "summary": summary})
		}
		return
	}
	if err := activities.UpdateStatus(ctx, activity.ID, status, &summary); err != nil {
		return
	}
	activity.Status = status
	activity.Summary = summary
	s.broadcastActivityUpdated(activity, map[string]any{"status": status, "summary": summary})
}

// resumeWorkflowReply 把停在 waiting_user 的占位回复重新标成 running。
// 触发点：HumanGate 批准、暂停后恢复 —— 同一个 Run 继续跑，不新开回复。
func (s *Service) resumeWorkflowReply(ctx context.Context, store *TaskStore, taskID string) {
	if s == nil || store == nil {
		return
	}
	run, found, err := s.workflowRunForTask(ctx, store, taskID)
	if err != nil || !found || run == nil {
		return
	}
	activities := NewActivityStore(store.db)
	activity, err := activities.ActivityByRunID(ctx, run.ID)
	if err != nil || activity == nil {
		return
	}
	if activity.Status != ActivityStatusWaitingUser && activity.Status != ActivityStatusPending {
		return
	}
	if err := activities.UpdateStatus(ctx, activity.ID, ActivityStatusRunning, nil); err != nil {
		return
	}
	activity.Status = ActivityStatusRunning
	s.broadcastActivityUpdated(activity, map[string]any{"status": ActivityStatusRunning})
}

// workflowReplyOutcome maps the projected task state of one run to the reply
// the Router shows the user. waiting_user 表示这一轮没结束、在等人；它不是
// 失败，审批后同一个 Run 会继续。
//
// 这里只写「内容」，不写「壳」：状态徽标与占位文案由前端按 locale 渲染，
// 后端硬编码中文会让英文界面出现中英混排。
func workflowReplyOutcome(ctx context.Context, store *TaskStore, run *WorkflowRun, task Task) (string, string) {
	switch task.Status {
	case StatusSuccess:
		return ActivityStatusCompleted, workflowReplySummary(ctx, store, run)
	case StatusFail:
		return ActivityStatusFailed, workflowReplyReason(ctx, store, run)
	case StatusCancelled:
		return ActivityStatusCancelled, workflowReplyReason(ctx, store, run)
	case StatusWaitingUser, StatusPaused:
		return ActivityStatusWaitingUser, workflowReplyReason(ctx, store, run)
	default:
		return "", ""
	}
}

// workflowReplySummary 取最后一个成功节点输出的 summary；没有就取最后一个
// 节点输出；再没有就留空，由前端回退到本地化的状态文案。
func workflowReplySummary(ctx context.Context, store *TaskStore, run *WorkflowRun) string {
	runs, err := store.ListWorkflowNodeRuns(ctx, run.ID)
	if err != nil {
		return ""
	}
	var fallback string
	for i := len(runs) - 1; i >= 0; i-- {
		if runs[i].Status != NodeRunSuccess {
			continue
		}
		if summary := nodeRunOutputSummary(runs[i]); summary != "" {
			return summary
		}
		if fallback == "" {
			fallback = nodeRunOutputJSON(runs[i])
		}
	}
	return fallback
}

// workflowReplyReason 取最近一条非成功节点留下的可读原因（纯原因，不加前缀
// 或冒号）。NodeRun 本身不存 reason 列；能读的是节点输出里的 reason/prompt/summary。
func workflowReplyReason(ctx context.Context, store *TaskStore, run *WorkflowRun) string {
	runs, err := store.ListWorkflowNodeRuns(ctx, run.ID)
	if err != nil {
		return ""
	}
	for i := len(runs) - 1; i >= 0; i-- {
		if runs[i].Status == NodeRunSuccess || runs[i].Status == NodeRunQueued {
			continue
		}
		if reason := nodeRunText(runs[i], "reason", "error", "prompt", "summary"); reason != "" {
			return reason
		}
	}
	return ""
}

func nodeRunText(nr NodeRun, keys ...string) string {
	for _, key := range keys {
		value, _ := nr.Output[key].(string)
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func nodeRunOutputSummary(nr NodeRun) string {
	if nr.Output == nil {
		return ""
	}
	summary, _ := nr.Output["summary"].(string)
	return strings.TrimSpace(summary)
}

func nodeRunOutputJSON(nr NodeRun) string {
	if len(nr.Output) == 0 {
		return ""
	}
	raw, err := marshalWorkflowJSON(nr.Output)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func (s *Service) broadcastActivityUpdated(activity *TaskActivity, changes map[string]any) {
	s.mu.Lock()
	b := s.activityBroadcaster
	s.mu.Unlock()
	if b != nil && activity != nil {
		b.ActivityUpdated(activity, changes)
	}
}
