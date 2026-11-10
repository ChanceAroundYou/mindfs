package kanban

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// NodeRunSource 说明一条 NodeRun 身份由哪张表承载。
//
// canonical 是 workflow_node_runs（(run_id,node_id,loop_path,iteration,attempt)
// 唯一键 + id 主键，承载 Workflow Runtime 的执行身份）；legacy 是 WP-03 的
// node_runs 影子行（只有 task 级 session 绑定，没有 run/node 归属）。两者都
// 必须可读：旧留言/恢复数据不能被新运行时丢掉。
type NodeRunSource string

const (
	NodeRunSourceWorkflow NodeRunSource = "workflow"
	NodeRunSourceLegacy   NodeRunSource = "legacy"
)

// ResolveNodeRun 按「canonical 优先、legacy 兜底」解析 NodeRun 身份。
// 新运行时只应写 workflow_node_runs；legacy 行保持只读兼容。
func ResolveNodeRun(ctx context.Context, store *TaskStore, nodeRunID string) (NodeRunRecord, NodeRunSource, error) {
	if store == nil {
		return NodeRunRecord{}, "", errors.New("task store required")
	}
	id := strings.TrimSpace(nodeRunID)
	if id == "" {
		return NodeRunRecord{}, "", ErrOutboxNotFound
	}
	if rec, found, err := resolveWorkflowNodeRun(ctx, store, id); err != nil {
		return NodeRunRecord{}, "", err
	} else if found {
		return rec, NodeRunSourceWorkflow, nil
	}
	rec, err := getLegacyNodeRun(ctx, store, id)
	if err != nil {
		return NodeRunRecord{}, "", err
	}
	return rec, NodeRunSourceLegacy, nil
}

func resolveWorkflowNodeRun(ctx context.Context, store *TaskStore, id string) (NodeRunRecord, bool, error) {
	var rec NodeRunRecord
	var createdAt, updatedAt string
	err := store.db.QueryRowContext(ctx, `
SELECT n.id, r.task_id, COALESCE(t.root_id,''), n.session_key, n.status, n.attempt, n.created_at, n.updated_at
FROM workflow_node_runs n
JOIN workflow_runs r ON r.id = n.run_id
LEFT JOIN tasks t ON t.id = r.task_id
WHERE n.id=?`, id).Scan(
		&rec.ID, &rec.TaskID, &rec.RootID, &rec.SessionKey, &rec.Status, &rec.Attempt, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return NodeRunRecord{}, false, nil
	}
	if err != nil {
		return NodeRunRecord{}, false, err
	}
	rec.CreatedAt = parseOutboxTime(createdAt)
	rec.UpdatedAt = parseOutboxTime(updatedAt)
	return rec, true, nil
}

func getLegacyNodeRun(ctx context.Context, store *TaskStore, id string) (NodeRunRecord, error) {
	var rec NodeRunRecord
	var createdAt, updatedAt string
	err := store.db.QueryRowContext(ctx,
		`SELECT id, task_id, root_id, session_key, status, attempt, created_at, updated_at FROM node_runs WHERE id=?`, id).Scan(
		&rec.ID, &rec.TaskID, &rec.RootID, &rec.SessionKey, &rec.Status, &rec.Attempt, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return NodeRunRecord{}, ErrOutboxNotFound
	}
	if err != nil {
		return NodeRunRecord{}, err
	}
	rec.CreatedAt = parseOutboxTime(createdAt)
	rec.UpdatedAt = parseOutboxTime(updatedAt)
	return rec, nil
}
