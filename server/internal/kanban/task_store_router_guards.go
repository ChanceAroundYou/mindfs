package kanban

import (
	"context"
	"errors"
	"fmt"
	"time"
)

func routeTaskStatusError(status string) error {
	if status == StatusPaused {
		return ErrTaskPaused
	}
	if isTerminalStatus(status) {
		return ErrTaskClosed
	}
	return nil
}

// checkRouteDirectGate checks the current workflow at direct dispatch, after the model turn.
func (s *TaskStore) checkRouteDirectGate(ctx context.Context, taskID string) error {
	engine, err := s.TaskEngine(ctx, taskID)
	if err != nil || engine.Engine != EngineWorkflow {
		return err
	}
	run, err := s.WorkflowRunForTask(ctx, taskID)
	if errors.Is(err, ErrWorkflowRunNotFound) && engine.WorkflowRunID == "" {
		return nil
	}
	if err != nil {
		return err
	}
	if gate, waiting := waitingHumanGate(run); waiting {
		return fmt.Errorf("%w（%s）", ErrWorkflowWaitingApproval, gate)
	}
	return nil
}

// A reply may already be visible when its result projection runs. Keep a pause
// or completion that committed in that interval authoritative for the task card.
// The status list must stay in sync with routeTaskStatusError above.
func (s *TaskStore) recordReplySummaryIfActive(ctx context.Context, taskID, body string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE tasks SET result_summary=?, updated_at=? WHERE id=? AND status NOT IN (?,?,?,?)`,
		truncateSummary(body), s.now().UTC().Format(time.RFC3339Nano), taskID,
		StatusPaused, StatusSuccess, StatusFail, StatusCancelled)
	return err
}
