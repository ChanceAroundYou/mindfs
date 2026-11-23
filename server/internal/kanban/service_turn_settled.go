package kanban

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// 会话回合结算事件（W2）。
//
// 为什么需要：Router 只在 Run 结算或既有事件上被唤醒。用户在任务详情里推进一次
// 会话（追问、改口、让节点接着干），那一轮走完没有任何事件，Router 既不总结也
// 不跟进 —— 线上 #68 就是这样漂出视野的。EndSessionTurn 是**所有**会话回合的
// 唯一终结器（主会话、节点会话、委派会话都经过它），由它发一条 turn_settled，
// Router 才有收拾这一轮的机会。
//
// 这里只负责「把事实写进账本」，不判断 Router 该做什么：消费侧在
// beginCoordinateEventTurn（预算与闸门）与 attachRouterEventBrief（证据）。

// RouterEventTurnInstanceKey 是 turn_settled 的实例键：一个回合一条。
// EndSessionTurn 可能因重连、重放、重复调度被调用多次，唯一键让重复写入变成
// 一次 no-op，而不是多次唤醒 Router。
func RouterEventTurnInstanceKey(turnID string) string {
	return "turn:" + strings.TrimSpace(turnID)
}

// routerEventAllowsEmptyRun 报告该事件是否允许没有 Run。会话回合与直连停摆、
// 证据缺口一样，本来就不属于任何 Run。
func routerEventAllowsEmptyRun(kind RouterEventKind) bool {
	switch kind {
	case RouterEventDirectStalled, RouterEventCompletionBlocked, RouterEventTurnSettled,
		RouterEventTurnStalled, RouterEventTaskOrphaned, RouterEventTurnInterrupted:
		return true
	default:
		return false
	}
}

// routerSettlementTurnsPerHour 是每个任务每小时的结算回合上限。结算回合不计入
// 用户留言的 3 回合预算，所以它需要一个自己的刹车：否则一个高频轮询/委派的会话
// 会把 Router 的每小时额度全花在收尾上。
const routerSettlementTurnsPerHour = 20

// routerSettlementExcerptRunes 是写进事件载荷的产出摘要长度上限。它是给 Router
// 判断「这一轮值不值得总结」用的证据，不是全文；原文在活动流里。
const routerSettlementExcerptRunes = 400

// turnSettledErrorRunes 是结算事件里失败原因的长度上限。它比 excerpt 短得多：
// Router 只需要知道「为什么没跑成」，堆栈与整段请求体都在会话行与日志里。
// 也不能直接复用 400 —— payload 值有 500 **字节**的硬上限，400 个中文字符
// 会被 brief 判成「载荷被截断」，凭空多一条未验证项。
const turnSettledErrorRunes = 160

// routerEventBriefExcerptRunes 是交给 Router 的产出摘要长度上限。它比写进
// 账本的 routerSettlementExcerptRunes 更宽松：账本记事实，brief 供判断。
const routerEventBriefExcerptRunes = 1200

// routerEventBriefExcerpt 从事件载荷里取出产出摘要。取不到就是空串——摘要缺失
// 只意味着「这一轮没有可读产出」，不是 brief 出错。
func routerEventBriefExcerpt(payloadJSON string) string {
	trimmed := strings.TrimSpace(payloadJSON)
	if trimmed == "" {
		return ""
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return ""
	}
	text, _ := raw["excerpt"].(string)
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) > routerEventBriefExcerptRunes {
		return string(runes[:routerEventBriefExcerptRunes]) + "…"
	}
	return text
}

// TurnSettledInput 是一次会话回合的结算输入。Excerpt 通常来自 harness 累积的
// 助手文本末尾；OutOfBand 表示这一轮是系统发起的（巡检、自动补做），
// AnsweredQuestion 表示回合内答复了一张提问卡。
//
// Failed / Error 来自会话行的 Aux.SessionError —— 回合开始时它被清空，所以结算时
// 读到非空就一定是**本轮内**报过的错。没有这个标记，失败回合在 Router 眼里和正常
// 回合一模一样（错误说明本身也算 has_output），会被当成「这一轮正常结束」。
type TurnSettledInput struct {
	SessionKey string
	TurnID     string
	Excerpt    string
	// ToolCalls 是这一轮发起过的工具调用次数。它是「有真实执行结果」的唯一判据：
	// 高于 0 时 Router 的最后一条 say 才允许提升成任务结论（纯闲聊回合不行）。
	ToolCalls        int
	OutOfBand        bool
	AnsweredQuestion bool
	Failed           bool
	Error            string
}

// SettleSessionTurn 给任务绑定的会话记一条 turn_settled 事件。返回 true 表示
// 这次调用真的写入了新事件（重复调用同一 turnID 返回 false）。
//
// 不是任务会话、任务已终态或已归档时直接返回 false：这些回合不需要 Router 收尾。
func (s *Service) SettleSessionTurn(ctx context.Context, rootID string, in TurnSettledInput) (bool, error) {
	if s == nil {
		return false, nil
	}
	sessionKey := strings.TrimSpace(in.SessionKey)
	turnID := strings.TrimSpace(in.TurnID)
	if sessionKey == "" || turnID == "" {
		return false, nil
	}
	store, err := s.taskStore(rootID)
	if err != nil {
		return false, nil
	}
	taskID, err := store.TaskIDForSession(ctx, sessionKey)
	if err != nil || taskID == "" {
		return false, err
	}
	task, err := store.GetTask(ctx, taskID)
	if err != nil {
		return false, err
	}
	if isTerminalStatus(task.Status) || strings.TrimSpace(task.ArchivedAt) != "" {
		return false, nil
	}
	// 会话推进跟随（W4）：两个标记都在回合结束时取走，一次性生效。
	if s.takeSessionAnsweredQuestion(rootID, sessionKey) {
		in.AnsweredQuestion = true
	}
	outOfBand := s.takeSessionOutOfBandTurn(rootID, sessionKey)
	if outOfBand {
		in.OutOfBand = true
	}
	now := time.Now().UTC()
	activities := NewActivityStore(store.db)
	// 结算回合尽量挂在「这一轮之前最近的用户留言」上：brief 会把它作为「用户这一轮
	// 的要求」展示给 Router。系统发起的回合（巡检补做、自动续跑）没有留言，此时
	// 事件照样要落 —— 缺留言只是少了背景，不是没有值得收尾的一轮。
	userActivity, err := activities.PrecedingUserMessage(ctx, taskID, now, "")
	if err != nil {
		return false, err
	}
	userActivityID := ""
	durationMS := int64(0)
	if userActivity != nil {
		userActivityID = userActivity.ID
		durationMS = now.Sub(userActivity.CreatedAt).Milliseconds()
	}
	instanceKey := RouterEventTurnInstanceKey(turnID)
	payload := map[string]any{
		"session_key":       sessionKey,
		"turn_id":           turnID,
		"duration_ms":       durationMS,
		"has_output":        strings.TrimSpace(in.Excerpt) != "",
		"tool_calls":        in.ToolCalls,
		"out_of_band":       in.OutOfBand,
		"answered_question": in.AnsweredQuestion,
		"failed":            in.Failed,
		"excerpt":           tailExcerpt(in.Excerpt, routerSettlementExcerptRunes),
	}
	if in.Failed {
		payload["error"] = cutAtSentence(strings.TrimSpace(in.Error), turnSettledErrorRunes)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return false, err
	}
	_, inserted, err := NewRouterStore(store.db).InsertEvent(ctx, RouterEvent{
		RootID:         rootID,
		TaskID:         taskID,
		Kind:           RouterEventTurnSettled,
		InstanceKey:    instanceKey,
		TurnID:         routerEventTurnID(turnID, RouterEventTurnSettled, instanceKey),
		UserActivityID: userActivityID,
		PayloadJSON:    string(raw),
		CreatedAt:      now,
	})
	// out-of-band 回合结束了：任务头放回 waiting_user（只动仍停在 running 的）。
	// 放在错误返回之前：事件落库失败不代表这一轮没结束，任务头不能永远 running。
	if outOfBand {
		s.restoreOutOfBandTask(ctx, store, taskID)
	}
	if err != nil {
		return false, err
	}
	// 账本是唯一事实来源；消费由 coordinate 循环按拍扫描，这里不抢跑。
	return inserted, nil
}

// ListRouterEventsForTask 读任务的事件账本（按创建时间升序）。供管理端只读诊断
// 与测试取证；Router 自己的消费走 ListPendingEvents。
func (s *Service) ListRouterEventsForTask(ctx context.Context, rootID, taskID string) ([]RouterEvent, error) {
	store, err := s.taskStore(rootID)
	if err != nil {
		return nil, err
	}
	return NewRouterStore(store.db).ListEventsByTask(ctx, taskID)
}

// tailExcerpt 取文本末尾最多 limit 个字符：harness 的累积摘要本身就是尾部，
// 而「最后干了什么」比开头更能说明这一轮的结论。
func tailExcerpt(text string, limit int) string {
	trimmed := strings.TrimSpace(text)
	runes := []rune(trimmed)
	if len(runes) <= limit {
		return trimmed
	}
	return "…" + string(runes[len(runes)-limit:])
}

// eventTurnBudgetSkip 判一个事件回合是否还有额度：返回非空 reason 表示该落
// skipped，返回 err 表示查库失败（落 failed）。
//
// 三类事件各有自己的预算口径，不能共用「每条留言 3 回合」那把尺：
//   - completion_blocked：补做额度由契约的 MaxRepairs 管；
//   - turn_settled：会话回合结算，按每任务每小时 20 次；
//   - turn_interrupted / turn_stalled / task_orphaned：巡检与重启恢复回合，
//     额度是「每档重开次数 + 每 Run 12 + 每小时 20 个动作」；
//   - 其余（含其余监管回合）：按原始留言的 3 回合。
func eventTurnBudgetSkip(ctx context.Context, store *TaskStore, event RouterEvent, userActivityID string) (string, error) {
	if store == nil {
		return "", nil
	}
	switch event.Kind {
	case RouterEventCompletionBlocked:
		return "", nil
	case RouterEventTurnSettled:
		allowed, reason, err := settlementTurnBudget(ctx, store, event.TaskID, time.Time{})
		if err != nil {
			return "", err
		}
		if !allowed {
			return reason, nil
		}
		return "", nil
	case RouterEventTurnInterrupted, RouterEventTurnStalled, RouterEventTaskOrphaned:
		// 恢复回合不挤占用户留言的 3 回合额度：重启/停滞本来就是用户留言之外
		// 的事故，算进去会让「入口 1 轮 + 自动重开 3 次」的第 3 次被静默跳过，
		// 用户看到的仍是「跑了一半就没了」且没有任何解释。
		return "", nil
	}
	turns, err := countUserTurnDecisions(ctx, store.db, event.TaskID, userActivityID)
	if err != nil {
		return "", err
	}
	if turns >= RouterCoordinateMaxTurnsPerUserActivity {
		return "turn budget exhausted (" + strconv.Itoa(turns) + "/" +
			strconv.Itoa(RouterCoordinateMaxTurnsPerUserActivity) + ")", nil
	}
	return "", nil
}

// routerEventAllowsNoUserActivity 判断这类事件是否允许在没有来源用户留言时
// 起一个 Router 回合（闸门在 router_coordinate_turn.go）。允许的都是「系统
// 自己发现的现场」：用户没说话，但确实有事情要处理 —— 回合结算、停滞、孤儿、
// 重启打断。它们也只能回话，不能借机收尾（见 applyRouteAction）。
func routerEventAllowsNoUserActivity(kind RouterEventKind) bool {
	return routerTurnAllowsNoUserActivity(string(kind))
}

// routerTurnAllowsNoUserActivity 是同一口径的字符串版本：决策的 TriggerKind
// 就是事件 kind，应用侧按它判断「这一轮本来就没有来源留言」。
func routerTurnAllowsNoUserActivity(kind string) bool {
	switch kind {
	case string(RouterEventTurnSettled), string(RouterEventTurnStalled),
		string(RouterEventTaskOrphaned), string(RouterEventTurnInterrupted):
		return true
	default:
		return false
	}
}

// settlementTurnBudget 回答「这个任务此刻还能不能再开一个结算回合」。
// 返回 allowed=false 时 reason 是一句可直接落到 skip 上的说明。
func settlementTurnBudget(ctx context.Context, store *TaskStore, taskID string, now time.Time) (bool, string, error) {
	if store == nil {
		return false, "task store unavailable", nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	used, err := countSettlementTurnsSince(ctx, store.db, taskID, now.Add(-time.Hour))
	if err != nil {
		return false, "", err
	}
	if used >= routerSettlementTurnsPerHour {
		return false, "settlement turn budget exhausted (" +
			strconv.Itoa(routerSettlementTurnsPerHour) + "/hour), skipped", nil
	}
	return true, "", nil
}

// countSettlementTurnsSince 数该任务在 since 之后开过多少个结算回合。
func countSettlementTurnsSince(ctx context.Context, db *sql.DB, taskID string, since time.Time) (int, error) {
	if db == nil {
		return 0, nil
	}
	var turns int
	err := db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM router_decisions
WHERE task_id = ? AND trigger_kind = ? AND created_at >= ?`,
		strings.TrimSpace(taskID), string(RouterTriggerTurnSettled), formatRouterTime(since),
	).Scan(&turns)
	return turns, err
}
