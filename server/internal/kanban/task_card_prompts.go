package kanban

import (
	"context"
	"log"
	"strings"
)

// TaskRouterAsk 是任务卡上的 Router 提问角标。
//
// 判据与会话 worker 透传的 ask 不同：
//   - Router 自己派发的提问卡由 applyRouteAskUser 落库，session_key 为空（Router
//     不在任何会话里），且不写 aux_flags.ask_user_waiting；
//   - worker 透传的 ask 是会话里的 tool call 提问卡，session_key 非空，卡片走
//     AuxFlags.AskUserWaiting。
//
// 两者同时存在时各显示各的：这里只报 Router 侧，永不读写 aux flag。
type TaskRouterAsk struct {
	Count int `json:"count"`
	// Since 是本任务最早一张仍未回答的 Router 提问卡时间。
	Since string `json:"since,omitempty"`
	// Question 取最新一张提问卡的正文，供卡片 tooltip 直接显示问题。
	Question string `json:"question,omitempty"`
}

// TaskLatestAttention 是任务卡上的最新提醒。Body 就是对话里那条提醒正文，
// Severity 沿用 attention 行的 info/warning/error（存量行为空，前端按 info 着色）。
type TaskLatestAttention struct {
	Body      string `json:"body"`
	Severity  string `json:"severity,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

// attachCardPrompts 给一批任务补卡片派生字段，一次查询覆盖整页。
func (s *TaskStore) attachCardPrompts(ctx context.Context, tasks []Task) {
	if s == nil || len(tasks) == 0 {
		return
	}
	s.attachRouterAsk(ctx, tasks)
	s.attachLatestAttention(ctx, tasks)
}

// attachRouterAsk 只认 session_key 为空的 waiting_user 提问卡。查询口径与
// ActivityStore.OpenRouterQuestionsByTask 保持一致 —— 那里的注释已说明为什么
// 空 session_key 是 Router 卡与会话卡的分界，这里不另造判据。
func (s *TaskStore) attachRouterAsk(ctx context.Context, tasks []Task) {
	index, placeholders, args := taskPlaceholders(tasks, 2)
	args = append(args, ActivityTypeQuestion, ActivityStatusWaitingUser)
	rows, err := s.db.QueryContext(ctx,
		`SELECT task_id, body, created_at FROM task_activities
		 WHERE task_id IN (`+strings.Join(placeholders, ",")+`)
		   AND type = ? AND status = ? AND COALESCE(session_key, '') = ''
		 ORDER BY created_at ASC, rowid ASC`, args...)
	if err != nil {
		log.Printf("[kanban] attach router ask: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var taskID, body, createdAt string
		if err := rows.Scan(&taskID, &body, &createdAt); err != nil {
			log.Printf("[kanban] scan router ask: %v", err)
			return
		}
		i, ok := index[taskID]
		if !ok {
			continue
		}
		if tasks[i].RouterAsk == nil {
			tasks[i].RouterAsk = &TaskRouterAsk{Since: createdAt}
		}
		tasks[i].RouterAsk.Count++
		// 行按 created_at 升序；最后写入的正文就是最新那张提问卡。
		tasks[i].RouterAsk.Question = strings.TrimSpace(body)
	}
	if err := rows.Err(); err != nil {
		log.Printf("[kanban] attach router ask: %v", err)
	}
}

// attachLatestAttention 取每个任务最新一条非 hidden 的提醒。存量行没有 severity，
// 原样透传空串，让前端唯一的归一口径（attentionSeverityColor）决定颜色。
func (s *TaskStore) attachLatestAttention(ctx context.Context, tasks []Task) {
	index, placeholders, args := taskPlaceholders(tasks, 1)
	args = append(args, ActivityTypeAttention)
	rows, err := s.db.QueryContext(ctx,
		`SELECT task_id, body, severity, created_at FROM task_activities
		 WHERE task_id IN (`+strings.Join(placeholders, ",")+`)
		   AND type = ? AND hidden = 0
		 ORDER BY created_at DESC, rowid DESC`, args...)
	if err != nil {
		log.Printf("[kanban] attach latest attention: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var taskID, body, severity, createdAt string
		if err := rows.Scan(&taskID, &body, &severity, &createdAt); err != nil {
			log.Printf("[kanban] scan latest attention: %v", err)
			return
		}
		i, ok := index[taskID]
		if !ok || tasks[i].LatestAttention != nil {
			continue
		}
		tasks[i].LatestAttention = &TaskLatestAttention{
			Body:      strings.TrimSpace(body),
			Severity:  strings.TrimSpace(severity),
			CreatedAt: createdAt,
		}
	}
	if err := rows.Err(); err != nil {
		log.Printf("[kanban] attach latest attention: %v", err)
	}
}

func taskPlaceholders(tasks []Task, extraArgs int) (map[string]int, []string, []any) {
	index := make(map[string]int, len(tasks))
	placeholders := make([]string, 0, len(tasks))
	args := make([]any, 0, len(tasks)+extraArgs)
	for i, task := range tasks {
		index[task.ID] = i
		placeholders = append(placeholders, "?")
		args = append(args, task.ID)
	}
	return index, placeholders, args
}
