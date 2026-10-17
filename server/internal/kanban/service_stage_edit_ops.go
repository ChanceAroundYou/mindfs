package kanban

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

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
	if task.Status == StatusWaitingUser && strings.TrimSpace(stage.PromptTemplate) != "" {
		// 准入检查放在**最前面**：下面已经要把当前段批成 approved、要把新段推进上去。
		// 收尾期间放进来做完这些再返回，留下的是「段已 approved、没有执行体」——
		// 和「永远 running」是同一种卡死，只是换了张脸。
		release, admErr := s.beginRunAdmission(in.RootID, task.ID)
		if admErr != nil {
			return store.GetDetail(ctx, task.ID)
		}
		defer release()
		stage.Name = defaultStageName(task, len(task.Stages))
		task.Stages = append(task.Stages, stage)
		if latest, runErr := store.LatestStageRun(ctx, task.ID, task.CurrentStageIndex); runErr == nil {
			// 同 moveRelative：agent 段没走完时，追加一句评论不能当成「这段已完成」。
			// user 段的 waiting_user 是在等输入，补评论正是答案，照常批准。
			//
			// ByRequest 是另一条线：发起者不是「一句评论」而是人/系统的明确动作（收尾段），
			// 规则换成 canLeaveStageOnRequest —— agent 段的 waiting_user 正该由人来判
			// 「就这样，往下走」。见 AddStageInput.ByRequest。
			canLeave := canAdvanceFromStage
			if in.ByRequest {
				canLeave = canLeaveStageOnRequest
			}
			if !canLeave(task.Stages[task.CurrentStageIndex].Role, latest.Status) {
				return TaskDetail{}, fmt.Errorf(
					"current stage is %s: 这一段没走完，追加评论不能替代完成本段，重跑本段或改任务后再试",
					latest.Status,
				)
			}
			if latest.Status != StageStatusSuccess {
				_ = store.UpdateStageRunStatus(ctx, latest.ID, StageStatusApproved)
			}
		}
		detail, err := s.moveTo(ctx, store, task, len(task.Stages)-1, "user_approved", StageStatusApproved, "comment")
		if err != nil {
			return TaskDetail{}, err
		}
		if runErr := s.RunTask(detail.Task.RootID, detail.Task.ID); runErr != nil {
			return TaskDetail{}, runErr
		}
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

// RemoveStage 删除任务流水里尚未执行的一段。
// index 0 是任务输入段，不可删；当前指针所在段不可删（执行体正对着它）；
// 产生过 StageRun 的段也不可删（要改走 UpdateStage）。
func (s *Service) RemoveStage(ctx context.Context, in RemoveStageInput) (TaskDetail, error) {
	store, err := s.taskStore(in.RootID)
	if err != nil {
		return TaskDetail{}, err
	}
	task, err := s.ensureServiceTask(ctx, in.RootID, store, strings.TrimSpace(in.TaskID))
	if err != nil {
		return TaskDetail{}, err
	}
	idx := in.Index
	if idx <= 0 || idx >= len(task.Stages) {
		return TaskDetail{}, errors.New("stage_index out of range")
	}
	if idx == task.CurrentStageIndex {
		return TaskDetail{}, errors.New("current stage cannot be removed")
	}
	runs, err := store.ListStageRuns(ctx, task.ID)
	if err != nil {
		return TaskDetail{}, err
	}
	for _, run := range runs {
		if run.StageIndex == idx && run.Status != StageStatusPending {
			return TaskDetail{}, errors.New("stage already executed")
		}
	}

	task.Stages = append(task.Stages[:idx], task.Stages[idx+1:]...)
	if idx < task.CurrentStageIndex {
		task.CurrentStageIndex--
	}
	if task.CurrentStageIndex >= len(task.Stages) {
		task.CurrentStageIndex = len(task.Stages) - 1
	}
	if task.CurrentStageIndex < 0 {
		task.CurrentStageIndex = 0
	}
	now := time.Now().UTC()
	task.AuxFlags.SessionError = ""
	task.UpdatedAt = now
	if err := store.UpdateTask(ctx, task); err != nil {
		return TaskDetail{}, err
	}
	_ = store.AddEvent(ctx, TaskEvent{
		ID:        newID("event"),
		TaskID:    task.ID,
		Type:      "stage_removed",
		Payload:   eventPayload(map[string]any{"stage_index": idx}),
		CreatedAt: now,
	})
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
	release, admErr := s.beginRunAdmission(in.RootID, task.ID)
	if admErr != nil {
		return store.GetDetail(ctx, task.ID)
	}
	defer release()
	detail, err := s.moveTo(ctx, store, task, in.StageIndex, "stage_rerun", "", in.Reason)
	if err != nil {
		return TaskDetail{}, err
	}
	if !isTerminalStatus(detail.Task.Status) {
		if runErr := s.RunTask(detail.Task.RootID, detail.Task.ID); runErr != nil {
			return TaskDetail{}, runErr
		}
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
		// 建过树就不许再改 create_worktree —— 已经跑在那个目录里了。判据用
		// 「路径字段非空」而不是「目录还在」：目录被删是失效状态，该走的是重建入口，
		// 不该让用户借此把任务改成一个和既有历史矛盾的状态。
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
