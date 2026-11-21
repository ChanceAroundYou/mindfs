package kanban

import (
	"context"
	"errors"
	"fmt"
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
	created, err := store.CreateTask(ctx, task, TaskEvent{ID: newID("event"), TaskID: taskID, Type: "task_created", Payload: eventPayload(map[string]any{"input": input}), CreatedAt: now})
	if err != nil {
		return TaskDetail{}, err
	}
	// 用落库后的行继续：任务号是 CreateTask 里分配的，建树命名（task-<编号>）依赖它。
	task = created
	// 勾了 worktree 就在建卡时把树真正建出来：用户要的是「勾上就有」，而不是等到
	// 第一个节点要跑才发现活全在主 checkout 里。失败就把刚建的任务删掉并返回错误
	// （fail closed）—— 留一张「勾了却没树」的卡比直接报错更误导。
	if task.CreateWorktree {
		ensured, werr := s.ensureTaskWorktree(ctx, store, task)
		if werr != nil {
			_ = store.DeleteTask(ctx, taskID)
			return TaskDetail{}, fmt.Errorf("create task worktree: %w", werr)
		}
		task = ensured
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
			var choice string
			var versionID *string
			choice, versionID, actErr = normalizeCreateTaskWorkflow(in.WorkflowChoice, in.WorkflowVersionID)
			if actErr == nil {
				if in.StartImmediately != nil && !*in.StartImmediately {
					_, actErr = activitySvc.QueueUserActivity(ctx, taskID, input, choice, versionID)
				} else {
					_, _, actErr = activitySvc.CreateUserActivity(ctx, taskID, input, choice, versionID)
				}
			}
		}
		if actErr != nil {
			_ = store.DeleteTask(ctx, taskID)
			return TaskDetail{}, actErr
		}
	}
	return store.GetDetail(ctx, taskID)
}

// normalizeCreateTaskWorkflow 规整建任务时带的工作流选择。
//
// 空串按 `empty`（交给 Router 自动挑）处理；版本只在 specified 时下发。
// 非法选择不在这里吞掉 —— 交给 ActivityService 的 normalizeWorkflowChoice 报错，
// 让用户在建卡这一步就看见问题，而不是任务建好、留言却派发失败。
//
// specified 但没版本在这里就拒：排队路径要等用户点「立即执行」才派发，如果放到
// 派发时才失败，卡片会一直停在待开始、每次点都报同一句错，用户无从修起。
func normalizeCreateTaskWorkflow(choice, versionID string) (string, *string, error) {
	normalized := strings.TrimSpace(choice)
	if normalized == "" {
		normalized = CommentWorkflowChoiceEmpty
	}
	if normalized != CommentWorkflowChoiceSpecified {
		return normalized, nil, nil
	}
	version := strings.TrimSpace(versionID)
	if version == "" {
		return normalized, nil, errors.New("workflow_version_id required when workflow_choice=specified")
	}
	return normalized, &version, nil
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
		tasks, err := store.ListTasks(ctx, ListTasksOptions{Limit: 50, ExcludeArchived: true})
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
