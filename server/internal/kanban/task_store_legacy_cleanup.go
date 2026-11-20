package kanban

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// LegacyImportRunRef 指向一条落在已结束任务上的历史迁移审计 Run。
type LegacyImportRunRef struct {
	RunID  string
	TaskID string
}

// ListLegacyImportRunsOnTerminalTasks 返回任务已终态的 legacy 审计 Run。
//
// 这类 Run 是 ConvertStagesToConversation 留下的历史快照：它不进
// recoverWorkflowRunsOnStartup（那里按 triggered_by 排除），run_id 也不为空，
// 所以直连孤儿回复（run_id=”）同样收不到它。结果是迁移那一刻刚好在跑的节点
// 与回复永远停在 pending/running。任务非终态的一律不碰：那可能是用户正在继续
// 处理的历史任务，冻结它等于替用户做决定。
func (s *TaskStore) ListLegacyImportRunsOnTerminalTasks(ctx context.Context) ([]LegacyImportRunRef, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT r.id, r.task_id FROM workflow_runs r JOIN tasks t ON t.id = r.task_id
WHERE r.triggered_by = ? AND t.status IN (?, ?, ?)
ORDER BY r.task_id, r.id`,
		WorkflowTriggeredByLegacyImport, StatusSuccess, StatusFail, StatusCancelled)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LegacyImportRunRef{}
	for rows.Next() {
		var ref LegacyImportRunRef
		if err := rows.Scan(&ref.RunID, &ref.TaskID); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

// LegacyImportRunOpenNodes 返回这条 Run 里还没收尾的节点行（含 pending）。
// ListOpenWorkflowNodeRuns 只认 queued/running，迁移留下的 pending 行会漏掉。
func (s *TaskStore) LegacyImportRunOpenNodes(ctx context.Context, runID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT node_id FROM workflow_node_runs WHERE run_id = ? AND status IN (?, ?, ?) ORDER BY node_id`,
		strings.TrimSpace(runID), NodeRunPending, NodeRunQueued, NodeRunRunning)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var nodeID string
		if err := rows.Scan(&nodeID); err != nil {
			return nil, err
		}
		out = append(out, nodeID)
	}
	return out, rows.Err()
}

// SettleLegacyImportRun 把一条历史快照 Run 收尾：开放的 NodeRun 行冻结成
// cancelled，投影里所有非终态一并冻结，并在同一事务里记一条 node_cancelled
// 事件。返回被冻结的节点行 id 列表；没有可改的东西时不写库、不推 revision。
//
// 只有「任务已终态的历史快照」可以走这里。正常运行中的 Run 必须走
// transitionWorkflowNodeRun，那里还有状态机与账本。
func (s *TaskStore) SettleLegacyImportRun(ctx context.Context, runID string, finishedAt time.Time) ([]string, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, errors.New("workflow run id required")
	}
	openNodes, err := s.LegacyImportRunOpenNodes(ctx, runID)
	if err != nil {
		return nil, err
	}
	// 先只读判一次：绝大多数历史任务没有可收尾的东西，不该为它们各开一个写事务。
	needsProjection, err := s.legacyImportProjectionOpen(ctx, runID)
	if err != nil {
		return nil, err
	}
	if len(openNodes) == 0 && !needsProjection {
		return nil, nil
	}

	now := finishedAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	stamp := now.Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if len(openNodes) > 0 {
		if _, err := tx.ExecContext(ctx,
			`UPDATE workflow_node_runs SET status = ?, finished_at = ?, updated_at = ?
			 WHERE run_id = ? AND status IN (?, ?, ?)`,
			NodeRunCancelled, stamp, stamp, runID, NodeRunPending, NodeRunQueued, NodeRunRunning); err != nil {
			return nil, err
		}
	}

	var revision int
	var statesJSON string
	if err := tx.QueryRowContext(ctx, `SELECT revision, node_states_json FROM workflow_runs WHERE id = ?`, runID).
		Scan(&revision, &statesJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrWorkflowRunNotFound
		}
		return nil, err
	}
	states := map[string]string{}
	if strings.TrimSpace(statesJSON) != "" {
		if err := json.Unmarshal([]byte(statesJSON), &states); err != nil {
			return nil, err
		}
	}
	frozen := []string{}
	for nodeID, state := range states {
		if workflowNodeStateTerminal(state) {
			continue
		}
		states[nodeID] = NodeRunCancelled
		frozen = append(frozen, nodeID)
	}
	if len(frozen) > 0 {
		encoded, err := marshalWorkflowJSON(states)
		if err != nil {
			return nil, err
		}
		res, err := tx.ExecContext(ctx,
			`UPDATE workflow_runs SET revision = revision + 1, node_states_json = ?, updated_at = ? WHERE id = ? AND revision = ?`,
			encoded, stamp, runID, revision)
		if err != nil {
			return nil, err
		}
		if affected, err := res.RowsAffected(); err == nil && affected == 0 {
			return nil, ErrRunConflict
		}
	}

	if len(openNodes) > 0 || len(frozen) > 0 {
		if _, err := appendWorkflowRunEventTx(ctx, tx, WorkflowRunEvent{
			RunID: runID, Type: WorkflowEventNodeCancelled,
			Payload: map[string]any{
				"reason":              "legacy_import_leftover",
				"cancelled_node_runs": openNodes,
				"frozen_projection":   frozen,
			},
			CreatedAt: now,
		}); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return openNodes, nil
}

// legacyImportProjectionOpen 报这条 Run 的投影里是否还有非终态节点。
func (s *TaskStore) legacyImportProjectionOpen(ctx context.Context, runID string) (bool, error) {
	var statesJSON string
	if err := s.db.QueryRowContext(ctx, `SELECT node_states_json FROM workflow_runs WHERE id = ?`, runID).Scan(&statesJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, ErrWorkflowRunNotFound
		}
		return false, err
	}
	states := map[string]string{}
	if strings.TrimSpace(statesJSON) != "" {
		if err := json.Unmarshal([]byte(statesJSON), &states); err != nil {
			return false, err
		}
	}
	for _, state := range states {
		if !workflowNodeStateTerminal(state) {
			return true, nil
		}
	}
	return false, nil
}
