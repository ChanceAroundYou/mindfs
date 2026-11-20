package kanban

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// completion_blocked 事件的组装（Plan 2026-11-20 Task 6）。
//
// 一轮补做只该有一条事件：同一轮里缺口没变就复用（决策重放 / 换 ID 重试都不
// 再落第二条）；缺口变了、或者进入下一轮补做，才是新事实 —— 否则预算的第二轮
// 永远没有事件，补做会卡死在第一次。

// RouterEventCompletionInstanceKey 是 completion_blocked 的实例键。直连任务没有
// Run，键里退到 taskID。round 是补做轮次（1 起）：同一轮内按缺口去重，跨轮必
// 须各有一条事件，否则 Router 不会再被叫起来。
func RouterEventCompletionInstanceKey(runID, taskID, reportHash string, round int) string {
	target := strings.TrimSpace(runID)
	if target == "" {
		target = strings.TrimSpace(taskID)
	}
	if round < 1 {
		round = 1
	}
	return fmt.Sprintf("completion_blocked:%s:%s:r%d", target, strings.TrimSpace(reportHash), round)
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
	instanceKey := RouterEventCompletionInstanceKey(runID, task.ID, completionGapHash(report), repairsUsed+1)
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
