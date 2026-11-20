package kanban

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// 完成记录（Plan 2026-11-20 Task 5）。
//
// 一张任务一行（主键 task_id）：记录最近一次按完成契约核对的结果，供界面展示
// 「差哪几项」。历次核对的过程留在系统行与 task_events 里，这里只保留当前结论。
const (
	CompletionStatusCompleted = "completed"
	CompletionStatusBlocked   = "blocked"
)

// TaskCompletion 是一次收尾核对的快照。
//
// ReportJSON 存整个 CompletionReport（逐项 pass/detail + 契约哈希），
// VerifierKey/VerifierVerdict 留给无契约时的独立复核（异步，不占 Router 回合）。
type TaskCompletion struct {
	TaskID          string
	RunID           string
	Status          string
	ReportJSON      string
	VerifierKey     string
	VerifierVerdict string
	DecisionID      string
	RepairsUsed     int
	UpdatedAt       time.Time
}

// UpsertTaskCompletion 写入或覆盖任务当前的完成记录。
func (s *TaskStore) UpsertTaskCompletion(ctx context.Context, c TaskCompletion) error {
	updated := c.UpdatedAt.UTC()
	if updated.IsZero() {
		updated = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO task_completion (task_id, run_id, status, report_json, verifier_key, verifier_verdict, decision_id, repairs_used, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(task_id) DO UPDATE SET
	run_id = excluded.run_id,
	status = excluded.status,
	report_json = excluded.report_json,
	verifier_key = excluded.verifier_key,
	verifier_verdict = excluded.verifier_verdict,
	decision_id = excluded.decision_id,
	repairs_used = excluded.repairs_used,
	updated_at = excluded.updated_at`,
		c.TaskID, c.RunID, c.Status, c.ReportJSON, c.VerifierKey, c.VerifierVerdict, c.DecisionID, c.RepairsUsed, updated.Format(time.RFC3339Nano))
	return err
}

// GetTaskCompletion 读取任务当前的完成记录；没有记录时 found=false。
func (s *TaskStore) GetTaskCompletion(ctx context.Context, taskID string) (TaskCompletion, bool, error) {
	var (
		c       TaskCompletion
		updated string
	)
	err := s.db.QueryRowContext(ctx, `
SELECT task_id, run_id, status, report_json, verifier_key, verifier_verdict, decision_id, repairs_used, updated_at
FROM task_completion WHERE task_id = ?`, taskID).
		Scan(&c.TaskID, &c.RunID, &c.Status, &c.ReportJSON, &c.VerifierKey, &c.VerifierVerdict, &c.DecisionID, &c.RepairsUsed, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return TaskCompletion{}, false, nil
	}
	if err != nil {
		return TaskCompletion{}, false, err
	}
	if ts, parseErr := time.Parse(time.RFC3339Nano, updated); parseErr == nil {
		c.UpdatedAt = ts
	}
	return c, true, nil
}
