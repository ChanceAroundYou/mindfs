package kanban

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"
)

// 重启恢复与停滞的监管观测（Plan 2026-11-22 W3-3）。
//
// W3-2 把三种「没人推进」的现场写成了事件，但事件本身不会做任何事：运行期回合
// 的白名单要求事件的监管块存在，没有块就只能 say/notify。这一步给它们补上监管
// 观测，让 Router 真的能修：
//   - turn_interrupted：进程死在回合中间，旧回复已被启动对账结算成 cancelled。
//     自动重开至多 3 次（用户 2026-11-22 裁定），每次都用原始用户留言重跑。
//   - turn_stalled：回合在飞但输出水位 10 分钟没动。允许重开一次，且不受
//     「会话探针仍报 replying」阻挡 —— 探针证明进程活着，水位证明没有进展。
//   - task_orphaned：任务写着 running 而底下什么都没有。没有可重开的回复，
//     只能通知/升级/取消。
//
// 观测只读库，不写任何状态；动作仍由 validation + apply 两层把关。

const (
	// supervisionInterruptedMaxRetries 是同一条中断链允许的自动重开次数。用户
	// 裁定「重启后可以重试至多 3 次」：一次重启常常只是把好回合打断，值得多
	// 给几次；但必须封顶，否则模型反复重开会变成死循环。
	supervisionInterruptedMaxRetries = 3
	// routerInterruptedRetryMaxAge 是中断回合还能自动重开的最长年龄。更老的回合
	// 用户多半已经自己接着做了别的事，静默重跑只会制造噪音；观测直接把
	// retry_allowed 关掉，让模型改走 notify/escalate。
	routerInterruptedRetryMaxAge = 24 * time.Hour
)

// healthEventSupervision 给巡检/恢复事件挂监管观测。mode=off 返回 nil（调用方
// 据此完全不改 payload）。未知事件返回 nil。
func (s *Service) healthEventSupervision(ctx context.Context, store *TaskStore, task Task, event RouterEvent, now time.Time) *SupervisionObservation {
	if s == nil || store == nil {
		return nil
	}
	cfg := s.RouterConfig().Supervision.Normalized()
	if cfg.Mode == RouterSupervisionOff {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	obs := &SupervisionObservation{
		Mode:            cfg.Mode,
		TaskID:          task.ID,
		TaskStatus:      task.Status,
		ActionBudget:    cfg.MaxActionsPerHour,
		RunActionBudget: cfg.MaxActionsPerRun,
	}
	switch event.Kind {
	case RouterEventTurnInterrupted:
		obs.Direct = s.interruptedTurnState(ctx, store, task, event, now)
	case RouterEventTurnStalled:
		obs.Direct = s.stalledTurnState(ctx, store, task, event, now)
	case RouterEventTaskOrphaned:
		// 孤儿任务既没有回复行也没有 open 节点：只留通用块（预算、任务状态），
		// 让 notify_user / escalate / cancel_task 可用；retry_direct 会因为
		// Direct 为空被动作校验拒绝。
	default:
		return nil
	}
	// 三种事件都没有 Run，只受滚动一小时额度约束。
	_, obs.ActionsLastHour = s.supervisionActionUsage(ctx, store, task.ID, "", now)
	return obs
}

// interruptedTurnState 从事件载荷读回复行，算出「还能不能自动重开」。
func (s *Service) interruptedTurnState(ctx context.Context, store *TaskStore, task Task, event RouterEvent, now time.Time) *SupervisionDirectState {
	activityID := strings.TrimSpace(routerEventPayloadString(event.PayloadJSON, "activity_id"))
	if activityID == "" {
		return nil
	}
	reply, err := NewActivityStore(store.db).Get(ctx, activityID)
	if err != nil || reply == nil {
		// 回复行都没了就没有可核对的证据：允许块存在（通知/升级仍可用），但
		// 关掉重开 —— 没人知道该拿哪条留言重跑。
		return &SupervisionDirectState{ActivityID: activityID, RetryAllowed: false}
	}
	used := s.supervisionInterruptedRetriesUsed(ctx, store, task.ID, event.UserActivityID)
	age := now.Sub(reply.CreatedAt)
	state := &SupervisionDirectState{
		ActivityID:  activityID,
		Status:      reply.Status,
		StartedAt:   reply.CreatedAt.UTC().Format(routerTimeLayout),
		Replying:    s.sessionRunning(reply.SessionKey),
		RetriesUsed: used,
		// 超龄的中断不再自动重跑，但重试额度本身没被用掉。
		RetryAllowed: used < supervisionInterruptedMaxRetries && age < routerInterruptedRetryMaxAge,
		Interrupted:  true,
		MaxRetries:   supervisionInterruptedMaxRetries,
	}
	if idle := int(age.Seconds()); idle > 0 {
		state.IdleSeconds = idle
	}
	return state
}

// stalledTurnState 从事件载荷与实时水位算出停滞现场。停滞后仍允许重开一次，
// 且探针报 replying 也不挡 —— 那正是水位证据的意义。
func (s *Service) stalledTurnState(ctx context.Context, store *TaskStore, task Task, event RouterEvent, now time.Time) *SupervisionDirectState {
	activityID := strings.TrimSpace(routerEventPayloadString(event.PayloadJSON, "activity_id"))
	if activityID == "" {
		return nil
	}
	reply, err := NewActivityStore(store.db).Get(ctx, activityID)
	if err != nil || reply == nil {
		return &SupervisionDirectState{ActivityID: activityID, RetryAllowed: false}
	}
	lastOutput := routerEventPayloadTime(event.PayloadJSON, "last_output_at")
	if watermark, known := s.SessionTurnWatermark(task.RootID, reply.SessionKey); known {
		lastOutput = watermark
	}
	replying := s.sessionRunning(reply.SessionKey)
	used := s.supervisionDirectRetriesUsed(ctx, store, task.ID, activityID)
	retriesLeft := supervisionDirectMaxRetries - used
	if retriesLeft < 0 {
		retriesLeft = 0
	}
	state := &SupervisionDirectState{
		ActivityID:         activityID,
		Status:             reply.Status,
		StartedAt:          reply.CreatedAt.UTC().Format(routerTimeLayout),
		Replying:           replying,
		RetriesUsed:        used,
		RetryAllowed:       retriesLeft > 0,
		StalledByWatermark: true,
		MaxRetries:         supervisionDirectMaxRetries,
	}
	if !lastOutput.IsZero() {
		if idle := int(now.Sub(lastOutput).Seconds()); idle > 0 {
			state.IdleSeconds = idle
		}
	}
	return state
}

// supervisionInterruptedRetriesUsed 数「这条留言已经被重启恢复重开过几次」：只认
// trigger=turn_interrupted 且动作带 retry_direct 的决策（applied / applying）。
//
// 口径按原始留言而不是回复行：每次重开都会生成一条新的回复行，按行数会让
// 同一条链在两三次重启后无限续命。
func (s *Service) supervisionInterruptedRetriesUsed(ctx context.Context, store *TaskStore, taskID, userActivityID string) int {
	taskID, userActivityID = strings.TrimSpace(taskID), strings.TrimSpace(userActivityID)
	if store == nil || taskID == "" || userActivityID == "" {
		return 0
	}
	rows, err := store.db.QueryContext(ctx,
		`SELECT actions_json FROM router_decisions
		 WHERE task_id = ? AND user_activity_id = ? AND trigger_kind = ? AND status IN (?, ?)`,
		taskID, userActivityID, string(RouterTriggerTurnInterrupted),
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
			if action.Kind == RouterActionRetryDirect {
				used++
			}
		}
	}
	return used
}

// recordTurnInterruptedEvent 把「这一轮被重启带走」落进事件账本。
//
// 启动对账只负责把回复结算成 cancelled、任务放回 waiting_user：用户看到的是一轮
// 跑了一半就没了的结果，而没有任何东西会去补。事件给 Router 一次机会（自动重开
// 至多 3 次，见 supervisionInterruptedMaxRetries），也给审计留下「这一轮为什么
// 没结果」的唯一记录。
//
// 拿不到原始用户留言时不落事件：重开要用它当正文，没有它就只剩通知，而通知在
// 启动对账的可见系统说明里已经给过了，再落一条只会是噪音。
func (s *Service) recordTurnInterruptedEvent(ctx context.Context, store *TaskStore, activities *ActivityStore, reply *TaskActivity) {
	if s == nil || store == nil || activities == nil || reply == nil {
		return
	}
	taskID := strings.TrimSpace(reply.TaskID)
	rootID := strings.TrimSpace(reply.RootID)
	if taskID == "" || rootID == "" {
		return
	}
	userActivityID := s.directTurnUserActivity(ctx, activities, reply)
	if userActivityID == "" {
		log.Printf("[kanban/recover] interrupted reply without user message task=%s activity=%s", taskID, reply.ID)
		return
	}
	payload := map[string]any{
		"task_id":     taskID,
		"activity_id": reply.ID,
		"status":      reply.Status,
		"since":       reply.CreatedAt.UTC().Format(routerTimeLayout),
	}
	// 载荷里的监管块只是账本快照；决策时 brief 会重算（预算必须是最新的）。
	if probe, probeErr := json.Marshal(map[string]any{"activity_id": reply.ID}); probeErr == nil {
		if task, taskErr := store.GetTask(ctx, taskID); taskErr == nil {
			if obs := s.healthEventSupervision(ctx, store, task, RouterEvent{
				Kind: RouterEventTurnInterrupted, UserActivityID: userActivityID, PayloadJSON: string(probe),
			}, time.Now().UTC()); obs != nil {
				payload["supervision"] = obs
			}
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[kanban/recover] interrupted payload task=%s activity=%s err=%v", taskID, reply.ID, err)
		return
	}
	instanceKey := "interrupt:" + reply.ID
	if _, _, err := NewRouterStore(store.db).InsertEvent(ctx, RouterEvent{
		RootID:         rootID,
		TaskID:         taskID,
		Kind:           RouterEventTurnInterrupted,
		InstanceKey:    instanceKey,
		UserActivityID: userActivityID,
		TurnID:         routerEventTurnID(reply.ID, RouterEventTurnInterrupted, instanceKey),
		ChainDepth:     s.routerEventChainDepth(ctx, store, userActivityID),
		PayloadJSON:    string(raw),
		CreatedAt:      time.Now().UTC(),
	}); err != nil {
		log.Printf("[kanban/recover] interrupted event task=%s activity=%s err=%v", taskID, reply.ID, err)
	}
}

// routerEventPayloadTime 读载荷里的 RFC3339 时间戳。缺失或坏值返回零值。
func routerEventPayloadTime(payloadJSON, key string) time.Time {
	raw := strings.TrimSpace(routerEventPayloadString(payloadJSON, key))
	if raw == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(routerTimeLayout, raw)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}
