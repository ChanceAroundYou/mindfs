package kanban

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"
)

// Router 生命周期事件的生产端（Plan 2026-11-18，R1 T6）。事件只来自权威持久化
// 状态：结算/失败/等待/停摆都能被扫描从 Task/Run/NodeRun 重新算出来，所以进程
// 死在「状态已落库、事件未写」的窗口里也不会漏事件；内存回调只是加速器。

const (
	// routerEventStallMinRuntime 是「运行中且已启动 ≥ 该时长」的门槛；正常长回合
	// 不按耗时判失败，它只是必要条件。
	routerEventStallMinRuntime = 60 * time.Second
	// routerEventStallConfirmGap 是两次 inactive 观测的最小间隔（生产扫描 30s 一轮）。
	routerEventStallConfirmGap = 30 * time.Second
	// routerEventNodesLimit 限制 payload 里的节点数，保证单条事件有界。
	routerEventNodesLimit = 64
	// routerEventChainWalkLimit 限制留言链回溯深度。
	routerEventChainWalkLimit = 32
)

// RouterEventScanResult 是扫描产出：Produced 只含本次新建（被去重的不算），
// Notes 记录被跳过的 run 及原因。
type RouterEventScanResult struct {
	Produced []RouterEvent
	Notes    []string
}

// routerEventOrigin 是 Router-owned 判定结果；不满足时 owned=false，reason 说明原因。
type routerEventOrigin struct {
	owned          bool
	userActivityID string
	decisionID     string
	reason         string
}

// routerEventStallObservations 记录「上次看到该 NodeRun 会话 inactive」的时间。
// 它是探针的实时观测而非权威状态，写进任何表都会违反「扫描不得改权威状态」；
// Service 结构体又不在本任务可改文件里，所以按 Service 实例分组放进程内。不跨
// 进程宣称 durable：重启后需重新观测两轮，方向保守。
var routerEventStallObservations = struct {
	sync.Mutex
	seen map[*Service]map[string]time.Time
}{seen: map[*Service]map[string]time.Time{}}

// ScanRouterEventSources 以权威持久化状态为唯一事实来源：遍历该 root 下的 Run，
// 判定终态、gate waiting、stall 并幂等 InsertEvent；不读内存回调、不推进游标、
// 不改 Task/Run/NodeRun/Activity。now 由调用方注入（测试假时钟 / T7 每 30s）。
// 同一 now 区间重复调用只产生首次那一组事件。
func (s *Service) ScanRouterEventSources(ctx context.Context, rootID string, now time.Time) (RouterEventScanResult, error) {
	result := RouterEventScanResult{Produced: []RouterEvent{}}
	if s == nil {
		return result, errors.New("kanban service not configured")
	}
	rootID = strings.TrimSpace(rootID)
	if rootID == "" {
		return result, errors.New("root id required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	store, err := s.taskStore(rootID)
	if err != nil {
		return result, err
	}
	// legacy_import Run 也取出来，但只记 Note 不判定：Notes 需要说明它们为什么
	// 不产事件，而 SQL 直接过滤掉会让「跳过」变成「不存在」。
	rows, err := store.db.QueryContext(ctx, `
SELECT r.id FROM workflow_runs r JOIN tasks t ON t.id = r.task_id
WHERE t.root_id = ? ORDER BY r.created_at ASC, r.id ASC`, rootID)
	if err != nil {
		return result, err
	}
	var runIDs []string
	for rows.Next() {
		var runID string
		if err := rows.Scan(&runID); err != nil {
			rows.Close()
			return result, err
		}
		runIDs = append(runIDs, runID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return result, err
	}

	running := map[string]bool{}
	for _, runID := range runIDs {
		run, err := store.WorkflowRunByID(ctx, runID)
		if err != nil {
			return result, err
		}
		task, err := store.GetTask(ctx, run.TaskID)
		if err != nil {
			return result, err
		}
		origin, err := s.routerEventOriginForRun(ctx, store, run)
		if err != nil {
			return result, err
		}
		if !origin.owned {
			result.Notes = append(result.Notes, fmt.Sprintf("run %s (task %s): skipped, %s", run.ID, run.TaskID, origin.reason))
			continue
		}
		produced, notes, err := s.scanRouterEventsForRun(ctx, store, task, run, origin, now, running)
		if err != nil {
			return result, err
		}
		result.Produced = append(result.Produced, produced...)
		result.Notes = append(result.Notes, notes...)
	}
	s.pruneRouterStallObservations(running)
	return result, nil
}

func (s *Service) scanRouterEventsForRun(ctx context.Context, store *TaskStore, task Task, run *WorkflowRun, origin routerEventOrigin, now time.Time, running map[string]bool) ([]RouterEvent, []string, error) {
	var produced []RouterEvent
	appendInserted := func(ev RouterEvent, inserted bool) {
		if inserted {
			produced = append(produced, ev)
		}
	}

	projected := ProjectStatusFromRun(run)
	kind := s.routerEventTerminalKind(ctx, store, task, run)
	if kind != "" {
		instanceKey, err := RouterEventInstanceKey(kind, run.ID, "", 0, now)
		if err != nil {
			return nil, nil, err
		}
		ev, inserted, err := s.insertRouterEventForRun(ctx, store, task, run, origin, kind, instanceKey, 0, nil, "", now)
		if err != nil {
			return nil, nil, err
		}
		appendInserted(ev, inserted)
	}

	if kind != "" || projected == StatusCancelled || task.Status == StatusCancelled {
		// 已结算/失败/取消的 Run 不再判 gate/stall；cancelled 不产任何事件。
		return produced, nil, nil
	}

	nodeRuns, err := store.ListWorkflowNodeRuns(ctx, run.ID)
	if err != nil {
		return nil, nil, err
	}
	for _, nr := range nodeRuns {
		switch nr.Status {
		case NodeRunWaitingUser:
			node, ok := workflowNodeByID(run.Graph, nr.NodeID)
			if !ok || node.Kind != NodeKindHumanGate {
				continue
			}
			instanceKey, err := RouterEventInstanceKey(RouterEventGateWaiting, run.ID, nr.ID, nr.Attempt, now)
			if err != nil {
				return nil, nil, err
			}
			extra := map[string]any{"node_id": node.ID, "node_run_id": nr.ID, "attempt": nr.Attempt}
			ev, inserted, err := s.insertRouterEventForRun(ctx, store, task, run, origin, RouterEventGateWaiting, instanceKey, nr.Attempt, extra, "", now)
			if err != nil {
				return nil, nil, err
			}
			appendInserted(ev, inserted)
		case NodeRunRunning:
			running[nr.ID] = true
			if !s.sessionProbeConfigured() {
				continue
			}
			if strings.TrimSpace(nr.SessionKey) == "" || nr.StartedAt.IsZero() {
				// 没有会话或没有启动时间：不是「明确证据」，绝不宣告停摆。
				continue
			}
			if s.sessionRunning(nr.SessionKey) {
				s.clearRouterStallObservation(nr.ID)
				continue
			}
			if now.Sub(nr.StartedAt) < routerEventStallMinRuntime {
				continue
			}
			if !s.confirmRouterStallObservation(nr.ID, now) {
				continue
			}
			instanceKey, err := RouterEventInstanceKey(RouterEventRunStalled, run.ID, nr.ID, nr.Attempt, now)
			if err != nil {
				return nil, nil, err
			}
			extra := map[string]any{"node_id": nr.NodeID, "node_run_id": nr.ID, "attempt": nr.Attempt, "started_at": nr.StartedAt.UTC().Format(routerTimeLayout)}
			ev, inserted, err := s.insertRouterEventForRun(ctx, store, task, run, origin, RouterEventRunStalled, instanceKey, nr.Attempt, extra, "", now)
			if err != nil {
				return nil, nil, err
			}
			appendInserted(ev, inserted)
		}
	}
	return produced, nil, nil
}

// routerEventTerminalKind 判定 Run 是否已进入可汇报终态。Run 投影是首选事实；
// 任务状态只在「该 Run 仍是任务当前绑定 Run」时补充解释操作员 Complete/Fail
// 的终态（那条路径不重写投影）。旧 Run 只按自己的投影判定，不借用新回合状态。
func (s *Service) routerEventTerminalKind(ctx context.Context, store *TaskStore, task Task, run *WorkflowRun) RouterEventKind {
	switch ProjectStatusFromRun(run) {
	case StatusSuccess:
		return RouterEventRunSettled
	case StatusFail:
		return RouterEventRunFailed
	case StatusCancelled:
		return ""
	}
	if task.Status == StatusCancelled {
		return ""
	}
	engine, err := taskEngineOf(ctx, store.db, run.TaskID)
	if err != nil || engine.Engine != EngineWorkflow || engine.WorkflowRunID != run.ID {
		return ""
	}
	switch task.Status {
	case StatusSuccess:
		return RouterEventRunSettled
	case StatusFail:
		return RouterEventRunFailed
	}
	return ""
}

// routerEventOriginForRun 是 Router-owned 判据的唯一口径：非 legacy_import，
// 且存在一条绑定该 Run、带 router_decision_id 的活动（Router 的 run_workflow）。
func (s *Service) routerEventOriginForRun(ctx context.Context, store *TaskStore, run *WorkflowRun) (routerEventOrigin, error) {
	if run.TriggeredBy == WorkflowTriggeredByLegacyImport {
		return routerEventOrigin{reason: "triggered_by=legacy_import"}, nil
	}
	var replyToID, decisionID string
	err := store.db.QueryRowContext(ctx, `
SELECT reply_to_id, router_decision_id FROM task_activities
WHERE task_id = ? AND run_id = ? AND router_decision_id <> ''
ORDER BY created_at ASC, id ASC LIMIT 1`, run.TaskID, run.ID).Scan(&replyToID, &decisionID)
	if errors.Is(err, sql.ErrNoRows) {
		return routerEventOrigin{reason: "no router decision owns this run (explicit dispatch)"}, nil
	}
	if err != nil {
		return routerEventOrigin{}, err
	}
	userActivityID := strings.TrimSpace(run.ActivityID)
	if userActivityID == "" {
		userActivityID = strings.TrimSpace(replyToID)
	}
	return routerEventOrigin{owned: true, userActivityID: userActivityID, decisionID: decisionID}, nil
}

// insertRouterEventForRun 组装并插入一条事件。inserted=false 是正常去重。
func (s *Service) insertRouterEventForRun(ctx context.Context, store *TaskStore, task Task, run *WorkflowRun, origin routerEventOrigin, kind RouterEventKind, instanceKey string, attempt int, extra map[string]any, turnID string, now time.Time) (RouterEvent, bool, error) {
	payload := s.routerEventPayload(ctx, run)
	for key, value := range extra {
		payload[key] = value
	}
	// S0 主动监管：失败/停摆事件额外带一份水位观测（模式、open/failed 节点、预算余额）。
	// off 时完全不改 payload，与既有事件逐字节一致。
	if kind == RouterEventRunFailed || kind == RouterEventRunStalled {
		if _, exists := payload["supervision"]; !exists {
			if obs := s.supervisionObservation(ctx, store, task, run, now); obs != nil {
				payload["supervision"] = obs
			}
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return RouterEvent{}, false, fmt.Errorf("marshal router event %s payload: %w", kind, err)
	}
	if len(raw) > RouterEventMaxPayloadBytes {
		return RouterEvent{}, false, fmt.Errorf("router event %s payload %d bytes exceeds %d", kind, len(raw), RouterEventMaxPayloadBytes)
	}
	if strings.TrimSpace(turnID) == "" {
		turnID = routerEventTurnID(run.ID, kind, instanceKey)
	}
	ev := RouterEvent{
		RootID:           strings.TrimSpace(task.RootID),
		TaskID:           run.TaskID,
		RunID:            run.ID,
		Kind:             kind,
		InstanceKey:      instanceKey,
		UserActivityID:   origin.userActivityID,
		TurnID:           turnID,
		ChainDepth:       s.routerEventChainDepth(ctx, store, origin.userActivityID),
		SourceDecisionID: origin.decisionID,
		Attempt:          attempt,
		PayloadJSON:      string(raw),
		CreatedAt:        now.UTC(),
	}
	return NewRouterStore(store.db).InsertEvent(ctx, ev)
}

// emitRouterEvent 是生命周期钩子的薄封装：判定该 Run 是否 Router-owned，
// 不满足就静默返回（不报错、不写事件）。结算窗口内的钩子也走它，保证与扫描
// 完全同一套判据。
func (s *Service) emitRouterEvent(ctx context.Context, store *TaskStore, runID string, kind RouterEventKind, instanceKey, userActivityID, turnID, sourceDecisionID string, attempt int, payload any) error {
	if s == nil || store == nil {
		return errors.New("kanban service and task store required")
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return errors.New("router event run id required")
	}
	if !knownRouterEventKind(kind) {
		return fmt.Errorf("unknown router event kind %q", kind)
	}
	run, err := store.WorkflowRunByID(ctx, runID)
	if err != nil {
		return err
	}
	task, err := store.GetTask(ctx, run.TaskID)
	if err != nil {
		return err
	}
	origin, err := s.routerEventOriginForRun(ctx, store, run)
	if err != nil {
		return err
	}
	if !origin.owned {
		return nil
	}
	if value := strings.TrimSpace(userActivityID); value != "" {
		origin.userActivityID = value
	}
	if value := strings.TrimSpace(sourceDecisionID); value != "" {
		origin.decisionID = value
	}
	extra := map[string]any{}
	if payload != nil {
		fields, ok := payload.(map[string]any)
		if !ok {
			return fmt.Errorf("router event %s payload must be a map", kind)
		}
		extra = fields
	}
	now := time.Now().UTC()
	instanceKey = strings.TrimSpace(instanceKey)
	if instanceKey == "" {
		instanceKey, err = RouterEventInstanceKey(kind, run.ID, "", attempt, now)
		if err != nil {
			return err
		}
	}
	_, _, err = s.insertRouterEventForRun(ctx, store, task, run, origin, kind, instanceKey, attempt, extra, turnID, now)
	return err
}

// emitRunTerminalRouterEvent 把 Run 的终态翻译成 settled/failed；cancelled 不产
// 事件（取消是操作员决定）。判据与扫描共用 routerEventTerminalKind，避免「终态
// 来自任务行」的两条路径给出不同结论。
func (s *Service) emitRunTerminalRouterEvent(ctx context.Context, store *TaskStore, run *WorkflowRun) {
	if s == nil || store == nil || run == nil {
		return
	}
	task, err := store.GetTask(ctx, run.TaskID)
	if err != nil {
		return
	}
	kind := s.routerEventTerminalKind(ctx, store, task, run)
	if kind == "" {
		return
	}
	if err := s.emitRouterEvent(ctx, store, run.ID, kind, "", "", "", "", 0, nil); err != nil {
		log.Printf("[kanban/router-events] terminal hook run=%s kind=%s err=%v", run.ID, kind, err)
	}
}

// emitOperatorOutcomeRouterEvent 是操作员 Complete/Fail 的钩子：这条路径不重写
// Run 投影，按操作员给出的任务终态决定类型。写事件失败只记日志，不回滚决定。
func (s *Service) emitOperatorOutcomeRouterEvent(ctx context.Context, store *TaskStore, runID, status string) {
	kind := RouterEventKind("")
	switch status {
	case StatusSuccess:
		kind = RouterEventRunSettled
	case StatusFail:
		kind = RouterEventRunFailed
	default:
		return
	}
	if err := s.emitRouterEvent(ctx, store, runID, kind, "", "", "", "", 0, nil); err != nil {
		log.Printf("[kanban/router-events] operator hook run=%s status=%s err=%v", runID, status, err)
	}
}

// routerEventPayload 构造有界摘要：只含 id、节点状态、版本与 revision；禁止
// NodeRun 输出、会话内容与 git 字段进入 payload。
func (s *Service) routerEventPayload(ctx context.Context, run *WorkflowRun) map[string]any {
	nodes := map[string]string{}
	ids := make([]string, 0, len(run.NodeStates))
	for id := range run.NodeStates {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	truncated := false
	for _, id := range ids {
		switch status := run.NodeStates[id]; status {
		case NodeRunSuccess, NodeRunFail, NodeRunCancelled, NodeRunSkipped, NodeRunRunning:
			if len(nodes) >= routerEventNodesLimit {
				truncated = true
				continue
			}
			nodes[id] = status
		}
	}
	workflowID := strings.TrimSpace(run.Version)
	if s != nil && s.Workflows != nil && workflowID != "" {
		if version, err := s.Workflows.GetVersion(ctx, workflowID); err == nil && strings.TrimSpace(version.WorkflowID) != "" {
			workflowID = strings.TrimSpace(version.WorkflowID)
		}
	}
	payload := map[string]any{
		"run_id":      run.ID,
		"task_id":     run.TaskID,
		"nodes":       nodes,
		"workflow_id": workflowID,
		"revision":    run.Revision,
	}
	if truncated {
		payload["nodes_truncated"] = true
	}
	return payload
}

// routerEventTurnID 由 (runID, kind, instanceKey) 稳定派生：重扫与钩子不会因为
// newID 把同一实例分裂成两个回合。
func routerEventTurnID(runID string, kind RouterEventKind, instanceKey string) string {
	sum := sha256.Sum256([]byte(runID + "\x00" + string(kind) + "\x00" + instanceKey))
	return "revt-turn:" + hex.EncodeToString(sum[:8])
}

// routerEventChainDepth 沿 ReplyToID 回溯原始留言链，深度有界。入口留言 = 0。
func (s *Service) routerEventChainDepth(ctx context.Context, store *TaskStore, userActivityID string) int {
	id := strings.TrimSpace(userActivityID)
	if store == nil || id == "" {
		return 0
	}
	activities := NewActivityStore(store.db)
	depth := 0
	for hop := 0; hop < routerEventChainWalkLimit; hop++ {
		activity, err := activities.Get(ctx, id)
		if err != nil || strings.TrimSpace(activity.ReplyToID) == "" {
			break
		}
		depth++
		id = activity.ReplyToID
	}
	return depth
}

// confirmRouterStallObservation 记录一次 inactive 观测；与上一次相隔 ≥30s 时返回
// true 并开启新的观测窗口。同一 now 重复调用仍返回 false（幂等）。
func (s *Service) confirmRouterStallObservation(nodeRunID string, now time.Time) bool {
	routerEventStallObservations.Lock()
	defer routerEventStallObservations.Unlock()
	perService := routerEventStallObservations.seen[s]
	if perService == nil {
		perService = map[string]time.Time{}
		routerEventStallObservations.seen[s] = perService
	}
	first, ok := perService[nodeRunID]
	if ok && now.Sub(first) >= routerEventStallConfirmGap {
		perService[nodeRunID] = now
		return true
	}
	if !ok {
		perService[nodeRunID] = now
	}
	return false
}

func (s *Service) clearRouterStallObservation(nodeRunID string) {
	routerEventStallObservations.Lock()
	defer routerEventStallObservations.Unlock()
	if perService := routerEventStallObservations.seen[s]; perService != nil {
		delete(perService, nodeRunID)
	}
}

// pruneRouterStallObservations 丢掉已不在运行的 NodeRun 的观测，避免登记表随
// 历史节点无限增长。
func (s *Service) pruneRouterStallObservations(running map[string]bool) {
	routerEventStallObservations.Lock()
	defer routerEventStallObservations.Unlock()
	perService := routerEventStallObservations.seen[s]
	if perService == nil {
		return
	}
	for nodeRunID := range perService {
		if !running[nodeRunID] {
			delete(perService, nodeRunID)
		}
	}
	if len(perService) == 0 {
		delete(routerEventStallObservations.seen, s)
	}
}
