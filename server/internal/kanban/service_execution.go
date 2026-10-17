package kanban

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

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
		if task.CreateWorktree && !worktreeDirUsable(task.WorktreePath) {
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
