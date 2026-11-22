package kanban

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// TaskIDForSession 反查绑定了该会话的任务。
//
// TaskIDForMainSession 只看 tasks.main_session_key，覆盖不了节点会话与委派会话：
// 一轮工作流跑在独立会话里（workflow_runs.session_key / workflow_node_runs 的
// session_key），Agent 也可能在别的会话里替任务干活（task_activities.session_key）。
// 会话回合结算（turn_settled）要覆盖「任务绑定的任何会话」，所以反查必须走全这
// 四条绑定，而不是只认主会话。
//
// 顺序即优先级：主会话 → 本轮 Run 的共享会话 → 节点会话 → 活动行。多条命中时
// 取新建时间最晚的那条（最可能是这一轮真正的归属）。找不到返回空串，不报错。
func (s *TaskStore) TaskIDForSession(ctx context.Context, sessionKey string) (string, error) {
	if s == nil {
		return "", nil
	}
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return "", nil
	}
	queries := []string{
		`SELECT id FROM tasks WHERE main_session_key = ? LIMIT 1`,
		`SELECT task_id FROM workflow_runs WHERE session_key = ? ORDER BY created_at DESC LIMIT 1`,
		`SELECT r.task_id FROM workflow_node_runs n JOIN workflow_runs r ON r.id = n.run_id
		 WHERE n.session_key = ? ORDER BY n.created_at DESC LIMIT 1`,
		`SELECT task_id FROM task_activities WHERE session_key = ? ORDER BY created_at DESC LIMIT 1`,
	}
	for _, query := range queries {
		var taskID string
		err := s.db.QueryRowContext(ctx, query, sessionKey).Scan(&taskID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return "", err
		}
		if taskID = strings.TrimSpace(taskID); taskID != "" {
			return taskID, nil
		}
	}
	return "", nil
}
