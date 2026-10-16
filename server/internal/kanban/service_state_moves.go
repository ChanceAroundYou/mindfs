package kanban

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

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
	if delta > 0 && !canLeaveStageOnRequest(task.Stages[task.CurrentStageIndex].Role, latest.Status) {
		// 走 canLeaveStageOnRequest 而不是 canAdvanceFromStage：到这里的一定是人点的
		// （引擎自动推进走 moveTo），而 agent 段的 waiting_user（没输出 [STAGE-DONE:N]）
		// 正该由人来判「就这样，推进」。canAdvanceFromStage 是留给引擎的那条更严的线。
		return TaskDetail{}, fmt.Errorf(
			"current stage is %s: 这一段没走完，重跑本段或改任务后再试",
			latest.Status,
		)
	}
	if delta > 0 && stageRequiresCurrentInput(task.Stages[target], task.CurrentStageIndex) && strings.TrimSpace(latest.Input) == "" {
		return TaskDetail{}, errors.New("current stage input required")
	}
	if delta > 0 && task.CreateWorktree && !worktreeDirUsable(task.WorktreePath) {
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
	detail, err := store.GetDetail(ctx, task.ID)
	// 换段指针一定要播：卡片上的「当前在第几段 / 什么状态」就挂在这上面。
	// 以前只靠 HTTP handler 事后补，于是 KickPending、handleKanbanTaskBeginFinish
	// 这类非 HTTP 入口推不出去 —— 界面上就停在旧状态。
	if err == nil && s.Runner != nil {
		s.Runner.TaskUpdated(task.RootID, detail)
	}
	return detail, err
}

// setTaskStatus 改任务状态（Pause/Resume/Cancel/Fail 共用）。
// 终态任务不再接受任何状态改写：已归档的任务不能被 Resume/Pause 复活。
func (s *Service) setTaskStatus(ctx context.Context, rootID, taskID, status, eventType, reason string, terminal bool) (TaskDetail, error) {
	store, err := s.taskStore(rootID)
	if err != nil {
		return TaskDetail{}, err
	}
	if current, getErr := store.GetTask(ctx, taskID); getErr == nil && isTerminalStatus(current.Status) {
		// 状态没变（已经是终态了），刻意不播：推一条与现状相同的 task.updated
		// 只会让每个客户端白做一次重渲染。
		return store.GetDetail(ctx, current.ID)
	}
	if err := store.UpdateTaskStatus(ctx, taskID, status, terminal); err != nil {
		return TaskDetail{}, err
	}
	_ = store.AddEvent(ctx, TaskEvent{
		ID:        newID("event"),
		TaskID:    strings.TrimSpace(taskID),
		Type:      eventType,
		Payload:   eventPayload(map[string]any{"reason": strings.TrimSpace(reason)}),
		CreatedAt: time.Now().UTC(),
	})
	detail, err := store.GetDetail(ctx, taskID)
	// Pause/Resume/Cancel/Fail 的界面反馈全靠这条推送。以前不发、只由 HTTP handler
	// 补，于是任何非 HTTP 调用方推不出去，别的客户端看到的还是暂停前的状态。
	if err == nil && s.Runner != nil {
		s.Runner.TaskUpdated(rootID, detail)
	}
	return detail, err
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
