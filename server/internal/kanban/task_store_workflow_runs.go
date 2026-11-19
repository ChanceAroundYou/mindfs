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
 loop_exit_expr_json TEXT NOT NULL DEFAULT '{}', created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
 integration_branch TEXT NOT NULL DEFAULT '',
 triggered_by TEXT NOT NULL DEFAULT '', activity_id TEXT NOT NULL DEFAULT '',
 session_key TEXT NOT NULL DEFAULT ''
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
 worktree_id TEXT NOT NULL DEFAULT '', worktree_path TEXT NOT NULL DEFAULT '', worktree_branch TEXT NOT NULL DEFAULT '',
 UNIQUE(run_id,node_id,loop_path,iteration,attempt)
);
CREATE INDEX IF NOT EXISTS idx_workflow_node_runs_run ON workflow_node_runs(run_id,created_at);
`)
	if err != nil {
		return err
	}
	// 存量库补列：CREATE TABLE IF NOT EXISTS 不会给已存在的表加列。顺序固定、
	// 逐列判断，重复启动幂等。列名是常量，不接受外部输入。
	for _, col := range []struct{ table, name, ddl string }{
		{"workflow_runs", "integration_branch", "TEXT NOT NULL DEFAULT ''"},
		{"workflow_runs", "triggered_by", "TEXT NOT NULL DEFAULT ''"},
		{"workflow_runs", "activity_id", "TEXT NOT NULL DEFAULT ''"},
		{"workflow_runs", "session_key", "TEXT NOT NULL DEFAULT ''"},
		{"workflow_node_runs", "worktree_id", "TEXT NOT NULL DEFAULT ''"},
		{"workflow_node_runs", "worktree_path", "TEXT NOT NULL DEFAULT ''"},
		{"workflow_node_runs", "worktree_branch", "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := s.ensureTableColumn(col.table, col.name, col.ddl); err != nil {
			return err
		}
	}
	return nil
}

// ensureTableColumn adds a column when the table predates it. Table/column names
// are internal constants; never call this with request-derived strings.
func (s *TaskStore) ensureTableColumn(table, column, ddl string) error {
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == column {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if found {
		return nil
	}
	_, err = s.db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + column + ` ` + ddl)
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
 loop_exit_expr_json TEXT NOT NULL DEFAULT '{}', created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
 integration_branch TEXT NOT NULL DEFAULT '', triggered_by TEXT NOT NULL DEFAULT '', activity_id TEXT NOT NULL DEFAULT '',
 session_key TEXT NOT NULL DEFAULT ''
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
		// 例外：一次性迁移生成的 legacy_import Run 只是审计快照，不是活运行；
		// 否则一条 waiting_user 的历史任务会永久挡住用户的新留言。
		bound, berr := scanWorkflowRun(tx.QueryRowContext(ctx, `SELECT `+workflowRunColumns+` FROM workflow_runs WHERE id=?`, boundRunID))
		if berr != nil {
			return fmt.Errorf("task %s has workflow engine bound to unreadable run %q: %w", run.TaskID, boundRunID, berr)
		}
		if !isTerminalStatus(ProjectStatusFromRun(bound)) && bound.TriggeredBy != WorkflowTriggeredByLegacyImport {
			return ErrTaskAlreadyWorkflow
		}
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO workflow_runs(id,task_id,version,revision,graph_json,node_states_json,node_outputs_json,selected_routes_json,loop_state_json,loop_exit_expr_json,created_at,updated_at,integration_branch,triggered_by,activity_id,session_key) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, run.ID, run.TaskID, run.Version, run.Revision, graph, states, outputs, routes, loops, exits, now, now, strings.TrimSpace(run.IntegrationBranch), strings.TrimSpace(run.TriggeredBy), strings.TrimSpace(run.ActivityID), strings.TrimSpace(run.SessionKey))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO task_execution_engines(task_id,engine,workflow_run_id,updated_at) VALUES(?,?,?,?) ON CONFLICT(task_id) DO UPDATE SET engine=excluded.engine,workflow_run_id=excluded.workflow_run_id,updated_at=excluded.updated_at`, run.TaskID, EngineWorkflow, run.ID, now)
	if err != nil {
		return err
	}
	return tx.Commit()
}

const workflowRunColumns = `id,task_id,version,revision,graph_json,node_states_json,node_outputs_json,selected_routes_json,loop_state_json,loop_exit_expr_json,created_at,updated_at,integration_branch,triggered_by,activity_id,session_key`

func scanWorkflowRun(row interface{ Scan(...any) error }) (*WorkflowRun, error) {
	var r WorkflowRun
	var graph, states, outputs, routes, loops, exits, created, updated string
	if err := row.Scan(&r.ID, &r.TaskID, &r.Version, &r.Revision, &graph, &states, &outputs, &routes, &loops, &exits, &created, &updated, &r.IntegrationBranch, &r.TriggeredBy, &r.ActivityID, &r.SessionKey); err != nil {
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

// WorkflowRunSessionKey 读这一轮 Run 已经绑定的共享会话，没绑定返回空串。
func (s *TaskStore) WorkflowRunSessionKey(ctx context.Context, runID string) (string, error) {
	var key string
	if err := s.db.QueryRowContext(ctx, `SELECT session_key FROM workflow_runs WHERE id=?`, strings.TrimSpace(runID)).Scan(&key); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrWorkflowRunNotFound
		}
		return "", err
	}
	return strings.TrimSpace(key), nil
}

// SetWorkflowRunSessionKey 把这一轮 Run 绑到一条共享会话上（session_reuse_policy=run）。
//
// 先到先得：只在 session_key 还是空的时候写入，然后读回最终值。并行分支同时
// 首次执行时，两边都会尝试建会话，但只有一条会被记成 Run 的会话；调用方拿
// 返回的 key 去跑节点，落败的那条会话不会被使用。
//
// 刻意不走 saveWorkflowRunTx / revision CAS：会话绑定不是运行状态，推进 revision
// 会让正在跑的 driver 撞 ErrRunConflict。
func (s *TaskStore) SetWorkflowRunSessionKey(ctx context.Context, runID, sessionKey string) (string, error) {
	runID = strings.TrimSpace(runID)
	sessionKey = strings.TrimSpace(sessionKey)
	if runID == "" || sessionKey == "" {
		return "", errors.New("workflow run id and session key required")
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE workflow_runs SET session_key=? WHERE id=? AND session_key=''`, sessionKey, runID); err != nil {
		return "", err
	}
	var current string
	if err := s.db.QueryRowContext(ctx, `SELECT session_key FROM workflow_runs WHERE id=?`, runID).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrWorkflowRunNotFound
		}
		return "", err
	}
	return strings.TrimSpace(current), nil
}

// LatestTaskSessionKey 返回「这条任务最近一次真正在用的会话」，供 Router 选择
// 接续（session_mode=continue）时把新一轮 Run 直接绑上去。
//
// 顺序：最近一条带会话的对话活动 → 最近一轮 Run 的共享会话 → 任务主会话。
// 活动排在前面是因为它反映「用户/agent 最后实际在哪条会话里说话」；Run 表次之
// 是为了兜住「活动已被清理但 Run 还在」的历史任务。
func (s *TaskStore) LatestTaskSessionKey(ctx context.Context, taskID string) (string, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return "", errors.New("task id required")
	}
	var key string
	err := s.db.QueryRowContext(ctx,
		`SELECT session_key FROM task_activities WHERE task_id=? AND session_key != '' ORDER BY created_at DESC,rowid DESC LIMIT 1`,
		taskID).Scan(&key)
	if err == nil && strings.TrimSpace(key) != "" {
		return strings.TrimSpace(key), nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	err = s.db.QueryRowContext(ctx,
		`SELECT session_key FROM workflow_runs WHERE task_id=? AND session_key != '' ORDER BY created_at DESC,rowid DESC LIMIT 1`,
		taskID).Scan(&key)
	if err == nil && strings.TrimSpace(key) != "" {
		return strings.TrimSpace(key), nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT main_session_key FROM tasks WHERE id=?`, taskID).Scan(&key); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(key), nil
}
func (s *TaskStore) SaveWorkflowRun(ctx context.Context, run *WorkflowRun) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.saveWorkflowRunTx(ctx, tx, run); err != nil {
		return err
	}
	return tx.Commit()
}

// SaveWorkflowRunWithEvent applies a run projection update and its ledger event
// in one transaction (D13: a projection never moves without its typed event).
func (s *TaskStore) SaveWorkflowRunWithEvent(ctx context.Context, run *WorkflowRun, ev WorkflowRunEvent) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.saveWorkflowRunTx(ctx, tx, run); err != nil {
		return err
	}
	ev.RunID = run.ID
	if _, err := appendWorkflowRunEventTx(ctx, tx, ev); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *TaskStore) saveWorkflowRunTx(ctx context.Context, tx *sql.Tx, run *WorkflowRun) error {
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
	return nil
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
	frozen, err := s.workflowRunFrozenNodes(ctx, run)
	if err != nil {
		return err
	}
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
	// Edges incident to a triggered node are frozen too, including a route
	// change on an existing edge. Plan Task 6 Step 4 is deliberately stricter
	// than "add only unconsumed frontier edges": v1 cannot prove a newly added
	// outgoing edge was not already reasoned about by the node that ran, so it
	// fails closed. Unchanged incident edges and untriggered-subtree edits stay
	// allowed, and this rule can be relaxed later without invalidating history.
	if err := checkWorkflowFrozenEdges(run.Graph.Edges, graph.Edges, frozen); err != nil {
		return err
	}
	run.Graph = cloneGraph(graph)
	return s.SaveWorkflowRun(ctx, run)
}

type workflowEdgeKey struct {
	From  string
	To    string
	Route string
}

// checkWorkflowFrozenEdges compares edge multisets and rejects any change that
// touches a frozen node. An "executed edge" is exactly the (from,to,route)
// tuple; changing the route of an existing incident edge is a semantic edit and
// is therefore frozen as well.
func checkWorkflowFrozenEdges(oldEdges, newEdges []WorkflowEdge, frozen map[string]bool) error {
	count := func(edges []WorkflowEdge) map[workflowEdgeKey]int {
		out := make(map[workflowEdgeKey]int, len(edges))
		for _, e := range edges {
			out[workflowEdgeKey{From: e.From, To: e.To, Route: e.Route}]++
		}
		return out
	}
	oldCount, newCount := count(oldEdges), count(newEdges)
	check := func(changed []workflowEdgeKey) error {
		for _, e := range changed {
			if frozen[e.From] || frozen[e.To] {
				return fmt.Errorf("%w: edge %s->%s", ErrFrozenNodeEdit, e.From, e.To)
			}
		}
		return nil
	}
	var removed, added []workflowEdgeKey
	for k, n := range oldCount {
		if newCount[k] < n {
			removed = append(removed, k)
		}
	}
	for k, n := range newCount {
		if oldCount[k] < n {
			added = append(added, k)
		}
	}
	if err := check(removed); err != nil {
		return err
	}
	return check(added)
}
