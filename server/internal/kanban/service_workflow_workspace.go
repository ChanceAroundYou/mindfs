package kanban

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrWorkflowIntegrationBranchMissing：Run 没有快照集成分支时不允许合并。
// 与建树时的 D11 检查共用同一语义：运行期绝不隐含回落 "main"。
var ErrWorkflowIntegrationBranchMissing = errors.New("workflow run has no integration branch snapshot")

// WorkflowNodeWorkspaceLease 是一次 Agent NodeRun 的物理 workspace 租约：
// 目录、独立 worktree 归属，以及必须在本轮结束后释放的 writer gate。
//
// 共享主树的节点 Entry 为零值（它不是任何 NodeRun 拥有的树）；独立树的节点
// Entry 来自 registry，并已持久化到 NodeRun 的 worktree 列（D16）。
type WorkflowNodeWorkspaceLease struct {
	RootPath string
	Entry    WorktreeEntry
	// NodeRun 带上了本次解析出的 worktree 身份，调用方后续落库/收尾都用它。
	NodeRun NodeRun

	release func()
}

// Release 释放 writer gate。可重复调用；只读节点拿到的是 no-op。
func (l *WorkflowNodeWorkspaceLease) Release() {
	if l == nil || l.release == nil {
		return
	}
	l.release()
	l.release = nil
}

// PrepareWorkflowNodeWorktree 在外部回合开始前确定该 NodeRun 的物理工作区：
//
//   - 默认共享 Task 主树/项目根（D10），写节点取得该物理树的 writer gate；
//   - `worktree=independent` 才建/认领独立树，并把 owner 持久化到 NodeRun；
//   - 只读节点（writes_workspace=false）不占 gate。
//
// 失败时返回错误且不调用 Agent：调用方必须把 NodeRun 明确收成 fail/waiting_user，
// 不能在没有可得 workspace 的情况下继续。未知副作用一律保留已有 registry 行。
func (s *Service) PrepareWorkflowNodeWorktree(ctx context.Context, store *TaskStore, run *WorkflowRun, node WorkflowNode, nodeRun NodeRun) (*WorkflowNodeWorkspaceLease, error) {
	if s == nil || store == nil || run == nil {
		return nil, errors.New("workflow service, store and run required")
	}
	if node.Kind != NodeKindAgent {
		return nil, fmt.Errorf("node %q is %s, not agent", node.ID, node.Kind)
	}
	if nodeRun.RunID != run.ID || nodeRun.NodeID != node.ID {
		return nil, fmt.Errorf("node run %s does not belong to run/node %s/%s", nodeRun.ID, run.ID, node.ID)
	}
	task, err := store.GetTask(ctx, run.TaskID)
	if err != nil {
		return nil, err
	}
	intent := WorkflowNodeWorkspaceOf(node)
	lease := &WorkflowNodeWorkspaceLease{NodeRun: nodeRun, release: func() {}}

	if intent.Independent {
		if strings.TrimSpace(run.IntegrationBranch) == "" {
			// D11：没有 Run 快照就不允许建/跑独立树 —— 运行时隐式回落 "main"
			// 会把工作合并到用户没选的集成分支。
			return nil, fmt.Errorf("%w: independent worktree node %q cannot start", ErrWorkflowIntegrationBranchMissing, node.ID)
		}
		entry, err := s.EnsureNodeWorktree(ctx, EnsureNodeWorktreeInput{
			RootID:            task.RootID,
			TaskID:            task.ID,
			NodeRunID:         nodeRun.ID,
			BranchMode:        "new",
			IntegrationBranch: run.IntegrationBranch,
		})
		if err != nil {
			return nil, err
		}
		if err := store.BindWorkflowNodeWorktree(ctx, nodeRun, entry); err != nil {
			// 树已建立、绑定失败：不删除目录（可能已被外部引用），留给恢复路径按
			// registry owner 重新绑定；调用方会把 NodeRun 显式标失败。
			return nil, err
		}
		lease.RootPath = entry.Path
		lease.Entry = entry
		lease.NodeRun.WorktreeID = entry.WorktreeID
		lease.NodeRun.WorktreePath = entry.Path
		lease.NodeRun.WorktreeBranch = entry.Branch
	} else {
		lease.RootPath = strings.TrimSpace(task.WorktreePath)
		if lease.RootPath != "" && !worktreeDirUsable(lease.RootPath) {
			// 与 legacy 路径同一口径：目录被删后不猜、不在主 checkout 里跑，
			// 明确让用户重建。直接把失效路径当 cwd 会得到看不懂的 chdir 错误。
			return nil, fmt.Errorf("worktree 目录已不存在（%s），点「重建 worktree」恢复后再执行", lease.RootPath)
		}
		if lease.RootPath == "" && s.Roots != nil {
			root, err := s.Roots.GetRoot(task.RootID)
			if err != nil {
				return nil, err
			}
			lease.RootPath = strings.TrimSpace(root.RootPath)
		}
	}

	branch := strings.TrimSpace(lease.Entry.Branch)
	if branch == "" {
		branch = strings.TrimSpace(task.WorktreeBranch)
	}
	if branch == "" {
		branch = strings.TrimSpace(run.IntegrationBranch)
	}
	release, err := s.AcquireWorkflowNodeWriter(ctx, node, lease.RootPath, branch)
	if err != nil {
		return nil, err
	}
	lease.release = release
	return lease, nil
}

// WorkflowNodeWorktreeResult 是一个成功 NodeRun 的合并/关闭事实。它只描述
// 独立 worktree 节点；共享主树节点返回零值。
type WorkflowNodeWorktreeResult struct {
	Merged        bool
	Commit        string
	ConflictFiles []string
	// CloseError 记录「合并已持久化成功、关闭失败」：合并事实不因关闭失败回滚，
	// 重试路径只补关闭。空 = 没有关闭错误。
	CloseError string
}

// MergeWorkflowNodeWorktree 在节点报告 done 后把独立 worktree 合并回 Run 快照的
// 集成分支，并在成功后关闭。冲突/脏主树返回错误且保留两边现场；合并成功但关闭
// 失败只记 CloseError，不返回错误（否则会把已完成的合并在调用方误标为未合并）。
func (s *Service) MergeWorkflowNodeWorktree(ctx context.Context, store *TaskStore, run *WorkflowRun, nodeRun NodeRun) (WorkflowNodeWorktreeResult, error) {
	plan, hasMerge, err := s.prepareWorkflowNodeMerge(ctx, store, run, nodeRun)
	if err != nil {
		return WorkflowNodeWorktreeResult{}, err
	}
	if !hasMerge {
		return WorkflowNodeWorktreeResult{}, nil
	}
	if plan.needsGate() {
		releaseGate, err := s.AcquireNodeWriter(ctx, plan.mainDir, plan.target)
		if err != nil {
			return WorkflowNodeWorktreeResult{}, err
		}
		defer releaseGate()
	}
	return s.executeWorkflowNodeMerge(ctx, plan)
}

// prepareWorkflowNodeMerge 解析独立树节点的合并计划。共享主树节点
// （WorktreeID 为空）返回 hasMerge=false，不碰 git、不拿 gate。
func (s *Service) prepareWorkflowNodeMerge(ctx context.Context, store *TaskStore, run *WorkflowRun, nodeRun NodeRun) (autoMergePlan, bool, error) {
	if strings.TrimSpace(nodeRun.WorktreeID) == "" {
		return autoMergePlan{}, false, nil
	}
	if strings.TrimSpace(run.IntegrationBranch) == "" {
		return autoMergePlan{}, true, ErrWorkflowIntegrationBranchMissing
	}
	task, err := store.GetTask(ctx, run.TaskID)
	if err != nil {
		return autoMergePlan{}, true, err
	}
	plan, err := s.prepareAutoMerge(ctx, AutoMergeWorktreeInput{
		RootID: task.RootID, TaskID: task.ID, NodeRunID: nodeRun.ID,
		IntegrationBranch: run.IntegrationBranch,
		CloseAfterMerge:   true,
		DeleteBranch:      true,
	})
	return plan, true, err
}

// acquireWorkflowMergeGate 为已解析的合并计划取得主 checkout 的写 gate。
// 失败时也返回非 nil 的 no-op release：AcquireWorkspace 在每条错误路径
// （包括 ctx 取消）上返回 nil release，调用方若把返回值直接赋给 no-op
// release 再在错误分支调用，会 panic 掉整个进程。
func (s *Service) acquireWorkflowMergeGate(ctx context.Context, plan autoMergePlan, hasMerge bool) (func(), error) {
	if !hasMerge || !plan.needsGate() {
		return func() {}, nil
	}
	release, err := s.AcquireNodeWriter(ctx, plan.mainDir, plan.target)
	if err != nil {
		return func() {}, err
	}
	return release, nil
}

// executeWorkflowNodeMerge 执行已解析的计划；gate 必须已按 plan.needsGate()
// 取得。返回值把 registry 事实翻译成 NodeRun output 所需的形状。
func (s *Service) executeWorkflowNodeMerge(ctx context.Context, plan autoMergePlan) (WorkflowNodeWorktreeResult, error) {
	res, err := s.executeAutoMerge(ctx, plan)
	out := WorkflowNodeWorktreeResult{
		Merged:        res.Entry.State == WorktreeStateMerged || res.Entry.State == WorktreeStateClosed,
		Commit:        res.Entry.MergedCommit,
		ConflictFiles: res.ConflictFiles,
	}
	if err != nil {
		if out.Merged {
			out.CloseError = err.Error()
			return out, nil
		}
		return out, err
	}
	if out.Commit == "" {
		out.Commit = res.Commit
	}
	return out, nil
}
