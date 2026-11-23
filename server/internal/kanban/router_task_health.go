package kanban

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

// 任务级巡检（Plan 2026-11-22 W3-2）。
//
// 既有监管只会在**事件**上被唤醒：Run 结算/失败、gate 等待、会话探针空闲停摆。
// 三种现场它完全看不见 ——
//   - 回合在飞但一个字节都不吐（挂死的 CLI，探针照样报「在跑」）；
//   - 任务写着 running 而底下什么都没了（重启/崩溃留下的孤儿，没有任何事件）；
//   - waiting_user 却有会话在跑（不该静默，但也不必报事件）。
//
// 所以要有**一拍一次**的巡检：只读非终态任务，分类后只把可行动的两类写成事件。
// 巡检本身不结算、不重试：动作仍归 Router（supervision.mode=on 时）。
// 唯一例外是 waiting_busy：任务头已经落到待处理、而绑定会话/节点确实还在跑时，
// 巡检把它纠回 running（只改任务头，不发事件也不决策）。

const (
	// taskHealthStallAfter 是「在飞但没有任何产出」的停滞阈值。取 10 分钟：
	// 单次长工具调用（编译、整包测试）常见 3–8 分钟，10 分钟才把「慢」与「卡死」
	// 分开；计划里「停滞发现 ≤10 min」也是这个数。
	taskHealthStallAfter = 10 * time.Minute
	// taskHealthBucket 是实例键的时间桶：同一现场 5 分钟内只报一条，避免每一拍
	// 都唤醒 Router。
	taskHealthBucket = 5 * time.Minute
	// taskHealthSweepLimit 限制单拍巡检的任务数（有界查询，见计划「事件风暴」风险）。
	taskHealthSweepLimit = 64
)

// taskHealth 是巡检分类。只有 stalled_turn / orphaned 会产事件。
type taskHealth string

const (
	taskHealthHealthy     taskHealth = "healthy"
	taskHealthStalledTurn taskHealth = "stalled_turn"
	taskHealthOrphaned    taskHealth = "orphaned"
	taskHealthWaitingBusy taskHealth = "waiting_busy"
)

// taskHealthReply 是仍 pending/running 的回复行，附带该会话的输出水位。
type taskHealthReply struct {
	ActivityID  string
	SessionKey  string
	CreatedAt   time.Time
	LastOutput  time.Time
	OutputKnown bool
}

// taskHealthInput 是判定所需的全部事实。判定是纯函数，取证留给巡检。
type taskHealthInput struct {
	Status      string
	Now         time.Time
	Replies     []taskHealthReply
	OpenNodes   bool
	SessionLive bool
}

// classifyTaskHealth 把现场分成四类。
//
// 停滞只看水位，不看会话探针：探针回答的是「进程还在不在回包」，挂死的 CLI 会
// 一直回包。水位未知（刚重启、本进程没见过帧）一律当健康 —— 把未知当停滞，
// 每次重启都会给每个在跑的回合记一次假卡死。
func classifyTaskHealth(in taskHealthInput) taskHealth {
	if s := strings.TrimSpace(in.Status); s == StatusPaused || isTerminalStatus(s) {
		return taskHealthHealthy
	}
	live := in.SessionLive || len(in.Replies) > 0
	switch strings.TrimSpace(in.Status) {
	case StatusRunning:
		if len(in.Replies) == 0 {
			// 没有回复行也没有 open 节点/活会话：任务只是一个写着 running 的空壳。
			if in.OpenNodes || in.SessionLive {
				return taskHealthHealthy
			}
			return taskHealthOrphaned
		}
		for _, reply := range in.Replies {
			if !reply.OutputKnown {
				continue
			}
			if in.Now.Sub(reply.LastOutput) >= taskHealthStallAfter {
				return taskHealthStalledTurn
			}
		}
		return taskHealthHealthy
	case StatusWaitingUser:
		if live {
			return taskHealthWaitingBusy
		}
		return taskHealthHealthy
	default:
		return taskHealthHealthy
	}
}

// taskHealthTask 是巡检读出来的任务行（只取判定要用的字段）。
type taskHealthTask struct {
	ID             string
	Status         string
	MainSessionKey string
}

// taskHealthSweep 跑一拍巡检。返回本拍真正写入的事件（重复的实例键不算）。
func (s *Service) taskHealthSweep(ctx context.Context, rootID string, now time.Time) ([]RouterEvent, error) {
	if s == nil {
		return nil, nil
	}
	store, err := s.taskStore(rootID)
	if err != nil {
		return nil, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	tasks, err := listTaskHealthCandidates(ctx, store, rootID)
	if err != nil {
		return nil, err
	}
	routerStore := NewRouterStore(store.db)
	var produced []RouterEvent
	for _, task := range tasks {
		in, err := s.taskHealthInputFor(ctx, store, rootID, task, now)
		if err != nil {
			// 单个任务取证失败不影响其他任务：下一拍照样会再看一次。
			continue
		}
		var kind RouterEventKind
		payload := map[string]any{"task_id": task.ID, "status": task.Status, "session_key": task.MainSessionKey}
		switch classifyTaskHealth(in) {
		case taskHealthWaitingBusy:
			// 任务头已经落到待处理，但绑定会话/在跑节点其实还在干活：巡检把
			// 任务头纠回 running。这是唯一由巡检直接改任务状态的分支 —— 它不
			// 产生事件、不替代 Router 的决策，只让看板不再说谎。
			if s.taskRuntimeLive(ctx, store, task.ID, "") {
				s.markTaskRuntimeRunning(ctx, store, task.ID)
			}
			continue
		case taskHealthStalledTurn:
			kind = RouterEventTurnStalled
			stale := stalestReply(in.Replies, now)
			payload["activity_id"] = stale.ActivityID
			payload["session_key"] = stale.SessionKey
			payload["since"] = stale.CreatedAt.UTC().Format(routerTimeLayout)
			payload["last_output_at"] = stale.LastOutput.UTC().Format(routerTimeLayout)
			payload["stalled_for_ms"] = now.Sub(stale.LastOutput).Milliseconds()
			payload["replying"] = in.SessionLive
		case taskHealthOrphaned:
			kind = RouterEventTaskOrphaned
			payload["open_nodes"] = false
		default:
			continue
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			continue
		}
		ref := task.ID
		if activityID, _ := payload["activity_id"].(string); strings.TrimSpace(activityID) != "" {
			ref = activityID
		}
		instanceKey := routerEventHealthInstanceKey(kind, ref, now)
		event, inserted, err := routerStore.InsertEvent(ctx, RouterEvent{
			RootID:      rootID,
			TaskID:      task.ID,
			Kind:        kind,
			InstanceKey: instanceKey,
			TurnID:      routerEventTurnID(ref, kind, instanceKey),
			PayloadJSON: string(raw),
			CreatedAt:   now,
		})
		if err != nil {
			return produced, err
		}
		if inserted {
			produced = append(produced, event)
		}
	}
	return produced, nil
}

// routerEventHealthInstanceKey 是巡检事件的实例键：同一现场（任务或回复）在同一
// 5 分钟桶里只报一条。
func routerEventHealthInstanceKey(kind RouterEventKind, ref string, at time.Time) string {
	return string(kind) + ":" + strings.TrimSpace(ref) + ":" + strconv.FormatInt(at.UTC().Unix()/int64(taskHealthBucket/time.Second), 10)
}

// listTaskHealthCandidates 只读「可能在跑」的任务：running 与 waiting_user，
// 未归档。暂停与终态任务不需要巡检。
func listTaskHealthCandidates(ctx context.Context, store *TaskStore, rootID string) ([]taskHealthTask, error) {
	rows, err := store.db.QueryContext(ctx, `
SELECT id, status, main_session_key FROM tasks
WHERE root_id = ? AND archived_at = '' AND status IN (?, ?)
ORDER BY updated_at ASC LIMIT ?`,
		rootID, StatusRunning, StatusWaitingUser, taskHealthSweepLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []taskHealthTask
	for rows.Next() {
		var task taskHealthTask
		if err := rows.Scan(&task.ID, &task.Status, &task.MainSessionKey); err != nil {
			return nil, err
		}
		out = append(out, task)
	}
	return out, rows.Err()
}

// taskHealthInputFor 取一个任务的判定事实：在飞回复、open 节点、会话活性、水位。
func (s *Service) taskHealthInputFor(ctx context.Context, store *TaskStore, rootID string, task taskHealthTask, now time.Time) (taskHealthInput, error) {
	replies, err := runningTurnReplies(ctx, store, task.ID)
	if err != nil {
		return taskHealthInput{}, err
	}
	openNodes, err := taskHasOpenNodes(ctx, store, task.ID)
	if err != nil {
		return taskHealthInput{}, err
	}
	live := s.sessionRunning(task.MainSessionKey)
	for i := range replies {
		if s.sessionRunning(replies[i].SessionKey) {
			live = true
		}
		if watermark, known := s.SessionTurnWatermark(rootID, replies[i].SessionKey); known {
			replies[i].LastOutput, replies[i].OutputKnown = watermark, true
		}
	}
	return taskHealthInput{Status: task.Status, Now: now, Replies: replies, OpenNodes: openNodes, SessionLive: live}, nil
}

// runningTurnReplies 读任务仍在 pending/running 的回复行。
func runningTurnReplies(ctx context.Context, store *TaskStore, taskID string) ([]taskHealthReply, error) {
	rows, err := store.db.QueryContext(ctx, `
SELECT id, session_key, created_at FROM task_activities
WHERE task_id = ? AND type = ? AND status IN (?, ?)
ORDER BY created_at ASC`,
		taskID, ActivityTypeAgentReply, ActivityStatusPending, ActivityStatusRunning)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []taskHealthReply
	for rows.Next() {
		var reply taskHealthReply
		var created string
		if err := rows.Scan(&reply.ActivityID, &reply.SessionKey, &created); err != nil {
			return nil, err
		}
		reply.CreatedAt = parseActivityTime(created)
		out = append(out, reply)
	}
	return out, rows.Err()
}

// taskHasOpenNodes 报告任务是否还有未终态的节点（引擎随时可能继续推进）。
func taskHasOpenNodes(ctx context.Context, store *TaskStore, taskID string) (bool, error) {
	var one int
	err := store.db.QueryRowContext(ctx, `
SELECT 1 FROM workflow_node_runs n
JOIN workflow_runs r ON r.id = n.run_id
WHERE r.task_id = ? AND n.status IN (?, ?, ?)
LIMIT 1`,
		taskID, NodeRunQueued, NodeRunRunning, NodeRunWaitingUser).Scan(&one)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// stalestReply 取水位最旧的那条回复行（调用前已确保至少一条可用）。
func stalestReply(replies []taskHealthReply, now time.Time) taskHealthReply {
	var best taskHealthReply
	bestIdle := time.Duration(-1)
	for _, reply := range replies {
		if !reply.OutputKnown {
			continue
		}
		idle := now.Sub(reply.LastOutput)
		if idle > bestIdle {
			best, bestIdle = reply, idle
		}
	}
	return best
}

// routerEventHealthKind 报告该事件是否属于巡检/恢复事件（没有 Run）。
func routerEventHealthKind(kind RouterEventKind) bool {
	switch kind {
	case RouterEventTurnStalled, RouterEventTaskOrphaned, RouterEventTurnInterrupted:
		return true
	default:
		return false
	}
}

// routerEventHealthUnverified 把载荷里的现场事实写成模型能直读的句子。这些话是
// 证据，不是面向用户的成品文案 —— Router 要照它们决定「重试还是告知」。
func routerEventHealthUnverified(event RouterEvent) []string {
	var payload map[string]any
	_ = json.Unmarshal([]byte(strings.TrimSpace(event.PayloadJSON)), &payload)
	switch event.Kind {
	case RouterEventTurnStalled:
		idleMS, _ := payload["stalled_for_ms"].(float64)
		line := "turn stalled: no output for " +
			(time.Duration(idleMS) * time.Millisecond).Round(time.Second).String() +
			" while the turn is still marked running; this is not a verified failure"
		if replying, _ := payload["replying"].(bool); replying {
			line += "; the session probe still reports it as replying, which is exactly why the output watermark is the evidence here"
		}
		return []string{line}
	case RouterEventTurnInterrupted:
		return []string{"interrupted turn: the process ended while this turn was still running; the reply was settled as cancelled and the transcript may be incomplete"}
	case RouterEventTaskOrphaned:
		return []string{"orphaned task: the task is marked running, but no session is live, no node is open and no reply is in flight"}
	default:
		return nil
	}
}
