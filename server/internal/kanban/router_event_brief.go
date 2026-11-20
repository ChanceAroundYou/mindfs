package kanban

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// 运行期事件的证据 brief（Plan 2026-11-18，R1 T8）。
//
// coordinate 的事件回合不是「用户又说了什么」，而是「某个 Run 到了什么状态」。
// 所以事件 brief 必须描述**事件那个 Run** 的真实节点结果，而不是任务当前最新的
// Run；宁可显式写「未验证」，也不能让模型拿别的 Run 顶替，更不能把载荷/输出
// 无界放大进上下文。

const (
	routerEventBriefMaxNodes          = 20
	routerEventBriefOutputLimit       = 800 // 字节，UTF-8 截断后加 "…"
	routerEventBriefErrorLimit        = 500
	routerEventBriefPayloadBytes      = 2 * 1024
	routerEventBriefPayloadValueLimit = 500
	routerEventBriefUnverifiedLimit   = 10
)

// routerEventBriefUserMessageLimit 是运行期回合里原始留言正文的字节上限：比用户
// 回合的 500 字节宽，模型要能读懂原始要求。
const routerEventBriefUserMessageLimit = 2000

// capSituationBrief 在事件 brief 上的补充裁剪阈值：节点先收缩到 10 条，未验证项
// 最后收缩到 5 条。两者都比正常上限更紧，因为它们排在活动与正文之前被砍。
const (
	routerEventBriefKeptNodes    = 10
	routerEventBriefKeptUnverifd = 5
)

// routerEventBriefPayloadKeys 是事件载荷唯一允许进入 brief 的白名单。未知键一律
// 丢弃并标 truncated：账本载荷可能带内部标识，brief 不是它的转储口。
var routerEventBriefPayloadKeys = []string{
	"run_id", "task_id", "node_id", "node_run_id", "gate_node_id",
	"status", "reason", "error", "attempt", "since",
}

// routerEventBriefProducerKeys 是生产端自己写进事件载荷、但不进 brief 的键：
// `routerEventPayload` 的节点摘要（nodes / nodes_truncated / workflow_id /
// revision）、停摆事件的 started_at、启动对账的 open_node_runs / resumed /
// unknown。它们不是「未知键」——把自家摘要当成越界载荷，会让每条真实事件都报
// truncated，汇报里凭空多一句「载荷被截断、信息不全」。
var routerEventBriefProducerKeys = []string{
	"nodes", "nodes_truncated", "workflow_id", "revision",
	"started_at", "open_node_runs", "resumed", "unknown",
	// 主动监管的水位块由生产端写进载荷，事件回合原样挂回 brief.Supervision。
	"supervision",
}

// routerEventBriefControlledKey 判断该键是否由生产端受控写入（白名单或摘要）。
func routerEventBriefControlledKey(key string) bool {
	for _, allowed := range routerEventBriefPayloadKeys {
		if key == allowed {
			return true
		}
	}
	for _, producer := range routerEventBriefProducerKeys {
		if key == producer {
			return true
		}
	}
	return false
}

type RouterBriefEvent struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"`
	RunID      string         `json:"run_id"`
	TurnID     string         `json:"turn_id"`
	ChainDepth int            `json:"chain_depth"`
	Attempt    int            `json:"attempt,omitempty"`
	Payload    map[string]any `json:"payload,omitempty"`
	Truncated  bool           `json:"truncated,omitempty"`
}

type RouterBriefEvidence struct {
	TaskStatus   string                    `json:"task_status"`
	RunStatus    string                    `json:"run_status"`
	Nodes        []RouterBriefEvidenceNode `json:"nodes,omitempty"`
	OmittedNodes int                       `json:"omitted_nodes,omitempty"`
	Unverified   []string                  `json:"unverified,omitempty"`
	Truncated    bool                      `json:"truncated,omitempty"`
}

type RouterBriefEvidenceNode struct {
	NodeID         string `json:"node_id"`
	LoopPath       string `json:"loop_path,omitempty"`
	Iteration      int    `json:"iteration,omitempty"`
	Kind           string `json:"kind,omitempty"`
	Status         string `json:"status"`
	Attempt        int    `json:"attempt,omitempty"`
	OutputExcerpt  string `json:"output_excerpt,omitempty"`
	ErrorExcerpt   string `json:"error_excerpt,omitempty"`
	SessionPresent bool   `json:"session_present,omitempty"`
}

type RouterBriefBudget struct {
	MaxTurns       int `json:"max_turns"`
	TurnsUsed      int `json:"turns_used"` // 含本轮
	TurnsRemaining int `json:"turns_remaining"`
	ChainDepth     int `json:"chain_depth"`
	MaxChainDepth  int `json:"max_chain_depth"`
}

// attachRouterEventBrief 只在 turnBrief 拿到 trigger.Event 时调用：把事件本体、
// 事件 Run 的节点证据、未验证项与预算补进 brief，并清掉会串话的世界摘要。
func attachRouterEventBrief(ctx context.Context, store *TaskStore, brief *SituationBrief, event RouterEvent) error {
	if brief == nil {
		return errors.New("situation brief required")
	}
	if store == nil {
		return errors.New("task store unavailable")
	}
	payload, payloadTruncated := routerEventBriefPayload(event.PayloadJSON)
	brief.Event = &RouterBriefEvent{
		ID:         event.ID,
		Kind:       string(event.Kind),
		RunID:      event.RunID,
		TurnID:     event.TurnID,
		ChainDepth: event.ChainDepth,
		Attempt:    event.Attempt,
		Payload:    payload,
		Truncated:  payloadTruncated,
	}
	brief.Trigger.Ref = event.ID
	attachRouterEventUserMessage(ctx, store, brief, event.UserActivityID)
	// 运行期动作本来就不允许开新执行；给出别的 Run 或 workflow 列表只会让模型
	// 把汇报写错对象。
	brief.LatestRun = nil
	brief.ActiveExecution = nil
	brief.Workflows = nil

	taskStatus := brief.Task.Status
	if task, err := store.GetTask(ctx, brief.Task.ID); err == nil {
		taskStatus = task.Status
	}
	evidence := &RouterBriefEvidence{
		TaskStatus: taskStatus,
		RunStatus:  routerEventRunStatus(event.Kind),
	}
	brief.Evidence = evidence
	run, err := store.WorkflowRunByID(ctx, event.RunID)
	if err != nil || run == nil {
		evidence.RunStatus = "missing"
		evidence.Unverified = append(evidence.Unverified, fmt.Sprintf("run %s: not found in task store", event.RunID))
	} else {
		attachRouterEventNodes(ctx, store, evidence, run, event)
	}
	if brief.Event.Truncated {
		evidence.Unverified = append(evidence.Unverified,
			"event payload was truncated; only whitelisted fields are shown")
	}
	evidence.Unverified = capRouterEventUnverified(evidence.Unverified)

	used := 1
	if turns, countErr := countRouterEventDecisions(ctx, store, event.TaskID, event.UserActivityID); countErr != nil {
		return fmt.Errorf("count decisions: %w", countErr)
	} else {
		used += turns
	}
	remaining := RouterCoordinateMaxTurnsPerUserActivity - used
	if remaining < 0 {
		remaining = 0
	}
	brief.Budget = &RouterBriefBudget{
		MaxTurns:       RouterCoordinateMaxTurnsPerUserActivity,
		TurnsUsed:      used,
		TurnsRemaining: remaining,
		ChainDepth:     event.ChainDepth,
		MaxChainDepth:  routerEventMaxChainDepth,
	}
	return nil
}

// attachRouterEventUserMessage 用原始留言全文（上限 2000 字节）覆盖 build 阶段
// 写入的 500 字节摘要：事件回合要照着原始要求汇报。
func attachRouterEventUserMessage(ctx context.Context, store *TaskStore, brief *SituationBrief, userActivityID string) {
	userActivityID = strings.TrimSpace(userActivityID)
	if userActivityID == "" {
		return
	}
	act, err := NewActivityStore(store.db).Get(ctx, userActivityID)
	if err != nil || act == nil {
		return
	}
	brief.Trigger.UserMessage = &RouterBriefUserMessage{
		ID:             act.ID,
		Body:           truncateRouterText(act.Body, routerEventBriefUserMessageLimit),
		WorkflowChoice: act.WorkflowChoice,
		At:             act.CreatedAt.UTC().Format(rfc3339),
	}
}

func attachRouterEventNodes(ctx context.Context, store *TaskStore, evidence *RouterBriefEvidence, run *WorkflowRun, event RouterEvent) {
	runs, err := store.ListWorkflowNodeRuns(ctx, run.ID)
	if err != nil {
		evidence.Unverified = append(evidence.Unverified, fmt.Sprintf("run %s: node runs unreadable: %v", run.ID, err))
		return
	}
	kinds := workflowNodeKinds(run)
	nodes := make([]RouterBriefEvidenceNode, 0, len(runs))
	for _, nodeRun := range runs {
		nodes = append(nodes, RouterBriefEvidenceNode{
			NodeID:         nodeRun.NodeID,
			LoopPath:       nodeRun.LoopPath,
			Iteration:      nodeRun.Iteration,
			Kind:           kinds[nodeRun.NodeID],
			Status:         nodeRun.Status,
			Attempt:        nodeRun.Attempt,
			OutputExcerpt:  truncateRouterText(nodeRunOutputSummary(nodeRun), routerEventBriefOutputLimit),
			ErrorExcerpt:   truncateRouterText(nodeRunErrorText(nodeRun), routerEventBriefErrorLimit),
			SessionPresent: strings.TrimSpace(nodeRun.SessionKey) != "",
		})
	}
	evidence.Unverified = append(evidence.Unverified, routerEventUnverifiedNodes(evidence.TaskStatus, event, nodes)...)
	if len(nodes) > routerEventBriefMaxNodes {
		keep := routerEventKeepNodeIndexes(nodes)
		trimmed := make([]RouterBriefEvidenceNode, 0, len(keep))
		for _, index := range keep {
			trimmed = append(trimmed, nodes[index])
		}
		evidence.OmittedNodes = len(nodes) - len(trimmed)
		evidence.Truncated = true
		nodes = trimmed
	}
	evidence.Nodes = nodes
}

// routerEventKeepNodeIndexes 选出保留的节点下标：按状态优先级取前 20 条（失败与
// 等待用户优先），同状态保持存储顺序，最后按下标还原存储顺序输出。
func routerEventKeepNodeIndexes(nodes []RouterBriefEvidenceNode) []int {
	indexes := make([]int, len(nodes))
	for i := range indexes {
		indexes[i] = i
	}
	sort.SliceStable(indexes, func(a, b int) bool {
		left, right := routerEventNodePriority(nodes[indexes[a]].Status), routerEventNodePriority(nodes[indexes[b]].Status)
		if left != right {
			return left < right
		}
		return indexes[a] < indexes[b]
	})
	indexes = indexes[:routerEventBriefMaxNodes]
	sort.Ints(indexes)
	return indexes
}

func routerEventNodePriority(status string) int {
	switch status {
	case NodeRunFail:
		return 0
	case NodeRunWaitingUser:
		return 1
	case NodeRunRunning:
		return 2
	case NodeRunQueued:
		return 3
	case NodeRunPending:
		return 4
	case NodeRunCancelled:
		return 5
	case NodeRunSuccess:
		return 6
	default:
		return 7
	}
}

// routerEventUnverifiedNodes 把「证据不足」写成模型能直读的事实。这些文案是
// 给模型看的证据，不是面向用户的成品文案。
func routerEventUnverifiedNodes(taskStatus string, event RouterEvent, nodes []RouterBriefEvidenceNode) []string {
	var out []string
	taskSettled := isTerminalStatus(taskStatus)
	hasWaitingUser, hasSuccess, runningNodeID := false, false, ""
	for _, node := range nodes {
		if taskSettled && !routerEventNodeSettled(node.Status) {
			out = append(out, fmt.Sprintf("node %s: %s while task is %s", node.NodeID, node.Status, taskStatus))
		}
		if node.Status == NodeRunSuccess {
			hasSuccess = true
			if node.OutputExcerpt == "" {
				out = append(out, fmt.Sprintf("node %s: success without recorded output", node.NodeID))
			}
		}
		if node.Status == NodeRunFail && node.ErrorExcerpt == "" {
			out = append(out, fmt.Sprintf("node %s: failed without recorded error", node.NodeID))
		}
		if node.Status == NodeRunWaitingUser {
			hasWaitingUser = true
		}
		if node.Status == NodeRunRunning && runningNodeID == "" {
			runningNodeID = node.NodeID
		}
	}
	if event.Kind == RouterEventRunStalled {
		nodeID := routerEventPayloadString(event.PayloadJSON, "node_id")
		if nodeID == "" {
			nodeID = runningNodeID
		}
		if nodeID != "" {
			out = append(out, fmt.Sprintf(
				"stalled event: node %s was still running at event time; not a verified failure", nodeID))
		}
	}
	if event.Kind == RouterEventGateWaiting && !hasWaitingUser {
		out = append(out, "gate_waiting event has no waiting_user node")
	}
	if event.Kind == RouterEventRunSettled && !hasSuccess {
		out = append(out, "completion event has no successful node")
	}
	return out
}

func routerEventNodeSettled(status string) bool {
	switch status {
	case NodeRunSuccess, NodeRunFail, NodeRunCancelled, NodeRunSkipped:
		return true
	default:
		return false
	}
}

// capRouterEventUnverified 把未验证项收敛到上限，并保留「还有 N 条」的可见性。
func capRouterEventUnverified(items []string) []string {
	if len(items) <= routerEventBriefUnverifiedLimit {
		return items
	}
	omitted := len(items) - routerEventBriefUnverifiedLimit
	out := make([]string, 0, routerEventBriefUnverifiedLimit+1)
	out = append(out, items[:routerEventBriefUnverifiedLimit]...)
	return append(out, fmt.Sprintf("… and %d more unverified items", omitted))
}

func countRouterEventDecisions(ctx context.Context, store *TaskStore, taskID, userActivityID string) (int, error) {
	taskID, userActivityID = strings.TrimSpace(taskID), strings.TrimSpace(userActivityID)
	if taskID == "" || userActivityID == "" {
		return 0, nil
	}
	var count int
	err := store.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM router_decisions WHERE task_id = ? AND user_activity_id = ?`,
		taskID, userActivityID).Scan(&count)
	return count, err
}

func routerEventRunStatus(kind RouterEventKind) string {
	switch kind {
	case RouterEventRunSettled:
		return "settled"
	case RouterEventRunFailed:
		return "failed"
	case RouterEventGateWaiting:
		return "gate_waiting"
	case RouterEventRunStalled:
		return "stalled"
	case RouterEventStartupReconcile:
		return "reconcile"
	default:
		return "unknown"
	}
}

// nodeRunErrorText 依次取节点 output 里的 error / reason 文本。
func nodeRunErrorText(nodeRun NodeRun) string {
	for _, key := range []string{"error", "reason"} {
		value, _ := nodeRun.Output[key].(string)
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func routerEventPayloadString(payloadJSON, key string) string {
	var payload map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(payloadJSON)), &payload); err != nil {
		return ""
	}
	value, _ := payload[key].(string)
	return strings.TrimSpace(value)
}

// routerEventBriefPayload 只保留白名单键：字符串值截到 500 字节，累计超过 2KB
// 就停止追加，未知键与任何截断都置 truncated。载荷无法解析时按截断处理。
func routerEventBriefPayload(payloadJSON string) (map[string]any, bool) {
	payloadJSON = strings.TrimSpace(payloadJSON)
	if payloadJSON == "" {
		return nil, false
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(payloadJSON), &decoded); err != nil {
		return nil, true
	}
	truncated := false
	for key := range decoded {
		if !routerEventBriefControlledKey(key) {
			truncated = true
		}
	}
	payload := make(map[string]any, len(decoded))
	for _, key := range routerEventBriefPayloadKeys {
		value, ok := decoded[key]
		if !ok {
			continue
		}
		if text, isText := value.(string); isText {
			if len(text) > routerEventBriefPayloadValueLimit {
				truncated = true
			}
			value = truncateRouterText(text, routerEventBriefPayloadValueLimit)
		}
		candidate := make(map[string]any, len(payload)+1)
		for existingKey, existingValue := range payload {
			candidate[existingKey] = existingValue
		}
		candidate[key] = value
		raw, err := json.Marshal(candidate)
		if err != nil || len(raw) > routerEventBriefPayloadBytes {
			truncated = true
			break
		}
		payload = candidate
	}
	if len(payload) == 0 {
		payload = nil
	}
	return payload, truncated
}

// routerEventEffectID 是运行期汇报活动的确定标识：同一决策同一动作序号永远得到
// 同一活动 ID。空决策 ID 或负数序号返回空串，调用方保持随机 ID（不静默复用）。
func routerEventEffectID(decisionID string, seq int) string {
	decisionID = strings.TrimSpace(decisionID)
	if decisionID == "" || seq < 0 {
		return ""
	}
	sum := sha256.Sum256([]byte(decisionID + ":" + strconv.Itoa(seq)))
	return "act-rtev-" + hex.EncodeToString(sum[:])[:20]
}
