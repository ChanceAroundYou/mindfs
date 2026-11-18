package kanban

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// RouterDecisionBroadcaster 广播决策生命周期事件。生产实现挂在每账户
// StreamHub 上；nil 时静默。
type RouterDecisionBroadcaster interface {
	RouterDecisionUpdated(rootID, taskID string, decision *RouterDecision)
}

// RouterService 是一个 root 下的 Router lane 管理器。
//
// lane 的粒度是任务：同一任务的 turn 严格串行（否则两个回合会抢同一 Router
// 会话），不同任务可以并行。这个对象必须由长生命周期的 kanban.Service 缓存，
// 不能挂在每次请求新建的 ActivityService 上。
type RouterService struct {
	service *Service
	rootID  string
	store   *RouterStore

	mu    sync.Mutex
	lanes map[string]*routerLane
	// closed 用于进程退出/测试时让空闲清扫停止。
	closed chan struct{}
}

type routerLane struct {
	mu      sync.Mutex
	queue   []RouterTrigger
	running bool
}

func newRouterService(service *Service, rootID string, config RouterConfig, store *TaskStore) *RouterService {
	rs := &RouterService{
		service: service,
		rootID:  rootID,
		lanes:   make(map[string]*routerLane),
		closed:  make(chan struct{}),
	}
	if store != nil {
		rs.store = NewRouterStore(store.db)
	}
	config = config.Normalized()
	if config.IdleClose > 0 && config.ModeForRoot(rootID) != RouterModeOff {
		go rs.sweepIdleSessions(config.IdleClose)
	}
	// route 模式的 apply 是「先落 applying、再逐条应用」。进程死在中间时这些行
	// 永远不会自己前进，启动时统一标 failed，避免界面/诊断看到永久的 applying。
	if rs.store != nil && config.ModeForRoot(rootID) != RouterModeOff {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if n, err := rs.store.RecoverApplyingDecisions(ctx, "router apply interrupted by process restart"); err != nil {
			log.Printf("[router] recover.applying.error root=%s err=%v", rootID, err)
		} else if n > 0 {
			log.Printf("[router] recover.applying root=%s count=%d", rootID, n)
		}
		cancel()
	}
	return rs
}

// Enqueue 把一个触发放进任务 lane。shadow 只记录决策；route 在回合结束后
// 按 seq 应用动作（失败/非法时回落旧路径）。coordinate 尚未实现，显式不接管。
func (rs *RouterService) Enqueue(trigger RouterTrigger) {
	if rs == nil || rs.service == nil {
		return
	}
	taskID := strings.TrimSpace(trigger.TaskID)
	rootID := strings.TrimSpace(trigger.RootID)
	if taskID == "" {
		return
	}
	if rootID == "" {
		rootID = rs.rootID
	}
	config := rs.service.RouterConfig()
	mode := config.ModeForRoot(rootID)
	if mode != RouterModeShadow && mode != RouterModeRoute {
		return
	}
	if trigger.Mode == "" {
		trigger.Mode = mode
	}
	if strings.TrimSpace(trigger.TurnID) == "" {
		trigger.TurnID = newID("rturn")
	}
	lane := rs.lane(taskID)
	lane.mu.Lock()
	lane.queue = append(lane.queue, trigger)
	if lane.running {
		lane.mu.Unlock()
		return
	}
	lane.running = true
	lane.mu.Unlock()

	go rs.drain(taskID, lane)
}

func (rs *RouterService) lane(taskID string) *routerLane {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	lane := rs.lanes[taskID]
	if lane == nil {
		lane = &routerLane{}
		rs.lanes[taskID] = lane
	}
	return lane
}

func (rs *RouterService) drain(taskID string, lane *routerLane) {
	for {
		lane.mu.Lock()
		if len(lane.queue) == 0 {
			lane.running = false
			lane.mu.Unlock()
			return
		}
		trigger := lane.queue[0]
		lane.queue = lane.queue[1:]
		lane.mu.Unlock()

		rs.runRouterTurn(taskID, trigger)
	}
}

// activeLanes 报当前有回合在跑的任务数，仅供诊断。
func (rs *RouterService) activeLanes() int {
	if rs == nil {
		return 0
	}
	rs.mu.Lock()
	lanes := make([]*routerLane, 0, len(rs.lanes))
	for _, lane := range rs.lanes {
		lanes = append(lanes, lane)
	}
	rs.mu.Unlock()
	active := 0
	for _, lane := range lanes {
		lane.mu.Lock()
		if lane.running {
			active++
		}
		lane.mu.Unlock()
	}
	return active
}

// CloseTask 关闭某任务的 Router runtime 并丢弃未开始的回合。已落库的决策/
// 会话行由 TaskStore.DeleteTask 删除。
func (rs *RouterService) CloseTask(ctx context.Context, taskID string) {
	if rs == nil {
		return
	}
	rs.mu.Lock()
	lane := rs.lanes[taskID]
	delete(rs.lanes, taskID)
	rs.mu.Unlock()
	if lane != nil {
		lane.mu.Lock()
		lane.queue = nil
		lane.mu.Unlock()
	}
	rs.closeSession(ctx, taskID, "task deleted")
}

// Close 停止空闲清扫。仅测试/进程收尾使用。
func (rs *RouterService) Close() {
	if rs == nil {
		return
	}
	select {
	case <-rs.closed:
	default:
		close(rs.closed)
	}
}

func (rs *RouterService) runRouterTurn(taskID string, trigger RouterTrigger) {
	if rs.store == nil || rs.service == nil {
		return
	}
	ctx := context.Background()
	config := rs.service.RouterConfig().Normalized()
	routeMode := trigger.Mode == RouterModeRoute
	started := time.Now()
	decisionID := newID("rdec")
	status := RouterDecisionShadow
	if routeMode {
		status = RouterDecisionApplying
	}
	decision := &RouterDecision{
		ID:                    decisionID,
		TaskID:                taskID,
		RootID:                trigger.RootID,
		Scope:                 RouterScopeTask,
		OwnerID:               taskID,
		UserActivityID:        trigger.UserActivityID,
		PlaceholderActivityID: trigger.PlaceholderID,
		TriggerKind:           trigger.Kind,
		TriggerRef:            trigger.TriggerRef,
		TurnID:                trigger.TurnID,
		Status:                status,
	}
	if !routeMode {
		decision.LegacyAction = trigger.LegacyAction
	}
	if decision.RootID == "" {
		decision.RootID = rs.rootID
	}
	brief, err := rs.turnBrief(ctx, taskID, trigger, config)
	if err != nil {
		rs.finishFallback(ctx, decision, started, err, routeMode)
		return
	}
	if routeMode && (brief.Task.Status == StatusPaused || isTerminalStatus(brief.Task.Status)) {
		decision.Status = RouterDecisionRejected
		decision.Error = ErrTaskClosed.Error()
		if brief.Task.Status == StatusPaused {
			decision.Error = ErrTaskPaused.Error()
		}
	}
	decision.BriefHash = brief.Hash()
	if raw, err := json.Marshal(brief); err == nil {
		decision.BriefJSON = string(raw)
	}
	if err := rs.store.InsertDecision(ctx, decision); err != nil {
		if errors.Is(err, ErrRouterDecisionExists) {
			return
		}
		log.Printf("[router] decision.insert.error root=%s task=%s turn=%s err=%v", decision.RootID, taskID, decision.TurnID, err)
		return
	}

	if decision.Status == RouterDecisionRejected {
		activity, err := rs.service.ActivityServiceForRoot(rs.rootID)
		if err == nil {
			err = activity.failUnusedRoutePlaceholder(ctx, decision, decision.Error)
		}
		if err != nil {
			log.Printf("[router] placeholder.fail.error id=%s err=%v", decision.ID, err)
		}
		rs.broadcast(decision)
		return
	}

	agent := rs.agent()
	if agent == nil {
		rs.finishFallback(ctx, decision, started, errors.New("router agent unavailable"), routeMode)
		return
	}
	promptHash := RouterPromptHash()
	sessionKey := rs.prepareSessionKey(ctx, agent, decision.RootID, taskID, config, promptHash)

	collector := newRouterTurnCollector(config)
	env := &routerToolEnv{store: rs.taskStore(), brief: brief, collector: collector}
	timeout := config.TurnTimeout
	if timeout <= 0 {
		timeout = defaultRouterTurnTimeout
	}
	turnCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

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
		rs.finishFallback(ctx, decision, started, err, routeMode)
		return
	}
	decision.RouterSessionKey = result.SessionKey
	if decision.RouterSessionKey == "" {
		decision.RouterSessionKey = sessionKey
	}
	actions, synthesized := synthesizeActions(collector.snapshot(), result.FinalText)
	if routeMode {
		rs.finishRouteTurn(ctx, config, promptHash, brief, decision, result, actions, latency)
		return
	}
	status = RouterDecisionShadow
	errText := ""
	agreement := routerAgreement(trigger.LegacyAction, actions)
	if validationErr := validateRouterActions(brief, actions, config); validationErr != nil {
		status = RouterDecisionRejected
		errText = validationErr.Error()
	}
	if synthesized && status == RouterDecisionShadow && errText == "" {
		errText = "synthesized from final text (no write tool called)"
	}
	if updateErr := rs.store.UpdateDecisionResult(ctx, decision.ID, status, actions, decision.RouterSessionKey, agreement, errText,
		latency, result.Meta.PromptTokens, result.Meta.CompletionTokens); updateErr != nil {
		log.Printf("[router] decision.update.error id=%s err=%v", decision.ID, updateErr)
	}
	decision.Actions = actions
	decision.Status = status
	decision.Agreement = agreement
	decision.Error = errText
	decision.LatencyMS = latency
	decision.PromptTokens = result.Meta.PromptTokens
	decision.CompletionTokens = result.Meta.CompletionTokens
	rs.recordSession(ctx, config, promptHash, taskID, decision.RootID, result, RouterSessionActive, "")
	rs.broadcast(decision)
}

// finishRouteTurn 是 route 模式的后半程：校验失败 → rejected 并回落旧路径；
// 校验通过 → 先持久化 actions（applying），再按 seq 应用，最后 applied/failed。
func (rs *RouterService) finishRouteTurn(ctx context.Context, config RouterConfig, promptHash string, brief *SituationBrief, decision *RouterDecision, result RouterAgentResult, actions []RouterAction, latency int) {
	ctx = context.Background()
	now := func(status RouterDecisionStatus, errText string) {
		decision.Status = status
		decision.Error = errText
		decision.LatencyMS = latency
		decision.PromptTokens = result.Meta.PromptTokens
		decision.CompletionTokens = result.Meta.CompletionTokens
		rs.broadcast(decision)
	}
	if validationErr := validateRouterActions(brief, actions, config); validationErr != nil {
		errText := validationErr.Error()
		if updateErr := rs.store.UpdateDecisionResult(ctx, decision.ID, RouterDecisionRejected, actions, decision.RouterSessionKey, RouterAgreementNoRouter, errText,
			latency, result.Meta.PromptTokens, result.Meta.CompletionTokens); updateErr != nil {
			log.Printf("[router] decision.update.error id=%s err=%v", decision.ID, updateErr)
		}
		decision.Actions = actions
		rs.recordSession(ctx, config, promptHash, decision.TaskID, decision.RootID, result, RouterSessionActive, "")
		now(RouterDecisionRejected, errText)
		rs.fallbackRoute(ctx, decision)
		return
	}
	if updateErr := rs.store.UpdateDecisionResult(ctx, decision.ID, RouterDecisionApplying, actions, decision.RouterSessionKey, RouterAgreementNoRouter, "",
		latency, result.Meta.PromptTokens, result.Meta.CompletionTokens); updateErr != nil {
		log.Printf("[router] decision.update.error id=%s err=%v", decision.ID, updateErr)
	}
	decision.Actions = actions
	rs.recordSession(ctx, config, promptHash, decision.TaskID, decision.RootID, result, RouterSessionActive, "")
	now(RouterDecisionApplying, "")

	if err := rs.applyRoute(ctx, decision); err != nil {
		log.Printf("[router] decision.apply.error id=%s task=%s err=%v", decision.ID, decision.TaskID, err)
		_ = rs.store.MarkDecisionStatus(ctx, decision.ID, RouterDecisionFailed, err.Error())
		now(RouterDecisionFailed, err.Error())
		return
	}
	if err := rs.store.MarkDecisionStatus(ctx, decision.ID, RouterDecisionApplied, ""); err != nil {
		log.Printf("[router] decision.status.error id=%s err=%v", decision.ID, err)
	}
	now(RouterDecisionApplied, "")
}

// applyRoute 取该 root 的 ActivityService 并应用动作。ActivityService 每次新建，
// 但 runtime 状态（lane/agent/gate）都挂在长生命周期的 Service 上。
func (rs *RouterService) applyRoute(ctx context.Context, decision *RouterDecision) error {
	svc, err := rs.service.ActivityServiceForRoot(rs.rootID)
	if err != nil {
		return fmt.Errorf("activity service: %w", err)
	}
	return svc.ApplyRouteActions(ctx, decision)
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
	decision.Status = RouterDecisionFallback
	decision.Error = errText
	decision.Agreement = RouterAgreementUnknown
	decision.LatencyMS = latency
	rs.broadcast(decision)
	if routeMode {
		rs.fallbackRoute(ctx, decision)
	}
}

func (rs *RouterService) agent() RouterAgent {
	if rs == nil || rs.service == nil {
		return nil
	}
	rs.service.mu.Lock()
	defer rs.service.mu.Unlock()
	return rs.service.routerAgent
}

func (rs *RouterService) taskStore() *TaskStore {
	if rs == nil || rs.service == nil {
		return nil
	}
	store, err := rs.service.taskStore(rs.rootID)
	if err != nil {
		return nil
	}
	return store
}

func (rs *RouterService) broadcast(decision *RouterDecision) {
	if rs == nil || rs.service == nil || decision == nil {
		return
	}
	rs.service.mu.Lock()
	broadcaster := rs.service.routerBroadcaster
	rs.service.mu.Unlock()
	if broadcaster != nil {
		broadcaster.RouterDecisionUpdated(decision.RootID, decision.TaskID, decision)
	}
}

// routerAgreement 是 shadow 的可解释比较：
//   - legacy 失败/不可判定 -> unknown；
//   - legacy workflow vs run_workflow：同 workflow -> match，否则 mismatch；
//   - legacy direct vs message_agent -> match；
//   - legacy none vs silent -> match；
//   - 其余组合 -> mismatch。
func routerAgreement(legacy *RouterLegacyAction, actions []RouterAction) string {
	if legacy == nil {
		return RouterAgreementNoRouter
	}
	switch legacy.Kind {
	case "failed", "":
		return RouterAgreementUnknown
	case "workflow":
		for _, action := range actions {
			if action.Kind == RouterActionRunWorkflow {
				if action.WorkflowID == legacy.WorkflowID {
					return RouterAgreementMatch
				}
				return RouterAgreementMismatch
			}
		}
		return RouterAgreementMismatch
	case "direct":
		for _, action := range actions {
			if action.Kind == RouterActionMessageAgent {
				return RouterAgreementMatch
			}
		}
		return RouterAgreementMismatch
	case "none":
		if len(actions) == 1 && actions[0].Kind == RouterActionSilent {
			return RouterAgreementMatch
		}
		return RouterAgreementMismatch
	default:
		return RouterAgreementUnknown
	}
}
