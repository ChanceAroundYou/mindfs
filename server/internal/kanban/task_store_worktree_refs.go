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
