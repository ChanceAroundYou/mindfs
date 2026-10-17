package kanban

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
)

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
	}) + BuildStageExitContract(task.CurrentStageIndex)
	// 这里是 cwd 真正被取用的地方，所以独立判一次，不依赖上游那几个 gate 有没有跑过
	// （rerun / resume 等路径不一定先过 ensureTaskWorktree）。失效就明确报错，别把
	// 路径流到 agent 启动层再报 chdir 错 —— 那个错看不出是 worktree 被删了。
	if p := strings.TrimSpace(task.WorktreePath); p != "" && !worktreeDirUsable(p) {
		return s.failTask(ctx, store, task, run.ID, fmt.Errorf(
			"worktree 目录已不存在（%s），点「重建 worktree」恢复后再执行", p))
	}
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
	result, err := s.Runner.RunAgentStage(ctx, AgentStageExecution{
		RootID:          task.RootID,
		RuntimeRootPath: runtimeRootPath,
		Task:            task,
		Stage:           stage,
		Run:             run,
		Prompt:          prompt,
	})
	if err != nil {
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
	// agent 没显式回报完成就不许算成功：Blocked（它自己说受阻）与 Silent（压根没
	// 回报）都停在 waiting_user，错误摆到卡面上等人处理。AutoAdvance 只看 success，
	// 停在这里就等于下一段不会被自动植进来。
	if result.Outcome != StageOutcomeDone {
		reason := strings.TrimSpace(result.Reason)
		if reason == "" {
			reason = "本段未回报完成：agent 既没输出 [" + stageDoneTag + ":N]，也没说受阻"
		}
		now = time.Now().UTC()
		task.Status = StatusWaitingUser
		task.AuxFlags.SessionError = reason
		task.UpdatedAt = now
		run.Status = StageStatusWaitingUser
		run.FinishedAt = now.Format(time.RFC3339Nano)
		if err := store.UpdateTaskAndStageRun(ctx, task, run, TaskEvent{
			ID:         newID("event"),
			TaskID:     task.ID,
			StageRunID: run.ID,
			Type:       "agent_stage_not_done",
			Payload:    eventPayload(map[string]any{"stage_index": run.StageIndex, "reason": reason}),
			CreatedAt:  now,
		}); err != nil {
			return err
		}
		if detail, err := store.GetDetail(ctx, task.ID); err == nil {
			s.Runner.TaskUpdated(task.RootID, detail)
		}
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
