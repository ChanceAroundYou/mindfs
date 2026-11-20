package kanban

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// completion_blocked 事件的组装（Plan 2026-11-20 Task 6）。
//
// 一次「证据不成立」只该让 Router 补做一次：唯一键带缺口哈希，缺口没变就
// 复用自己的那条事件；缺口变了（例如补做成功后只剩最后一项）才是新事实。

// RouterEventCompletionInstanceKey 是 completion_blocked 的实例键。直连任务没有
// Run，键里退到 taskID，保证同一任务的同一缺口仍然只落一条。
func RouterEventCompletionInstanceKey(runID, taskID, reportHash string) string {
	target := strings.TrimSpace(runID)
	if target == "" {
		target = strings.TrimSpace(taskID)
	}
	return "completion_blocked:" + target + ":" + strings.TrimSpace(reportHash)
}

// completionGapHash 只对未通过的项取哈希：顺序按契约，细节文本不进哈希 ——
// 同一组缺口在不同回合的 Detail 可能带上不同的现场值，那不该算新事件。
func completionGapHash(report CompletionReport) string {
	parts := make([]string, 0, len(report.Items))
	for _, item := range report.Items {
		if item.Pass {
			continue
		}
		parts = append(parts, item.ID)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:8])
}

// completionBlockedPayload 只放定位与计数：契约项 id/kind/是否通过、补做用量。
// payload 不进转录、不写会话键。
func completionBlockedPayload(taskID, runID string, report CompletionReport, repairsUsed, repairsMax int) map[string]any {
	items := make([]map[string]any, 0, len(report.Items))
	for _, item := range report.Items {
		items = append(items, map[string]any{
			"id":   truncateRouterText(item.ID, 80),
			"kind": item.Kind,
			"pass": item.Pass,
		})
	}
	payload := map[string]any{
		"task_id":      taskID,
		"items":        items,
		"repairs_used": repairsUsed,
		"repairs_max":  repairsMax,
	}
	if strings.TrimSpace(runID) != "" {
		payload["run_id"] = runID
	}
	return payload
}

// emitCompletionBlockedEvent 落一条去重的事件。重复调用同缺口返回已有行，
// 不报错也不产生第二条。
func (s *Service) emitCompletionBlockedEvent(ctx context.Context, store *TaskStore, task Task, run *WorkflowRun, report CompletionReport, decision RouterDecision, repairsUsed, repairsMax int) error {
	if store == nil {
		return nil
	}
	runID := ""
	if run != nil {
		runID = run.ID
	}
	instanceKey := RouterEventCompletionInstanceKey(runID, task.ID, completionGapHash(report))
	raw, err := json.Marshal(completionBlockedPayload(task.ID, runID, report, repairsUsed, repairsMax))
	if err != nil {
		return err
	}
	userActivityID := strings.TrimSpace(decision.UserActivityID)
	_, _, err = NewRouterStore(store.db).InsertEvent(ctx, RouterEvent{
		RootID:           task.RootID,
		TaskID:           task.ID,
		RunID:            runID,
		Kind:             RouterEventCompletionBlocked,
		InstanceKey:      instanceKey,
		TurnID:           routerEventTurnID(runID, RouterEventCompletionBlocked, instanceKey),
		UserActivityID:   userActivityID,
		ChainDepth:       s.routerEventChainDepth(ctx, store, userActivityID),
		SourceDecisionID: strings.TrimSpace(decision.ID),
		PayloadJSON:      string(raw),
	})
	return err
}

// supervisionEvidenceFailedNodes 把「还值得补做」的缺口翻成节点状态。
//
// 只有最新 attempt 停在 fail/unknown/waiting_user 的节点才进这个列表：重试
// 路径不接受 success / cancelled，把它们列出来只会让模型调一个必然失败的
// 工具。其余缺口（文件不存在、gate 未批、节点根本没跑）交给 notify_user。
func supervisionEvidenceFailedNodes(ctx context.Context, store *TaskStore, run *WorkflowRun, report CompletionReport) []SupervisionNodeState {
	if store == nil || run == nil {
		return nil
	}
	nodeRuns, err := store.ListWorkflowNodeRuns(ctx, run.ID)
	if err != nil {
		return nil
	}
	latest := map[string]NodeRun{}
	for _, nr := range nodeRuns {
		if prev, ok := latest[nr.NodeID]; !ok || nr.Attempt > prev.Attempt {
			latest[nr.NodeID] = nr
		}
	}
	seen := map[string]bool{}
	var out []SupervisionNodeState
	for _, item := range report.Items {
		if item.Pass || strings.TrimSpace(item.NodeID) == "" || seen[item.NodeID] {
			continue
		}
		nr, ok := latest[item.NodeID]
		if !ok {
			continue
		}
		switch nr.Status {
		case NodeRunFail, NodeRunUnknown, NodeRunWaitingUser:
		default:
			continue
		}
		seen[item.NodeID] = true
		node, _ := workflowNodeByID(run.Graph, nr.NodeID)
		state := SupervisionNodeState{
			NodeID:      nr.NodeID,
			NodeRunID:   nr.ID,
			NodeKind:    node.Kind,
			Status:      nr.Status,
			Attempt:     nr.Attempt,
			FailureKind: SupervisionFailureEvidenceFailed,
			Summary:     truncateRouterText(item.Detail, 240),
		}
		if used := supervisionRetriesUsed(nodeRuns, nr.NodeID); used > 0 {
			state.RetriesUsed = used
		}
		out = append(out, state)
		if len(out) >= supervisionNodeLimit {
			break
		}
	}
	return out
}
