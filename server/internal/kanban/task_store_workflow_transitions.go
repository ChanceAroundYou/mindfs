package kanban

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// WorkflowNodeTransition is one node reaching a terminal state without an
// external agent turn (condition, notify, skip, or a config error). The NodeRun,
// the run rollup (states/outputs/routes) and the ledger event commit in one
// transaction: condition routes in particular must never be persisted apart
// from the condition's terminal event, or a crash would let both branches run.
type WorkflowNodeTransition struct {
	NodeRun      NodeRun
	EventType    string
	EventPayload map[string]any
	// Route is the condition route selected by this transition.
	Route string
	// Activity is an optional correlated activity written in the same tx.
	Activity *TaskActivity
}

// CommitWorkflowNodeTransition writes a terminal internal-node transition. The
// NodeRun unique key makes it idempotent: a lost insert returns the persisted
// row with won=false and changes nothing else.
func (s *TaskStore) CommitWorkflowNodeTransition(ctx context.Context, run *WorkflowRun, tr WorkflowNodeTransition) (NodeRun, bool, error) {
	if run == nil {
		return NodeRun{}, false, errors.New("workflow run required")
	}
	n := tr.NodeRun
	if strings.TrimSpace(n.ID) == "" || n.RunID != run.ID || strings.TrimSpace(n.NodeID) == "" {
		return NodeRun{}, false, fmt.Errorf("node run %q does not belong to run %s", n.ID, run.ID)
	}
	if n.Attempt < 1 {
		return NodeRun{}, false, errors.New("workflow node run attempt must be >= 1")
	}
	switch n.Status {
	case NodeRunSuccess, NodeRunFail, NodeRunWaitingUser, NodeRunUnknown, NodeRunSkipped:
	default:
		return NodeRun{}, false, fmt.Errorf("invalid internal workflow node terminal status %q", n.Status)
	}
	if strings.TrimSpace(tr.EventType) == "" {
		return NodeRun{}, false, errors.New("workflow transition event type required")
	}
	now := time.Now().UTC()
	if n.StartedAt.IsZero() {
		n.StartedAt = now
	}
	if n.FinishedAt.IsZero() {
		n.FinishedAt = now
	}
	input, err := marshalWorkflowJSON(n.Input)
	if err != nil {
		return NodeRun{}, false, err
	}
	output, err := marshalWorkflowJSON(n.Output)
	if err != nil {
		return NodeRun{}, false, err
	}
	stamp := now.Format(time.RFC3339Nano)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return NodeRun{}, false, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`INSERT INTO workflow_node_runs(id,run_id,node_id,loop_path,iteration,attempt,agent,harness,model,session_key,status,input_json,output_json,started_at,finished_at,prev_attempt_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(run_id,node_id,loop_path,iteration,attempt) DO NOTHING`,
		n.ID, n.RunID, n.NodeID, n.LoopPath, n.Iteration, n.Attempt, n.Agent, n.Harness, n.Model, n.SessionKey, n.Status, input, output,
		n.StartedAt.UTC().Format(time.RFC3339Nano), n.FinishedAt.UTC().Format(time.RFC3339Nano), n.PrevAttemptID, stamp, stamp)
	if err != nil {
		return NodeRun{}, false, err
	}
	inserted, err := res.RowsAffected()
	if err != nil {
		return NodeRun{}, false, err
	}
	if inserted != 1 {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			return NodeRun{}, false, err
		}
		existing, err := s.WorkflowNodeRunByKey(ctx, run.ID, n.NodeID, n.LoopPath, n.Iteration, n.Attempt)
		if err != nil {
			return NodeRun{}, false, err
		}
		return existing, false, nil
	}

	states := cloneWorkflowStringMap(run.NodeStates)
	outputs := cloneWorkflowOutputMap(run.NodeOutputs)
	routes := cloneWorkflowStringMap(run.SelectedRoutes)
	// Inner loop nodes are per-iteration facts. They must never land in the
	// run-level projection: a second iteration would overwrite the first, and
	// a top-level node with the same id would be shadowed. Their history is the
	// NodeRun rows (loop_path/iteration).
	if n.LoopPath == "" {
		states[n.NodeID] = n.Status
		if n.Output != nil {
			outputs[n.NodeID] = n.Output
		}
		if tr.Route != "" {
			routes[n.NodeID] = tr.Route
		}
	} else if n.Output == nil {
		n.Output = map[string]any{}
	}
	statesJSON, err := marshalWorkflowJSON(states)
	if err != nil {
		return NodeRun{}, false, err
	}
	outputsJSON, err := marshalWorkflowJSON(outputs)
	if err != nil {
		return NodeRun{}, false, err
	}
	routesJSON, err := marshalWorkflowJSON(routes)
	if err != nil {
		return NodeRun{}, false, err
	}
	updated, err := tx.ExecContext(ctx,
		`UPDATE workflow_runs SET revision=revision+1,node_states_json=?,node_outputs_json=?,selected_routes_json=?,updated_at=? WHERE id=? AND revision=?`,
		statesJSON, outputsJSON, routesJSON, stamp, run.ID, run.Revision)
	if err != nil {
		return NodeRun{}, false, err
	}
	changed, err := updated.RowsAffected()
	if err != nil {
		return NodeRun{}, false, err
	}
	if changed != 1 {
		return NodeRun{}, false, ErrRunConflict
	}

	if act := tr.Activity; act != nil {
		act.TaskID = run.TaskID
		if act.RunID == "" {
			act.RunID = run.ID
		}
		if act.NodeRunID == "" {
			act.NodeRunID = n.ID
		}
		if act.CreatedAt.IsZero() {
			act.CreatedAt = now
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO task_activities (id, task_id, type, author, role, body, workflow_choice, workflow_version_id, run_id, node_run_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(task_id,id) DO NOTHING`,
			act.ID, act.TaskID, act.Type, act.Author, act.Role, act.Body, act.WorkflowChoice, act.WorkflowVersionID, act.RunID, act.NodeRunID, act.CreatedAt.Format(time.RFC3339Nano)); err != nil {
			return NodeRun{}, false, err
		}
	}

	payload := tr.EventPayload
	if payload == nil {
		payload = map[string]any{"status": n.Status}
	} else if _, ok := payload["status"]; !ok {
		payload["status"] = n.Status
	}
	if tr.Route != "" {
		payload["route"] = tr.Route
	}
	if _, err := appendWorkflowRunEventTx(ctx, tx, WorkflowRunEvent{
		RunID: run.ID, Type: tr.EventType, NodeID: n.NodeID, NodeRunID: n.ID,
		Attempt: n.Attempt, Payload: payload, CreatedAt: now,
	}); err != nil {
		return NodeRun{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return NodeRun{}, false, err
	}

	run.NodeStates = states
	run.NodeOutputs = outputs
	run.SelectedRoutes = routes
	run.Revision++
	run.UpdatedAt = now
	return n, true, nil
}

func cloneWorkflowStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneWorkflowOutputMap(in map[string]map[string]any) map[string]map[string]any {
	out := make(map[string]map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
