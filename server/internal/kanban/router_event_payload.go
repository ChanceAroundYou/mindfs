package kanban

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// 事件 payload 的组装与回合身份派生。拆出 router_event_sources.go 是为了守住
// 500 行硬线：生产端（扫描 + 钩子）与「payload 长什么样」是两块独立的关注点。

// routerEventPayload 构造有界摘要：只含 id、节点状态、版本与 revision；禁止
// NodeRun 输出、会话内容与 git 字段进入 payload。
func (s *Service) routerEventPayload(ctx context.Context, run *WorkflowRun) map[string]any {
	nodes := map[string]string{}
	ids := make([]string, 0, len(run.NodeStates))
	for id := range run.NodeStates {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	truncated := false
	for _, id := range ids {
		switch status := run.NodeStates[id]; status {
		case NodeRunSuccess, NodeRunFail, NodeRunCancelled, NodeRunSkipped, NodeRunRunning:
			if len(nodes) >= routerEventNodesLimit {
				truncated = true
				continue
			}
			nodes[id] = status
		}
	}
	workflowID := strings.TrimSpace(run.Version)
	if s != nil && s.Workflows != nil && workflowID != "" {
		if version, err := s.Workflows.GetVersion(ctx, workflowID); err == nil && strings.TrimSpace(version.WorkflowID) != "" {
			workflowID = strings.TrimSpace(version.WorkflowID)
		}
	}
	payload := map[string]any{
		"run_id":      run.ID,
		"task_id":     run.TaskID,
		"nodes":       nodes,
		"workflow_id": workflowID,
		"revision":    run.Revision,
	}
	if truncated {
		payload["nodes_truncated"] = true
	}
	return payload
}

// routerEventTurnID 由 (runID, kind, instanceKey) 稳定派生：重扫与钩子不会因为
// newID 把同一实例分裂成两个回合。
func routerEventTurnID(runID string, kind RouterEventKind, instanceKey string) string {
	sum := sha256.Sum256([]byte(runID + "\x00" + string(kind) + "\x00" + instanceKey))
	return "revt-turn:" + hex.EncodeToString(sum[:8])
}

// routerEventChainDepth 沿 ReplyToID 回溯原始留言链，深度有界。入口留言 = 0。
func (s *Service) routerEventChainDepth(ctx context.Context, store *TaskStore, userActivityID string) int {
	id := strings.TrimSpace(userActivityID)
	if store == nil || id == "" {
		return 0
	}
	activities := NewActivityStore(store.db)
	depth := 0
	for hop := 0; hop < routerEventChainWalkLimit; hop++ {
		activity, err := activities.Get(ctx, id)
		if err != nil || strings.TrimSpace(activity.ReplyToID) == "" {
			break
		}
		depth++
		id = activity.ReplyToID
	}
	return depth
}
