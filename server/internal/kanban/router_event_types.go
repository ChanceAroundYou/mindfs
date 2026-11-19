package kanban

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Router 持久事件账本（Plan 2026-11-18，R1 T5）。
//
// coordinate 模式里 Router 不再是「留言进来就同步跑一轮」，而是被事件驱动：
// Run 结算/失败、HumanGate 等待、Run 停摆、启动对账都会落一条事件，由 Router
// 消费并决定下一步。事件必须跨进程重启存活，且同一个实例只处理一次——
// 因此账本是 append-only 的唯一键去重 + CAS 领取，而不是内存队列。

type RouterEventKind string

const (
	RouterEventRunSettled       RouterEventKind = "run_settled"
	RouterEventRunFailed        RouterEventKind = "run_failed"
	RouterEventGateWaiting      RouterEventKind = "gate_waiting"
	RouterEventRunStalled       RouterEventKind = "run_stalled"
	RouterEventStartupReconcile RouterEventKind = "startup_reconcile"
)

const (
	RouterEventPending = "pending"
	RouterEventClaimed = "claimed"
	RouterEventDone    = "done"
	RouterEventFailed  = "failed"
	RouterEventSkipped = "skipped"
)

// RouterEventMaxPayloadBytes 是 PayloadJSON 的硬上限。超过直接拒绝，不做
// 截断后静默写入——账本里宁可缺一条事件，也不能留一条骗人的摘要。
const RouterEventMaxPayloadBytes = 8192

// routerEventStallBucket 是 run_stalled 的去重时间桶：同一 Run 在同一个
// 5 分钟窗口内只产生一条停摆事件。
const routerEventStallBucket = 5 * time.Minute

// routerEventPruneLimit 是单次回收的行数上限。分批是为了不长事务锁表；
// 这个数只是「一拍删多少」，不是保留期。
const routerEventPruneLimit = 200

// RouterEvent 是一行持久事件。字段与 schema 一一对应；时间一律 UTC。
type RouterEvent struct {
	ID               string // newID("revt")
	RootID           string
	TaskID           string
	RunID            string // 关联 WorkflowRun；startup_reconcile 也必须有
	Kind             RouterEventKind
	InstanceKey      string // 事件实例稳定标识（见 RouterEventInstanceKey）
	UniqueKey        string // taskID\x00runID\x00kind\x00InstanceKey
	UserActivityID   string // originating user activity
	TurnID           string // 确定性 turn 标识（同一实例稳定）
	ChainDepth       int    // 原始留言链深度，入口留言=0
	SourceDecisionID string // 产生该事件的 Router 决策（可空）
	Attempt          int    // gate/stall 相关 attempt，其他为 0
	PayloadJSON      string // 有界 JSON，不放大转录/文件/密钥
	Status           string
	ClaimedBy        string
	ClaimedAt        time.Time
	CompletedAt      time.Time
	LastError        string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// RouterEventUniqueKey 是账本的幂等键。kind 与 InstanceKey 都进键：同一个 Run
// 的 settled 与 failed 是两条独立事实，不同 gate attempt 也是。
func RouterEventUniqueKey(taskID, runID string, kind RouterEventKind, instanceKey string) string {
	return taskID + "\x00" + runID + "\x00" + string(kind) + "\x00" + instanceKey
}

// RouterEventInstanceKey 按事件类型计算实例键。T6 的事件生产端直接复用这里，
// 保证「生产者算出来的实例」与「账本去重用的实例」是同一套规则：
//
//   - run_settled / run_failed / startup_reconcile: RunID
//   - gate_waiting: nodeRunID + ":" + attempt
//   - run_stalled: RunID + ":" + UTC unix/300（5 分钟桶）
func RouterEventInstanceKey(kind RouterEventKind, runID, nodeRunID string, attempt int, at time.Time) (string, error) {
	runID = strings.TrimSpace(runID)
	switch kind {
	case RouterEventRunSettled, RouterEventRunFailed, RouterEventStartupReconcile:
		if runID == "" {
			return "", fmt.Errorf("router event %s requires a run id", kind)
		}
		return runID, nil
	case RouterEventGateWaiting:
		nodeRunID = strings.TrimSpace(nodeRunID)
		if nodeRunID == "" {
			return "", errors.New("router event gate_waiting requires a node run id")
		}
		if attempt < 0 {
			return "", fmt.Errorf("router event gate_waiting attempt %d must be >= 0", attempt)
		}
		return nodeRunID + ":" + strconv.Itoa(attempt), nil
	case RouterEventRunStalled:
		if runID == "" {
			return "", errors.New("router event run_stalled requires a run id")
		}
		if at.IsZero() {
			return "", errors.New("router event run_stalled requires a timestamp")
		}
		bucket := at.UTC().Unix() / int64(routerEventStallBucket/time.Second)
		return runID + ":" + strconv.FormatInt(bucket, 10), nil
	default:
		return "", fmt.Errorf("unknown router event kind %q", kind)
	}
}

// knownRouterEventKind 报告 kind 是否是本版认识的事件类型。
func knownRouterEventKind(kind RouterEventKind) bool {
	switch kind {
	case RouterEventRunSettled, RouterEventRunFailed, RouterEventGateWaiting,
		RouterEventRunStalled, RouterEventStartupReconcile:
		return true
	default:
		return false
	}
}

// knownRouterEventStatus 报告状态是否是账本里出现过的合法值。
func knownRouterEventStatus(status string) bool {
	switch status {
	case RouterEventPending, RouterEventClaimed, RouterEventDone, RouterEventFailed, RouterEventSkipped:
		return true
	default:
		return false
	}
}
