package api

import (
	"strings"
	"time"
)

func cloneQueue(queue []QueuedUserMessage) []QueuedUserMessage {
	if len(queue) == 0 {
		return nil
	}
	out := make([]QueuedUserMessage, len(queue))
	copy(out, queue)
	return out
}

func (h *StreamHub) queueSnapshot(sessionKey string) (string, []QueuedUserMessage, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	state := h.pendingSessions[sessionKey]
	if state == nil {
		return "", nil, false
	}
	return state.RootID, cloneQueue(state.Queue), state.QueueFrozen
}

func (h *StreamHub) EnqueueSessionMessage(rootID, sessionKey, sessionTitle string, item QueuedUserMessage) []QueuedUserMessage {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.enqueueSessionMessageLocked(rootID, sessionKey, sessionTitle, item)
}

// Reserve a turn before starting its goroutine so concurrent HTTP and WS
// submissions queue behind the same active conversation.
func (h *StreamHub) reserveOrQueueSessionMessage(rootID, key, title string, item QueuedUserMessage) ([]QueuedUserMessage, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	state := h.ensurePendingSessionLocked(key)
	if state.Active {
		return h.enqueueSessionMessageLocked(rootID, key, title, item), true
	}
	state.RootID, state.SessionTitle, state.Active = rootID, title, true
	state.UpdatedAt = time.Now().UTC()
	return nil, false
}

func (h *StreamHub) enqueueSessionMessageLocked(rootID, sessionKey, sessionTitle string, item QueuedUserMessage) []QueuedUserMessage {
	if item.ID == "" {
		item.ID = time.Now().UTC().Format("20060102150405.000000000")
	}
	if item.Timestamp.IsZero() {
		item.Timestamp = time.Now().UTC()
	}
	state := h.ensurePendingSessionLocked(sessionKey)
	state.RootID = rootID
	if strings.TrimSpace(sessionTitle) != "" {
		state.SessionTitle = strings.TrimSpace(sessionTitle)
	}
	state.Queue = append(state.Queue, item)
	state.UpdatedAt = item.Timestamp
	h.clearReplayStatesForSessionLocked(sessionKey)
	return cloneQueue(state.Queue)
}

func (h *StreamHub) RemoveQueuedSessionMessage(sessionKey, queueID string) []QueuedUserMessage {
	h.mu.Lock()
	defer h.mu.Unlock()
	state := h.pendingSessions[sessionKey]
	if state == nil || queueID == "" {
		return nil
	}
	next := state.Queue[:0]
	for _, item := range state.Queue {
		if item.ID == queueID {
			continue
		}
		next = append(next, item)
	}
	state.Queue = next
	state.UpdatedAt = time.Now().UTC()
	h.clearReplayStatesForSessionLocked(sessionKey)
	return cloneQueue(state.Queue)
}

func (h *StreamHub) UpdateQueuedSessionMessage(sessionKey, queueID, content string) []QueuedUserMessage {
	h.mu.Lock()
	defer h.mu.Unlock()
	state := h.pendingSessions[sessionKey]
	if state == nil || queueID == "" {
		return nil
	}
	for i := range state.Queue {
		if state.Queue[i].ID == queueID {
			state.Queue[i].Content = content
			break
		}
	}
	state.UpdatedAt = time.Now().UTC()
	h.clearReplayStatesForSessionLocked(sessionKey)
	return cloneQueue(state.Queue)
}

func (h *StreamHub) FreezeQueuedSessionMessages(sessionKey string) ([]QueuedUserMessage, bool) {
	if blank(sessionKey) {
		return nil, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	state := h.pendingSessions[sessionKey]
	if state == nil || len(state.Queue) == 0 {
		return nil, false
	}
	state.QueueFrozen = true
	state.UpdatedAt = time.Now().UTC()
	return cloneQueue(state.Queue), true
}

func (h *StreamHub) UnfreezeQueuedSessionMessages(sessionKey string) ([]QueuedUserMessage, bool) {
	if blank(sessionKey) {
		return nil, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	state := h.pendingSessions[sessionKey]
	if state == nil || !state.QueueFrozen {
		if state == nil {
			return nil, false
		}
		return cloneQueue(state.Queue), false
	}
	state.QueueFrozen = false
	state.UpdatedAt = time.Now().UTC()
	return cloneQueue(state.Queue), true
}

func (h *StreamHub) PopQueuedSessionMessage(sessionKey, queueID string) (QueuedUserMessage, []QueuedUserMessage, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	state := h.pendingSessions[sessionKey]
	if state == nil || len(state.Queue) == 0 {
		return QueuedUserMessage{}, nil, false
	}
	if state.Active {
		return QueuedUserMessage{}, cloneQueue(state.Queue), false
	}
	index := 0
	if trimmedQueueID := strings.TrimSpace(queueID); trimmedQueueID != "" {
		index = -1
		for i := range state.Queue {
			if state.Queue[i].ID == trimmedQueueID {
				index = i
				break
			}
		}
		if index < 0 {
			return QueuedUserMessage{}, cloneQueue(state.Queue), false
		}
	} else if state.QueueFrozen {
		return QueuedUserMessage{}, cloneQueue(state.Queue), false
	}
	item := state.Queue[index]
	state.Active = true
	state.Queue = append(state.Queue[:index], state.Queue[index+1:]...)
	state.UpdatedAt = time.Now().UTC()
	h.clearReplayStatesForSessionLocked(sessionKey)
	return item, cloneQueue(state.Queue), true
}

func (h *StreamHub) PromoteQueuedSessionMessage(sessionKey, queueID string) ([]QueuedUserMessage, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	state := h.pendingSessions[sessionKey]
	if state == nil || len(state.Queue) == 0 || strings.TrimSpace(queueID) == "" {
		return nil, false
	}
	index := -1
	for i := range state.Queue {
		if state.Queue[i].ID == queueID {
			index = i
			break
		}
	}
	if index < 0 {
		return cloneQueue(state.Queue), false
	}
	if index > 0 {
		item := state.Queue[index]
		copy(state.Queue[1:index+1], state.Queue[0:index])
		state.Queue[0] = item
	}
	state.QueueFrozen = false
	state.UpdatedAt = time.Now().UTC()
	h.clearReplayStatesForSessionLocked(sessionKey)
	return cloneQueue(state.Queue), true
}

func (h *StreamHub) BroadcastSessionQueueUpdated(rootID, sessionKey string, queue []QueuedUserMessage) {
	_, _, frozen := h.queueSnapshot(sessionKey)
	resp := buildSessionQueueUpdatedResponse(rootID, sessionKey, queue, frozen)
	for _, clientID := range h.GetSessionClientIDs(sessionKey, false) {
		h.SendToClient(clientID, resp)
	}
}
