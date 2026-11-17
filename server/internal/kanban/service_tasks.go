package kanban

import (
	"context"
	"errors"
	"strings"
	"time"
)

func (s *Service) CreateTask(ctx context.Context, in CreateTaskInput) (TaskDetail, error) {
	rootID := strings.TrimSpace(in.RootID)
	if rootID == "" {
		return TaskDetail{}, errors.New("root_id required")
	}
	store, err := s.taskStore(rootID)
	if err != nil {
		return TaskDetail{}, err
	}
	name := strings.TrimSpace(in.Name)
	input := strings.TrimSpace(in.Input)
	now := time.Now().UTC()
	branchMode, branch := normalizeTaskWorktreeBranch(in.WorktreeBranchMode, in.WorktreeBranch)
	taskID := newID("task")
	task := Task{
		ID:                 taskID,
		RootID:             rootID,
		Name:               name,
		CreateWorktree:     in.CreateWorktree,
		WorktreeBranchMode: branchMode,
		WorktreeBranch:     branch,
		Status:             StatusPending,
		Labels:             []string{},
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if _, err := store.CreateTask(ctx, task, TaskEvent{ID: newID("event"), TaskID: taskID, Type: "task_created", Payload: eventPayload(map[string]any{"input": input}), CreatedAt: now}); err != nil {
		return TaskDetail{}, err
	}
	// 建任务 = 发出第一条留言。任务行先提交（ActivityService 会重新读它），
	// 再把这条留言交给 Router：Router 自己决定这一轮跑工作流还是普通会话。
	// 留言创建失败时删掉刚建的空任务，不让用户看到一张没有输入的卡。
	//
	// 「立即执行」勾掉时（StartImmediately=false）留言以 queued 落库、不派发：
	// 任务立刻出现在看板上，用户之后点「立即执行」（RunNow）再交给 Router。
	if input != "" {
		activitySvc, actErr := s.ActivityServiceForRoot(rootID)
		if actErr == nil {
			if in.StartImmediately != nil && !*in.StartImmediately {
				_, actErr = activitySvc.QueueUserActivity(ctx, taskID, input)
			} else {
				_, _, actErr = activitySvc.CreateUserActivity(ctx, taskID, input, CommentWorkflowChoiceEmpty, nil)
			}
		}
		if actErr != nil {
			_ = store.DeleteTask(ctx, taskID)
			return TaskDetail{}, actErr
		}
	}
	return store.GetDetail(ctx, taskID)
}

func (s *Service) ListTasks(ctx context.Context, rootID string, opts ListTasksOptions) ([]Task, error) {
	store, err := s.taskStore(rootID)
	if err != nil {
		return nil, err
	}
	return store.ListTasks(ctx, opts)
}

func (s *Service) ListTaskDetails(ctx context.Context, rootID string, opts ListTasksOptions) ([]TaskDetail, error) {
	store, err := s.taskStore(rootID)
	if err != nil {
		return nil, err
	}
	return store.ListTaskDetails(ctx, opts)
}

// TaskOverviewItem 是跨项目工作台的一行：任务 + 所属项目。
type TaskOverviewItem struct {
	RootID   string `json:"root_id"`
	RootName string `json:"root_name"`
	Task     Task   `json:"task"`
}

// Overview 汇总所有项目的在途任务（未终态 + 最近完成的少量，供归档区查看）。
// 只读，不建 store 之外的缓存；单个项目失败时跳过不阻塞全貌。
func (s *Service) Overview(ctx context.Context) ([]TaskOverviewItem, error) {
	if s == nil || s.Roots == nil {
		return nil, errors.New("root provider not configured")
	}
	items := []TaskOverviewItem{}
	for _, root := range s.Roots.ListRoots() {
		store, err := s.taskStore(root.ID)
		if err != nil {
			continue
		}
		tasks, err := store.ListTasks(ctx, ListTasksOptions{Limit: 50})
		if err != nil {
			continue
		}
		for _, task := range tasks {
			items = append(items, TaskOverviewItem{RootID: root.ID, RootName: root.EffectiveName(), Task: task})
		}
	}
	return items, nil
}

func (s *Service) GetTask(ctx context.Context, rootID, taskID string) (TaskDetail, error) {
	store, err := s.taskStore(rootID)
	if err != nil {
		return TaskDetail{}, err
	}
	return store.GetDetail(ctx, taskID)
}

// taskSessionNameSeparator 是任务名与 #编号之间的分隔，与后缀一起构成会话名。
const taskSessionNameSeparator = " / "
