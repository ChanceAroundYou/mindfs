package kanban

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"
)

// normalizeTaskStages 展平并规范化任务流水。
func normalizeTaskStages(in []StageTemplate) []StageTemplate {
	out := make([]StageTemplate, 0, len(in))
	for _, st := range in {
		out = append(out, normalizeStageTemplate(st))
	}
	return out
}

func (s *Service) CreateTask(ctx context.Context, in CreateTaskInput) (TaskDetail, error) {
	rootID := strings.TrimSpace(in.RootID)
	if rootID == "" {
		return TaskDetail{}, errors.New("root_id required")
	}
	store, err := s.taskStore(rootID)
	if err != nil {
		return TaskDetail{}, err
	}
	stages := normalizeTaskStages(in.Stages)
	name := strings.TrimSpace(in.Name)
	// 流水必须由调用方随包带上（前端内置默认流水）。模板库已下线，
	// 这里不再有任何回查回退：没有 stages 就是调用方错了，直接报错。
	if len(stages) == 0 || stages[0].Role != RoleUser {
		return TaskDetail{}, errors.New("task first stage must be user")
	}
	now := time.Now().UTC()
	branchMode, branch := normalizeTaskWorktreeBranch(in.WorktreeBranchMode, in.WorktreeBranch)
	taskID := newID("task")
	first := stages[0]
	task := Task{
		ID:                 taskID,
		RootID:             rootID,
		Name:               name,
		Stages:             stages,
		CreateWorktree:     in.CreateWorktree,
		WorktreeBranchMode: branchMode,
		WorktreeBranch:     branch,
		CurrentStageIndex:  0,
		Status:             StatusPending,
		Labels:             []string{},
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	run := StageRun{
		ID:         newID("run"),
		TaskID:     taskID,
		StageIndex: 0,
		StageName:  first.Name,
		Role:       RoleUser,
		Status:     StageStatusPending,
		Input:      strings.TrimSpace(in.Input),
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	event := TaskEvent{
		ID:         newID("event"),
		TaskID:     taskID,
		StageRunID: run.ID,
		Type:       "task_created",
		Payload:    eventPayload(map[string]any{"input": run.Input}),
		CreatedAt:  now,
	}
	if _, err := store.CreateTask(ctx, task, run, event); err != nil {
		return TaskDetail{}, err
	}
	// 首段勾了「立即执行」就直接推进到下一个 agent 段跑起来，没勾就安静停在
	//「未开始」（等用户点「立即执行」）。RunNow 会把当前 user 段一并批准掉，
	// 并替我们兜住 worktree 建不起来的情况。
	//
	// 输入为空时不推进：目标段多半引用 {previous_input}，RunNow 会因
	//「current stage input required」失败。服务端不能假设只有前端一个调用方，
	// 那种情况应当停在「未开始」而不是让创建请求报错。
	if first.StartImmediately && len(stages) > 1 && strings.TrimSpace(in.Input) != "" {
		if _, runErr := s.RunNow(ctx, MoveInput{RootID: rootID, TaskID: taskID, Reason: "start_immediately"}); runErr != nil {
			log.Printf("[kanban] task.start_immediately.error root=%s task=%s err=%v", rootID, taskID, runErr)
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

// TaskSessionName 组出绑定会话该有的名字：<任务名> / #<任务号>。
// 建会话（AppContext.EnsureAgentSession）与「任务改名 → 同步会话名」都走它，
// 后缀才不会只在建会话那一刻存在 —— 改名一次就丢，那正是这个函数要收口的原因。
//
// 幂等：base 尾部已带本任务号的后缀时先剥再拼，重复调用不会叠成 "名 / #8 / #8"。
// taskNumber <= 0（legacy 行没有编号）时原样返回，不凭空造后缀。
