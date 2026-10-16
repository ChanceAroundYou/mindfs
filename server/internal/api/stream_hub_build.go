package api

import (
	"strings"
	"time"
)

func buildSessionStreamResponse(rootID, sessionKey string, event *StreamEvent) WSResponse {
	return WSResponse{
		Type: "session.stream",
		Payload: map[string]any{
			"root_id":     rootID,
			"session_key": sessionKey,
			"event":       event,
		},
	}
}

// buildSessionStreamBatchResponse 与单条版同类型，只是把 `event` 换成 `events` 数组。
//
// 两件事各自独立，**别混**：
//
//  1. **整批一条消息**（形状）—— 逐条发 = 客户端「收一条 → 渲染一次」，几百条事件就是
//     几百次渲染，用户看到的是「打开/切回会话时逐渐刷一大堆」。整批放进一条消息，客户端
//     在一次同步循环里应用，React 批处理成**一次**渲染 —— 观感是一步到位。
//  2. **reset:true**（语义）—— 这是「本轮状态快照」，不是「补发缺失的几条」。服务端不从
//     客户端取任何进度，每次挂上都把当前回合的事件整份重发（见 ReplayPending）。客户端
//     收到 reset 先把该会话的**瞬时尾巴清空**，再照单重建。于是「客户端尾巴 = 服务端
//     buffer」无条件成立，重放 N 次结果逐字节相同 —— 客户端此前见过什么、有没有漏收，
//     都不影响结果。空 `events` 也是合法的：空批只清尾巴，不产生内容。
//
// **`reset` 必须与「形状」分开传**（这就是它是参数而不是常量的原因）：
//   - `reset=true` —— 挂载会话的首帧，本轮状态快照；
//   - `reset=false` —— 快照之后的**续投**（`replayStepToClient`，排空循环期间新到的事件）。
//
// 曾把这个函数写成恒发 `reset:true`，后果是排空期间一到新事件就把客户端刚重建好的尾巴
// 又清一遍 —— 续投的那几条反而把前面整批抹掉，越活跃的会话越容易命中。
// 客户端侧同样分开判：按**形状**展开，按 `reset` 清尾（见 session.ts 的 emitDecrypted）。
func buildSessionStreamBatchResponse(rootID, sessionKey string, events []StreamEvent, reset bool) WSResponse {
	if events == nil {
		events = []StreamEvent{}
	}
	payload := map[string]any{
		"root_id":     rootID,
		"session_key": sessionKey,
		"events":      events,
	}
	if reset {
		payload["reset"] = true
	}
	return WSResponse{
		Type:    "session.stream",
		Payload: payload,
	}
}

func buildSessionDoneResponse(rootID, sessionKey, requestID string) WSResponse {
	return WSResponse{
		ID:   requestID,
		Type: "session.done",
		Payload: map[string]any{
			"root_id":     rootID,
			"session_key": sessionKey,
		},
	}
}

func buildSlashCommandStreamResponse(rootID, sessionKey, command, requestID string, event *StreamEvent) WSResponse {
	return WSResponse{
		ID:   requestID,
		Type: "session.slash_command.stream",
		Payload: map[string]any{
			"root_id":     rootID,
			"session_key": sessionKey,
			"command":     command,
			"request_id":  requestID,
			"event":       event,
		},
	}
}

func buildSlashCommandDoneResponse(rootID, sessionKey, command, requestID string) WSResponse {
	return WSResponse{
		ID:   requestID,
		Type: "session.slash_command.done",
		Payload: map[string]any{
			"root_id":     rootID,
			"session_key": sessionKey,
			"command":     command,
			"request_id":  requestID,
		},
	}
}

func buildSessionUserMessageResponse(rootID, sessionKey, sessionType, sessionName, agentName, model, modelDisplayName, mode, effort, fastService string, planMode bool, content string, timestamp time.Time, queued bool, userExchangeSeq int, mindfsID string) WSResponse {
	queueState := "active"
	if queued {
		queueState = "dequeued"
	}
	sessionPayload := map[string]any{
		"key":                sessionKey,
		"type":               sessionType,
		"agent":              agentName,
		"model":              model,
		"model_display_name": modelDisplayName,
		"mode":               mode,
		"effort":             effort,
		"fast_service":       fastService,
		"plan_mode":          planMode,
		"created_at":         timestamp,
		"updated_at":         timestamp,
	}
	if strings.TrimSpace(sessionName) != "" {
		sessionPayload["name"] = sessionName
	}
	exchangePayload := map[string]any{
		"role":               "user",
		"agent":              agentName,
		"model":              model,
		"model_display_name": modelDisplayName,
		"mode":               mode,
		"effort":             effort,
		"fast_service":       fastService,
		"content":            content,
		"timestamp":          timestamp,
		"queued":             queued,
		"queue_state":        queueState,
	}
	// seq 是「本轮用户消息已持久化」的权威标记：客户端据此把本地乐观条目（无 seq）
	// 收敛为已持久化条目，避免它与窗口取回的同一条消息重复渲染。未知时不下发，
	// 客户端保持原行为（作为瞬时项渲染）。
	if userExchangeSeq > 0 {
		exchangePayload["seq"] = userExchangeSeq
	}
	if strings.TrimSpace(mindfsID) != "" {
		exchangePayload["mindfs_id"] = strings.TrimSpace(mindfsID)
	}
	return WSResponse{
		Type: "session.user_message",
		Payload: map[string]any{
			"root_id":     rootID,
			"session_key": sessionKey,
			"session":     sessionPayload,
			"exchange":    exchangePayload,
		},
	}
}

func buildSessionQueueUpdatedResponse(rootID, sessionKey string, queue []QueuedUserMessage, frozen bool) WSResponse {
	return WSResponse{
		Type: "session.queue.updated",
		Payload: map[string]any{
			"root_id":      rootID,
			"session_key":  sessionKey,
			"queue":        queue,
			"queue_frozen": frozen,
		},
	}
}
