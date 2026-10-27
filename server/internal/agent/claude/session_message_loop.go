package claude

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"

	types "mindfs/server/internal/agent/types"

	claudeagent "github.com/roasbeef/claude-agent-sdk-go"
)

func (s *session) consumeMessages() {
	if s.stream == nil {
		return
	}

	// Claude SDK multiplexes several logical message types onto one channel:
	// - PartialAssistantMessage: incremental tokens for visible text or thinking
	// - AssistantMessage: a finalized assistant payload, including tool_use blocks
	// - UserMessage: a local echo that primarily carries tool_use_result here
	// - ToolProgressMessage: intermediate progress for a running tool call
	// - ResultMessage: the completion or error boundary for the current turn
	//
	// This function normalizes them into our internal session event stream.
	s.sawDelta = false
	s.pendingText.Reset()
	s.pendingThinking.Reset()
	for msg := range s.stream.Messages() {
		raw, _ := json.Marshal(msg)
		s.updateSessionID(msg)

		switch m := msg.(type) {
		case claudeagent.PartialAssistantMessage:
			// Incremental text / thinking tokens. Buffer and coalesce them into
			// larger readable chunks before emitting UI updates.
			s.handlePartialAssistantMessage(m.Event, subagentMetaFromPartial(m))
		case claudeagent.StreamEvent:
			s.handleStreamEvent(m)
		case claudeagent.AssistantMessage:
			// Finalized assistant message. Flush pending deltas first so finalized
			// blocks do not interleave with previously buffered streaming output.
			s.flushAllDeltas()
			s.handleAssistantMessage(m, s.sawDelta)
		case claudeagent.UserMessage:
			// Claude SDK surfaces tool execution results as a synthetic "user"
			// message. ToolUseResult maps to the earliest pending tool_use.
			s.flushAllDeltas()
			s.logRawToolResult(m)
			s.handleUserMessage(m)
		case claudeagent.TodoUpdateMessage:
			s.flushAllDeltas()
			s.handleTodoUpdateMessage(m)
		case claudeagent.ToolProgressMessage:
			// Lightweight progress heartbeat for a tool call that is still running.
			s.flushAllDeltas()
			s.emitToolUpdate(m.ToolUseID, m.ToolName, subagentMetaFromToolProgress(m))
		case claudeagent.CompactBoundaryMessage:
			s.flushAllDeltas()
			s.handleCompactBoundaryMessage(m)
		case claudeagent.AuthStatusMessage:
			s.flushAllDeltas()
			s.handleAuthStatusMessage(m)
		case claudeagent.SubagentResultMessage:
			s.flushAllDeltas()
			s.handleSubagentResultMessage(m)
		case claudeagent.TaskStartedMessage:
			s.flushAllDeltas()
			s.handleTaskStartedMessage(m)
		case claudeagent.TaskProgressMessage:
			s.flushAllDeltas()
			s.handleTaskProgressMessage(m)
		case claudeagent.TaskUpdatedMessage:
			s.flushAllDeltas()
			s.handleTaskUpdatedMessage(m)
		case claudeagent.TaskNotificationMessage:
			s.flushAllDeltas()
			s.handleTaskNotificationMessage(m)
		case claudeagent.ThinkingTokensMessage:
			// Live estimate for redacted thinking token progress. It is useful
			// telemetry for clients that render token counters, but MindFS does
			// not currently expose it in the session timeline.
		case claudeagent.ResultMessage:
			s.handleResultMessage(m, raw)
		default:
			s.logRawMessage(raw)
		}
	}

	s.failPendingTurns(errors.New("response stream ended unexpectedly"))
}

// handleResultMessage 处理一条 ResultMessage。
//
// SDK 的 Stream.Messages() 是**长驻迭代器**（只有一次性的 Query() 才在 result 上停），
// 所以同一条流里会出现不止一条 result：真回合结束时的那条，以及 CLI 插进来的
// 非人类来源 result（后台任务完成时 origin.kind=task-notification，实测会插在
// **下一轮的开头**）。只有人类发起的那一轮结束才是用户看到的回合边界；把后者也当
// 边界，就会在 agent 还在跑的时候广播 session.done 并清掉 pending —— 灯只在有
// chunk 时亮（静默期必灭），且回合已出列 ⇒ 停止按钮取到 target_turn=0 被
// staleCancelTarget 恒丢弃（2026-10-27 幽灵会话实测）。
//
// 判定口径是**白名单**（见 isTurnBoundaryResult）：SDK 的 MessageOriginKind 有 6 个值，
// 黑名单只挡今天碰巧出现的那一个，下一种 kind 一出现就原样复发。
func (s *session) handleResultMessage(msg claudeagent.ResultMessage, raw []byte) {
	s.logRawMessage(raw)
	if !isTurnBoundaryResult(msg) {
		// 非边界：不补正文、不发 MessageDone、不推进 context window、不结束本轮。
		// 子任务输出本来就走 TaskNotificationMessage → tool_call_update 卡片；
		// 当正文润出去会被 persistTurnBlocks 落成孤立句并插进用户这一轮的回答里。
		// 也不动 sawDelta/sawMessageText —— 否则真回合结束时会被误判成「已出过正文」。
		origin := ""
		if msg.Origin != nil {
			origin = string(msg.Origin.Kind)
		}
		// 定向日志：logRawMessage 在 1024 字节处截断，而 origin 序列化在 JSON 末尾，
		// 实测 987/1103 条 result 的 origin 在日志里根本看不到。
		log.Printf("[agent/claude] result.ignored session=%s origin=%q subtype=%q result_len=%d",
			s.sessionKey, origin, msg.Subtype, len(msg.Result))
		return
	}
	// 回合边界。Claude may place the final text here when no incremental tokens
	// were streamed, so emit it as a last fallback.
	s.flushAllDeltas()
	if !s.sawMessageText && strings.TrimSpace(msg.Result) != "" {
		s.emitMessageChunk(msg.Result)
	}
	s.updateContextWindow(msg)
	contextWindow, _ := s.ContextWindow(context.Background())
	tokenUsage := claudeTokenUsage(msg)
	s.emit(types.Event{
		Type:      types.EventTypeMessageDone,
		SessionID: s.SessionID(),
		Data:      types.MessageDone{ContextWindow: contextWindow, TokenUsage: tokenUsage},
	})
	s.completeTurn(resultErr(msg))
	s.sawDelta = false
	s.sawMessageText = false
}
