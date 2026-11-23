package kanban

import (
	"context"
)

// 这一组查询只服务「启动时收口已被重问取代的提问卡」：Agent 换了新的
// tool_use_id 重问之后，旧卡的 (session_key, tool_use_id) 再也不会被任何路径
// 命中，只会在等待态上一直挡着任务对话的输入框（而前端锁输入框读的是最早的
// 那张卡，见现场 #104）。

// ListSupersededOpenQuestions 返回同一会话里存在更晚提问、自己仍停在
// waiting_user 的提问卡。
//
// 「更晚的提问」只认 type=question：同会话里后到的 HumanGate 或别的等待态
// 活动不代表 AskUserQuestion 被重问过，旧卡仍然有效。
func (s *ActivityStore) ListSupersededOpenQuestions(ctx context.Context) ([]*TaskActivity, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+activitySelectColumns+` FROM task_activities q
		 WHERE q.type = ? AND q.status = ? AND COALESCE(q.session_key, '') <> ''
		   AND EXISTS (
		     SELECT 1 FROM task_activities n
		      WHERE n.session_key = q.session_key AND n.type = ? AND n.id <> q.id
		        AND (n.created_at > q.created_at
		             OR (n.created_at = q.created_at AND n.rowid > q.rowid))
		   )
		 ORDER BY q.created_at ASC, q.rowid ASC`,
		ActivityTypeQuestion, ActivityStatusWaitingUser, ActivityTypeQuestion)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []*TaskActivity{}
	for rows.Next() {
		activity, err := scanActivityRecord(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, activity)
	}
	return items, rows.Err()
}
