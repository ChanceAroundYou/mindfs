package api

import (
	"fmt"
	"strings"
	"time"

	"mindfs/server/internal/session"
)

func (h *StreamHub) ensurePendingSessionLocked(sessionKey string) *SessionPendingState {
	state := h.pendingSessions[sessionKey]
	if state == nil {
		state = &SessionPendingState{}
		h.pendingSessions[sessionKey] = state
	}
	return state
}

// nextPendingTurnIDLocked 生成一个新的回合身份。调用方必须已持有 h.mu。
//
// 与旧实现的关键差异：不再自增计数器，而是 mint 一个 mindfs_id。Hub 是每账户一份的
// （多账户分区，见 AGENTS.md），计数器在本进程内唯一，而 mindfs_id 全局唯一 ——
// 于是「哪个 id」这个问题从「哪个 hub」里解耦出来，S2e 结束时同一个逻辑 id
// 在主日志与 pending 上只有一种表示（方案 D3/D6）。
//
// 注意：**pending 的 id 不再由这里铸**。用户回合直接使用落盘后传入的
// authoritative mindfs_id；只有兼容调用没带 id 时才从 seq 派生。这里只留给
// SetPendingReply（子会话/续轮，没有 user 行 seq 可用）这类确实没有 seed 的路径。
func (h *StreamHub) nextPendingTurnIDLocked() string {
	// seed 只需在本进程内唯一：这条路径（SetPendingReply 子会话/续轮）没有
	// user 行 seq 可对齐，灯的身份不代表任何落盘行。UnixNano 够用且不确定性问题
	// 只影响灯的重复匹配（多进程不会共享 pending 表）。
	return session.NewMindFSID("", fmt.Sprintf("pending|%d", time.Now().UnixNano()))
}

func (h *StreamHub) SetPendingUser(rootID, sessionKey, sessionTitle, agent, model, modelDisplayName, mode, effort, fastService string, planMode bool, content string) (*PendingUserMessage, string) {
	return h.SetPendingUserAt(rootID, sessionKey, sessionTitle, agent, model, modelDisplayName, mode, effort, fastService, planMode, content, time.Now().UTC())
}

// SetPendingUserAt 置入「用户已发送」的 pending，返回该条目所属**回合的 mindfs_id**。
// 调用方必须把这个 id 交给回合终结器（AppContext.EndSessionTurn），
// 否则终结器无法判定「这条 done 是不是本轮的」。用户发送路径应传入持久化时已定的 id。
func (h *StreamHub) SetPendingUserAt(rootID, sessionKey, sessionTitle, agent, model, modelDisplayName, mode, effort, fastService string, planMode bool, content string, timestamp time.Time, userExchangeSeq ...int) (*PendingUserMessage, string) {
	seq := 0
	if len(userExchangeSeq) > 0 {
		seq = userExchangeSeq[0]
	}
	return h.setPendingUserAt(rootID, sessionKey, sessionTitle, agent, model, modelDisplayName, mode, effort, fastService, planMode, content, timestamp, seq, "")
}

func (h *StreamHub) setPendingUserAt(rootID, sessionKey, sessionTitle, agent, model, modelDisplayName, mode, effort, fastService string, planMode bool, content string, timestamp time.Time, userExchangeSeq int, userTurnID string) (*PendingUserMessage, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	} else {
		timestamp = timestamp.UTC()
	}
	state := h.ensurePendingSessionLocked(sessionKey)
	// Live send passes the identity selected by persistence. The seq-derived
	// fallback is only for compatibility callers that do not provide that value.
	turnID := strings.TrimSpace(userTurnID)
	if turnID == "" {
		turnID = session.NewUserTurnMindFSID(sessionKey, userExchangeSeq)
	}
	state.MindFSID = turnID
	state.RootID = rootID
	state.SessionTitle = strings.TrimSpace(sessionTitle)
	state.Active = true
	// 新的一轮开始了 ⇒ 上一轮的「等你回答」到此为止（用户已经在输入框回了，或者
	// 卡片回答走了 ws 那条清位）。不清的话本轮 done 时会退化成"又一条在等回答"。
	state.AwaitingAnswer = false
	state.AwaitingCallID = ""
	state.User = &PendingUserMessage{
		Agent:            agent,
		Model:            model,
		ModelDisplayName: modelDisplayName,
		Mode:             mode,
		Effort:           effort,
		FastService:      fastService,
		PlanMode:         planMode,
		Content:          content,
		Timestamp:        timestamp,
	}
	state.ReplyingList = nil
	state.BaseExchangeSeq = 0
	if userExchangeSeq > 0 {
		state.BaseExchangeSeq = userExchangeSeq
	}
	state.NextEventSeq = 0
	state.Summary = ""
	state.UpdatedAt = state.User.Timestamp
	h.clearReplayStatesForSessionLocked(sessionKey)
	return &PendingUserMessage{
		Agent:            state.User.Agent,
		Model:            state.User.Model,
		ModelDisplayName: state.User.ModelDisplayName,
		Mode:             state.User.Mode,
		Effort:           state.User.Effort,
		FastService:      state.User.FastService,
		PlanMode:         state.User.PlanMode,
		Content:          state.User.Content,
		Timestamp:        state.User.Timestamp,
	}, turnID
}

func (h *StreamHub) IsSessionReplying(sessionKey string) bool {
	if blank(sessionKey) {
		return false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	state := h.pendingSessions[sessionKey]
	return state != nil && state.Active
}

// MarkSessionAwaiting 记录/解除「有个 ask_user 提问还没回答」。
//
// 触发点是流事件本身（tool_call kind=ask_user 置位、tool_update complete 清位），
// 以及「用户已经用别的方式回答/另起一轮」的那几个入口 —— 三条路都必须清，
// 否则空心灯会永久挂在这条会话上（与 B 那类"清不掉的灯"是同一个坑）。
func (h *StreamHub) MarkSessionAwaiting(rootID, sessionKey, callID string, awaiting bool) {
	if blank(sessionKey) {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	state := h.pendingSessions[sessionKey]
	if awaiting {
		if state == nil {
			// 提问事件理论上总在某一轮的 pending 条目里到达（回合先置 pending 才有
			// stream），这里仍然兜一手：条目缺失时自建，RootID 留空即不进
			// ListReplyingSessions（那里的 blank(RootID) 闸门），不会凭空点亮实心灯。
			state = h.ensurePendingSessionLocked(sessionKey)
			state.RootID = rootID
			state.UpdatedAt = time.Now().UTC()
		}
		state.AwaitingAnswer = true
		state.AwaitingCallID = strings.TrimSpace(callID)
		return
	}
	if state == nil {
		return
	}
	state.AwaitingAnswer = false
	state.AwaitingCallID = ""
	// 提问是这条条目的**最后一个**存在理由：回合早结束（Active=false）、没有排队
	// 消息、提问也答完了 —— 那就该整条消失，否则它会一直挂在内存里。
	if !state.Active && len(state.Queue) == 0 {
		delete(h.pendingSessions, sessionKey)
		h.clearReplayStatesForSessionLocked(sessionKey)
	}
}

// AwaitingAnswer 报告这条会话是否在等用户回答（前端空心灯的唯一读数）。
func (h *StreamHub) AwaitingAnswer(sessionKey string) bool {
	if blank(sessionKey) {
		return false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	state := h.pendingSessions[sessionKey]
	return state != nil && state.AwaitingAnswer
}

// SetPendingReply 置入「子会话/续轮在回复」的 pending，返回该条目所属回合的 mindfs_id。
// 语义同 SetPendingUserAt：这个 id 必须交给终结器。
func (h *StreamHub) SetPendingReply(rootID, sessionKey, sessionTitle string, mindfsID ...string) string {
	if blank(sessionKey) {
		return ""
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	state := h.ensurePendingSessionLocked(sessionKey)
	turnID := ""
	if len(mindfsID) > 0 {
		turnID = strings.TrimSpace(mindfsID[0])
	}
	if turnID == "" {
		turnID = h.nextPendingTurnIDLocked()
	}
	state.MindFSID = turnID
	state.RootID = rootID
	state.SessionTitle = strings.TrimSpace(sessionTitle)
	state.Active = true
	// 同 setPendingUserAt：新回合开始即终结上一轮的"等回答"。
	state.AwaitingAnswer = false
	state.AwaitingCallID = ""
	if state.UpdatedAt.IsZero() {
		state.UpdatedAt = time.Now().UTC()
	}
	return turnID
}

// PendingSessionSnapshot 是 broadcastSessionDone 给 Web Push 通知用的会话快照。
//
// 这里**没有**「LastEventAt 超过 N 秒就报不活跃」的判据：真在跑的会话会长时间没有
// 事件（服务端不转发 SDK 的 keep_alive，唯一的周期事件只覆盖 tool 执行），按静默
// 判「已结束」是错的。快照只反映 pending 表当下状态 —— 条目在回合结束时被
// ClearSessionPending 删掉，快照自然就空了。
func (h *StreamHub) PendingSessionSnapshot(sessionKey string) PendingSessionSnapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()
	state := h.pendingSessions[sessionKey]
	if state == nil {
		return PendingSessionSnapshot{}
	}
	return PendingSessionSnapshot{
		RootID:       state.RootID,
		SessionTitle: state.SessionTitle,
		Summary:      state.Summary,
		UpdatedAt:    state.UpdatedAt,
	}
}

// PendingTurnIDMatches 报告「带 turnID 的终结请求是否属于当前 pending 条目」。
//
// 仅当**条目存在、且双方都有身份、且身份不一致**时返回 false —— 即这是一条
// 「别的回合」的迟到 done。其余情况（条目已不存在 / 没有身份）返回 true，
// 保持改造前行为，保证前端总能收到「清灯」的 done 帧。
//
// 返回 false 时，EndSessionTurn 会整条丢弃（不广播 done 帧、不推送、不清理）：
// 否则那条帧仍会让前端按 key 清掉新轮的灯（症状「灯不亮」的客户端一侧）。
func (h *StreamHub) PendingTurnIDMatches(sessionKey string, turnID string) bool {
	if blank(sessionKey) {
		return true
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	state := h.pendingSessions[sessionKey]
	if state == nil {
		return true
	}
	// 空串是「无身份」的哨兵，对位旧实现的 turnGen==0。
	if turnID == "" || state.MindFSID == "" {
		return true
	}
	return state.MindFSID == turnID
}
