package kanban

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"mindfs/server/internal/gitview"
)

// ── WP-04 多 worktree：按 NodeRun 归属创建/关闭（registry 化）────────────────
//
// 命名：spec 说「节点可声明需要独立 worktree 时自动创建」，NodeRun 是 WP-05 才
// 落地的执行行（目前不存在）。这里的参数名用 NodeRunID，但**类型仍是 string**：
// WP-05 接入时只传 NodeRun.ID 进来，不改本节签名。空 NodeRunID = legacy/stage
// 归属（等价于 ensureTaskWorktree 的主 worktree）。
//
// 生命周期：EnsureNodeWorktree 建树 + 注册（state=active）；CloseNodeWorktree
// 按归属关闭（默认只做状态翻转 + 可选拆目录，合并由 service_worktree_merge.go
// 的 AutoMergeWorktree 在关闭前完成）；幂等可重复调。
//
// 与单 worktree 的关系：ensureTaskWorktree（上文）仍是 legacy 主路径，本节是
// 加法 —— 主 worktree 也双写进 registry（node_run_id 空），这样 registry 是
// 超集、关闭/合并逻辑只走一处。存量任务（registry 为空）走 legacy 路径，行为不变。

// EnsureNodeWorktreeInput 是一次「节点声明独立 worktree」的请求。
type EnsureNodeWorktreeInput struct {
	RootID    string
	TaskID    string
	NodeRunID string // 空 = legacy/stage 归属（主 worktree 语义）
	// Branch 是这个 worktree 的分支；空 → 按任务的 WorktreeBranch 规则（new 时用目录名）。
	Branch string
	// BranchMode "new"（默认）或 "existing"（认领已检出分支，语义同 ensureTaskWorktree）。
	BranchMode string
	// Name 是目录名；空 → renderWorktreeName（task-N）。
	Name string
	// WorktreeID 显式身份；空 → Branch（分支天然是身份）。
	WorktreeID string
	// IntegrationBranch 是这棵树最终要并回的集成分支（自动合并用，见 service_worktree_merge.go）。
	// 这里只记录意图，不合并；空 → "main"（与 gitview.MergeBranch 默认一致）。
	IntegrationBranch string
	// CreateWorktree 显式关掉时直接返回错误（调用方声明了 worktree 但任务没开）。
	// 空任务 CreateWorktree=false 时，本方法仍可建（节点级显式声明优先于任务级开关）。
}

// ensureRegistry 双写 helper：best-effort 注册主 worktree 进 registry。
// 失败**不**回滚 ensureTaskWorktree 的 legacy 写入：legacy 列仍是真相源，
// registry 漏一行只影响多 worktree 视图，不影响单 worktree 收尾。调用方应在
// DB 层面有感知（这里记日志由调用方决定，本函数静默吞掉以保持 legacy 零回归）。
func (s *Service) registerPrimaryWorktree(ctx context.Context, store *TaskStore, task Task) {
	branch := strings.TrimSpace(task.WorktreeBranch)
	if branch == "" {
		branch = worktreeDirName(task.WorktreePath)
	}
	_, _ = store.UpsertWorktreeEntry(ctx, WorktreeEntry{
		TaskID: task.ID, WorktreeID: branch, Branch: branch,
		Path: task.WorktreePath, RootID: task.WorktreeRootID,
		NodeRunID: "", State: WorktreeStateActive,
	})
}

// EnsureNodeWorktree 按 NodeRun 归属创建（或认领/复用）一棵独立 worktree，
// 并注册进 task_worktrees（state=active）。幂等：同一 (task, NodeRun) 已有
// active 行且目录可用时直接返回现有行，不新建。
//
// 「自动创建」在什么时机触发：由 WP-05 workflow engine 在节点启动时调用；
// legacy 路径（StageRun）继续走 ensureTaskWorktree。本方法不依赖 NodeRun 行
// 已存在：归属只记 node_run_id 字符串，WP-05 落地前传 stage_run_id 也可。
func (s *Service) EnsureNodeWorktree(ctx context.Context, in EnsureNodeWorktreeInput) (WorktreeEntry, error) {
	store, task, err := s.loadForMove(ctx, strings.TrimSpace(in.RootID), strings.TrimSpace(in.TaskID))
	if err != nil {
		return WorktreeEntry{}, err
	}
	if s.Runner == nil {
		return WorktreeEntry{}, errors.New("task runner not configured")
	}
	nodeRunID := strings.TrimSpace(in.NodeRunID)
	// 幂等：已有 active 行（按 NodeRun 归属）且目录可用 → 返回。
	if nodeRunID != "" {
		if e, err := store.FindWorktreeEntryByNodeRun(ctx, task.ID, nodeRunID); err == nil {
			if e.State == WorktreeStateActive && worktreeDirUsable(e.Path) {
				return e, nil
			}
			if e.State == WorktreeStateNeedsUser {
				return WorktreeEntry{}, fmt.Errorf("worktree %s 处于 needs_user（合并冲突待人工处理），先解决再重开", e.WorktreeID)
			}
		}
	}
	branchMode, branch := normalizeTaskWorktreeBranch(in.BranchMode, in.Branch)
	if strings.TrimSpace(in.Branch) != "" {
		// 显式给了 Branch：mode 尊重调用方，branch 以调用方为准。
		branchMode = strings.TrimSpace(in.BranchMode)
		if branchMode == "" {
			branchMode = "new"
		}
		branch = strings.TrimSpace(in.Branch)
		if branchMode != "existing" {
			branchMode = "new"
		}
	}
	// 认领：existing 分支已被别的 linked worktree 检出时复用那棵树（与主路径同语义）。
	if branchMode == "existing" && branch != "" {
		if path, ok := s.worktreePathForBranch(ctx, task.RootID, branch); ok {
			return store.UpsertWorktreeEntry(ctx, WorktreeEntry{
				TaskID: task.ID, WorktreeID: strings.TrimSpace(in.WorktreeID), Branch: branch,
				Path: path, RootID: task.RootID, NodeRunID: nodeRunID, State: WorktreeStateActive,
			})
		}
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = renderWorktreeName("", task)
		if nodeRunID != "" {
			name = name + "-" + shortID(nodeRunID)
		}
	}
	wt, err := s.Runner.CreateTaskWorktree(ctx, task.RootID, name, branchMode, branch)
	if err != nil {
		return WorktreeEntry{}, err
	}
	if !worktreeDirUsable(wt.Path) {
		return WorktreeEntry{}, fmt.Errorf("worktree 创建没有产出可用目录（路径=%q），请重试", strings.TrimSpace(wt.Path))
	}
	// 分支名：new 时约等于目录名（CreateTaskWorktree 的 branch=new 用 name 当分支），
	// existing 时是调用方给的。registry 的 branch 落库以实际值为准。
	if branchMode == "new" {
		branch = name
	}
	return store.UpsertWorktreeEntry(ctx, WorktreeEntry{
		TaskID: task.ID, WorktreeID: strings.TrimSpace(in.WorktreeID), Branch: branch,
		Path: wt.Path, RootID: wt.RootID, NodeRunID: nodeRunID, State: WorktreeStateActive,
	})
}

// CloseNodeWorktreeInput 是一次「节点完成/取消 → 关闭归属 worktree」的请求。
type CloseNodeWorktreeInput struct {
	RootID     string
	TaskID     string
	NodeRunID  string
	WorktreeID string
	// RemoveDir 为 true 时连目录一起拆（仅在已合并/从未有活时由调用方置 true；
	// 冲突态禁止拆，见 service_worktree_merge.go 的守卫）。
	RemoveDir bool
	// DeleteBranch 为 true 时连分支一起删（只用 branch -d，见 gitview.DeleteBranch）。
	DeleteBranch bool
}

// CloseNodeWorktree 按 NodeRun 归属（或 worktree_id）把 registry 行翻到 closed，
// 可选拆目录/删分支。幂等：已 closed 直接返回。
// 守卫：state=needs_user 时拒绝关闭（冲突没解就拆 = 丢活）；目录还有别的
// active 引用时只翻状态不拆目录（引用计数判据，见 task_store_worktree_refs.go）。
func (s *Service) CloseNodeWorktree(ctx context.Context, in CloseNodeWorktreeInput) (WorktreeEntry, error) {
	store, task, err := s.loadForMove(ctx, strings.TrimSpace(in.RootID), strings.TrimSpace(in.TaskID))
	if err != nil {
		return WorktreeEntry{}, err
	}
	var entry WorktreeEntry
	if wid := strings.TrimSpace(in.WorktreeID); wid != "" {
		entry, err = store.GetWorktreeEntry(ctx, task.ID, wid)
	} else if nrid := strings.TrimSpace(in.NodeRunID); nrid != "" {
		entry, err = store.FindWorktreeEntryByNodeRun(ctx, task.ID, nrid)
	} else {
		return WorktreeEntry{}, errors.New("node_run_id or worktree_id required")
	}
	if err != nil {
		return WorktreeEntry{}, err
	}
	if entry.State == WorktreeStateNeedsUser {
		return WorktreeEntry{}, fmt.Errorf("worktree %s 处于 needs_user（合并冲突未解），关闭会丢活 —— 先人工解决冲突", entry.WorktreeID)
	}
	if entry.State == WorktreeStateClosed {
		return entry, nil
	}
	if in.RemoveDir {
		if removed, err := s.removeNodeWorktreeDir(ctx, task, entry, in.DeleteBranch); err != nil {
			return entry, err
		} else {
			_ = removed
		}
	}
	return store.UpdateWorktreeEntryState(ctx, task.ID, entry.WorktreeID, WorktreeStateClosed, entry.MergedCommit, nil, "")
}

// removeNodeWorktreeDir 拆一棵 registry worktree 的目录（+ 可选删分支）。
// 不用 --force、不 stash、不覆盖：有用户改动时拒绝（ClassifyWorktreeRemoveBlockers）。
func (s *Service) removeNodeWorktreeDir(ctx context.Context, task Task, entry WorktreeEntry, deleteBranch bool) (bool, error) {
	// 引用计数：还有别的 active 引用钉着同一目录时只解绑（状态翻转），不拆目录。
	refs, err := store_worktreePathRefs(ctx, s, task, entry)
	if err != nil {
		return false, err
	}
	if refs > 0 {
		return false, nil
	}
	mainDir, err := s.taskWorktreeMainDir(ctx, task.RootID)
	if err != nil {
		return false, err
	}
	if err := s.assertWorktreeMatches(ctx, mainDir, entry.Path, entry.Branch); err != nil {
		return false, err
	}
	if isWT, wtErr := gitview.IsWorktree(entry.Path); wtErr == nil && isWT {
		blockers, bErr := gitview.ClassifyWorktreeRemoveBlockers(ctx, entry.Path)
		if bErr != nil {
			return false, fmt.Errorf("拆除 worktree 前读不了 %s 的 git 状态，不敢替你清理：%w", entry.Path, bErr)
		}
		if len(blockers.UserChanges) > 0 {
			return false, fmt.Errorf("worktree %s 里还有未提交的用户改动，不拆（先提交或暂存）：%v", entry.WorktreeID, blockers.UserChanges)
		}
		if len(blockers.AgentLeftovers) > 0 {
			gitview.CleanupAgentLeftovers(entry.Path, blockers.AgentLeftovers)
		}
		if err := gitview.RemoveWorktree(ctx, entry.Path); err != nil {
			return false, fmt.Errorf("拆除 worktree 失败：%w", err)
		}
	}
	if _, err := gitview.RemoveWorktreeDir(entry.Path); err != nil {
		// 目录没删干净不判失败（合并/拆登记已成），原因由调用方决定是否上报。
		return false, nil
	}
	if deleteBranch && strings.TrimSpace(entry.Branch) != "" {
		// branch -d：未合并时 git 自己拒绝，宁可留分支也不丢提交。
		_ = gitview.DeleteBranch(ctx, mainDir, entry.Branch)
	}
	return true, nil
}

func store_worktreePathRefs(ctx context.Context, s *Service, task Task, entry WorktreeEntry) (int, error) {
	store, err := s.taskStore(task.RootID)
	if err != nil {
		return 0, err
	}
	return store.WorktreePathReferenceCount(ctx, entry.Path, task.ID, entry.WorktreeID)
}

func shortID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
