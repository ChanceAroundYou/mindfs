package api

import (
	"strings"
	"time"

	"mindfs/server/internal/agent"
	"mindfs/server/internal/fs"
	"mindfs/server/internal/githubimport"
	"mindfs/server/internal/session"
	"mindfs/server/internal/update"

	"github.com/gorilla/websocket"
)

func (h *WSHandler) broadcastFileChange(change fs.FileChangeEvent) {
	resp := WSResponse{
		Type: "file.changed",
		Payload: map[string]any{
			"root_id": change.RootID,
			"path":    change.Path,
			"op":      change.Op,
			"is_dir":  change.IsDir,
		},
	}
	h.broadcastWS(resp)
}

func (h *WSHandler) broadcastFileChangeBatch(change fs.FileChangeBatchEvent) {
	events := make([]map[string]any, 0, len(change.Events))
	for _, event := range change.Events {
		events = append(events, map[string]any{
			"path":   event.Path,
			"op":     event.Op,
			"is_dir": event.IsDir,
		})
	}
	resp := WSResponse{
		Type: "file.changed.batch",
		Payload: map[string]any{
			"root_id": change.RootID,
			"paths":   change.Paths,
			"dirs":    change.Dirs,
			"events":  events,
		},
	}
	h.broadcastWS(resp)
}

func (h *WSHandler) broadcastRelatedFileChange(change fs.RelatedFileEvent) {
	payload := map[string]any{
		"root_id":     change.RootID,
		"session_key": change.SessionKey,
		"path":        change.Path,
	}
	resp := WSResponse{
		Type:    "session.related_files.updated",
		Payload: payload,
	}
	if change.RelatedWorktree != nil {
		payload["related_worktree"] = map[string]any{
			"root_id": change.RootID,
			"path":    change.RelatedWorktree.Path,
			"branch":  change.RelatedWorktree.Branch,
			"head":    change.RelatedWorktree.Head,
			"current": change.RelatedWorktree.Current,
		}
	}
	h.broadcastWS(resp)
}

func (h *WSHandler) broadcastSessionMetaUpdated(rootID string, sess *session.Session) {
	if sess == nil {
		h.broadcastWS(WSResponse{Type: "session.meta.updated"})
		return
	}
	resp := WSResponse{
		Type: "session.meta.updated",
		Payload: map[string]any{
			"root_id": rootID,
			"session": sessionMetaUpdatedPayload(sess),
		},
	}
	h.broadcastWS(resp)
}

// sessionMetaUpdatedPayload 是 session.meta.updated 的会话体白名单。抽成函数是为了
// 能被测试直接调用 —— 它与会话列表/详情两处 payload 是**各自独立**的白名单，
// 加字段时漏掉任何一处都只会静默少一个字段（见 http_session_work_dir_test.go）。
func sessionMetaUpdatedPayload(sess *session.Session) map[string]any {
	return map[string]any{
		"key":                 sess.Key,
		"type":                sess.Type,
		"parent_session_key":  sess.ParentSessionKey,
		"parent_tool_call_id": sess.ParentToolCallID,
		"source":              sess.Source,
		"task_id":             sess.TaskID,
		"name":                sess.Name,
		"model":               sess.Model,
		"mode":                session.InferModeFromSession(sess),
		"effort":              session.InferEffortFromSession(sess),
		"fast_service":        session.InferFastServiceFromSession(sess),
		"plan_mode":           sess.PlanMode,
		"related_worktree":    sess.RelatedWorktree,
		"worktree_missing":    sess.RelatedWorktree.WorktreeMissing(),
		"worktree_active":     relatedWorktreeActive(sess.RelatedWorktree),
		"work_dir":            sess.WorkDir,
		"aux_flags":           sess.Aux.AuxFlagsPayload(),
		"pinned_at":           sess.PinnedAt,
		"updated_at":          sess.UpdatedAt,
	}
}

func (h *WSHandler) broadcastAgentStatusChange(status agent.Status) {
	status = applyAgentAPIProviderCapabilities([]agent.Status{status})[0]
	resp := WSResponse{
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
	}
	h.broadcastWS(resp)
}

func (h *WSHandler) broadcastWS(resp WSResponse) {
	if h.AppContext == nil {
		return
	}
	h.AppContext.GetSessionStreamHub().BroadcastAll(resp)
}

func (h *WSHandler) broadcastAppUpdate(status update.Status) {
	resp := WSResponse{
		Type:    "app.update",
		Payload: map[string]any{"state": status},
	}
	h.broadcastWS(resp)
}

func (h *WSHandler) pushInitialAppUpdate(clientID string) {
	if h.AppContext == nil || h.AppContext.GetUpdateService() == nil {
		return
	}
	h.AppContext.GetSessionStreamHub().SendToClient(clientID, WSResponse{
		Type:    "app.update",
		Payload: map[string]any{"state": h.AppContext.GetUpdateService().GetStatus()},
	})
}

func (h *WSHandler) broadcastGitHubImport(status githubimport.Status) {
	resp := WSResponse{
		Type:    "github.import",
		Payload: map[string]any{"status": status},
	}
	h.broadcastWS(resp)
}

func (h *WSHandler) pushInitialGitHubImports(clientID string) {
	if h.AppContext == nil || h.AppContext.GetGitHubImportService() == nil {
		return
	}
	for _, status := range h.AppContext.GetGitHubImportService().ActiveStatuses() {
		h.AppContext.GetSessionStreamHub().SendToClient(clientID, WSResponse{
			Type:    "github.import",
			Payload: map[string]any{"status": status},
		})
	}
}

func (h *WSHandler) sendWSError(conn *websocket.Conn, clientID, id, code, message string) {
	resp := WSResponse{
		ID:   id,
		Type: "session.error",
		Error: &WSResponseError{
			Code:    code,
			Message: message,
		},
		Payload: map[string]any{},
	}
	_ = h.writeWSJSON(clientID, conn, resp)
}

func (h *WSHandler) sendWSAccepted(conn *websocket.Conn, clientID, requestID, rootID, sessionKey string) {
	h.sendWSAcceptedAt(conn, clientID, requestID, rootID, sessionKey, time.Time{}, "")
}

func (h *WSHandler) sendWSAcceptedAt(conn *websocket.Conn, clientID, requestID, rootID, sessionKey string, timestamp time.Time, modelDisplayName string) {
	payload := map[string]any{
		"request_id":  requestID,
		"root_id":     rootID,
		"session_key": sessionKey,
	}
	if !timestamp.IsZero() {
		payload["timestamp"] = timestamp.UTC()
	}
	if strings.TrimSpace(modelDisplayName) != "" {
		payload["model_display_name"] = modelDisplayName
	}
	resp := WSResponse{
		ID:      requestID,
		Type:    "session.accepted",
		Payload: payload,
	}
	_ = h.writeWSJSON(clientID, conn, resp)
}

func (h *WSHandler) reserveClientRequest(requestID string) (time.Time, bool) {
	requestID = strings.TrimSpace(requestID)
	now := time.Now().UTC()
	if requestID == "" {
		return now, true
	}
	h.requestMu.Lock()
	defer h.requestMu.Unlock()
	if h.requests == nil {
		h.requests = make(map[string]time.Time)
	}
	cutoff := time.Now().Add(-24 * time.Hour)
	for id, seenAt := range h.requests {
		if seenAt.Before(cutoff) {
			delete(h.requests, id)
		}
	}
	if seenAt, exists := h.requests[requestID]; exists {
		return seenAt, false
	}
	h.requests[requestID] = now
	return now, true
}
