package kanban

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"mindfs/server/internal/fs"

	_ "modernc.org/sqlite"
)

const taskDBMetaPath = "tasks/task-kanban.db"
const taskSelectColumns = "id, task_number, root_id, task_template_id, task_template_name, template_snapshot_json, create_worktree, worktree_branch_mode, worktree_branch, current_stage_index, status, scheduler_admitted, main_session_key, worktree_root_id, worktree_path, worktree_built, aux_ask_user_waiting, aux_has_plan, aux_has_todos, aux_has_task, aux_session_error, labels_json, created_at, updated_at, completed_at, name, task_stages_json"

type TaskStore struct {
	root fs.RootInfo
	db   *sql.DB
	now  func() time.Time
}

func NewTaskStore(root fs.RootInfo) (*TaskStore, error) {
	path, err := taskDBPath(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &TaskStore{root: root, db: db, now: time.Now}
	if err := store.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func taskDBPath(root fs.RootInfo) (string, error) {
	meta, err := root.EnsureMetaDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(meta, filepath.FromSlash(taskDBMetaPath)), nil
}

func (s *TaskStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *TaskStore) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS tasks (
	id TEXT PRIMARY KEY,
	task_number INTEGER NOT NULL DEFAULT 0,
	root_id TEXT NOT NULL,
	task_template_id TEXT NOT NULL,
	task_template_name TEXT NOT NULL,
	template_snapshot_json TEXT NOT NULL,
	create_worktree INTEGER NOT NULL DEFAULT 0,
	worktree_branch_mode TEXT NOT NULL DEFAULT '',
	worktree_branch TEXT NOT NULL DEFAULT '',
	current_stage_index INTEGER NOT NULL DEFAULT 0,
	status TEXT NOT NULL DEFAULT 'pending',
	scheduler_admitted INTEGER NOT NULL DEFAULT 0,
	main_session_key TEXT NOT NULL DEFAULT '',
	worktree_root_id TEXT NOT NULL DEFAULT '',
	worktree_path TEXT NOT NULL DEFAULT '',
	worktree_built INTEGER NOT NULL DEFAULT 0,
	aux_ask_user_waiting INTEGER NOT NULL DEFAULT 0,
	aux_has_plan INTEGER NOT NULL DEFAULT 0,
	aux_has_todos INTEGER NOT NULL DEFAULT 0,
	aux_has_task INTEGER NOT NULL DEFAULT 0,
	aux_session_error TEXT NOT NULL DEFAULT '',
	labels_json TEXT NOT NULL DEFAULT '[]',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	completed_at TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS stage_runs (
	id TEXT PRIMARY KEY,
	task_id TEXT NOT NULL,
	stage_index INTEGER NOT NULL,
	stage_name TEXT NOT NULL,
	role TEXT NOT NULL,
	status TEXT NOT NULL DEFAULT 'pending',
	session_key TEXT NOT NULL DEFAULT '',
	input TEXT NOT NULL DEFAULT '',
	rendered_prompt TEXT NOT NULL DEFAULT '',
	started_at TEXT NOT NULL DEFAULT '',
	finished_at TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS task_events (
	id TEXT PRIMARY KEY,
	task_id TEXT NOT NULL,
	stage_run_id TEXT NOT NULL DEFAULT '',
	type TEXT NOT NULL,
	payload_json TEXT NOT NULL DEFAULT '{}',
	created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_tasks_status_created ON tasks(status, created_at);
CREATE INDEX IF NOT EXISTS idx_stage_runs_task_stage ON stage_runs(task_id, stage_index, created_at);
CREATE INDEX IF NOT EXISTS idx_task_events_task_created ON task_events(task_id, created_at);
`)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(`ALTER TABLE tasks ADD COLUMN create_worktree INTEGER NOT NULL DEFAULT 0`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
		return err
	}
	if _, err := s.db.Exec(`ALTER TABLE tasks ADD COLUMN worktree_branch_mode TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
		return err
	}
	if _, err := s.db.Exec(`ALTER TABLE tasks ADD COLUMN worktree_branch TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
		return err
	}
	if _, err := s.db.Exec(`ALTER TABLE tasks ADD COLUMN task_number INTEGER NOT NULL DEFAULT 0`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
		return err
	}
	if _, err := s.db.Exec(`ALTER TABLE tasks ADD COLUMN aux_ask_user_waiting INTEGER NOT NULL DEFAULT 0`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
		return err
	}
	if _, err := s.db.Exec(`ALTER TABLE tasks ADD COLUMN aux_has_plan INTEGER NOT NULL DEFAULT 0`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
		return err
	}
	if _, err := s.db.Exec(`ALTER TABLE tasks ADD COLUMN aux_has_todos INTEGER NOT NULL DEFAULT 0`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
		return err
	}
	if _, err := s.db.Exec(`ALTER TABLE tasks ADD COLUMN aux_has_task INTEGER NOT NULL DEFAULT 0`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
		return err
	}
	if _, err := s.db.Exec(`ALTER TABLE tasks ADD COLUMN aux_session_error TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
		return err
	}
	// 任务命名与任务自有流水（阶段快照）；存在任务身上的 stage 定义不再回查模板。
	if _, err := s.db.Exec(`ALTER TABLE tasks ADD COLUMN name TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
		return err
	}
	if _, err := s.db.Exec(`ALTER TABLE tasks ADD COLUMN task_stages_json TEXT NOT NULL DEFAULT '[]'`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
		return err
	}
	// worktree_built：这个任务**曾经建过** worktree（2026-10-01 加）。
	//
	// 为什么需要这一列：worktree_path 被清空有两种完全不同的原因，而它们对用户
	// 意味着相反的事 ——
	//   「还没建」  ：首段还是 user 段，路径本来就该是空的，不是问题；
	//   「建过、被清」：路径记录丢了，但那个目录可能还在、还在被人用。
	// 前端只看到「路径为空」一律渲染成「已收尾」，于是第二种会被说成「活已经并回
	// 主干了」—— 2026-10-01 实测就是一个仍在使用的 worktree 被标成了已收尾。
	//
	// 存量数据一律补 0（读作「没建过」，与今天的行为一致，零回归）：判据宁保守，
	// 也不要把没证据的任务说成收过尾。
	if _, err := s.db.Exec(`ALTER TABLE tasks ADD COLUMN worktree_built INTEGER NOT NULL DEFAULT 0`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
		return err
	}
	// 存量回填：凡是**当前**还带着 worktree 路径的行，一定建过树。这一步只让新列
	// 对现存活着的 worktree 立刻可用，其余保持 0。
	if _, err := s.db.Exec(`UPDATE tasks SET worktree_built = 1 WHERE create_worktree = 1 AND TRIM(worktree_path) != ''`); err != nil {
		return err
	}
	// 旧数据：颓废状态归入新模型（queued→pending）；旧任务若只有模板无快照，在读取时惰性补齐。
	if _, err := s.db.Exec(`UPDATE tasks SET status = 'pending' WHERE status = 'queued'`); err != nil {
		return err
	}
	if err := s.backfillTaskNumbers(); err != nil {
		return err
	}
	return err
}

func (s *TaskStore) backfillTaskNumbers() error {
	rows, err := s.db.Query(`SELECT id FROM tasks WHERE task_number = 0 ORDER BY created_at ASC`)
	if err != nil {
		return err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	var maxNumber int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(task_number), 0) FROM tasks`).Scan(&maxNumber); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		maxNumber++
		if _, err := tx.Exec(`UPDATE tasks SET task_number = ? WHERE id = ?`, maxNumber, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type ListTasksOptions struct {
	TemplateID string
	Status     string
	TaskNumber int
	Stage      int
	HasStage   bool
	After      string
	Before     string
	Limit      int
}

func (s *TaskStore) CreateTask(ctx context.Context, task Task, firstRun StageRun, event TaskEvent) (Task, error) {
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
	if err := insertStageRun(ctx, tx, firstRun); err != nil {
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
	if strings.TrimSpace(opts.TemplateID) != "" {
		where = append(where, "task_template_id = ?")
		args = append(args, strings.TrimSpace(opts.TemplateID))
	}
	if strings.TrimSpace(opts.Status) != "" {
		where = append(where, "status = ?")
		args = append(args, strings.TrimSpace(opts.Status))
	}
	if opts.TaskNumber > 0 {
		where = append(where, "task_number = ?")
		args = append(args, opts.TaskNumber)
	}
	if opts.HasStage {
		where = append(where, "current_stage_index = ?")
		args = append(args, opts.Stage)
	}
	if strings.TrimSpace(opts.After) != "" {
		where = append(where, "updated_at > ?")
		args = append(args, strings.TrimSpace(opts.After))
	}
	if strings.TrimSpace(opts.Before) != "" {
		where = append(where, "updated_at < ?")
		args = append(args, strings.TrimSpace(opts.Before))
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
	rows.Close()
	for i := range items {
		s.decorateCurrentStage(ctx, &items[i])
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
		runs, err := s.ListStageRuns(ctx, task.ID)
		if err != nil {
			return nil, err
		}
		events, err := s.ListEvents(ctx, task.ID)
		if err != nil {
			return nil, err
		}
		items = append(items, TaskDetail{Task: task, StageRuns: runs, Events: events})
	}
	return items, nil
}

func (s *TaskStore) GetTask(ctx context.Context, id string) (Task, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+taskSelectColumns+` FROM tasks WHERE id = ?`, strings.TrimSpace(id))
	task, err := scanTask(row)
	if err != nil {
		return Task{}, err
	}
	s.decorateCurrentStage(ctx, &task)
	return task, nil
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
	runs, err := s.ListStageRuns(ctx, id)
	if err != nil {
		return TaskDetail{}, err
	}
	events, err := s.ListEvents(ctx, id)
	if err != nil {
		return TaskDetail{}, err
	}
	return TaskDetail{Task: task, StageRuns: runs, Events: events}, nil
}

func (s *TaskStore) ListStageRuns(ctx context.Context, taskID string) ([]StageRun, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, task_id, stage_index, stage_name, role, status, session_key, input, rendered_prompt, started_at, finished_at, created_at, updated_at FROM stage_runs WHERE task_id = ? ORDER BY created_at ASC`, strings.TrimSpace(taskID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []StageRun{}
	for rows.Next() {
		run, err := scanStageRun(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, run)
	}
	return items, rows.Err()
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

func (s *TaskStore) LatestStageRun(ctx context.Context, taskID string, stageIndex int) (StageRun, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, task_id, stage_index, stage_name, role, status, session_key, input, rendered_prompt, started_at, finished_at, created_at, updated_at FROM stage_runs WHERE task_id = ? AND stage_index = ? ORDER BY created_at DESC LIMIT 1`, strings.TrimSpace(taskID), stageIndex)
	return scanStageRun(row)
}

func (s *TaskStore) UpdateTaskStatus(ctx context.Context, taskID, status string, admitted *bool, completed bool) error {
	now := s.now().UTC().Format(time.RFC3339Nano)
	set := []string{"status = ?", "updated_at = ?"}
	args := []any{strings.TrimSpace(status), now}
	if admitted != nil {
		set = append(set, "scheduler_admitted = ?")
		if *admitted {
			args = append(args, 1)
		} else {
			args = append(args, 0)
		}
	}
	if completed {
		set = append(set, "completed_at = ?")
		args = append(args, now)
		set = append(set, "aux_ask_user_waiting = ?")
		args = append(args, 0)
	}
	args = append(args, strings.TrimSpace(taskID))
	_, err := s.db.ExecContext(ctx, `UPDATE tasks SET `+strings.Join(set, ", ")+` WHERE id = ?`, args...)
	return err
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

// ClearSessionRefs 清掉任务指向某个会话的所有引用（main_session_key + 阶段运行的 session_key）。
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
	if _, err := tx.ExecContext(ctx, `UPDATE stage_runs SET session_key = '', updated_at = ? WHERE task_id = ? AND session_key = ?`,
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

// setWorktreeRefsKeepBuilt 清归属但保留 worktree_built。
//
// 之所以不直接复用 setWorktreeRefs：那个方法的 built 参数是给**建树**用的，
// 清这一路必须原样保留旧值，否则清一次就把「建过」这个事实抹了。
func (s *TaskStore) setWorktreeRefsKeepBuilt(ctx context.Context, taskID, rootID, path string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE tasks SET worktree_root_id = ?, worktree_path = ?, updated_at = ? WHERE id = ?`,
		rootID, path, s.now().UTC().Format(time.RFC3339Nano), taskID)
	return err
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

func (s *TaskStore) MoveTask(ctx context.Context, task Task, run StageRun, event TaskEvent) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := updateTaskCore(ctx, tx, task); err != nil {
		return err
	}
	if strings.TrimSpace(run.ID) != "" {
		if err := insertStageRun(ctx, tx, run); err != nil {
			return err
		}
	}
	if err := insertTaskEvent(ctx, tx, event); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *TaskStore) UpdateStageRunStatus(ctx context.Context, runID, status string) error {
	now := s.now().UTC().Format(time.RFC3339Nano)
	finished := ""
	if status == StageStatusApproved || status == StageStatusRejected || status == StageStatusSuccess || status == StageStatusFail || status == StageStatusCancelled {
		finished = now
	}
	_, err := s.db.ExecContext(ctx, `UPDATE stage_runs SET status = ?, finished_at = CASE WHEN ? != '' THEN ? ELSE finished_at END, updated_at = ? WHERE id = ?`, strings.TrimSpace(status), finished, finished, now, strings.TrimSpace(runID))
	return err
}

func (s *TaskStore) UpdateStageRunExecution(ctx context.Context, run StageRun) error {
	now := s.now().UTC().Format(time.RFC3339Nano)
	set := []string{"status = ?", "session_key = ?", "input = ?", "rendered_prompt = ?", "updated_at = ?"}
	args := []any{strings.TrimSpace(run.Status), strings.TrimSpace(run.SessionKey), run.Input, run.RenderedPrompt, now}
	if strings.TrimSpace(run.StartedAt) != "" {
		set = append(set, "started_at = ?")
		args = append(args, strings.TrimSpace(run.StartedAt))
	}
	if strings.TrimSpace(run.FinishedAt) != "" {
		set = append(set, "finished_at = ?")
		args = append(args, strings.TrimSpace(run.FinishedAt))
	}
	args = append(args, strings.TrimSpace(run.ID))
	_, err := s.db.ExecContext(ctx, `UPDATE stage_runs SET `+strings.Join(set, ", ")+` WHERE id = ?`, args...)
	return err
}

func (s *TaskStore) UpdateStageRunInput(ctx context.Context, runID, input string) error {
	now := s.now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `UPDATE stage_runs SET input = ?, updated_at = ? WHERE id = ?`, input, now, strings.TrimSpace(runID))
	return err
}

func (s *TaskStore) UpdateTaskAndStageRun(ctx context.Context, task Task, run StageRun, event TaskEvent) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := updateTaskCore(ctx, tx, task); err != nil {
		return err
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	_, err = tx.ExecContext(ctx, `UPDATE stage_runs SET status = ?, session_key = ?, input = ?, rendered_prompt = ?, started_at = ?, finished_at = ?, updated_at = ? WHERE id = ?`,
		run.Status, run.SessionKey, run.Input, run.RenderedPrompt, run.StartedAt, run.FinishedAt, now, run.ID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(event.ID) != "" {
		if err := insertTaskEvent(ctx, tx, event); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *TaskStore) AddEvent(ctx context.Context, event TaskEvent) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO task_events (id, task_id, stage_run_id, type, payload_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`, event.ID, event.TaskID, event.StageRunID, event.Type, event.Payload, event.CreatedAt.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *TaskStore) decorateCurrentStage(ctx context.Context, task *Task) {
	run, err := s.LatestStageRun(ctx, task.ID, task.CurrentStageIndex)
	if err != nil {
		return
	}
	task.CurrentStageName = run.StageName
	task.CurrentStageStatus = run.Status
}

func insertTask(ctx context.Context, tx *sql.Tx, task Task) error {
	labels, _ := json.Marshal(task.Labels)
	stages, _ := json.Marshal(task.Stages)
	_, err := tx.ExecContext(ctx, `INSERT INTO tasks (id, task_number, root_id, task_template_id, task_template_name, template_snapshot_json, create_worktree, worktree_branch_mode, worktree_branch, current_stage_index, status, scheduler_admitted, main_session_key, worktree_root_id, worktree_path, worktree_built, aux_ask_user_waiting, aux_has_plan, aux_has_todos, aux_has_task, aux_session_error, labels_json, created_at, updated_at, completed_at, name, task_stages_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		task.ID, task.TaskNumber, task.RootID, task.TaskTemplateID, task.TaskTemplateName, "", boolInt(task.CreateWorktree), task.WorktreeBranchMode, task.WorktreeBranch, task.CurrentStageIndex, task.Status, boolInt(task.SchedulerAdmitted), task.MainSessionKey, task.WorktreeRootID, task.WorktreePath, boolInt(task.WorktreeBuilt), boolInt(task.AuxFlags.AskUserWaiting), boolInt(task.AuxFlags.HasPlan), boolInt(task.AuxFlags.HasTodos), boolInt(task.AuxFlags.HasTask), strings.TrimSpace(task.AuxFlags.SessionError), string(labels), task.CreatedAt.UTC().Format(time.RFC3339Nano), task.UpdatedAt.UTC().Format(time.RFC3339Nano), task.CompletedAt, task.Name, string(stages))
	return err
}

func updateTaskCore(ctx context.Context, tx *sql.Tx, task Task) error {
	labels, _ := json.Marshal(task.Labels)
	stages, _ := json.Marshal(task.Stages)
	// 刻意**不含** worktree_root_id / worktree_path：这两个列只有一个写入点
	// （Service.ensureTaskWorktree，见 setWorktreeRefs）。留在整行 UPDATE 里，
	// 任何拿着旧快照的后台写入（executeTask 是异步的，跑完一整段才写回）都会把
	// 已清空的归属又写回去 —— 症状是「清完 worktree，路径又回来了」，且只在后台
	// 流程还没结束时出现。归属是「谁建的树」这种一次性事实，不该跟着每次状态更新漂。
	_, err := tx.ExecContext(ctx, `UPDATE tasks SET create_worktree = ?, worktree_branch_mode = ?, worktree_branch = ?, current_stage_index = ?, status = ?, scheduler_admitted = ?, main_session_key = ?, aux_ask_user_waiting = ?, aux_has_plan = ?, aux_has_todos = ?, aux_has_task = ?, aux_session_error = ?, labels_json = ?, updated_at = ?, completed_at = ?, name = ?, task_stages_json = ? WHERE id = ?`,
		boolInt(task.CreateWorktree), task.WorktreeBranchMode, task.WorktreeBranch, task.CurrentStageIndex, task.Status, boolInt(task.SchedulerAdmitted), task.MainSessionKey, boolInt(task.AuxFlags.AskUserWaiting), boolInt(task.AuxFlags.HasPlan), boolInt(task.AuxFlags.HasTodos), boolInt(task.AuxFlags.HasTask), strings.TrimSpace(task.AuxFlags.SessionError), string(labels), task.UpdatedAt.UTC().Format(time.RFC3339Nano), task.CompletedAt, task.Name, string(stages), task.ID)
	return err
}

// setWorktreeRefs 写/清任务的 worktree 归属。建树与清归属共用一个出口，
// 这样「这两个列归谁管」在 schema 旁边就能看全，不用去数整行 UPDATE 里的字段。
//
// built=true 表示「这棵树确实建出来了」，与 path 是否为空无关：清归属只清路径，
// 不清这个事实 —— 前端要靠它区分「还没建」（首段是 user 段）和「建过、记录丢了」
// （目录可能还在被人用）。2026-10-01 加，后两者在界面上被混成同一个「已收尾」。
func (s *TaskStore) setWorktreeRefs(ctx context.Context, taskID, rootID, path string, built bool) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE tasks SET worktree_root_id = ?, worktree_path = ?, worktree_built = ?, updated_at = ? WHERE id = ?`,
		rootID, path, boolInt(built), s.now().UTC().Format(time.RFC3339Nano), taskID)
	return err
}

// SetWorktreeRefsAndTask 在**一个事务**里写归属 + 更新任务其余字段。
//
// 建树时两者必须一起生效：只有归属没有状态更新会留下「路径可用但 session_error
// 还挂着」的中间态，而分开两次写中间撞上读就是那种状态。分开两个方法是为了让
// ClearWorktreeRefs（只清归属、不碰别的）也能走同一个出口。
func (s *TaskStore) SetWorktreeRefsAndTask(ctx context.Context, task Task) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := updateTaskCore(ctx, tx, task); err != nil {
		return err
	}
	// 建树这一路：path 非空即视为已建出来。
	if _, err := tx.ExecContext(ctx,
		`UPDATE tasks SET worktree_root_id = ?, worktree_path = ?, worktree_built = ? WHERE id = ?`,
		task.WorktreeRootID, task.WorktreePath, boolInt(task.WorktreeBuilt || strings.TrimSpace(task.WorktreePath) != ""), task.ID); err != nil {
		return err
	}
	return tx.Commit()
}

func insertStageRun(ctx context.Context, tx *sql.Tx, run StageRun) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO stage_runs (id, task_id, stage_index, stage_name, role, status, session_key, input, rendered_prompt, started_at, finished_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.TaskID, run.StageIndex, run.StageName, run.Role, run.Status, run.SessionKey, run.Input, run.RenderedPrompt, run.StartedAt, run.FinishedAt, run.CreatedAt.UTC().Format(time.RFC3339Nano), run.UpdatedAt.UTC().Format(time.RFC3339Nano))
	return err
}

func insertTaskEvent(ctx context.Context, tx *sql.Tx, event TaskEvent) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO task_events (id, task_id, stage_run_id, type, payload_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`, event.ID, event.TaskID, event.StageRunID, event.Type, event.Payload, event.CreatedAt.UTC().Format(time.RFC3339Nano))
	return err
}

type scanner interface{ Scan(dest ...any) error }

func scanTask(row scanner) (Task, error) {
	var task Task
	var createWorktree, admitted, askUserWaiting, hasPlan, hasTodos, hasTask int
	var worktreeBuilt int
	var sessionError string
	var labels, stagesJSON string
	var templateSnapshot string
	var created, updated string
	if err := row.Scan(&task.ID, &task.TaskNumber, &task.RootID, &task.TaskTemplateID, &task.TaskTemplateName, &templateSnapshot, &createWorktree, &task.WorktreeBranchMode, &task.WorktreeBranch, &task.CurrentStageIndex, &task.Status, &admitted, &task.MainSessionKey, &task.WorktreeRootID, &task.WorktreePath, &worktreeBuilt, &askUserWaiting, &hasPlan, &hasTodos, &hasTask, &sessionError, &labels, &created, &updated, &task.CompletedAt, &task.Name, &stagesJSON); err != nil {
		return Task{}, err
	}
	task.CreateWorktree = createWorktree != 0
	task.SchedulerAdmitted = admitted != 0
	// 存量回填之外还有一层兜底：老库里 worktree_built 可能还是 0，但路径非空说明
	// 树确实在。按「有路径就算建过」算，界面才不会把一棵活着的树说成没建过。
	task.WorktreeBuilt = worktreeBuilt != 0 || strings.TrimSpace(task.WorktreePath) != ""
	task.AuxFlags = TaskAuxFlags{
		AskUserWaiting: askUserWaiting != 0,
		HasPlan:        hasPlan != 0,
		HasTodos:       hasTodos != 0,
		HasTask:        hasTask != 0,
		SessionError:   strings.TrimSpace(sessionError),
	}
	_ = json.Unmarshal([]byte(labels), &task.Labels)
	if task.Stages == nil {
		task.Stages = []StageTemplate{}
	}
	if strings.TrimSpace(stagesJSON) != "" && stagesJSON != "[]" {
		_ = json.Unmarshal([]byte(stagesJSON), &task.Stages)
	}
	if task.Stages == nil {
		task.Stages = []StageTemplate{}
	}
	task.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	task.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return task, nil
}

func scanStageRun(row scanner) (StageRun, error) {
	var run StageRun
	var created, updated string
	if err := row.Scan(&run.ID, &run.TaskID, &run.StageIndex, &run.StageName, &run.Role, &run.Status, &run.SessionKey, &run.Input, &run.RenderedPrompt, &run.StartedAt, &run.FinishedAt, &created, &updated); err != nil {
		return StageRun{}, err
	}
	run.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	run.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return run, nil
}

func scanTaskEvent(row scanner) (TaskEvent, error) {
	var event TaskEvent
	var created string
	if err := row.Scan(&event.ID, &event.TaskID, &event.StageRunID, &event.Type, &event.Payload, &created); err != nil {
		return TaskEvent{}, err
	}
	event.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return event, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func isTerminalStatus(status string) bool {
	switch status {
	case StatusSuccess, StatusFail, StatusCancelled:
		return true
	default:
		return false
	}
}

// canLeaveStageOnRequest 报告用户**手动**点「下一段 / 立即执行」时能不能离开这一段。
//
// 与 canAdvanceFromStage 只差一处：agent 段的 waiting_user 在这里放行。
//
// 那个状态的字面含义是「agent 没输出 [STAGE-DONE:N]，也没说受阻」—— 是**没回报**，
// 不是活没干完。2026-10-03 实测（mindfs 任务 26）：定位段把方案讲完了，停在
// waiting_user，于是详情面板按 canAdvanceFromCurrentStage 不给按钮、看板按
// showAdvance 给了个点了没反应的按钮，任务彻底卡死 —— 而用户要做的恰恰是
// 「就这样，推进到修复段」。
//
// 放行安全的前提是**只有人点的路径走这里**：Next 只有 /api/tasks/{id}/next 这一个
// 入口（连 RunNow 的 waiting_user/pending 分支也是转调它），而引擎的自动推进走的是
// moveTo（auto_advanced 事件），根本不经过本函数。所以引擎依旧不会自己跳过没回报的段 ——
// 阶段错乱那条防线（TestAgentStageWithoutDoneMarkerStopsAtCurrentStage）不受影响。
// AddStage 也不经过这里：补一句评论顶不掉没走完的段，那条规则原样保留。
func canLeaveStageOnRequest(role, runStatus string) bool {
	if role == RoleAgent && runStatus == StageStatusWaitingUser {
		return true
	}
	return canAdvanceFromStage(role, runStatus)
}

// canAdvanceFromStage 报告是否允许离开由 role/runStatus 代表的这一段。
//
// 拦的是「跑过但没走完」：fail / cancelled / rejected。以前 moveRelative 只拦
// running，其余一律被 Next 当成「已批准」强行推进（曾被
// TestNextAdvancesFailedCurrentStageAfterUserReview 固化成期望行为），
// 于是前一段没干完，下一段就在错误前提上开跑，阶段错乱。
//
// role 必须一起看，两种「waiting_user」含义相反：
//   - user 段的 waiting_user 是「在等你的输入」，补一句评论就是答案，照常放行；
//   - agent 段的 waiting_user 是「agent 自己没回报完成」，停下等你，别当成答完。
//     引擎的自动推进因此也不走这条路；要放行只有用户手动点，见 canLeaveStageOnRequest。
//
// pending（还没跑过，首段等人批准）和 running（正在跑）另说：前者正是「用户批准
// 首段」这条正常流程，后者由调用方的 running 检查单独拦（并发推进会重复执行）。
func canAdvanceFromStage(role, runStatus string) bool {
	if role == RoleAgent {
		switch runStatus {
		case StageStatusPending, StageStatusRunning, StageStatusSuccess, StageStatusApproved:
			return true
		default:
			// fail / cancelled / rejected：agent 没走完，不许引擎自动推进。
			// waiting_user 同理 —— 但用户手动点「下一段」时走
			// canLeaveStageOnRequest，那里是放行的。
			return false
		}
	}
	// user 段：等的就是你，pending（等输入）与 waiting_user（还在等）都算可推进。
	switch runStatus {
	case StageStatusPending, StageStatusRunning, StageStatusWaitingUser,
		StageStatusApproved, StageStatusSuccess, StageStatusRejected:
		return true
	default:
		// fail / cancelled：user 段自己失败/被取消，不能靠一句评论跳过。
		return false
	}
}

func sortStageRunsDesc(items []StageRun) {
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
}

var errNoRows = sql.ErrNoRows

func isNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
