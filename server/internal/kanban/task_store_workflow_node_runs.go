package kanban

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrWorkflowNodeRunNotFound = errors.New("workflow node run not found on this task node")

// ErrWorkflowNodeRunNotQueued is returned when a queued→running transition is
// lost to another actor (cancelled/finished first). Callers treat it as "this
// row is already somebody else's outcome", never as a reason to run a turn.
var ErrWorkflowNodeRunNotQueued = errors.New("workflow node run is not queued")

const workflowNodeRunColumns = `id,run_id,node_id,loop_path,iteration,attempt,agent,harness,model,session_key,status,input_json,output_json,started_at,finished_at,prev_attempt_id,worktree_id,worktree_path,worktree_branch`

func scanWorkflowNodeRun(row interface{ Scan(...any) error }) (*NodeRun, error) {
	var n NodeRun
	var input, output, started, finished string
	if err := row.Scan(&n.ID, &n.RunID, &n.NodeID, &n.LoopPath, &n.Iteration, &n.Attempt, &n.Agent, &n.Harness, &n.Model, &n.SessionKey, &n.Status, &input, &output, &started, &finished, &n.PrevAttemptID, &n.WorktreeID, &n.WorktreePath, &n.WorktreeBranch); err != nil {
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return NodeRun{}, false, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx,
		`INSERT INTO workflow_node_runs(id,run_id,node_id,loop_path,iteration,attempt,agent,harness,model,session_key,status,input_json,output_json,started_at,finished_at,prev_attempt_id,worktree_id,worktree_path,worktree_branch,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(run_id,node_id,loop_path,iteration,attempt) DO NOTHING`,
		n.ID, n.RunID, n.NodeID, n.LoopPath, n.Iteration, n.Attempt, n.Agent, n.Harness, n.Model, n.SessionKey, n.Status, input, output, started, finished, n.PrevAttemptID, n.WorktreeID, n.WorktreePath, n.WorktreeBranch, stamp, stamp)
	if err != nil {
		return NodeRun{}, false, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return NodeRun{}, false, err
	}
	if inserted == 1 {
		// 账本与 claim 同事务：没有 queued 事件的行等于没有赢家。
		if _, err := appendWorkflowRunEventTx(ctx, tx, WorkflowRunEvent{
			RunID: n.RunID, Type: WorkflowEventNodeQueued, NodeID: n.NodeID, NodeRunID: n.ID,
			Attempt: n.Attempt, Payload: map[string]any{"status": n.Status}, CreatedAt: now,
		}); err != nil {
			return NodeRun{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return NodeRun{}, false, err
		}
		return n, true, nil
	}
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		return NodeRun{}, false, err
	}
	existing, err := s.WorkflowNodeRunByKey(ctx, n.RunID, n.NodeID, n.LoopPath, n.Iteration, n.Attempt)
	if err != nil {
		return NodeRun{}, false, err
	}
	return existing, false, nil
}

// BeginWorkflowNodeRun moves a claimed row queued→running and records the
// started event in the same transaction. A rejected transition returns an
// error so the caller never starts an external turn on a row it does not own.
func (s *TaskStore) BeginWorkflowNodeRun(ctx context.Context, nodeRun NodeRun, startedAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stamp := startedAt.UTC().Format(time.RFC3339Nano)
	// D7/终态与暂停：driver 在步首看过任务状态之后，操作员的 complete/fail/
	// cancel/pause 仍可能落库。把闸门放在 queued→running 的同一条 UPDATE 里，
	// 才不会出现「检查通过 → 开始外部回合」的窗口。终态/暂停任务永远不启动
	// 新的 Agent 回合，残留的 queued 行只是历史，不会被重新派发。
	res, err := tx.ExecContext(ctx,
		`UPDATE workflow_node_runs SET status=?, started_at=?, updated_at=? WHERE id=? AND status=? AND EXISTS (
			SELECT 1 FROM workflow_runs wr JOIN tasks t ON t.id = wr.task_id
			WHERE wr.id = workflow_node_runs.run_id AND t.status NOT IN (?,?,?,?)
		)`,
		NodeRunRunning, stamp, stamp, nodeRun.ID, NodeRunQueued,
		StatusSuccess, StatusFail, StatusCancelled, StatusPaused)
	if err != nil {
		return err
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return fmt.Errorf("%w: %s cannot transition to running (row not queued or task paused/terminal)", ErrWorkflowNodeRunNotQueued, nodeRun.ID)
	}
	if _, err := appendWorkflowRunEventTx(ctx, tx, WorkflowRunEvent{
		RunID: nodeRun.RunID, Type: WorkflowEventNodeStarted, NodeID: nodeRun.NodeID, NodeRunID: nodeRun.ID,
		Attempt: nodeRun.Attempt, CreatedAt: startedAt,
	}); err != nil {
		return err
	}
	return tx.Commit()
}

// BindWorkflowNodeSession persists the session identity before the external
// turn starts, so a crash mid-turn still points recovery at the transcript.
func (s *TaskStore) BindWorkflowNodeSession(ctx context.Context, nodeRun NodeRun, sessionKey string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := tx.ExecContext(ctx, `UPDATE workflow_node_runs SET session_key=?, updated_at=? WHERE id=? AND status=?`,
		sessionKey, now, nodeRun.ID, NodeRunRunning)
	if err != nil {
		return err
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return fmt.Errorf("workflow node run %s cannot bind session while not running", nodeRun.ID)
	}
	if _, err := appendWorkflowRunEventTx(ctx, tx, WorkflowRunEvent{
		RunID: nodeRun.RunID, Type: WorkflowEventNodeSessionBound, NodeID: nodeRun.NodeID, NodeRunID: nodeRun.ID,
		Attempt: nodeRun.Attempt, Payload: map[string]any{"session_key": sessionKey},
	}); err != nil {
		return err
	}
	return tx.Commit()
}

// BindWorkflowNodeWorktree persists the NodeRun's durable ownership of an
// independent worktree (D16). It only applies to a running row: binding after
// the turn ended would describe a workspace the node never used.
func (s *TaskStore) BindWorkflowNodeWorktree(ctx context.Context, nodeRun NodeRun, entry WorktreeEntry) error {
	entry, err := normalizeWorktreeEntry(entry)
	if err != nil {
		return err
	}
	if entry.NodeRunID != nodeRun.ID {
		return fmt.Errorf("worktree %s owner %q does not match node run %q", entry.WorktreeID, entry.NodeRunID, nodeRun.ID)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := tx.ExecContext(ctx,
		`UPDATE workflow_node_runs SET worktree_id=?, worktree_path=?, worktree_branch=?, updated_at=? WHERE id=? AND status=?`,
		entry.WorktreeID, entry.Path, entry.Branch, now, nodeRun.ID, NodeRunRunning)
	if err != nil {
		return err
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return fmt.Errorf("workflow node run %s cannot bind worktree while not running", nodeRun.ID)
	}
	if _, err := appendWorkflowRunEventTx(ctx, tx, WorkflowRunEvent{
		RunID: nodeRun.RunID, Type: WorkflowEventNodeWorktreeBound, NodeID: nodeRun.NodeID, NodeRunID: nodeRun.ID,
		Attempt: nodeRun.Attempt, Payload: map[string]any{
			"worktree_id": entry.WorktreeID, "path": entry.Path, "branch": entry.Branch,
		}, CreatedAt: time.Now().UTC(),
	}); err != nil {
		return err
	}
	return tx.Commit()
}

// FinishWorkflowNodeRun moves running→terminal/waiting and records the result
// event atomically. Only a row still observed as running may finish, so a
// duplicate callback cannot overwrite the first outcome.
func (s *TaskStore) FinishWorkflowNodeRun(ctx context.Context, nodeRun NodeRun, status string, output map[string]any, finishedAt time.Time, eventType string, payload map[string]any) error {
	allowed := false
	switch status {
	case NodeRunSuccess, NodeRunFail, NodeRunWaitingUser, NodeRunUnknown, NodeRunCancelled:
		allowed = true
	}
	if !allowed {
		return fmt.Errorf("invalid workflow node terminal status %q", status)
	}
	outputJSON, err := marshalWorkflowJSON(output)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stamp := finishedAt.UTC().Format(time.RFC3339Nano)
	res, err := tx.ExecContext(ctx,
		`UPDATE workflow_node_runs SET status=?, output_json=?, finished_at=?, updated_at=? WHERE id=? AND status=?`,
		status, outputJSON, stamp, stamp, nodeRun.ID, NodeRunRunning)
	if err != nil {
		return err
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return fmt.Errorf("workflow node run %s cannot finish from a non-running state", nodeRun.ID)
	}
	if _, err := appendWorkflowRunEventTx(ctx, tx, WorkflowRunEvent{
		RunID: nodeRun.RunID, Type: eventType, NodeID: nodeRun.NodeID, NodeRunID: nodeRun.ID,
		Attempt: nodeRun.Attempt, Payload: payload, CreatedAt: finishedAt,
	}); err != nil {
		return err
	}
	return tx.Commit()
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
	_, err = s.db.ExecContext(ctx, `INSERT INTO workflow_node_runs(id,run_id,node_id,loop_path,iteration,attempt,agent,harness,model,session_key,status,input_json,output_json,started_at,finished_at,prev_attempt_id,worktree_id,worktree_path,worktree_branch,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET agent=excluded.agent,harness=excluded.harness,model=excluded.model,session_key=excluded.session_key,status=excluded.status,input_json=excluded.input_json,output_json=excluded.output_json,started_at=excluded.started_at,finished_at=excluded.finished_at,prev_attempt_id=excluded.prev_attempt_id,worktree_id=excluded.worktree_id,worktree_path=excluded.worktree_path,worktree_branch=excluded.worktree_branch,updated_at=excluded.updated_at`, n.ID, n.RunID, n.NodeID, n.LoopPath, n.Iteration, n.Attempt, n.Agent, n.Harness, n.Model, n.SessionKey, n.Status, input, output, started, finished, n.PrevAttemptID, n.WorktreeID, n.WorktreePath, n.WorktreeBranch, now, now)
	return err
}
