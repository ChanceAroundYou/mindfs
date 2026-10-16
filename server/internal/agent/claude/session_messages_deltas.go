package claude

import (
	"encoding/json"
	"log"
	"strings"

	types "mindfs/server/internal/agent/types"

	claudeagent "github.com/roasbeef/claude-agent-sdk-go"
)

func (s *session) handlePartialAssistantMessage(rawEvent json.RawMessage, meta subagentMeta) {
	if contextTokens := contextTokensFromPartialEvent(rawEvent); contextTokens > 0 {
		s.mu.Lock()
		s.context.TotalTokens = contextTokens
		s.mu.Unlock()
	}
	textDelta, thinkingDelta := extractDeltas(rawEvent)
	if textDelta == "" && thinkingDelta == "" && len(rawEvent) > 0 && !isKnownNonDisplayPartialEvent(rawEvent) {
		log.Printf("[agent/claude] output.unhandled.partial session=%s raw=%s", s.sessionKey, truncateRaw(rawEvent))
	}
	if meta.hasSubagentRef() {
		s.flushAllDeltas()
		if thinkingDelta != "" {
			s.emitThoughtChunkWithMeta(thinkingDelta, meta)
		}
		if textDelta != "" {
			s.emitMessageChunkWithMeta(textDelta, meta)
		}
		return
	}
	// Thinking and visible text are rendered in separate UI lanes, so flush the
	// other lane before appending to the current one.
	if thinkingDelta != "" {
		s.flushDelta(deltaTypeText)
		s.appendDelta(deltaTypeThinking, thinkingDelta)
	}
	if textDelta != "" {
		s.flushDelta(deltaTypeThinking)
		s.appendDelta(deltaTypeText, textDelta)
	}
}

func (s *session) pendingBuilder(kind deltaType) *strings.Builder {
	if kind == deltaTypeThinking {
		return &s.pendingThinking
	}
	return &s.pendingText
}

func (s *session) flushAllDeltas() {
	s.flushDelta(deltaTypeText)
	s.flushDelta(deltaTypeThinking)
}

func (s *session) flushDelta(kind deltaType) {
	pending := s.pendingBuilder(kind)
	if pending.Len() == 0 {
		return
	}
	delta := pending.String()
	pending.Reset()
	if kind == deltaTypeThinking {
		s.emitThoughtChunk(delta)
		return
	}
	s.sawDelta = true
	s.emitMessageChunk(delta)
}

func (s *session) appendDelta(kind deltaType, delta string) {
	if delta == "" {
		return
	}
	pending := s.pendingBuilder(kind)
	pending.WriteString(delta)
	// Coalesce token-level fragments into readable chunks while keeping streaming feel.
	if pending.Len() >= chunkFlushThreshold || strings.ContainsAny(delta, "\n.!?;:") {
		s.flushDelta(kind)
	}
}

func (s *session) emitThoughtChunk(content string) {
	s.emitThoughtChunkWithMeta(content, subagentMeta{})
}

func (s *session) emitThoughtChunkWithMeta(content string, meta subagentMeta) {
	s.emit(types.Event{
		Type:      types.EventTypeThoughtChunk,
		SessionID: s.SessionID(),
		Data: types.ThoughtChunk{
			Content:         content,
			ParentToolUseID: meta.ParentToolUseID,
			TaskID:          meta.TaskID,
			SubagentType:    meta.SubagentType,
			TaskDescription: meta.TaskDescription,
		},
	})
}

func (s *session) handleAssistantMessage(msg claudeagent.AssistantMessage, sawDelta bool) {
	meta := subagentMetaFromAssistant(msg)
	// 补上 agent 侧桥梁键：AssistantMessage.UUID 是这条消息在转录里的 uuid，
	// 导入器重放同一条时读回的就是它。SDK 一直带着这个字段，只是此前从未读取 ——
	// 于是 live 写的助手行没有 agent_id、import 写的那行有，两个写入者永远对不上。
	meta.MessageID = strings.TrimSpace(msg.UUID)
	for _, block := range msg.Message.Content {
		switch block.Type {
		case "text":
			// If text was already streamed via partial deltas, skip the duplicated
			// finalized text block.
			if sawDelta {
				continue
			}
			s.emitMessageChunkWithMeta(block.Text, meta)
		case "thinking":
			s.emitThoughtChunkWithMeta(block.Text, meta)
		case "tool_use":
			// tool_use is the structured tool invocation request. Its completion
			// arrives later as UserMessage + ToolUseResult.
			s.logRawToolCallBlock(block)
			toolCall := newRunningToolCall(block.ID, block.Name, block.Type, block.Input)
			toolCall.Meta = mergeToolCallMeta(toolCall.Meta, meta.toMap())
			if isTaskListToolName(block.Name) {
				toolCall.Meta = mergeToolCallMeta(toolCall.Meta, map[string]any{
					"taskTool":  canonicalTaskListToolName(block.Name),
					"toolUseId": block.ID,
				})
				s.trackPendingToolCall(toolCall)
				continue
			}
			s.trackPendingToolCall(toolCall)
			// AskUserQuestion 已由 CanUseTool 回调（awaitAskUserQuestion）发过带完整
			// meta.questions 的 tool_call；此处是 SDK 的同一次提问的第二条路径，
			// 跳过以免同一张卡出现两份。
			if mapToolKind(block.Name) == types.ToolKindAskUser {
				if !s.claimAskUserEmit(block.ID) {
					continue
				}
			}
			s.emit(types.Event{
				Type:      types.EventTypeToolCall,
				SessionID: s.SessionID(),
				Data:      toolCall,
			})
		}
	}
}

func (s *session) handleStreamEvent(msg claudeagent.StreamEvent) {
	if msg.Delta != "" {
		s.emitMessageChunk(msg.Delta)
		return
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		return
	}
	s.handlePartialAssistantMessage(raw, subagentMeta{})
}
