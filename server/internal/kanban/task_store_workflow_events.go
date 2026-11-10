package kanban

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// D13（2026-11-10 用户确认）：最小 typed、append-only 的 run 事件账本。
// 每个状态迁移与投影更新同事务写一条事件，seq 在 run 内串行分配。
// 投影（workflow_runs/workflow_node_runs）可重建，账本是恢复与审计的顺序真相。
const (
	WorkflowEventNodeQueued       = "node_queued"
	WorkflowEventNodeStarted      = "node_started"
	WorkflowEventNodeSessionBound = "node_session_bound"
	// WorkflowEventNodeAgentDone 是「Agent 回合已返回、节点终态尚未落库」的持久证据。
	// 重启恢复靠它区分「agent 已经跑完、可能只差 merge」与「副作用不明」：
	// 前者可以幂等续 merge，后者只能标 unknown，绝不重放。
	WorkflowEventNodeAgentDone = "node_agent_done"
	// WorktreeBound 记录 NodeRun 与物理工作树/分支的归属（D16）。共享主树不写
	// 这条事件；只有节点自己创建/认领的独立树才需要持久关联。
	WorkflowEventNodeWorktreeBound = "node_worktree_bound"
	WorkflowEventNodeSucceeded     = "node_succeeded"
	WorkflowEventNodeBlocked       = "node_blocked"
	WorkflowEventNodeSilent        = "node_silent"
	WorkflowEventNodeUnknown       = "node_unknown"
	WorkflowEventNodeFailed        = "node_failed"
	WorkflowEventNodeSkipped       = "node_skipped"
	WorkflowEventNodeNotified      = "node_notified"
	// Retry/restart/cancellation keep the original evidence: requeue means a
	// fresh turn on the same attempt row (crash before send), retried means a new
	// attempt linked by prev_attempt_id, cancelled means the operator stopped it.
	WorkflowEventNodeRequeued  = "node_requeued"
	WorkflowEventNodeRetried   = "node_retried"
	WorkflowEventNodeCancelled = "node_cancelled"
	WorkflowEventRunPaused     = "run_paused"
	WorkflowEventRunResumed    = "run_resumed"
	WorkflowEventRunCancelled  = "run_cancelled"
	// D7: board verbs map to execution semantics. Complete/Fail are the human
	// overrides; they stop open work exactly like cancel but record the
	// operator's intended outcome instead of a cancellation.
	WorkflowEventRunCompleted = "run_completed"
	WorkflowEventRunFailed    = "run_failed"
	// HumanGate 是唯一需要人决策的节点。等待与决定各记一条 typed 事件；
	// 决定事件不会写成普通的 node_succeeded，审计时才看得出来是谁放行的。
	WorkflowEventHumanWaiting  = "human_waiting"
	WorkflowEventHumanApproved = "human_approved"
	WorkflowEventHumanRejected = "human_rejected"
	// Loop 容器的迭代推进不是节点终态，单独记账：iteration 记录「第 N 轮开始」，
	// exited 记录退出表达式为真，limit 记录达到上限或语义无法解释而暂停。
	WorkflowEventLoopIteration = "loop_iteration"
	WorkflowEventLoopExited    = "loop_exited"
	WorkflowEventLoopLimit     = "loop_limit"
)

type WorkflowRunEvent struct {
	RunID     string         `json:"run_id"`
	Seq       int64          `json:"seq"`
	Type      string         `json:"type"`
	NodeID    string         `json:"node_id,omitempty"`
	NodeRunID string         `json:"node_run_id,omitempty"`
	Attempt   int            `json:"attempt,omitempty"`
	Payload   map[string]any `json:"payload,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

func (s *TaskStore) migrateWorkflowRunEvents() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS workflow_run_events (
	run_id TEXT NOT NULL,
	seq INTEGER NOT NULL,
	type TEXT NOT NULL,
	node_id TEXT NOT NULL DEFAULT '',
	node_run_id TEXT NOT NULL DEFAULT '',
	attempt INTEGER NOT NULL DEFAULT 0,
	payload_json TEXT NOT NULL DEFAULT '{}',
	created_at TEXT NOT NULL,
	PRIMARY KEY (run_id, seq)
);
CREATE INDEX IF NOT EXISTS idx_workflow_run_events_node ON workflow_run_events(run_id, node_id, node_run_id, seq);
`)
	return err
}

// appendWorkflowRunEventTx must run inside the same transaction as the
// projection update it describes. SetMaxOpenConns(1) serializes writers, so
// MAX(seq)+1 inside the tx cannot race another writer.
func appendWorkflowRunEventTx(ctx context.Context, tx *sql.Tx, ev WorkflowRunEvent) (WorkflowRunEvent, error) {
	if ev.RunID == "" || ev.Type == "" {
		return WorkflowRunEvent{}, errors.New("workflow event requires run id and type")
	}
	payload, err := marshalWorkflowJSON(ev.Payload)
	if err != nil {
		return WorkflowRunEvent{}, err
	}
	var seq int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0)+1 FROM workflow_run_events WHERE run_id = ?`, ev.RunID).Scan(&seq); err != nil {
		return WorkflowRunEvent{}, err
	}
	created := time.Now().UTC()
	if !ev.CreatedAt.IsZero() {
		created = ev.CreatedAt.UTC()
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO workflow_run_events(run_id,seq,type,node_id,node_run_id,attempt,payload_json,created_at) VALUES(?,?,?,?,?,?,?,?)`,
		ev.RunID, seq, ev.Type, ev.NodeID, ev.NodeRunID, ev.Attempt, payload, created.Format(time.RFC3339Nano)); err != nil {
		return WorkflowRunEvent{}, err
	}
	ev.Seq = seq
	ev.CreatedAt = created
	return ev, nil
}

// AppendWorkflowRunEvent writes one event in its own transaction. Callers that
// also mutate a projection must use the same transaction instead (see
// transitionWorkflowNodeRun).
func (s *TaskStore) AppendWorkflowRunEvent(ctx context.Context, ev WorkflowRunEvent) (WorkflowRunEvent, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WorkflowRunEvent{}, err
	}
	defer tx.Rollback()
	out, err := appendWorkflowRunEventTx(ctx, tx, ev)
	if err != nil {
		return WorkflowRunEvent{}, err
	}
	if err := tx.Commit(); err != nil {
		return WorkflowRunEvent{}, err
	}
	return out, nil
}

func (s *TaskStore) ListWorkflowRunEvents(ctx context.Context, runID string) ([]WorkflowRunEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT run_id,seq,type,node_id,node_run_id,attempt,payload_json,created_at FROM workflow_run_events WHERE run_id = ? ORDER BY seq ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []WorkflowRunEvent{}
	for rows.Next() {
		var ev WorkflowRunEvent
		var payload, created string
		if err := rows.Scan(&ev.RunID, &ev.Seq, &ev.Type, &ev.NodeID, &ev.NodeRunID, &ev.Attempt, &payload, &created); err != nil {
			return nil, err
		}
		if ev.Payload, err = unmarshalWorkflowJSON[map[string]any](payload); err != nil {
			return nil, err
		}
		ev.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		items = append(items, ev)
	}
	return items, rows.Err()
}
