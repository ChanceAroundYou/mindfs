package kanban

import (
	"context"
	"time"
)

// 验收项的落库层（Plan 2026-11-21 W1 T3）。写入只有一种语义：按 (task, run)
// 整体替换 —— Router 每一轮登记的是「这一轮做完的样子」，不是一份追加日志。

// ReplaceAcceptanceItems 用 items 整体替换某一轮 Run 的验收项。seq 从 1 开始，
// 保证收尾报告里的编号稳定且可读。
func (s *TaskStore) ReplaceAcceptanceItems(ctx context.Context, taskID, runID string, items []AcceptanceItemInput) ([]AcceptanceItem, error) {
	normalized, err := normalizeAcceptanceItems(items)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM task_acceptance_items WHERE task_id=? AND run_id=?`, taskID, runID); err != nil {
		return nil, err
	}
	out := make([]AcceptanceItem, 0, len(normalized))
	for index, item := range normalized {
		row := AcceptanceItem{
			ID:            acceptanceItemID(taskID, runID, index+1),
			TaskID:        taskID,
			RunID:         runID,
			NodeID:        item.NodeID,
			Seq:           index + 1,
			Text:          item.Text,
			EvidenceClass: item.EvidenceClass,
			Status:        AcceptanceStatusOpen,
			EvidenceRef:   item.EvidenceRef,
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO task_acceptance_items (id, task_id, run_id, node_id, seq, text, evidence_class, status, evidence_ref, rework_round, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)`,
			row.ID, row.TaskID, row.RunID, row.NodeID, row.Seq, row.Text,
			string(row.EvidenceClass), row.Status, row.EvidenceRef,
			row.CreatedAt.Format(time.RFC3339Nano), row.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, tx.Commit()
}

// ListAcceptanceItems 按 (task, run) 取验收项；runID 为空表示直连任务。
func (s *TaskStore) ListAcceptanceItems(ctx context.Context, taskID, runID string) ([]AcceptanceItem, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, task_id, run_id, node_id, seq, text, evidence_class, status, evidence_ref, rework_round, created_at, updated_at
FROM task_acceptance_items WHERE task_id=? AND run_id=? ORDER BY seq`, taskID, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AcceptanceItem
	for rows.Next() {
		var item AcceptanceItem
		var class, createdAt, updatedAt string
		if err := rows.Scan(&item.ID, &item.TaskID, &item.RunID, &item.NodeID, &item.Seq, &item.Text,
			&class, &item.Status, &item.EvidenceRef, &item.ReworkRound, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		item.EvidenceClass = AcceptanceEvidenceClass(class)
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
		out = append(out, item)
	}
	return out, rows.Err()
}

// BumpAcceptanceRework 把这一轮还开着的验收项的返工轮数 +1。只在证据门驳回
// （blockCompletion）时调用，纯审计：真正的补做上限由完成契约的 repairs 把关。
func (s *TaskStore) BumpAcceptanceRework(ctx context.Context, taskID, runID string) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE task_acceptance_items SET rework_round = rework_round + 1, updated_at = ?
WHERE task_id=? AND run_id=? AND status=?`,
		time.Now().UTC().Format(time.RFC3339Nano), taskID, runID, AcceptanceStatusOpen)
	return err
}

// MarkAcceptanceMet 在收尾成立时把这一轮全部验收项标成 met（进度证据）。
func (s *TaskStore) MarkAcceptanceMet(ctx context.Context, taskID, runID string) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE task_acceptance_items SET status = ?, updated_at = ?
WHERE task_id=? AND run_id=? AND status != ?`,
		AcceptanceStatusMet, time.Now().UTC().Format(time.RFC3339Nano),
		taskID, runID, AcceptanceStatusMet)
	return err
}
