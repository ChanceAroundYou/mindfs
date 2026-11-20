package kanban

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// Router 主动监管（Plan 2026-11-20 B8/B9）。S0 只做观测：把「派出去的 worker 现在
// 怎么样」整理成结构化事实挂到 run_failed / run_stalled 事件上，不调模型、不改
// 任何权威状态。S1（shadow）与 S2（有限执行）在同一份观测上叠加动作。

// RouterSupervisionMode 是监管开关。off 不采集；shadow 采集并让 Router 给方案但
// 不执行；on 允许有限执行（S2）。
type RouterSupervisionMode string

const (
	RouterSupervisionOff    RouterSupervisionMode = "off"
	RouterSupervisionShadow RouterSupervisionMode = "shadow"
	RouterSupervisionOn     RouterSupervisionMode = "on"
)

const (
	// defaultSupervisionMaxRetriesPerNode 是每节点最多几次监管重试（用户裁定 B8-1）。
	defaultSupervisionMaxRetriesPerNode = 2
	// defaultSupervisionMaxActionsPerHour 是每 Run 每小时最多几个监管动作。
	defaultSupervisionMaxActionsPerHour = 6
	// supervisionNodeLimit 限制观测里每类节点的条数，保证 payload 有界（事件总上限 8 KiB）。
	supervisionNodeLimit = 8
	// supervisionSummaryLimit 限制失败摘要长度，避免把整段错误正文塞进事件。
	supervisionSummaryLimit = 240
	// defaultSupervisionQuotaHold 是拿不到上游重置时刻时的挂起时长。
	defaultSupervisionQuotaHold = 15 * time.Minute
	// supervisionQuotaHoldMin/Max 夹住挂起时长：太短等于原地重试，太长则整轮停摆。
	supervisionQuotaHoldMin = time.Minute
	supervisionQuotaHoldMax = 6 * time.Hour
)

// RouterSupervisionConfig 挂在 RouterConfig 上，来自 config.json 的
// router.supervision 段（S0 只做观测与预算事实）。
type RouterSupervisionConfig struct {
	Mode              RouterSupervisionMode
	MaxRetriesPerNode int
	MaxActionsPerHour int
	// QuotaHold 是撞到用量上限时挂起多久（拿不到上游重置时刻时用它）。
	QuotaHold time.Duration
}

// Normalized 收敛未知模式与非法预算；零值保持「不采集」。
func (c RouterSupervisionConfig) Normalized() RouterSupervisionConfig {
	switch RouterSupervisionMode(strings.ToLower(strings.TrimSpace(string(c.Mode)))) {
	case RouterSupervisionShadow:
		c.Mode = RouterSupervisionShadow
	case RouterSupervisionOn:
		c.Mode = RouterSupervisionOn
	default:
		c.Mode = RouterSupervisionOff
	}
	if c.MaxRetriesPerNode <= 0 {
		c.MaxRetriesPerNode = defaultSupervisionMaxRetriesPerNode
	}
	if c.MaxActionsPerHour <= 0 {
		c.MaxActionsPerHour = defaultSupervisionMaxActionsPerHour
	}
	if c.QuotaHold <= 0 {
		c.QuotaHold = defaultSupervisionQuotaHold
	}
	if c.QuotaHold < supervisionQuotaHoldMin {
		c.QuotaHold = supervisionQuotaHoldMin
	}
	if c.QuotaHold > supervisionQuotaHoldMax {
		c.QuotaHold = supervisionQuotaHoldMax
	}
	return c
}

// SupervisionFailureKind 决定 S2 能对失败节点做什么：只有 retryable 允许自动重试；
// uncertain（例如 worktree 合并状态不明）一律只通知，绝不自动重试。
type SupervisionFailureKind string

const (
	SupervisionFailureRetryable    SupervisionFailureKind = "retryable"
	SupervisionFailureNonRetryable SupervisionFailureKind = "non_retryable"
	SupervisionFailureUncertain    SupervisionFailureKind = "uncertain"
	// SupervisionFailureQuota 是撞到上游用量/速率上限：不是节点的错，也不该在几秒内
	// 连续重试，改为挂到重置时刻再跑。
	SupervisionFailureQuota SupervisionFailureKind = "quota"
	// SupervisionFailureEvidenceFailed 只出现在 completion_blocked 事件回合：
	// 节点本身跑完了，但完成契约在它身上不成立，补做上限由契约的 MaxRepairs
	// 独立把关（不受每节点重试预算约束）。
	SupervisionFailureEvidenceFailed SupervisionFailureKind = "evidence_failed"
)

// SupervisionNodeState 是单个节点在观测时刻的水位。
type SupervisionNodeState struct {
	NodeID      string                 `json:"node_id"`
	NodeRunID   string                 `json:"node_run_id"`
	NodeKind    string                 `json:"node_kind,omitempty"`
	Status      string                 `json:"status"`
	Attempt     int                    `json:"attempt"`
	IdleSeconds int                    `json:"idle_seconds,omitempty"`
	RetriesUsed int                    `json:"retries_used,omitempty"`
	StartedAt   string                 `json:"started_at,omitempty"`
	Replying    bool                   `json:"replying,omitempty"`
	FailureKind SupervisionFailureKind `json:"failure_kind,omitempty"`
	Summary     string                 `json:"summary,omitempty"`
}

// SupervisionObservation 是事件 payload 里的 supervision 块。它只含计数与节点水位，
// 刻意不带会话键/转录：事件 payload 的既有约束是「不泄漏 session」。
type SupervisionObservation struct {
	Mode              RouterSupervisionMode  `json:"mode"`
	RunID             string                 `json:"run_id"`
	TaskID            string                 `json:"task_id"`
	TaskStatus        string                 `json:"task_status,omitempty"`
	OpenNodes         []SupervisionNodeState `json:"open_nodes,omitempty"`
	FailedNodes       []SupervisionNodeState `json:"failed_nodes,omitempty"`
	LastProgressAt    string                 `json:"last_progress_at,omitempty"`
	MaxRetriesPerNode int                    `json:"max_retries_per_node"`
	RetriesLeft       int                    `json:"retries_left"`
	ActionsLastHour   int                    `json:"actions_last_hour"`
	ActionBudget      int                    `json:"action_budget"`
	// Direct 只在直连回合的监管块里出现（那条事件没有 Run，节点段不适用）。
	Direct *SupervisionDirectState `json:"direct,omitempty"`
	// EvidenceFailedNodes 只出现在 completion_blocked 事件回合：这些节点的缺口
	// 还值得补做，且补做对象是节点级重试。它不并进 FailedNodes，免得普通监管
	// 回合把「跑了但证据不成立」误当成「跑失败了」。
	EvidenceFailedNodes []SupervisionNodeState `json:"evidence_failed_nodes,omitempty"`
}

// supervisionObservation 汇总一个 Run 的 open/failed 节点水位。mode=off 时返回 nil
// （调用方据此完全不改 payload）。它只读库，不调模型、不写任何状态。
func (s *Service) supervisionObservation(ctx context.Context, store *TaskStore, task Task, run *WorkflowRun, now time.Time) *SupervisionObservation {
	if s == nil || store == nil || run == nil {
		return nil
	}
	// 服务未显式注入配置时 routerConfig 是零值，这里就地归一化，避免把「未配置」
	// 读成一个空模式（空模式也要按 off 处理）。
	cfg := s.RouterConfig().Supervision.Normalized()
	if cfg.Mode == RouterSupervisionOff {
		return nil
	}
	nodeRuns, err := store.ListWorkflowNodeRuns(ctx, run.ID)
	if err != nil {
		return nil
	}
	obs := &SupervisionObservation{
		Mode:              cfg.Mode,
		RunID:             run.ID,
		TaskID:            run.TaskID,
		TaskStatus:        task.Status,
		MaxRetriesPerNode: cfg.MaxRetriesPerNode,
		ActionBudget:      cfg.MaxActionsPerHour,
	}
	// 同一节点可能有多次 attempt：水位只认最新一条，重试次数 = 最新 attempt - 1。
	latest := map[string]NodeRun{}
	for _, nr := range nodeRuns {
		prev, ok := latest[nr.NodeID]
		if !ok || nr.Attempt > prev.Attempt {
			latest[nr.NodeID] = nr
		}
	}
	var lastProgress time.Time
	noteProgress := func(at time.Time) {
		if at.After(lastProgress) {
			lastProgress = at
		}
	}
	for _, nr := range nodeRuns {
		if !nr.FinishedAt.IsZero() {
			noteProgress(nr.FinishedAt)
		} else if !nr.StartedAt.IsZero() {
			noteProgress(nr.StartedAt)
		}
	}
	usedMax := 0
	for _, nr := range latest {
		node, _ := workflowNodeByID(run.Graph, nr.NodeID)
		state := SupervisionNodeState{
			NodeID:    nr.NodeID,
			NodeRunID: nr.ID,
			NodeKind:  node.Kind,
			Status:    nr.Status,
			Attempt:   nr.Attempt,
		}
		if used := supervisionRetriesUsed(nodeRuns, nr.NodeID); used > 0 {
			state.RetriesUsed = used
		}
		switch nr.Status {
		case NodeRunQueued, NodeRunRunning, NodeRunWaitingUser:
			if !nr.StartedAt.IsZero() {
				state.StartedAt = nr.StartedAt.UTC().Format(routerTimeLayout)
				if idle := int(now.Sub(nr.StartedAt).Seconds()); idle > 0 {
					state.IdleSeconds = idle
				}
			}
			if nr.Status == NodeRunRunning {
				state.Replying = s.sessionRunning(nr.SessionKey)
			}
			if len(obs.OpenNodes) < supervisionNodeLimit {
				obs.OpenNodes = append(obs.OpenNodes, state)
			}
		case NodeRunFail, NodeRunUnknown, NodeRunCancelled:
			state.FailureKind, state.Summary = classifySupervisionFailure(nr)
			if state.RetriesUsed > usedMax {
				usedMax = state.RetriesUsed
			}
			if len(obs.FailedNodes) < supervisionNodeLimit {
				obs.FailedNodes = append(obs.FailedNodes, state)
			}
		}
	}
	if !lastProgress.IsZero() {
		obs.LastProgressAt = lastProgress.UTC().Format(routerTimeLayout)
	}
	obs.RetriesLeft = cfg.MaxRetriesPerNode - usedMax
	if obs.RetriesLeft < 0 {
		obs.RetriesLeft = 0
	}
	obs.ActionsLastHour = s.supervisionActionsLastHour(ctx, store, run.TaskID, now)
	return obs
}

// supervisionActionsLastHour 数本任务最近一小时里由运行期事件触发的决策行，
// 即监管动作的真实用量（失败/停摆各算一个动作回合）。读不到就记 0，不影响事件本身。
func (s *Service) supervisionActionsLastHour(ctx context.Context, store *TaskStore, taskID string, now time.Time) int {
	if store == nil || strings.TrimSpace(taskID) == "" {
		return 0
	}
	cutoff := now.UTC().Add(-time.Hour).Format(routerTimeLayout)
	var n int
	err := store.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM router_decisions WHERE task_id=? AND created_at>=? AND trigger_kind IN (?,?,?,?)`,
		taskID, cutoff, string(RouterTriggerRunFailed), string(RouterTriggerRunStalled),
		string(RouterTriggerDirectStalled), string(RouterTriggerCompletionBlocked)).Scan(&n)
	if err != nil {
		return 0
	}
	return n
}

// classifySupervisionFailure 给失败节点分级并附一段可读摘要。顺序：结构化证据
// （worktree_error、unknown 状态）优先，其次按错误文本，最后保守落到不可重试。
func classifySupervisionFailure(nr NodeRun) (SupervisionFailureKind, string) {
	summary := supervisionFailureSummary(nr)
	if worktreeErr, _ := nr.Output["worktree_error"].(string); strings.TrimSpace(worktreeErr) != "" {
		return SupervisionFailureUncertain, "workspace merge state is unknown: " + summary
	}
	if nr.Status == NodeRunUnknown {
		return SupervisionFailureUncertain, summary
	}
	if nr.Status == NodeRunCancelled {
		return SupervisionFailureNonRetryable, summary
	}
	text := strings.ToLower(summary)
	for _, mark := range []string{"invalid", "unauthorized", "forbidden", "permission denied",
		"validation", "merge conflict", "not found"} {
		if strings.Contains(text, mark) {
			return SupervisionFailureNonRetryable, summary
		}
	}
	// 用量/速率上限先判：它们以前落在 retryable 里，结果是几百毫秒内再次 429、两次
	// 重试预算烧光。现在改判 quota，由挂起路径排到重置时刻。
	for _, mark := range []string{"rate limit", "rate_limit", "ratelimit", "usage limit",
		"quota", "too many requests", "overloaded", "429"} {
		if strings.Contains(text, mark) {
			return SupervisionFailureQuota, summary
		}
	}
	for _, mark := range []string{"timeout", "timed out", "deadline exceeded", "stream ended",
		"unexpected eof", "connection reset", "broken pipe", "temporarily unavailable",
		"502", "503", "504"} {
		if strings.Contains(text, mark) {
			return SupervisionFailureRetryable, summary
		}
	}
	if strings.Contains(text, "401") || strings.Contains(text, "403") || strings.Contains(text, "400") {
		return SupervisionFailureNonRetryable, summary
	}
	return SupervisionFailureNonRetryable, summary
}

// supervisionFailureSummary 从节点输出里取一段短摘要；优先 error，其次 message。
func supervisionFailureSummary(nr NodeRun) string {
	for _, key := range []string{"error", "message", "reason", "worktree_error"} {
		value, _ := nr.Output[key].(string)
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if len(value) > supervisionSummaryLimit {
			value = value[:supervisionSummaryLimit] + "…"
		}
		return value
	}
	return "node ended " + nr.Status + " without a recorded reason"
}

// supervisionDecisionStats 统计监管用量：最近一小时的监管动作回合数，与最近
// 24 小时的监管决策数。只读，读不到就记 0（诊断快照不能因为统计失败而报错）。
func supervisionDecisionStats(ctx context.Context, db *sql.DB, now time.Time) (lastHour, recent int) {
	if db == nil {
		return 0, 0
	}
	now = now.UTC()
	hourCutoff := now.Add(-time.Hour).Format(routerTimeLayout)
	dayCutoff := now.Add(-24 * time.Hour).Format(routerTimeLayout)
	var hour, day sql.NullInt64
	err := db.QueryRowContext(ctx, `
SELECT SUM(CASE WHEN created_at >= ? THEN 1 ELSE 0 END), COUNT(*)
FROM router_decisions
WHERE trigger_kind IN (?, ?, ?, ?) AND created_at >= ?`,
		hourCutoff,
		string(RouterTriggerRunFailed), string(RouterTriggerRunStalled), string(RouterTriggerDirectStalled),
		string(RouterTriggerCompletionBlocked),
		dayCutoff).Scan(&hour, &day)
	if err != nil {
		return 0, 0
	}
	return int(hour.Int64), int(day.Int64)
}
