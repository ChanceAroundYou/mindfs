package kanban

import (
	"context"
	"strings"
)

// WorktreeReferenceCount 数还有几个**别的**任务盯着这个 worktree 目录。
//
// 只给「共享 worktree」的收尾用（见 worktree_finish.go 的引用计数分流）：
// 一个目录被多个任务共用时，先收尾的那个只该解绑自己，最后一个才合并/拆除。
//
// 按 worktree_path 精确比较而不是按分支或按目录名：绑定路径是
// ensureTaskWorktree 写进去的那一份，同一棵树上的任务写的是同一个字符串。
// 路径为空一律返回 0 —— 那是「还没建树」，不是「大家都在用主 checkout」。
func (s *TaskStore) WorktreeReferenceCount(ctx context.Context, path, excludeTaskID string) (int, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return 0, nil
	}
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tasks WHERE worktree_path = ? AND id <> ?`,
		path, strings.TrimSpace(excludeTaskID),
	).Scan(&count)
	return count, err
}

// ── 多 worktree 引用计数（WP-04，扩展 legacy）───────────────────────────────
//
// legacy 的 WorktreeReferenceCount 只数 tasks.worktree_path（「主 worktree」）。
// 多 worktree 时还有 task_worktrees 的行在计数：收尾/关闭一棵树前要确认没有别的
// **活跃** entry 还钉着同一个目录。三个口径：
//   - RegistryReferenceCount：只数 registry（state=active）；
//   - WorktreePathReferenceCount：registry + legacy tasks 列之和（关闭判据）；
//   - legacy WorktreeReferenceCount 不变（存量行为零回归，旧测试原样钉住）。

// RegistryReferenceCount 数 registry 里 state=active、且 path 相同、排除指定
// worktree_id 之后还剩几个。path 为空一律 0（同 legacy 口径：没建树不是共享）。
func (s *TaskStore) RegistryReferenceCount(ctx context.Context, path, excludeWorktreeID string) (int, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return 0, nil
	}
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM task_worktrees WHERE path = ? AND state = ? AND worktree_id <> ?`,
		path, WorktreeStateActive, strings.TrimSpace(excludeWorktreeID),
	).Scan(&count)
	return count, err
}

// RegistryReferenceCountForTask 数**同一 task** 内 active entries 里 path 相同
// （排除自己）还剩几个 —— 多 worktree 同 task 关闭时的判据：同 task 的另一棵
// active 树共用同一目录时不能拆。
func (s *TaskStore) RegistryReferenceCountForTask(ctx context.Context, taskID, path, excludeWorktreeID string) (int, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return 0, nil
	}
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM task_worktrees WHERE task_id = ? AND path = ? AND state = ? AND worktree_id <> ?`,
		strings.TrimSpace(taskID), path, WorktreeStateActive, strings.TrimSpace(excludeWorktreeID),
	).Scan(&count)
	return count, err
}

// WorktreePathReferenceCount = registry active 引用 + legacy tasks 列引用。
// 关闭一棵 registry 树时用它：任一非零都不能拆目录。
func (s *TaskStore) WorktreePathReferenceCount(ctx context.Context, path, excludeTaskID, excludeWorktreeID string) (int, error) {
	legacy, err := s.WorktreeReferenceCount(ctx, path, excludeTaskID)
	if err != nil {
		return 0, err
	}
	reg, err := s.RegistryReferenceCount(ctx, path, excludeWorktreeID)
	if err != nil {
		return 0, err
	}
	return legacy + reg, nil
}
