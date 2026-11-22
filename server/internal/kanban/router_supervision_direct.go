package kanban

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// 直连回合的监管水位（S3-3）。
//
// Run 的监管水位按节点算（每节点已重试几次、最近一小时用了几个动作）；直连回合
// 只有「一轮对话」这一层，所以水位按卡住的那条回复行算：同一轮最多自动重开
// 一次（B8-1：预算维持现值，2 次/节点不适用于直连），第二次必须交给用户。

// SupervisionDirectState 是 supervision 块里的直连回合段。与 Run 段同样的约束：
// 只含计数与水位，不带会话键/转录（用 replying 表达「会话还在动」）。
type SupervisionDirectState struct {
	ActivityID   string `json:"activity_id"`
	Status       string `json:"status"`
	StartedAt    string `json:"started_at,omitempty"`
	IdleSeconds  int    `json:"idle_seconds,omitempty"`
	Replying     bool   `json:"replying"`
	RetriesUsed  int    `json:"retries_used"`
	RetryAllowed bool   `json:"retry_allowed"`
	// Interrupted 表示这一轮是被进程重启带走的（W3-3）：旧回复已按设计结算成
	// cancelled，不会再有人推进它，所以重开不需要「仍停在 pending/running」这个
	// 前提，也不受「会话还在回复」阻挡。
	Interrupted bool `json:"interrupted,omitempty"`
	// StalledByWatermark 表示停滞证据来自输出水位（这一轮最后一次真的吐内容的
	// 时刻），而不是会话探针。探针仍报 replying 时正是靠它把「活着」与「有进展」
	// 分开。
	StalledByWatermark bool `json:"stalled_by_watermark,omitempty"`
	// MaxRetries 是这一档允许的自动重开次数（停滞 1 次，重启恢复 3 次）。零值按
	// supervisionDirectMaxRetries 处理。
	MaxRetries int `json:"max_retries,omitempty"`
}

// supervisionDirectMaxRetries 是同一轮直连允许的自动重开次数。
const supervisionDirectMaxRetries = 1

// supervisionDirectObservation 汇总一条卡住的直连回复的水位。mode=off 返回 nil。
func (s *Service) supervisionDirectObservation(ctx context.Context, store *TaskStore, task Task, reply *TaskActivity, now time.Time) *SupervisionObservation {
	if s == nil || store == nil || reply == nil {
		return nil
	}
	cfg := s.RouterConfig().Supervision.Normalized()
	if cfg.Mode == RouterSupervisionOff {
		return nil
	}
	used := s.supervisionDirectRetriesUsed(ctx, store, task.ID, reply.ID)
	retriesLeft := supervisionDirectMaxRetries - used
	if retriesLeft < 0 {
		retriesLeft = 0
	}
	state := &SupervisionDirectState{
		ActivityID:   reply.ID,
		Status:       reply.Status,
		Replying:     s.sessionRunning(task.MainSessionKey),
		RetriesUsed:  used,
		RetryAllowed: retriesLeft > 0,
	}
	obs := &SupervisionObservation{
		Mode:              cfg.Mode,
		TaskID:            task.ID,
		TaskStatus:        task.Status,
		MaxRetriesPerNode: supervisionDirectMaxRetries,
		RetriesLeft:       retriesLeft,
		ActionBudget:      cfg.MaxActionsPerHour,
		RunActionBudget:   cfg.MaxActionsPerRun,
		Direct:            state,
	}
	if !reply.CreatedAt.IsZero() {
		obs.LastProgressAt = reply.CreatedAt.UTC().Format(routerTimeLayout)
		state.StartedAt = obs.LastProgressAt
		if idle := int(now.Sub(reply.CreatedAt).Seconds()); idle > 0 {
			state.IdleSeconds = idle
		}
	}
	// 直连回合没有 Run，只受滚动一小时额度约束。
	_, obs.ActionsLastHour = s.supervisionActionUsage(ctx, store, task.ID, "", now)
	return obs
}

// supervisionDirectRetriesUsed 数「这一轮直连已经被自动重开过几次」：只认
// trigger=direct_stalled 且动作指向同一条回复的决策（applied / applying）。
func (s *Service) supervisionDirectRetriesUsed(ctx context.Context, store *TaskStore, taskID, activityID string) int {
	taskID, activityID = strings.TrimSpace(taskID), strings.TrimSpace(activityID)
	if store == nil || taskID == "" || activityID == "" {
		return 0
	}
	rows, err := store.db.QueryContext(ctx,
		`SELECT actions_json FROM router_decisions
		 WHERE task_id = ? AND trigger_kind = ? AND status IN (?, ?)`,
		taskID, string(RouterTriggerDirectStalled),
		string(RouterDecisionApplied), string(RouterDecisionApplying))
	if err != nil {
		return 0
	}
	defer rows.Close()
	used := 0
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return used
		}
		var actions []RouterAction
		if err := json.Unmarshal([]byte(raw), &actions); err != nil {
			continue
		}
		for _, action := range actions {
			if action.Kind == RouterActionRetryDirect && strings.TrimSpace(action.NodeRunID) == activityID {
				used++
			}
		}
	}
	return used
}
