package kanban

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// 直连回合的停摆事件（S3-3）。
//
// 直连回合（纯对话 / Router message_agent）没有 WorkflowRun：收尾写在跑它的
// goroutine 里，回合一旦被吞掉，回复行就永远停在 running，而任务主会话其实早已
// 空闲。权威证据只有两样——回复行状态与主会话活跃位——所以这里跟 run_stalled
// 用同一套保守判据：会话探针必须装配、创建满 60s、相隔 30s 两次观测都空闲。
//
// 与启动对账（settleOrphanDirectReplies）的分工：那条路只在启动时结算「重启带
// 走的回合」；这里只在运行期把「活着的进程丢了回合」变成一条可监管的事件，
// 结算由 Router 的 retry_direct 动作完成。

// routerEventDirectScanLimit 限制单次扫描产出的直连事件数：直连回复可能积压很
// 多历史行，扫描不能无界。
const routerEventDirectScanLimit = 64

// RouterEventDirectInstanceKey 是 direct_stalled 的实例键。直连回合没有 Run，
// 去重按「卡住的回复活动 + 同一个 5 分钟桶」。
func RouterEventDirectInstanceKey(activityID string, at time.Time) (string, error) {
	activityID = strings.TrimSpace(activityID)
	if activityID == "" {
		return "", errors.New("router event direct_stalled requires an activity id")
	}
	if at.IsZero() {
		return "", errors.New("router event direct_stalled requires a timestamp")
	}
	bucket := at.UTC().Unix() / int64(routerEventStallBucket/time.Second)
	return activityID + ":" + strconv.FormatInt(bucket, 10), nil
}

// scanDirectStalledEvents 把「run_id 为空、停在 running、主会话已空闲」的直连
// 回复变成 direct_stalled 事件。它只读库与探针，不结算任何行。
func (s *Service) scanDirectStalledEvents(ctx context.Context, store *TaskStore, now time.Time, running map[string]bool) ([]RouterEvent, []string, error) {
	if s == nil || store == nil {
		return nil, nil, errors.New("kanban service and task store required")
	}
	// 没有会话探针就无法证明回合真的停了 —— 宁可不报，也不误杀一轮活着的回合。
	if !s.sessionProbeConfigured() {
		return nil, nil, nil
	}
	activities := NewActivityStore(store.db)
	replies, err := activities.ListOrphanDirectReplies(ctx)
	if err != nil {
		return nil, nil, err
	}
	var produced []RouterEvent
	var notes []string
	tasks := map[string]Task{}
	for _, reply := range replies {
		if len(produced) >= routerEventDirectScanLimit {
			break
		}
		task, ok := tasks[reply.TaskID]
		if !ok {
			task, err = store.GetTask(ctx, reply.TaskID)
			if err != nil {
				return nil, nil, err
			}
			tasks[reply.TaskID] = task
		}
		// 没有主会话键就没有「这条回复属于哪一轮对话」的证据；这类行交给启动
		// 对账（按任务整体结算），扫描不产事件。
		sessionKey := strings.TrimSpace(task.MainSessionKey)
		if sessionKey == "" {
			continue
		}
		observationKey := "direct:" + reply.ID
		if s.sessionRunning(sessionKey) {
			s.clearRouterStallObservation(observationKey)
			continue
		}
		if now.Sub(reply.CreatedAt) < routerEventStallMinRuntime {
			continue
		}
		// 先登记再确认：prune 只保留登记表里仍被标为「活着的候选」的键。
		running[observationKey] = true
		if !s.confirmRouterStallObservation(observationKey, now) {
			continue
		}
		instanceKey, err := RouterEventDirectInstanceKey(reply.ID, now)
		if err != nil {
			return nil, nil, err
		}
		userActivityID := s.directTurnUserActivity(ctx, activities, reply)
		payload := map[string]any{
			"task_id":     task.ID,
			"activity_id": reply.ID,
			"status":      reply.Status,
			"since":       reply.CreatedAt.UTC().Format(routerTimeLayout),
		}
		if obs := s.supervisionDirectObservation(ctx, store, task, reply, now); obs != nil {
			payload["supervision"] = obs
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, nil, fmt.Errorf("marshal router event direct_stalled payload: %w", err)
		}
		if len(raw) > RouterEventMaxPayloadBytes {
			notes = append(notes, fmt.Sprintf("activity %s: payload %d bytes exceeds limit", reply.ID, len(raw)))
			continue
		}
		ev, inserted, err := NewRouterStore(store.db).InsertEvent(ctx, RouterEvent{
			RootID:         task.RootID,
			TaskID:         task.ID,
			Kind:           RouterEventDirectStalled,
			InstanceKey:    instanceKey,
			UserActivityID: userActivityID,
			TurnID:         routerEventTurnID("direct:"+reply.ID, RouterEventDirectStalled, instanceKey),
			ChainDepth:     s.routerEventChainDepth(ctx, store, userActivityID),
			PayloadJSON:    string(raw),
			CreatedAt:      now.UTC(),
		})
		if err != nil {
			return nil, nil, err
		}
		if inserted {
			produced = append(produced, ev)
		}
	}
	return produced, notes, nil
}

// directTurnUserActivity 找这条回复对应的原始用户留言：优先显式 ReplyToID，
// 否则回溯到它之前最近的一条用户留言。事件回合靠这个 ID 才能拿到「用户到底
// 要什么」，空着会被 beginCoordinateEventTurn 直接跳过。
func (s *Service) directTurnUserActivity(ctx context.Context, activities *ActivityStore, reply *TaskActivity) string {
	if id := strings.TrimSpace(reply.ReplyToID); id != "" {
		return id
	}
	userActivity, err := activities.PrecedingUserMessage(ctx, reply.TaskID, reply.CreatedAt, reply.ID)
	if err != nil || userActivity == nil {
		return ""
	}
	return userActivity.ID
}

// PrecedingUserMessage 返回该任务在 at 之前（同一时刻则按写入顺序在前）最近的
// 一条用户留言。没有就返回 (nil, nil)。
func (s *ActivityStore) PrecedingUserMessage(ctx context.Context, taskID string, at time.Time, excludeID string) (*TaskActivity, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, errors.New("task id required")
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT `+activitySelectColumns+` FROM task_activities
		 WHERE task_id = ? AND role = ? AND id <> ? AND created_at <= ?
		 ORDER BY created_at DESC, rowid DESC LIMIT 1`,
		taskID, RoleUser, strings.TrimSpace(excludeID), at.UTC().Format(time.RFC3339Nano))
	activity, err := scanActivityRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return activity, nil
}

// attachRouterEventDirectReply 给直连事件补证据：那条回复行现在是什么状态。
// 直连回合没有 Run，节点段与 Run 状态都不适用。
func attachRouterEventDirectReply(ctx context.Context, store *TaskStore, evidence *RouterBriefEvidence, event RouterEvent) {
	activityID := routerEventPayloadString(event.PayloadJSON, "activity_id")
	if activityID == "" {
		evidence.Unverified = append(evidence.Unverified, "direct_stalled event has no activity id")
		return
	}
	reply, err := NewActivityStore(store.db).Get(ctx, activityID)
	if err != nil || reply == nil {
		evidence.Unverified = append(evidence.Unverified, fmt.Sprintf("reply %s: not found in task store", activityID))
		return
	}
	evidence.Unverified = append(evidence.Unverified, fmt.Sprintf(
		"direct turn: reply %s is %s since %s; no workflow run is involved, this is not a verified failure",
		reply.ID, reply.Status, reply.CreatedAt.UTC().Format(routerTimeLayout)))
}
