package kanban

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

var ErrWorkflowNodeRunNotFound = errors.New("workflow node run not found on this task node")

const workflowNodeRunColumns = `id,run_id,node_id,loop_path,iteration,attempt,agent,harness,model,session_key,status,input_json,output_json,started_at,finished_at,prev_attempt_id`

func scanWorkflowNodeRun(row interface{ Scan(...any) error }) (*NodeRun, error) {
	var n NodeRun
	var input, output, started, finished string
	if err := row.Scan(&n.ID, &n.RunID, &n.NodeID, &n.LoopPath, &n.Iteration, &n.Attempt, &n.Agent, &n.Harness, &n.Model, &n.SessionKey, &n.Status, &input, &output, &started, &finished, &n.PrevAttemptID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrWorkflowNodeRunNotFound
		}
		return nil, err
	}
	var err error
	if n.Input, err = unmarshalWorkflowJSON[map[string]any](input); err != nil {
		return nil, err
	}
	if n.Output, err = unmarshalWorkflowJSON[map[string]any](output); err != nil {
		return nil, err
	}
	if started != "" {
		n.StartedAt, _ = time.Parse(time.RFC3339Nano, started)
	}
	if finished != "" {
		n.FinishedAt, _ = time.Parse(time.RFC3339Nano, finished)
	}
	return &n, nil
}

// WorkflowNodeRunByKey loads the canonical row for a NodeRun unique key.
func (s *TaskStore) WorkflowNodeRunByKey(ctx context.Context, runID, nodeID, loopPath string, iteration, attempt int) (NodeRun, error) {
	node, err := scanWorkflowNodeRun(s.db.QueryRowContext(ctx,
		`SELECT `+workflowNodeRunColumns+` FROM workflow_node_runs WHERE run_id=? AND node_id=? AND loop_path=? AND iteration=? AND attempt=?`,
		runID, nodeID, loopPath, iteration, attempt))
	if err != nil {
		return NodeRun{}, err
	}
	return *node, nil
}

// ClaimWorkflowNodeRun atomically claims the unique NodeRun key
// (run_id,node_id,loop_path,iteration,attempt). Exactly one concurrent caller
// gets claimed=true; every other caller receives the already-persisted row with
// claimed=false. The claim is a single DB statement and never calls a Runner:
// external turns start only after the caller observes claimed=true.
func (s *TaskStore) ClaimWorkflowNodeRun(ctx context.Context, n NodeRun) (NodeRun, bool, error) {
	if strings.TrimSpace(n.ID) == "" || strings.TrimSpace(n.RunID) == "" || strings.TrimSpace(n.NodeID) == "" {
		return NodeRun{}, false, errors.New("workflow node run id, run id and node id required")
	}
	if n.Attempt < 1 {
		return NodeRun{}, false, errors.New("workflow node run attempt must be >= 1")
	}
	if strings.TrimSpace(n.Status) == "" {
		return NodeRun{}, false, errors.New("workflow node run status required")
	}
	input, err := marshalWorkflowJSON(n.Input)
	if err != nil {
		return NodeRun{}, false, err
	}
	output, err := marshalWorkflowJSON(n.Output)
	if err != nil {
		return NodeRun{}, false, err
	}
	now := time.Now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	started, finished := "", ""
	if !n.StartedAt.IsZero() {
		started = n.StartedAt.UTC().Format(time.RFC3339Nano)
	}
	if !n.FinishedAt.IsZero() {
		finished = n.FinishedAt.UTC().Format(time.RFC3339Nano)
	}
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO workflow_node_runs(id,run_id,node_id,loop_path,iteration,attempt,agent,harness,model,session_key,status,input_json,output_json,started_at,finished_at,prev_attempt_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(run_id,node_id,loop_path,iteration,attempt) DO NOTHING`,
		n.ID, n.RunID, n.NodeID, n.LoopPath, n.Iteration, n.Attempt, n.Agent, n.Harness, n.Model, n.SessionKey, n.Status, input, output, started, finished, n.PrevAttemptID, stamp, stamp)
	if err != nil {
		return NodeRun{}, false, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return NodeRun{}, false, err
	}
	if inserted == 1 {
		return n, true, nil
	}
	existing, err := s.WorkflowNodeRunByKey(ctx, n.RunID, n.NodeID, n.LoopPath, n.Iteration, n.Attempt)
	if err != nil {
		return NodeRun{}, false, err
	}
	return existing, false, nil
}

// LatestWorkflowNodeSessionKey returns the most recent bound session for the
// same run/node/loop path so session_reuse_policy=same_stage can continue it.
// No earlier binding is a normal state, not an error.
func (s *TaskStore) LatestWorkflowNodeSessionKey(ctx context.Context, runID, nodeID, loopPath string) (string, error) {
	var key string
	err := s.db.QueryRowContext(ctx,
		`SELECT session_key FROM workflow_node_runs WHERE run_id=? AND node_id=? AND loop_path=? AND session_key<>'' ORDER BY iteration DESC,attempt DESC LIMIT 1`,
		runID, nodeID, loopPath).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return key, nil
}

func (s *TaskStore) ListWorkflowNodeRuns(ctx context.Context, runID string) ([]NodeRun, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+workflowNodeRunColumns+` FROM workflow_node_runs WHERE run_id=? ORDER BY created_at,node_id,loop_path,iteration,attempt`, runID)
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

func (s *TaskStore) UpsertWorkflowNodeRun(ctx context.Context, n NodeRun) error {
	input, err := marshalWorkflowJSON(n.Input)
	if err != nil {
		return err
	}
	output, err := marshalWorkflowJSON(n.Output)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	started, finished := "", ""
	if !n.StartedAt.IsZero() {
		started = n.StartedAt.UTC().Format(time.RFC3339Nano)
	}
	if !n.FinishedAt.IsZero() {
		finished = n.FinishedAt.UTC().Format(time.RFC3339Nano)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO workflow_node_runs(id,run_id,node_id,loop_path,iteration,attempt,agent,harness,model,session_key,status,input_json,output_json,started_at,finished_at,prev_attempt_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET agent=excluded.agent,harness=excluded.harness,model=excluded.model,session_key=excluded.session_key,status=excluded.status,input_json=excluded.input_json,output_json=excluded.output_json,started_at=excluded.started_at,finished_at=excluded.finished_at,prev_attempt_id=excluded.prev_attempt_id,updated_at=excluded.updated_at`, n.ID, n.RunID, n.NodeID, n.LoopPath, n.Iteration, n.Attempt, n.Agent, n.Harness, n.Model, n.SessionKey, n.Status, input, output, started, finished, n.PrevAttemptID, now, now)
	return err
}
