package kanban

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
)

// coordinate 模式的事件驱动层（Plan 2026-11-18，R1 T7）。
//
// route 模式只在用户留言进来时跑一轮；coordinate 在此基础上消费 T5/T6 的持久
// 事件账本：Run 结算/失败、HumanGate 等待、Run 停摆、启动对账都会推进一轮
// Router。循环的每次扫描必须是有界的——历史 Run 每 30s 全量重扫会把进程拖死，
// 所以候选集只含「窗口内更新」或「仍有 open NodeRun」的 Run。

const (
	// RouterCoordinateMaxTurnsPerUserActivity 是同一条原始留言最多能触发的
	// Router 回合数（含入口那一轮）。预算耗尽后事件直接 skipped，不调模型。
	RouterCoordinateMaxTurnsPerUserActivity = 3
	// routerEventMaxChainDepth 是留言链深度上限（入口留言 = 0）。
	routerEventMaxChainDepth       = 3
	routerCoordinateLoopInterval   = 30 * time.Second
	routerCoordinateScanWindow     = time.Hour
	routerCoordinateConsumeLimit   = 50
	routerCoordinateSQLTimeLayout  = "2006-01-02T15:04:05"
	routerCoordinateClaimPrefix    = "router-coordinator-"
	routerCoordinateTickTimeout    = 2 * time.Minute
	routerCoordinateRecoverTimeout = 5 * time.Second
	// routerEventClaimLease 是事件领取租约：超过它就认为领取者已死/已丢回合。
	// 取回合超时（90s）的若干倍：正常回合一定在租约内收尾，误判只会让一个
	// 还在跑的回合与被打回的事件并存（ClaimEvent 是 CAS，第二个回合拿不到），
	// 不会重复产生副作用（运行期 effect ID 是确定的）。
	routerEventClaimLease = 10 * time.Minute
	// routerEventRetention 是账本保留期：终态行留 7 天够查「上周那次结算」，
	// 再老就没有人读了。pending/claimed 不受它影响（见 PruneEvents）。
	routerEventRetention = 7 * 24 * time.Hour
	// routerEventPruneEveryTicks 是回收频率（以拍计）。一拍 30s，120 拍即约
	// 1 小时一次 —— 回收是个后台杂活，没必要每拍打一条 DELETE。
	routerEventPruneEveryTicks = 120
)

// RouterCoordinateConsumeResult 是一拍的消费统计，供测试与诊断。
type RouterCoordinateConsumeResult struct {
	Enqueued int
	Skipped  int
}

// routerTriggerForEvent 是事件 → 触发的唯一映射。第二个返回值为 "" 表示事件
// 类型未知（账本入口会拒绝，防御性跳过，避免永久 pending）。
func routerTriggerForEvent(ev RouterEvent) (RouterTrigger, RouterTriggerKind) {
	var kind RouterTriggerKind
	switch ev.Kind {
	case RouterEventRunSettled:
		kind = RouterTriggerRunSettled
	case RouterEventRunFailed:
		kind = RouterTriggerRunFailed
	case RouterEventGateWaiting:
		kind = RouterTriggerGateWaiting
	case RouterEventRunStalled:
		kind = RouterTriggerRunStalled
	case RouterEventStartupReconcile:
		kind = RouterTriggerStartup
	case RouterEventDirectStalled:
		kind = RouterTriggerDirectStalled
	case RouterEventCompletionBlocked:
		kind = RouterTriggerCompletionBlocked
	case RouterEventTurnSettled:
		kind = RouterTriggerTurnSettled
	case RouterEventTurnStalled:
		kind = RouterTriggerTurnStalled
	case RouterEventTurnInterrupted:
		kind = RouterTriggerTurnInterrupted
	case RouterEventTaskOrphaned:
		kind = RouterTriggerTaskOrphaned
	default:
		return RouterTrigger{}, ""
	}
	triggerRef := strings.TrimSpace(ev.RunID)
	if triggerRef == "" {
		// 直连事件没有 Run：指向卡住的那条回复行（completion_blocked 则指向任务），
		// 便于日志/审计对齐。
		triggerRef = routerEventPayloadString(ev.PayloadJSON, "activity_id")
	}
	if triggerRef == "" && kind == RouterTriggerCompletionBlocked {
		triggerRef = strings.TrimSpace(ev.TaskID)
	}
	if triggerRef == "" && kind == RouterTriggerTurnSettled {
		// 会话回合没有 Run、也没有卡住的回复行：指向回合 id，便于日志对齐。
		triggerRef = strings.TrimSpace(ev.TurnID)
	}
	if triggerRef == "" && kind == RouterTriggerTaskOrphaned {
		// 孤儿任务连回复行都没有：指向任务本身。
		triggerRef = strings.TrimSpace(ev.TaskID)
	}
	event := ev
	return RouterTrigger{
		Kind:           kind,
		TaskID:         strings.TrimSpace(ev.TaskID),
		RootID:         strings.TrimSpace(ev.RootID),
		UserActivityID: strings.TrimSpace(ev.UserActivityID),
		TriggerRef:     triggerRef,
		TurnID:         strings.TrimSpace(ev.TurnID),
		Mode:           RouterModeCoordinate,
		Event:          &event,
	}, kind
}

// ConsumeRouterEvents 取一批 pending 事件并按任务入队。claim 在回合内做：
// 同一事件被多拍重复 enqueue 必须无害（CAS 只允许一个回合继续）。
func (rs *RouterService) ConsumeRouterEvents(ctx context.Context, now time.Time) (RouterCoordinateConsumeResult, error) {
	result := RouterCoordinateConsumeResult{}
	if rs == nil || rs.store == nil || rs.service == nil {
		return result, nil
	}
	// 非 coordinate 模式零读写：事件保持 pending，等模式切换后再处理。
	if rs.service.RouterConfig().ModeForRoot(rs.rootID) != RouterModeCoordinate {
		return result, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	_ = now // 保留注入点：消费本身不按时间过滤，窗口只属于扫描。
	events, err := rs.store.ListPendingEvents(ctx, routerCoordinateConsumeLimit)
	if err != nil {
		return result, err
	}
	for _, ev := range events {
		trigger, kind := routerTriggerForEvent(ev)
		reason := ""
		switch {
		case kind == "":
			reason = "unknown router event kind " + string(ev.Kind)
		case trigger.TaskID == "":
			reason = "missing task id"
		case trigger.TurnID == "":
			reason = "missing deterministic turn id"
		}
		if reason != "" {
			rs.skipCoordinatedEvent(ctx, ev.ID, reason)
			result.Skipped++
			continue
		}
		rs.Enqueue(trigger)
		result.Enqueued++
	}
	return result, nil
}

// skipCoordinatedEvent 领取后直接跳过：只有 claimed 行才能落终态。
func (rs *RouterService) skipCoordinatedEvent(ctx context.Context, eventID, reason string) {
	claimed, err := rs.store.ClaimEvent(ctx, eventID, rs.claimerID)
	if err != nil {
		log.Printf("[router/coordinate] skip.claim.error event=%s err=%v", eventID, err)
		return
	}
	if !claimed {
		return
	}
	if err := rs.store.CompleteEvent(ctx, eventID, RouterEventSkipped, reason); err != nil {
		log.Printf("[router/coordinate] skip.complete.error event=%s err=%v", eventID, err)
	}
}

// startCoordinate 装配 coordinate 循环：领取者标识（含 pid，跨进程可辨认）、
// 上次进程遗留的 claimed 回收、立即跑一拍。仅由 newRouterService 在 coordinate
// 模式下调用一次。
func (rs *RouterService) startCoordinate() {
	if rs == nil || rs.store == nil {
		return
	}
	rs.coordinateMu.Lock()
	if rs.coordinateRunning {
		rs.coordinateMu.Unlock()
		return
	}
	rs.coordinateRunning = true
	rs.coordinateDone = make(chan struct{})
	done := rs.coordinateDone
	rs.coordinateMu.Unlock()
	rs.claimerID = fmt.Sprintf("%s%s-%d", routerCoordinateClaimPrefix, rs.rootID, os.Getpid())
	ctx, cancel := context.WithTimeout(context.Background(), routerCoordinateRecoverTimeout)
	if n, err := rs.store.RecoverClaimedEvents(ctx, "router event claim interrupted by process restart"); err != nil {
		log.Printf("[router/coordinate] recover.error root=%s err=%v", rs.rootID, err)
	} else if n > 0 {
		log.Printf("[router/coordinate] recover root=%s count=%d", rs.rootID, n)
	}
	cancel()
	go rs.coordinateLoop(done)
}

// coordinateLoopActive 仅供测试断言：循环在跑时为 true，Close 后收敛为 false。
func (rs *RouterService) coordinateLoopActive() bool {
	if rs == nil {
		return false
	}
	rs.coordinateMu.Lock()
	defer rs.coordinateMu.Unlock()
	return rs.coordinateRunning
}

func (rs *RouterService) coordinateLoop(done chan struct{}) {
	defer func() {
		rs.coordinateMu.Lock()
		rs.coordinateRunning = false
		rs.coordinateMu.Unlock()
		close(done)
	}()
	rs.coordinateTick()
	ticker := time.NewTicker(routerCoordinateLoopInterval)
	defer ticker.Stop()
	for {
		select {
		case <-rs.closed:
			return
		case <-ticker.C:
			rs.coordinateTick()
		}
	}
}

// coordinateTick 是一拍：先收回过期领取，再补事件（有界扫描），最后消费。
// 各步失败只记日志，不能让循环退出——下一拍会重试，事件账本保证幂等。
func (rs *RouterService) coordinateTick() {
	ctx, cancel := context.WithTimeout(context.Background(), routerCoordinateTickTimeout)
	defer cancel()
	now := time.Now().UTC()
	rs.reclaimStaleEventClaims(ctx, now)
	rs.requeueFailedSettleEvents(ctx, now)
	rs.releaseDueQuotaHolds(ctx, now)
	rs.pruneExpiredEvents(ctx, now)
	if _, err := rs.service.scanRouterEventSourcesBounded(ctx, rs.rootID, now, routerCoordinateScanWindow); err != nil {
		log.Printf("[router/coordinate] scan.error root=%s err=%v", rs.rootID, err)
	}
	// 任务级巡检（W3-2）：事件扫描只看 Run 与直连回复，看不见「回合在飞但没有
	// 产出」和「running 的空壳任务」。它只写事件（waiting_busy 例外：按会话存活把
	// 已落到待处理的任务头纠回 running），动作仍由消费侧决定。
	if produced, err := rs.service.taskHealthSweep(ctx, rs.rootID, now); err != nil {
		log.Printf("[router/coordinate] health.error root=%s err=%v", rs.rootID, err)
	} else if len(produced) > 0 {
		log.Printf("[router/coordinate] health.produced root=%s count=%d", rs.rootID, len(produced))
	}
	if _, err := rs.ConsumeRouterEvents(ctx, now); err != nil {
		log.Printf("[router/coordinate] consume.error root=%s err=%v", rs.rootID, err)
	}
}

// releaseDueQuotaHolds 唤醒到期的配额挂起。撞用量上限的节点不立刻重试，而是挂到
// 重置时刻；这一拍把它们放回可运行并唤醒所属 Run。失败只记日志，下一拍再看。
func (rs *RouterService) releaseDueQuotaHolds(ctx context.Context, now time.Time) {
	if rs == nil || rs.service == nil {
		return
	}
	store, err := rs.service.taskStore(rs.rootID)
	if err != nil {
		log.Printf("[router/coordinate] quota_hold.store root=%s err=%v", rs.rootID, err)
		return
	}
	runs, err := rs.service.releaseDueNodeHolds(ctx, store, now)
	if err != nil {
		log.Printf("[router/coordinate] quota_hold.release_error root=%s err=%v", rs.rootID, err)
		return
	}
	for _, runID := range runs {
		run, err := store.WorkflowRunByID(ctx, runID)
		if err != nil || run == nil {
			log.Printf("[router/coordinate] quota_hold.run_lookup root=%s run=%s err=%v", rs.rootID, runID, err)
			continue
		}
		if err := rs.service.WakeWorkflowRun(ctx, rs.rootID, run.TaskID, "quota_hold_released"); err != nil {
			log.Printf("[router/coordinate] quota_hold.wake root=%s task=%s err=%v", rs.rootID, run.TaskID, err)
			continue
		}
		log.Printf("[router/coordinate] quota_hold.released root=%s task=%s run=%s", rs.rootID, run.TaskID, runID)
	}
}

// reclaimStaleEventClaims 把领了太久的事件打回 pending。启动对账只在进程重启
// 时跑；进程不死、某回合又把事件丢在 claimed（panic 被吞 / 提前 return），
// 这条事件就再也不会被消费，账本上既非 pending 也非终态。
func (rs *RouterService) reclaimStaleEventClaims(ctx context.Context, now time.Time) {
	if rs == nil || rs.store == nil {
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	n, err := rs.store.ReclaimStaleEvents(ctx, now.Add(-routerEventClaimLease), "claim lease expired")
	if err != nil {
		log.Printf("[router/coordinate] reclaim.error root=%s err=%v", rs.rootID, err)
		return
	}
	if n > 0 {
		log.Printf("[router/coordinate] reclaim root=%s count=%d", rs.rootID, n)
	}
}

// pruneExpiredEvents 低频回收账本。只在 coordinate 循环的 goroutine 里被调用
// （测试里先 stopLoop 再由测试独占），所以拍数用普通计数器即可。
//
// 第 0 拍（启动）与之后每 routerEventPruneEveryTicks 拍各跑一次：账本回收是
// 后台杂活，每拍打一条 DELETE 只会白白争 SQLite 写锁。
func (rs *RouterService) pruneExpiredEvents(ctx context.Context, now time.Time) {
	if rs == nil || rs.store == nil {
		return
	}
	tick := rs.coordinateTicks
	rs.coordinateTicks++
	if tick%routerEventPruneEveryTicks != 0 {
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	n, err := rs.store.PruneEvents(ctx, now.Add(-routerEventRetention), routerEventPruneLimit)
	if err != nil {
		log.Printf("[router/coordinate] prune.error root=%s err=%v", rs.rootID, err)
		return
	}
	if n > 0 {
		log.Printf("[router/coordinate] prune root=%s count=%d", rs.rootID, n)
	}
}

// waitCoordinateLoop 让 Close 等循环退出（有界）：测试收尾时干净关库，生产
// 停机也不无限等一拍。
func (rs *RouterService) waitCoordinateLoop(timeout time.Duration) {
	if rs == nil {
		return
	}
	rs.coordinateMu.Lock()
	done := rs.coordinateDone
	rs.coordinateMu.Unlock()
	if done == nil {
		return
	}
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

// scanRouterEventSourcesBounded 是 T6 全量扫描的有界替代入口：候选 Run 只含
// 窗口内更新过的、或仍有 open NodeRun 的。逐 Run 复用 T6 的 ownership 判定与
// 事件判定，绝不在这里另写一套口径。
//
// 取舍：钩子没写、又早于窗口的终态 Run 不会被补齐。钩子是主路径，扫描只是
// 兜底；1 小时窗口覆盖「进程死在结算窗口内」的现场，足够启动对账再用 T6 的
// 全量扫描补历史。
func (s *Service) scanRouterEventSourcesBounded(ctx context.Context, rootID string, now time.Time, window time.Duration) (RouterEventScanResult, error) {
	result := RouterEventScanResult{Produced: []RouterEvent{}}
	if s == nil {
		return result, errors.New("kanban service not configured")
	}
	rootID = strings.TrimSpace(rootID)
	if rootID == "" {
		return result, errors.New("root id required")
	}
	if window <= 0 {
		window = routerCoordinateScanWindow
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	store, err := s.taskStore(rootID)
	if err != nil {
		return result, err
	}
	// substr(...,1,19) 抹掉 RFC3339Nano 的小数位：字符串比较在整秒边界上必须
	// 同精度，否则 '...:00.5Z' < '...:00Z' 这类比较会把候选排错序。
	cutoff := now.Add(-window).UTC().Format(routerCoordinateSQLTimeLayout)
	// open = 还没进终态：queued/running/waiting_user 的节点都可能继续推进，
	// 所以它的 Run 必须留在候选集里、不按 updated_at 丢弃。
	rows, err := store.db.QueryContext(ctx, `
SELECT r.id FROM workflow_runs r JOIN tasks t ON t.id = r.task_id
WHERE t.root_id = ?
  AND (substr(r.updated_at, 1, 19) >= ?
       OR EXISTS (SELECT 1 FROM workflow_node_runs n WHERE n.run_id = r.id AND n.status IN (?, ?, ?)))
ORDER BY r.created_at ASC, r.id ASC`,
		rootID, cutoff, NodeRunQueued, NodeRunRunning, NodeRunWaitingUser)
	if err != nil {
		return result, err
	}
	var runIDs []string
	for rows.Next() {
		var runID string
		if scanErr := rows.Scan(&runID); scanErr != nil {
			rows.Close()
			return result, scanErr
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
	// 直连回合没有 Run，不在上面的候选集里：单独扫一遍，仍共用同一张观测表，
	// 所以必须在 prune 之前。
	produced, notes, err := s.scanDirectStalledEvents(ctx, store, now, running)
	if err != nil {
		return result, err
	}
	result.Produced = append(result.Produced, produced...)
	result.Notes = append(result.Notes, notes...)
	s.pruneRouterStallObservations(running)
	return result, nil
}

// recoverInterruptedUserMessageDecisions 是 coordinate 专用的启动回收：只把
// 用户留言的 applying 决策标 failed。事件决策的 applying 代表「副作用可能已
// 发生、事件还没落终态」，必须留给 replayCoordinateEventDecision 重新应用，
// 否则崩溃恢复会把已经可见的回复标成失败。
func (s *RouterStore) recoverInterruptedUserMessageDecisions(ctx context.Context, reason string) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("router store requires a database")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "router apply interrupted by process restart"
	}
	res, err := s.db.ExecContext(ctx, `
UPDATE router_decisions SET status = ?, error = ?, updated_at = ?
WHERE status = ? AND trigger_kind = ?`,
		string(RouterDecisionFailed), reason, time.Now().UTC().Format(routerTimeLayout),
		string(RouterDecisionApplying), string(RouterTriggerUserMessage))
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return n, nil
}
