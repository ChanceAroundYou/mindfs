package api

import (
	"context"
	"encoding/json"
	"log"
	"strings"

	"mindfs/server/internal/api/usecase"
	"mindfs/server/internal/kanban"

	"github.com/gorilla/websocket"
)

func (h *WSHandler) handleSessionAnswerQuestion(ctx context.Context, conn *websocket.Conn, clientID string, req WSRequest) {
	rootID := getString(req.Payload, "root_id")
	key := getString(req.Payload, "session_key")
	agentName := getString(req.Payload, "agent")
	toolUseID := getString(req.Payload, "tool_use_id")
	if key == "" || toolUseID == "" {
		h.sendWSError(conn, clientID, req.ID, "invalid_request", "session_key and tool_use_id required")
		return
	}
	answers := parseStringMap(req.Payload["answers"])
	if len(answers) == 0 {
		h.sendWSError(conn, clientID, req.ID, "invalid_request", "answers required")
		return
	}
	uc := &usecase.Service{Registry: h.AppContext}
	if err := uc.AnswerQuestion(ctx, usecase.AnswerQuestionInput{
		RootID:     rootID,
		SessionKey: key,
		Agent:      agentName,
		ToolUseID:  toolUseID,
		Answers:    answers,
	}); err != nil {
		h.sendWSError(conn, clientID, req.ID, "session.answer_question_failed", err.Error())
		return
	}
	if manager, err := h.AppContext.GetSessionManager(rootID); err == nil {
		if sess, getErr := manager.Get(ctx, key, 0); getErr == nil && sess != nil && strings.TrimSpace(sess.TaskID) != "" {
			if svc, svcErr := h.AppContext.GetKanbanService(); svcErr == nil {
				value := false
				if _, clearErr := svc.UpdateTaskAuxFlags(ctx, rootID, sess.TaskID, kanban.TaskAuxFlagsPatch{AskUserWaiting: &value}, "aux_ask_user_answered"); clearErr != nil {
					log.Printf("[kanban] ask_user.answered.flag_clear.error root=%s task=%s session=%s err=%v", rootID, sess.TaskID, key, clearErr)
				}
			}
		}
	}
	// 卡片回答 = 提问有了答案 ⇒ 会话级的"等你回答"到此为止。上面那条只清看板任务的
	// aux 标记（且要求会话挂了任务），会话空心灯走的是另一条通道，必须在这里单独清，
	// 否则答完仍是一条空心灯挂在列表上。
	h.AppContext.GetSessionStreamHub().MarkSessionAwaiting(rootID, key, "", false)
	// 会话行的问号徽标（sessions.aux_ask_user_waiting）也必须在**提交答案这一刻**清。
	// 原来只等 tool_call_update complete 才清（updateTaskAuxFlagsFromEvent 那条），
	// 而那一手要等 agent 真的把答案当 tool result 收下才有 —— 徽标因此一直亮到
	// 那一轮结束（2026-10-20 用户实测「答完不灭，要会话结束才更新」）。
	// 这里清完 updateSessionAuxFlags 会广播 session.meta.updated ⇒ 列表立刻灭。
	h.AppContext.clearSessionAskUserWaiting(rootID, key)
	// 曾经在这里回一条 `session.answer_question.accepted`（带 req.ID 的回执）。已删除（2026-10-06）：
	// 前端零消费 —— 它提交答案后靠在本地把卡片翻成「已回答」，不需要回执；而 `sendWSMessage`
	// 也不做请求/响应配对（只对 `session.message` 这类做重发）。发一条只有空操作响应的消息
	// 属于自产自销（与已删的 `done(replay:true)` 同一类问题）。
}

func (h *WSHandler) handleSessionCancel(ctx context.Context, conn *websocket.Conn, clientID string, req WSRequest, targetTurnID uint64) {
	rootID := getString(req.Payload, "root_id")
	key := getString(req.Payload, "session_key")
	if rootID == "" || key == "" {
		h.sendWSError(conn, clientID, req.ID, "invalid_request", "root_id and session_key required")
		return
	}
	log.Printf("[ws] session.cancel root=%s session=%s request=%s target_turn=%d", rootID, key, req.ID, targetTurnID)

	streamHub := h.AppContext.GetSessionStreamHub()
	if queue, ok := streamHub.FreezeQueuedSessionMessages(key); ok {
		log.Printf("[ws] session.queue.freeze root=%s session=%s request=%s", rootID, key, req.ID)
		streamHub.BroadcastSessionQueueUpdated(rootID, key, queue)
	}

	uc := &usecase.Service{Registry: h.AppContext}
	if err := uc.CancelSessionTurn(ctx, usecase.CancelSessionTurnInput{
		RootID:       rootID,
		Key:          key,
		TargetTurnID: targetTurnID,
		TargetPinned: true,
	}); err != nil {
		if queue, changed := streamHub.UnfreezeQueuedSessionMessages(key); changed {
			streamHub.BroadcastSessionQueueUpdated(rootID, key, queue)
		}
		log.Printf("[ws] session.cancel.error root=%s session=%s request=%s err=%v", rootID, key, req.ID, err)
		h.sendWSError(conn, clientID, req.ID, "session.cancel_failed", err.Error())
		return
	}
}

func normalizeAgentErrorMessage(err error) string {
	if err == nil {
		return "Unknown error"
	}
	raw := strings.TrimSpace(err.Error())
	if raw == "" {
		return "Unknown error"
	}
	var payload struct {
		Message string `json:"message"`
	}
	if strings.HasPrefix(raw, "{") && json.Unmarshal([]byte(raw), &payload) == nil && strings.TrimSpace(payload.Message) != "" {
		return strings.TrimSpace(payload.Message)
	}
	return raw
}

func parsePlanMessage(content string) (bool, string) {
	trimmed := strings.TrimSpace(content)
	lower := strings.ToLower(trimmed)
	if lower == "/plan" {
		return true, ""
	}
	if strings.HasPrefix(lower, "/plan ") {
		return true, strings.TrimSpace(trimmed[len("/plan"):])
	}
	return false, content
}

func getString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	if value, ok := payload[key]; ok {
		if s, ok := value.(string); ok {
			return s
		}
	}
	return ""
}

func getInt(payload map[string]any, key string) int {
	if payload == nil {
		return 0
	}
	value, ok := payload[key]
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	case string:
		parsed := 0
		for _, ch := range strings.TrimSpace(typed) {
			if ch < '0' || ch > '9' {
				return 0
			}
			parsed = parsed*10 + int(ch-'0')
		}
		return parsed
	default:
		return 0
	}
}

func normalizeFastServiceValue(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "on":
		return "on"
	case "off":
		return "off"
	default:
		return ""
	}
}

func getBool(payload map[string]any, key string) bool {
	if payload == nil {
		return false
	}
	value, ok := payload[key]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "1", "true", "yes", "on", "fast":
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func parseStringMap(raw any) map[string]string {
	items, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(items))
	for key, value := range items {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		switch v := value.(type) {
		case string:
			if trimmed := strings.TrimSpace(v); trimmed != "" {
				out[key] = trimmed
			}
		case []any:
			parts := make([]string, 0, len(v))
			for _, item := range v {
				if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
					parts = append(parts, strings.TrimSpace(text))
				}
			}
			if len(parts) > 0 {
				out[key] = strings.Join(parts, ", ")
			}
		}
	}
	return out
}

func parseClientContext(payload map[string]any, rootID string) usecase.ClientContext {
	ctx := usecase.ClientContext{CurrentRoot: rootID}
	if payload == nil {
		return ctx
	}
	raw, ok := payload["context"]
	if !ok || raw == nil {
		return ctx
	}
	body, err := json.Marshal(raw)
	if err != nil {
		return ctx
	}
	if err := json.Unmarshal(body, &ctx); err != nil {
		return ctx
	}
	if ctx.CurrentRoot == "" {
		ctx.CurrentRoot = rootID
	}
	return ctx
}
