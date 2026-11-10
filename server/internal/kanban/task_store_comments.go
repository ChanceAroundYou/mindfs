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
		// D13/Task 3 Activity 关联：agent_reply/attention 要能指回产生它的 Run/NodeRun。
		// 旧行与 legacy 留言留空串，读取端保持兼容。
		`ALTER TABLE task_activities ADD COLUMN run_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE task_activities ADD COLUMN node_run_id TEXT NOT NULL DEFAULT ''`,
	}
	for _, stmt := range alters {
		if _, err := s.db.Exec(stmt); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return err
		}
	}
	// A3 用 id 列做主键而 A1 用 request_id：给 id 列建唯一索引并非 schema 级主键，
	// 只暴露一个可查投影 id = request_id（A3 测试/代码以 id 访问改为读 request_id）。
	// 这里不引入 id 列：所有 A3 访问点已改成 request_id（见 service_outbox.go）。
	//
	// outbox_items/node_runs 是留言投递的持久投影；PostComment 在绑定事务里
	// 直接写它，所以旧库也必须在迁移阶段建好，不能等到运行时才懒建。
	return EnsureOutboxTables(context.Background(), s.db)
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
	in.ExpectedNodeRunID = strings.TrimSpace(in.ExpectedNodeRunID)
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
		req, scanErr := scanContinueRequest(tx.QueryRowContext(ctx,
			`SELECT request_id, task_id, comment_id, status, node_run_id, session_key, created_at, updated_at FROM continue_requests WHERE task_id = ? AND comment_id = ?`,
			in.TaskID, in.CommentID))
		if scanErr != nil {
			if errors.Is(scanErr, sql.ErrNoRows) {
				// 旧行：补写请求。历史留言不重新判定绑定：绑定必须停在首次
				// 提交那一刻，重试不得把它改投到后来启动的节点。
				if err := insertContinueReq(ctx, tx, in.TaskID, in.CommentID, status, in.Body, in.Author, nowStr); err != nil {
					return TaskActivity{}, ContinueRequest{}, err
				}
				if err := upsertCommentOutbox(ctx, tx, in.TaskID, in.CommentID, status, "", "", nowStr); err != nil {
					return TaskActivity{}, ContinueRequest{}, err
				}
				req = ContinueRequest{RequestID: in.CommentID, TaskID: in.TaskID, CommentID: in.CommentID, Status: status, CreatedAt: now, UpdatedAt: now}
				if err := tx.Commit(); err != nil {
					return TaskActivity{}, ContinueRequest{}, err
				}
				return act, req, nil
			}
			return TaskActivity{}, ContinueRequest{}, scanErr
		}
		// Binding is frozen at first submission. A retry naming a different
		// target is an explicit conflict, never a silent rebind.
		if e := strings.TrimSpace(in.ExpectedNodeRunID); e != "" && req.NodeRunID != "" && e != req.NodeRunID {
			return TaskActivity{}, ContinueRequest{}, ErrCommentBindingConflict
		}
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
	if err := insertContinueReq(ctx, tx, in.TaskID, in.CommentID, status, in.Body, in.Author, nowStr); err != nil {
		return TaskActivity{}, ContinueRequest{}, err
	}
	if err := upsertCommentOutbox(ctx, tx, in.TaskID, in.CommentID, status, "", "", nowStr); err != nil {
		return TaskActivity{}, ContinueRequest{}, err
	}
	var req ContinueRequest
	if !isTerminal(task.Status) {
		b, err := s.bindCommentInTx(ctx, tx, in.TaskID, in.CommentID, in.ExpectedNodeRunID)
		if err != nil {
			return TaskActivity{}, ContinueRequest{}, err
		}
		req = ContinueRequest{RequestID: in.CommentID, TaskID: in.TaskID, CommentID: in.CommentID,
			Status: b.Status, NodeRunID: b.NodeRunID, SessionKey: b.SessionKey, CreatedAt: now, UpdatedAt: now}
	} else {
		req = ContinueRequest{RequestID: in.CommentID, TaskID: in.TaskID, CommentID: in.CommentID, Status: status, CreatedAt: now, UpdatedAt: now}
	}
	act, err := scanActivity(tx, in.TaskID, in.CommentID)
	if err != nil {
		return TaskActivity{}, ContinueRequest{}, err
	}
	if err := tx.Commit(); err != nil {
		return TaskActivity{}, ContinueRequest{}, err
	}
	return act, req, nil
}

func insertContinueReq(ctx context.Context, tx *sql.Tx, taskID, commentID, status, message, author, nowStr string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO continue_requests (request_id, task_id, comment_id, status, message, author, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, commentID, taskID, commentID, status, message, author, nowStr, nowStr)
	return err
}

// upsertCommentOutbox keeps the durable delivery projection in step with the
// canonical ContinueRequest. The outbox row is a copy, never a second truth:
// status and binding always come from continue_requests.
func upsertCommentOutbox(ctx context.Context, tx *sql.Tx, taskID, commentID, status, nodeRunID, sessionKey, nowStr string) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO outbox_items(id,task_id,request_id,node_run_id,session_key,message,status,created_at,updated_at)
		 VALUES(?,?,?,?,?,COALESCE((SELECT message FROM continue_requests WHERE task_id=? AND comment_id=?),''),?,?,?)
		 ON CONFLICT(id) DO UPDATE SET node_run_id=excluded.node_run_id,session_key=excluded.session_key,message=excluded.message,status=excluded.status,updated_at=excluded.updated_at`,
		"ob-"+commentID, taskID, commentID, nodeRunID, sessionKey, taskID, commentID, status, nowStr, nowStr)
	return err
}

func parseTime(t time.Time) time.Time { return t }

func scanActivity(q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, taskID, id string) (TaskActivity, error) {
	var a TaskActivity
	var created string
	err := q.QueryRowContext(context.Background(), `SELECT id, task_id, type, author, role, body, workflow_choice, workflow_version_id, run_id, node_run_id, created_at FROM task_activities WHERE task_id = ? AND id = ?`, taskID, id).Scan(&a.ID, &a.TaskID, &a.Type, &a.Author, &a.Role, &a.Body, &a.WorkflowChoice, &a.WorkflowVersionID, &a.RunID, &a.NodeRunID, &created)
	if err != nil {
		return TaskActivity{}, err
	}
	a.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return a, nil
}

// AppendTaskActivity writes one system/agent activity correlated to a workflow
// run/node. It is idempotent on (task_id,id): the first row wins, so a retried
// callback cannot duplicate the reply.
func (s *TaskStore) AppendTaskActivity(ctx context.Context, a TaskActivity) (TaskActivity, error) {
	a.TaskID = strings.TrimSpace(a.TaskID)
	a.ID = strings.TrimSpace(a.ID)
	a.Type = strings.TrimSpace(a.Type)
	if a.TaskID == "" || a.ID == "" || a.Type == "" {
		return TaskActivity{}, errors.New("activity task id, id and type required")
	}
	now := s.now().UTC()
	if a.CreatedAt.IsZero() {
		a.CreatedAt = now
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO task_activities (id, task_id, type, author, role, body, workflow_choice, workflow_version_id, run_id, node_run_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(task_id,id) DO NOTHING`,
		a.ID, a.TaskID, a.Type, a.Author, a.Role, a.Body, a.WorkflowChoice, a.WorkflowVersionID, a.RunID, a.NodeRunID, a.CreatedAt.Format(time.RFC3339Nano)); err != nil {
		return TaskActivity{}, err
	}
	return scanActivity(s.db, a.TaskID, a.ID)
}

func (s *TaskStore) ListActivities(ctx context.Context, taskID string) ([]TaskActivity, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, task_id, type, author, role, body, workflow_choice, workflow_version_id, run_id, node_run_id, created_at FROM task_activities WHERE task_id = ? ORDER BY created_at ASC`, strings.TrimSpace(taskID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []TaskActivity{}
	for rows.Next() {
		var a TaskActivity
		var created string
		if err := rows.Scan(&a.ID, &a.TaskID, &a.Type, &a.Author, &a.Role, &a.Body, &a.WorkflowChoice, &a.WorkflowVersionID, &a.RunID, &a.NodeRunID, &created); err != nil {
			return nil, err
		}
		a.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		items = append(items, a)
	}
	return items, rows.Err()
}

func (s *TaskStore) ListContinueRequests(ctx context.Context, taskID string) ([]ContinueRequest, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT request_id, task_id, comment_id, status, node_run_id, session_key, created_at, updated_at FROM continue_requests WHERE task_id = ? ORDER BY created_at ASC`, strings.TrimSpace(taskID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ContinueRequest{}
	for rows.Next() {
		r, err := scanContinueRequest(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, r)
	}
	return items, rows.Err()
}
