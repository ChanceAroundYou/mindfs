package kanban

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

// coordinate 的事件回合（Plan 2026-11-18，R1 T7）。
//
// 一轮运行期事件的顺序不可换：claim → 去重/预算/证据闸门 → 模型 → 校验 →
// 应用可见副作用 → 决策 applied → 事件 done。任何一步失败都把事件推进到
// failed/skipped 终态并写清原因；绝不回落旧路径、绝不新开 Run、绝不自动
// cancel/retry/审批——运行期事件只是汇报，不是新的执行指令。

// runCoordinateEventTurn 是 lane 里的运行期事件回合入口。
func (rs *RouterService) runCoordinateEventTurn(taskID string, trigger RouterTrigger) {
	if rs == nil || rs.store == nil || rs.service == nil || trigger.Event == nil {
		return
	}
	ctx := context.Background()
	event := *trigger.Event
	config := rs.service.RouterConfig().Normalized()
	rootID := strings.TrimSpace(trigger.RootID)
	if rootID == "" {
		rootID = rs.rootID
	}
	decision := &RouterDecision{
		ID:             newID("rdec"),
		TaskID:         taskID,
		RootID:         rootID,
		Scope:          RouterScopeTask,
		OwnerID:        taskID,
		UserActivityID: strings.TrimSpace(event.UserActivityID),
		TriggerKind:    trigger.Kind,
		TriggerRef:     trigger.TriggerRef,
		TurnID:         trigger.TurnID,
		Status:         RouterDecisionApplying,
	}
	if !rs.beginCoordinateEventTurn(ctx, decision, &event) {
		return
	}
	brief, err := rs.turnBrief(ctx, taskID, trigger, config)
	if err != nil {
		rs.failCoordinateEventTurn(ctx, decision, RouterDecisionFailed, event.ID, err.Error())
		return
	}
	decision.BriefHash = brief.Hash()
	if raw, marshalErr := json.Marshal(brief); marshalErr == nil {
		decision.BriefJSON = string(raw)
	}
	if err := rs.store.InsertDecision(ctx, decision); err != nil {
		if errors.Is(err, ErrRouterDecisionExists) {
			// 同一 turn 已有行：上一轮死在了「插入之后、应用之前」。能收尾就收尾；
			// 只有零动作的 applying 才代表模型回合没跑完，删行重跑。
			existing, getErr := rs.store.DecisionByTurn(ctx, taskID, decision.TurnID)
			if getErr != nil {
				rs.failCoordinateEventTurn(ctx, decision, RouterDecisionFailed, event.ID, getErr.Error())
				return
			}
			if !rs.retryInterruptedTurnDecision(ctx, existing, event) {
				return
			}
			if insertErr := rs.store.InsertDecision(ctx, decision); insertErr != nil {
				rs.failCoordinateEventTurn(ctx, decision, RouterDecisionFailed, event.ID, insertErr.Error())
				return
			}
		} else {
			rs.failCoordinateEventTurn(ctx, decision, RouterDecisionFailed, event.ID, err.Error())
			return
		}
	}

	agent := rs.agent()
	if agent == nil {
		rs.failCoordinateEventTurn(ctx, decision, RouterDecisionFailed, event.ID, "router agent unavailable")
		return
	}
	promptHash := RouterPromptHash()
	sessionKey := rs.prepareSessionKey(ctx, agent, decision.RootID, taskID, config, promptHash)
	collector := newRouterTurnCollector(config)
	env := &routerToolEnv{store: rs.taskStore(), brief: brief, collector: collector, transcripts: rs.service.routerTranscriptReader(),
		supervisionMode: rs.service.RouterConfig().Supervision.Normalized().Mode}
	timeout := config.TurnTimeout
	if timeout <= 0 {
		timeout = defaultRouterTurnTimeout
	}
	turnCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	started := time.Now()
	result, err := agent.RunRouterTurn(turnCtx, RouterAgentRequest{
		RootID:                decision.RootID,
		TaskID:                taskID,
		SessionKey:            sessionKey,
		Agent:                 config.Agent,
		Model:                 config.Model,
		Effort:                config.Effort,
		Prompt:                decision.BriefJSON,
		DeveloperInstructions: RouterDeveloperInstructions(),
		PromptHash:            promptHash,
		Tools:                 env.tools(),
		ToolPolicy:            DefaultRouterToolPolicy(),
		Timeout:               timeout,
	})
	latency := int(time.Since(started).Milliseconds())
	if err != nil {
		rs.store.UpsertSession(ctx, &RouterSessionRecord{
			Scope: RouterScopeTask, OwnerID: taskID, RootID: decision.RootID,
			SessionKey: sessionKey, Agent: config.Agent, Model: config.Model, Effort: config.Effort,
			PromptHash: promptHash, Status: RouterSessionFailed, LastError: err.Error(), LastTurnAt: time.Now().UTC(),
		})
		rs.failCoordinateEventTurn(ctx, decision, RouterDecisionFailed, event.ID, err.Error())
		return
	}
	decision.RouterSessionKey = result.SessionKey
	if decision.RouterSessionKey == "" {
		decision.RouterSessionKey = sessionKey
	}
	actions, _, synthErr := synthesizeActions(collector.snapshot(), result.FinalText)
	if synthErr != nil {
		// 空回合不是「故意沉默」：运行期事件没有旧路径可回落，直接落 failed。
		rs.failCoordinateEventTurn(ctx, decision, RouterDecisionFailed, event.ID, synthErr.Error())
		return
	}
	// 模型只说话没调工具时合成出的 say 默认 purpose=chat（那是用户对话的语义）。
	// 事件回合这句是进度汇报，统一改写成 progress，再交给同一个应用层。
	for i := range actions {
		if actions[i].Kind == RouterActionSay && strings.TrimSpace(actions[i].Purpose) != "progress" {
			actions[i].Purpose = "progress"
		}
	}
	rs.finishCoordinateEventTurn(ctx, config, promptHash, brief, decision, result, actions, latency, event)
}

// beginCoordinateEventTurn 是闸门：CAS 领取、崩溃/重复投递 replay、预算与证据
// 重查。返回 false 表示本回合到此为止（调用方直接 return）。
func (rs *RouterService) beginCoordinateEventTurn(ctx context.Context, decision *RouterDecision, event *RouterEvent) bool {
	if rs == nil || rs.store == nil || decision == nil || event == nil {
		return false
	}
	claimed, err := rs.store.ClaimEvent(ctx, event.ID, rs.claimerID)
	if err != nil {
		log.Printf("[router/coordinate] claim.error event=%s err=%v", event.ID, err)
		return false
	}
	if !claimed {
		// 别的消费者（另一拍/另一进程）已经拿走；它负责落终态。
		return false
	}
	existing, err := rs.store.DecisionByTurn(ctx, decision.TaskID, decision.TurnID)
	if err == nil && existing != nil {
		if !rs.retryInterruptedTurnDecision(ctx, existing, *event) {
			return false
		}
	}
	if err != nil && !errors.Is(err, ErrRouterDecisionNotFound) {
		rs.completeCoordinatedEvent(ctx, event.ID, RouterEventFailed, "load decision: "+err.Error())
		return false
	}
	userActivityID := strings.TrimSpace(event.UserActivityID)
	if userActivityID == "" {
		rs.completeCoordinatedEvent(ctx, event.ID, RouterEventSkipped, "missing originating user activity")
		return false
	}
	if event.ChainDepth > routerEventMaxChainDepth {
		rs.completeCoordinatedEvent(ctx, event.ID, RouterEventSkipped,
			fmt.Sprintf("chain depth %d exceeds %d", event.ChainDepth, routerEventMaxChainDepth))
		return false
	}
	// 补做回合不占每留言 3 回合预算：它的上限是契约的 MaxRepairs（超出预算时
	// 压根不会有事件）。否则一条留言跑完一轮再补做两次，第三次就被静默跳过了。
	if event.Kind != RouterEventCompletionBlocked {
		turns, err := rs.countTurnsForUserActivity(ctx, decision.TaskID, userActivityID)
		if err != nil {
			rs.completeCoordinatedEvent(ctx, event.ID, RouterEventFailed, "count turns: "+err.Error())
			return false
		}
		if turns >= RouterCoordinateMaxTurnsPerUserActivity {
			rs.completeCoordinatedEvent(ctx, event.ID, RouterEventSkipped,
				fmt.Sprintf("turn budget exhausted (%d/%d)", turns, RouterCoordinateMaxTurnsPerUserActivity))
			return false
		}
	}
	if event.Kind == RouterEventRunStalled && !rs.stallEvidenceHolds(ctx, *event) {
		rs.completeCoordinatedEvent(ctx, event.ID, RouterEventSkipped, "no longer stalled")
		return false
	}
	if event.Kind == RouterEventDirectStalled && !rs.directEvidenceHolds(ctx, *event) {
		rs.completeCoordinatedEvent(ctx, event.ID, RouterEventSkipped, "no longer stalled")
		return false
	}
	return true
}

// countTurnsForUserActivity 数这条原始留言已经消耗的 Router 回合。预算按
// 留言而不是按任务：一个任务可以有多个用户回合，各自独立计数。
func (rs *RouterService) countTurnsForUserActivity(ctx context.Context, taskID, userActivityID string) (int, error) {
	if rs == nil {
		return 0, nil
	}
	return countUserTurnDecisions(ctx, rs.store.db, taskID, userActivityID)
}

// countUserTurnDecisions 是「这条原始留言花了几个 Router 回合」的唯一口径：
// 预算闸门与事件 brief 的 TurnsUsed 必须同源，否则模型看到的剩余额度与实际
// 闸门不一致。两类决策不计入：
//   - completion_blocked：补做有自己的契约预算（MaxRepairs）。
//   - run_failed / run_stalled / direct_stalled：监管回合。它们自己的上限是
//     每节点重试次数、每 Run 每小时动作预算与链深度；算进留言预算的话，
//     「入口 1 轮 + 2 次重试」正好把 3 轮用满，第 3 次失败被静默 skipped，
//     用户永远拿不到收尾说明（隔离实例 2026-11-20 实测：任务停在
//     waiting_user、零系统说明行）。
func countUserTurnDecisions(ctx context.Context, db *sql.DB, taskID, userActivityID string) (int, error) {
	taskID, userActivityID = strings.TrimSpace(taskID), strings.TrimSpace(userActivityID)
	if db == nil || taskID == "" || userActivityID == "" {
		return 0, nil
	}
	var turns int
	err := db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM router_decisions
WHERE task_id = ? AND user_activity_id = ?
  AND trigger_kind NOT IN (?, ?, ?, ?)`,
		taskID, userActivityID,
		string(RouterTriggerCompletionBlocked),
		string(RouterTriggerRunFailed), string(RouterTriggerRunStalled), string(RouterTriggerDirectStalled),
	).Scan(&turns)
	return turns, err
}

// stallEvidenceHolds 重查 stop-stall 的证据：payload 指向的 NodeRun 必须仍存在
// 且 running。事件是排队时的事实，轮到消费时可能已经结束。
func (rs *RouterService) stallEvidenceHolds(ctx context.Context, event RouterEvent) bool {
	var payload struct {
		NodeRunID string `json:"node_run_id"`
	}
	_ = json.Unmarshal([]byte(event.PayloadJSON), &payload)
	nodeRunID := strings.TrimSpace(payload.NodeRunID)
	if nodeRunID == "" {
		return false
	}
	store := rs.taskStore()
	if store == nil {
		return false
	}
	nodeRun, err := store.WorkflowNodeRunByID(ctx, nodeRunID)
	if err != nil {
		return false
	}
	return nodeRun.Status == NodeRunRunning
}

// replayCoordinateEventDecision 收尾「决策已存在」的事件。第一个返回值表示
// 本回合已处理完（调用方直接 return）；第二个返回值表示决策只插了一行、模型
// 回合没留下任何结果，调用方应删行重跑（见 retryInterruptedTurnDecision）。
// applied 直接 done；applying 再跑一次 ApplyRouteActions（确定 effect ID 幂等）；
// 拒绝/失败按原错误落终态。零模型调用、零重复可见活动。
func (rs *RouterService) replayCoordinateEventDecision(ctx context.Context, decision *RouterDecision, event RouterEvent) (handled bool, retry bool) {
	if rs == nil || rs.store == nil || decision == nil {
		return false, false
	}
	reason := strings.TrimSpace(decision.Error)
	switch decision.Status {
	case RouterDecisionApplied:
		rs.completeCoordinatedEvent(ctx, event.ID, RouterEventDone, "replayed")
		return true, false
	case RouterDecisionApplying:
		if len(decision.Actions) == 0 && decision.AppliedSeq == 0 && strings.TrimSpace(decision.RouterSessionKey) == "" {
			// 进程死在模型回合里：没有动作就没有副作用可重放，也没有结果可收尾。
			// 交给调用方删行重跑；否则这里会静默 done，汇报永久丢失。
			return false, true
		}
		if err := rs.applyRoute(ctx, decision); err != nil {
			log.Printf("[router/coordinate] replay.apply.error id=%s err=%v", decision.ID, err)
			_ = rs.store.MarkDecisionStatus(ctx, decision.ID, RouterDecisionFailed, err.Error())
			decision.Status = RouterDecisionFailed
			decision.Error = err.Error()
			rs.broadcast(decision)
			rs.completeCoordinatedEvent(ctx, event.ID, RouterEventFailed, err.Error())
			return true, false
		}
		if err := rs.store.MarkDecisionStatus(ctx, decision.ID, RouterDecisionApplied, ""); err != nil {
			log.Printf("[router/coordinate] replay.status.error id=%s err=%v", decision.ID, err)
		}
		decision.Status = RouterDecisionApplied
		decision.Error = ""
		rs.broadcast(decision)
		rs.completeCoordinatedEvent(ctx, event.ID, RouterEventDone, "replayed")
		return true, false
	case RouterDecisionRejected, RouterDecisionFailed, RouterDecisionFallback:
		if reason == "" {
			reason = "decision " + string(decision.Status)
		}
		rs.completeCoordinatedEvent(ctx, event.ID, RouterEventFailed, reason)
		return true, false
	case RouterDecisionShadow:
		// 监管影子回合的汇报类动作已应用、监管动作只记录：重放时按已完成收尾。
		rs.completeCoordinatedEvent(ctx, event.ID, RouterEventDone, "supervision_shadow")
		return true, false
	case RouterDecisionSkipped, RouterDecisionBypassed:
		rs.completeCoordinatedEvent(ctx, event.ID, RouterEventSkipped, "decision was "+string(decision.Status))
		return true, false
	default:
		return false, false
	}
}

// retryInterruptedTurnDecision 处理「同一 turn 已有决策行」。返回 true 表示那行
// 只是模型回合的残骸、已删掉，调用方可以继续重跑模型。任何无法收尾也无法
// 重跑的状态都必须落事件终态，不能把事件永久留在 claimed。
func (rs *RouterService) retryInterruptedTurnDecision(ctx context.Context, existing *RouterDecision, event RouterEvent) bool {
	if existing == nil {
		rs.completeCoordinatedEvent(ctx, event.ID, RouterEventFailed, "decision turn already exists")
		return false
	}
	if handled, retry := rs.replayCoordinateEventDecision(ctx, existing, event); handled || !retry {
		if !handled {
			reason := "decision " + string(existing.Status) + " cannot be replayed"
			_ = rs.store.MarkDecisionStatus(ctx, existing.ID, RouterDecisionFailed, reason)
			rs.completeCoordinatedEvent(ctx, event.ID, RouterEventFailed, reason)
		}
		return false
	}
	deleted, err := rs.store.DeleteInterruptedDecision(ctx, existing.ID)
	if err != nil || !deleted {
		reason := "interrupted decision could not be recovered"
		if err != nil {
			reason += ": " + err.Error()
		}
		_ = rs.store.MarkDecisionStatus(ctx, existing.ID, RouterDecisionFailed, reason)
		rs.completeCoordinatedEvent(ctx, event.ID, RouterEventFailed, reason)
		return false
	}
	log.Printf("[router/coordinate] decision.retry id=%s turn=%s", existing.ID, existing.TurnID)
	return true
}

// finishCoordinateEventTurn 复用 route 的「先落 actions、再按 seq 应用」路径，
// 额外多一步运行期动作白名单，并在副作用成功之后才把事件标 done。
func (rs *RouterService) finishCoordinateEventTurn(
	ctx context.Context,
	config RouterConfig,
	promptHash string,
	brief *SituationBrief,
	decision *RouterDecision,
	result RouterAgentResult,
	actions []RouterAction,
	latency int,
	event RouterEvent,
) {
	ctx = context.Background()
	decision.LatencyMS = latency
	decision.PromptTokens = result.Meta.PromptTokens
	decision.CompletionTokens = result.Meta.CompletionTokens
	decision.Actions = actions
	validationErr := validateRouterActions(brief, actions, config)
	if validationErr == nil {
		validationErr = validateRuntimeEventActions(brief, actions, config)
	}
	if validationErr != nil {
		errText := validationErr.Error()
		if updateErr := rs.store.UpdateDecisionResult(ctx, decision.ID, RouterDecisionRejected, actions, decision.RouterSessionKey,
			RouterAgreementNoRouter, errText, latency, result.Meta.PromptTokens, result.Meta.CompletionTokens); updateErr != nil {
			log.Printf("[router/coordinate] decision.update.error id=%s err=%v", decision.ID, updateErr)
		}
		rs.recordSession(ctx, config, promptHash, decision.TaskID, decision.RootID, result, RouterSessionActive, "")
		if isSupervisionBudgetError(validationErr) {
			// 预算用尽不是模型犯错：停手、告知用户、把任务放回待审核。
			rs.stopSupervisionForUser(ctx, decision, event.ID, errText)
			return
		}
		rs.failCoordinateEventTurn(ctx, decision, RouterDecisionRejected, event.ID, errText)
		return
	}
	if updateErr := rs.store.UpdateDecisionResult(ctx, decision.ID, RouterDecisionApplying, actions, decision.RouterSessionKey,
		RouterAgreementNoRouter, "", latency, result.Meta.PromptTokens, result.Meta.CompletionTokens); updateErr != nil {
		log.Printf("[router/coordinate] decision.update.error id=%s err=%v", decision.ID, updateErr)
	}
	rs.recordSession(ctx, config, promptHash, decision.TaskID, decision.RootID, result, RouterSessionActive, "")
	rs.broadcast(decision)
	// 影子模式：汇报类动作照常应用，监管写入只留在决策里（status=shadow）。
	applicable, shadowed := supervisionActionSplit(config.Supervision.Normalized().Mode, actions)
	applyDecision := decision
	if len(shadowed) > 0 {
		clone := *decision
		clone.Actions = applicable
		applyDecision = &clone
	}
	if err := rs.applyRoute(ctx, applyDecision); err != nil {
		log.Printf("[router/coordinate] decision.apply.error id=%s task=%s err=%v", decision.ID, decision.TaskID, err)
		_ = rs.store.MarkDecisionStatus(ctx, decision.ID, RouterDecisionFailed, err.Error())
		rs.failCoordinateEventTurn(ctx, decision, RouterDecisionFailed, event.ID, err.Error())
		return
	}
	if len(shadowed) > 0 {
		if err := rs.store.MarkDecisionStatus(ctx, decision.ID, RouterDecisionShadow, "supervision actions recorded in shadow mode"); err != nil {
			log.Printf("[router/coordinate] decision.status.error id=%s err=%v", decision.ID, err)
		}
		decision.Status = RouterDecisionShadow
		decision.Error = ""
		rs.broadcast(decision)
		rs.completeCoordinatedEvent(ctx, event.ID, RouterEventDone, "supervision_shadow")
		return
	}
	if err := rs.store.MarkDecisionStatus(ctx, decision.ID, RouterDecisionApplied, ""); err != nil {
		log.Printf("[router/coordinate] decision.status.error id=%s err=%v", decision.ID, err)
	}
	decision.Status = RouterDecisionApplied
	decision.Error = ""
	rs.broadcast(decision)
	// 事件完成必须晚于可见副作用：反过来会留下「done 但用户看不到」的不可恢复态。
	rs.completeCoordinatedEvent(ctx, event.ID, RouterEventDone, "")
}

// failCoordinateEventTurn 把失败写进决策与事件的终态。绝不调 fallbackRoute：
// 运行期事件没有「旧路径」可回落，回落会变成自动开新执行。
func (rs *RouterService) failCoordinateEventTurn(ctx context.Context, decision *RouterDecision, status RouterDecisionStatus, eventID, reason string) {
	if rs == nil || rs.store == nil {
		return
	}
	if decision != nil {
		if err := rs.store.MarkDecisionStatus(ctx, decision.ID, status, reason); err != nil {
			log.Printf("[router/coordinate] decision.fail.error id=%s err=%v", decision.ID, err)
		}
		decision.Status = status
		decision.Error = reason
		rs.broadcast(decision)
	}
	rs.completeCoordinatedEvent(ctx, eventID, RouterEventFailed, reason)
}

// completeCoordinatedEvent 是落事件终态的薄封装：失败只记日志，事件由启动
// 对账/重扫兜底，绝不让「写终态失败」回滚已经发生的副作用。
func (rs *RouterService) completeCoordinatedEvent(ctx context.Context, eventID, status, reason string) {
	if rs == nil || rs.store == nil || strings.TrimSpace(eventID) == "" {
		return
	}
	if err := rs.store.CompleteEvent(ctx, eventID, status, reason); err != nil {
		log.Printf("[router/coordinate] event.complete.error event=%s status=%s err=%v", eventID, status, err)
	}
}

// validateRuntimeEventActions 是运行期白名单：只允许汇报类动作与监管写入。
// 只读工具（如 router_inspect_run）不在 actions 里，保持可用。
func validateRuntimeEventActions(brief *SituationBrief, actions []RouterAction, _ RouterConfig) error {
	for _, action := range actions {
		switch action.Kind {
		case RouterActionSay, RouterActionAskUser, RouterActionSilent, RouterActionComplete:
			// complete_task 不属于监管动作：事件回合（例如 run_settled）本来就是
			// 收尾时机，不该要求 supervision 观测块。
			continue
		case RouterActionRetryNode, RouterActionNotifyUser, RouterActionResumeRun, RouterActionEscalate,
			RouterActionRetryDirect:
			// 监管动作的可用性绑定在观测块上：没有观测就不该出现这个动作。
			if brief == nil || brief.Supervision == nil {
				return fmt.Errorf("%s requires a supervision block", action.Kind)
			}
			continue
		default:
			return fmt.Errorf("%s is not allowed for runtime event turns", action.Kind)
		}
	}
	return nil
}

// directEvidenceHolds 重查直连停摆的证据：payload 指向的回复行必须仍存在，
// 且仍停在 pending/running。会话是否又在回复不在这里判 —— 那一步留给动作校验，
// 这样「事件成立但此刻不该重试」会落成一次可解释的拒绝，而不是静默跳过。
func (rs *RouterService) directEvidenceHolds(ctx context.Context, event RouterEvent) bool {
	activityID := routerEventPayloadString(event.PayloadJSON, "activity_id")
	if activityID == "" {
		return false
	}
	store := rs.taskStore()
	if store == nil {
		return false
	}
	reply, err := NewActivityStore(store.db).Get(ctx, activityID)
	if err != nil || reply == nil {
		return false
	}
	return reply.Status == ActivityStatusPending || reply.Status == ActivityStatusRunning
}
