package kanban

import (
	"context"
	"log"
	"strings"
)

// 「当前会话」回退（W4-3）。
//
// 对话式任务的主会话是新建直连回合时才记账的（SetTaskMainSessionKey），而工作流
// 节点在自己的会话里跑。于是 #68 这类任务明明在工作，main_session_key 却是空：
// 详情面板没有会话入口、轮询也没有落点。这里在**读取侧**兜底：主会话为空时，
// 用这个任务最近活跃的相关会话（活动行 / Run 共享会话 / 节点会话）当当前会话。
//
// 只在内存里补，不写库：显式绑定仍然只由 SetTaskMainSessionKey 发生；回退结果
// 随会话活跃度自然变化，不会把某个节点会话永久钉成主会话。

func (s *TaskStore) attachCurrentSessionKeys(ctx context.Context, tasks []Task) {
	if len(tasks) == 0 {
		return
	}
	missing := make(map[string]int)
	args := make([]any, 0, len(tasks))
	for i := range tasks {
		if strings.TrimSpace(tasks[i].MainSessionKey) != "" {
			continue
		}
		missing[tasks[i].ID] = i
		args = append(args, tasks[i].ID)
	}
	if len(args) == 0 {
		return
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(args)), ",")
	query := `SELECT task_id, session_key FROM (
		SELECT task_id, session_key, updated_at AS at FROM task_activities
		WHERE task_id IN (` + placeholders + `) AND TRIM(session_key) <> ''
		UNION ALL
		SELECT task_id, session_key, updated_at AS at FROM workflow_runs
		WHERE task_id IN (` + placeholders + `) AND TRIM(session_key) <> ''
		UNION ALL
		SELECT r.task_id, n.session_key, n.updated_at AS at FROM workflow_node_runs n
		JOIN workflow_runs r ON r.id = n.run_id
		WHERE r.task_id IN (` + placeholders + `) AND TRIM(n.session_key) <> ''
	) ORDER BY at DESC`
	queryArgs := make([]any, 0, len(args)*3)
	for i := 0; i < 3; i++ {
		queryArgs = append(queryArgs, args...)
	}
	rows, err := s.db.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		log.Printf("[kanban] current session fallback: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var taskID, sessionKey string
		if err := rows.Scan(&taskID, &sessionKey); err != nil {
			log.Printf("[kanban] current session fallback scan: %v", err)
			return
		}
		index, ok := missing[taskID]
		if !ok {
			continue
		}
		tasks[index].MainSessionKey = strings.TrimSpace(sessionKey)
		delete(missing, taskID)
	}
}
