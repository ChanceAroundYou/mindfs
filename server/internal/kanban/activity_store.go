package kanban

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ActivityStore 是对话式任务模型（Phase 2）的 task_activities 读写门面。
//
// 计划里的 006 SQL 迁移在本仓库落成一个幂等的 Go 迁移
// （migrateTaskActivities），因为 schema 变更统一由 TaskStore.migrate 驱动；
// 表本身早在 workflow 原型（migrateComments）时就已存在，这里只做加列。
//
// 与计划的字段映射：
//   - workflow_run_id → 既有 run_id 列（TaskActivity.RunID）。两者指同一关系，
//     不新增第二列，避免同一语义两份真相。
//   - created_at/updated_at 沿用本表既有的 TEXT RFC3339Nano 表示（存量行如此），
//     不改成 INTEGER 毫秒，否则存量活动流会读不出来。
//   - status/summary 在 Go 层用空串表示「无」，不建 CHECK 约束：存量活动类型
//     （user_comment/attention/merge_conflict 等）超出计划的枚举，硬约束会
//     直接拒绝现网写入。
const activitySelectColumns = "id, task_id, root_id, type, author, role, body, workflow_choice, workflow_version_id, status, summary, run_id, node_run_id, session_key, created_at, updated_at"

var (
	// ErrActivityNotFound 表示按 id 找不到活动。
	ErrActivityNotFound = errors.New("task activity not found")
	// ErrInvalidActivityStatus 表示 status 不在对话模型的枚举内。
	ErrInvalidActivityStatus = errors.New("invalid task activity status")
)

var activityStatuses = map[string]bool{
	ActivityStatusPending:   true,
	ActivityStatusRunning:   true,
	ActivityStatusCompleted: true,
	ActivityStatusFailed:    true,
	ActivityStatusCancelled: true,
}

type ActivityStore struct {
	db *sql.DB
}

func NewActivityStore(db *sql.DB) *ActivityStore {
	return &ActivityStore{db: db}
}

// migrateTaskActivities 是计划中 006_task_activities.sql 的等价迁移：
// 建表（幂等）+ 为旧库补齐对话模型需要的列与索引。可重复执行。
func migrateTaskActivities(db *sql.DB) error {
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS task_activities (
	id TEXT NOT NULL,
	task_id TEXT NOT NULL,
	type TEXT NOT NULL DEFAULT 'user_comment',
	author TEXT NOT NULL DEFAULT '',
	role TEXT NOT NULL DEFAULT '',
	body TEXT NOT NULL DEFAULT '',
	workflow_choice TEXT NOT NULL DEFAULT 'empty',
	workflow_version_id TEXT NOT NULL DEFAULT '',
	root_id TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT '',
	summary TEXT NOT NULL DEFAULT '',
	session_key TEXT NOT NULL DEFAULT '',
	run_id TEXT NOT NULL DEFAULT '',
	node_run_id TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (task_id, id)
);`); err != nil {
		return err
	}
	// 旧库逐列补齐。全部幂等（duplicate column name 忽略），存量行读作空串，
	// 与改造前行为一致。
	alters := []string{
		`ALTER TABLE task_activities ADD COLUMN root_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE task_activities ADD COLUMN status TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE task_activities ADD COLUMN summary TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE task_activities ADD COLUMN session_key TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE task_activities ADD COLUMN run_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE task_activities ADD COLUMN node_run_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE task_activities ADD COLUMN updated_at TEXT NOT NULL DEFAULT ''`,
	}
	for _, stmt := range alters {
		if _, err := db.Exec(stmt); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return err
		}
	}
	_, err := db.Exec(`
CREATE INDEX IF NOT EXISTS idx_task_activities_task_created ON task_activities(task_id, created_at);
CREATE INDEX IF NOT EXISTS idx_task_activities_workflow_run ON task_activities(run_id);
CREATE INDEX IF NOT EXISTS idx_task_activities_id ON task_activities(id);
`)
	return err
}

// Insert 写入一条活动，自动补齐时间戳。活动 id 在对话模型里是 UUID，预期
// 全局唯一；表主键仍是 (task_id, id)，查询按 id 单列走 idx_task_activities_id。
func (s *ActivityStore) Insert(ctx context.Context, activity *TaskActivity) error {
	if activity == nil {
		return errors.New("activity required")
	}
	activity.ID = strings.TrimSpace(activity.ID)
	activity.TaskID = strings.TrimSpace(activity.TaskID)
	activity.Type = strings.TrimSpace(activity.Type)
	if activity.ID == "" || activity.TaskID == "" || activity.Type == "" {
		return errors.New("activity id, task id and type required")
	}
	if activity.CreatedAt.IsZero() {
		activity.CreatedAt = time.Now().UTC()
	}
	if activity.UpdatedAt.IsZero() {
		activity.UpdatedAt = activity.CreatedAt
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO task_activities (id, task_id, root_id, type, author, role, body, workflow_choice, workflow_version_id, status, summary, run_id, node_run_id, session_key, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		activity.ID, activity.TaskID, activity.RootID, activity.Type, activity.Author, activity.Role,
		activity.Body, activity.WorkflowChoice, activity.WorkflowVersionID, activity.Status,
		activity.Summary, activity.RunID, activity.NodeRunID, activity.SessionKey,
		activity.CreatedAt.Format(time.RFC3339Nano), activity.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *ActivityStore) Get(ctx context.Context, id string) (*TaskActivity, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+activitySelectColumns+` FROM task_activities WHERE id = ? LIMIT 1`,
		strings.TrimSpace(id))
	activity, err := scanActivityRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrActivityNotFound
	}
	if err != nil {
		return nil, err
	}
	return activity, nil
}

// List 按创建时间升序返回某任务的活动。rowid 作为同一时间戳的次级排序，
// 保证并发写入后顺序稳定。
func (s *ActivityStore) List(ctx context.Context, taskID string) ([]*TaskActivity, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+activitySelectColumns+` FROM task_activities WHERE task_id = ? ORDER BY created_at ASC, rowid ASC`,
		strings.TrimSpace(taskID))
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

// UpdateStatus 更新 Agent 回复的状态与总结，并推进 updated_at。
// summary 为 nil 时保留原总结。
func (s *ActivityStore) UpdateStatus(ctx context.Context, id string, status string, summary *string) error {
	status = strings.TrimSpace(status)
	if !activityStatuses[status] {
		return fmt.Errorf("%w: %q", ErrInvalidActivityStatus, status)
	}
	id = strings.TrimSpace(id)
	nowStr := time.Now().UTC().Format(time.RFC3339Nano)
	var (
		res sql.Result
		err error
	)
	if summary != nil {
		res, err = s.db.ExecContext(ctx,
			`UPDATE task_activities SET status = ?, summary = ?, updated_at = ? WHERE id = ?`,
			status, *summary, nowStr, id)
	} else {
		res, err = s.db.ExecContext(ctx,
			`UPDATE task_activities SET status = ?, updated_at = ? WHERE id = ?`,
			status, nowStr, id)
	}
	if err != nil {
		return err
	}
	return ensureActivityUpdated(res, id)
}

// UpdateBody 改写活动正文（例如 Agent 回复占位被最终文本替换），并推进 updated_at。
func (s *ActivityStore) UpdateBody(ctx context.Context, id string, body string) error {
	body = strings.TrimSpace(body)
	if body == "" {
		return errors.New("activity body required")
	}
	id = strings.TrimSpace(id)
	res, err := s.db.ExecContext(ctx,
		`UPDATE task_activities SET body = ?, updated_at = ? WHERE id = ?`,
		body, time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return err
	}
	return ensureActivityUpdated(res, id)
}

func ensureActivityUpdated(res sql.Result, id string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: %s", ErrActivityNotFound, id)
	}
	return nil
}

// scanActivityRecord 同时接受 *sql.Row 与 *sql.Rows（复用包内 scanner 接口）。
func scanActivityRecord(row scanner) (*TaskActivity, error) {
	var (
		a                TaskActivity
		created, updated string
	)
	if err := row.Scan(
		&a.ID, &a.TaskID, &a.RootID, &a.Type, &a.Author, &a.Role, &a.Body,
		&a.WorkflowChoice, &a.WorkflowVersionID, &a.Status, &a.Summary,
		&a.RunID, &a.NodeRunID, &a.SessionKey, &created, &updated,
	); err != nil {
		return nil, err
	}
	a.CreatedAt = parseActivityTime(created)
	a.UpdatedAt = parseActivityTime(updated)
	return &a, nil
}

func parseActivityTime(raw string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}
	}
	return parsed
}
