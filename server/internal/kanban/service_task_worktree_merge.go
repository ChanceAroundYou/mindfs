package kanban

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"mindfs/server/internal/gitview"
)

// 任务级 worktree 的收尾：一轮 Run 全部成功后，把任务树合并回主 checkout 当前分支。
//
// 这是「w2 → w1 → main」的最后一跳（w2 由节点自己的自动合并并进 w1）。它与
// AutoMergeWorktree 刻意分开，因为语义相反：
//   - 节点树是一次性的，合完就关目录、删分支，registry 行翻成 merged；
//   - 任务树要活到任务结束，合完**保持 active**，下一轮还要从它继续长。若复用
//     executeAutoMerge 的 merged 短路，第二次投影就会看到 merged 直接返回、再也
//     不会把新活合并回 main。
//
// 冲突按既有 needs-user 策略：两个分支都保留、不自动解、不 stash；落
// merge_conflict 活动 + 卡片可见错误，任务停在 waiting_user 等人工处理。

// taskWorktreeBranchOf 返回任务主 worktree 的分支。WorktreeBranch 为空时用目录名：
// 建树时分支名就是目录名（usecase.CreateGitWorktree 在 branch 为空时取 Name），
// 这是存量任务不写这一列时的可靠回落。
func taskWorktreeBranchOf(task Task) string {
	if branch := strings.TrimSpace(task.WorktreeBranch); branch != "" {
		return branch
	}
	return worktreeDirName(task.WorktreePath)
}

// autoMergeTaskWorktreeAfterRun 判断并执行「任务树 → 主分支」。幂等：源分支已在
// 目标里时 MergeBranch 返回 Merged=false，重复投影不会造出空合并提交。
func (s *Service) autoMergeTaskWorktreeAfterRun(ctx context.Context, store *TaskStore, run *WorkflowRun) error {
	if s == nil || store == nil || run == nil || s.Runner == nil {
		return nil
	}
	task, err := store.GetTask(ctx, run.TaskID)
	if err != nil {
		return err
	}
	if !task.CreateWorktree || !worktreeDirUsable(task.WorktreePath) {
		return nil
	}
	branch := taskWorktreeBranchOf(task)
	// 只有「这一轮的合并目标就是任务树」才轮到这里收尾。集成目标仍是主 checkout
	// 分支时（没有任务树，或 Run 建在任务树之前）节点树已经直接并进 main 了。
	if branch == "" || strings.TrimSpace(run.IntegrationBranch) != branch {
		return nil
	}
	mainDir, err := s.taskWorktreeMainDir(ctx, task.RootID)
	if err != nil {
		return err
	}
	target, err := gitview.CurrentBranch(ctx, mainDir)
	if err != nil {
		return err
	}
	// 主 checkout 处于 detached HEAD 时不猜合并目标：那可能是用户正在检视的
	// 某个提交，往上合并会造出一个谁都没想到的分支位置。
	if target == "" || target == branch {
		return nil
	}
	if err := s.assertWorktreeMatches(ctx, mainDir, task.WorktreePath, branch); err != nil {
		return err
	}
	release, err := s.AcquireNodeWriter(ctx, mainDir, target)
	if err != nil {
		return err
	}
	defer release()
	merged, err := gitview.MergeBranch(ctx, gitview.MergeOptions{
		Source:       branch,
		Target:       target,
		MainDir:      mainDir,
		WorktreePath: task.WorktreePath,
		Message:      fmt.Sprintf("Merge task worktree '%s' into '%s'", branch, target),
	})
	if err != nil {
		return s.handleTaskWorktreeMergeError(ctx, store, task, branch, err)
	}
	if !merged.Merged {
		// 已经是目标的历史（重复投影 / 上一轮已合）。不写事件，避免刷屏。
		return nil
	}
	_ = store.AddEvent(ctx, TaskEvent{
		ID: newID("event"), TaskID: task.ID, Type: "task_worktree_merged",
		Payload: eventPayload(map[string]any{
			"branch": branch, "target": target, "commit": merged.Commit,
		}),
		CreatedAt: time.Now().UTC(),
	})
	return nil
}

// handleTaskWorktreeMergeError 把冲突变成可见的 needs-user：两个分支都保留，
// 现场不动（MERGE_HEAD / 未合并文件留在主 checkout 里），用户解完冲突再点收尾。
func (s *Service) handleTaskWorktreeMergeError(ctx context.Context, store *TaskStore, task Task, branch string, mergeErr error) error {
	var conflict gitview.MergeConflict
	if errors.As(mergeErr, &conflict) {
		// 主 worktree 的 registry 行（node_run_id 空）存在时一并标 needs_user；
		// 老任务没有这行也不影响合并冲突本身的可见性。
		if entry, err := store.GetWorktreeEntry(ctx, task.ID, branch); err == nil {
			_, _ = store.UpdateWorktreeEntryState(ctx, task.ID, entry.WorktreeID,
				WorktreeStateNeedsUser, "", conflict.ConflictFiles, conflict.Output)
		}
		_, _ = store.InsertMergeConflictActivity(ctx, task.ID, branch, branch, strings.Join(conflict.ConflictFiles, ", "))
		_ = s.recordTaskError(ctx, store, task, "任务 worktree "+branch+" 合并回主分支冲突，需人工处理")
		return &MergeConflictError{WorktreeID: branch, ConflictFiles: conflict.ConflictFiles, Output: conflict.Output}
	}
	var dirty *gitview.MainCheckoutDirtyError
	if errors.As(mergeErr, &dirty) {
		_ = s.recordTaskError(ctx, store, task, "主分支工作区有未提交改动，任务 worktree 暂不能合并")
		return &FinishWorktreeDirty{Files: dirty.Files}
	}
	if errors.Is(mergeErr, gitview.ErrMergeUncommitted) {
		_ = s.recordTaskError(ctx, store, task, "主分支还停在上一次未提交的合并里，先处理它")
		return &FinishWorktreeUserChanges{Files: nil}
	}
	return mergeErr
}
