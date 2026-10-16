package api

import (
	"context"
	"log"
	"strings"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/kanban"
	"mindfs/server/internal/session"
)

func (s *AppContext) ClearTaskAuxFlagsForSession(rootID, sessionKey string) {
	empty := ""
	no := false
	// 新一轮的用户消息已经在路上 ⇒ 上一轮的"等你回答"作废（这条入口覆盖的正是
	// 用户在输入框直接回一句的情形：那时不一定有 tool_update complete 事件）。
	s.GetSessionStreamHub().MarkSessionAwaiting(rootID, sessionKey, "", false)
	s.updateTaskAuxFlagsForSession(rootID, sessionKey, kanban.TaskAuxFlagsPatch{
		AskUserWaiting: &no,
		HasPlan:        &no,
		HasTodos:       &no,
		HasTask:        &no,
		SessionError:   &empty,
	}, "aux_flags_cleared")
	s.updateSessionAuxFlags(rootID, sessionKey, session.SessionAuxFlagsPatch{
		AskUserWaiting: &no,
		HasPlan:        &no,
		HasTodos:       &no,
		HasTask:        &no,
		SessionError:   &empty,
	})
}

func (s *AppContext) UpdateTaskSessionErrorForSession(rootID, sessionKey, message string) {
	trimmed := strings.TrimSpace(message)
	if trimmed == "" {
		return
	}
	s.updateTaskAuxFlagsForSession(rootID, sessionKey, kanban.TaskAuxFlagsPatch{
		SessionError: &trimmed,
	}, "agent_session_error")
	s.updateSessionAuxFlags(rootID, sessionKey, session.SessionAuxFlagsPatch{
		SessionError: &trimmed,
	})
}

// EndSessionTurn 是**回合的唯一终结器**：通知 → 广播 session.done → 按回合 mindfs_id 清 pending。
//
// 所有「这一轮结束了」的边（成功 / 出错 / 取消 / 父轮收尾 / panic）都必须走到这里，
// 且必须带**置 pending 时拿到的同一个 turnID**。清理按身份比对（见
// StreamHub.ClearSessionPending），因此：
//   - 迟到或重复的 done 是 no-op（不会抹掉新轮的灯）；
//   - 正常 done 幂等；
//   - 调用方用 defer 保证 panic/提前 return 也必达 —— 这正是「灯不灭」的根治点。
func (s *AppContext) EndSessionTurn(rootID, sessionKey, requestID string, turnID string) {
	hub := s.GetSessionStreamHub()
	// 身份不匹配 ⇒ 这是别的回合的迟到/重复 done：整条丢弃。
	// 如果在此时仍广播 session.done 帧，前端会按 key 清掉**新轮**的灯（灯不亮）。
	if !hub.PendingTurnIDMatches(sessionKey, turnID) {
		return
	}
	pending := hub.PendingSessionSnapshot(sessionKey)
	s.notifySessionDone(rootID, sessionKey, requestID, pending)
	// 广播必须在清之前：ClearSessionPending 有超时兜底，但广播是前端唯一的
	// session.done 来源。如果先清再广播，清卡住时广播永远发不出去。
	hub.broadcastSessionDone(rootID, sessionKey, requestID)
	hub.ClearSessionPending(sessionKey, turnID)
}

// updateSessionAwaitingFromEvent 把 ask_user 的生命周期镜像进 StreamHub（会话级的
// "等你回答"）。
//
// 与 updateTaskAuxFlagsFromEvent 落点不同是有意的：那个写的是**看板任务**的 aux 标记
// （只有任务卡读得到，且要求会话挂了任务），这里写的是**会话**的，喂前端空心灯。
// 两条都留 —— 任务卡问的是"这个任务的流水卡在哪一步"，会话灯问的是"这条会话在等谁"。
func (s *AppContext) updateSessionAwaitingFromEvent(rootID, sessionKey string, event *StreamEvent) {
	if s == nil {
		return
	}
	callID, awaiting, ok := askUserStreamEvent(event)
	if !ok {
		return
	}
	s.GetSessionStreamHub().MarkSessionAwaiting(rootID, sessionKey, callID, awaiting)
}

func (s *AppContext) updateTaskAuxFlagsFromEvent(rootID, sessionKey string, event *StreamEvent) {
	if s == nil || event == nil {
		return
	}
	if _, awaiting, ok := askUserStreamEvent(event); ok {
		value := awaiting
		eventType := "aux_ask_user_waiting"
		if !awaiting {
			eventType = "aux_ask_user_answered"
		}
		s.updateTaskAuxFlagsForSession(rootID, sessionKey, kanban.TaskAuxFlagsPatch{AskUserWaiting: &value}, eventType)
		return
	}
	patch := kanban.TaskAuxFlagsPatch{}
	eventType := ""
	switch event.Type {
	case string(agenttypes.EventTypeToolCall):
		toolCall, ok := event.Data.(agenttypes.ToolCall)
		if !ok {
			return
		}
		switch toolCall.Kind {
		case agenttypes.ToolKindTask:
			value := true
			patch.HasTask = &value
			eventType = "aux_task_seen"
		default:
			return
		}
	case string(agenttypes.EventTypePlanUpdate):
		value := true
		patch.HasPlan = &value
		eventType = "aux_plan_seen"
	case string(agenttypes.EventTypeTodoUpdate):
		value := true
		patch.HasTodos = &value
		eventType = "aux_todos_seen"
	default:
		return
	}
	s.updateTaskAuxFlagsForSession(rootID, sessionKey, patch, eventType)
	s.updateSessionAuxFlagsFromTaskPatch(rootID, sessionKey, patch)
}

func (s *AppContext) updateTaskAuxFlagsForSession(rootID, sessionKey string, patch kanban.TaskAuxFlagsPatch, eventType string) {
	if s == nil {
		return
	}
	manager, err := s.GetSessionManager(rootID)
	if err != nil {
		return
	}
	sess, err := manager.Get(context.Background(), sessionKey, 0)
	if err != nil || sess == nil || strings.TrimSpace(sess.TaskID) == "" {
		return
	}
	svc, err := s.GetKanbanService()
	if err != nil {
		return
	}
	if _, err := svc.UpdateTaskAuxFlags(context.Background(), rootID, sess.TaskID, patch, eventType); err != nil {
		log.Printf("[kanban] task.aux_flags.update.error root=%s task=%s session=%s err=%v", rootID, sess.TaskID, sessionKey, err)
	}
}

// updateSessionAuxFlags 写会话行的内容徽标（G-AD），与上面的任务行写入并列。
// 区别只有守卫：会话行不要求 sess.TaskID —— 普通聊天会话也落盘。
func (s *AppContext) updateSessionAuxFlags(rootID, sessionKey string, patch session.SessionAuxFlagsPatch) {
	if s == nil {
		return
	}
	manager, err := s.GetSessionManager(rootID)
	if err != nil {
		return
	}
	if err := manager.UpdateSessionAuxFlags(context.Background(), sessionKey, patch); err != nil {
		log.Printf("[session] session.aux_flags.update.error root=%s session=%s err=%v", rootID, sessionKey, err)
	}
}

// updateSessionAuxFlagsFromTaskPatch 把任务行 patch 同语义镜像到会话行。
// 字段与 kanban.TaskAuxFlagsPatch 一一对应（同名同义），调用点与
// updateTaskAuxFlagsForSession 并列，不改任务行语义。
func (s *AppContext) updateSessionAuxFlagsFromTaskPatch(rootID, sessionKey string, patch kanban.TaskAuxFlagsPatch) {
	s.updateSessionAuxFlags(rootID, sessionKey, session.SessionAuxFlagsPatch{
		AskUserWaiting: patch.AskUserWaiting,
		HasPlan:        patch.HasPlan,
		HasTodos:       patch.HasTodos,
		HasTask:        patch.HasTask,
		SessionError:   patch.SessionError,
	})
}
