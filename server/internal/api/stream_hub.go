package api

import (
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
)

type StreamHub struct {
	mu              sync.RWMutex
	clients         map[string]*websocket.Conn
	connLocks       map[*websocket.Conn]*sync.Mutex
	clientNodes     map[string]string
	sessionClients  map[string]map[string]struct{}
	pendingSessions map[string]*SessionPendingState
	replayStates    map[string]*ClientReplayState
}

func NewStreamHub() *StreamHub {
	return &StreamHub{
		clients:         make(map[string]*websocket.Conn),
		connLocks:       make(map[*websocket.Conn]*sync.Mutex),
		clientNodes:     make(map[string]string),
		sessionClients:  make(map[string]map[string]struct{}),
		pendingSessions: make(map[string]*SessionPendingState),
		replayStates:    make(map[string]*ClientReplayState),
	}
}

func pendingClientKey(clientID, sessionKey string) string {
	return clientID + "::" + sessionKey
}

func (h *StreamHub) clearReplayStatesForSessionLocked(sessionKey string) {
	for _, replayKey := range h.getReplayKeyListLocked(sessionKey, "") {
		delete(h.replayStates, replayKey)
	}
}

func (h *StreamHub) RegisterClient(clientID string, conn *websocket.Conn) *websocket.Conn {
	if blank(clientID) || conn == nil {
		return nil
	}
	h.mu.Lock()
	previous := h.clients[clientID]
	h.clients[clientID] = conn
	if _, ok := h.connLocks[conn]; !ok {
		h.connLocks[conn] = &sync.Mutex{}
	}
	h.mu.Unlock()
	if previous == conn {
		return nil
	}
	return previous
}

func (h *StreamHub) RegisterClientWithNode(clientID string, conn *websocket.Conn, nodeID string) *websocket.Conn {
	if blank(clientID) || conn == nil {
		return nil
	}
	nodeID = h.normalizeNodeID(nodeID)
	h.mu.Lock()
	previous := h.clients[clientID]
	h.clients[clientID] = conn
	if _, ok := h.connLocks[conn]; !ok {
		h.connLocks[conn] = &sync.Mutex{}
	}
	if nodeID != "" {
		if h.clientNodes == nil {
			h.clientNodes = make(map[string]string)
		}
		h.clientNodes[clientID] = nodeID
	}
	h.mu.Unlock()
	if previous == conn {
		return nil
	}
	return previous
}

func (h *StreamHub) normalizeNodeID(v string) string {
	return strings.TrimSpace(v)
}

func (h *StreamHub) UnregisterClient(clientID string, conn *websocket.Conn) {
	if blank(clientID) {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	existing := h.clients[clientID]
	delete(h.connLocks, conn)
	if existing != conn {
		return
	}
	delete(h.clients, clientID)
	delete(h.clientNodes, clientID)
	for sessionKey, clientSet := range h.sessionClients {
		delete(clientSet, clientID)
		if len(clientSet) == 0 {
			delete(h.sessionClients, sessionKey)
		}
	}
	for _, replayKey := range h.getReplayKeyListLocked("", clientID) {
		delete(h.replayStates, replayKey)
	}
}

func (h *StreamHub) BindSessionClient(sessionKey, clientID string) {
	if blank(sessionKey) || blank(clientID) {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[clientID]; !ok {
		return
	}
	clientSet := h.sessionClients[sessionKey]
	if clientSet == nil {
		clientSet = make(map[string]struct{})
		h.sessionClients[sessionKey] = clientSet
	}
	clientSet[clientID] = struct{}{}
}

func (h *StreamHub) GetSessionClientIDs(sessionKey string, liveOnly bool) []string {
	if blank(sessionKey) {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	clientSet := h.sessionClients[sessionKey]
	if len(clientSet) == 0 {
		return nil
	}
	out := make([]string, 0, len(clientSet))
	for clientID := range clientSet {
		if h.clients[clientID] == nil {
			continue
		}
		if liveOnly && h.isReplayClientLocked(clientID, sessionKey) {
			continue
		}
		out = append(out, clientID)
	}
	return out
}

func (h *StreamHub) getAllClientIDs() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if len(h.clients) == 0 {
		return nil
	}
	clientIDs := make([]string, 0, len(h.clients))
	for clientID, conn := range h.clients {
		if conn != nil {
			clientIDs = append(clientIDs, clientID)
		}
	}
	return clientIDs
}

func (h *StreamHub) ListReplyingSessions() []ReplyingSessionState {
	h.mu.RLock()
	defer h.mu.RUnlock()
	items := make([]ReplyingSessionState, 0, len(h.pendingSessions))
	for sessionKey, state := range h.pendingSessions {
		if state == nil || blank(sessionKey) || blank(state.RootID) {
			continue
		}
		// Active 与 AwaitingAnswer 任一为真即纳入：这是「sidecar 状态」的统一出口，
		// 客户端按 AwaitingAnswer 区分空心灯、按 Active/Status 区分实心灯。
		// 之前 awaiting 被排除在外（怕点亮实心灯），代价是跨节点轮询永远看不到它。
		if !state.Active && !state.AwaitingAnswer {
			continue
		}
		status := ""
		if state.Active {
			status = "replying"
		}
		items = append(items, ReplyingSessionState{
			RootID:         state.RootID,
			SessionKey:     sessionKey,
			SessionTitle:   state.SessionTitle,
			Status:         status,
			Summary:        state.Summary,
			UpdatedAt:      state.UpdatedAt,
			AwaitingAnswer: state.AwaitingAnswer,
		})
	}
	return items
}

// ReplayPending 把一个刚挂上会话的客户端从「重放」推进到「实时」。
//
// **不从客户端取任何进度**：每次挂上都把当前回合的事件整份重发（reset 帧，见
// buildSessionStreamBatchResponse）。曾经这里接受客户端自报的 event_cursor 做增量续传，
// 那条路被删掉了 —— 它要求客户端缓存与服务端游标严格同步，而这条不变量在这套系统里
// 维持不住（游标恒为空 ⇒ 每次都是全量重发，客户端却按「增量」去 merge，于是同一段
// 正文被拼进一行新的 agent 行，表现为「ask 卡下面多出一整份正文」）。
//
// 排空循环本身保留：`GetSessionClientIDs(liveOnly=true)` 会跳过 replay 状态的客户端，
// 所以「取快照 → 排空期间新到的事件 → 切 live」之间不丢事件、不插序。
func (h *StreamHub) ReplayPending(rootID, clientID, sessionKey string) {
	h.mu.Lock()
	h.replayStates[pendingClientKey(clientID, sessionKey)] = &ClientReplayState{
		Status: ClientStreamStatusReplay,
	}
	h.mu.Unlock()

	h.replayQueueToClient(rootID, clientID, sessionKey)
	first := true
	for {
		step := h.collectReplayStep(clientID, sessionKey)
		if first {
			// 首帧**恒发**（events 可空）且带 reset：让「ready 之后尾巴 = 服务端 buffer」
			// 无条件成立，而不是「buffer 恰好非空时才成立」。
			h.SendToClient(clientID, buildSessionStreamBatchResponse(rootID, sessionKey, step.events, true))
			first = false
		} else {
			h.replayStepToClient(rootID, clientID, sessionKey, step.events)
		}
		if step.live {
			return
		}
	}
}

func (h *StreamHub) HasReplayClients(rootID, sessionKey string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, replayKey := range h.getReplayKeyListLocked(sessionKey, "") {
		replay := h.replayStates[replayKey]
		if replay != nil && replay.Status == ClientStreamStatusReplay {
			return true
		}
	}
	return false
}

// ClearSessionPending 删除一条 pending —— 但**只清属于回合 turnID 的那一条**。
//
// 这是「在回复」状态与回合生命周期绑定的关键：条目记录 MindFSID，终结器带上同一个
// id 才生效。于是：
//   - 第 A 轮迟到的 done（id=A）落在第 B 轮已开始（条目 MindFSID=B）之后 → id 不匹配
//     → no-op，B 轮的灯不被误抹（症状「正在运行但灯不亮」的成因）。
//   - 重复的 done 天然幂等（条目已删 / id 已换 → no-op）。
//
// turnID=="" 表示调用方没有身份（旧调用点、或该轮从未置过 pending）：此时按
// 「两侧都有身份才比对」的规则保守清掉，保持改造前的行为，绝不因缺身份而漏清
// （漏清 = 灯不灭）。
func (h *StreamHub) ClearSessionPending(sessionKey string, turnID string) {
	if blank(sessionKey) {
		return
	}
	// 超时兜底：replay 客户端排不空时不能永久卡住 broadcastSessionDone。
	// 2s 是经验值：正常 replay 排空是毫秒级，2s 足够慢网络下完成；
	// 超过 2s 说明客户端已失联（后台标签页、WS 写阻塞、连接已断但清理未跑），
	// 此时必须强制清，否则 session.done 永远发不出去、pending 永远 true。
	const timeout = 2 * time.Second
	deadline := time.Now().Add(timeout)
	for h.HasReplayClients("", sessionKey) {
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	state := h.pendingSessions[sessionKey]
	if state == nil {
		return
	}
	// 两侧都有身份且不一致 ⇒ 这是**别的回合**的 done，放弃（迟到/重复 done 的护栏）。
	if turnID != "" && state.MindFSID != "" && state.MindFSID != turnID {
		return
	}
	// 有未回答的提问 ⇒ 降级保留，不删：回合是结束了（灯不能再是实心），但 agent 在
	// 等用户，这条会话远没到"全闲"。删掉就等于把"等你回答"和"任务做完"画成同一个样子，
	// 那正是 C 要修的观感。
	if state.AwaitingAnswer {
		state.Active = false
		state.User = nil
		state.ReplyingList = nil
		state.Summary = ""
		state.UpdatedAt = time.Now().UTC()
		h.clearReplayStatesForSessionLocked(sessionKey)
		return
	}
	if len(state.Queue) > 0 {
		state.Active = false
		state.User = nil
		state.ReplyingList = nil
		state.Summary = ""
		state.UpdatedAt = time.Now().UTC()
	} else {
		delete(h.pendingSessions, sessionKey)
	}
	h.clearReplayStatesForSessionLocked(sessionKey)
}

func (h *StreamHub) SendToClient(clientID string, resp WSResponse) {
	if blank(clientID) {
		return
	}
	h.mu.RLock()
	conn := h.clients[clientID]
	h.mu.RUnlock()
	if conn == nil {
		return
	}
	_ = h.WriteJSON(clientID, conn, resp)
}

func (h *StreamHub) BroadcastAll(resp WSResponse) {
	for _, clientID := range h.getAllClientIDs() {
		h.SendToClient(clientID, resp)
	}
}

func (h *StreamHub) BroadcastSessionStream(rootID, sessionKey string, event *StreamEvent) {
	if event == nil {
		return
	}
	stored := h.AppendReplyEvent(sessionKey, *event)
	for _, clientID := range h.GetSessionClientIDs(sessionKey, true) {
		resp := buildSessionStreamResponse(rootID, sessionKey, &stored)
		h.SendToClient(clientID, resp)
	}
}

// broadcastSessionDone 广播一轮的结束。
//
// **刻意未导出**：带回合身份的收尾只有 `AppContext.EndSessionTurn` 一条合法路径
// —— 它先过 `PendingTurnIDMatches` 身份门再调这里。导出它等于又开一个「无身份也能
// 广播 done」的入口：前端收到 done 就按 key 清 pending，绕过身份门就会把新轮的灯误清
// （症状「正在运行但会话列表灯不亮」）。用编译器守住这条，比靠源码扫描某个调用点硬。
//
// `liveOnly=false`：**重放中的客户端也要收到**。它挂在回合中途、drain 还没走完时
// 这一轮结束了，这条就是它的终止信号 —— 因此不需要另开一条「补发你错过的 done」的
// 回执路径（曾有 completed 表 + done(replay:true)，现已删除：客户端对 replay 回执的两处
// 分支本来就都是空操作 —— 不放提示音、不触发重锚定，而重锚定正是它自己造成的自持闭环）。
//
// 「挂上来时这一轮早就结束了」的客户端不需要补偿：它的「在回复」状态取自 pending 列表，
// 而那时该会话早已不在列表里。**注意 `notifySessionDone` 不是这条路径** —— 它是 Web Push
// 推送，与 WS 的 pending 状态无关，别拿它当理由（写这段时错过一次）。
func (h *StreamHub) broadcastSessionDone(rootID, sessionKey, requestID string) {
	resp := buildSessionDoneResponse(rootID, sessionKey, requestID)
	for _, clientID := range h.GetSessionClientIDs(sessionKey, false) {
		h.SendToClient(clientID, resp)
	}
}

func (h *StreamHub) BroadcastSessionUserMessageAt(
	rootID string,
	sessionKey string,
	sessionType string,
	sessionName string,
	agentName string,
	model string,
	modelDisplayName string,
	mode string,
	effort string,
	fastService string,
	planMode bool,
	content string,
	timestamp time.Time,
	userExchangeSeq int,
	userTurnID string,
	excludeClientID string,
	queued bool,
) string {
	pendingUser, turnID := h.setPendingUserAt(rootID, sessionKey, sessionName, agentName, model, modelDisplayName, mode, effort, fastService, planMode, content, timestamp, userExchangeSeq, userTurnID)
	resp := buildSessionUserMessageResponse(rootID, sessionKey, sessionType, sessionName, agentName, model, modelDisplayName, mode, effort, fastService, planMode, content, pendingUser.Timestamp, queued, userExchangeSeq, turnID)
	for _, clientID := range h.GetSessionClientIDs(sessionKey, false) {
		if excludeClientID != "" && clientID == excludeClientID {
			continue
		}
		h.SendToClient(clientID, resp)
	}
	return turnID
}

func lastRunes(value string, max int) string {
	if max <= 0 || value == "" {
		return ""
	}
	if utf8.RuneCountInString(value) <= max {
		return value
	}
	runes := []rune(value)
	return "..." + string(runes[len(runes)-max:])
}

func (h *StreamHub) WriteJSON(clientID string, conn *websocket.Conn, value any) error {
	if conn == nil {
		return nil
	}
	lock := h.getConnLock(conn)
	lock.Lock()
	defer lock.Unlock()
	return conn.WriteJSON(value)
}

func (h *StreamHub) getConnLock(conn *websocket.Conn) *sync.Mutex {
	h.mu.RLock()
	lock := h.connLocks[conn]
	h.mu.RUnlock()
	if lock != nil {
		return lock
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if existing := h.connLocks[conn]; existing != nil {
		return existing
	}
	created := &sync.Mutex{}
	h.connLocks[conn] = created
	return created
}
