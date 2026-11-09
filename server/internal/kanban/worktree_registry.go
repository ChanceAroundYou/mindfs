package kanban

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// worktree_registry：Task→多 worktree 的持久化注册表（WP-04）。
//
// 表 task_worktrees(task_id, worktree_id, branch, path, root_id, node_run_id,
// state, merged_commit, conflict_files_json, conflict_output, created_at,
// updated_at)，主键 (task_id, worktree_id)。
// worktree_id 默认 = branch（git 里同一分支不能被两棵 worktree 同时检出，
// 分支天然是身份）；显式传 id 时与 branch 分开存。
// node_run_id 归属 NodeRun：WP-05 的 NodeRun 尚不存在，留空即 legacy/stage 归属
// （ensureTaskWorktree 的主 worktree）。WP-05 接入时只写这一列，不改表。
//
// 兼容：tasks 表的单 worktree 列保持为「主 worktree」，registry 只增不改既有
// 语义。存量任务 registry 为空 = 只有主 worktree，与今日行为一致。
//
// 迁移（migrateWorktreeRegistry）由 task_store_migrate.go 的 migrate() 显式调用
// （与 migrateComments 同一接线），不在任何运行时路径上懒建表：建表只发生在
// NewTaskStore → migrate() 时，打开库的调用方看得见。

func (s *TaskStore) migrateWorktreeRegistry() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS task_worktrees (
	task_id TEXT NOT NULL,
	worktree_id TEXT NOT NULL,
	branch TEXT NOT NULL DEFAULT '',
	path TEXT NOT NULL DEFAULT '',
	root_id TEXT NOT NULL DEFAULT '',
	node_run_id TEXT NOT NULL DEFAULT '',
	state TEXT NOT NULL DEFAULT 'active',
	merged_commit TEXT NOT NULL DEFAULT '',
	conflict_files_json TEXT NOT NULL DEFAULT '[]',
	conflict_output TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	PRIMARY KEY (task_id, worktree_id)
);
CREATE INDEX IF NOT EXISTS idx_task_worktrees_task ON task_worktrees(task_id, state);
CREATE INDEX IF NOT EXISTS idx_task_worktrees_path ON task_worktrees(path);
CREATE INDEX IF NOT EXISTS idx_task_worktrees_node_run ON task_worktrees(node_run_id);
`)
	return err
}

func normalizeWorktreeEntry(in WorktreeEntry) (WorktreeEntry, error) {
	in.TaskID = strings.TrimSpace(in.TaskID)
	in.WorktreeID = strings.TrimSpace(in.WorktreeID)
	in.Branch = strings.TrimSpace(in.Branch)
	in.Path = strings.TrimSpace(in.Path)
	in.RootID = strings.TrimSpace(in.RootID)
	in.NodeRunID = strings.TrimSpace(in.NodeRunID)
	in.State = strings.TrimSpace(in.State)
	in.MergedCommit = strings.TrimSpace(in.MergedCommit)
	in.ConflictOutput = strings.TrimSpace(in.ConflictOutput)
	if in.TaskID == "" {
		return in, errors.New("task id required")
	}
	if in.WorktreeID == "" {
		in.WorktreeID = in.Branch
	}
	if in.WorktreeID == "" {
		return in, errors.New("worktree id required")
	}
	if in.State == "" {
		in.State = WorktreeStateActive
	}
	switch in.State {
	case WorktreeStateActive, WorktreeStateMerged, WorktreeStateClosed, WorktreeStateNeedsUser:
	default:
		return in, errors.New("invalid worktree state")
	}
	return in, nil
}

// UpsertWorktreeEntry 按 (task_id, worktree_id) 插入或更新一行注册表。
// state 单调性不由本方法强制（merged→active 这类回退由调用方决定），
// 但**不允许**把 merged/needs_user 静默写回 active：见 UpdateWorktreeEntryState 的守卫。
func (s *TaskStore) UpsertWorktreeEntry(ctx context.Context, in WorktreeEntry) (WorktreeEntry, error) {
	in, err := normalizeWorktreeEntry(in)
	if err != nil {
		return WorktreeEntry{}, err
	}
	now := s.now().UTC()
	nowStr := now.Format(time.RFC3339Nano)
	if in.CreatedAt.IsZero() {
		in.CreatedAt = now
	}
	in.UpdatedAt = now
	conflictJSON, _ := json.Marshal(in.ConflictFiles)
	if conflictJSON == nil {
		conflictJSON = []byte("[]")
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO task_worktrees (task_id, worktree_id, branch, path, root_id, node_run_id, state, merged_commit, conflict_files_json, conflict_output, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(task_id, worktree_id) DO UPDATE SET
	branch = excluded.branch,
	path = excluded.path,
	root_id = excluded.root_id,
	node_run_id = excluded.node_run_id,
	state = excluded.state,
	merged_commit = excluded.merged_commit,
	conflict_files_json = excluded.conflict_files_json,
	conflict_output = excluded.conflict_output,
	updated_at = excluded.updated_at`,
		in.TaskID, in.WorktreeID, in.Branch, in.Path, in.RootID, in.NodeRunID,
		in.State, in.MergedCommit, string(conflictJSON), in.ConflictOutput,
		in.CreatedAt.UTC().Format(time.RFC3339Nano), nowStr)
	if err != nil {
		return WorktreeEntry{}, err
	}
	got, err := s.GetWorktreeEntry(ctx, in.TaskID, in.WorktreeID)
	if err != nil {
		return in, err
	}
	return got, nil
}

func scanWorktreeEntry(row interface {
	Scan(dest ...any) error
}) (WorktreeEntry, error) {
	var e WorktreeEntry
	var conflictJSON, created, updated string
	err := row.Scan(&e.TaskID, &e.WorktreeID, &e.Branch, &e.Path, &e.RootID, &e.NodeRunID,
		&e.State, &e.MergedCommit, &conflictJSON, &e.ConflictOutput, &created, &updated)
	if err != nil {
		return WorktreeEntry{}, err
	}
	var files []string
	if err := json.Unmarshal([]byte(conflictJSON), &files); err == nil {
		e.ConflictFiles = files
	}
	e.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	e.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return e, nil
}

func (s *TaskStore) GetWorktreeEntry(ctx context.Context, taskID, worktreeID string) (WorktreeEntry, error) {
	return scanWorktreeEntry(s.db.QueryRowContext(ctx, `
SELECT task_id, worktree_id, branch, path, root_id, node_run_id, state, merged_commit, conflict_files_json, conflict_output, created_at, updated_at
FROM task_worktrees WHERE task_id = ? AND worktree_id = ?`,
		strings.TrimSpace(taskID), strings.TrimSpace(worktreeID)))
}

func (s *TaskStore) ListWorktreeEntries(ctx context.Context, taskID string) ([]WorktreeEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT task_id, worktree_id, branch, path, root_id, node_run_id, state, merged_commit, conflict_files_json, conflict_output, created_at, updated_at
FROM task_worktrees WHERE task_id = ? ORDER BY created_at ASC, worktree_id ASC`,
		strings.TrimSpace(taskID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorktreeEntry
	for rows.Next() {
		e, err := scanWorktreeEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListActiveWorktreeEntries 只返回 state=active 的行（正在干活的那几棵）。
func (s *TaskStore) ListActiveWorktreeEntries(ctx context.Context, taskID string) ([]WorktreeEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT task_id, worktree_id, branch, path, root_id, node_run_id, state, merged_commit, conflict_files_json, conflict_output, created_at, updated_at
FROM task_worktrees WHERE task_id = ? AND state = ? ORDER BY created_at ASC, worktree_id ASC`,
		strings.TrimSpace(taskID), WorktreeStateActive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorktreeEntry
	for rows.Next() {
		e, err := scanWorktreeEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// FindWorktreeEntryByNodeRun 按归属 NodeRun 找行（WP-05 接入点；现阶段返回
// sql.ErrNoRows 时调用方落回 legacy 单 worktree 路径）。
func (s *TaskStore) FindWorktreeEntryByNodeRun(ctx context.Context, taskID, nodeRunID string) (WorktreeEntry, error) {
	return scanWorktreeEntry(s.db.QueryRowContext(ctx, `
SELECT task_id, worktree_id, branch, path, root_id, node_run_id, state, merged_commit, conflict_files_json, conflict_output, created_at, updated_at
FROM task_worktrees WHERE task_id = ? AND node_run_id = ? ORDER BY created_at ASC LIMIT 1`,
		strings.TrimSpace(taskID), strings.TrimSpace(nodeRunID)))
}

// UpdateWorktreeEntryState 只改 state（+ 可选的 merged/conflict 字段），列级 UPDATE。
// 守卫：从 merged/needs_user 回到 active 必须显式走 Upsert（本方法拒绝，防静默复活）。
func (s *TaskStore) UpdateWorktreeEntryState(ctx context.Context, taskID, worktreeID, state string, mergedCommit string, conflictFiles []string, conflictOutput string) (WorktreeEntry, error) {
	taskID = strings.TrimSpace(taskID)
	worktreeID = strings.TrimSpace(worktreeID)
	state = strings.TrimSpace(state)
	if taskID == "" || worktreeID == "" {
		return WorktreeEntry{}, errors.New("task id and worktree id required")
	}
	switch state {
	case WorktreeStateActive, WorktreeStateMerged, WorktreeStateClosed, WorktreeStateNeedsUser:
	default:
		return WorktreeEntry{}, errors.New("invalid worktree state")
	}
	cur, err := s.GetWorktreeEntry(ctx, taskID, worktreeID)
	if err != nil {
		return WorktreeEntry{}, err
	}
	if state == WorktreeStateActive && (cur.State == WorktreeStateMerged || cur.State == WorktreeStateNeedsUser) {
		return WorktreeEntry{}, errors.New("worktree already " + cur.State + "; reopen via UpsertWorktreeEntry")
	}
	conflictJSON, _ := json.Marshal(conflictFiles)
	if conflictJSON == nil {
		conflictJSON = []byte("[]")
	}
	// conflict 清空只在 state 离开 needs_user 时（merged/closed 时清掉旧冲突记录）。
	if state != WorktreeStateNeedsUser {
		conflictJSON = []byte("[]")
		conflictOutput = ""
	}
	nowStr := s.now().UTC().Format(time.RFC3339Nano)
	_, err = s.db.ExecContext(ctx,
		`UPDATE task_worktrees SET state = ?, merged_commit = ?, conflict_files_json = ?, conflict_output = ?, updated_at = ? WHERE task_id = ? AND worktree_id = ?`,
		state, strings.TrimSpace(mergedCommit), string(conflictJSON), strings.TrimSpace(conflictOutput), nowStr, taskID, worktreeID)
	if err != nil {
		return WorktreeEntry{}, err
	}
	return s.GetWorktreeEntry(ctx, taskID, worktreeID)
}

// InsertMergeConflictActivity 往 WP-01 的 task_activities 插一行 merge_conflict。
// 只插 activity，不建 ContinueRequest（冲突不是新留言，不触发执行）。
func (s *TaskStore) InsertMergeConflictActivity(ctx context.Context, taskID, worktreeID, branch, detail string) (TaskActivity, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return TaskActivity{}, errors.New("task id required")
	}
	if _, err := s.GetTask(ctx, taskID); err != nil {
		return TaskActivity{}, err
	}
	now := s.now().UTC()
	body := "worktree " + strings.TrimSpace(worktreeID)
	if b := strings.TrimSpace(branch); b != "" {
		body += "（分支 " + b + "）"
	}
	body += " 合并冲突，需人工处理"
	if d := strings.TrimSpace(detail); d != "" {
		body += "：" + d
	}
	act := TaskActivity{
		ID:        newID("merge_conflict"),
		TaskID:    taskID,
		Type:      ActivityTypeMergeConflict,
		Author:    "system",
		Role:      "system",
		Body:      body,
		CreatedAt: now,
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO task_activities (id, task_id, type, author, role, body, workflow_choice, workflow_version_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		act.ID, act.TaskID, act.Type, act.Author, act.Role, act.Body, CommentWorkflowChoiceEmpty, "", now.Format(time.RFC3339Nano))
	if err != nil {
		return TaskActivity{}, err
	}
	return act, nil
}

var errWorktreeEntryNotFound = sql.ErrNoRows
