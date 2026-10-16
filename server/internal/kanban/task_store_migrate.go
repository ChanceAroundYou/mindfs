package kanban

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mindfs/server/internal/fs"

	_ "modernc.org/sqlite"
)

const taskDBMetaPath = "tasks/task-kanban.db"

// scheduler_admitted 已退役（本模型无调度器），不再读也不再写；
// 列保留在表里不做破坏性迁移，恒为默认值。
const taskSelectColumns = "id, task_number, root_id, task_template_id, task_template_name, template_snapshot_json, create_worktree, worktree_branch_mode, worktree_branch, current_stage_index, status, main_session_key, worktree_root_id, worktree_path, worktree_built, aux_ask_user_waiting, aux_has_plan, aux_has_todos, aux_has_task, aux_session_error, labels_json, created_at, updated_at, completed_at, name, task_stages_json"

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
