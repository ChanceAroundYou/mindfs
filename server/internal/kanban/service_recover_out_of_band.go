package kanban

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"
)

// out-of-band 会话回合的重启恢复（C2）。
//
// 用户直接在任务绑定的会话里推一轮时，任务头被推进到 running，收尾依赖
// 「回合结束」回调（SettleSessionTurn → restoreOutOfBandTask）。进程重启会
// 把那个回调和进程内标记一起带走，任务从此永远停在 running。
//
// 判定口径（用户 2026-11-23 裁定）：超过 30 分钟的 OOB 标记一定是被重启带走
// 的 —— 本进程启动后不会再认领同一轮；恢复成 waiting_user，并写一条没有来源
// 留言的 turn_interrupted，让巡检回合（C1）至少能向用户解释这一轮没了。

const (
	// routerOutOfBandInterruptAfter 是 OOB 回合被认定「已被重启带走」的等待时长。
	// 与停滞巡检（taskHealthStallAfter）用同一个尺度：短了会把真正的长回合误判，
	// 长了用户就得多盯着一个转圈的任务。
	routerOutOfBandInterruptAfter = 30 * time.Minute
	// routerOutOfBandSweepInterval 是后台恢复扫描的节拍。它只查一行带索引的
	// 状态表，成本远低于一拍巡检。
	routerOutOfBandSweepInterval = time.Minute
)

// restoreStaleOutOfBandTasks 恢复超过阈值的 OOB 标记。返回真正恢复的任务数。
//
// 幂等：恢复成功即清标记，重复对账不会再写第二条事件；若事件写入成功而状态
// 更新失败，标记会保留，下一拍用同一 instance key 重放（InsertEvent 去重）。
func (s *Service) restoreStaleOutOfBandTasks(ctx context.Context, store *TaskStore, now time.Time) (int, error) {
	if s == nil || store == nil {
		return 0, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	router := NewRouterStore(store.db)
	stale, err := router.ListStaleOutOfBand(ctx, store.root.ID, now.Add(-routerOutOfBandInterruptAfter))
	if err != nil {
		return 0, err
	}
	restored := 0
	for _, item := range stale {
		task, err := store.GetTask(ctx, item.TaskID)
		if err != nil {
			log.Printf("[kanban/recover] out-of-band task %s: %v", item.TaskID, err)
			continue
		}
		// 任务已经不在 running（被用户取消、被别处收尾、终态归档）：不要重新
		// 打开它，但上一轮留下的标记是垃圾，清掉。
		if task.Status != StatusRunning || isTerminalStatus(task.Status) || strings.TrimSpace(task.ArchivedAt) != "" {
			if err := router.ClearOutOfBand(ctx, item.TaskID); err != nil {
				log.Printf("[kanban/recover] clear out-of-band task %s: %v", item.TaskID, err)
			}
			continue
		}
		// 会话还在写：这一轮没被带走，只是慢。「超过 30 分钟」只对「重启后没
		// 人再推这一轮」成立，对本进程里仍在跑的慢回合不成立 —— 现场 #104
		// 两次误判时块流都还在持续增长。此时**什么都不动**，尤其是不能清标记：
		// 清掉之后回合结束时收尾代码找不到 OOB 归属，任务就再也回不到
		// waiting_user，等于把一种卡死换成另一种。
		if s.sessionRunning(item.SessionKey) {
			continue
		}
		// 先写事件、后改状态：反过来若崩在两步之间，任务已 waiting_user、事件
		// 却没落，巡检与用户都看不到「这一轮为什么没了」。
		if err := s.insertOutOfBandInterruptedEvent(ctx, router, store, task, item, now); err != nil {
			log.Printf("[kanban/recover] out-of-band task %s event: %v", item.TaskID, err)
			continue
		}
		if err := store.UpdateTaskStatus(ctx, item.TaskID, StatusWaitingUser, false); err != nil {
			log.Printf("[kanban/recover] out-of-band task %s status: %v", item.TaskID, err)
			continue
		}
		if err := router.ClearOutOfBand(ctx, item.TaskID); err != nil {
			log.Printf("[kanban/recover] clear out-of-band task %s: %v", item.TaskID, err)
		}
		s.broadcastTask(ctx, store, item.TaskID)
		restored++
	}
	return restored, nil
}

// insertOutOfBandInterruptedEvent 落一条 turn_interrupted。instance key 由任务
// 与开始时刻决定 —— 同一轮被重复对账时只会命中同一行。
func (s *Service) insertOutOfBandInterruptedEvent(
	ctx context.Context,
	router *RouterStore,
	store *TaskStore,
	task Task,
	item outOfBandTask,
	now time.Time,
) error {
	started := formatRouterTime(item.StartedAt)
	instanceKey := "oob:" + task.ID + ":" + started
	payload := map[string]any{
		"task_id":           task.ID,
		"session_key":       item.SessionKey,
		"started_at":        started,
		"reason":            "out_of_band_turn_interrupted",
		"threshold_minutes": int(routerOutOfBandInterruptAfter / time.Minute),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, _, err = router.InsertEvent(ctx, RouterEvent{
		RootID:      store.root.ID,
		TaskID:      task.ID,
		Kind:        RouterEventTurnInterrupted,
		InstanceKey: instanceKey,
		TurnID:      routerEventTurnID(task.ID, RouterEventTurnInterrupted, instanceKey),
		PayloadJSON: string(raw),
		CreatedAt:   now,
	})
	return err
}

// StartOutOfBandRecovery 起一个后台扫描（启动即跑一次，之后每分钟）。
// ctx 取消即退出；同一 Service 重复调用只会起一个 goroutine。
func (s *Service) StartOutOfBandRecovery(ctx context.Context) {
	if s == nil {
		return
	}
	s.outOfBandSweepOnce.Do(func() {
		go func() {
			sweep := func() {
				if s.Roots == nil {
					return
				}
				for _, root := range s.Roots.ListRoots() {
					store, err := s.taskStore(root.ID)
					if err != nil {
						log.Printf("[kanban/recover] root=%s out-of-band store: %v", root.ID, err)
						continue
					}
					if n, err := s.restoreStaleOutOfBandTasks(ctx, store, time.Now().UTC()); err != nil {
						log.Printf("[kanban/recover] root=%s out-of-band sweep: %v", root.ID, err)
					} else if n > 0 {
						log.Printf("[kanban/recover] root=%s out-of-band recovered=%d", root.ID, n)
					}
				}
			}
			ticker := time.NewTicker(routerOutOfBandSweepInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					sweep()
				}
			}
		}()
	})
}
