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
	if coalesceUserShellStreamEvent(state, event) {
		return event
	}
	state.ReplyingList = append(state.ReplyingList, cloneEvent(event))
	if event.Type == "message_chunk" {
		if chunk, ok := event.Data.(agenttypes.MessageChunk); ok {
			state.Summary = lastRunes(state.Summary+chunk.Content, notify.BodyMaxRunes)
		}
	} else if isAuxiliarySummaryBoundary(event.Type) {
		state.Summary = ""
	}
	return event
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

// ReplyingList **原先没有上限** —— 后台会话（看板/定时任务起的、没在浏览器里打开的）
// 没有绑定客户端，事件只进不出，整个回合全堆在内存里。长回合（几十分钟的 agent 工作）
// 能堆出上千条事件。这里补一条条数上限：超出就丢最旧的，保留最近的（用户打开会话时
// 最关心的也是最近这段）。被丢的事件由会话快照兜底 —— 挂载会话是「快照重建」，
// 客户端拿到的 exchanges 不依赖这份 buffer。
const maxReplayEvents = 2000

func coalesceUserShellStreamEvent(state *SessionPendingState, event StreamEvent) bool {
	if state == nil || event.Type != string(agenttypes.EventTypeToolUpdate) {
		return false
	}
	next, ok := event.Data.(agenttypes.ToolCall)
	if !ok || next.Meta == nil || next.Meta["source"] != "userShell" || next.Meta["phase"] != "stream" || next.CallID == "" {
		return false
	}
	for i := len(state.ReplyingList) - 1; i >= 0; i-- {
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
		state.ReplyingList[i] = StreamEvent{Type: event.Type, Data: merged, EventCursor: event.EventCursor, Timestamp: event.Timestamp, MindFSID: event.MindFSID}
		return true
	}
	return false
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
	defer h.mu.Unlock()
	return h.nextReplayStepLocked(clientID, sessionKey)
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
	sort.SliceStable(events, func(i, j int) bool {
		_, left, _ := parseEventCursor(events[i].EventCursor)
		_, right, _ := parseEventCursor(events[j].EventCursor)
		return left < right
	})
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

func (h *StreamHub) replayQueueToClient(rootID, clientID, sessionKey string) {
	stateRoot, queue, frozen := h.queueSnapshot(sessionKey)
	if rootID == "" {
		rootID = stateRoot
	}
	if rootID == "" || len(queue) == 0 {
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
