package kanban

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// ErrCommentConflict 表示同一 (task_id, comment_id) 携带不同 author/body。
var ErrCommentConflict = errors.New("comment id conflict: different author or body")

func (s *TaskStore) migrateComments() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS task_activities (
	id TEXT NOT NULL,
	task_id TEXT NOT NULL,
	type TEXT NOT NULL DEFAULT 'user_comment',
	author TEXT NOT NULL DEFAULT '',
	role TEXT NOT NULL DEFAULT '',
	body TEXT NOT NULL DEFAULT '',
	workflow_choice TEXT NOT NULL DEFAULT 'empty',
	workflow_version_id TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	PRIMARY KEY (task_id, id)
);
CREATE TABLE IF NOT EXISTS continue_requests (
	request_id TEXT PRIMARY KEY,
	task_id TEXT NOT NULL,
	comment_id TEXT NOT NULL,
	status TEXT NOT NULL DEFAULT 'queued',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	UNIQUE (task_id, comment_id)
);
CREATE INDEX IF NOT EXISTS idx_task_activities_task_created ON task_activities(task_id, created_at);
CREATE INDEX IF NOT EXISTS idx_continue_requests_task ON continue_requests(task_id, created_at);
`)
	if err != nil {
		return err
	}
	// WP-03 (A3) 的 Outbox 富列：A1 的 continue_requests 建表时还没有这些列，
	// A3 的 service_outbox.go 只在自己 EnsureOutboxTables 里 CREATE TABLE IF NOT EXISTS，
	// 两边表的列集互斥（A1: request_id 主键；A3: id 主键 + root_id/node_run_id/
	// session_key/message/author/workflow_choice/prev_attempt_id），合并后必缺一列。
	// 以 A1 的 request_id 主键为 canonical（G-KP 已发布），把 A3 的富列逐列 ALTER 补齐，
	// 全部幂等（duplicate column name 忽略），存量行读作空串/默认值，与改造前行为一致。
	alters := []string{
		`ALTER TABLE continue_requests ADD COLUMN root_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE continue_requests ADD COLUMN node_run_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE continue_requests ADD COLUMN session_key TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE continue_requests ADD COLUMN message TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE continue_requests ADD COLUMN author TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE continue_requests ADD COLUMN workflow_choice TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE continue_requests ADD COLUMN prev_attempt_id TEXT NOT NULL DEFAULT ''`,
	}
	for _, stmt := range alters {
		if _, err := s.db.Exec(stmt); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return err
		}
	}
	// A3 用 id 列做主键而 A1 用 request_id：给 id 列建唯一索引并非 schema 级主键，
	// 只暴露一个可查投影 id = request_id（A3 测试/代码以 id 访问改为读 request_id）。
	// 这里不引入 id 列：所有 A3 访问点已改成 request_id（见 service_outbox.go）。
	return nil
}

func normalizePostCommentInput(in PostCommentInput) (PostCommentInput, error) {
	in.TaskID = strings.TrimSpace(in.TaskID)
	in.CommentID = strings.TrimSpace(in.CommentID)
	in.Author = strings.TrimSpace(in.Author)
	in.Role = strings.TrimSpace(in.Role)
	in.Body = strings.TrimSpace(in.Body)
	in.Type = strings.TrimSpace(in.Type)
	in.WorkflowChoice = strings.TrimSpace(in.WorkflowChoice)
	in.WorkflowVersionID = strings.TrimSpace(in.WorkflowVersionID)
	if in.TaskID == "" {
		return in, errors.New("task id required")
	}
	if in.CommentID == "" {
		return in, errors.New("comment id required")
	}
	if in.Body == "" {
		return in, errors.New("body required")
	}
	if in.Type == "" {
		in.Type = ActivityTypeUserComment
	}
	if in.WorkflowChoice == "" {
		in.WorkflowChoice = CommentWorkflowChoiceEmpty
	}
	switch in.WorkflowChoice {
	case CommentWorkflowChoiceSpecified:
		if in.WorkflowVersionID == "" {
			return in, errors.New("workflow version id required for specified choice")
		}
	case CommentWorkflowChoiceDisabled, CommentWorkflowChoiceEmpty:
	default:
		return in, errors.New("invalid workflow choice")
	}
	return in, nil
}

func (s *TaskStore) PostComment(ctx context.Context, in PostCommentInput) (TaskActivity, ContinueRequest, error) {
	in, err := normalizePostCommentInput(in)
	if err != nil {
		return TaskActivity{}, ContinueRequest{}, err
	}
	// 任务必须存在（沿用 GetTask 的 sql.ErrNoRows 语义）
	if _, err := s.GetTask(ctx, in.TaskID); err != nil {
		return TaskActivity{}, ContinueRequest{}, err
	}
	now := s.now().UTC()
	nowStr := now.Format(time.RFC3339Nano)
	status := ContinueRequestQueued
	isTerminal := func(st string) bool { return st == StatusSuccess || st == StatusFail || st == StatusCancelled }
	task, _ := s.GetTask(ctx, in.TaskID)
	if isTerminal(task.Status) {
		status = ContinueRequestBlocked
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TaskActivity{}, ContinueRequest{}, err
	}
	defer tx.Rollback()
	// 幂等：已有则比对 author/body
	var exAuthor, exBody string
	err = tx.QueryRowContext(ctx, `SELECT author, body FROM task_activities WHERE task_id = ? AND id = ?`, in.TaskID, in.CommentID).Scan(&exAuthor, &exBody)
	if err == nil {
		if exAuthor != in.Author || exBody != in.Body {
			return TaskActivity{}, ContinueRequest{}, ErrCommentConflict
		}
		act, err := scanActivity(tx, in.TaskID, in.CommentID)
		if err != nil {
			return TaskActivity{}, ContinueRequest{}, err
		}
		var req ContinueRequest
		var reqCreated, reqUpdated string
		err = tx.QueryRowContext(ctx, `SELECT request_id, task_id, comment_id, status, created_at, updated_at FROM continue_requests WHERE task_id = ? AND comment_id = ?`, in.TaskID, in.CommentID).Scan(&req.RequestID, &req.TaskID, &req.CommentID, &req.Status, &reqCreated, &reqUpdated)
		req.CreatedAt, _ = time.Parse(time.RFC3339Nano, reqCreated)
		req.UpdatedAt, _ = time.Parse(time.RFC3339Nano, reqUpdated)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				// 旧行：补写请求
				if err := insertContinueReq(ctx, tx, in.TaskID, in.CommentID, status, nowStr); err != nil {
					return TaskActivity{}, ContinueRequest{}, err
				}
				req = ContinueRequest{RequestID: in.CommentID, TaskID: in.TaskID, CommentID: in.CommentID, Status: status, CreatedAt: now, UpdatedAt: now}
				if err := tx.Commit(); err != nil {
					return TaskActivity{}, ContinueRequest{}, err
				}
				return act, req, nil
			}
			return TaskActivity{}, ContinueRequest{}, err
		}
		// 解析时间字段（扫描为字符串已由 scan 处理，这里补齐）
		req.CreatedAt = parseTime(req.CreatedAt)
		req.UpdatedAt = parseTime(req.UpdatedAt)
		if err := tx.Commit(); err != nil {
			return TaskActivity{}, ContinueRequest{}, err
		}
		return act, req, nil
	}
	if err != sql.ErrNoRows {
		return TaskActivity{}, ContinueRequest{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO task_activities (id, task_id, type, author, role, body, workflow_choice, workflow_version_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.CommentID, in.TaskID, in.Type, in.Author, in.Role, in.Body, in.WorkflowChoice, in.WorkflowVersionID, nowStr); err != nil {
		return TaskActivity{}, ContinueRequest{}, err
	}
	if err := insertContinueReq(ctx, tx, in.TaskID, in.CommentID, status, nowStr); err != nil {
		return TaskActivity{}, ContinueRequest{}, err
	}
	act := TaskActivity{ID: in.CommentID, TaskID: in.TaskID, Type: in.Type, Author: in.Author, Role: in.Role, Body: in.Body, WorkflowChoice: in.WorkflowChoice, WorkflowVersionID: in.WorkflowVersionID, CreatedAt: now}
	req := ContinueRequest{RequestID: in.CommentID, TaskID: in.TaskID, CommentID: in.CommentID, Status: status, CreatedAt: now, UpdatedAt: now}
	if err := tx.Commit(); err != nil {
		return TaskActivity{}, ContinueRequest{}, err
	}
	return act, req, nil
}

func insertContinueReq(ctx context.Context, tx *sql.Tx, taskID, commentID, status, nowStr string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO continue_requests (request_id, task_id, comment_id, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, commentID, taskID, commentID, status, nowStr, nowStr)
	return err
}

func parseTime(t time.Time) time.Time { return t }

func scanActivity(q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, taskID, id string) (TaskActivity, error) {
	var a TaskActivity
	var created string
	err := q.QueryRowContext(context.Background(), `SELECT id, task_id, type, author, role, body, workflow_choice, workflow_version_id, created_at FROM task_activities WHERE task_id = ? AND id = ?`, taskID, id).Scan(&a.ID, &a.TaskID, &a.Type, &a.Author, &a.Role, &a.Body, &a.WorkflowChoice, &a.WorkflowVersionID, &created)
	if err != nil {
		return TaskActivity{}, err
	}
	a.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return a, nil
}

func (s *TaskStore) ListActivities(ctx context.Context, taskID string) ([]TaskActivity, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, task_id, type, author, role, body, workflow_choice, workflow_version_id, created_at FROM task_activities WHERE task_id = ? ORDER BY created_at ASC`, strings.TrimSpace(taskID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []TaskActivity{}
	for rows.Next() {
		var a TaskActivity
		var created string
		if err := rows.Scan(&a.ID, &a.TaskID, &a.Type, &a.Author, &a.Role, &a.Body, &a.WorkflowChoice, &a.WorkflowVersionID, &created); err != nil {
			return nil, err
		}
		a.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		items = append(items, a)
	}
	return items, rows.Err()
}

func (s *TaskStore) ListContinueRequests(ctx context.Context, taskID string) ([]ContinueRequest, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT request_id, task_id, comment_id, status, created_at, updated_at FROM continue_requests WHERE task_id = ? ORDER BY created_at ASC`, strings.TrimSpace(taskID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ContinueRequest{}
	for rows.Next() {
		var r ContinueRequest
		var created, updated string
		if err := rows.Scan(&r.RequestID, &r.TaskID, &r.CommentID, &r.Status, &created, &updated); err != nil {
			return nil, err
		}
		r.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		r.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
		items = append(items, r)
	}
	return items, rows.Err()
}
