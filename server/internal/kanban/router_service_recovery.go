package kanban

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
)

// 回合失败的收尾：保留已记录的动作、回落旧路径、并把「超时」这件事告诉用户。
//
// 2026-11-19 线上事故（看板任务 #86）：Router 回合的 90s 死线包住了
// usecase.SendMessage 的**回合后落库**，于是 persistTurnBlocks 在已取消的 ctx
// 上失败，整轮被判 fallback。而模型其实早已把 router_ask_user 调完、工具已返回
// {"status":"recorded"} —— 已经发生的决定被整轮丢弃，用户什么也看不到，旧路径
// 还另起了一条 Run。写工具返回 recorded 就是已发生的事实，后置失败不能把它抹掉。

// finishTurnFailure 是 runRouterTurn 的错误分支：先尝试救回已记录的动作，救不
// 回来才回落旧路径；超时（且是真正执行这一轮的模式）还要留一条可见告知。
func (rs *RouterService) finishTurnFailure(
	ctx context.Context,
	config RouterConfig,
	decision *RouterDecision,
	brief *SituationBrief,
	actions []RouterAction,
	cause error,
	started time.Time,
	routeMode bool,
) {
	if decision == nil {
		return
	}
	if routeMode && len(actions) > 0 && rs.salvageRouteActions(config, decision, brief, actions, cause, started) {
		return
	}
	rs.finishFallback(ctx, decision, started, cause, routeMode)
	// 只有超时需要额外告知：其它失败本来就会回落成一条旧路径回复，再插一行系统
	// 消息只是噪声。空回合（显式无决策）同理。
	if routeMode && isRouterTurnTimeout(cause) {
		rs.notifyRouterTurnTimeout(config, decision, len(actions))
	}
}

// salvageRouteActions 把回合失败前已经记录的动作按正常 route 路径应用。
//
// 校验必须照旧：记下来的动作同样可能非法（例如活动节点在回合中途消失），这时
// 不能硬应用，交给回落。应用本身用独立 ctx —— 回合的死线已经过去，落库不该再
// 受它影响。
func (rs *RouterService) salvageRouteActions(
	config RouterConfig,
	decision *RouterDecision,
	brief *SituationBrief,
	actions []RouterAction,
	cause error,
	started time.Time,
) bool {
	if validationErr := validateRouterActions(brief, actions, config); validationErr != nil {
		log.Printf("[router] salvage.rejected task=%s decision=%s actions=%d err=%v",
			decision.TaskID, decision.ID, len(actions), validationErr)
		return false
	}
	ctx := context.Background()
	latency := int(time.Since(started).Milliseconds())
	note := "router turn did not finish (" + cause.Error() + "); applied " +
		strconv.Itoa(len(actions)) + " actions recorded before the failure"
	if err := rs.store.UpdateDecisionResult(ctx, decision.ID, RouterDecisionApplying, actions,
		decision.RouterSessionKey, RouterAgreementNoRouter, "", latency, 0, 0); err != nil {
		log.Printf("[router] salvage.decision.update.error id=%s err=%v", decision.ID, err)
	}
	decision.Actions = actions
	decision.Status = RouterDecisionApplying
	decision.Error = ""
	decision.LatencyMS = latency
	rs.broadcast(decision)

	if err := rs.applyRoute(ctx, decision); err != nil {
		log.Printf("[router] salvage.apply.error id=%s task=%s err=%v", decision.ID, decision.TaskID, err)
		_ = rs.store.MarkDecisionStatus(ctx, decision.ID, RouterDecisionFailed, err.Error())
		decision.Status = RouterDecisionFailed
		decision.Error = err.Error()
		rs.broadcast(decision)
		// 动作只应用了一部分（或一条都没成功）：超时的话更要让用户知道这轮被截断了。
		if isRouterTurnTimeout(cause) {
			rs.notifyRouterTurnTimeout(config, decision, len(actions))
		}
		return true
	}
	if err := rs.store.MarkDecisionStatus(ctx, decision.ID, RouterDecisionApplied, note); err != nil {
		log.Printf("[router] salvage.status.error id=%s err=%v", decision.ID, err)
	}
	decision.Status = RouterDecisionApplied
	decision.Error = note
	rs.broadcast(decision)
	log.Printf("[router] salvaged task=%s decision=%s actions=%d kinds=%s latency_ms=%d cause=%s",
		decision.TaskID, decision.ID, len(actions), routerActionKinds(actions), latency, cause)
	// 告知行只说「超时」：救回本身与失败原因无关，CLI 崩溃也会救回动作，
	// 但写成「超过 90 秒」就是假消息。
	if isRouterTurnTimeout(cause) {
		rs.notifyRouterTurnTimeout(config, decision, len(actions))
	}
	return true
}

// notifyRouterTurnTimeout 写一条用户可见的系统行：调度回合超时了。
//
// 没有这一行，线上表现只是「90 秒后突然出现一条旧路径回复」，用户无法分辨是
// Router 超时、模型故意沉默，还是自己的话被吞了。ID 由决策派生，重放/重启不会
// 写出第二行（InsertOrIgnore）。
func (rs *RouterService) notifyRouterTurnTimeout(config RouterConfig, decision *RouterDecision, salvaged int) {
	if rs == nil || rs.service == nil || decision == nil {
		return
	}
	store := rs.taskStore()
	if store == nil {
		return
	}
	timeout := config.TurnTimeout
	if timeout <= 0 {
		timeout = defaultRouterTurnTimeout
	}
	seconds := int(timeout.Seconds())
	var body string
	if salvaged > 0 {
		body = fmt.Sprintf("调度回合超过 %d 秒未完成，已保留并执行此前记录的 %d 个动作；其余决策未能执行。", seconds, salvaged)
	} else {
		body = fmt.Sprintf("调度回合超过 %d 秒未完成，这条留言已改由原执行路径继续处理。", seconds)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	activity := &TaskActivity{
		ID:     "router-turn-timeout:" + decision.ID,
		TaskID: decision.TaskID,
		RootID: decision.RootID,
		Type:   ActivityTypeAttention,
		Author: "system",
		Role:   "system",
		Status: ActivityStatusCompleted,
		// 超时但已由原路径继续，属于降级不是失败：标 warning。
		Severity: ActivitySeverityWarning,
		Body:     body,
	}
	created, err := NewActivityStore(store.db).InsertOrIgnore(ctx, activity)
	if err != nil {
		log.Printf("[router] timeout.notice.error task=%s decision=%s err=%v", decision.TaskID, decision.ID, err)
		return
	}
	if created {
		rs.service.broadcastWorkflowActivity(ctx, store, activity, true, nil)
	}
}

// isRouterTurnTimeout 区分「回合超时」与其它失败。SendMessage 用 %w 包装了落库
// 错误，errors.Is 能穿过去；字符串兜底只防上游有人把错误又拼成新错误。
func isRouterTurnTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return strings.Contains(err.Error(), "deadline exceeded")
}

// fallbackRoute 在 Router 不可用/拒绝时把这一轮交回旧路径。占位回复复用，
// 不新增一条；旧路径的失败会写回占位回复。
func (rs *RouterService) fallbackRoute(ctx context.Context, decision *RouterDecision) {
	svc, err := rs.service.ActivityServiceForRoot(rs.rootID)
	if err != nil {
		log.Printf("[router] fallback.unavailable task=%s err=%v", decision.TaskID, err)
		return
	}
	if err := svc.FallbackRoute(ctx, decision.TaskID, decision.UserActivityID, decision.PlaceholderActivityID); err != nil {
		log.Printf("[router] fallback.error task=%s err=%v", decision.TaskID, err)
	}
}

func (rs *RouterService) finishFallback(ctx context.Context, decision *RouterDecision, started time.Time, cause error, routeMode bool) {
	if decision == nil {
		return
	}
	if strings.TrimSpace(decision.BriefJSON) == "" {
		decision.BriefJSON = "{}"
	}
	if err := rs.store.InsertDecision(ctx, decision); err != nil && !errors.Is(err, ErrRouterDecisionExists) {
		log.Printf("[router] decision.insert.error id=%s err=%v", decision.ID, err)
		return
	}
	latency := int(time.Since(started).Milliseconds())
	errText := ""
	if cause != nil {
		errText = cause.Error()
	}
	if err := rs.store.UpdateDecisionResult(ctx, decision.ID, RouterDecisionFallback, nil, "", RouterAgreementUnknown, errText, latency, 0, 0); err != nil {
		log.Printf("[router] decision.fallback.error id=%s err=%v", decision.ID, err)
	}
	// 回落是「用户看到的结果其实是旧路径跑的」的唯一线索。没有这一行，
	// 线上只能看到一条 90s 后出现的普通回复，分不清是 Router 超时还是模型故意沉默。
	log.Printf("[router] fallback task=%s decision=%s latency_ms=%d route_mode=%v cause=%s",
		decision.TaskID, decision.ID, latency, routeMode, errText)
	decision.Status = RouterDecisionFallback
	decision.Error = errText
	decision.Agreement = RouterAgreementUnknown
	decision.LatencyMS = latency
	rs.broadcast(decision)
	if routeMode {
		rs.fallbackRoute(ctx, decision)
	}
}
