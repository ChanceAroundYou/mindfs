package kanban

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	// ErrCommentTargetNotActive：留言显式选择的目标不是当前 run 的活动 Agent
	// NodeRun。拒绝，不降级到别的节点 —— 选错目标时静默改投比报错危险。
	ErrCommentTargetNotActive = errors.New("comment target is not an active agent node run")
	// ErrCommentBindingConflict：同一条留言的重试携带了与已持久化绑定不同的目标。
	// 绑定不可变，冲突必须显式返回。
	ErrCommentBindingConflict = errors.New("comment already bound to a different node run")
)

// workflowCommentBinding 是一次留言绑定判定的结果。
//
// NodeRunID 为空有两种完全不同的含义，用 Status 区分：
//   - pending_phase：到达时没有任何活动 Agent NodeRun，留言开启新阶段；
//   - queued：有活动节点但还不能唯一确定（多个分支），等收束。
//
// 两者都不能在之后的节点启动时被自动改投。
type workflowCommentBinding struct {
	RunID      string
	NodeRunID  string
	SessionKey string
	Status     string
}

// workflowCommentRow 是投递队列要读的一行：留言正文在 activity 上，执行
// 绑定与状态在 continue_requests 上。
type workflowCommentRow struct {
	RequestID  string
	CommentID  string
	Status     string
	NodeRunID  string
	SessionKey string
	Body       string
	CreatedAt  time.Time
}

func parseDBTime(s string) time.Time {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return time.Time{}
}

// workflowRunIDForTaskTx returns the workflow run bound to the task, or empty
// for a legacy task. Only the explicit engine binding counts: a historical run
// row must never resurrect a task that is not running on the workflow engine.
func workflowRunIDForTaskTx(ctx context.Context, tx *sql.Tx, taskID string) (string, error) {
	var engine, runID string
	err := tx.QueryRowContext(ctx, `SELECT engine, workflow_run_id FROM task_execution_engines WHERE task_id=?`, taskID).Scan(&engine, &runID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if engine != EngineWorkflow {
		return "", nil
	}
	return strings.TrimSpace(runID), nil
}

// activeWorkflowAgentNodesTx lists agent NodeRuns currently running for a run.
// A node whose session is not bound yet is still active (it is between claim
// and the external turn), so callers must not treat "one active, no session"
// as "zero active": that would start a new phase while a turn is being set up.
func activeWorkflowAgentNodesTx(ctx context.Context, tx *sql.Tx, runID string) ([]NodeRun, error) {
	if strings.TrimSpace(runID) == "" {
		return nil, nil
	}
	var graphJSON string
	if err := tx.QueryRowContext(ctx, `SELECT graph_json FROM workflow_runs WHERE id=?`, runID).Scan(&graphJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	graph, err := unmarshalWorkflowJSON[WorkflowDefinition](graphJSON)
	if err != nil {
		return nil, err
	}
	agentIDs := workflowAgentNodeIDs(graph)
	rows, err := tx.QueryContext(ctx,
		`SELECT id,run_id,node_id,loop_path,iteration,attempt,session_key,status FROM workflow_node_runs WHERE run_id=? AND status=? ORDER BY created_at,id`,
		runID, NodeRunRunning)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NodeRun
	for rows.Next() {
		var n NodeRun
		if err := rows.Scan(&n.ID, &n.RunID, &n.NodeID, &n.LoopPath, &n.Iteration, &n.Attempt, &n.SessionKey, &n.Status); err != nil {
			return nil, err
		}
		if !agentIDs[n.NodeID] {
			continue
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func containsNodeRun(nodes []NodeRun, id string) (NodeRun, bool) {
	for _, n := range nodes {
		if n.ID == id {
			return n, true
		}
	}
	return NodeRun{}, false
}

// ActiveWorkflowAgentNodeRuns is the read-side view used by the post-node
// convergence check. Same filtering rule as the transaction helper: running,
// kind=agent. Session binding may still be empty.
func (s *TaskStore) ActiveWorkflowAgentNodeRuns(ctx context.Context, runID string) ([]NodeRun, error) {
	if strings.TrimSpace(runID) == "" {
		return nil, nil
	}
	run, err := s.WorkflowRunByID(ctx, runID)
	if err != nil {
		return nil, err
	}
	agentIDs := workflowAgentNodeIDs(run.Graph)
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,run_id,node_id,loop_path,iteration,attempt,session_key,status FROM workflow_node_runs WHERE run_id=? AND status=? ORDER BY created_at,id`,
		runID, NodeRunRunning)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NodeRun
	for rows.Next() {
		var n NodeRun
		if err := rows.Scan(&n.ID, &n.RunID, &n.NodeID, &n.LoopPath, &n.Iteration, &n.Attempt, &n.SessionKey, &n.Status); err != nil {
			return nil, err
		}
		if !agentIDs[n.NodeID] {
			continue
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// decideCommentBinding applies D3 to one comment.
//
// Precedence:
//  1. an explicit target must be an active agent node of the current run;
//  2. exactly one active node and it has a session → bind immediately;
//  3. several active nodes → queued until branches converge;
//  4. no active node → pending_phase (a new execution phase, never auto-bound
//     to a node that starts later).
func decideCommentBinding(runID string, active []NodeRun, expected string) (workflowCommentBinding, error) {
	b := workflowCommentBinding{RunID: runID}
	expected = strings.TrimSpace(expected)
	if expected != "" {
		target, ok := containsNodeRun(active, expected)
		if !ok || strings.TrimSpace(target.SessionKey) == "" {
			return b, ErrCommentTargetNotActive
		}
		b.NodeRunID = target.ID
		b.SessionKey = target.SessionKey
		b.Status = ContinueRequestQueued
		return b, nil
	}
	switch len(active) {
	case 0:
		b.Status = ContinueRequestPendingPhase
	case 1:
		if strings.TrimSpace(active[0].SessionKey) == "" {
			// 正在建立会话：不允许把留言绑到空 session，也不把它误判成
			// 「没有活动节点」。等这个节点结束时的收敛检查处理。
			b.Status = ContinueRequestQueued
			return b, nil
		}
		b.NodeRunID = active[0].ID
		b.SessionKey = active[0].SessionKey
		b.Status = ContinueRequestQueued
	default:
		b.Status = ContinueRequestQueued
	}
	return b, nil
}

func (s *TaskStore) updateCommentBindingTx(ctx context.Context, tx *sql.Tx, taskID, commentID string, b workflowCommentBinding, now string) error {
	if _, err := tx.ExecContext(ctx,
		`UPDATE continue_requests SET node_run_id=?, session_key=?, status=?, updated_at=? WHERE task_id=? AND comment_id=?`,
		b.NodeRunID, b.SessionKey, b.Status, now, taskID, commentID); err != nil {
		return err
	}
	if b.NodeRunID != "" {
		if _, err := tx.ExecContext(ctx,
			`UPDATE task_activities SET run_id=?, node_run_id=? WHERE task_id=? AND id=?`,
			b.RunID, b.NodeRunID, taskID, commentID); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO outbox_items(id,task_id,request_id,node_run_id,session_key,message,status,created_at,updated_at)
		 VALUES(?,?,?,?,?,COALESCE((SELECT message FROM continue_requests WHERE task_id=? AND comment_id=?),''),?,?,?)
		 ON CONFLICT(id) DO UPDATE SET node_run_id=excluded.node_run_id,session_key=excluded.session_key,message=excluded.message,status=excluded.status,updated_at=excluded.updated_at`,
		"ob-"+commentID, taskID, commentID, b.NodeRunID, b.SessionKey, taskID, commentID, b.Status, now, now)
	return err
}

// bindCommentInTx is called with the comment row already persisted. It returns
// the persisted binding; an already-bound comment is immutable.
func (s *TaskStore) bindCommentInTx(ctx context.Context, tx *sql.Tx, taskID, commentID, expected string) (workflowCommentBinding, error) {
	runID, err := workflowRunIDForTaskTx(ctx, tx, taskID)
	if err != nil {
		return workflowCommentBinding{}, err
	}
	var existingNodeRun, existingSession, existingStatus string
	if err := tx.QueryRowContext(ctx,
		`SELECT node_run_id, session_key, status FROM continue_requests WHERE task_id=? AND comment_id=?`,
		taskID, commentID).Scan(&existingNodeRun, &existingSession, &existingStatus); err != nil {
		return workflowCommentBinding{}, err
	}
	if existingNodeRun != "" {
		if e := strings.TrimSpace(expected); e != "" && e != existingNodeRun {
			return workflowCommentBinding{}, ErrCommentBindingConflict
		}
		return workflowCommentBinding{RunID: runID, NodeRunID: existingNodeRun, SessionKey: existingSession, Status: existingStatus}, nil
	}
	if runID == "" {
		// legacy 任务没有 workflow 运行；保持既有 queued 行为（零回填）。
		return workflowCommentBinding{Status: existingStatus}, nil
	}
	active, err := activeWorkflowAgentNodesTx(ctx, tx, runID)
	if err != nil {
		return workflowCommentBinding{}, err
	}
	b, err := decideCommentBinding(runID, active, expected)
	if err != nil {
		return workflowCommentBinding{}, err
	}
	if b.Status != existingStatus || b.NodeRunID != "" {
		now := s.now().UTC().Format(time.RFC3339Nano)
		if err := s.updateCommentBindingTx(ctx, tx, taskID, commentID, b, now); err != nil {
			return workflowCommentBinding{}, err
		}
	}
	return b, nil
}

// BindCommentToActiveNodeRun re-evaluates an existing unbound comment against
// the current active Agent NodeRuns. It is the explicit-target entry point and
// the convergence path used after a node finishes. A bound comment never moves.
func (s *TaskStore) BindCommentToActiveNodeRun(ctx context.Context, taskID, commentID, expectedNodeRunID string) (ContinueRequest, error) {
	taskID = strings.TrimSpace(taskID)
	commentID = strings.TrimSpace(commentID)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ContinueRequest{}, err
	}
	defer tx.Rollback()
	var status, nodeRunID, sessionKey, created, updated string
	if err := tx.QueryRowContext(ctx,
		`SELECT status, node_run_id, session_key, created_at, updated_at FROM continue_requests WHERE task_id=? AND comment_id=?`,
		taskID, commentID).Scan(&status, &nodeRunID, &sessionKey, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ContinueRequest{}, ErrOutboxNotFound
		}
		return ContinueRequest{}, err
	}
	if nodeRunID == "" && status != ContinueRequestBlocked && status != ContinueRequestConsumed {
		b, berr := s.bindCommentInTx(ctx, tx, taskID, commentID, expectedNodeRunID)
		if berr != nil {
			return ContinueRequest{}, berr
		}
		status, nodeRunID, sessionKey = b.Status, b.NodeRunID, b.SessionKey
	} else if e := strings.TrimSpace(expectedNodeRunID); e != "" && e != nodeRunID {
		return ContinueRequest{}, ErrCommentBindingConflict
	}
	if err := tx.Commit(); err != nil {
		return ContinueRequest{}, err
	}
	return ContinueRequest{
		RequestID: commentID, TaskID: taskID, CommentID: commentID, Status: status,
		NodeRunID: nodeRunID, SessionKey: sessionKey,
		CreatedAt: parseDBTime(created), UpdatedAt: parseDBTime(updated),
	}, nil
}

func scanContinueRequest(row interface{ Scan(...any) error }) (ContinueRequest, error) {
	var r ContinueRequest
	var created, updated string
	if err := row.Scan(&r.RequestID, &r.TaskID, &r.CommentID, &r.Status, &r.NodeRunID, &r.SessionKey, &created, &updated); err != nil {
		return ContinueRequest{}, err
	}
	r.CreatedAt = parseDBTime(created)
	r.UpdatedAt = parseDBTime(updated)
	return r, nil
}

// ListOpenWorkflowComments returns undelivered comments in FIFO order. Bound
// rows carry the exact node/session they must go to; pending_phase rows are
// visible but never delivered by convergence.
func (s *TaskStore) ListOpenWorkflowComments(ctx context.Context, taskID string) ([]workflowCommentRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT cr.request_id, cr.comment_id, cr.status, cr.node_run_id, cr.session_key,
		        COALESCE((SELECT a.body FROM task_activities a WHERE a.task_id=cr.task_id AND a.id=cr.comment_id), ''),
		        cr.created_at
		 FROM continue_requests cr
		 WHERE cr.task_id=? AND cr.status IN (?,?)
		 ORDER BY cr.created_at ASC, cr.request_id ASC`,
		strings.TrimSpace(taskID), ContinueRequestQueued, ContinueRequestPendingPhase)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []workflowCommentRow
	for rows.Next() {
		var r workflowCommentRow
		var created string
		if err := rows.Scan(&r.RequestID, &r.CommentID, &r.Status, &r.NodeRunID, &r.SessionKey, &r.Body, &created); err != nil {
			return nil, err
		}
		r.CreatedAt = parseDBTime(created)
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetWorkflowCommentStatus updates both the canonical ContinueRequest and its
// outbox projection. Optionally (re)binds node/session in the same statement.
func (s *TaskStore) SetWorkflowCommentStatus(ctx context.Context, taskID, commentID, status, nodeRunID, sessionKey string) error {
	taskID = strings.TrimSpace(taskID)
	commentID = strings.TrimSpace(commentID)
	now := s.now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`UPDATE continue_requests SET status=?, node_run_id=?, session_key=?, updated_at=? WHERE task_id=? AND comment_id=?`,
		status, nodeRunID, sessionKey, now, taskID, commentID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO outbox_items(id,task_id,request_id,node_run_id,session_key,message,status,created_at,updated_at)
		 VALUES(?,?,?,?,?,COALESCE((SELECT message FROM continue_requests WHERE task_id=? AND comment_id=?),''),?,?,?)
		 ON CONFLICT(id) DO UPDATE SET node_run_id=excluded.node_run_id,session_key=excluded.session_key,message=excluded.message,status=excluded.status,updated_at=excluded.updated_at`,
		"ob-"+commentID, taskID, commentID, nodeRunID, sessionKey, taskID, commentID, status, now, now); err != nil {
		return err
	}
	if nodeRunID != "" {
		if _, err := tx.ExecContext(ctx,
			`UPDATE task_activities SET node_run_id=? WHERE task_id=? AND id=?`, nodeRunID, taskID, commentID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// BlockWorkflowCommentQueue marks every undelivered comment blocked. It never
// deletes rows: the activity stream keeps the user's message and the status
// says the system did not deliver it.
func (s *TaskStore) BlockWorkflowCommentQueue(ctx context.Context, taskID string) error {
	rows, err := s.ListOpenWorkflowComments(ctx, taskID)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := s.SetWorkflowCommentStatus(ctx, taskID, r.CommentID, ContinueRequestBlocked, r.NodeRunID, r.SessionKey); err != nil {
			return fmt.Errorf("block comment %s: %w", r.CommentID, err)
		}
	}
	return nil
}
