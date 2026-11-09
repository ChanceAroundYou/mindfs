package kanban

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"mindfs/server/internal/gitview"
)

// service_worktree_merge：多 worktree 自动合并到集成分支（WP-04）。
//
// 策略（spec「多 worktree 与自动合并」）：
//   - 成功 → registry state=merged（+ 可选关目录删分支），合并提交落到集成分支；
//   - 冲突 → registry state=needs_user + task_activities 落一行 merge_conflict
//     activity + 任务卡片 session_error，两分支都保留，**不自动解、不 stash、
//     不覆盖用户改动**（gitview.MergeBranch 撞冲突后停在 MERGE_HEAD，本文件不做
//     abort/stash 任何收尾动作；用户解完冲突提交后重跑本方法即可收敛）；
//   - 主 checkout 脏 → 直接返回脏错误（同 FinishTaskWorktree 口径：那是「你自己
//     的活还没收」，下一步提交/暂存，不是重试）；
//   - 幂等：已 merged/needs_user 的行重复调按当前状态返回，不重复合并。
//
// git 调用一律走 gitview 的安全 API（exec 参数数组，无 shell 字符串拼接 ——
// git_command.go 的 newGitCommand 用 exec.CommandContext + args 切片，
// 另有 30s 超时兜底）。本文件**不**拼任何 git 命令行。

// AutoMergeWorktreeInput 是一次自动合并的请求。
type AutoMergeWorktreeInput struct {
	RootID     string
	TaskID     string
	NodeRunID  string
	WorktreeID string
	// IntegrationBranch 是集成分支；空 → "main"（与 gitview.MergeBranch 一致）。
	IntegrationBranch string
	// CloseAfterMerge 成功后是否拆目录 + 删分支（默认 true 的语义由调用方显式置）。
	CloseAfterMerge bool
	// DeleteBranch 仅 CloseAfterMerge=true 时生效（branch -d 安全删）。
	DeleteBranch bool
}

// AutoMergeWorktreeResult 是一次自动合并的结果。
type AutoMergeWorktreeResult struct {
	Entry         WorktreeEntry `json:"entry"`
	Merged        bool          `json:"merged"`
	Commit        string        `json:"commit,omitempty"`
	ConflictFiles []string      `json:"conflict_files,omitempty"`
	NeedsUser     bool          `json:"needs_user,omitempty"`
}

// MergeConflictError 是自动合并撞上冲突的对外形态：registry 已翻 needs_user、
// activity 已落库，两分支保留，用户解完冲突后重跑即可。
type MergeConflictError struct {
	WorktreeID    string
	ConflictFiles []string
	Output        string
}

func (e *MergeConflictError) Error() string {
	files := strings.Join(e.ConflictFiles, ", ")
	if files == "" {
		files = "(见 git 输出)"
	}
	return fmt.Sprintf("worktree %s 合并冲突，需人工处理：%s", e.WorktreeID, files)
}

func (e *MergeConflictError) Unwrap() error { return gitview.ErrMergeConflict }

// AutoMergeWorktree 把一棵 registry worktree 的分支合并到集成分支。
// 成功自动关（CloseAfterMerge 时）；冲突转 needs-user（见文件头策略）。
func (s *Service) AutoMergeWorktree(ctx context.Context, in AutoMergeWorktreeInput) (AutoMergeWorktreeResult, error) {
	store, task, err := s.loadForMove(ctx, strings.TrimSpace(in.RootID), strings.TrimSpace(in.TaskID))
	if err != nil {
		return AutoMergeWorktreeResult{}, err
	}
	var entry WorktreeEntry
	if wid := strings.TrimSpace(in.WorktreeID); wid != "" {
		entry, err = store.GetWorktreeEntry(ctx, task.ID, wid)
	} else if nrid := strings.TrimSpace(in.NodeRunID); nrid != "" {
		entry, err = store.FindWorktreeEntryByNodeRun(ctx, task.ID, nrid)
	} else {
		return AutoMergeWorktreeResult{}, errors.New("node_run_id or worktree_id required")
	}
	if err != nil {
		return AutoMergeWorktreeResult{}, err
	}
	// 幂等：已 merged/closed（合并已成、随后关闭）直接返回成功，不重复合并。
	if entry.State == WorktreeStateMerged || entry.State == WorktreeStateClosed {
		return AutoMergeWorktreeResult{Entry: entry}, nil
	}
	branch := strings.TrimSpace(entry.Branch)
	if branch == "" {
		branch = worktreeDirName(entry.Path)
	}
	if branch == "" {
		return AutoMergeWorktreeResult{}, errors.New("worktree has no branch")
	}
	mainDir, err := s.taskWorktreeMainDir(ctx, task.RootID)
	if err != nil {
		return AutoMergeWorktreeResult{}, err
	}
	if err := s.assertWorktreeMatches(ctx, mainDir, entry.Path, branch); err != nil {
		return AutoMergeWorktreeResult{}, err
	}
	target := strings.TrimSpace(in.IntegrationBranch)
	if target == "" {
		target = "main"
	}
	merged, err := gitview.MergeBranch(ctx, gitview.MergeOptions{
		Source:       branch,
		Target:       target,
		MainDir:      mainDir,
		WorktreePath: entry.Path,
	})
	if err != nil {
		return s.handleAutoMergeError(ctx, store, task, entry, err)
	}
	// 成功（Merged=false = 已是祖先，同样算并入完成）。
	updated, err := store.UpdateWorktreeEntryState(ctx, task.ID, entry.WorktreeID, WorktreeStateMerged, merged.Commit, nil, "")
	if err != nil {
		return AutoMergeWorktreeResult{}, err
	}
	result := AutoMergeWorktreeResult{Entry: updated, Merged: merged.Merged, Commit: merged.Commit}
	if in.CloseAfterMerge {
		if _, err := s.CloseNodeWorktree(ctx, CloseNodeWorktreeInput{
			RootID: task.RootID, TaskID: task.ID, WorktreeID: entry.WorktreeID,
			RemoveDir: true, DeleteBranch: in.DeleteBranch,
		}); err != nil {
			// 合并已成、关闭失败：如实返回（state 已是 merged，不会误报未合并）。
			result.Entry = updated
			return result, fmt.Errorf("合并已成功，但关闭 worktree 失败：%w", err)
		}
		if fresh, err := store.GetWorktreeEntry(ctx, task.ID, entry.WorktreeID); err == nil {
			result.Entry = fresh
		}
	}
	return result, nil
}

// handleAutoMergeError 把 MergeBranch 的失败翻译成 registry 状态：
// 冲突 → needs_user + activity；脏 → 原样返回（调用方先清主 checkout）；
// 其它 → 原样返回（不翻状态，下次可重跑）。
func (s *Service) handleAutoMergeError(ctx context.Context, store *TaskStore, task Task, entry WorktreeEntry, mergeErr error) (AutoMergeWorktreeResult, error) {
	var conflict gitview.MergeConflict
	if errors.As(mergeErr, &conflict) {
		conflictFiles := conflict.ConflictFiles
		updated, err := store.UpdateWorktreeEntryState(ctx, task.ID, entry.WorktreeID,
			WorktreeStateNeedsUser, "", conflictFiles, conflict.Output)
		if err != nil {
			return AutoMergeWorktreeResult{}, err
		}
		// merge_conflict activity（WP-01 留言板时间线）+ 卡片 session_error。
		// 两者都 best-effort：activity 失败不吞掉冲突本身（registry 已记）。
		if _, err := store.InsertMergeConflictActivity(ctx, task.ID, entry.WorktreeID, entry.Branch, strings.Join(conflictFiles, ", ")); err != nil {
			// activity 表不存在（库未迁 comments）时不致命：registry needs_user 仍在。
			_ = err
		}
		_ = s.recordTaskError(ctx, store, task, "", "worktree "+entry.WorktreeID+" 合并冲突，需人工处理")
		return AutoMergeWorktreeResult{Entry: updated, NeedsUser: true, ConflictFiles: conflictFiles},
			&MergeConflictError{WorktreeID: entry.WorktreeID, ConflictFiles: conflictFiles, Output: conflict.Output}
	}
	var dirty *gitview.MainCheckoutDirtyError
	if errors.As(mergeErr, &dirty) {
		return AutoMergeWorktreeResult{}, &FinishWorktreeDirty{Files: dirty.Files}
	}
	if errors.Is(mergeErr, gitview.ErrMergeUncommitted) {
		return AutoMergeWorktreeResult{}, &FinishWorktreeUserChanges{Files: nil}
	}
	return AutoMergeWorktreeResult{}, mergeErr
}
