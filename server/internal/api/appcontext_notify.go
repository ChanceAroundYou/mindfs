package api

import (
	"context"
	"strings"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/notify"
	"mindfs/server/internal/session"
)

func (s *AppContext) notifySessionDone(rootID, sessionKey, requestID string, pending PendingSessionSnapshot) {
	if s == nil {
		return
	}
	if strings.HasPrefix(strings.TrimSpace(requestID), "scheduled:") {
		return
	}
	rootTitle := s.rootTitle(rootID)
	sessionTitle := strings.TrimSpace(pending.SessionTitle)
	if sessionTitle == "" {
		sessionTitle = s.sessionTitle(rootID, sessionKey)
	}
	summary := strings.TrimSpace(pending.Summary)
	eventID := strings.TrimSpace(requestID)
	if eventID == "" {
		eventID = "session.done:" + rootID + ":" + sessionKey + ":" + pending.UpdatedAt.Format(time.RFC3339Nano)
	}
	payload := notify.BuildSessionPayload(notify.SessionNotification{
		Type:         "session.done",
		RootID:       rootID,
		RootTitle:    rootTitle,
		SessionKey:   sessionKey,
		SessionTitle: sessionTitle,
		Summary:      summary,
		EventID:      eventID,
	})
	s.notifyPayload(context.Background(), eventID, payload)
}

func (s *AppContext) notifyAskUserIfNeeded(rootID, sessionKey string, event *StreamEvent) {
	if s == nil || event == nil || event.Type != string(agenttypes.EventTypeToolCall) {
		return
	}
	toolCall, ok := event.Data.(agenttypes.ToolCall)
	if !ok || toolCall.Kind != agenttypes.ToolKindAskUser {
		return
	}
	payload := notify.BuildSessionPayload(notify.SessionNotification{
		Type:         "session.ask_user",
		RootID:       rootID,
		RootTitle:    s.rootTitle(rootID),
		SessionKey:   sessionKey,
		SessionTitle: s.sessionTitle(rootID, sessionKey),
		Summary:      askUserSummary(toolCall),
		EventID:      "session.ask_user:" + rootID + ":" + sessionKey + ":" + toolCall.CallID,
	})
	s.notifyPayload(context.Background(), notify.EventID(payload), payload)
}

// askUserStreamEvent 从流事件里认出 ask_user 的生命周期节点：awaiting=true 是「提问
// 刚刚发出」，false 是「提问已答完」。两个消费者（看板任务的 aux 标记、会话空心灯）
// 共用它 —— 判据复制两份的话，"一个改对了另一个还是死的"必然再发生一次。
//
// ⚠️ event.Type 是 **StreamEvent** 的类型（updateToEvent 的产物），不是
// agenttypes.EventType：同一个 tool_update 在流里叫 "tool_call_update"。早先这里按
// agenttypes.EventTypeToolUpdate（"tool_update"）比较，分支永远不匹配 —— 任务卡上的
// ask_user_waiting 只置位、清位是死代码。
func askUserStreamEvent(event *StreamEvent) (callID string, awaiting bool, ok bool) {
	if event == nil {
		return "", false, false
	}
	switch event.Type {
	case "tool_call":
		toolCall, isToolCall := event.Data.(agenttypes.ToolCall)
		if !isToolCall || toolCall.Kind != agenttypes.ToolKindAskUser {
			return "", false, false
		}
		return toolCall.CallID, true, true
	case "tool_call_update":
		toolCall, isToolCall := event.Data.(agenttypes.ToolCall)
		if !isToolCall || toolCall.Kind != agenttypes.ToolKindAskUser || strings.TrimSpace(toolCall.Status) != "complete" {
			return "", false, false
		}
		return toolCall.CallID, false, true
	}
	return "", false, false
}

func (s *AppContext) notifyScheduled(rootID, taskID, taskName, sessionKey, summary, message string, success bool) {
	if s == nil {
		return
	}
	if strings.TrimSpace(summary) == "" && success {
		summary = s.sessionSummary(rootID, sessionKey)
	}
	eventID := "scheduled:" + rootID + ":" + taskID + ":" + time.Now().UTC().Format(time.RFC3339Nano)
	payload := notify.BuildScheduledPayload(notify.ScheduledNotification{
		RootID:     rootID,
		RootTitle:  s.rootTitle(rootID),
		TaskID:     taskID,
		TaskName:   taskName,
		SessionKey: sessionKey,
		Summary:    summary,
		Error:      message,
		Success:    success,
		EventID:    eventID,
	})
	s.notifyPayload(context.Background(), eventID, payload)
}

func (s *AppContext) notifyPayload(ctx context.Context, eventID string, payload notify.Payload) {
	if s == nil {
		return
	}
	if s.WebPush != nil && s.WebPush.Enabled() {
		s.WebPush.NotifyPayload(ctx, eventID, payload)
	}
	if s.Notify != nil && s.Notify.Enabled() {
		s.Notify.NotifyPayload(ctx, payload)
	}
}

func (s *AppContext) rootTitle(rootID string) string {
	if s == nil || s.Dirs == nil {
		return strings.TrimSpace(rootID)
	}
	root, ok := s.Dirs.Get(rootID)
	if !ok {
		return strings.TrimSpace(rootID)
	}
	return firstNonBlank(root.EffectiveName(), root.Name, root.ID)
}

func (s *AppContext) sessionTitle(rootID, sessionKey string) string {
	if strings.TrimSpace(rootID) == "" || strings.TrimSpace(sessionKey) == "" {
		return ""
	}
	manager, err := s.GetSessionManager(rootID)
	if err != nil {
		return ""
	}
	sess, err := manager.Get(context.Background(), sessionKey, 0)
	if err != nil || sess == nil {
		return ""
	}
	return strings.TrimSpace(sess.Name)
}

func (s *AppContext) sessionSummary(rootID, sessionKey string) string {
	if strings.TrimSpace(rootID) == "" || strings.TrimSpace(sessionKey) == "" {
		return ""
	}
	manager, err := s.GetSessionManager(rootID)
	if err != nil {
		return ""
	}
	sess, err := manager.Get(context.Background(), sessionKey, 0)
	if err != nil || sess == nil || len(sess.Exchanges) == 0 {
		return ""
	}
	for i := len(sess.Exchanges) - 1; i >= 0; i-- {
		if strings.TrimSpace(sess.Exchanges[i].Role) != "assistant" {
			continue
		}
		// 正文已搬去块流（S2d-2）：取该轮所有 text 块按 pos 顺序拼起来。
		// 只读一个 seq 的窗口，不整会话全量读 —— 那是通知链路，不是渲染链路。
		blocks, err := manager.GetBlocksWindow(context.Background(), sessionKey, map[int]bool{sess.Exchanges[i].Seq: true})
		if err != nil {
			return ""
		}
		var parts []string
		for _, block := range blocks {
			if block.Type != session.BlockTypeText {
				continue
			}
			if text := strings.TrimSpace(block.Text); text != "" {
				parts = append(parts, text)
			}
		}
		return lastRunes(strings.Join(parts, "\n"), notify.BodyMaxRunes)
	}
	return ""
}

func askUserSummary(toolCall agenttypes.ToolCall) string {
	if len(toolCall.Content) > 0 {
		for _, item := range toolCall.Content {
			if strings.TrimSpace(item.Text) != "" {
				return strings.TrimSpace(item.Text)
			}
		}
	}
	if toolCall.Meta != nil {
		if questions, ok := toolCall.Meta["questions"].([]agenttypes.AskUserQuestionItem); ok && len(questions) > 0 {
			return strings.TrimSpace(questions[0].Question)
		}
		if input := strings.TrimSpace(stringMetaValue(toolCall.Meta, "input")); input != "" {
			return "需要确认：" + lastRunes(input, notify.BodyMaxRunes)
		}
	}
	return "需要你继续输入"
}

func stringMetaValue(meta map[string]any, key string) string {
	if meta == nil {
		return ""
	}
	value, _ := meta[key].(string)
	return strings.TrimSpace(value)
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
