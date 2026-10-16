package api

import (
	"errors"
	"strings"
	"time"

	"mindfs/server/internal/agent"
	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/fs"
	"mindfs/server/internal/session"
)

func (s *AppContext) AddFileChangeListener(listener func(fs.FileChangeEvent)) {
	if listener == nil {
		return
	}
	s.mu.Lock()
	s.fileChangeListeners = append(s.fileChangeListeners, listener)
	s.mu.Unlock()
}

func (s *AppContext) AddFileChangeBatchListener(listener func(fs.FileChangeBatchEvent)) {
	if listener == nil {
		return
	}
	s.mu.Lock()
	s.fileChangeBatchListeners = append(s.fileChangeBatchListeners, listener)
	s.mu.Unlock()
}

func (s *AppContext) AddRelatedFileListener(listener func(fs.RelatedFileEvent)) {
	if listener == nil {
		return
	}
	s.mu.Lock()
	s.relatedFileListeners = append(s.relatedFileListeners, listener)
	s.mu.Unlock()
}

func (s *AppContext) emitFileChange(change fs.FileChangeEvent) {
	s.mu.RLock()
	listeners := append([]func(fs.FileChangeEvent){}, s.fileChangeListeners...)
	s.mu.RUnlock()
	for _, listener := range listeners {
		listener(change)
	}
}

func (s *AppContext) emitFileChangeBatch(change fs.FileChangeBatchEvent) {
	s.mu.RLock()
	listeners := append([]func(fs.FileChangeBatchEvent){}, s.fileChangeBatchListeners...)
	s.mu.RUnlock()
	for _, listener := range listeners {
		listener(change)
	}
}

func (s *AppContext) emitRelatedFile(change fs.RelatedFileEvent) {
	s.mu.RLock()
	listeners := append([]func(fs.RelatedFileEvent){}, s.relatedFileListeners...)
	s.mu.RUnlock()
	for _, listener := range listeners {
		listener(change)
	}
}

func (s *AppContext) BroadcastSessionMetaUpdated(rootID string, sess *session.Session) {
	if sess == nil {
		s.GetSessionStreamHub().BroadcastAll(WSResponse{Type: "session.meta.updated"})
		return
	}
	s.GetSessionStreamHub().BroadcastAll(WSResponse{
		Type: "session.meta.updated",
		Payload: map[string]any{
			"root_id": rootID,
			"session": map[string]any{
				"key":                 sess.Key,
				"type":                sess.Type,
				"parent_session_key":  sess.ParentSessionKey,
				"parent_tool_call_id": sess.ParentToolCallID,
				"source":              sess.Source,
				"task_id":             sess.TaskID,
				"name":                sess.Name,
				"agent":               session.InferAgentFromSession(sess),
				"model":               sess.Model,
				"mode":                session.InferModeFromSession(sess),
				"effort":              session.InferEffortFromSession(sess),
				"fast_service":        session.InferFastServiceFromSession(sess),
				"plan_mode":           sess.PlanMode,
				"related_worktree":    sess.RelatedWorktree,
				"worktree_missing":    sess.RelatedWorktree.WorktreeMissing(),
				"worktree_active":     relatedWorktreeActive(sess.RelatedWorktree),
				"aux_flags":           sess.Aux.AuxFlagsPayload(),
				"updated_at":          sess.UpdatedAt,
			},
		},
	})
}

// BroadcastSessionCreated 广播「新会话出现了」。
//
// 为什么必须单列一个事件、而不能靠 session.meta.updated 顶替：前者的前端 handler
// 会把新会话**补进列表快照**（loadMultiProjectSessionGroups / scheduleSessionListReload），
// 后者只更新 sessionCacheRef 里**已经存在**的条目。两者对「列表里还没有这个会话」的
// 场合表现完全不同——发 meta.updated 只会让已缓存的条目更新元信息。
//
// 谁会建出会话：前端 WS 建会话（ws.go handleChatMessage 那条，那条紧接着会发
// session.created 之外的事件）、看板/定时任务（AppContext.EnsureAgentSession、
// scheduled/tasks.go）——它们过去只发 meta.updated，于是「用户不在这个项目页时
// 看板建的会话」永远进不了对话列表（实测：任务 #5 的会话在库里、在 API 返回里、
// 就是不在列表里）。所以凡是建会话的路径都统一走这个函数。
func (s *AppContext) BroadcastSessionCreated(rootID string, sess *session.Session) {
	if s == nil || sess == nil {
		return
	}
	s.GetSessionStreamHub().BroadcastAll(WSResponse{
		Type: "session.created",
		Payload: map[string]any{
			"root_id": rootID,
			"session": sessionListResponse(sess, s.ResolveCommandShell()),
		},
	})
}

func (s *AppContext) BroadcastAgentStatusChanged(agentName string) {
	agentName = strings.TrimSpace(agentName)
	if s == nil || agentName == "" || s.GetProber() == nil {
		return
	}
	status, ok := s.GetProber().GetStatus(agentName)
	if !ok {
		return
	}
	statuses := []agent.Status{status}
	if prefs := s.GetPreferences(); prefs != nil {
		statuses = prefs.ApplyAgentDefaults(statuses)
	}
	status = applyAgentAPIProviderCapabilities(statuses)[0]
	s.GetSessionStreamHub().BroadcastAll(WSResponse{
		Type: "agent.status.changed",
		Payload: map[string]any{
			"name":                             status.Name,
			"protocol":                         status.Protocol,
			"installed":                        status.Installed,
			"available":                        status.Available,
			"version":                          status.Version,
			"error":                            status.Error,
			"last_probe":                       status.LastProbe,
			"current_model_id":                 status.CurrentModelID,
			"current_mode_id":                  status.CurrentModeID,
			"default_model_id":                 status.DefaultModelID,
			"default_effort":                   status.DefaultEffort,
			"default_fast_service":             status.DefaultFastService,
			"supports_api_provider_switch":     status.SupportsAPIProviderSwitch,
			"supported_api_provider_protocols": status.SupportedAPIProviderProtocols,
			"supports_fast_service":            status.SupportsFastService,
			"efforts":                          status.Efforts,
			"models":                           status.Models,
			"modes":                            status.Modes,
			"models_error":                     status.ModelsError,
			"modes_error":                      status.ModesError,
			"commands":                         status.Commands,
			"commands_error":                   status.CommandsError,
		},
	})
}

// SetSessionPendingReply 置子会话/看板会话的 pending，返回该条目的回合 mindfs_id。
// 收尾时必须把同一个 id 交给 EndSessionTurn，否则清理退化成无条件清。
func (s *AppContext) SetSessionPendingReply(rootID, sessionKey, sessionTitle, mindfsID string) string {
	return s.GetSessionStreamHub().SetPendingReply(rootID, sessionKey, sessionTitle, mindfsID)
}

// BroadcastSessionUserMessageAt 广播用户消息并置该会话 pending，返回回合 mindfs_id。
// 与 SetSessionPendingReply 同理：返回的 id 是收尾时唯一能定位「这一轮」的凭据。
func (s *AppContext) BroadcastSessionUserMessageAt(rootID, sessionKey, sessionType, sessionName, agentName, model, modelDisplayName, mode, effort, fastService string, planMode bool, content string, timestamp time.Time, userExchangeSeq int, userTurnID string) string {
	s.ClearTaskAuxFlagsForSession(rootID, sessionKey)
	return s.GetSessionStreamHub().BroadcastSessionUserMessageAt(rootID, sessionKey, sessionType, sessionName, agentName, model, modelDisplayName, mode, effort, fastService, planMode, content, timestamp, userExchangeSeq, userTurnID, "", false)
}

func (s *AppContext) BroadcastSessionUpdate(rootID, sessionKey string, update agenttypes.Event) {
	event := updateToEvent(update)
	if event == nil {
		return
	}
	s.updateTaskAuxFlagsFromEvent(rootID, sessionKey, event)
	s.updateSessionAwaitingFromEvent(rootID, sessionKey, event)
	s.notifyAskUserIfNeeded(rootID, sessionKey, event)
	s.GetSessionStreamHub().BroadcastSessionStream(rootID, sessionKey, event)
}

func (s *AppContext) BroadcastSessionError(rootID, sessionKey, message string) {
	s.UpdateTaskSessionErrorForSession(rootID, sessionKey, message)
	s.GetSessionStreamHub().BroadcastSessionStream(rootID, sessionKey, &StreamEvent{
		Type: "error",
		Data: map[string]string{"message": normalizeAgentErrorMessage(errors.New(message))},
	})
}

func (s *AppContext) BroadcastScheduledTaskDone(rootID, taskID, taskName, sessionKey, summary string) {
	s.notifyScheduled(rootID, taskID, taskName, sessionKey, summary, "", true)
}

func (s *AppContext) BroadcastScheduledTaskFailed(rootID, taskID, taskName, sessionKey, message string) {
	s.notifyScheduled(rootID, taskID, taskName, sessionKey, "", message, false)
}
