package kanban

// Situation Brief 的各个视图：活动、任务状态、Run 与节点卡片、活动执行、可用
// workflow，以及最后的截断工具。类型与入口在 router_brief.go。
// 从该文件拆出（硬线 500，2026-11-22）。

import (
	"context"
	"strings"
)

func briefActivities(items []*TaskActivity, triggerUserActivityID string) []RouterBriefActivity {
	if len(items) == 0 {
		return nil
	}
	start := len(items) - routerBriefMaxActivities
	if start < 0 {
		start = 0
	}
	out := make([]RouterBriefActivity, 0, len(items)-start)
	for _, item := range items[start:] {
		if item == nil {
			continue
		}
		out = append(out, RouterBriefActivity{
			ID:          item.ID,
			Type:        item.Type,
			Role:        item.Role,
			Purpose:     item.Purpose,
			Status:      item.Status,
			BodyExcerpt: truncateRouterText(item.Body, routerBriefActivityExcerpt),
			At:          item.CreatedAt.UTC().Format(rfc3339),
		})
	}
	return out
}

// briefTaskStatus 是 brief 里「任务现在到底在干什么」的唯一来源。
//
// 任务行的 status 由另一条投影写入，可能落后于 Run 的节点事实（实测：任务行
// waiting_user、节点却在 running）。brief 是 Router 的唯一世界快照，陈旧状态会
// 直接变成错误决策，所以这里以 Run 投影为准；只有操作员拥有的状态（暂停）与
// 任务已经终态这两种「投影看不见 / 不该覆盖」的情况才采信任务行。
func briefTaskStatus(run *WorkflowRun, task Task) string {
	if task.Status == StatusPaused || isTerminalStatus(task.Status) {
		return task.Status
	}
	if run == nil {
		return task.Status
	}
	derived := taskStatusFromRun(run)
	// 投影是节点事实，但两份证据都可能落后：任务行会停在上一轮的 waiting_user，
	// 而 NodeStates 在节点刚 claim 的那一瞬间也可能还没写进去（NodeRun 行已经是
	// running）。两边都说「有活动」时以投影为准；一边说在跑、另一边说空闲时采信
	// 更活跃的那个——宁可多报一次活动，也不要把一条本该转交的留言判成新开一轮。
	//
	// 但终态的 Run 只有在任务行也是终态时才算任务结论。任务行不是终态，说明这
	// 条 Run 只是上一轮的证据（被取代、或用户点了「回到待审核」）：采信任务行，
	// 否则 apply 闸门会拿旧结论把新留言判成「任务已结束」。
	if isTerminalStatus(derived) {
		return task.Status
	}
	if isActiveTaskStatus(derived) {
		return derived
	}
	if isActiveTaskStatus(task.Status) {
		return task.Status
	}
	if derived != "" {
		return derived
	}
	return task.Status
}

// briefRun 汇总 Run 的节点状态。nodeRuns 是调用方一次取出的行（可传 nil，只
// 看投影）。
//
// 投影只在节点终态时写，所以「空投影 + unknown 行」（#86）必须由行补上：否则
// 摘要永远是 0 个节点，Router 看不到这一轮到底做过什么。
func briefRun(ctx context.Context, store *TaskStore, run *WorkflowRun, task Task, nodeRuns []NodeRun) *RouterBriefRun {
	if run == nil {
		return nil
	}
	summary := map[string]int{"total": 0, "success": 0, "running": 0, "pending": 0, "failed": 0, "waiting_user": 0, "cancelled": 0, "unknown": 0}
	waiting := make([]string, 0)
	for _, status := range run.NodeStates {
		summary["total"]++
		if bucket := briefRunBucket(status); bucket != "" {
			summary[bucket]++
		}
	}
	// NodeStates 不保留节点名，也不保留还没写进投影的节点；用行补齐。
	if nodeRuns == nil && store != nil {
		nodeRuns, _ = store.ListWorkflowNodeRuns(ctx, run.ID)
	}
	for _, nodeRun := range nodeRuns {
		if nodeRun.Status == NodeRunWaitingUser {
			waiting = append(waiting, nodeRun.NodeID)
		}
	}
	for nodeID, status := range latestNodeRunStatus(nodeRuns) {
		if _, ok := run.NodeStates[nodeID]; ok {
			continue
		}
		if bucket := briefRunBucket(status); bucket != "" {
			summary["total"]++
			summary[bucket]++
		}
	}
	cards, truncated := briefNodeCards(run, nodeRuns)
	return &RouterBriefRun{
		ID:                 run.ID,
		Status:             briefTaskStatus(run, task),
		Version:            run.Version,
		NodeSummary:        summary,
		WaitingUserNodes:   waiting,
		Nodes:              cards,
		NodeCardsTruncated: truncated,
	}
}

// graphNodeModel 读节点当前的模型档（params.model）。空 = 跟随 harness 默认，
// 卡片留空而不是编一个名字：Router 只能报告它真的知道的东西。
func graphNodeModel(node WorkflowNode) string {
	if node.Params == nil {
		return ""
	}
	model, _ := node.Params["model"].(string)
	return strings.TrimSpace(model)
}

// briefNodeCards 按图顺序给每个顶层节点出一张卡。状态以投影为准、行做补充
// （投影可能落后一行）；动作表由状态唯一决定 —— 运行中只能取消，改档与跳过
// 只在节点不在跑时出现（用户裁定「运行中不切换」）。
func briefNodeCards(run *WorkflowRun, nodeRuns []NodeRun) ([]RouterBriefNode, int) {
	if run == nil {
		return nil, 0
	}
	type latest struct {
		status  string
		attempt int
		summary string
	}
	rows := make(map[string]latest, len(nodeRuns))
	for _, nodeRun := range nodeRuns {
		// Loop 内部行按 (loop_path, iteration) 落自己的事实，不属于顶层节点卡片
		// （与 repairCancelledNodeProjections、CommitWorkflowNodeTransition 同一规则）。
		if nodeRun.LoopPath != "" {
			continue
		}
		prev, ok := rows[nodeRun.NodeID]
		if ok && prev.attempt >= nodeRun.Attempt {
			continue
		}
		summary := nodeRunText(nodeRun, "summary", "reason", "error")
		rows[nodeRun.NodeID] = latest{status: nodeRun.Status, attempt: nodeRun.Attempt, summary: summary}
	}
	cards := make([]RouterBriefNode, 0, len(run.Graph.Nodes))
	truncated := 0
	for _, node := range run.Graph.Nodes {
		row := rows[node.ID]
		status := run.NodeStates[node.ID]
		if status == "" {
			status = row.status
		}
		if status == "" {
			status = NodeRunPending
		}
		if len(cards) >= briefNodeCardLimit {
			truncated++
			continue
		}
		cards = append(cards, RouterBriefNode{
			NodeID:  node.ID,
			Kind:    node.Kind,
			Name:    node.Name,
			Status:  status,
			Attempt: row.attempt,
			Model:   graphNodeModel(node),
			Summary: truncateRouterText(row.summary, routerBriefNodeExcerpt),
			Actions: briefNodeActions(node, status),
		})
	}
	if len(cards) == 0 {
		return nil, truncated
	}
	return cards, truncated
}

// briefNodeActions 是「这个状态下 Router 能对这个节点做什么」的唯一口径。
// 卡片、校验层与提示词都从这里取，避免三处各写一套。
func briefNodeActions(node WorkflowNode, status string) []string {
	switch status {
	case NodeRunQueued, NodeRunRunning:
		return []string{"cancel_node"}
	case NodeRunWaitingUser:
		// 审批卡只能由人批准/驳回；agent 节点停在这里时 Router 可以代用户回话。
		if node.Kind == NodeKindHumanGate {
			return nil
		}
		return []string{"answer_waiting_node", "cancel_node"}
	case NodeRunFail, NodeRunUnknown:
		return []string{"retry_node", "set_node_model"}
	case NodeRunSuccess, NodeRunSkipped, NodeRunCancelled:
		// 已收尾的节点不再接受动作（重跑成功节点要走新一轮，不是节点级动作）。
		return nil
	}
	// 还没跑过（投影与行都没有）：可以跳过、可以改档。
	if node.Kind != NodeKindAgent {
		return nil
	}
	return []string{"skip_node", "set_node_model"}
}

// briefRunBucket 把节点状态映射到 node_summary 的桶；未知状态返回空串，
// 宁可少报一个桶也不要把状态名拼进 map 里造出没人认识的新桶。
func briefRunBucket(status string) string {
	switch status {
	case NodeRunSuccess:
		return "success"
	case NodeRunRunning:
		return "running"
	case NodeRunPending, NodeRunQueued:
		return "pending"
	case NodeRunFail:
		return "failed"
	case NodeRunWaitingUser:
		return "waiting_user"
	case NodeRunCancelled:
		return "cancelled"
	case NodeRunUnknown:
		return "unknown"
	default:
		return ""
	}
}

// latestNodeRunStatus 取每个节点最新一次尝试的状态。行只是投影的补充证据：
// 投影里有的节点以投影为准，这里只用来补投影还没写到的节点。
func latestNodeRunStatus(nodeRuns []NodeRun) map[string]string {
	out := make(map[string]string, len(nodeRuns))
	attempts := make(map[string]int, len(nodeRuns))
	for _, nodeRun := range nodeRuns {
		if nodeRun.LoopPath != "" {
			continue
		}
		if prev, ok := attempts[nodeRun.NodeID]; ok && prev > nodeRun.Attempt {
			continue
		}
		attempts[nodeRun.NodeID] = nodeRun.Attempt
		out[nodeRun.NodeID] = nodeRun.Status
	}
	return out
}

func briefActiveExecution(ctx context.Context, store *TaskStore, run *WorkflowRun, task Task) *RouterBriefExecution {
	execution := &RouterBriefExecution{
		Kind:      "workflow",
		RunID:     run.ID,
		Status:    briefTaskStatus(run, task),
		StartedAt: run.CreatedAt.UTC().Format(rfc3339),
	}
	if store == nil {
		return execution
	}
	runs, err := store.ListWorkflowNodeRuns(ctx, run.ID)
	if err != nil {
		return execution
	}
	kinds := workflowNodeKinds(run)
	for _, nodeRun := range runs {
		if kinds[nodeRun.NodeID] != NodeKindAgent || nodeRun.Status != NodeRunRunning {
			continue
		}
		// A running Agent without a session still counts towards store uniqueness.
		// Even another, bound Agent cannot accept implicit delivery in that case.
		if strings.TrimSpace(nodeRun.SessionKey) == "" {
			execution.CurrentNodes = nil
			return execution
		}
		execution.CurrentNodes = append(execution.CurrentNodes, RouterBriefNode{
			NodeID:         nodeRun.NodeID,
			Name:           nodeRun.NodeID,
			Status:         nodeRun.Status,
			SessionPresent: true,
		})
	}
	return execution
}

func briefWorkflows(ctx context.Context, workflows *WorkflowStore) []RouterBriefWorkflow {
	if workflows == nil {
		return nil
	}
	available, err := workflows.ListAvailable(ctx)
	if err != nil {
		return nil
	}
	versions, _ := workflows.ListVersions(ctx)
	latest := make(map[string]string, len(versions))
	for _, version := range versions {
		if current := latest[version.WorkflowID]; current == "" || workflowVersionSeq(version.Version) > workflowVersionSeq(current) {
			latest[version.WorkflowID] = version.Version
		}
	}
	out := make([]RouterBriefWorkflow, 0, len(available))
	for _, meta := range available {
		if meta == nil {
			continue
		}
		out = append(out, RouterBriefWorkflow{
			ID:            meta.ID,
			Name:          truncateRouterText(meta.Name, 200),
			Description:   truncateRouterText(meta.Description, 500),
			UseCases:      truncateRouterList(meta.UseCases, 8, 100),
			Tags:          truncateRouterList(meta.Tags, 8, 100),
			LatestVersion: latest[meta.ID],
		})
		if len(out) >= routerBriefMaxWorkflows {
			break
		}
	}
	return out
}

func isActiveTaskStatus(status string) bool {
	switch status {
	case StatusRunning, StatusWaitingUser, StatusPaused:
		return true
	default:
		return false
	}
}

func truncateRouterText(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	return strings.ToValidUTF8(text[:limit], "") + "…"
}

func truncateRouterList(items []string, maxItems, maxLen int) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = truncateRouterText(item, maxLen)
		if item == "" {
			continue
		}
		out = append(out, item)
		if len(out) >= maxItems {
			break
		}
	}
	return out
}

const rfc3339 = "2006-01-02T15:04:05Z07:00"
