package kanban

import (
	"context"
	"strings"
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
	// 装配合并分支解析器
	svc.SetBranchResolver(func(ctx context.Context, task Task, graph WorkflowDefinition) (string, error) {
		return s.snapshotRunIntegrationBranch(ctx, task, graph)
	})
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
	store, err := s.taskStore(rootID)
	if err != nil {
		return nil, err
	}
	if _, err := store.GetTask(ctx, taskID); err != nil {
		return nil, err
	}
	return NewActivityStore(store.db).List(ctx, taskID)
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

// ExecuteActivityWorkflow implements ActivityWorkflowExecutor by triggering
// the workflow runtime driver (WakeWorkflowRun).
func (s *Service) ExecuteActivityWorkflow(ctx context.Context, task Task, run *WorkflowRun) (summary string, err error) {
	if err := s.WakeWorkflowRun(ctx, task.RootID, task.ID, "activity_created"); err != nil {
		return "", err
	}
	// The driver runs asynchronously; the summary will be filled in when the run completes
	return "正在执行工作流...", nil
}

// ExecuteActivityWorkflowWithProgress implements ActivityWorkflowProgressExecutor
// by triggering the workflow runtime driver with progress callbacks.
func (s *Service) ExecuteActivityWorkflowWithProgress(ctx context.Context, task Task, run *WorkflowRun, onNodeStarted func(nodeID string)) (summary string, err error) {
	// For now, just delegate to the basic version
	// TODO: Wire up actual progress reporting from the workflow driver
	return s.ExecuteActivityWorkflow(ctx, task, run)
}
