package kanban

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Runtime transitions are deliberately separate from FinishWorkflowNodeRun:
// cancellation must be able to stop a row that never started (queued), and
// restart recovery must put a started-but-not-delivered row back into queued
// without inventing a terminal outcome. Every transition is one transaction
// with its typed ledger event.

func (s *TaskStore) WorkflowNodeRunByID(ctx context.Context, id string) (NodeRun, error) {
	node, err := scanWorkflowNodeRun(s.db.QueryRowContext(ctx,
		`SELECT `+workflowNodeRunColumns+` FROM workflow_node_runs WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return NodeRun{}, ErrWorkflowNodeRunNotFound
	}
	if err != nil {
		return NodeRun{}, err
	}
	return *node, nil
}

// ListOpenWorkflowNodeRuns returns rows that can still produce a turn or need a
// decision: queued claims that have not started and running turns. Terminal
// rows are history and are never rewritten by cancellation.
func (s *TaskStore) ListOpenWorkflowNodeRuns(ctx context.Context, runID string) ([]NodeRun, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+workflowNodeRunColumns+` FROM workflow_node_runs WHERE run_id=? AND status IN (?,?) ORDER BY created_at,id`,
		runID, NodeRunQueued, NodeRunRunning)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NodeRun{}
	for rows.Next() {
		n, err := scanWorkflowNodeRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, rows.Err()
}

func (s *TaskStore) transitionWorkflowNodeRun(ctx context.Context, n NodeRun, to, eventType string, payload map[string]any, resetStart bool) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	outputJSON, err := marshalWorkflowJSON(payload)
	if err != nil {
		return false, err
	}
	// Requeue clears the start stamp because the next attempt has not begun.
	// Cancel/unknown are terminal evidence: they must keep started_at so the
	// record still says when the turn began. Wiping it here made the only
	// trace of an in-flight turn disappear exactly when an operator cancelled.
	var res sql.Result
	if resetStart {
		res, err = tx.ExecContext(ctx,
			`UPDATE workflow_node_runs SET status=?, started_at='', finished_at='', output_json=?, updated_at=? WHERE id=? AND status IN (?,?)`,
			to, outputJSON, stamp, n.ID, NodeRunQueued, NodeRunRunning)
	} else {
		res, err = tx.ExecContext(ctx,
			`UPDATE workflow_node_runs SET status=?, finished_at=?, output_json=?, updated_at=? WHERE id=? AND status IN (?,?)`,
			to, stamp, outputJSON, stamp, n.ID, NodeRunQueued, NodeRunRunning)
	}
	if err != nil {
		return false, err
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if changed != 1 {
		return false, nil
	}
	if _, err := appendWorkflowRunEventTx(ctx, tx, WorkflowRunEvent{
		RunID: n.RunID, Type: eventType, NodeID: n.NodeID, NodeRunID: n.ID,
		Attempt: n.Attempt, Payload: payload, CreatedAt: now,
	}); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// RequeueWorkflowNodeRun is only for evidence-backed restart (queued-not-sent
// or a running row whose session is known resumable). It keeps the session
// binding and attempt identity; the original claim/start events stay in the
// ledger, so the retry is auditable.
func (s *TaskStore) RequeueWorkflowNodeRun(ctx context.Context, n NodeRun, reason string) (bool, error) {
	return s.transitionWorkflowNodeRun(ctx, n, NodeRunQueued, WorkflowEventNodeRequeued,
		map[string]any{"reason": reason}, true)
}

func (s *TaskStore) CancelWorkflowNodeRun(ctx context.Context, n NodeRun, reason string) (bool, error) {
	return s.transitionWorkflowNodeRun(ctx, n, NodeRunCancelled, WorkflowEventNodeCancelled,
		map[string]any{"reason": reason}, false)
}

func (s *TaskStore) MarkWorkflowNodeRunUnknown(ctx context.Context, n NodeRun, reason string) (bool, error) {
	return s.transitionWorkflowNodeRun(ctx, n, NodeRunUnknown, WorkflowEventNodeUnknown,
		map[string]any{"reason": reason}, false)
}

// ErrWorkflowNodeRunNotActionable is a typed refusal for operator verbs. It
// keeps HTTP callers from translating a program bug into a 500.
var ErrWorkflowNodeRunNotActionable = errors.New("workflow node run is not in a state this action accepts")

// CreateRetryWorkflowNodeRun inserts attempt+1 linked to the original row. The
// original row, its output, and any comment bound to it are never touched; the
// retry is a new attempt identity, not a rewrite of history.
func (s *TaskStore) CreateRetryWorkflowNodeRun(ctx context.Context, run *WorkflowRun, original NodeRun) (NodeRun, error) {
	if original.ID == "" || run == nil || original.RunID != run.ID {
		return NodeRun{}, fmt.Errorf("workflow node run %q does not belong to run", original.ID)
	}
	next := NodeRun{
		ID:            workflowNodeRunID(run.ID, original.NodeID, original.LoopPath, original.Iteration, original.Attempt+1),
		RunID:         run.ID,
		NodeID:        original.NodeID,
		LoopPath:      original.LoopPath,
		Iteration:     original.Iteration,
		Attempt:       original.Attempt + 1,
		Agent:         original.Agent,
		Harness:       original.Harness,
		Model:         original.Model,
		Input:         cloneWorkflowOutput(original.Input),
		Status:        NodeRunQueued,
		PrevAttemptID: original.ID,
	}
	claimed, _, err := s.ClaimWorkflowNodeRun(ctx, next)
	if err != nil {
		return NodeRun{}, err
	}
	return claimed, nil
}

func cloneWorkflowOutput(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
