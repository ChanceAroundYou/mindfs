package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func (s *session) handleUserMessage(msg claudeagent.UserMessage) {
	// Claude tool results do not come back on AssistantMessage. They arrive
	// here, and we map the result onto the earliest pending tool call.
	update, ok := s.toolResultUpdate(msg)
	if !ok {
		return
	}
	s.emit(types.Event{
		Type:      types.EventTypeToolUpdate,
		SessionID: s.SessionID(),
		Data:      update,
	})
}

func (s *session) handleTodoUpdateMessage(msg claudeagent.TodoUpdateMessage) {
	items := make([]types.TodoItem, 0, len(msg.Items))
	for _, item := range msg.Items {
		items = append(items, types.TodoItem{
			Content:    item.Content,
			ActiveForm: item.ActiveForm,
			Status:     string(item.Status),
		})
	}
	s.emit(types.Event{
		Type:      types.EventTypeTodoUpdate,
		SessionID: s.SessionID(),
		Data:      types.TodoUpdate{Items: items},
	})
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

func (s *session) handleCompactBoundaryMessage(msg claudeagent.CompactBoundaryMessage) {
	s.emit(types.Event{
		Type:      types.EventTypeCompact,
		SessionID: s.SessionID(),
		Data: types.CompactNotice{
			ID:      firstNonEmpty(msg.UUID, msg.SessionID),
			Status:  strings.TrimSpace(msg.CompactMetadata.Trigger),
			Summary: compactBoundarySummary(msg),
		},
	})
}

func (s *session) handleAuthStatusMessage(msg claudeagent.AuthStatusMessage) {
	status := "success"
	if msg.IsAuthenticating {
		status = "running"
	}
	if strings.TrimSpace(msg.Error) != "" {
		status = "failed"
	}
	lines := append([]string(nil), msg.Output...)
	if errText := strings.TrimSpace(msg.Error); errText != "" {
		lines = append(lines, errText)
	}
	callID := firstNonEmpty(msg.UUID, "auth-"+msg.SessionID, "auth-status")
	s.emit(types.Event{
		Type:      types.EventTypeToolUpdate,
		SessionID: s.SessionID(),
		Data: types.ToolCall{
			CallID:  callID,
			Title:   "authentication",
			Status:  status,
			Kind:    types.ToolKindOther,
			RawType: "auth_status",
			Content: []types.ToolCallContentItem{{Type: "text", Text: strings.Join(lines, "\n")}},
			Meta: map[string]any{
				"rawType":          "auth_status",
				"isAuthenticating": msg.IsAuthenticating,
			},
		},
	})
}

func (s *session) handleSubagentResultMessage(msg claudeagent.SubagentResultMessage) {
	agentName := strings.TrimSpace(msg.AgentName)
	title := firstNonEmpty(agentName, "subagent")
	status := "complete"
	if strings.EqualFold(strings.TrimSpace(msg.Status), "error") {
		status = "failed"
	}
	callID := "subagent-result-" + strings.ToLower(strings.ReplaceAll(title, " ", "-"))
	s.emit(types.Event{
		Type:      types.EventTypeToolUpdate,
		SessionID: s.SessionID(),
		Data: types.ToolCall{
			CallID:  callID,
			Title:   title,
			Status:  status,
			Kind:    types.ToolKindTask,
			RawType: "subagent_result",
			Content: []types.ToolCallContentItem{{Type: "text", Text: strings.TrimSpace(msg.Result)}},
			Meta: map[string]any{
				"rawType":      "subagent_result",
				"subagentType": agentName,
				"status":       strings.TrimSpace(msg.Status),
			},
		},
	})
}

func (s *session) handleTaskStartedMessage(msg claudeagent.TaskStartedMessage) {
	s.trackTaskInfo(msg.TaskID, claudeTaskInfo{
		ToolUseID:       msg.ToolUseID,
		TaskType:        msg.TaskType,
		Description:     msg.Description,
		SubagentType:    msg.SubagentType,
		TaskDescription: msg.Description,
	})
	if isIgnoredClaudeTaskType(msg.TaskType) {
		return
	}
	toolCall := claudeTaskToolCall(
		firstNonEmpty(msg.ToolUseID, msg.TaskID),
		"running",
		msg.Description,
		msg.Prompt,
		subagentMeta{
			ParentToolUseID: msg.ToolUseID,
			TaskID:          msg.TaskID,
			SubagentType:    msg.SubagentType,
			TaskDescription: msg.Description,
		},
		map[string]any{
			"subtype":      msg.Subtype,
			"taskType":     msg.TaskType,
			"workflowName": msg.WorkflowName,
		},
	)
	if strings.TrimSpace(toolCall.CallID) != "" {
		s.trackPendingToolCall(toolCall)
	}
	s.emit(types.Event{Type: types.EventTypeToolCall, SessionID: s.SessionID(), Data: toolCall})
}

func (s *session) handleTaskProgressMessage(msg claudeagent.TaskProgressMessage) {
	if isIgnoredClaudeTaskType(s.taskType(msg.TaskID)) {
		return
	}
	s.trackTaskInfo(msg.TaskID, claudeTaskInfo{
		ToolUseID:       msg.ToolUseID,
		Description:     msg.Description,
		SubagentType:    msg.SubagentType,
		TaskDescription: msg.Description,
	})
	callID := firstNonEmpty(msg.ToolUseID, msg.TaskID)
	toolCall := claudeTaskToolCall(
		callID,
		"running",
		msg.Description,
		msg.Summary,
		subagentMeta{
			ParentToolUseID: msg.ToolUseID,
			TaskID:          msg.TaskID,
			SubagentType:    msg.SubagentType,
			TaskDescription: msg.Description,
		},
		map[string]any{
			"subtype":      msg.Subtype,
			"lastToolName": msg.LastToolName,
			"progress":     firstNonEmpty(msg.Summary, msg.Description),
		},
	)
	if s.hasPendingToolCall(callID) {
		toolCall.Title = ""
	}
	s.emit(types.Event{Type: types.EventTypeToolUpdate, SessionID: s.SessionID(), Data: toolCall})
}

func (s *session) handleTaskUpdatedMessage(msg claudeagent.TaskUpdatedMessage) {
	info := s.taskInfo(msg.TaskID)
	if isIgnoredClaudeTaskType(info.TaskType) {
		return
	}
	description := firstNonEmpty(msg.Patch.Description, info.TaskDescription, info.Description)
	toolCall := claudeTaskToolCall(
		firstNonEmpty(info.ToolUseID, msg.TaskID),
		claudeTaskRunStatus(string(msg.Patch.Status)),
		description,
		msg.Patch.Error,
		subagentMeta{
			ParentToolUseID: info.ToolUseID,
			TaskID:          msg.TaskID,
			SubagentType:    info.SubagentType,
			TaskDescription: description,
		},
		map[string]any{
			"subtype":        msg.Subtype,
			"error":          msg.Patch.Error,
			"isBackgrounded": boolPtrValue(msg.Patch.IsBackgrounded),
		},
	)
	s.emit(types.Event{Type: types.EventTypeToolUpdate, SessionID: s.SessionID(), Data: toolCall})
}

func (s *session) handleTaskNotificationMessage(msg claudeagent.TaskNotificationMessage) {
	info := s.taskInfo(msg.TaskID)
	if isIgnoredClaudeTaskType(info.TaskType) {
		return
	}
	status := "complete"
	switch msg.Status {
	case claudeagent.TaskNotificationStatusFailed:
		status = "failed"
	case claudeagent.TaskNotificationStatusStopped:
		status = "cancelled"
	}
	extra := map[string]any{
		"subtype":    msg.Subtype,
		"outputFile": msg.OutputFile,
	}
	if strings.TrimSpace(msg.Summary) != "" {
		extra["summary"] = msg.Summary
	}
	// 注意：description 参数会成为卡片标题（见 claudeTaskToolCall 的 title := description）。
	// SDK 的 TaskNotificationMessage 没有 Description 字段，只有 Summary —— 而 Summary 是
	// 子代理的报告正文。过去这里直接把 msg.Summary 当 description 传，导致报告全文被提升成
	// 卡片标题，UI 上出现「一段没有思考的裸文本工具卡」（实测 2026-09-12：与同 line 锚点的
	// ask_user 卡相邻渲染，看起来像提问被重复了一遍，实际是子代理报告卡）。
	// 真正的任务描述在 trackTaskInfo 里（task_started/progress 写入），这里取它；
	// 拿不到时退到子代理类型，也不退到报告正文。
	description := firstNonEmpty(info.Description, info.TaskDescription, info.SubagentType, "task")
	toolCall := claudeTaskToolCall(
		firstNonEmpty(msg.ToolUseID, info.ToolUseID, msg.TaskID),
		status,
		description,
		msg.Summary,
		subagentMeta{
			ParentToolUseID: firstNonEmpty(msg.ToolUseID, info.ToolUseID),
			TaskID:          msg.TaskID,
			SubagentType:    info.SubagentType,
			TaskDescription: description,
		},
		extra,
	)
	s.emit(types.Event{Type: types.EventTypeToolUpdate, SessionID: s.SessionID(), Data: toolCall})
}

func (s *session) awaitAskUserQuestion(ctx context.Context, qs claudeagent.QuestionSet) (claudeagent.Answers, error) {
	callID := strings.TrimSpace(qs.ToolUseID)
	if callID == "" {
		return nil, errors.New("ask user question missing tool use id")
	}

	waiter := make(chan askUserAnswerResult, 1)
	s.questionMu.Lock()
	if s.questionWaits == nil {
		s.questionWaits = make(map[string]chan askUserAnswerResult)
	}
	if _, exists := s.questionWaits[callID]; exists {
		s.questionMu.Unlock()
		return nil, errors.New("ask user question already pending: " + callID)
	}
	s.questionWaits[callID] = waiter
	s.questionMu.Unlock()
	defer func() {
		s.questionMu.Lock()
		delete(s.questionWaits, callID)
		s.questionMu.Unlock()
	}()

	toolCall := askUserQuestionToolCall(qs)
	s.trackPendingToolCall(toolCall)
	// 与 handleAssistantMessage 的 tool_use 分支二选一发射：谁先到谁发，避免重复卡片。
	if s.claimAskUserEmit(callID) {
		s.emit(types.Event{
			Type:      types.EventTypeToolCall,
			SessionID: s.SessionID(),
			Data:      toolCall,
		})
	}

	select {
	case result := <-waiter:
		if result.err != nil {
			return nil, result.err
		}
		if len(result.answers) == 0 {
			return nil, errors.New("empty ask user answers")
		}
		return result.answers, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func askUserQuestionToolCall(qs claudeagent.QuestionSet) types.ToolCall {
	questions := make([]types.AskUserQuestionItem, 0, len(qs.Questions))
	for _, question := range qs.Questions {
		options := make([]types.AskUserQuestionOption, 0, len(question.Options))
		for _, option := range question.Options {
			options = append(options, types.AskUserQuestionOption{
				Label:       option.Label,
				Description: option.Description,
			})
		}
		questions = append(questions, types.AskUserQuestionItem{
			Question:    question.Question,
			Header:      question.Header,
			Options:     options,
			MultiSelect: question.MultiSelect,
		})
	}

	payload := claudeagent.AskUserQuestionInput{Questions: qs.Questions}
	raw, _ := json.Marshal(payload)
	toolCall := newRunningToolCall(qs.ToolUseID, "AskUserQuestion", "tool_use", raw)
	if toolCall.Meta == nil {
		toolCall.Meta = map[string]any{}
	}
	toolCall.Meta["toolUseId"] = qs.ToolUseID
	toolCall.Meta["questions"] = questions
	if qs.ParentToolUseID != nil && strings.TrimSpace(*qs.ParentToolUseID) != "" {
		toolCall.Meta["parentToolUseId"] = strings.TrimSpace(*qs.ParentToolUseID)
	}
	return toolCall
}

func newRunningToolCall(callID, name, rawType string, input json.RawMessage) types.ToolCall {
	title, meta, locations, content := summarizeToolCall(name, input)
	return types.ToolCall{
		CallID:    callID,
		Title:     title,
		Status:    "running",
		Kind:      mapToolKind(name),
		Locations: locations,
		Content:   content,
		RawType:   rawType,
		Meta:      meta,
	}
}

func subagentMetaFromPartial(msg claudeagent.PartialAssistantMessage) subagentMeta {
	meta := subagentMeta{}
	if msg.ParentToolUseID != nil {
		meta.ParentToolUseID = strings.TrimSpace(*msg.ParentToolUseID)
	}
	return meta
}

func subagentMetaFromAssistant(msg claudeagent.AssistantMessage) subagentMeta {
	meta := subagentMeta{
		SubagentType:    strings.TrimSpace(msg.SubagentType),
		TaskDescription: strings.TrimSpace(msg.TaskDescription),
	}
	if msg.ParentToolUseID != nil {
		meta.ParentToolUseID = strings.TrimSpace(*msg.ParentToolUseID)
	}
	return meta
}

func subagentMetaFromToolProgress(msg claudeagent.ToolProgressMessage) subagentMeta {
	meta := subagentMeta{}
	if msg.ParentToolUseID != nil {
		meta.ParentToolUseID = strings.TrimSpace(*msg.ParentToolUseID)
	}
	if msg.TaskID != nil {
		meta.TaskID = strings.TrimSpace(*msg.TaskID)
	}
	return meta
}

func (m subagentMeta) hasSubagentRef() bool {
	return strings.TrimSpace(m.ParentToolUseID) != "" || strings.TrimSpace(m.TaskID) != ""
}

func (m subagentMeta) toMap() map[string]any {
	out := map[string]any{}
	if value := strings.TrimSpace(m.ParentToolUseID); value != "" {
		out["parentToolUseId"] = value
	}
	if value := strings.TrimSpace(m.TaskID); value != "" {
		out["taskId"] = value
	}
	if value := strings.TrimSpace(m.SubagentType); value != "" {
		out["subagentType"] = value
	}
	if value := strings.TrimSpace(m.TaskDescription); value != "" {
		out["taskDescription"] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func claudeTaskToolCall(callID, status, description, text string, meta subagentMeta, extra map[string]any) types.ToolCall {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		callID = strings.TrimSpace(meta.TaskID)
	}
	if callID == "" {
		callID = "claude-task"
	}
	title := strings.TrimSpace(description)
	if title == "" {
		title = strings.TrimSpace(text)
	}
	if title == "" {
		title = firstNonEmpty(meta.SubagentType, meta.TaskID, callID, "task")
	}
	status = strings.TrimSpace(status)
	if status == "" {
		status = "running"
	}
	content := []types.ToolCallContentItem(nil)
	if body := strings.TrimSpace(text); body != "" && body != title {
		content = []types.ToolCallContentItem{{Type: "text", Text: body}}
	}
	merged := mergeToolCallMeta(meta.toMap(), map[string]any{
		"rawType": "claude_task",
	})
	merged = mergeToolCallMeta(merged, extra)
	return types.ToolCall{
		CallID:  callID,
		Title:   title,
		Status:  status,
		Kind:    types.ToolKindTask,
		RawType: "claude_task",
		Content: content,
		Meta:    merged,
	}
}

func claudeTaskRunStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed":
		return "complete"
	case "failed", "killed":
		return "failed"
	case "paused":
		return "running"
	case "pending", "running":
		return "running"
	default:
		return "running"
	}
}

func compactBoundarySummary(msg claudeagent.CompactBoundaryMessage) string {
	parts := make([]string, 0, 2)
	if trigger := strings.TrimSpace(msg.CompactMetadata.Trigger); trigger != "" {
		parts = append(parts, "trigger: "+trigger)
	}
	if msg.CompactMetadata.PreTokens > 0 {
		parts = append(parts, fmt.Sprintf("tokens before compact: %d", msg.CompactMetadata.PreTokens))
	}
	return strings.Join(parts, "\n")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func boolPtrValue(value *bool) any {
	if value == nil {
		return nil
	}
	return *value
}

func isIgnoredClaudeTaskType(taskType string) bool {
	switch strings.ToLower(strings.TrimSpace(taskType)) {
	case "local_bash":
		return true
	default:
		return false
	}
}

func (s *session) trackTaskInfo(taskID string, update claudeTaskInfo) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return
	}
	s.pendingToolMu.Lock()
	defer s.pendingToolMu.Unlock()
	if s.taskInfos == nil {
		s.taskInfos = make(map[string]claudeTaskInfo)
	}
	info := s.taskInfos[taskID]
	if value := strings.TrimSpace(update.ToolUseID); value != "" {
		info.ToolUseID = value
	}
	if value := strings.TrimSpace(update.TaskType); value != "" {
		info.TaskType = value
	}
	if value := strings.TrimSpace(update.Description); value != "" {
		info.Description = value
	}
	if value := strings.TrimSpace(update.SubagentType); value != "" {
		info.SubagentType = value
	}
	if value := strings.TrimSpace(update.TaskDescription); value != "" {
		info.TaskDescription = value
	}
	s.taskInfos[taskID] = info
}

func (s *session) taskInfo(taskID string) claudeTaskInfo {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return claudeTaskInfo{}
	}
	s.pendingToolMu.Lock()
	defer s.pendingToolMu.Unlock()
	if s.taskInfos == nil {
		return claudeTaskInfo{}
	}
	return s.taskInfos[taskID]
}

func (s *session) taskType(taskID string) string {
	return s.taskInfo(taskID).TaskType
}
