package api

import (
	"context"
	"strings"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/api/usecase"
	"mindfs/server/internal/session"
)

func (h *WSHandler) startNextQueuedSessionMessage(rootID, key string) {
	if h == nil || h.AppContext == nil || rootID == "" || key == "" {
		return
	}
	streamHub := h.AppContext.GetSessionStreamHub()
	item, queue, ok := streamHub.PopQueuedSessionMessage(key, "")
	if !ok {
		return
	}
	streamHub.BroadcastSessionQueueUpdated(rootID, key, queue)
	sessionType := session.TypeChat
	sessionName := ""
	shell := ""
	runtimeRootPath := ""
	uc := &usecase.Service{Registry: h.AppContext}
	if current, _, err := uc.GetSession(context.Background(), usecase.GetSessionInput{RootID: rootID, Key: key}); err == nil && current != nil {
		sessionType = current.Type
		sessionName = current.Name
		shell = current.Shell
		runtimeRootPath = sessionRuntimeRootPath(current)
	}
	go h.runSessionMessage(sessionMessageJob{
		RootID:          rootID,
		RuntimeRootPath: runtimeRootPath,
		Key:             key,
		SessionType:     sessionType,
		SessionName:     sessionName,
		Shell:           shell,
		User:            item.PendingUserMessage,
		RequestID:       item.ID,
		ClientCtx:       item.ClientCtx,
		Queued:          true,
	})
}

func sessionRuntimeRootPath(current *session.Session) string {
	if current == nil {
		return ""
	}
	if workDir := strings.TrimSpace(current.WorkDir); workDir != "" {
		return workDir
	}
	if current.RelatedWorktree == nil {
		return ""
	}
	if strings.TrimSpace(current.TaskID) == "" && strings.TrimSpace(current.Source) != "worktree" {
		return ""
	}
	return strings.TrimSpace(current.RelatedWorktree.Path)
}

func (h *WSHandler) sessionMessageContext() (context.Context, context.CancelFunc) {
	parentCtx := context.Background()
	if h != nil && h.AppContext != nil {
		if agentPool := h.AppContext.GetAgentPool(); agentPool != nil {
			parentCtx = agentPool.Context()
		}
	}
	return context.WithCancel(parentCtx)
}

func updateToEvent(update agenttypes.Event) *StreamEvent {
	switch update.Type {
	case agenttypes.EventTypeMessageChunk:
		if chunk, ok := update.Data.(agenttypes.MessageChunk); ok {
			return &StreamEvent{Type: "message_chunk", Data: chunk}
		}
	case agenttypes.EventTypeThoughtChunk:
		if chunk, ok := update.Data.(agenttypes.ThoughtChunk); ok {
			return &StreamEvent{Type: "thought_chunk", Data: chunk}
		}
	case agenttypes.EventTypeToolCall:
		if tc, ok := update.Data.(agenttypes.ToolCall); ok {
			return &StreamEvent{Type: "tool_call", Data: tc}
		}
	case agenttypes.EventTypeToolUpdate:
		if tu, ok := update.Data.(agenttypes.ToolCall); ok {
			return &StreamEvent{Type: "tool_call_update", Data: tu}
		}
	case agenttypes.EventTypeTodoUpdate:
		if todo, ok := update.Data.(agenttypes.TodoUpdate); ok {
			return &StreamEvent{Type: "todo_update", Data: todo}
		}
	case agenttypes.EventTypePlanUpdate:
		if plan, ok := update.Data.(agenttypes.PlanUpdate); ok {
			return &StreamEvent{Type: "plan_update", Data: plan}
		}
	case agenttypes.EventTypeCompact:
		if compact, ok := update.Data.(agenttypes.CompactNotice); ok {
			return &StreamEvent{Type: "compact_notice", Data: compact}
		}
	case agenttypes.EventTypeLogin:
		if login, ok := update.Data.(agenttypes.LoginNotice); ok {
			return &StreamEvent{Type: "login_notice", Data: login}
		}
	case agenttypes.EventTypeMessageDone:
		if done, ok := update.Data.(agenttypes.MessageDone); ok {
			return &StreamEvent{Type: "message_done", Data: done}
		}
		return &StreamEvent{Type: "message_done", Data: agenttypes.MessageDone{}}
	case agenttypes.EventTypeRecovery:
		if recovery, ok := update.Data.(agenttypes.RecoveryStatus); ok {
			return &StreamEvent{Type: "recovery", Data: recovery}
		}
		return &StreamEvent{Type: "recovery", Data: agenttypes.RecoveryStatus{}}
	}
	return nil
}
