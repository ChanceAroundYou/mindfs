package kanban

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"mindfs/server/internal/fs"

	_ "modernc.org/sqlite"
)

type TaskStore struct {
	root fs.RootInfo
	db   *sql.DB
	now  func() time.Time
}

type ListTasksOptions struct {
	Status     string
	TaskNumber int
	After      string
	Before     string
	// ArchivedAfter 是「归档状态」的独立水位：只看 archived_at > 它。
	//
	// 必须与 After 分开：归档刻意不推动 updated_at（它既是看板排序键，也是
	// 自动归档的兜底起算点），所以归档变化只能靠这一条到达增量客户端。混成一个
	// max 水位会漏掉「先归档、之后别处又更新」的行 —— 客户端的旧副本就永远停在
	// 「完成」列。
	ArchivedAfter string
	Limit         int
	// ExcludeArchived 给工作台用：归档的任务不在工作台显示（也应该让位给
	// 未归档任务，而不是先占掉 LIMIT 的名额再被滤掉）。
	ExcludeArchived bool
}

func (s *TaskStore) CreateTask(ctx context.Context, task Task, event TaskEvent) (Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	if task.TaskNumber <= 0 {
		var maxNumber int
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(task_number), 0) FROM tasks`).Scan(&maxNumber); err != nil {
			return Task{}, err
		}
		task.TaskNumber = maxNumber + 1
	}
	if err := insertTask(ctx, tx, task); err != nil {
		return Task{}, err
	}
	if err := insertTaskEvent(ctx, tx, event); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (s *TaskStore) ListTasks(ctx context.Context, opts ListTasksOptions) ([]Task, error) {
	where := []string{"1=1"}
	args := []any{}
	if strings.TrimSpace(opts.Status) != "" {
		where = append(where, "status = ?")
		args = append(args, strings.TrimSpace(opts.Status))
	}
	if opts.TaskNumber > 0 {
		where = append(where, "task_number = ?")
		args = append(args, opts.TaskNumber)
	}
	// 变化水位两条：任务自身更新与归档状态。同时给定时必须取 OR —— 客户端要的是
	// 「这两件里任一发生于水位之后」的行；AND 起来几乎永远为空。
	after := strings.TrimSpace(opts.After)
	archivedAfter := strings.TrimSpace(opts.ArchivedAfter)
	switch {
	case after != "" && archivedAfter != "":
		where = append(where, "(updated_at > ? OR archived_at > ?)")
		args = append(args, after, archivedAfter)
	case after != "":
		where = append(where, "updated_at > ?")
		args = append(args, after)
	case archivedAfter != "":
		where = append(where, "archived_at > ?")
		args = append(args, archivedAfter)
	}
	if strings.TrimSpace(opts.Before) != "" {
		where = append(where, "updated_at < ?")
		args = append(args, strings.TrimSpace(opts.Before))
	}
	if opts.ExcludeArchived {
		where = append(where, "archived_at = ''")
	}
	limitClause := ""
	if opts.Limit > 0 {
		limitClause = " LIMIT ?"
		args = append(args, opts.Limit)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+taskSelectColumns+` FROM tasks WHERE `+strings.Join(where, " AND ")+` ORDER BY updated_at DESC, created_at DESC`+limitClause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Task{}
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, task)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *TaskStore) ListTaskDetails(ctx context.Context, opts ListTasksOptions) ([]TaskDetail, error) {
	tasks, err := s.ListTasks(ctx, opts)
	if err != nil {
		return nil, err
	}
	items := make([]TaskDetail, 0, len(tasks))
	for _, task := range tasks {
		events, err := s.ListEvents(ctx, task.ID)
		if err != nil {
			return nil, err
		}
		items = append(items, TaskDetail{Task: task, Events: events})
	}
	return items, nil
}

func (s *TaskStore) GetTask(ctx context.Context, id string) (Task, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+taskSelectColumns+` FROM tasks WHERE id = ?`, strings.TrimSpace(id))
	return scanTask(row)
}

// TaskIDForMainSession 反查绑定了该会话的任务（main_session_key 匹配）；无则返回空串。
func (s *TaskStore) TaskIDForMainSession(ctx context.Context, sessionKey string) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM tasks WHERE main_session_key = ? LIMIT 1`,
		strings.TrimSpace(sessionKey)).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return id, err
}

func (s *TaskStore) GetDetail(ctx context.Context, id string) (TaskDetail, error) {
	task, err := s.GetTask(ctx, id)
	if err != nil {
		return TaskDetail{}, err
	}
	events, err := s.ListEvents(ctx, id)
	if err != nil {
		return TaskDetail{}, err
	}
	return TaskDetail{Task: task, Events: events}, nil
}

func (s *TaskStore) ListEvents(ctx context.Context, taskID string) ([]TaskEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, task_id, stage_run_id, type, payload_json, created_at FROM task_events WHERE task_id = ? ORDER BY created_at ASC`, strings.TrimSpace(taskID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []TaskEvent{}
	for rows.Next() {
		event, err := scanTaskEvent(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, event)
	}
	return items, rows.Err()
}

func (s *TaskStore) UpdateTaskStatus(ctx context.Context, taskID, status string, completed bool) error {
	now := s.now().UTC().Format(time.RFC3339Nano)
	set := []string{"status = ?", "updated_at = ?"}
	args := []any{strings.TrimSpace(status), now}
	if completed {
		set = append(set, "completed_at = ?")
		args = append(args, now)
		set = append(set, "aux_ask_user_waiting = ?")
		args = append(args, 0)
	}
	// 归档只属于终态任务：任何把任务拉回非终态的写入（重开、恢复、新的执行轮）
	// 都必须顺手清掉归档标记，否则卡片会「明明在跑却躺在归档组里」。
	// archive_dismissed_at 一起清：那是针对旧一轮终态的取舍，新一轮开始后自动归档
	// 应该重新按新的完成时间正常生效。
	if !isTerminalStatus(strings.TrimSpace(status)) {
		set = append(set, "archived_at = ''", "archive_dismissed_at = ''")
	}
	args = append(args, strings.TrimSpace(taskID))
	_, err := s.db.ExecContext(ctx, `UPDATE tasks SET `+strings.Join(set, ", ")+` WHERE id = ?`, args...)
	return err
}

// ReopenTask 把终态任务放回「待审核」。只动状态与 completed_at：
// 上一轮的 Run、回复、结果摘要都是证据，重开不该抹掉它们。
//
// 单独的 SQL 而不是复用 UpdateTaskStatus：后者只会在 completed=true 时写
// completed_at，没有清空分支，而 redo 一个已完成任务必须把封板时间清掉。
func (s *TaskStore) ReopenTask(ctx context.Context, taskID string) error {
	now := s.now().UTC().Format(time.RFC3339Nano)
	// archived_at = ''：重开后任务回到活动列，归档标记必须一起清掉（归档只属于终态）。
	// archive_dismissed_at 同理：它约束的是旧一轮终态下的自动归档，重开后重新计。
	_, err := s.db.ExecContext(ctx,
		`UPDATE tasks SET status = ?, completed_at = '', archived_at = '', archive_dismissed_at = '', updated_at = ? WHERE id = ?`,
		StatusWaitingUser, now, strings.TrimSpace(taskID))
	return err
}

// UpdateTaskResultSummary 记录最近一轮结算后的可读结果，供看板卡/任务列表展示。
// 与状态写入一样推进 updated_at：结果变了就是任务变了，列表排序也该跟着动。
func (s *TaskStore) UpdateTaskResultSummary(ctx context.Context, taskID, summary string) error {
	now := s.now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx,
		`UPDATE tasks SET result_summary = ?, updated_at = ? WHERE id = ?`,
		strings.TrimSpace(summary), now, strings.TrimSpace(taskID))
	return err
}

// ReplaceTaskResultSummaryIfUnchanged 与 ActivityStore.ReplaceSummaryIfUnchanged
// 同一条口径：异步压缩落地时只改写「还是它读到的那个值」，否则放弃。
// 操作员改写、下一轮结算、暂停完成都必须赢过迟到的压缩结果。
func (s *TaskStore) ReplaceTaskResultSummaryIfUnchanged(ctx context.Context, taskID, want, summary string) (bool, error) {
	now := s.now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx,
		`UPDATE tasks SET result_summary = ?, updated_at = ? WHERE id = ? AND result_summary = ?`,
		summary, now, strings.TrimSpace(taskID), want)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *TaskStore) UpdateTaskAuxFlags(ctx context.Context, taskID string, patch TaskAuxFlagsPatch) error {
	now := s.now().UTC().Format(time.RFC3339Nano)
	set := []string{"updated_at = ?"}
	args := []any{now}
	if patch.AskUserWaiting != nil {
		set = append(set, "aux_ask_user_waiting = ?")
		args = append(args, boolInt(*patch.AskUserWaiting))
	}
	if patch.HasPlan != nil {
		set = append(set, "aux_has_plan = ?")
		args = append(args, boolInt(*patch.HasPlan))
	}
	if patch.HasTodos != nil {
		set = append(set, "aux_has_todos = ?")
		args = append(args, boolInt(*patch.HasTodos))
	}
	if patch.HasTask != nil {
		set = append(set, "aux_has_task = ?")
		args = append(args, boolInt(*patch.HasTask))
	}
	if patch.SessionError != nil {
		set = append(set, "aux_session_error = ?")
		args = append(args, strings.TrimSpace(*patch.SessionError))
	}
	if len(set) == 1 {
		return nil
	}
	args = append(args, strings.TrimSpace(taskID))
	_, err := s.db.ExecContext(ctx, `UPDATE tasks SET `+strings.Join(set, ", ")+` WHERE id = ?`, args...)
	return err
}

// ClearSessionRefs 清掉任务指向某个会话的主会话引用。
//
// 会话被删除后必须调它：否则任务仍指向一个不存在的 key，任务面板点进去是空白。
// 归档不走这里 —— 归档的会话还能打开，链接必须留着。
func (s *TaskStore) ClearSessionRefs(ctx context.Context, taskID, sessionKey string) error {
	taskID = strings.TrimSpace(taskID)
	key := strings.TrimSpace(sessionKey)
	if taskID == "" || key == "" {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// 只清「确实指向这个会话」的那一条：任务的主会话可能已被改成别的 key。
	if _, err := tx.ExecContext(ctx, `UPDATE tasks SET main_session_key = '', updated_at = ? WHERE id = ? AND main_session_key = ?`,
		s.now().UTC().Format(time.RFC3339Nano), taskID, key); err != nil {
		return err
	}
	return tx.Commit()
}

// ClearWorktreeRefs 清掉任务的 worktree 归属。写/清都走 setWorktreeRefs —— 归属
// 只有这一个出口，后台流程的整行 UPDATE 不碰这两格（理由见 updateTaskCore）。
//
// 刻意**不清** worktree_built：「清掉路径」和「没建过树」是两件事。目录被拆掉之后
// 归属要清，但「这个任务开过 worktree」这个事实得留着，否则界面会把一个曾经干过活
// 的目录说成「从没建过」。
func (s *TaskStore) ClearWorktreeRefs(ctx context.Context, taskID string) error {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil
	}
	return s.setWorktreeRefsKeepBuilt(ctx, taskID, "", "")
}

// DeleteTask 彻底删掉一张任务卡：tasks 行 + 它名下的 stage_runs、task_events
// 与 Router 账本（决策/任务态/会话/事件）。stage_runs 是迁移前的只读证据，
// 任务卡删掉时一并清掉，避免留下没有主人的行。
//
// 只删卡片本身 —— worktree 目录、分支、会话都不是这张卡的一部分，用户要的是
// 「别再堆在板上」，不是「把活删掉」。
//
// 这几张表必须在一个事务里一起消失：留下孤儿 stage_runs / events 只会让
// 「按 task_id 捞出来的证据」指向一个已经不存在的任务。
//
// router_events 尤其不能漏：它的 unique_key 含 task_id，删卡却留行的话，任务
// 用同 ID 重建时第一件事就撞上旧键（InsertEvent 是 ON CONFLICT DO NOTHING），
// 新 Run 的事件永远插不进去，且不报错 —— 账本看起来「这次一条都没有」。
//
// 先 GetTask 是为了沿用「任务不存在」那套错误语义（sql.ErrNoRows），
// 不让一个不存在的 id 静默返回成功。
func (s *TaskStore) DeleteTask(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("task id required")
	}
	if _, err := s.GetTask(ctx, id); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// workflow 的三张表按 run 归属：node_run / run_event 没有 task_id 列，只能先
	// 按 run_id 子查询。子表必须先删，否则删 run 后子行再也找不到归属。
	runOwned := []string{
		`DELETE FROM workflow_run_events WHERE run_id IN (SELECT id FROM workflow_runs WHERE task_id = ?)`,
		`DELETE FROM workflow_node_runs WHERE run_id IN (SELECT id FROM workflow_runs WHERE task_id = ?)`,
		`DELETE FROM workflow_runs WHERE task_id = ?`,
	}
	for _, stmt := range runOwned {
		if _, err := tx.ExecContext(ctx, stmt, id); err != nil {
			return err
		}
	}
	// 任务自有的数据面行。漏一张就会留下指向已删除任务的孤儿（实测删卡后
	// 6 条 task_activities 仍在库里被页面查出来）。
	for _, table := range []string{
		"task_activities",
		"task_acceptance_items",
		"continue_requests",
		"outbox_items",
		"node_runs",
		"task_worktrees",
		"task_execution_engines",
		"stage_runs",
		"task_events",
		"router_decisions",
		"router_task_state",
		"router_events",
	} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE task_id = ?`, id); err != nil {
			return err
		}
	}
	// Router 会话按 (scope, owner_id) 主键，不是 task_id 列。
	if _, err := tx.ExecContext(ctx, `DELETE FROM router_sessions WHERE scope = ? AND owner_id = ?`, string(RouterScopeTask), id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM tasks WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *TaskStore) UpdateTask(ctx context.Context, task Task) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := updateTaskCore(ctx, tx, task); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *TaskStore) AddEvent(ctx context.Context, event TaskEvent) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO task_events (id, task_id, stage_run_id, type, payload_json, created_at) VALUES (?, ?, '', ?, ?, ?)`, event.ID, event.TaskID, event.Type, event.Payload, event.CreatedAt.UTC().Format(time.RFC3339Nano))
	return err
}
