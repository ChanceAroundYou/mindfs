package session

import "strings"

// RuntimeRootPath 返回会话此刻真正干活的目录。
//
// 判据只有两个来源：显式钉住的 WorkDir，或是仍归属工作树时的 RelatedWorktree.Path。
// 后者只有在「绑了任务」或「来源是 worktree」时才算 —— 普通会话被顺手关联到某个文件
// 目录，不代表它在该目录里跑。
//
// api 的巡检与 usecase 的 repoint 必须用同一份判据：两边各写一份时，巡检说「目录已删」
// 而 repoint 说「还在原地」，用户看到的是修不动的会话。
func RuntimeRootPath(s *Session) string {
	if s == nil {
		return ""
	}
	if dir := strings.TrimSpace(s.WorkDir); dir != "" {
		return dir
	}
	if s.RelatedWorktree == nil {
		return ""
	}
	if strings.TrimSpace(s.TaskID) == "" && strings.TrimSpace(s.Source) != "worktree" {
		return ""
	}
	return strings.TrimSpace(s.RelatedWorktree.Path)
}
