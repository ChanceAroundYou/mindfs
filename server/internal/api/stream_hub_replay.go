package api

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/notify"
)

func (h *StreamHub) AppendReplyEvent(sessionKey string, event StreamEvent) StreamEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	state := h.ensurePendingSessionLocked(sessionKey)
	if strings.TrimSpace(event.MindFSID) == "" {
		event.MindFSID = state.MindFSID
	}
	state.NextEventSeq++
	event.EventCursor = formatEventCursor(state.BaseExchangeSeq, state.NextEventSeq)
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	state.UpdatedAt = event.Timestamp
	// 只数真正的工具调用：流式更新（userShell 的 ToolUpdate）是同一个调用的续传，
	// 数进来会让一条命令被记成很多次。回合结算时这个值决定 Router 的总结能不能
	// 成为任务结论，宁少勿多。
	if event.Type == string(agenttypes.EventTypeToolCall) {
		state.ToolCalls++
	}
	if coalesceUserShellStreamEvent(state, event) {
		// 保持旧行为：userShell 续传不重置 Summary（它在上面提前返回）。
		return event
	}
	// 连续文本块合并只在「本会话没有在途重放」时做。重放客户端拿到的是一帧
	// reset 快照，合并后内容逐字不变；而在途重放正在按 cursor 续投——把一个已经
	// 投递过的事件改成更大的累积快照，客户端会当成增量再拼一遍。
	if !h.hasActiveReplayLocked(sessionKey) && mergeContiguousChunkLocked(state, event) {
		updateReplySummaryLocked(state, event)
		state.trimReplyListLocked()
		return event
	}
	state.ReplyingList = append(state.ReplyingList, cloneEvent(event))
	state.replyBytes += approxReplyEventBytes(event)
	updateReplySummaryLocked(state, event)
	state.trimReplyListLocked()
	return event
}

// updateReplySummaryLocked 维护「本轮最后一段文本」：Summary 只用于回合结算时给
// Router 判断有没有实质产出，合并只改存储形状，不能改变它逐片累积的语义。
func updateReplySummaryLocked(state *SessionPendingState, event StreamEvent) {
	if event.Type == "message_chunk" {
		if chunk, ok := event.Data.(agenttypes.MessageChunk); ok {
			state.Summary = lastRunes(state.Summary+chunk.Content, notify.BodyMaxRunes)
		}
	} else if isAuxiliarySummaryBoundary(event.Type) {
		state.Summary = ""
	}
}

func formatEventCursor(baseExchangeSeq int, eventSeq uint64) string {
	return fmt.Sprintf("%d:%d", baseExchangeSeq, eventSeq)
}

func parseEventCursor(value string) (int, uint64, bool) {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) != 2 {
		return 0, 0, false
	}
	baseExchangeSeq, err := strconv.Atoi(parts[0])
	if err != nil || baseExchangeSeq < 0 {
		return 0, 0, false
	}
	eventSeq, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return baseExchangeSeq, eventSeq, true
}

func isAuxiliarySummaryBoundary(eventType string) bool {
	switch agenttypes.EventType(eventType) {
	case agenttypes.EventTypeThoughtChunk,
		agenttypes.EventTypeToolCall,
		agenttypes.EventTypeToolUpdate,
		agenttypes.EventTypeTodoUpdate,
		agenttypes.EventTypePlanUpdate,
		agenttypes.EventTypeCompact:
		return true
	default:
		return false
	}
}

const maxReplayUserShellStreamBytes = 256 * 1024

// ReplyingList 原先没有上限 —— 后台会话（看板/定时任务起的、没在浏览器里打开的）
// 没有绑定客户端，事件只进不出，整个回合全堆在内存里。长回合（几十分钟的 agent 工作）
// 能堆出上千条事件；单轮跑数小时、工具调用上千条时会到数万条，挂载会话时要在全局
// 锁内克隆/排序/序列化整份列表，表现为「打开卡死、别的会话也点不动」。
//
// 两道收口：
//   - 连续文本块在写入时合并（见 mergeContiguousChunkLocked），长文本流从 N 片变 1 片；
//   - 条数与近似字节双上限，超出丢最旧的（用户打开会话时最关心最近这段）。
//
// 丢掉的最早事件对「后挂上的客户端」不可见，但工具卡仍会从各自最新的 tool_update
// 重建；正在重放的客户端不受影响（裁剪只发生在追加路径）。
const (
	maxReplayEvents = 4000
	maxReplayBytes  = 16 << 20
)

// userShell 续传按 callID 向前找同一条调用。只扫尾部这么多条：找不到就新开一条，
// 客户端本来就会按 callID 合并，正确性不依赖服务端一定命中；而全表扫描会让长回合的
// 每条输出都变成 O(N)。
const maxUserShellCoalesceScan = 64

func coalesceUserShellStreamEvent(state *SessionPendingState, event StreamEvent) bool {
	if state == nil || event.Type != string(agenttypes.EventTypeToolUpdate) {
		return false
	}
	next, ok := event.Data.(agenttypes.ToolCall)
	if !ok || next.Meta == nil || next.Meta["source"] != "userShell" || next.Meta["phase"] != "stream" || next.CallID == "" {
		return false
	}
	floor := len(state.ReplyingList) - maxUserShellCoalesceScan
	if floor < 0 {
		floor = 0
	}
	for i := len(state.ReplyingList) - 1; i >= floor; i-- {
		prevEvent := state.ReplyingList[i]
		if prevEvent.Type != event.Type {
			continue
		}
		prev, ok := prevEvent.Data.(agenttypes.ToolCall)
		if !ok || prev.CallID != next.CallID || prev.Meta == nil || prev.Meta["source"] != "userShell" || prev.Meta["phase"] != "stream" {
			continue
		}
		merged := prev
		merged.Status = next.Status
		merged.Meta = map[string]any{}
		for key, value := range prev.Meta {
			merged.Meta[key] = value
		}
		for key, value := range next.Meta {
			merged.Meta[key] = value
		}
		merged.Meta["replaySnapshot"] = true
		text := toolCallText(prev.Content) + toolCallText(next.Content)
		if len(text) > maxReplayUserShellStreamBytes {
			text = text[len(text)-maxReplayUserShellStreamBytes:]
			merged.Meta["replayTruncated"] = true
			merged.Meta["replayTruncation"] = "tail"
		}
		merged.Content = []agenttypes.ToolCallContentItem{{Type: "text", Text: text}}
		state.replaceReplyEventLocked(i, event, merged)
		return true
	}
	return false
}

// mergeContiguousChunkLocked 把紧邻的同类文本块合并成一条：重放时长文本流从
// N 条事件变成 1 条。只比较最后一条，O(1)，且不会跨过 tool/message_done 等边界
// （边界事件类型不同，直接不合并）。
func mergeContiguousChunkLocked(state *SessionPendingState, event StreamEvent) bool {
	if state == nil || len(state.ReplyingList) == 0 {
		return false
	}
	i := len(state.ReplyingList) - 1
	prev := state.ReplyingList[i]
	if prev.Type != event.Type || prev.MindFSID != event.MindFSID {
		return false
	}
	switch event.Type {
	case string(agenttypes.EventTypeMessageChunk):
		a, okA := prev.Data.(agenttypes.MessageChunk)
		b, okB := event.Data.(agenttypes.MessageChunk)
		if !okA || !okB {
			return false
		}
		if a.MessageID != b.MessageID ||
			a.ParentToolUseID != b.ParentToolUseID ||
			a.TaskID != b.TaskID ||
			a.TaskDescription != b.TaskDescription ||
			a.SubagentType != b.SubagentType {
			return false
		}
		merged := a
		merged.Content = a.Content + b.Content
		state.replaceReplyEventLocked(i, event, merged)
		return true
	case string(agenttypes.EventTypeThoughtChunk):
		a, okA := prev.Data.(agenttypes.ThoughtChunk)
		b, okB := event.Data.(agenttypes.ThoughtChunk)
		if !okA || !okB {
			return false
		}
		if a.ID != b.ID ||
			a.ParentToolUseID != b.ParentToolUseID ||
			a.TaskID != b.TaskID ||
			a.TaskDescription != b.TaskDescription ||
			a.SubagentType != b.SubagentType {
			return false
		}
		merged := a
		merged.Content = a.Content + b.Content
		state.replaceReplyEventLocked(i, event, merged)
		return true
	}
	return false
}

// replaceReplyEventLocked 原位替换一条待发事件：位置（= 卡片顺序）不变，cursor 取
// 最新（续投循环据此判断哪些事件还没发过），字节计数保持增量一致。
func (state *SessionPendingState) replaceReplyEventLocked(i int, event StreamEvent, data any) {
	old := state.ReplyingList[i]
	replaced := StreamEvent{Type: old.Type, Data: data, EventCursor: event.EventCursor, Timestamp: event.Timestamp, MindFSID: event.MindFSID}
	state.replyBytes += approxReplyEventBytes(replaced) - approxReplyEventBytes(old)
	if state.replyBytes < 0 {
		state.replyBytes = 0
	}
	state.ReplyingList[i] = replaced
}

// trimReplyListLocked 把待发历史压回上限内：一次丢一批（至少 1/8），避免每个事件
// 都搬一次切片；永远保留最新一条。
func (state *SessionPendingState) trimReplyListLocked() {
	if len(state.ReplyingList) <= maxReplayEvents && state.replyBytes <= maxReplayBytes {
		return
	}
	drop := len(state.ReplyingList) - maxReplayEvents
	if chunk := len(state.ReplyingList) / 8; drop < chunk {
		drop = chunk
	}
	if drop < 1 {
		drop = 1
	}
	if drop >= len(state.ReplyingList) {
		drop = len(state.ReplyingList) - 1
	}
	if drop <= 0 {
		return
	}
	for i := 0; i < drop; i++ {
		state.replyBytes -= approxReplyEventBytes(state.ReplyingList[i])
	}
	if state.replyBytes < 0 {
		state.replyBytes = 0
	}
	copy(state.ReplyingList, state.ReplyingList[drop:])
	tail := state.ReplyingList[len(state.ReplyingList)-drop:]
	for i := range tail {
		tail[i] = StreamEvent{}
	}
	state.ReplyingList = state.ReplyingList[:len(state.ReplyingList)-drop]
}

// approxReplyEventBytes 是载荷的粗略上界，只用于控制上限，不要求与 JSON 逐字相等。
func approxReplyEventBytes(event StreamEvent) int {
	const overhead = 64
	switch data := event.Data.(type) {
	case agenttypes.MessageChunk:
		return overhead + len(data.Content)
	case agenttypes.ThoughtChunk:
		return overhead + len(data.Content)
	case agenttypes.ToolCall:
		size := overhead + len(data.Title) + len(data.RawType)
		for _, item := range data.Content {
			size += len(item.Text) + len(item.Path) + len(item.NewText)
			if item.OldText != nil {
				size += len(*item.OldText)
			}
		}
		for key, value := range data.Meta {
			size += len(key)
			if text, ok := value.(string); ok {
				size += len(text)
			}
		}
		return size
	default:
		return 256
	}
}

func toolCallText(items []agenttypes.ToolCallContentItem) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	for _, item := range items {
		b.WriteString(item.Text)
	}
	return b.String()
}

func (h *StreamHub) collectReplayStep(clientID, sessionKey string) replayStep {
	h.mu.Lock()
	step := h.nextReplayStepLocked(clientID, sessionKey)
	h.mu.Unlock()
	// 排序/克隆都作用在本步私有切片上，放到锁外做：重放挂载期间别的会话的流事件
	// 不必为这 O(N log N) 排队（切片是本次快照，state 里的事件不会被并发改写）。
	if len(step.events) > 1 {
		sort.SliceStable(step.events, func(i, j int) bool {
			_, left, _ := parseEventCursor(step.events[i].EventCursor)
			_, right, _ := parseEventCursor(step.events[j].EventCursor)
			return left < right
		})
	}
	return step
}

func (h *StreamHub) nextReplayStepLocked(clientID, sessionKey string) replayStep {
	clientKey := pendingClientKey(clientID, sessionKey)
	replay := h.replayStates[clientKey]
	if replay == nil {
		return replayStep{live: true}
	}
	state := h.pendingSessions[sessionKey]
	if state == nil {
		replay.Status = ClientStreamStatusLive
		return replayStep{live: true}
	}
	events := make([]StreamEvent, 0, len(state.ReplyingList))
	maxEventSeq := replay.LastEventSeq
	for _, event := range state.ReplyingList {
		baseExchangeSeq, eventSeq, ok := parseEventCursor(event.EventCursor)
		if !ok || baseExchangeSeq != state.BaseExchangeSeq || eventSeq <= replay.LastEventSeq {
			continue
		}
		events = append(events, cloneEvent(event))
		if eventSeq > maxEventSeq {
			maxEventSeq = eventSeq
		}
	}
	if len(events) == 0 {
		replay.Status = ClientStreamStatusLive
		return replayStep{live: true}
	}
	replay.LastEventSeq = maxEventSeq
	return replayStep{events: events}
}

func (h *StreamHub) replayStepToClient(rootID, clientID, sessionKey string, events []StreamEvent) {
	if len(events) == 0 {
		return
	}
	// **整批一条消息发出去。** 逐条发的话客户端每收一条就渲染一次，几百条事件就是
	// 几百次渲染 —— 用户看到的是「打开/切回会话时逐渐刷一大堆」。一条消息里循环应用
	// 会被 React 批处理成一次渲染，观感是一步到位。
	batch := make([]StreamEvent, len(events))
	copy(batch, events)
	h.SendToClient(clientID, buildSessionStreamBatchResponse(rootID, sessionKey, batch, false))
}

// replayQueueToClient 在客户端挂上会话时投一份**权威队列状态**。
//
// 队列帧只有这一条下发路径（BroadcastSessionQueueUpdated 是即时的、发完即忘），
// 所以这里不能按「队列非空才发」走：后台标签页错过「出队/清空」那一帧之后再挂上来，
// 拿到的仍是本地旧状态，改写的界面永远留着那条已经执行完的排队消息（2026-10-25 用户报
// 「排队样式没消失」）。空队列也必须发一帧，客户端据此把本地队列收敛成空。
func (h *StreamHub) replayQueueToClient(rootID, clientID, sessionKey string) {
	stateRoot, queue, frozen := h.queueSnapshot(sessionKey)
	if rootID == "" {
		rootID = stateRoot
	}
	if rootID == "" {
		return
	}
	h.SendToClient(clientID, buildSessionQueueUpdatedResponse(rootID, sessionKey, queue, frozen))
}

func (h *StreamHub) isReplayClientLocked(clientID, sessionKey string) bool {
	for _, replayKey := range h.getReplayKeyListLocked(sessionKey, clientID) {
		state := h.replayStates[replayKey]
		return state != nil && state.Status != ClientStreamStatusLive
	}
	return false
}

// hasActiveReplayLocked 判断该会话是否还有客户端在重放/续投。有的话写入路径不能
// 原地合并已投递事件（客户端会把累积快照当增量再拼一遍）。
func (h *StreamHub) hasActiveReplayLocked(sessionKey string) bool {
	if sessionKey == "" || len(h.replayStates) == 0 {
		return false
	}
	suffix := "::" + sessionKey
	for key, state := range h.replayStates {
		if state == nil || state.Status == ClientStreamStatusLive {
			continue
		}
		if strings.HasSuffix(key, suffix) {
			return true
		}
	}
	return false
}

func (h *StreamHub) getReplayKeyListLocked(sessionKey, clientID string) []string {
	if len(h.replayStates) == 0 {
		return nil
	}
	keys := make([]string, 0, len(h.replayStates))
	for replayKey := range h.replayStates {
		if sessionKey != "" && !strings.HasSuffix(replayKey, "::"+sessionKey) {
			continue
		}
		if clientID != "" && !strings.HasPrefix(replayKey, clientID+"::") {
			continue
		}
		keys = append(keys, replayKey)
	}
	return keys
}
