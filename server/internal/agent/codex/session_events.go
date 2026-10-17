package codex

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"

	types "mindfs/server/internal/agent/types"

	codexsdk "github.com/fanwenlin/codex-go-sdk/codex"
	codextypes "github.com/fanwenlin/codex-go-sdk/types"
)

// handleStreamedEvents 消费一轮的事件流，返回 (是否看到了 TurnCompletedEvent, error)。
//
// 关键：**通道关闭 ≠ 回合成功**。进程中途死亡时通道同样会关闭，旧实现直接 `return nil`
// 会把「中途死掉」当成功上报（与 Claude 侧 failPendingTurns 的处理不对称）。调用方
// SendMessage 据此判定：通道关闭但没见过 TurnCompleted ⇒ 返回错误。
// 长连接订阅（SubscribeThreadEvents）不要求 TurnCompleted，忽略第一个返回值即可。
func (s *session) handleStreamedEvents(events <-chan codexsdk.ThreadEvent) (bool, error) {
	textByID := map[string]string{}
	sawTurnCompleted := false
	for event := range events {
		raw, _ := json.Marshal(event)
		switch e := event.(type) {
		case *codexsdk.ThreadStartedEvent:
			s.setThreadID(e.ThreadId)
		case *codexsdk.TurnStartedEvent:
			continue
		case *codexsdk.ThreadGoalUpdatedEvent:
			s.setThreadID(e.ThreadID)
			continue
		case *codexsdk.ThreadGoalClearedEvent:
			s.setThreadID(e.ThreadID)
			continue
		case *codexsdk.TurnDiffUpdatedEvent:
			continue
		case *codexsdk.ItemStartedEvent:
			s.logRawToolItem(e.Item)
			if _, ok := e.Item.(*codexsdk.AgentMessageItem); ok {
				continue
			}
			if s.handleNonToolItem(e.Item, true) {
				continue
			}
			if toolCall, ok := mapToolItem(e.Item, true); ok {
				s.emit(types.Event{Type: types.EventTypeToolCall, SessionID: s.SessionID(), Data: toolCall})
				continue
			}
			logUnhandledEvent(s.sessionKey, "item.started", raw)
		case *codexsdk.ItemUpdatedEvent:
			s.logRawToolItem(e.Item)
			if toolCall, ok := mapToolItem(e.Item, false); ok {
				s.emit(types.Event{Type: types.EventTypeToolUpdate, SessionID: s.SessionID(), Data: toolCall})
				continue
			}
			if s.handleNonToolItem(e.Item, false) {
				continue
			}
			msg, ok := e.Item.(*codexsdk.AgentMessageItem)
			if !ok {
				logUnhandledEvent(s.sessionKey, "item.updated", raw)
				continue
			}
			s.emitMessageDelta(msg, textByID)
		case *codexsdk.ItemCompletedEvent:
			s.logRawToolItem(e.Item)
			if toolCall, ok := mapToolItem(e.Item, false); ok {
				s.emit(types.Event{Type: types.EventTypeToolUpdate, SessionID: s.SessionID(), Data: toolCall})
				continue
			}
			if s.handleNonToolItem(e.Item, false) {
				continue
			}
			msg, ok := e.Item.(*codexsdk.AgentMessageItem)
			if ok {
				s.emitMessageDelta(msg, textByID)
				continue
			}
			if thought, ok := e.Item.(*codexsdk.ReasoningItem); ok {
				summary := strings.TrimSpace(strings.Join(thought.Summary, "\n"))
				if summary != "" {
					s.emit(types.Event{Type: types.EventTypeThoughtChunk, SessionID: s.SessionID(), Data: types.ThoughtChunk{Content: summary}})
				}
				continue
			}
			logUnhandledEvent(s.sessionKey, "item.completed", raw)
		case *codexsdk.TurnCompletedEvent:
			sawTurnCompleted = true
			s.updateThreadIDFromThread()
			log.Printf("[agent/codex] output.done session=%s", s.sessionKey)
			contextWindow, _ := s.ContextWindow(context.Background())
			tokenUsage := codexTokenUsage(e.Usage)
			s.emit(types.Event{
				Type:      types.EventTypeMessageDone,
				SessionID: s.SessionID(),
				Data:      types.MessageDone{ContextWindow: contextWindow, TokenUsage: tokenUsage},
			})
		case *codexsdk.TurnFailedEvent:
			log.Printf("[agent/codex] send.error session=%s err=%s", s.sessionKey, e.Error.Message)
			return false, errors.New("codex turn failed: " + e.Error.Message)
		case *codexsdk.ThreadErrorEvent:
			log.Printf("[agent/codex] send.error session=%s err=%s", s.sessionKey, e.Message)
			return false, errors.New("codex thread error: " + e.Message)
		case *codexsdk.RawEvent:
			if s.handleRawEvent(e) {
				continue
			}
			logUnhandledEvent(s.sessionKey, "raw_event", raw)
		default:
			logUnhandledEvent(s.sessionKey, "event", raw)
		}
	}
	return sawTurnCompleted, nil
}

func (s *session) handleNonToolItem(item codexsdk.ThreadItem, started bool) bool {
	switch v := item.(type) {
	case *codexsdk.ReasoningItem:
		if started {
			return true
		}
		summary := strings.TrimSpace(strings.Join(v.Summary, "\n"))
		if summary != "" {
			s.emit(types.Event{Type: types.EventTypeThoughtChunk, SessionID: s.SessionID(), Data: types.ThoughtChunk{ID: v.ID, Content: summary}})
		}
		return true
	case *codexsdk.TodoListItem:
		items := make([]types.TodoItem, 0, len(v.Items))
		for _, item := range v.Items {
			content := strings.TrimSpace(item.Text)
			if content == "" {
				continue
			}
			status := "pending"
			if item.Completed {
				status = "completed"
			}
			items = append(items, types.TodoItem{Content: content, Status: status})
		}
		s.emit(types.Event{Type: types.EventTypeTodoUpdate, SessionID: s.SessionID(), Data: types.TodoUpdate{Items: items}})
		return true
	case *codexsdk.CompactedItem:
		status := "complete"
		if started {
			status = "running"
		}
		s.emit(types.Event{Type: types.EventTypeCompact, SessionID: s.SessionID(), Data: types.CompactNotice{ID: v.ID, Status: status, Summary: v.Summary}})
		return true
	case *codextypes.UnknownItem:
		switch v.GetType() {
		case "plan":
			plan, ok := parseUnknownPlan(v.Raw)
			if ok {
				s.emit(types.Event{Type: types.EventTypePlanUpdate, SessionID: s.SessionID(), Data: plan})
			}
			return true
		case "contextCompaction":
			compact := parseUnknownContextCompaction(v.Raw)
			s.emit(types.Event{Type: types.EventTypeCompact, SessionID: s.SessionID(), Data: compact})
			return true
		}
		return false
	default:
		switch item.GetType() {
		case "plan":
			return true
		case "contextCompaction":
			s.emit(types.Event{Type: types.EventTypeCompact, SessionID: s.SessionID(), Data: types.CompactNotice{Status: "complete"}})
			return true
		}
		return false
	}
}
