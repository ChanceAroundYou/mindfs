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

// DeleteTask 彻底删掉一张任务卡：tasks 行 + 它名下的 stage_runs 和 task_events。
//
// 只删卡片本身 —— worktree 目录、分支、会话都不是这张卡的一部分，用户要的是
// 「别再堆在板上」，不是「把活删掉」。
//
// 三张表必须在一个事务里一起消失：留孤儿 stage_runs / events 会让它们被
// ListStageRuns 之类的查询按 task_id 捞出来，而 tasks 行已经没了 —— 界面上
// 是一堆没有主人的执行记录。
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
	for _, table := range []string{"stage_runs", "task_events"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE task_id = ?`, id); err != nil {
			return err
		}
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

func (s *TaskStore) AddEvent(ctx context.Context, event TaskEvent) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO task_events (id, task_id, stage_run_id, type, payload_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`, event.ID, event.TaskID, event.StageRunID, event.Type, event.Payload, event.CreatedAt.UTC().Format(time.RFC3339Nano))
	return err
}
