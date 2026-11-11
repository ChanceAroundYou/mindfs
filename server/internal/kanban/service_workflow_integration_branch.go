package kanban

import (
	"context"
	"fmt"
	"strings"

	"mindfs/server/internal/gitview"
)

// D11：集成分支在 Run 创建时解析并快照。运行时（Task 3 的 merge）只读
// WorkflowRun.IntegrationBranch，绝不能再回落到字符串 "main"。
//
// 只有图中存在「独立 worktree」节点时才强制要求解析成功：共享主树的节点不
// 需要 merge target，在非 git 项目里迁移也不该因此被拒。需要时解析失败必须
// 在 Run 落库之前返回错误 —— 先建 Run 再发现没有 target，只会留下一个跑不动
// 的活运行。
func (s *Service) snapshotRunIntegrationBranch(ctx context.Context, task Task, graph WorkflowDefinition) (string, error) {
	rootID := task.RootID
	required := workflowGraphNeedsIntegrationBranch(graph)
	// 共享节点默认写入任务自己的 worktree；独立节点的合并目标必须是同一条线。
	// 只看主 checkout 当前分支会让「共享节点写 task-42、独立节点并进 main」。
	if branch := strings.TrimSpace(task.WorktreeBranch); branch != "" && worktreeDirUsable(task.WorktreePath) {
		return branch, nil
	}
	mainDir, err := s.taskWorktreeMainDir(ctx, rootID)
	if err != nil {
		if required {
			return "", fmt.Errorf("resolve integration branch: %w", err)
		}
		return "", nil
	}
	branches, err := gitview.ListBranches(ctx, mainDir)
	if err != nil {
		if required {
			return "", fmt.Errorf("resolve integration branch in %s: %w", mainDir, err)
		}
		return "", nil
	}
	current := strings.TrimSpace(branches.Current)
	if current == "" && required {
		return "", fmt.Errorf("project %s has no current branch; independent worktree nodes need an explicit integration target", rootID)
	}
	return current, nil
}

// workflowGraphNeedsIntegrationBranch reports whether any agent node (including
// agents nested in loop graphs) asks for an independent worktree.
func workflowGraphNeedsIntegrationBranch(graph WorkflowDefinition) bool {
	for _, node := range graph.Nodes {
		if node.Kind == NodeKindAgent && WorkflowNodeWorkspaceOf(node).Independent {
			return true
		}
		if node.Kind == NodeKindLoop {
			if inner, err := workflowLoopGraph(node); err == nil && workflowGraphNeedsIntegrationBranch(inner) {
				return true
			}
		}
	}
	return false
}
