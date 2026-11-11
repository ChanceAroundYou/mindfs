package kanban

import (
	"context"
	"strings"
)

// D10/D15：Agent 节点的 workspace 声明与物理工作树写互斥。
//
// 默认（node.Params 未声明）：节点会写 workspace，使用 Task 的主工作树，
// 并必须取得该物理树的 writer gate。只有显式 `writes_workspace=false` 的节点
// 才被视为只读，不占 gate、可以与其他节点并发。`worktree=independent` 是 D10
// 批准的显式 opt-in；独立工作树的物理路径不同，所以天然可与别的树并发。

// WorkflowNodeWorkspace 是一次节点执行的 workspace 意图。
type WorkflowNodeWorkspace struct {
	// WritesWorkspace 默认 true。解析失败（类型不对）也保持 true：拿不准时
	// 按写节点处理，宁可多串行一次，也不能把写者当只读放行。
	WritesWorkspace bool
	// Independent 表示节点显式声明独立 worktree（D10 opt-in）。
	Independent bool
}

// WorkflowNodeWorkspaceOf 读取节点参数。未知的 worktree 值一律回落共享主树，
// 因为共享 + gate 是更安全的一侧；拼错不能悄悄变成独立树。
func WorkflowNodeWorkspaceOf(node WorkflowNode) WorkflowNodeWorkspace {
	ws := WorkflowNodeWorkspace{WritesWorkspace: true}
	if v, ok := node.Params["writes_workspace"].(bool); ok {
		ws.WritesWorkspace = v
	}
	if v, ok := node.Params["worktree"].(string); ok && strings.TrimSpace(v) == "independent" {
		ws.Independent = true
	}
	return ws
}

// AcquireNodeWriter 取得物理工作树的写入权。path 是实际目录，branch 是该树
// 当前分支。Service 没有共享 gate 时 fail closed。
func (s *Service) AcquireNodeWriter(ctx context.Context, workspacePath, branch string) (func(), error) {
	if s == nil {
		return nil, ErrWorkflowWriterGateUnavailable
	}
	s.mu.Lock()
	gate := s.WriterGate
	s.mu.Unlock()
	if gate == nil {
		return nil, ErrWorkflowWriterGateUnavailable
	}
	if strings.TrimSpace(workspacePath) == "" && strings.TrimSpace(branch) == "" {
		return nil, ErrWorkflowWorkspaceIdentityMissing
	}
	return gate.AcquireWorkspace(ctx, workspacePath, branch)
}

// AcquireWorkflowNodeWriter 按节点声明的 workspace intent 取 gate。只读节点
// 直接返回 no-op release；写节点走 app 级共享 gate。
func (s *Service) AcquireWorkflowNodeWriter(ctx context.Context, node WorkflowNode, workspacePath, branch string) (func(), error) {
	if !WorkflowNodeWorkspaceOf(node).WritesWorkspace {
		return func() {}, nil
	}
	return s.AcquireNodeWriter(ctx, workspacePath, branch)
}
