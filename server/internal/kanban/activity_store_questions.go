package kanban

import (
	"context"
	"errors"
	"strings"
	"time"
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

// SetQuestionCard 把一条已经落库的等待回复原地升级成真正的提问卡。
//
// 为什么要升级而不是另建一张：applyRouteAskUser 用的就是这条回复行（事件回合的
// effect ID 由决策与序号决定，重放必须落成同一行）。另建一张卡会让每次重放多出
// 一条可见提问，且旧的等待卡会一直挡着输入框。改 type 与 payload 是原地升级，
// 重放再跑一次仍是同一行、同一份内容。
func (s *ActivityStore) SetQuestionCard(ctx context.Context, id, payloadJSON string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("activity id required")
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE task_activities SET type = ?, payload_json = ?, updated_at = ? WHERE id = ?`,
		ActivityTypeQuestion, payloadJSON, time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return err
	}
	return ensureActivityUpdated(res, id)
}

// OpenRouterQuestionsByTask 返回该任务里 Router 自己发起、仍停在 waiting_user
// 的提问卡。
//
// session_key 为空是判据：会话里的提问卡属于某个 tool call，它们的收口由会话
// 事件驱动，不能被新一张 Router 卡顺手关掉（那会让还在那个会话里等回答的
// Agent 永远收不到答案）。
func (s *ActivityStore) OpenRouterQuestionsByTask(ctx context.Context, taskID string) ([]*TaskActivity, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+activitySelectColumns+` FROM task_activities
		 WHERE task_id = ? AND type = ? AND status = ? AND COALESCE(session_key, '') = ''
		 ORDER BY created_at ASC, rowid ASC`,
		strings.TrimSpace(taskID), ActivityTypeQuestion, ActivityStatusWaitingUser)
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
