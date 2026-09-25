package kanban

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"mindfs/server/internal/fs"
)

type RootProvider interface {
	GetRoot(rootID string) (fs.RootInfo, error)
	ListRoots() []fs.RootInfo
}

type Service struct {
	Templates *TemplateStore
	Roots     RootProvider
	Runner    Runner

	mu       sync.Mutex
	stores   map[string]*TaskStore
	taskRun  map[string]bool
	taskPend map[string]bool
}

var errStopTaskExecution = errors.New("stop task execution")

func NewService(templates *TemplateStore, roots RootProvider) *Service {
	return &Service{Templates: templates, Roots: roots, stores: map[string]*TaskStore{}, taskRun: map[string]bool{}, taskPend: map[string]bool{}}
}

func (s *Service) SetRunner(runner Runner) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.Runner = runner
	s.mu.Unlock()
}

type CreateTaskInput struct {
	RootID             string
	TaskTemplateID     string          // 可选：预设来源，仅记录来自哪个预设；内容在创建时拷进任务
	Input              string          // 第一段的用户输入
	Name               string          // 可选：任务名；缺省时前端用输入首行回退
	Stages             []StageTemplate // 可选：直接给定流水；否则从预设拷贝
	CreateWorktree     bool
	WorktreeBranchMode string
	WorktreeBranch     string
}

type MoveInput struct {
	RootID     string
	TaskID     string
	Reason     string
	StageIndex int
}

type UpdateTaskInput struct {
	RootID             string
	TaskID             string
	Input              string
	CreateWorktree     *bool
	WorktreeBranchMode string
	WorktreeBranch     string
}

// AddStageInput 追加一段 prompt（下一段要执行的内容）。
// 任务处于等待用户时，追加即自动推进；其他状态排入流水尾。
type AddStageInput struct {
	RootID string
	TaskID string
	Stage  StageTemplate
}

// UpdateStageInput 修改任务流水里某一段的定义。
type UpdateStageInput struct {
	RootID string
	TaskID string
	Index  int
	Stage  *StageTemplate // 全量替换该段定义
}

func (s *Service) ListStageTemplates(ctx context.Context) ([]StageTemplate, error) {
	if s == nil || s.Templates == nil {
		return nil, errors.New("template store not configured")
	}
	return s.Templates.ListStageTemplates()
}

func (s *Service) SaveStageTemplate(ctx context.Context, in StageTemplate) (StageTemplate, error) {
	if s == nil || s.Templates == nil {
		return StageTemplate{}, errors.New("template store not configured")
	}
	return s.Templates.SaveStageTemplate(in)
}

func (s *Service) DeleteStageTemplate(ctx context.Context, id string) error {
	if s == nil || s.Templates == nil {
		return errors.New("template store not configured")
	}
	return s.Templates.DeleteStageTemplate(id)
}

func (s *Service) ListTaskTemplates(ctx context.Context) ([]TaskTemplate, error) {
	if s == nil || s.Templates == nil {
		return nil, errors.New("template store not configured")
	}
	return s.Templates.ListTaskTemplates()
}

// 模板至此只是「预设」：可随时编辑/删除，任务创建时已拷贝快照，与在途任务完全解耦。
func (s *Service) SaveTaskTemplate(ctx context.Context, in TaskTemplate) (TaskTemplate, error) {
	if s == nil || s.Templates == nil {
		return TaskTemplate{}, errors.New("template store not configured")
	}
	return s.Templates.SaveTaskTemplate(in)
}

func (s *Service) DeleteTaskTemplate(ctx context.Context, id string) error {
	if s == nil || s.Templates == nil {
		return errors.New("template store not configured")
	}
	return s.Templates.DeleteTaskTemplate(id)
}

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
	if len(stages) == 0 && strings.TrimSpace(in.TaskTemplateID) != "" {
		tmpl, err := s.Templates.GetTaskTemplate(in.TaskTemplateID)
		if err != nil {
			return TaskDetail{}, err
		}
		for _, ts := range tmpl.Stages {
			stages = append(stages, normalizeStageTemplate(ts.Snapshot))
		}
		if name == "" {
			name = tmpl.Name
		}
	}
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
		TaskTemplateID:     strings.TrimSpace(in.TaskTemplateID),
		TaskTemplateName:   templateNameForTask(s, in.TaskTemplateID, name),
		Stages:             stages,
		CreateWorktree:     in.CreateWorktree,
		WorktreeBranchMode: branchMode,
		WorktreeBranch:     branch,
		CurrentStageIndex:  0,
		Status:             StatusWaitingUser,
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
		Status:     StageStatusWaitingUser,
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
	return store.GetDetail(ctx, taskID)
}

func templateNameForTask(s *Service, templateID, fallback string) string {
	tmpl, err := s.Templates.GetTaskTemplate(templateID)
	if err != nil {
		return ""
	}
	return tmpl.Name
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
	RootID   string            `json:"root_id"`
	RootName string            `json:"root_name"`
	Task     Task              `json:"task"`
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

// RenameTask 设置任务名。
func (s *Service) RenameTask(ctx context.Context, rootID, taskID, name string) (TaskDetail, error) {
	store, err := s.taskStore(rootID)
	if err != nil {
		return TaskDetail{}, err
	}
	task, err := store.GetTask(ctx, strings.TrimSpace(taskID))
	if err != nil {
		return TaskDetail{}, err
	}
	task.Name = strings.TrimSpace(name)
	task.UpdatedAt = time.Now().UTC()
	if err := store.UpdateTask(ctx, task); err != nil {
		return TaskDetail{}, err
	}
	detail, err := store.GetDetail(ctx, task.ID)
	if err == nil && s.Runner != nil {
		s.Runner.TaskUpdated(task.RootID, detail)
	}
	return detail, err
}

// TaskNameFromSession：会话改名时同步到绑定的任务（main_session_key 方向；
// 与 HTTP 层「任务→会话」同步共同构成任务名 ↔ 会话名双向绑定）。
// 返回 (detail, true) 表示任务名确实更新；未找到任务或名字未变返回 (_, false)。
func (s *Service) TaskNameFromSession(ctx context.Context, rootID, sessionKey, name string) (TaskDetail, bool) {
	store, err := s.taskStore(rootID)
	if err != nil {
		return TaskDetail{}, false
	}
	taskID, err := store.TaskIDForMainSession(ctx, sessionKey)
	if err != nil || taskID == "" {
		return TaskDetail{}, false
	}
	task, err := store.GetTask(ctx, taskID)
	if err != nil || task.Name == strings.TrimSpace(name) {
		return TaskDetail{}, false
	}
	detail, err := s.RenameTask(ctx, rootID, taskID, name)
	if err != nil {
		return TaskDetail{}, false
	}
	return detail, true
}

// AddStage 追加下一段 prompt。任务等待用户时追加即推进并执行；其他状态排入流水尾。
func (s *Service) AddStage(ctx context.Context, in AddStageInput) (TaskDetail, error) {
	store, err := s.taskStore(in.RootID)
	if err != nil {
		return TaskDetail{}, err
	}
	task, err := s.ensureServiceTask(ctx, in.RootID, store, strings.TrimSpace(in.TaskID))
	if err != nil {
		return TaskDetail{}, err
	}
	stage := normalizeStageTemplate(in.Stage)
	if strings.TrimSpace(stage.PromptTemplate) == "" {
		return TaskDetail{}, errors.New("stage prompt required")
	}
	if task.Status == StatusWaitingUser {
		stage.Name = defaultStageName(task, len(task.Stages))
		task.Stages = append(task.Stages, stage)
		if latest, runErr := store.LatestStageRun(ctx, task.ID, task.CurrentStageIndex); runErr == nil {
			if latest.Status != StageStatusSuccess {
				_ = store.UpdateStageRunStatus(ctx, latest.ID, StageStatusApproved)
			}
		}
		detail, err := s.moveTo(ctx, store, task, len(task.Stages)-1, "user_approved", StageStatusApproved, "comment")
		if err != nil {
			return TaskDetail{}, err
		}
		s.RunTask(detail.Task.RootID, detail.Task.ID)
		return detail, nil
	}
	stage.Name = defaultStageName(task, len(task.Stages))
	task.Stages = append(task.Stages, stage)
	task.UpdatedAt = time.Now().UTC()
	if err := store.UpdateTask(ctx, task); err != nil {
		return TaskDetail{}, err
	}
	detail, err := store.GetDetail(ctx, task.ID)
	if err == nil && s.Runner != nil {
		s.Runner.TaskUpdated(task.RootID, detail)
	}
	return detail, err
}

// UpdateStage 修改任务某一段的定义（prompt / agent / model / effort 等）。
func (s *Service) UpdateStage(ctx context.Context, in UpdateStageInput) (TaskDetail, error) {
	store, err := s.taskStore(in.RootID)
	if err != nil {
		return TaskDetail{}, err
	}
	task, err := s.ensureServiceTask(ctx, in.RootID, store, strings.TrimSpace(in.TaskID))
	if err != nil {
		return TaskDetail{}, err
	}
	idx := in.Index
	if idx < 0 || idx >= len(task.Stages) {
		return TaskDetail{}, errors.New("stage_index out of range")
	}
	now := time.Now().UTC()
	if in.Stage != nil {
		updated := normalizeStageTemplate(*in.Stage)
		updated.ID = task.Stages[idx].ID
		updated.CreatedAt = task.Stages[idx].CreatedAt
		task.Stages[idx] = updated
	}
	task.Stages[idx].UpdatedAt = now
	task.UpdatedAt = now
	if err := store.UpdateTask(ctx, task); err != nil {
		return TaskDetail{}, err
	}
	detail, err := store.GetDetail(ctx, task.ID)
	if err == nil && s.Runner != nil {
		s.Runner.TaskUpdated(task.RootID, detail)
	}
	return detail, err
}

// RerunStage 重新执行某段（任务不在运行中时）：把指针移回此段并触发执行。
func (s *Service) RerunStage(ctx context.Context, in MoveInput) (TaskDetail, error) {
	store, task, err := s.loadForMove(ctx, in.RootID, in.TaskID)
	if err != nil {
		return TaskDetail{}, err
	}
	if running, runErr := store.LatestStageRun(ctx, task.ID, task.CurrentStageIndex); runErr == nil && running.Status == StageStatusRunning && task.Status == StatusRunning {
		return TaskDetail{}, errors.New("task is running")
	}
	if in.StageIndex < 0 || in.StageIndex >= len(task.Stages) {
		return TaskDetail{}, errors.New("stage_index out of range")
	}
	detail, err := s.moveTo(ctx, store, task, in.StageIndex, "stage_rerun", "", in.Reason)
	if err != nil {
		return TaskDetail{}, err
	}
	if !isTerminalStatus(detail.Task.Status) {
		s.RunTask(detail.Task.RootID, detail.Task.ID)
	}
	return detail, err
}

func (s *Service) UpdateCurrentInput(ctx context.Context, in UpdateTaskInput) (TaskDetail, error) {
	store, err := s.taskStore(in.RootID)
	if err != nil {
		return TaskDetail{}, err
	}
	task, err := store.GetTask(ctx, strings.TrimSpace(in.TaskID))
	if err != nil {
		return TaskDetail{}, err
	}
	run, err := store.LatestStageRun(ctx, task.ID, task.CurrentStageIndex)
	if err != nil {
		return TaskDetail{}, err
	}
	input := strings.TrimSpace(in.Input)
	payload := map[string]any{"input": input}
	if in.CreateWorktree != nil {
		if task.CurrentStageIndex != 0 {
			return TaskDetail{}, errors.New("create_worktree can only be changed in first stage")
		}
		if strings.TrimSpace(task.WorktreePath) != "" {
			return TaskDetail{}, errors.New("create_worktree cannot be changed after worktree is created")
		}
		now := time.Now().UTC()
		task.CreateWorktree = *in.CreateWorktree
		task.WorktreeBranchMode, task.WorktreeBranch = normalizeTaskWorktreeBranch(in.WorktreeBranchMode, in.WorktreeBranch)
		task.AuxFlags.SessionError = ""
		task.UpdatedAt = now
		run.Input = input
		event := TaskEvent{
			ID:         newID("event"),
			TaskID:     task.ID,
			StageRunID: run.ID,
			Type:       "stage_input_updated",
			Payload: eventPayload(map[string]any{
				"input":                input,
				"create_worktree":      *in.CreateWorktree,
				"worktree_branch_mode": task.WorktreeBranchMode,
				"worktree_branch":      task.WorktreeBranch,
			}),
			CreatedAt: now,
		}
		if err := store.UpdateTaskAndStageRun(ctx, task, run, event); err != nil {
			return TaskDetail{}, err
		}
		detail, err := store.GetDetail(ctx, task.ID)
		if err == nil && s.Runner != nil {
			s.Runner.TaskUpdated(task.RootID, detail)
		}
		return detail, err
	}
	if err := store.UpdateStageRunInput(ctx, run.ID, input); err != nil {
		return TaskDetail{}, err
	}
	if strings.TrimSpace(task.AuxFlags.SessionError) != "" {
		empty := ""
		_ = store.UpdateTaskAuxFlags(ctx, task.ID, TaskAuxFlagsPatch{SessionError: &empty})
	}
	_ = store.AddEvent(ctx, TaskEvent{
		ID:         newID("event"),
		TaskID:     task.ID,
		StageRunID: run.ID,
		Type:       "stage_input_updated",
		Payload:    eventPayload(payload),
		CreatedAt:  time.Now().UTC(),
	})
	detail, err := store.GetDetail(ctx, task.ID)
	if err == nil && s.Runner != nil {
		s.Runner.TaskUpdated(task.RootID, detail)
	}
	return detail, err
}

func (s *Service) UpdateFirstInput(ctx context.Context, in UpdateTaskInput) (TaskDetail, error) {
	return s.UpdateCurrentInput(ctx, in)
}

func (s *Service) Next(ctx context.Context, in MoveInput) (TaskDetail, error) {
	store, err := s.taskStore(in.RootID)
	if err != nil {
		return TaskDetail{}, err
	}
	task, err := store.GetTask(ctx, strings.TrimSpace(in.TaskID))
	if err != nil {
		return TaskDetail{}, err
	}
	if isTerminalStatus(task.Status) {
		return store.GetDetail(ctx, task.ID)
	}
	// 末段等待用户＝收尾，直接完成而不是报 stage out of range。
	if task.Status == StatusWaitingUser && task.CurrentStageIndex >= len(task.Stages)-1 {
		if latest, latestErr := store.LatestStageRun(ctx, task.ID, task.CurrentStageIndex); latestErr == nil {
			if latest.Status != StageStatusSuccess {
				_ = store.UpdateStageRunStatus(ctx, latest.ID, StageStatusApproved)
			}
		}
		if err := s.finishTask(ctx, store, task, StatusSuccess, "completed", in.Reason); err != nil {
			return TaskDetail{}, err
		}
		detail, err := store.GetDetail(ctx, task.ID)
		if err == nil && s.Runner != nil {
			s.Runner.TaskUpdated(task.RootID, detail)
		}
		return detail, err
	}
	detail, err := s.moveRelative(ctx, in, 1, "user_approved", StageStatusApproved)
	if err == nil {
		s.RunTask(detail.Task.RootID, detail.Task.ID)
	}
	return detail, err
}

// RunNow：待开始直接开跑；等待用户视同验收推进到下一段。
func (s *Service) RunNow(ctx context.Context, in MoveInput) (TaskDetail, error) {
	store, err := s.taskStore(in.RootID)
	if err != nil {
		return TaskDetail{}, err
	}
	task, err := store.GetTask(ctx, strings.TrimSpace(in.TaskID))
	if err != nil {
		return TaskDetail{}, err
	}
	switch task.Status {
	case StatusWaitingUser:
		return s.Next(ctx, in)
	case StatusRunning, StatusPaused:
		return store.GetDetail(ctx, task.ID)
	}
	_ = store.AddEvent(ctx, TaskEvent{
		ID:        newID("event"),
		TaskID:    task.ID,
		Type:      "run_now",
		Payload:   eventPayload(map[string]any{"reason": strings.TrimSpace(in.Reason)}),
		CreatedAt: time.Now().UTC(),
	})
	if task.CreateWorktree {
		if _, werr := s.ensureTaskWorktree(ctx, store, task); werr != nil {
			_ = s.recordTaskError(ctx, store, task, "", werr.Error())
			return store.GetDetail(ctx, task.ID)
		}
	}
	detail, err := store.GetDetail(ctx, task.ID)
	if err == nil {
		s.RunTask(detail.Task.RootID, detail.Task.ID)
	}
	return detail, err
}

func (s *Service) Pause(ctx context.Context, in MoveInput) (TaskDetail, error) {
	return s.setTaskStatus(ctx, in.RootID, in.TaskID, StatusPaused, "paused", in.Reason, false)
}

func (s *Service) Resume(ctx context.Context, in MoveInput) (TaskDetail, error) {
	detail, err := s.setTaskStatus(ctx, in.RootID, in.TaskID, StatusRunning, "resumed", in.Reason, false)
	if err == nil {
		s.RunTask(detail.Task.RootID, detail.Task.ID)
	}
	return detail, err
}

func (s *Service) Fail(ctx context.Context, in MoveInput) (TaskDetail, error) {
	return s.setTaskStatus(ctx, in.RootID, in.TaskID, StatusFail, "stage_failed", in.Reason, true)
}

func (s *Service) Cancel(ctx context.Context, in MoveInput) (TaskDetail, error) {
	return s.setTaskStatus(ctx, in.RootID, in.TaskID, StatusCancelled, "cancelled", in.Reason, true)
}

// Complete：任何非终态任务都可一键完成。
// 不看会话/worktree 是否还在——会话被删、worktree 丢了，任务状态照样能人工收尾，
// 否则这些任务会永远卡在 running 没法推进。
func (s *Service) Complete(ctx context.Context, in MoveInput) (TaskDetail, error) {
	store, task, err := s.loadForMove(ctx, in.RootID, in.TaskID)
	if err != nil {
		return TaskDetail{}, err
	}
	if isTerminalStatus(task.Status) {
		return store.GetDetail(ctx, task.ID)
	}
	if latest, runErr := store.LatestStageRun(ctx, task.ID, task.CurrentStageIndex); runErr == nil {
		if latest.Status != StageStatusSuccess {
			_ = store.UpdateStageRunStatus(ctx, latest.ID, StageStatusApproved)
		}
	}
	if err := s.finishTask(ctx, store, task, StatusSuccess, "completed", in.Reason); err != nil {
		return TaskDetail{}, err
	}
	return store.GetDetail(ctx, task.ID)
}

func (s *Service) Status(ctx context.Context, rootID, taskID string) (TaskDetail, error) {
	return s.GetTask(ctx, rootID, taskID)
}

func (s *Service) RunTask(rootID, taskID string) {
	if s == nil || s.Runner == nil {
		return
	}
	rootID = strings.TrimSpace(rootID)
	taskID = strings.TrimSpace(taskID)
	if rootID == "" || taskID == "" {
		return
	}
	// 同一任务同时只允许一个执行体。Next/Resume/RunNow 等重复请求不应把同一 agent 阶段跑两次
	// （重复创建 agent 会话、重复消耗 token）。重复请求记为待补跑。
	key := rootID + "\x00" + taskID
	s.mu.Lock()
	if s.taskRun[key] {
		s.taskPend[key] = true
		s.mu.Unlock()
		return
	}
	s.taskRun[key] = true
	s.mu.Unlock()
	go func() {
		for {
			if err := s.executeTask(context.Background(), rootID, taskID); err != nil {
				log.Printf("[kanban] task.execute.error root=%s task=%s err=%v", rootID, taskID, err)
			}
			s.mu.Lock()
			// 执行期间又有请求进来 → 补跑一次（此时阶段多已 waiting_user，补跑不会重复执行 agent）。
			if s.taskPend[key] {
				delete(s.taskPend, key)
				s.mu.Unlock()
				continue
			}
			delete(s.taskRun, key)
			s.mu.Unlock()
			break
		}
	}()
}

// KickPending 会启动时进入：仅兜底执行既存任务指针所在段落，不再有排队/槽位语义。
// 只对 pending/running 且未被任何执行体持有的任务触发一次 RunTask。
func (s *Service) KickPending(rootID string) {
	if s == nil || s.Runner == nil {
		return
	}
	rootID = strings.TrimSpace(rootID)
	if rootID == "" {
		return
	}
	go func() {
		store, err := s.taskStore(rootID)
		if err != nil {
			return
		}
		tasks, err := store.ListTasks(context.Background(), ListTasksOptions{})
		if err != nil {
			return
		}
		for _, task := range tasks {
			if isTerminalStatus(task.Status) || task.Status == StatusWaitingUser || task.Status == StatusPaused {
				continue
			}
			if strings.TrimSpace(task.AuxFlags.SessionError) != "" {
				continue
			}
			s.RunTask(rootID, task.ID)
		}
	}()
}

func (s *Service) UpdateTaskAuxFlags(ctx context.Context, rootID, taskID string, patch TaskAuxFlagsPatch, eventType string) (TaskDetail, error) {
	store, err := s.taskStore(rootID)
	if err != nil {
		return TaskDetail{}, err
	}
	task, err := store.GetTask(ctx, taskID)
	if err != nil {
		return TaskDetail{}, err
	}
	if isTerminalStatus(task.Status) && patch.AskUserWaiting != nil && *patch.AskUserWaiting {
		return store.GetDetail(ctx, task.ID)
	}
	if err := store.UpdateTaskAuxFlags(ctx, task.ID, patch); err != nil {
		return TaskDetail{}, err
	}
	if strings.TrimSpace(eventType) != "" {
		stageRunID := ""
		if run, err := store.LatestStageRun(ctx, task.ID, task.CurrentStageIndex); err == nil {
			stageRunID = run.ID
		}
		_ = store.AddEvent(ctx, TaskEvent{
			ID:         newID("event"),
			TaskID:     task.ID,
			StageRunID: stageRunID,
			Type:       strings.TrimSpace(eventType),
			Payload:    eventPayload(map[string]any{"aux_flags": patchPayload(patch)}),
			CreatedAt:  time.Now().UTC(),
		})
	}
	detail, err := store.GetDetail(ctx, task.ID)
	if err == nil && s.Runner != nil {
		s.Runner.TaskUpdated(rootID, detail)
	}
	return detail, err
}

func patchPayload(patch TaskAuxFlagsPatch) map[string]any {
	out := map[string]any{}
	if patch.AskUserWaiting != nil {
		out["ask_user_waiting"] = *patch.AskUserWaiting
	}
	if patch.HasPlan != nil {
		out["has_plan"] = *patch.HasPlan
	}
	if patch.HasTodos != nil {
		out["has_todos"] = *patch.HasTodos
	}
	if patch.HasTask != nil {
		out["has_task"] = *patch.HasTask
	}
	if patch.SessionError != nil {
		out["session_error"] = strings.TrimSpace(*patch.SessionError)
	}
	return out
}

func (s *Service) ensureTaskWorktree(ctx context.Context, store *TaskStore, task Task) (Task, error) {
	if !task.CreateWorktree || strings.TrimSpace(task.WorktreePath) != "" {
		return task, nil
	}
	if s.Runner == nil {
		return task, errors.New("task runner not configured")
	}
	name := renderWorktreeName("", task)
	branchMode, branch := normalizeTaskWorktreeBranch(task.WorktreeBranchMode, task.WorktreeBranch)
	wt, err := s.Runner.CreateTaskWorktree(ctx, task.RootID, name, branchMode, branch)
	if err != nil {
		return task, err
	}
	now := time.Now().UTC()
	task.WorktreeRootID = wt.RootID
	task.WorktreePath = wt.Path
	task.AuxFlags.SessionError = ""
	task.UpdatedAt = now
	if err := store.UpdateTask(ctx, task); err != nil {
		return task, err
	}
	return task, nil
}

func renderWorktreeName(tpl string, task Task) string {
	name := strings.TrimSpace(tpl)
	if name == "" {
		name = "task-{task_number}"
	}
	replacements := map[string]string{
		"task_id":       task.ID,
		"task_number":   strconv.Itoa(task.TaskNumber),
		"root_id":       task.RootID,
		"template_name": task.TaskTemplateName,
		"task_name":     task.Name,
	}
	for key, value := range replacements {
		name = strings.ReplaceAll(name, "{"+key+"}", value)
	}
	name = strings.TrimSpace(name)
	if name == "" || name == "task-0" {
		name = filepath.Base(task.ID)
	}
	return name
}

func normalizeTaskWorktreeBranch(mode, branch string) (string, string) {
	mode = strings.TrimSpace(mode)
	branch = strings.TrimSpace(branch)
	if mode != "existing" {
		return "new", ""
	}
	if branch == "" {
		return "new", ""
	}
	return "existing", branch
}

func (s *Service) executeTask(ctx context.Context, rootID, taskID string) error {
	store, task, err := s.loadForMove(ctx, rootID, taskID)
	if err != nil {
		return err
	}
	for {
		if task.Status == StatusPaused || isTerminalStatus(task.Status) {
			return nil
		}
		if task.CurrentStageIndex < 0 || task.CurrentStageIndex >= len(task.Stages) {
			// 流水被改短（段被删）：停在等待用户，不再静默完成。
			return s.waitForUserSimpl(ctx, store, task, "stage list truncated")
		}
		stage := task.Stages[task.CurrentStageIndex]
		run, err := store.LatestStageRun(ctx, task.ID, task.CurrentStageIndex)
		if err != nil {
			return err
		}
		if stage.Role == RoleUser {
			if run.Status == StageStatusApproved || run.Status == StageStatusSuccess {
				if task.CurrentStageIndex == len(task.Stages)-1 {
					return s.finishTask(ctx, store, task, StatusSuccess, "completed", "")
				}
				detail, err := s.moveTo(ctx, store, task, task.CurrentStageIndex+1, "auto_advanced", "", "")
				if err != nil {
					return err
				}
				task = detail.Task
				continue
			}
			return s.waitForUser(ctx, store, task, run, "user_input_required")
		}
		if stage.Role != RoleAgent {
			return s.failTask(ctx, store, task, run.ID, fmt.Errorf("unsupported stage role %q", stage.Role))
		}
		if run.Status == StageStatusSuccess {
			if stage.AutoAdvance {
				if task.CurrentStageIndex == len(task.Stages)-1 {
					return s.finishTask(ctx, store, task, StatusSuccess, "completed", "")
				}
				detail, err := s.moveTo(ctx, store, task, task.CurrentStageIndex+1, "auto_advanced", "", "")
				if err != nil {
					return err
				}
				task = detail.Task
				continue
			}
			return s.waitForUser(ctx, store, task, run, "agent_stage_done")
		}
		if run.Status == StageStatusWaitingUser {
			return nil
		}
		if task.CreateWorktree && strings.TrimSpace(task.WorktreePath) == "" {
			updated, werr := s.ensureTaskWorktree(ctx, store, task)
			if werr != nil {
				return s.failTask(ctx, store, task, run.ID, werr)
			}
			task = updated
		}
		if err := s.runAgentStage(ctx, store, task, stage, run); err != nil {
			if errors.Is(err, errStopTaskExecution) {
				return nil
			}
			return err
		}
		task, err = store.GetTask(ctx, task.ID)
		if err != nil {
			return err
		}
	}
}

// waitForUserSimpl 在尚未创建 StageRun 时把任务置于等待用户。
func (s *Service) waitForUserSimpl(ctx context.Context, store *TaskStore, task Task, reason string) error {
	now := time.Now().UTC()
	task.Status = StatusWaitingUser
	task.AuxFlags.SessionError = strings.TrimSpace(reason)
	task.UpdatedAt = now
	if err := store.UpdateTask(ctx, task); err != nil {
		return err
	}
	_ = store.AddEvent(ctx, TaskEvent{
		ID:        newID("event"),
		TaskID:    task.ID,
		Type:      "waiting_user",
		Payload:   eventPayload(map[string]any{"reason": strings.TrimSpace(reason)}),
		CreatedAt: now,
	})
	if detail, err := store.GetDetail(ctx, task.ID); err == nil && s.Runner != nil {
		s.Runner.TaskUpdated(task.RootID, detail)
	}
	return nil
}

func (s *Service) runAgentStage(ctx context.Context, store *TaskStore, task Task, stage StageTemplate, run StageRun) error {
	if s.Runner == nil {
		return errors.New("task runner not configured")
	}
	if strings.TrimSpace(stage.Agent) == "" {
		return s.failTask(ctx, store, task, run.ID, errors.New("agent stage requires agent"))
	}
	now := time.Now().UTC()
	values := s.promptValues(ctx, store, task, stage, run)
	prompt := BuildAgentPrompt(stage.PromptTemplate, values, TaskControlPromptContext{
		RootID:            task.RootID,
		TaskNumber:        task.TaskNumber,
		CurrentStageIndex: strconv.Itoa(task.CurrentStageIndex),
		CurrentStageName:  stage.Name,
		Enabled:           stage.AgentCanControlStage,
	})
	runtimeRootPath := strings.TrimSpace(task.WorktreePath)
	sessionKey, err := s.Runner.EnsureAgentSession(ctx, AgentStageExecution{
		RootID:          task.RootID,
		RuntimeRootPath: runtimeRootPath,
		Task:            task,
		Stage:           stage,
		Run:             run,
		Prompt:          prompt,
	})
	if err != nil {
		return s.failTask(ctx, store, task, run.ID, err)
	}
	if (strings.TrimSpace(stage.SessionReusePolicy) == "" || stage.SessionReusePolicy == SessionReuseTaskMain) && strings.TrimSpace(task.MainSessionKey) == "" {
		task.MainSessionKey = sessionKey
	}
	run.Status = StageStatusRunning
	run.SessionKey = sessionKey
	run.RenderedPrompt = prompt
	run.StartedAt = now.Format(time.RFC3339Nano)
	task.Status = StatusRunning
	task.AuxFlags = TaskAuxFlags{}
	task.UpdatedAt = now
	if err := store.UpdateTaskAndStageRun(ctx, task, run, TaskEvent{
		ID:         newID("event"),
		TaskID:     task.ID,
		StageRunID: run.ID,
		Type:       "stage_started",
		Payload:    eventPayload(map[string]any{"stage_index": run.StageIndex}),
		CreatedAt:  now,
	}); err != nil {
		return err
	}
	if detail, err := store.GetDetail(ctx, task.ID); err == nil {
		s.Runner.TaskUpdated(task.RootID, detail)
	}
	if err := s.Runner.RunAgentStage(ctx, AgentStageExecution{
		RootID:          task.RootID,
		RuntimeRootPath: runtimeRootPath,
		Task:            task,
		Stage:           stage,
		Run:             run,
		Prompt:          prompt,
	}); err != nil {
		log.Printf("[kanban] agent_stage.session_error root=%s task=%s run=%s err=%v", task.RootID, task.ID, run.ID, err)
		message := strings.TrimSpace(err.Error())
		now = time.Now().UTC()
		task.Status = StatusWaitingUser
		task.AuxFlags.SessionError = message
		task.UpdatedAt = now
		run.Status = StageStatusFail
		run.FinishedAt = now.Format(time.RFC3339Nano)
		if updateErr := store.UpdateTaskAndStageRun(ctx, task, run, TaskEvent{
			ID:         newID("event"),
			TaskID:     task.ID,
			StageRunID: run.ID,
			Type:       "agent_session_error",
			Payload:    eventPayload(map[string]any{"message": message}),
			CreatedAt:  now,
		}); updateErr != nil {
			return updateErr
		}
		if detail, detailErr := store.GetDetail(ctx, task.ID); detailErr == nil {
			s.Runner.TaskUpdated(task.RootID, detail)
		}
		return errStopTaskExecution
	}
	task, err = store.GetTask(ctx, task.ID)
	if err != nil {
		return err
	}
	run, err = store.LatestStageRun(ctx, task.ID, task.CurrentStageIndex)
	if err != nil {
		return err
	}
	if run.Status == StageStatusWaitingUser || task.Status == StatusWaitingUser || task.Status == StatusPaused || isTerminalStatus(task.Status) {
		return nil
	}
	now = time.Now().UTC()
	run.Status = StageStatusSuccess
	run.FinishedAt = now.Format(time.RFC3339Nano)
	task.UpdatedAt = now
	if err := store.UpdateTaskAndStageRun(ctx, task, run, TaskEvent{
		ID:         newID("event"),
		TaskID:     task.ID,
		StageRunID: run.ID,
		Type:       "stage_succeeded",
		Payload:    eventPayload(map[string]any{"stage_index": run.StageIndex}),
		CreatedAt:  now,
	}); err != nil {
		return err
	}
	if detail, err := store.GetDetail(ctx, task.ID); err == nil {
		s.Runner.TaskUpdated(task.RootID, detail)
	}
	return nil
}

func (s *Service) promptValues(ctx context.Context, store *TaskStore, task Task, stage StageTemplate, run StageRun) map[string]string {
	previousInput := strings.TrimSpace(run.Input)
	if previousInput == "" && run.StageIndex > 0 {
		if previous, err := store.LatestStageRun(ctx, task.ID, run.StageIndex-1); err == nil {
			previousInput = previous.Input
		}
	}
	initialInput := previousInput
	if first, err := store.LatestStageRun(ctx, task.ID, 0); err == nil {
		initialInput = first.Input
	}
	return map[string]string{
		"previous_input":     previousInput,
		"task_initial_input": initialInput,
		"task_number":        strconv.Itoa(task.TaskNumber),
	}
}

func (s *Service) waitForUser(ctx context.Context, store *TaskStore, task Task, run StageRun, reason string) error {
	if task.Status == StatusWaitingUser && run.Status == StageStatusWaitingUser {
		return nil
	}
	now := time.Now().UTC()
	task.Status = StatusWaitingUser
	task.UpdatedAt = now
	if run.Status != StageStatusApproved && run.Status != StageStatusSuccess {
		run.Status = StageStatusWaitingUser
	}
	event := TaskEvent{
		ID:         newID("event"),
		TaskID:     task.ID,
		StageRunID: run.ID,
		Type:       "waiting_user",
		Payload:    eventPayload(map[string]any{"reason": reason}),
		CreatedAt:  now,
	}
	if err := store.UpdateTaskAndStageRun(ctx, task, run, event); err != nil {
		return err
	}
	if detail, err := store.GetDetail(ctx, task.ID); err == nil && s.Runner != nil {
		s.Runner.TaskUpdated(task.RootID, detail)
	}
	return nil
}

func (s *Service) finishTask(ctx context.Context, store *TaskStore, task Task, status, eventType, reason string) error {
	now := time.Now().UTC()
	task.Status = status
	task.SchedulerAdmitted = false
	task.UpdatedAt = now
	task.CompletedAt = now.Format(time.RFC3339Nano)
	if err := store.UpdateTask(ctx, task); err != nil {
		return err
	}
	_ = store.AddEvent(ctx, TaskEvent{
		ID:        newID("event"),
		TaskID:    task.ID,
		Type:      eventType,
		Payload:   eventPayload(map[string]any{"reason": strings.TrimSpace(reason)}),
		CreatedAt: now,
	})
	if detail, err := store.GetDetail(ctx, task.ID); err == nil && s.Runner != nil {
		s.Runner.TaskUpdated(task.RootID, detail)
	}
	return nil
}

func (s *Service) failTask(ctx context.Context, store *TaskStore, task Task, runID string, err error) error {
	reason := ""
	if err != nil {
		reason = err.Error()
	}
	if updateErr := s.recordTaskError(ctx, store, task, runID, reason); updateErr != nil {
		return updateErr
	}
	return err
}

func (s *Service) recordTaskError(ctx context.Context, store *TaskStore, task Task, runID, message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil
	}
	current, err := store.GetTask(ctx, task.ID)
	if err != nil {
		return err
	}
	current.AuxFlags.SessionError = message
	current.UpdatedAt = time.Now().UTC()
	if err := store.UpdateTask(ctx, current); err != nil {
		return err
	}
	_ = store.AddEvent(ctx, TaskEvent{
		ID:         newID("event"),
		TaskID:     current.ID,
		StageRunID: strings.TrimSpace(runID),
		Type:       "task_error",
		Payload:    eventPayload(map[string]any{"message": message}),
		CreatedAt:  time.Now().UTC(),
	})
	if detail, err := store.GetDetail(ctx, current.ID); err == nil && s.Runner != nil {
		s.Runner.TaskUpdated(current.RootID, detail)
	}
	return nil
}

func (s *Service) moveRelative(ctx context.Context, in MoveInput, delta int, eventType, previousRunStatus string) (TaskDetail, error) {
	store, task, err := s.loadForMove(ctx, in.RootID, in.TaskID)
	if err != nil {
		return TaskDetail{}, err
	}
	target := task.CurrentStageIndex + delta
	if target < 0 || target >= len(task.Stages) {
		return TaskDetail{}, errors.New("target stage out of range")
	}
	if task.CurrentStageIndex < 0 || task.CurrentStageIndex >= len(task.Stages) {
		return TaskDetail{}, errors.New("current stage out of range")
	}
	latest, latestErr := store.LatestStageRun(ctx, task.ID, task.CurrentStageIndex)
	if latestErr != nil {
		return TaskDetail{}, latestErr
	}
	if delta > 0 && latest.Status == StageStatusRunning {
		return TaskDetail{}, errors.New("current stage is running")
	}
	if delta > 0 && stageRequiresCurrentInput(task.Stages[target], task.CurrentStageIndex) && strings.TrimSpace(latest.Input) == "" {
		return TaskDetail{}, errors.New("current stage input required")
	}
	if delta > 0 && task.CreateWorktree && strings.TrimSpace(task.WorktreePath) == "" {
		updated, err := s.ensureTaskWorktree(ctx, store, task)
		if err != nil {
			if recordErr := s.recordTaskError(ctx, store, task, latest.ID, err.Error()); recordErr != nil {
				return TaskDetail{}, recordErr
			}
			return TaskDetail{}, err
		}
		task = updated
	}
	if previousRunStatus != "" {
		_ = store.UpdateStageRunStatus(ctx, latest.ID, previousRunStatus)
	}
	return s.moveTo(ctx, store, task, target, eventType, previousRunStatus, in.Reason)
}

func stageRequiresCurrentInput(stage StageTemplate, currentStageIndex int) bool {
	prompt := stage.PromptTemplate
	return strings.Contains(prompt, "{previous_input}") ||
		(currentStageIndex == 0 && strings.Contains(prompt, "{task_initial_input}"))
}

func (s *Service) moveTo(ctx context.Context, store *TaskStore, task Task, target int, eventType, previousRunStatus, reason string) (TaskDetail, error) {
	now := time.Now().UTC()
	stage := task.Stages[target]
	status := StatusWaitingUser
	if stage.Role == RoleAgent {
		status = StatusRunning
	}
	task.CurrentStageIndex = target
	task.Status = status
	task.AuxFlags.SessionError = ""
	task.UpdatedAt = now
	run := StageRun{
		ID:         newID("run"),
		TaskID:     task.ID,
		StageIndex: target,
		StageName:  stage.Name,
		Role:       stage.Role,
		Status:     StageStatusPending,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if stage.Role == RoleUser {
		run.Status = StageStatusWaitingUser
	}
	event := TaskEvent{
		ID:         newID("event"),
		TaskID:     task.ID,
		StageRunID: run.ID,
		Type:       eventType,
		Payload:    eventPayload(map[string]any{"reason": strings.TrimSpace(reason), "stage_index": target}),
		CreatedAt:  now,
	}
	if err := store.MoveTask(ctx, task, run, event); err != nil {
		return TaskDetail{}, err
	}
	return store.GetDetail(ctx, task.ID)
}

// setTaskStatus 改任务状态（Pause/Resume/Cancel/Fail 共用）。
// 终态任务不再接受任何状态改写：已归档的任务不能被 Resume/Pause 复活。
func (s *Service) setTaskStatus(ctx context.Context, rootID, taskID, status, eventType, reason string, terminal bool) (TaskDetail, error) {
	store, err := s.taskStore(rootID)
	if err != nil {
		return TaskDetail{}, err
	}
	if current, getErr := store.GetTask(ctx, taskID); getErr == nil && isTerminalStatus(current.Status) {
		return store.GetDetail(ctx, current.ID)
	}
	if err := store.UpdateTaskStatus(ctx, taskID, status, nil, terminal); err != nil {
		return TaskDetail{}, err
	}
	_ = store.AddEvent(ctx, TaskEvent{
		ID:        newID("event"),
		TaskID:    strings.TrimSpace(taskID),
		Type:      eventType,
		Payload:   eventPayload(map[string]any{"reason": strings.TrimSpace(reason)}),
		CreatedAt: time.Now().UTC(),
	})
	return store.GetDetail(ctx, taskID)
}

// ensureServiceTask 读取任务并回填旧任务快照（详见 loadForMove）；当前任务字段仅服务端推动（如追加段落）。
func (s *Service) ensureServiceTask(ctx context.Context, rootID string, store *TaskStore, taskID string) (Task, error) {
	store, task, err := s.loadForMove(ctx, rootID, strings.TrimSpace(taskID))
	if err != nil {
		return Task{}, err
	}
	return task, nil
}

func defaultStageName(task Task, index int) string {
	if index < 0 {
		index = 0
	}
	name := strings.TrimSpace(fmt.Sprintf("阶段 %d", index+1))
	return name
}

func (s *Service) loadForMove(ctx context.Context, rootID, taskID string) (*TaskStore, Task, error) {
	store, err := s.taskStore(rootID)
	if err != nil {
		return nil, Task{}, err
	}
	task, err := store.GetTask(ctx, taskID)
	if err != nil {
		return nil, Task{}, err
	}
	// 兼容：老任务没存任务流水 → 惰性从预设拷贝一次并落盘；此后与模板解耦。
	if len(task.Stages) == 0 && strings.TrimSpace(task.TaskTemplateID) != "" {
		if tmpl, err := s.Templates.GetTaskTemplate(task.TaskTemplateID); err == nil {
			stages := []StageTemplate{}
			for _, ts := range tmpl.Stages {
				stages = append(stages, normalizeStageTemplate(ts.Snapshot))
			}
			task.Stages = stages
			task.UpdatedAt = time.Now().UTC()
			_ = store.UpdateTask(ctx, task)
		}
	}
	return store, task, nil
}

func (s *Service) taskStore(rootID string) (*TaskStore, error) {
	rootID = strings.TrimSpace(rootID)
	if rootID == "" {
		return nil, errors.New("root_id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stores == nil {
		s.stores = map[string]*TaskStore{}
	}
	if store := s.stores[rootID]; store != nil {
		return store, nil
	}
	if s.Roots == nil {
		return nil, errors.New("root provider not configured")
	}
	root, err := s.Roots.GetRoot(rootID)
	if err != nil {
		return nil, err
	}
	store, err := NewTaskStore(root)
	if err != nil {
		return nil, err
	}
	s.stores[rootID] = store
	return store, nil
}

func eventPayload(value map[string]any) string {
	payload, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(payload)
}

type TaskControlPromptContext struct {
	RootID            string
	TaskNumber        int
	CurrentStageIndex string
	CurrentStageName  string
	Enabled           bool
}

func BuildAgentPrompt(template string, values map[string]string, control TaskControlPromptContext) string {
	out := template
	for key, value := range values {
		out = strings.ReplaceAll(out, "{"+key+"}", value)
	}
	if control.Enabled {
		taskNumber := strconv.Itoa(control.TaskNumber)
		out += fmt.Sprintf("\n\nTask control context:\n- root_id: %s\n- task_number: %s\n- current_stage_index: %s\n- current_stage_name: %s\n\nBefore changing the task stage, inspect the current task state.\n\nmindfs %s -task %s\nmindfs %s -task %s -next\nmindfs %s -task %s -prev",
			control.RootID, taskNumber, control.CurrentStageIndex, control.CurrentStageName,
			control.RootID, taskNumber, control.RootID, taskNumber, control.RootID, taskNumber)
	}
	return out
}
