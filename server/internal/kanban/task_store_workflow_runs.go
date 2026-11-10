package kanban

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrWorkflowRunNotFound = errors.New("workflow run not found on this task node")

// migrateWorkflowRuns creates runtime snapshots and NodeRun attempts in the task-node SQLite DB.
// D14：一个任务可以有多个历史 Run，但至多一个非终态 Run。SQLite 不能原地去掉
// UNIQUE(task_id)，所以先按 sqlite_master 的 DDL 判断旧表，再做标准表重建。
func (s *TaskStore) migrateWorkflowRuns() error {
	if err := s.migrateWorkflowRunsDropTaskUnique(); err != nil {
		return err
	}
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS workflow_runs (
 id TEXT PRIMARY KEY, task_id TEXT NOT NULL, version TEXT NOT NULL DEFAULT '',
 revision INTEGER NOT NULL DEFAULT 1, graph_json TEXT NOT NULL,
 node_states_json TEXT NOT NULL DEFAULT '{}', node_outputs_json TEXT NOT NULL DEFAULT '{}',
 selected_routes_json TEXT NOT NULL DEFAULT '{}', loop_state_json TEXT NOT NULL DEFAULT '{}',
 loop_exit_expr_json TEXT NOT NULL DEFAULT '{}', created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_workflow_runs_task ON workflow_runs(task_id);
CREATE INDEX IF NOT EXISTS idx_workflow_runs_task_created ON workflow_runs(task_id,created_at DESC);
CREATE TABLE IF NOT EXISTS workflow_node_runs (
 id TEXT PRIMARY KEY, run_id TEXT NOT NULL, node_id TEXT NOT NULL, loop_path TEXT NOT NULL DEFAULT '',
 iteration INTEGER NOT NULL DEFAULT 0, attempt INTEGER NOT NULL DEFAULT 1,
 agent TEXT NOT NULL DEFAULT '', harness TEXT NOT NULL DEFAULT '', model TEXT NOT NULL DEFAULT '',
 session_key TEXT NOT NULL DEFAULT '', status TEXT NOT NULL, input_json TEXT NOT NULL DEFAULT '{}',
 output_json TEXT NOT NULL DEFAULT '{}', started_at TEXT NOT NULL DEFAULT '', finished_at TEXT NOT NULL DEFAULT '',
 prev_attempt_id TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
 UNIQUE(run_id,node_id,loop_path,iteration,attempt)
);
CREATE INDEX IF NOT EXISTS idx_workflow_node_runs_run ON workflow_node_runs(run_id,created_at);
`)
	return err
}

// migrateWorkflowRunsDropTaskUnique rebuilds workflow_runs when it still carries
// the original UNIQUE(task_id). Rows are copied verbatim; only the constraint
// changes, so existing runs and their node history survive.
func (s *TaskStore) migrateWorkflowRunsDropTaskUnique() error {
	var ddl string
	err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='workflow_runs'`).Scan(&ddl)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !strings.Contains(ddl, "task_id TEXT NOT NULL UNIQUE") {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statements := []string{
		`ALTER TABLE workflow_runs RENAME TO workflow_runs_task_unique_legacy`,
		`CREATE TABLE workflow_runs (
 id TEXT PRIMARY KEY, task_id TEXT NOT NULL, version TEXT NOT NULL DEFAULT '',
 revision INTEGER NOT NULL DEFAULT 1, graph_json TEXT NOT NULL,
 node_states_json TEXT NOT NULL DEFAULT '{}', node_outputs_json TEXT NOT NULL DEFAULT '{}',
 selected_routes_json TEXT NOT NULL DEFAULT '{}', loop_state_json TEXT NOT NULL DEFAULT '{}',
 loop_exit_expr_json TEXT NOT NULL DEFAULT '{}', created_at TEXT NOT NULL, updated_at TEXT NOT NULL
)`,
		`INSERT INTO workflow_runs(id,task_id,version,revision,graph_json,node_states_json,node_outputs_json,selected_routes_json,loop_state_json,loop_exit_expr_json,created_at,updated_at)
 SELECT id,task_id,version,revision,graph_json,node_states_json,node_outputs_json,selected_routes_json,loop_state_json,loop_exit_expr_json,created_at,updated_at FROM workflow_runs_task_unique_legacy`,
		`DROP TABLE workflow_runs_task_unique_legacy`,
		`CREATE INDEX IF NOT EXISTS idx_workflow_runs_task ON workflow_runs(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_workflow_runs_task_created ON workflow_runs(task_id,created_at DESC)`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func marshalWorkflowJSON(v any) (string, error) { b, err := json.Marshal(v); return string(b), err }
func unmarshalWorkflowJSON[T any](s string) (T, error) {
	var v T
	if strings.TrimSpace(s) == "" {
		return v, nil
	}
	return v, json.Unmarshal([]byte(s), &v)
}

func (s *TaskStore) CreateWorkflowRunAndSetEngine(ctx context.Context, run *WorkflowRun) error {
	if run == nil || strings.TrimSpace(run.ID) == "" || strings.TrimSpace(run.TaskID) == "" {
		return errors.New("workflow run id and task id required")
	}
	if err := ValidateWorkflowDefinition(run.Graph); err != nil {
		return err
	}
	if run.NodeStates == nil {
		run.NodeStates = map[string]string{}
	}
	if run.NodeOutputs == nil {
		run.NodeOutputs = map[string]map[string]any{}
	}
	if run.SelectedRoutes == nil {
		run.SelectedRoutes = map[string]string{}
	}
	if run.LoopState == nil {
		run.LoopState = map[string]*LoopState{}
	}
	if run.LoopExitExpr == nil {
		run.LoopExitExpr = map[string]string{}
	}
	graph, err := marshalWorkflowJSON(run.Graph)
	if err != nil {
		return err
	}
	states, err := marshalWorkflowJSON(run.NodeStates)
	if err != nil {
		return err
	}
	outputs, err := marshalWorkflowJSON(run.NodeOutputs)
	if err != nil {
		return err
	}
	routes, err := marshalWorkflowJSON(run.SelectedRoutes)
	if err != nil {
		return err
	}
	loops, err := marshalWorkflowJSON(run.LoopState)
	if err != nil {
		return err
	}
	exits, err := marshalWorkflowJSON(run.LoopExitExpr)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var engine, boundRunID string
	err = tx.QueryRowContext(ctx, `SELECT engine, workflow_run_id FROM task_execution_engines WHERE task_id=?`, run.TaskID).Scan(&engine, &boundRunID)
	if err == nil && engine == EngineWorkflow {
		// D14：历史 Run 可以并存，但绑定中的 Run 必须已终态，否则同一任务会
		// 同时推进两条活运行。绑定悬空（run 行不存在）时 fail closed。
		bound, berr := scanWorkflowRun(tx.QueryRowContext(ctx, `SELECT `+workflowRunColumns+` FROM workflow_runs WHERE id=?`, boundRunID))
		if berr != nil {
			return fmt.Errorf("task %s has workflow engine bound to unreadable run %q: %w", run.TaskID, boundRunID, berr)
		}
		if !isTerminalStatus(ProjectStatusFromRun(bound)) {
			return ErrTaskAlreadyWorkflow
		}
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO workflow_runs(id,task_id,version,revision,graph_json,node_states_json,node_outputs_json,selected_routes_json,loop_state_json,loop_exit_expr_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, run.ID, run.TaskID, run.Version, run.Revision, graph, states, outputs, routes, loops, exits, now, now)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO task_execution_engines(task_id,engine,workflow_run_id,updated_at) VALUES(?,?,?,?) ON CONFLICT(task_id) DO UPDATE SET engine=excluded.engine,workflow_run_id=excluded.workflow_run_id,updated_at=excluded.updated_at`, run.TaskID, EngineWorkflow, run.ID, now)
	if err != nil {
		return err
	}
	return tx.Commit()
}

const workflowRunColumns = `id,task_id,version,revision,graph_json,node_states_json,node_outputs_json,selected_routes_json,loop_state_json,loop_exit_expr_json,created_at,updated_at`

func scanWorkflowRun(row interface{ Scan(...any) error }) (*WorkflowRun, error) {
	var r WorkflowRun
	var graph, states, outputs, routes, loops, exits, created, updated string
	if err := row.Scan(&r.ID, &r.TaskID, &r.Version, &r.Revision, &graph, &states, &outputs, &routes, &loops, &exits, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrWorkflowRunNotFound
		}
		return nil, err
	}
	var err error
	if r.Graph, err = unmarshalWorkflowJSON[WorkflowDefinition](graph); err != nil {
		return nil, fmt.Errorf("decode workflow graph: %w", err)
	}
	if r.NodeStates, err = unmarshalWorkflowJSON[map[string]string](states); err != nil {
		return nil, err
	}
	if r.NodeOutputs, err = unmarshalWorkflowJSON[map[string]map[string]any](outputs); err != nil {
		return nil, err
	}
	if r.SelectedRoutes, err = unmarshalWorkflowJSON[map[string]string](routes); err != nil {
		return nil, err
	}
	if r.LoopState, err = unmarshalWorkflowJSON[map[string]*LoopState](loops); err != nil {
		return nil, err
	}
	if r.LoopExitExpr, err = unmarshalWorkflowJSON[map[string]string](exits); err != nil {
		return nil, err
	}
	r.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	r.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return &r, nil
}

// WorkflowRunForTask returns the task's current run: the engine binding when it
// points at a workflow run, otherwise the most recently created run. With D14 a
// task may own terminal history, so "first row for task" is no longer defined.
func (s *TaskStore) WorkflowRunForTask(ctx context.Context, taskID string) (*WorkflowRun, error) {
	engine, err := taskEngineOf(ctx, s.db, taskID)
	if err != nil {
		return nil, err
	}
	if engine.Engine == EngineWorkflow && strings.TrimSpace(engine.WorkflowRunID) != "" {
		return s.WorkflowRunByID(ctx, engine.WorkflowRunID)
	}
	return scanWorkflowRun(s.db.QueryRowContext(ctx, `SELECT `+workflowRunColumns+` FROM workflow_runs WHERE task_id=? ORDER BY created_at DESC,rowid DESC LIMIT 1`, taskID))
}

// ListWorkflowRunsForTask returns the task's run history, newest first.
func (s *TaskStore) ListWorkflowRunsForTask(ctx context.Context, taskID string) ([]*WorkflowRun, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+workflowRunColumns+` FROM workflow_runs WHERE task_id=? ORDER BY created_at DESC,rowid DESC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*WorkflowRun
	for rows.Next() {
		run, err := scanWorkflowRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}
func (s *TaskStore) WorkflowRunByID(ctx context.Context, runID string) (*WorkflowRun, error) {
	return scanWorkflowRun(s.db.QueryRowContext(ctx, `SELECT `+workflowRunColumns+` FROM workflow_runs WHERE id=?`, runID))
}
func (s *TaskStore) SaveWorkflowRun(ctx context.Context, run *WorkflowRun) error {
	if run == nil {
		return errors.New("workflow run required")
	}
	if run.NodeStates == nil {
		run.NodeStates = map[string]string{}
	}
	if run.NodeOutputs == nil {
		run.NodeOutputs = map[string]map[string]any{}
	}
	if run.SelectedRoutes == nil {
		run.SelectedRoutes = map[string]string{}
	}
	if run.LoopState == nil {
		run.LoopState = map[string]*LoopState{}
	}
	if run.LoopExitExpr == nil {
		run.LoopExitExpr = map[string]string{}
	}
	graph, err := marshalWorkflowJSON(run.Graph)
	if err != nil {
		return err
	}
	states, err := marshalWorkflowJSON(run.NodeStates)
	if err != nil {
		return err
	}
	outputs, err := marshalWorkflowJSON(run.NodeOutputs)
	if err != nil {
		return err
	}
	routes, err := marshalWorkflowJSON(run.SelectedRoutes)
	if err != nil {
		return err
	}
	loops, err := marshalWorkflowJSON(run.LoopState)
	if err != nil {
		return err
	}
	exits, err := marshalWorkflowJSON(run.LoopExitExpr)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var rev int
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM workflow_runs WHERE id=?`, run.ID).Scan(&rev); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrWorkflowRunNotFound
		}
		return err
	}
	if rev != run.Revision {
		return ErrRunConflict
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `UPDATE workflow_runs SET revision=revision+1,graph_json=?,node_states_json=?,node_outputs_json=?,selected_routes_json=?,loop_state_json=?,loop_exit_expr_json=?,updated_at=? WHERE id=? AND revision=?`, graph, states, outputs, routes, loops, exits, now.Format(time.RFC3339Nano), run.ID, run.Revision); err != nil {
		return err
	}
	run.Revision++
	run.UpdatedAt = now
	return tx.Commit()
}
func (s *TaskStore) EditWorkflowRunGraph(ctx context.Context, runID string, baseRevision int, graph WorkflowDefinition) error {
	if err := ValidateWorkflowDefinition(graph); err != nil {
		return err
	}
	run, err := s.WorkflowRunByID(ctx, runID)
	if err != nil {
		return err
	}
	if run.Revision != baseRevision {
		return ErrRunConflict
	}
	frozen := run.FrozenNodeIDs()
	for id := range frozen {
		old, ok := run.Graph.node(id)
		if !ok {
			continue
		}
		nw, ok := graph.node(id)
		if !ok {
			return fmt.Errorf("%w: %s", ErrFrozenNodeEdit, id)
		}
		ob, _ := json.Marshal(old)
		nb, _ := json.Marshal(nw)
		if string(ob) != string(nb) {
			return fmt.Errorf("%w: %s", ErrFrozenNodeEdit, id)
		}
	}
	run.Graph = cloneGraph(graph)
	return s.SaveWorkflowRun(ctx, run)
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
