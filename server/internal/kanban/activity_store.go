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
const activitySelectColumns = "id, task_id, root_id, type, author, role, body, workflow_choice, workflow_version_id, status, summary, run_id, node_run_id, session_key, payload_json, tool_use_id, reply_to_id, router_decision_id, purpose, ordinal, hidden, created_at, updated_at"

var (
	// ErrActivityNotFound 表示按 id 找不到活动。
	ErrActivityNotFound = errors.New("task activity not found")
	// ErrInvalidActivityStatus 表示 status 不在对话模型的枚举内。
	ErrInvalidActivityStatus = errors.New("invalid task activity status")
)

var activityStatuses = map[string]bool{
	ActivityStatusPending:     true,
	ActivityStatusRunning:     true,
	ActivityStatusCompleted:   true,
	ActivityStatusFailed:      true,
	ActivityStatusCancelled:   true,
	ActivityStatusWaitingUser: true,
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
	payload_json TEXT NOT NULL DEFAULT '',
	tool_use_id TEXT NOT NULL DEFAULT '',
	run_id TEXT NOT NULL DEFAULT '',
	node_run_id TEXT NOT NULL DEFAULT '',
	reply_to_id TEXT NOT NULL DEFAULT '',
	router_decision_id TEXT NOT NULL DEFAULT '',
	purpose TEXT NOT NULL DEFAULT '',
	ordinal INTEGER NOT NULL DEFAULT 0,
	hidden INTEGER NOT NULL DEFAULT 0,
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
		`ALTER TABLE task_activities ADD COLUMN payload_json TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE task_activities ADD COLUMN tool_use_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE task_activities ADD COLUMN run_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE task_activities ADD COLUMN node_run_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE task_activities ADD COLUMN reply_to_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE task_activities ADD COLUMN router_decision_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE task_activities ADD COLUMN purpose TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE task_activities ADD COLUMN ordinal INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE task_activities ADD COLUMN hidden INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE task_activities ADD COLUMN updated_at TEXT NOT NULL DEFAULT ''`,
	}
	for _, stmt := range alters {
		if _, err := db.Exec(stmt); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return err
		}
	}
	// 只为改造前的 agent_reply 行补 purpose：run_id 非空是 workflow 结果，
	// 否则是直连回复。新代码会显式写 purpose，purpose='' 的守卫保证只跑一次。
	// 存量 user_comment/attention 等行保持空串，不改写历史语义。
	if _, err := db.Exec(`
UPDATE task_activities
SET purpose = CASE WHEN run_id <> '' THEN 'result' ELSE 'reply' END
WHERE type = 'agent_reply' AND purpose = ''`); err != nil {
		return err
	}
	_, err := db.Exec(`
CREATE INDEX IF NOT EXISTS idx_task_activities_task_created ON task_activities(task_id, created_at);
CREATE INDEX IF NOT EXISTS idx_task_activities_workflow_run ON task_activities(run_id);
CREATE INDEX IF NOT EXISTS idx_task_activities_id ON task_activities(id);
CREATE INDEX IF NOT EXISTS idx_task_activities_decision ON task_activities(router_decision_id);
CREATE INDEX IF NOT EXISTS idx_task_activities_prompt_lookup ON task_activities(session_key, tool_use_id);
`)
	return err
}

// Insert 写入一条活动，自动补齐时间戳。活动 id 在对话模型里是 UUID，预期
// 全局唯一；表主键仍是 (task_id, id)，查询按 id 单列走 idx_task_activities_id。
func (s *ActivityStore) Insert(ctx context.Context, activity *TaskActivity) error {
	_, err := s.insert(ctx, activity, false)
	return err
}

// InsertOrIgnore 写入一条活动；同 (task_id,id) 已存在时返回 created=false。
// Prompt 卡的 id 由 (session_key, tool_use_id) 稳定派生，重复广播/重放必须
// 落成同一张卡，不能报错也不能插出第二行。
func (s *ActivityStore) InsertOrIgnore(ctx context.Context, activity *TaskActivity) (bool, error) {
	return s.insert(ctx, activity, true)
}

func (s *ActivityStore) insert(ctx context.Context, activity *TaskActivity, ignoreConflict bool) (bool, error) {
	if activity == nil {
		return false, errors.New("activity required")
	}
	activity.ID = strings.TrimSpace(activity.ID)
	activity.TaskID = strings.TrimSpace(activity.TaskID)
	activity.Type = strings.TrimSpace(activity.Type)
	if activity.ID == "" || activity.TaskID == "" || activity.Type == "" {
		return false, errors.New("activity id, task id and type required")
	}
	if activity.CreatedAt.IsZero() {
		activity.CreatedAt = time.Now().UTC()
	}
	if activity.UpdatedAt.IsZero() {
		activity.UpdatedAt = activity.CreatedAt
	}
	query := `INSERT INTO task_activities (id, task_id, root_id, type, author, role, body, workflow_choice, workflow_version_id, status, summary, run_id, node_run_id, session_key, payload_json, tool_use_id, reply_to_id, router_decision_id, purpose, ordinal, hidden, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if ignoreConflict {
		query += ` ON CONFLICT(task_id,id) DO NOTHING`
	}
	res, err := s.db.ExecContext(ctx, query,
		activity.ID, activity.TaskID, activity.RootID, activity.Type, activity.Author, activity.Role,
		activity.Body, activity.WorkflowChoice, activity.WorkflowVersionID, activity.Status,
		activity.Summary, activity.RunID, activity.NodeRunID, activity.SessionKey,
		activity.PayloadJSON, activity.ToolUseID,
		activity.ReplyToID, activity.RouterDecisionID, activity.Purpose, activity.Ordinal, boolToInt(activity.Hidden),
		activity.CreatedAt.Format(time.RFC3339Nano), activity.UpdatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}

// ClaimFirstQueuedUserMessage 领走任务里最早一条排队留言（status=queued），
// 并把状态清空。只有 RowsAffected==1 的调用方拿到留言：两个「立即执行」同时
// 到达时不会派发两轮。没有排队留言时返回 ErrActivityNotFound。
func (s *ActivityStore) ClaimFirstQueuedUserMessage(ctx context.Context, taskID string) (*TaskActivity, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+activitySelectColumns+` FROM task_activities
		 WHERE task_id = ? AND role = ? AND status = ?
		 ORDER BY created_at ASC, rowid ASC LIMIT 1`,
		strings.TrimSpace(taskID), RoleUser, ActivityStatusQueued)
	activity, err := scanActivityRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrActivityNotFound
	}
	if err != nil {
		return nil, err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE task_activities SET status = '', updated_at = ? WHERE id = ? AND status = ?`,
		time.Now().UTC().Format(time.RFC3339Nano), activity.ID, ActivityStatusQueued)
	if err != nil {
		return nil, err
	}
	claimed, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if claimed == 0 {
		return nil, ErrActivityNotFound
	}
	activity.Status = ""
	return activity, nil
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
	return s.list(ctx, taskID, false)
}

// ListIncludingHidden 返回完整活动流（含 hidden 行），供 include_hidden 调试与
// Router 的调度记录使用。默认列表刻意过滤 hidden：Router 的内部投递副本不属于
// 用户看到的对话流。
func (s *ActivityStore) ListIncludingHidden(ctx context.Context, taskID string) ([]*TaskActivity, error) {
	return s.list(ctx, taskID, true)
}

func (s *ActivityStore) list(ctx context.Context, taskID string, includeHidden bool) ([]*TaskActivity, error) {
	query := `SELECT ` + activitySelectColumns + ` FROM task_activities WHERE task_id = ?`
	if !includeHidden {
		query += ` AND hidden = 0`
	}
	query += ` ORDER BY created_at ASC, rowid ASC`
	rows, err := s.db.QueryContext(ctx, query, strings.TrimSpace(taskID))
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

// ActivityByRunID returns the agent reply activity linked to a workflow run.
// A run is produced by exactly one agent reply, so the newest match is the only
// meaningful answer; the run_id index keeps this a point lookup.
//
// 必须限定 type=agent_reply：同一 Run 还会产生 attention / system_event 行
// （节点 unknown、notify 等），它们同样带 run_id。按 run_id 取最新会把它们
// 当成「这一轮回复」去改状态、清正文（workflow_driver_test 钉住）。
func (s *ActivityStore) ActivityByRunID(ctx context.Context, runID string) (*TaskActivity, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+activitySelectColumns+` FROM task_activities WHERE run_id = ? AND type = ? ORDER BY created_at DESC, rowid DESC LIMIT 1`,
		strings.TrimSpace(runID), ActivityTypeAgentReply)
	activity, err := scanActivityRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrActivityNotFound
	}
	if err != nil {
		return nil, err
	}
	return activity, nil
}

// CompletedWorkflowRepliesWithoutSummary 找已经结算成 completed、但 summary 为空
// 的 workflow 回复。升级前的结果摘要只认 canonical 字段，changes 数组的产出会
// 落成空 summary；启动对账用这批行补一次，避免历史任务永远只显示「已完成」。
func (s *ActivityStore) CompletedWorkflowRepliesWithoutSummary(ctx context.Context) ([]*TaskActivity, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+activitySelectColumns+` FROM task_activities
		 WHERE type = ? AND status = ? AND run_id <> '' AND summary = ''
		 ORDER BY created_at ASC, rowid ASC`,
		ActivityTypeAgentReply, ActivityStatusCompleted)
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

// QuestionByToolUseID 按会话 + tool_use_id 找对应的任务提问卡。回答可以从任务
// 对话或会话查看器任一侧发起，两处必须落到同一张卡上，所以这是点查而不是列表过滤。
func (s *ActivityStore) QuestionByToolUseID(ctx context.Context, sessionKey, toolUseID string) (*TaskActivity, error) {
	sessionKey = strings.TrimSpace(sessionKey)
	toolUseID = strings.TrimSpace(toolUseID)
	if sessionKey == "" || toolUseID == "" {
		return nil, ErrActivityNotFound
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT `+activitySelectColumns+` FROM task_activities
		 WHERE session_key = ? AND tool_use_id = ? AND type = ?
		 ORDER BY created_at DESC, rowid DESC LIMIT 1`,
		sessionKey, toolUseID, ActivityTypeQuestion)
	activity, err := scanActivityRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrActivityNotFound
	}
	if err != nil {
		return nil, err
	}
	return activity, nil
}

// OpenQuestionsBySession 返回某会话里仍停在 waiting_user 的提问卡。回合结束
// （正常完成 / 取消 / harness 失败）时用它们做兜底收口：如果 harness 没发
// tool update，这些卡会永远挡住任务对话的输入框。
func (s *ActivityStore) OpenQuestionsBySession(ctx context.Context, sessionKey string) ([]*TaskActivity, error) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+activitySelectColumns+` FROM task_activities
		 WHERE session_key = ? AND type = ? AND status = ?
		 ORDER BY created_at ASC, rowid ASC`,
		sessionKey, ActivityTypeQuestion, ActivityStatusWaitingUser)
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

// SetWorkflowLink records the run/version an agent reply is executing. The
// activity row is inserted before the run exists, so the link is written after
// the run is persisted instead of leaving a hidden run with no activity.
func (s *ActivityStore) SetWorkflowLink(ctx context.Context, id, runID, versionID string) error {
	id = strings.TrimSpace(id)
	runID = strings.TrimSpace(runID)
	if id == "" || runID == "" {
		return errors.New("activity id and run id required")
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE task_activities SET run_id = ?, workflow_version_id = ?, updated_at = ? WHERE id = ?`,
		runID, strings.TrimSpace(versionID), time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return err
	}
	return ensureActivityUpdated(res, id)
}

// ResolvePrompt 把一张 waiting_user 的 Prompt 卡收成终态（completed/cancelled/
// failed），同时写回 payload（例如答案或 gate 决定）。只有 waiting_user 行会
// 变；重复/迟到的调用返回 changed=false，后到的结果绝不覆盖先到的。
func (s *ActivityStore) ResolvePrompt(ctx context.Context, id, status, payloadJSON string, summary *string) (*TaskActivity, bool, error) {
	status = strings.TrimSpace(status)
	switch status {
	case ActivityStatusCompleted, ActivityStatusCancelled, ActivityStatusFailed:
	default:
		return nil, false, fmt.Errorf("%w: %q", ErrInvalidActivityStatus, status)
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, false, errors.New("activity id required")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var (
		res sql.Result
		err error
	)
	if summary != nil {
		res, err = s.db.ExecContext(ctx,
			`UPDATE task_activities SET status=?, summary=?, payload_json=?, updated_at=? WHERE id=? AND status=?`,
			status, *summary, payloadJSON, now, id, ActivityStatusWaitingUser)
	} else {
		res, err = s.db.ExecContext(ctx,
			`UPDATE task_activities SET status=?, payload_json=?, updated_at=? WHERE id=? AND status=?`,
			status, payloadJSON, now, id, ActivityStatusWaitingUser)
	}
	if err != nil {
		return nil, false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, false, err
	}
	activity, err := s.Get(ctx, id)
	if err != nil {
		return nil, false, err
	}
	return activity, affected == 1, nil
}

// FillCompletedPromptPayload 给已经 completed、但还没有答案的 Prompt 卡补写
// 迟到答案。以原 payload 做 CAS：并发补写只有第一个生效，已有答案不被覆盖。
// 只对 completed 行生效，waiting_user 走 ResolvePrompt。
func (s *ActivityStore) FillCompletedPromptPayload(ctx context.Context, id, oldPayload, payloadJSON string) (*TaskActivity, bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, false, errors.New("activity id required")
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE task_activities SET payload_json=?, updated_at=? WHERE id=? AND status=? AND payload_json=?`,
		payloadJSON, time.Now().UTC().Format(time.RFC3339Nano), id, ActivityStatusCompleted, oldPayload)
	if err != nil {
		return nil, false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, false, err
	}
	activity, err := s.Get(ctx, id)
	if err != nil {
		return nil, false, err
	}
	return activity, affected == 1, nil
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

// SetHidden 把一条活动移出/放回用户可见流。Router 的 silent 决策用它把占位
// 回复变成仅审计可见的行：活动依然留在库里，include_hidden 仍可查。
func (s *ActivityStore) SetHidden(ctx context.Context, id string, hidden bool) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("activity id required")
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE task_activities SET hidden = ?, updated_at = ? WHERE id = ?`,
		boolToInt(hidden), time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return err
	}
	return ensureActivityUpdated(res, id)
}

// SetRouteFields 落 Router 归属（决策/回复对象/purpose/序号）。占位回复复用成
// 第一条 say 时用它，让前端能按决策追溯且多轮排序稳定。
func (s *ActivityStore) SetRouteFields(ctx context.Context, id, decisionID, replyToID, purpose string, ordinal int) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("activity id required")
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE task_activities SET router_decision_id = ?, reply_to_id = ?, purpose = ?, ordinal = ?, updated_at = ? WHERE id = ?`,
		strings.TrimSpace(decisionID), strings.TrimSpace(replyToID), strings.TrimSpace(purpose), ordinal,
		time.Now().UTC().Format(time.RFC3339Nano), id)
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

// ClearBody 清掉运行期进度正文（"步骤 i/n"）。一轮结束或被人取代后，
// 这条正文就不是当前事实了；summary 才是终态文案。
func (s *ActivityStore) ClearBody(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("activity id required")
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE task_activities SET body = '', updated_at = ? WHERE id = ? AND body <> ''`,
		time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
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
		hidden           int
		created, updated string
	)
	if err := row.Scan(
		&a.ID, &a.TaskID, &a.RootID, &a.Type, &a.Author, &a.Role, &a.Body,
		&a.WorkflowChoice, &a.WorkflowVersionID, &a.Status, &a.Summary,
		&a.RunID, &a.NodeRunID, &a.SessionKey,
		&a.PayloadJSON, &a.ToolUseID,
		&a.ReplyToID, &a.RouterDecisionID, &a.Purpose, &a.Ordinal, &hidden,
		&created, &updated,
	); err != nil {
		return nil, err
	}
	a.Hidden = hidden != 0
	a.CreatedAt = parseActivityTime(created)
	a.UpdatedAt = parseActivityTime(updated)
	return &a, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func parseActivityTime(raw string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}
	}
	return parsed
}
