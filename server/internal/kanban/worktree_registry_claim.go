package kanban

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrWorktreeOwnerConflict 表示这棵活动工作树已有另一个 NodeRun 所有者，
// 或同一活动分支已被另一棵工作树占用。调用方必须保留既有 registry 行不动，
// 让冲突显式暴露（needs-user/重试），不能静默改写 owner。
var ErrWorktreeOwnerConflict = errors.New("worktree is already owned by another active node run")

// ClaimWorktreeEntry 是 D16 的活动工作树归属声明：只有 owner 本人（或非 active
// 的历史行被重新认领）才能写入 (task_id, worktree_id)。与 UpsertWorktreeEntry 的
// 区别是后者会无条件覆盖 node_run_id —— 那正是两个 NodeRun 抢同一棵树时丢 owner
// 的根因。活动分支也做唯一性检查：git 里同一分支不能被两棵 worktree 同时检出。
func (s *TaskStore) ClaimWorktreeEntry(ctx context.Context, in WorktreeEntry) (WorktreeEntry, error) {
	in, err := normalizeWorktreeEntry(in)
	if err != nil {
		return WorktreeEntry{}, err
	}
	in.NodeRunID = strings.TrimSpace(in.NodeRunID)
	if in.NodeRunID == "" {
		return WorktreeEntry{}, errors.New("node run id required to claim a worktree")
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

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WorktreeEntry{}, err
	}
	defer tx.Rollback()

	if in.State == WorktreeStateActive && in.Branch != "" {
		var owner string
		err := tx.QueryRowContext(ctx, `
SELECT node_run_id FROM task_worktrees
WHERE task_id=? AND branch=? AND state=? AND node_run_id<>'' AND node_run_id<>? AND worktree_id<>?
LIMIT 1`,
			in.TaskID, in.Branch, WorktreeStateActive, in.NodeRunID, in.WorktreeID).Scan(&owner)
		if err == nil {
			return WorktreeEntry{}, fmt.Errorf("%w: branch %q is checked out by node run %s", ErrWorktreeOwnerConflict, in.Branch, owner)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return WorktreeEntry{}, err
		}
	}

	res, err := tx.ExecContext(ctx, `
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
	updated_at = excluded.updated_at
WHERE task_worktrees.node_run_id = excluded.node_run_id
   OR task_worktrees.state <> ?`,
		in.TaskID, in.WorktreeID, in.Branch, in.Path, in.RootID, in.NodeRunID,
		in.State, in.MergedCommit, string(conflictJSON), in.ConflictOutput,
		in.CreatedAt.UTC().Format(time.RFC3339Nano), nowStr, WorktreeStateActive)
	if err != nil {
		return WorktreeEntry{}, err
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return WorktreeEntry{}, err
	}
	if changed != 1 {
		return WorktreeEntry{}, fmt.Errorf("%w: %s", ErrWorktreeOwnerConflict, in.WorktreeID)
	}
	if err := tx.Commit(); err != nil {
		return WorktreeEntry{}, err
	}
	return s.GetWorktreeEntry(ctx, in.TaskID, in.WorktreeID)
}
