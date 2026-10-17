package claude

import (
	"encoding/json"
	"log"
	"strings"

	types "mindfs/server/internal/agent/types"

	claudeagent "github.com/roasbeef/claude-agent-sdk-go"
)

func (s *session) emitMessageChunk(content string) {
	s.emitMessageChunkWithMeta(content, subagentMeta{})
}

func (s *session) emitMessageChunkWithMeta(content string, meta subagentMeta) {
	if strings.TrimSpace(content) != "" && !meta.hasSubagentRef() {
		s.sawMessageText = true
	}
	s.emit(types.Event{
		Type:      types.EventTypeMessageChunk,
		SessionID: s.SessionID(),
		Data: types.MessageChunk{
			Content:         content,
			ParentToolUseID: meta.ParentToolUseID,
			TaskID:          meta.TaskID,
			SubagentType:    meta.SubagentType,
			TaskDescription: meta.TaskDescription,
			// MessageID 是 claude 转录条目的 `uuid`（SDK 的 AssistantMessage.UUID）。
			// 它是助手行在 agent 侧的桥梁键：导入器重放同一条消息时读回的就是它，
			// 两个写入者（live / import）由此对上 —— 否则判重只能靠内容猜。
			// 此前这层身份从不落盘，live 与 import 的 agent_id 永远对不上。
			MessageID: meta.MessageID,
		},
	})
}

func (s *session) emitToolUpdate(callID, name string, meta subagentMeta) {
	toolCall := types.ToolCall{
		CallID: callID,
		Title:  name,
		Status: "running",
		Kind:   mapToolKind(name),
		Meta:   meta.toMap(),
	}
	s.emit(types.Event{
		Type:      types.EventTypeToolUpdate,
		SessionID: s.SessionID(),
		Data:      toolCall,
	})
}

func (s *session) emit(event types.Event) {
	s.mu.RLock()
	handler := s.onUpdate
	s.mu.RUnlock()
	if handler == nil {
		return
	}
	handler(event)
}

func (s *session) logRawMessage(raw []byte) {
	log.Printf("[agent/claude] output.raw session=%s msg=%s", s.sessionKey, truncateRaw(raw))
}

func (s *session) logRawToolCallBlock(block claudeagent.ContentBlock) {
	raw, err := json.Marshal(block)
	if err != nil {
		return
	}
	s.agentDebugLog.AppendRaw(raw)
}

func (s *session) logRawToolResult(msg claudeagent.UserMessage) {
	if msg.ToolUseResult == nil {
		return
	}
	raw, err := json.Marshal(msg.ToolUseResult)
	if err != nil {
		return
	}
	s.agentDebugLog.AppendRaw(raw)
}

func (s *session) updateSessionID(msg any) {
	switch m := msg.(type) {
	case claudeagent.SystemMessage:
		s.setSessionID(m.SessionID)
	case claudeagent.AssistantMessage:
		s.setSessionID(m.SessionID)
	case claudeagent.ResultMessage:
		s.setSessionID(m.SessionID)
	case claudeagent.ToolProgressMessage:
		s.setSessionID(m.SessionID)
	case claudeagent.PartialAssistantMessage:
		s.setSessionID(m.SessionID)
	case claudeagent.CompactBoundaryMessage:
		s.setSessionID(m.SessionID)
	case claudeagent.AuthStatusMessage:
		s.setSessionID(m.SessionID)
	case claudeagent.TaskStartedMessage:
		s.setSessionID(m.SessionID)
	case claudeagent.TaskProgressMessage:
		s.setSessionID(m.SessionID)
	case claudeagent.TaskUpdatedMessage:
		s.setSessionID(m.SessionID)
	case claudeagent.TaskNotificationMessage:
		s.setSessionID(m.SessionID)
	case claudeagent.ThinkingTokensMessage:
		s.setSessionID(m.SessionID)
	}
}

func (s *session) setSessionID(sessionID string) {
	if strings.TrimSpace(sessionID) == "" {
		return
	}
	s.mu.Lock()
	s.sessionID = sessionID
	s.mu.Unlock()
}

func (s *session) updateContextWindow(msg claudeagent.ResultMessage) {
	selectedModel := s.CurrentModel()
	if selectedModel != "" {
		for model, usage := range msg.ModelUsage {
			if strings.EqualFold(strings.TrimSpace(model), selectedModel) && usage.ContextWindow > 0 {
				log.Printf("[agent/claude] context_window session=%s model=%q window=%d source=current_model", s.sessionKey, model, usage.ContextWindow)
				s.mu.Lock()
				s.context.ModelContextWindow = usage.ContextWindow
				s.mu.Unlock()
				return
			}
		}
	}

	modelContextWindow := 0
	switch len(msg.ModelUsage) {
	case 0:
	case 1:
		for _, usage := range msg.ModelUsage {
			modelContextWindow = usage.ContextWindow
		}
	default:
		maxUsageTokens := -1
		for _, usage := range msg.ModelUsage {
			usageTokens := usage.InputTokens + usage.OutputTokens
			if usageTokens <= maxUsageTokens {
				continue
			}
			maxUsageTokens = usageTokens
			modelContextWindow = usage.ContextWindow
		}
	}
	if modelContextWindow == 0 {
		return
	}

	s.mu.Lock()
	s.context.ModelContextWindow = modelContextWindow
	s.mu.Unlock()
}

func claudeTokenUsage(msg claudeagent.ResultMessage) *types.TokenUsage {
	inputTokens := 0
	outputTokens := 0
	cacheReadTokens := 0
	cacheWriteTokens := 0
	if msg.Usage != nil {
		inputTokens = max(0, msg.Usage.InputTokens)
		outputTokens = max(0, msg.Usage.OutputTokens)
		cacheReadTokens = max(0, msg.Usage.CacheReadInputTokens)
		cacheWriteTokens = max(0, msg.Usage.CacheCreationInputTokens)
	} else {
		for _, usage := range msg.ModelUsage {
			inputTokens += max(0, usage.InputTokens)
			outputTokens += max(0, usage.OutputTokens)
			cacheReadTokens += max(0, usage.CacheReadInputTokens)
			cacheWriteTokens += max(0, usage.CacheCreationInputTokens)
		}
	}
	if inputTokens == 0 && outputTokens == 0 && cacheReadTokens == 0 && cacheWriteTokens == 0 {
		return nil
	}
	logicalInputTokens := inputTokens + cacheReadTokens + cacheWriteTokens
	return &types.TokenUsage{
		InputTokens:      logicalInputTokens,
		OutputTokens:     outputTokens,
		CacheReadTokens:  &cacheReadTokens,
		CacheWriteTokens: &cacheWriteTokens,
	}
}

func contextTokensFromPartialEvent(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var event struct {
		Type  string `json:"type"`
		Usage struct {
			InputTokens              int `json:"input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &event); err != nil || strings.TrimSpace(event.Type) != "message_delta" {
		return 0
	}
	return event.Usage.InputTokens + event.Usage.CacheReadInputTokens + event.Usage.CacheCreationInputTokens
}

func extractDeltas(raw json.RawMessage) (string, string) {
	if len(raw) == 0 {
		return "", ""
	}
	var event struct {
		Delta struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Thinking string `json:"thinking"`
		} `json:"delta"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		return "", ""
	}
	switch strings.TrimSpace(event.Delta.Type) {
	case "text_delta":
		if event.Delta.Text != "" {
			return event.Delta.Text, ""
		}
	case "thinking_delta":
		if strings.TrimSpace(event.Delta.Thinking) != "" {
			return "", event.Delta.Thinking
		}
	}
	if event.Delta.Text != "" {
		return event.Delta.Text, ""
	}
	if strings.TrimSpace(event.Delta.Thinking) != "" {
		return "", event.Delta.Thinking
	}
	return event.Text, ""
}

func isKnownNonDisplayPartialEvent(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var event struct {
		Type  string `json:"type"`
		Delta struct {
			Type string `json:"type"`
		} `json:"delta"`
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		return false
	}
	switch strings.TrimSpace(event.Delta.Type) {
	case "input_json_delta", "signature_delta":
		return true
	}
	switch strings.TrimSpace(event.Type) {
	case "content_block_start", "content_block_stop", "message_start", "message_stop":
		return true
	default:
		return false
	}
}
