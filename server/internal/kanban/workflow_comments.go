package kanban

import (
	"context"
	"fmt"
	"strings"
)

// WorkflowCommentSender delivers a queued user comment as a normal continuation
// turn in the exact session that was bound when the comment arrived. It must not
// append the workflow JSON-output contract: a user message is not a node result.
type WorkflowCommentSender interface {
	SendWorkflowComment(ctx context.Context, exec WorkflowNodeExecution, message string) error
}

func (s *Service) workflowCommentSender() WorkflowCommentSender {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.WorkflowCommentSenderPort
}

// reconcileWorkflowCommentsAfterNodeRun implements D3's convergence rule.
//
//   - Several active Agent NodeRuns: comments stay queued until exactly one
//     remains, then bind to it. They are never fanned out.
//   - One active node finishing successfully: comments bound to it are sent in
//     FIFO order to its session before the driver evaluates outgoing edges.
//   - No active node left: queued comments that were waiting for convergence are
//     blocked, never forwarded to a node that starts later.
//   - pending_phase comments (arrived with zero active nodes) are left alone;
//     starting that new phase is a separate package.
func (s *Service) reconcileWorkflowCommentsAfterNodeRun(ctx context.Context, store *TaskStore, run *WorkflowRun, nodeRun NodeRun, status string) error {
	if store == nil || run == nil {
		return nil
	}
	rows, err := store.ListOpenWorkflowComments(ctx, run.TaskID)
	if err != nil || len(rows) == 0 {
		return err
	}
	task, err := store.GetTask(ctx, run.TaskID)
	if err != nil {
		return err
	}
	active, err := store.ActiveWorkflowAgentNodeRuns(ctx, run.ID)
	if err != nil {
		return err
	}
	if isTerminalStatus(task.Status) {
		return blockCommentRows(ctx, store, run.TaskID, rows)
	}

	finishingIsAgent := false
	if node, ok := workflowNodeByID(run.Graph, nodeRun.NodeID); ok && node.Kind == NodeKindAgent {
		finishingIsAgent = true
	}

	// Bind rows that were waiting for a unique active branch.
	switch {
	case len(active) == 1 && strings.TrimSpace(active[0].SessionKey) != "":
		for _, r := range rows {
			if r.NodeRunID == "" && r.Status == ContinueRequestQueued {
				if err := store.SetWorkflowCommentStatus(ctx, run.TaskID, r.CommentID, ContinueRequestQueued, active[0].ID, active[0].SessionKey); err != nil {
					return err
				}
			}
		}
	case len(active) == 0 && status == NodeRunSuccess && finishingIsAgent && strings.TrimSpace(nodeRun.SessionKey) != "":
		// All branches converged on the node that just finished. This also covers
		// the small race where the comment arrived after Begin but before the
		// session binding was persisted.
		for _, r := range rows {
			if r.NodeRunID == "" && r.Status == ContinueRequestQueued {
				if err := store.SetWorkflowCommentStatus(ctx, run.TaskID, r.CommentID, ContinueRequestQueued, nodeRun.ID, nodeRun.SessionKey); err != nil {
					return err
				}
			}
		}
	}

	// Re-read so bindings just written are visible and FIFO order is preserved.
	rows, err = store.ListOpenWorkflowComments(ctx, run.TaskID)
	if err != nil {
		return err
	}
	var toDeliver []workflowCommentRow
	for _, r := range rows {
		if r.NodeRunID == "" {
			continue // pending_phase or still waiting for a unique branch
		}
		if r.NodeRunID == nodeRun.ID {
			if status == NodeRunSuccess && strings.TrimSpace(nodeRun.SessionKey) != "" {
				toDeliver = append(toDeliver, r)
			} else if err := store.SetWorkflowCommentStatus(ctx, run.TaskID, r.CommentID, ContinueRequestBlocked, r.NodeRunID, r.SessionKey); err != nil {
				return err
			}
			continue
		}
		if _, stillActive := containsNodeRun(active, r.NodeRunID); !stillActive {
			// The bound node ended without this function seeing a deliverable
			// state (cancel/fail). Never move the comment to a later node.
			if err := store.SetWorkflowCommentStatus(ctx, run.TaskID, r.CommentID, ContinueRequestBlocked, r.NodeRunID, r.SessionKey); err != nil {
				return err
			}
		}
	}
	if len(toDeliver) == 0 {
		return nil
	}

	sender := s.workflowCommentSender()
	if sender == nil {
		// No delivery capability: fail closed, keep every message visible, and
		// do not let later comments jump the queue.
		return blockCommentRows(ctx, store, run.TaskID, toDeliver)
	}
	node, _ := workflowNodeByID(run.Graph, nodeRun.NodeID)
	deliveryPath, deliveryBranch, wsErr := s.workflowCommentDeliveryWorkspace(ctx, task, nodeRun, run)
	if wsErr != nil {
		return deliveryFailure(ctx, store, run, nodeRun, toDeliver, 0, wsErr)
	}
	for i, r := range toDeliver {
		if strings.TrimSpace(r.Body) == "" {
			// 空留言本身无法投递，但不能让它把队列后面所有消息一起饿死。
			// 只阻塞这一条，后续留言按 FIFO 继续。
			if err := store.SetWorkflowCommentStatus(ctx, run.TaskID, r.CommentID, ContinueRequestBlocked, r.NodeRunID, r.SessionKey); err != nil {
				return err
			}
			continue
		}
		// 续聊回合是对物理工作区的写：必须与节点执行、自动合并共用 writer gate。
		// 独立树在 merge 后已被拆掉时，投递会回落到主 checkout（见 workspace 解析）。
		release, gateErr := s.AcquireNodeWriter(ctx, deliveryPath, deliveryBranch)
		if gateErr != nil {
			return deliveryFailure(ctx, store, run, nodeRun, toDeliver, i, gateErr)
		}
		exec := WorkflowNodeExecution{
			RootID:          task.RootID,
			RuntimeRootPath: deliveryPath,
			Task:            task,
			Run:             run,
			Node:            node,
			NodeRun:         nodeRun,
			Prompt:          r.Body,
		}
		sendErr := sender.SendWorkflowComment(ctx, exec, r.Body)
		release()
		if sendErr != nil {
			return deliveryFailure(ctx, store, run, nodeRun, toDeliver, i, sendErr)
		}
		if err := store.SetWorkflowCommentStatus(ctx, run.TaskID, r.CommentID, ContinueRequestConsumed, r.NodeRunID, r.SessionKey); err != nil {
			return err
		}
	}
	return nil
}

// workflowCommentDeliveryWorkspace 选一个真实可用的 cwd 来续聊。
//
// 优先节点自己的独立树（失败/阻塞保留现场时还在），其次任务主树，最后项目根。
// 独立节点成功后 merge 会拆掉自己的树，此时必须回落到主 checkout —— 继续把
// 已删除的路径传给 agent 只会得到 chdir 错，或者更糟：在别处静默执行。
func (s *Service) workflowCommentDeliveryWorkspace(ctx context.Context, task Task, nodeRun NodeRun, run *WorkflowRun) (string, string, error) {
	branch := func(values ...string) string {
		for _, v := range values {
			if strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
		return ""
	}
	if p := strings.TrimSpace(nodeRun.WorktreePath); p != "" && worktreeDirUsable(p) {
		return p, branch(nodeRun.WorktreeBranch, task.WorktreeBranch, run.IntegrationBranch), nil
	}
	if p := strings.TrimSpace(task.WorktreePath); p != "" && worktreeDirUsable(p) {
		return p, branch(task.WorktreeBranch, run.IntegrationBranch), nil
	}
	if s.Roots != nil {
		root, err := s.Roots.GetRoot(task.RootID)
		if err != nil {
			return "", "", err
		}
		p := strings.TrimSpace(root.RootPath)
		if p == "" || !worktreeDirUsable(p) {
			return "", "", fmt.Errorf("项目根目录不可用（%q），无法投递留言", p)
		}
		return p, branch(task.WorktreeBranch, run.IntegrationBranch), nil
	}
	return "", "", fmt.Errorf("找不到可用的工作目录投递留言（node run %s）", nodeRun.ID)
}

// deliveryFailure 把当前及后续留言明确标成投递失败/阻塞，绝不跳过后面的消息。
func deliveryFailure(ctx context.Context, store *TaskStore, run *WorkflowRun, nodeRun NodeRun, toDeliver []workflowCommentRow, at int, cause error) error {
	if at >= 0 && at < len(toDeliver) {
		r := toDeliver[at]
		_ = store.SetWorkflowCommentStatus(ctx, run.TaskID, r.CommentID, ContinueRequestDeliveryFailed, r.NodeRunID, r.SessionKey)
		_, _ = store.AppendTaskActivity(ctx, TaskActivity{
			ID:        "wf:" + nodeRun.ID + ":comment:" + r.CommentID + ":delivery_failed",
			TaskID:    run.TaskID,
			Type:      ActivityTypeAttention,
			Author:    "system",
			Role:      "system",
			Body:      "留言投递失败：" + cause.Error(),
			RunID:     run.ID,
			NodeRunID: nodeRun.ID,
		})
	}
	start := at + 1
	if start < 0 {
		start = 0
	}
	for _, rest := range toDeliver[start:] {
		if err := store.SetWorkflowCommentStatus(ctx, run.TaskID, rest.CommentID, ContinueRequestBlocked, rest.NodeRunID, rest.SessionKey); err != nil {
			return err
		}
	}
	return nil
}

func blockCommentRows(ctx context.Context, store *TaskStore, taskID string, rows []workflowCommentRow) error {
	for _, r := range rows {
		if err := store.SetWorkflowCommentStatus(ctx, taskID, r.CommentID, ContinueRequestBlocked, r.NodeRunID, r.SessionKey); err != nil {
			return err
		}
	}
	return nil
}
