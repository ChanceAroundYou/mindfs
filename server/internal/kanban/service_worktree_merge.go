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
	// IntegrationBranch 是 Run 创建时快照的集成分支（D11）。空 = 缺失，直接失败；
	// 不允许运行时隐含回落 "main"。
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

// autoMergePlan 是一次已解析的合并请求：幂等判断、目标分支与主 checkout 都已
// 确定，只差（可选的）主 checkout writer gate 和真正的 git 合并。
//
// 拆出来是为了让调用方在**收尾锁之外**等待主 checkout gate。等待可能很久
// （另一个写者正在跑 agent 回合），如果此时还握着 NodeRun 收尾锁，操作员取消
// 就会被一个无关的写者无限期阻塞。
type autoMergePlan struct {
	store           *TaskStore
	task            Task
	entry           WorktreeEntry
	branch          string
	mainDir         string
	target          string
	done            *AutoMergeWorktreeResult
	closeAfterMerge bool
	deleteBranch    bool
}

// needsGate 报告执行该计划前是否必须持有主 checkout 的物理写 gate。
func (p autoMergePlan) needsGate() bool {
	if p.done != nil {
		// 已 closed 无事可做；已 merged 但要求关闭时，拆目录/删分支同样改写
		// 主 checkout，必须与其它写者互斥。
		return p.entry.State == WorktreeStateMerged && p.closeAfterMerge
	}
	return true
}

// prepareAutoMerge 只做读与校验，不拿 gate、不动 git。解析顺序与原
// AutoMergeWorktree 一致：幂等的 merged/closed 先行返回，空目标分支直接拒绝。
func (s *Service) prepareAutoMerge(ctx context.Context, in AutoMergeWorktreeInput) (autoMergePlan, error) {
	store, task, err := s.loadForMove(ctx, strings.TrimSpace(in.RootID), strings.TrimSpace(in.TaskID))
	if err != nil {
		return autoMergePlan{}, err
	}
	var entry WorktreeEntry
	if wid := strings.TrimSpace(in.WorktreeID); wid != "" {
		entry, err = store.GetWorktreeEntry(ctx, task.ID, wid)
	} else if nrid := strings.TrimSpace(in.NodeRunID); nrid != "" {
		entry, err = store.FindWorktreeEntryByNodeRun(ctx, task.ID, nrid)
	} else {
		return autoMergePlan{}, errors.New("node_run_id or worktree_id required")
	}
	if err != nil {
		return autoMergePlan{}, err
	}
	plan := autoMergePlan{
		store: store, task: task, entry: entry,
		closeAfterMerge: in.CloseAfterMerge, deleteBranch: in.DeleteBranch,
	}
	// 幂等：已 merged/closed（合并已成、随后关闭）直接返回成功，不重复合并。
	// 但 merged 且调用方要求关闭时仍要重试关闭：上次可能「合并成功、拆目录失败」，
	// 重试只补关闭，绝不再合一次。
	if entry.State == WorktreeStateMerged || entry.State == WorktreeStateClosed {
		result := AutoMergeWorktreeResult{Entry: entry, Commit: entry.MergedCommit}
		plan.done = &result
		if entry.State == WorktreeStateMerged && in.CloseAfterMerge {
			mainDir, err := s.taskWorktreeMainDir(ctx, task.RootID)
			if err != nil {
				return autoMergePlan{}, err
			}
			plan.mainDir = mainDir
		}
		return plan, nil
	}
	branch := strings.TrimSpace(entry.Branch)
	if branch == "" {
		branch = worktreeDirName(entry.Path)
	}
	if branch == "" {
		return autoMergePlan{}, errors.New("worktree has no branch")
	}
	mainDir, err := s.taskWorktreeMainDir(ctx, task.RootID)
	if err != nil {
		return autoMergePlan{}, err
	}
	if err := s.assertWorktreeMatches(ctx, mainDir, entry.Path, branch); err != nil {
		return autoMergePlan{}, err
	}
	target := strings.TrimSpace(in.IntegrationBranch)
	if target == "" {
		// D11：merge target 只能来自 Run 创建时的快照。运行期隐含回落 "main"
		// 会把工作合进用户没选的集成分支。
		return autoMergePlan{}, ErrWorkflowIntegrationBranchMissing
	}
	plan.branch, plan.mainDir, plan.target = branch, mainDir, target
	return plan, nil
}

// executeAutoMerge 执行已解析的计划。调用方必须已按 needsGate 取得主
// checkout writer gate；本函数自己不再拿锁，避免与「gate 先于收尾锁」的
// 调用顺序互相等待。
func (s *Service) executeAutoMerge(ctx context.Context, plan autoMergePlan) (AutoMergeWorktreeResult, error) {
	store, task, entry := plan.store, plan.task, plan.entry
	if plan.done != nil {
		result := *plan.done
		if entry.State == WorktreeStateMerged && plan.closeAfterMerge {
			if _, err := s.CloseNodeWorktree(ctx, CloseNodeWorktreeInput{
				RootID: task.RootID, TaskID: task.ID, WorktreeID: entry.WorktreeID,
				RemoveDir: true, DeleteBranch: plan.deleteBranch,
			}); err != nil {
				return result, fmt.Errorf("合并已成功，但关闭 worktree 失败：%w", err)
			}
			if fresh, err := store.GetWorktreeEntry(ctx, task.ID, entry.WorktreeID); err == nil {
				result.Entry = fresh
			}
		}
		return result, nil
	}
	merged, err := gitview.MergeBranch(ctx, gitview.MergeOptions{
		Source:       plan.branch,
		Target:       plan.target,
		MainDir:      plan.mainDir,
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
	if plan.closeAfterMerge {
		if _, err := s.CloseNodeWorktree(ctx, CloseNodeWorktreeInput{
			RootID: task.RootID, TaskID: task.ID, WorktreeID: entry.WorktreeID,
			RemoveDir: true, DeleteBranch: plan.deleteBranch,
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

// AutoMergeWorktree 把一棵 registry worktree 的分支合并到集成分支。
// 成功自动关（CloseAfterMerge 时）；冲突转 needs-user（见文件头策略）。
func (s *Service) AutoMergeWorktree(ctx context.Context, in AutoMergeWorktreeInput) (AutoMergeWorktreeResult, error) {
	plan, err := s.prepareAutoMerge(ctx, in)
	if err != nil {
		return AutoMergeWorktreeResult{}, err
	}
	if plan.needsGate() {
		releaseGate, err := s.AcquireNodeWriter(ctx, plan.mainDir, plan.target)
		if err != nil {
			return AutoMergeWorktreeResult{}, err
		}
		defer releaseGate()
	}
	return s.executeAutoMerge(ctx, plan)
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
