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
	return rs
}

// Enqueue 把一个触发放进任务 lane。P0 只接受 shadow；route/coordinate 由 P1
// 实现，在此之前显式不处理，避免「以为已经接管但实际没接管」。
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
	if config.ModeForRoot(rootID) != RouterModeShadow {
		return
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

		rs.runShadowTurn(taskID, trigger)
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

func (rs *RouterService) runShadowTurn(taskID string, trigger RouterTrigger) {
	if rs.store == nil || rs.service == nil {
		return
	}
	ctx := context.Background()
	config := rs.service.RouterConfig().Normalized()
	started := time.Now()
	decisionID := newID("rdec")
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
		Status:                RouterDecisionShadow,
		LegacyAction:          trigger.LegacyAction,
	}
	if decision.RootID == "" {
		decision.RootID = rs.rootID
	}
	brief := trigger.Brief
	if brief == nil {
		store, err := rs.service.taskStore(rs.rootID)
		if err != nil {
			rs.finishFallback(ctx, decision, started, fmt.Errorf("load task store: %w", err))
			return
		}
		task, err := store.GetTask(ctx, taskID)
		if err != nil {
			rs.finishFallback(ctx, decision, started, fmt.Errorf("load task: %w", err))
			return
		}
		brief, err = buildSituationBrief(ctx, store, rs.service.Workflows, config, task, trigger.Kind, trigger.UserActivityID)
		if err != nil {
			rs.finishFallback(ctx, decision, started, fmt.Errorf("build brief: %w", err))
			return
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

	agent := rs.agent()
	if agent == nil {
		rs.finishFallback(ctx, decision, started, errors.New("router agent unavailable"))
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
		rs.finishFallback(ctx, decision, started, err)
		return
	}
	decision.RouterSessionKey = result.SessionKey
	if decision.RouterSessionKey == "" {
		decision.RouterSessionKey = sessionKey
	}
	actions, synthesized := synthesizeActions(collector.snapshot(), result.FinalText)
	status := RouterDecisionShadow
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

func (rs *RouterService) finishFallback(ctx context.Context, decision *RouterDecision, started time.Time, cause error) {
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
}

func (rs *RouterService) prepareSessionKey(ctx context.Context, agent RouterAgent, rootID, taskID string, config RouterConfig, promptHash string) string {
	record, err := rs.store.Session(ctx, RouterScopeTask, taskID)
	if err != nil || record == nil {
		return ""
	}
	if record.PromptHash != "" && record.PromptHash != promptHash {
		// 提示词规则变了：旧进程内的 system prompt 不会自动替换，先关 runtime；
		// 下次 open 会带新的 append-system-prompt（manager binding 仍可 resume）。
		if err := agent.CloseRouterSession(ctx, rootID, record.SessionKey); err != nil {
			log.Printf("[router] session.close_on_prompt_change.error task=%s err=%v", taskID, err)
		}
		return ""
	}
	if record.Status == RouterSessionClosed || record.Status == RouterSessionFailed {
		return ""
	}
	return record.SessionKey
}

func (rs *RouterService) recordSession(ctx context.Context, config RouterConfig, promptHash, taskID, rootID string, result RouterAgentResult, status, errText string) {
	sessionKey := strings.TrimSpace(result.SessionKey)
	if sessionKey == "" {
		return
	}
	if err := rs.store.UpsertSession(ctx, &RouterSessionRecord{
		Scope:      RouterScopeTask,
		OwnerID:    taskID,
		RootID:     rootID,
		SessionKey: sessionKey,
		Agent:      config.Agent,
		Model:      config.Model,
		Effort:     config.Effort,
		ConfigDir:  result.ConfigDir,
		Cwd:        result.Cwd,
		PromptHash: promptHash,
		Status:     status,
		LastError:  errText,
		LastTurnAt: time.Now().UTC(),
	}); err != nil {
		log.Printf("[router] session.upsert.error task=%s err=%v", taskID, err)
	}
}

func (rs *RouterService) closeSession(ctx context.Context, taskID, reason string) {
	if rs.store == nil {
		return
	}
	record, err := rs.store.Session(ctx, RouterScopeTask, taskID)
	if err != nil || record == nil {
		return
	}
	if agent := rs.agent(); agent != nil && record.SessionKey != "" {
		if err := agent.CloseRouterSession(ctx, record.RootID, record.SessionKey); err != nil {
			log.Printf("[router] session.close.error task=%s err=%v", taskID, err)
		}
	}
	if err := rs.store.UpdateSessionStatus(ctx, RouterScopeTask, taskID, RouterSessionClosed, reason); err != nil {
		log.Printf("[router] session.status.error task=%s err=%v", taskID, err)
	}
}

// sweepIdleSessions 关闭长时间没有回合的 Router runtime。只关进程内 runtime，
// 不删 manager 会话行：下次回合仍能 resume，转录也仍可读。
func (rs *RouterService) sweepIdleSessions(idleClose time.Duration) {
	interval := idleClose / 4
	if interval < 50*time.Millisecond {
		interval = 50 * time.Millisecond
	}
	if interval > time.Minute {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-rs.closed:
			return
		case <-ticker.C:
			rs.sweepIdleOnce(idleClose)
		}
	}
}

func (rs *RouterService) sweepIdleOnce(idleClose time.Duration) {
	if rs == nil || rs.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	records, err := rs.store.ListSessions(ctx, RouterScopeTask, rs.rootID)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	for _, record := range records {
		if record == nil || record.Status != RouterSessionActive || record.LastTurnAt.IsZero() {
			continue
		}
		if now.Sub(record.LastTurnAt) < idleClose {
			continue
		}
		rs.closeSession(ctx, record.OwnerID, "idle timeout")
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
