package claude

import (
	"strings"

	types "mindfs/server/internal/agent/types"

	claudeagent "github.com/roasbeef/claude-agent-sdk-go"
)

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
