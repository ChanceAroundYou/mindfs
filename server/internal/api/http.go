package api

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	htmpl "html/template"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	stdpath "path"
	"path/filepath"
	"regexp"
	"strings"

	"mindfs/internal/deploy"
	"mindfs/server/internal/agent"
	"mindfs/server/internal/api/usecase"
	"mindfs/server/internal/githubimport"
	"mindfs/server/internal/nodeinfo"
	"mindfs/server/internal/preferences"

	"github.com/go-chi/chi/v5"
)

// HTTPHandler provides REST endpoints for health, tree, file, and action.
type HTTPHandler struct {
	AppContext    *AppContext
	StaticDir     string
	Version       string
	LocalCLIToken string
	// NodeRole 决定这台机器提不提供控制面。零值 "" 视为 RoleControl
	// —— 默认必须等于改造前的行为，否则升级不改配置就坏。
	NodeRole nodeinfo.Role
}

const (
	maxUploadRequestBytes = 64 << 20
	maxUploadFileCount    = 20
	sessionListPageSize   = 50
	multiRootSessionLimit = 6
	childSessionPageSize  = 20
	childSessionMaxLimit  = 50
	clientIDHeaderName    = "X-MindFS-Client-ID"
	localCLIHeaderName    = "X-MindFS-Local-CLI-Token"
)

var indexResourceRefPattern = regexp.MustCompile(`(?i)\b(?:src|href)\s*=\s*["']([^"']+)["']`)

func (h *HTTPHandler) service() *usecase.Service {
	return &usecase.Service{Registry: h.AppContext}
}

func (h *HTTPHandler) isLocalCLIRequest(r *http.Request) bool {
	token := strings.TrimSpace(h.LocalCLIToken)
	if token == "" || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(r.Header.Get(localCLIHeaderName))), []byte(token)) != 1 {
		return false
	}
	if !isLocalCLIPath(r) {
		return false
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		host = strings.TrimSpace(r.RemoteAddr)
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isLocalCLIPath(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}
	if strings.HasSuffix(r.URL.Path, "/approve-plan") {
		return false
	}
	if r.Method == http.MethodPost {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
		if len(parts) == 4 && parts[0] == "api" && parts[1] == "sessions" && parts[2] != "" && parts[3] == "messages" {
			return true
		}
	}
	if r.Method == http.MethodGet && (r.URL.Path == "/api/agents" || r.URL.Path == "/api/task-templates" || r.URL.Path == "/api/tasks") {
		return true
	}
	if r.Method == http.MethodPost && r.URL.Path == "/api/tasks" {
		return true
	}
	if (r.Method == http.MethodPatch || r.Method == http.MethodDelete) && isLocalCLITaskPath(r.URL.Path) {
		return true
	}
	switch r.Method {
	case http.MethodPost:
		return r.URL.Path == "/api/dirs" || isLocalCLITaskPath(r.URL.Path)
	case http.MethodDelete:
		return r.URL.Path == "/api/dirs"
	case http.MethodGet:
		return isLocalCLITaskPath(r.URL.Path)
	default:
		return false
	}
}

func isLocalCLITaskPath(path string) bool {
	if !strings.HasPrefix(path, "/api/tasks/") {
		return false
	}
	if strings.TrimPrefix(path, "/api/tasks/") == "" {
		return false
	}
	return true
}

func (h *HTTPHandler) broadcastRootChanged(action, rootID string, extra ...map[string]any) {
	if h.AppContext == nil {
		return
	}
	payload := map[string]any{
		"action":  action,
		"root_id": rootID,
	}
	for _, fields := range extra {
		for key, value := range fields {
			payload[key] = value
		}
	}
	h.AppContext.GetSessionStreamHub().BroadcastAll(WSResponse{
		Type:    "root.changed",
		Payload: payload,
	})
}

func (h *HTTPHandler) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		shouldEmit := false
		emitOrigin := origin
		prefs := h.AppContext.GetPreferences()
		mode := ""
		if prefs != nil {
			mode = prefs.CORSMode()
		}
		if mode == "" {
			mode = "open"
		}
		mode = strings.ToLower(strings.TrimSpace(mode))
		switch mode {
		case "open", "auto", "allow_all", "all", "*":
			shouldEmit = origin != ""
		case "allowlist", "whitelist":
			shouldEmit = prefs != nil && prefs.IsCORSOriginAllowed(origin)
		case "disabled", "off", "closed", "same_origin":
			shouldEmit = false
		default:
			shouldEmit = origin != ""
		}
		if shouldEmit && emitOrigin != "" {
			w.Header().Set("Access-Control-Allow-Origin", emitOrigin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-MindFS-Client-ID, X-MindFS-Local-CLI-Token, Authorization, X-Requested-With")
			w.Header().Set("Access-Control-Max-Age", "86400")
			w.Header().Set("Access-Control-Allow-Private-Network", "true")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Routes constructs the chi router with all endpoints.
// The deployment prefix is stripped once at the top-level mux (StripDeployPrefix),
// so every route here is registered prefix-free.
func (h *HTTPHandler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(h.corsMiddleware)
	r.Use(h.rejectControlPlaneOnWorker)
	r.Options("/*", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	r.NotFound(h.handleNotFound)
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) { h.handleNotFound(w, r) })
	r.Get("/", h.handleFrontend)
	r.Get("/health", h.handleHealth)
	// 角色自述：前端靠它知道对面提不提供 UI（static=false 就不给「打开网页」入口）。
	// 它自己也在控制面前缀表里，所以 worker 上这个端点是 403 —— 前端据此
	// 「探测失败 = 没有 UI」，与「节点挂了」区分开。
	r.Get("/api/node-info", h.handleNodeInfo)
	// 主页面登录 + 账户管理：公开端点。
	r.Get("/api/auth/status", h.handleAuthStatus)
	r.Post("/api/auth/login", h.handleAuthLogin)
	r.Get("/api/users", h.handleUsersList)
	r.Post("/api/users", h.handleUserCreate)
	r.Put("/api/users/{id}", h.handleUserUpdate)
	r.Post("/api/users/{id}/primary", h.handleUserSetPrimary)
	r.Delete("/api/users/{id}", h.handleUserDelete)
	r.Get("/api/tree", h.handleTree)
	r.Get("/api/file", h.handleFile)
	r.Put("/api/file", h.handleFileSave)
	r.Post("/api/file", h.handleFileCreate)
	r.Post("/api/file/operation", h.handleFileOperation)
	r.Get("/api/git/status", h.handleGitStatus)
	r.Get("/api/git/diff", h.handleGitDiff)
	r.Get("/api/git/history", h.handleGitHistory)
	r.Get("/api/git/commit/files", h.handleGitCommitFiles)
	r.Get("/api/git/commit/diff", h.handleGitCommitDiff)
	r.Get("/api/git/related-file/diff", h.handleGitRelatedFileDiff)
	// 批量版：一次拿回 N 个关联文件的 status/additions/deletions，不取 diff 正文。
	// 用 POST 是因为 80 个路径塞不进 URL；单文件那条 GET 仍然保留（点开看 diff 要正文）。
	r.Post("/api/git/related-files/stats", h.handleGitRelatedFileStats)
	r.Get("/api/git/branches", h.handleGitBranches)
	r.Get("/api/git/worktrees", h.handleGitWorktreeList)
	r.Post("/api/git/checkout", h.handleGitCheckout)
	r.Post("/api/git/pull", h.handleGitPull)
	r.Post("/api/git/push", h.handleGitPush)
	r.Post("/api/git/commit", h.handleGitCommit)
	r.Post("/api/git/stage", h.handleGitStage)
	r.Post("/api/git/unstage", h.handleGitUnstage)
	r.Post("/api/git/discard", h.handleGitDiscard)
	r.Post("/api/git/worktrees", h.handleGitWorktreeCreate)
	r.Delete("/api/git/worktrees", h.handleGitWorktreeRemove)
	r.Post("/api/upload", h.handleUpload)
	r.Get("/api/candidates", h.handleCandidates)
	r.Post("/api/prompts", h.handlePromptSave)
	r.Delete("/api/prompts", h.handlePromptDelete)
	r.Get("/api/sessions", h.handleSessions)
	r.Get("/api/preferences/session-naming", h.handleSessionNamingPreferenceGet)
	r.Put("/api/preferences/session-naming", h.handleSessionNamingPreferencePut)
	r.Get("/api/preferences/idle-session-resource-release", h.handleIdleSessionResourceReleasePreferenceGet)
	r.Put("/api/preferences/idle-session-resource-release", h.handleIdleSessionResourceReleasePreferencePut)
	r.Get("/api/agents/memory", h.handleAgentMemoryGet)
	r.Post("/api/agents/release-idle", h.handleAgentIdleRelease)
	r.Get("/api/preferences/new-project-meta-location", h.handleNewProjectMetaLocationPreferenceGet)
	r.Put("/api/preferences/new-project-meta-location", h.handleNewProjectMetaLocationPreferencePut)
	r.Get("/api/preferences/cors", h.handleCORSPreferenceGet)
	r.Put("/api/preferences/cors", h.handleCORSPreferencePut)
	// 置顶是控制面：权威在主节点，worker 上 403（nodeinfo 前缀表含 /api/pins）。
	r.Get("/api/pins", h.handlePinsGet)
	r.Put("/api/pins/project", h.handlePinsProjectPut)
	r.Put("/api/pins/session", h.handlePinsSessionPut)
	r.Get("/api/replying-sessions", h.handleReplyingSessions)
	r.Get("/api/sessions/search", h.handleSessionSearch)
	r.Get("/api/sessions/children", h.handleSessionChildren)
	r.Get("/api/sessions/external", h.handleExternalSessionsList)
	r.Post("/api/sessions/import", h.handleExternalSessionImport)
	r.Post("/api/sessions/import/batch", h.handleExternalSessionImportBatch)
	r.Post("/api/sessions/fork", h.handleSessionFork)
	r.Post("/api/sessions/{key}/repoint", h.handleSessionRepoint)
	// 无 path 参数的同一操作：给 wt-finish 这类只有 CLAUDE_CODE_SESSION_ID 的脚本用，
	// body 里带 agent_session_id，服务端反查 mindfs 会话。
	r.Post("/api/sessions/repoint", h.handleSessionRepoint)
	r.Get("/api/sessions/{key}/toolcalls/{callID}", h.handleSessionToolCallGet)
	r.Post("/api/sessions/{key}/sync", h.handleSessionSync)
	r.Get("/api/sessions/{key}", h.handleSessionGet)
	r.Post("/api/sessions/{key}/messages", h.handleSessionUserMessage)
	r.Get("/api/sessions/{key}/related-files", h.handleSessionRelatedFilesGet)
	r.Post("/api/sessions/{key}/pin", h.handleSessionPin)
	r.Post("/api/sessions/{key}/archive", h.handleSessionArchive)
	r.Post("/api/sessions/{key}/rename", h.handleSessionRename)
	r.Delete("/api/sessions/{key}/related-files", h.handleSessionRelatedFilesDelete)
	r.Delete("/api/sessions/{key}", h.handleSessionDelete)
	r.Get("/api/scheduled-agent-tasks", h.handleScheduledAgentTasksList)
	r.Post("/api/scheduled-agent-tasks", h.handleScheduledAgentTaskCreate)
	r.Put("/api/scheduled-agent-tasks/{id}", h.handleScheduledAgentTaskUpdate)
	r.Delete("/api/scheduled-agent-tasks/{id}", h.handleScheduledAgentTaskDelete)
	r.Post("/api/scheduled-agent-tasks/{id}/run", h.handleScheduledAgentTaskRun)
	r.Get("/api/task-stage-templates", h.handleTaskStageTemplatesList)
	r.Post("/api/task-stage-templates", h.handleTaskStageTemplateSave)
	r.Delete("/api/task-stage-templates/{id}", h.handleTaskStageTemplateDelete)
	r.Get("/api/task-templates", h.handleTaskTemplatesList)
	r.Post("/api/task-templates", h.handleTaskTemplateSave)
	r.Put("/api/task-templates/{id}", h.handleTaskTemplateSave)
	r.Delete("/api/task-templates/{id}", h.handleTaskTemplateDelete)
	r.Get("/api/tasks", h.handleKanbanTasksList)
	r.Get("/api/tasks/overview", h.handleKanbanTasksOverview)
	r.Post("/api/tasks", h.handleKanbanTaskCreate)
	r.Post("/api/tasks/{id}/input", h.handleKanbanTaskInputUpdate)
	r.Post("/api/tasks/{id}/rename", h.handleKanbanTaskRename)
	r.Post("/api/tasks/{id}/add-stage", h.handleKanbanTaskAddStage)
	r.Post("/api/tasks/{id}/update-stage", h.handleKanbanTaskUpdateStage)
	r.Post("/api/tasks/{id}/remove-stage", h.handleKanbanTaskRemoveStage)
	r.Post("/api/tasks/{id}/rerun", h.handleKanbanTaskRerun)
	r.Post("/api/tasks/{id}/next", h.handleKanbanTaskNext)
	r.Post("/api/tasks/{id}/run-now", h.handleKanbanTaskRunNow)
	r.Post("/api/tasks/{id}/pause", h.handleKanbanTaskPause)
	r.Post("/api/tasks/{id}/resume", h.handleKanbanTaskResume)
	r.Post("/api/tasks/{id}/complete", h.handleKanbanTaskComplete)
	r.Post("/api/tasks/{id}/cancel", h.handleKanbanTaskCancel)
	r.Post("/api/tasks/{id}/fail", h.handleKanbanTaskFail)
	r.Delete("/api/tasks/{id}", h.handleKanbanTaskDelete)
	r.Post("/api/tasks/{id}/rebuild-worktree", h.handleKanbanTaskRebuildWorktree)
	r.Post("/api/tasks/{id}/finish-worktree", h.handleKanbanTaskFinishWorktree)
	r.Post("/api/tasks/{id}/begin-finish", h.handleKanbanTaskBeginFinish)
	r.Get("/api/dirs", h.handleDirs)
	r.Post("/api/dirs", h.handleAddDir)
	r.Post("/api/dirs/{id}/rename", h.handleRenameDir)
	r.Post("/api/dirs/{id}/display-name", h.handleUpdateDirDisplayName)
	r.Delete("/api/dirs", h.handleRemoveDir)
	r.Get("/api/nodes", h.handleNodesList)
	r.Put("/api/nodes", h.handleNodesPut)
	r.Post("/api/nodes", h.handleNodesPost)
	r.Delete("/api/nodes/{id}", h.handleNodesDelete)
	r.Get("/api/local_dirs", h.handleLocalDirs)
	r.Get("/api/app/update", h.handleAppUpdateGet)
	r.Post("/api/app/update", h.handleAppUpdatePost)
	r.Post("/api/imports/github", h.handleGitHubImportStart)
	r.Get("/api/web-push/status", h.handleWebPushStatus)
	r.Post("/api/web-push/subscriptions", h.handleWebPushSubscriptionSave)
	r.Delete("/api/web-push/subscriptions", h.handleWebPushSubscriptionDelete)
	r.Post("/api/web-push/test", h.handleWebPushTest)

	// Agent status API
	r.Get("/api/agents", h.handleAgentsList)
	r.Post("/api/agents/restart", h.handleAgentRestart)
	r.Get("/api/agents/codex/rate-limits", h.handleCodexRateLimitsGet)
	r.Post("/api/agents/codex/rate-limit-reset", h.handleCodexRateLimitReset)
	r.Get("/api/agent-config/defaults", h.handleAgentConfigDefaults)
	r.Get("/api/agent-config/backups", h.handleAgentConfigBackupsList)
	r.Post("/api/agent-config/backups", h.handleAgentConfigBackupCreate)
	r.Delete("/api/agent-config/backups", h.handleAgentConfigBackupDelete)
	r.Post("/api/agent-config/switch", h.handleAgentConfigSwitch)
	r.Get("/api/agent-api-providers", h.handleAgentAPIProvidersList)
	r.Post("/api/agent-api-providers", h.handleAgentAPIProviderCreate)
	r.Post("/api/agent-api-providers/sync", h.handleAgentAPIProvidersSync)
	r.Post("/api/agent-api-providers/sync-all", h.handleAgentAPIProvidersSyncAll)
	r.Post("/api/agent-api-providers/test", h.handleAgentAPIProviderTest)
	r.Delete("/api/agent-api-providers", h.handleAgentAPIProviderDelete)
	r.Post("/api/agent-api-providers/switch", h.handleAgentAPIProviderSwitch)
	r.NotFound(h.handleNotFound)

	return r
}

func (h *HTTPHandler) handleSessionRepoint(w http.ResponseWriter, r *http.Request) {
	// path 里的 key 可选：wt-finish 这类脚本手里只有 CLAUDE_CODE_SESSION_ID，
	// 没有 mindfs 的 session key，所以也允许放 agent_session_id 让服务端反查。
	key := strings.TrimSpace(chi.URLParam(r, "key"))
	var req struct {
		RootID         string `json:"root_id"`
		AgentSessionID string `json:"agent_session_id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	agentSessionID := strings.TrimSpace(req.AgentSessionID)
	if key == "" && agentSessionID == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("session key or agent_session_id required"))
		return
	}
	if strings.TrimSpace(req.RootID) == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("root_id is required"))
		return
	}
	out, err := h.service().RepointSession(r.Context(), usecase.RepointSessionInput{
		RootID:         strings.TrimSpace(req.RootID),
		Key:            key,
		AgentSessionID: agentSessionID,
	})
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	// 搬完之后前端持有的还是旧会话视图（worktree 标签、agent_session_id），广播一次强制刷新。
	if h.AppContext != nil {
		h.AppContext.GetSessionStreamHub().BroadcastAll(WSResponse{
			Type: "session.repointed",
			Payload: map[string]any{
				"root_id":                   strings.TrimSpace(req.RootID),
				"session_key":               out.SessionKey,
				"agent":                     out.Agent,
				"previous_agent_session_id": out.PreviousAgentSessionID,
				"agent_session_id":          out.AgentSessionID,
			},
		})
	}
	respondJSON(w, http.StatusOK, out)
}

func (h *HTTPHandler) handleAgentsList(w http.ResponseWriter, r *http.Request) {
	if h.AppContext == nil || h.AppContext.GetProber() == nil {
		log.Printf("[http] agents.list.short_circuit returning_empty_response")
		respondJSONConditional(w, r, map[string]any{
			"agents": []map[string]any{},
			"shells": []map[string]any{},
		})
		return
	}
	statuses := h.AppContext.GetProber().GetInstalledStatuses()
	if r.URL.Query().Get("all") == "1" {
		statuses = h.AppContext.GetProber().GetConfiguredStatuses()
	}
	if prefs := h.AppContext.GetPreferences(); prefs != nil {
		statuses = prefs.ApplyAgentDefaults(statuses)
	}
	statuses = applyAgentAPIProviderCapabilities(statuses)
	shells := []agent.ShellStatus{}
	if pool := h.AppContext.GetAgentPool(); pool != nil {
		shells = pool.AvailableShells()
	}
	respondJSONConditional(w, r, map[string]any{
		"agents": statuses,
		"shells": shells,
	})
}

type sessionNamingPreferenceRequest struct {
	Agent    string `json:"agent"`
	Model    string `json:"model"`
	Disabled bool   `json:"disabled"`
}

type idleSessionResourceReleasePreferenceRequest struct {
	Hours int `json:"hours"`
}

type newProjectMetaLocationPreferenceRequest struct {
	Location string `json:"location"`
}

func (h *HTTPHandler) handleNewProjectMetaLocationPreferenceGet(w http.ResponseWriter, _ *http.Request) {
	if h.AppContext == nil || h.AppContext.GetPreferences() == nil {
		respondError(w, http.StatusServiceUnavailable, errInvalidRequest("preferences not configured"))
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"location": h.AppContext.GetPreferences().NewProjectMetaLocation()})
}

func (h *HTTPHandler) handleNewProjectMetaLocationPreferencePut(w http.ResponseWriter, r *http.Request) {
	if h.AppContext == nil || h.AppContext.GetPreferences() == nil {
		respondError(w, http.StatusServiceUnavailable, errInvalidRequest("preferences not configured"))
		return
	}
	var req newProjectMetaLocationPreferenceRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest(err.Error()))
		return
	}
	if err := h.AppContext.GetPreferences().UpdateNewProjectMetaLocation(req.Location); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest(err.Error()))
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"location": h.AppContext.GetPreferences().NewProjectMetaLocation()})
}

func (h *HTTPHandler) handleIdleSessionResourceReleasePreferenceGet(w http.ResponseWriter, _ *http.Request) {
	if h.AppContext == nil || h.AppContext.GetPreferences() == nil {
		respondError(w, http.StatusServiceUnavailable, errInvalidRequest("preferences not configured"))
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"hours": h.AppContext.GetPreferences().IdleSessionResourceReleaseHours(),
	})
}

func (h *HTTPHandler) handleIdleSessionResourceReleasePreferencePut(w http.ResponseWriter, r *http.Request) {
	if h.AppContext == nil || h.AppContext.GetPreferences() == nil {
		respondError(w, http.StatusServiceUnavailable, errInvalidRequest("preferences not configured"))
		return
	}
	var req idleSessionResourceReleasePreferenceRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest(err.Error()))
		return
	}
	if req.Hours <= 0 || req.Hours > preferences.MaxIdleSessionResourceReleaseHours {
		respondError(w, http.StatusBadRequest, errInvalidRequest("hours are out of range"))
		return
	}
	if err := h.AppContext.GetPreferences().UpdateIdleSessionResourceReleaseHours(req.Hours); err != nil {
		respondError(w, http.StatusInternalServerError, errInvalidRequest(err.Error()))
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"hours": req.Hours})
}

func (h *HTTPHandler) handleSessionNamingPreferenceGet(w http.ResponseWriter, _ *http.Request) {
	if h.AppContext == nil || h.AppContext.GetPreferences() == nil {
		respondError(w, http.StatusServiceUnavailable, errInvalidRequest("preferences not configured"))
		return
	}
	pref := h.AppContext.GetPreferences().SessionNamingDefaults()
	respondJSON(w, http.StatusOK, map[string]any{
		"agent":    pref.Agent,
		"model":    pref.Model,
		"disabled": pref.Disabled,
	})
}

func (h *HTTPHandler) handleSessionNamingPreferencePut(w http.ResponseWriter, r *http.Request) {
	if h.AppContext == nil || h.AppContext.GetPreferences() == nil {
		respondError(w, http.StatusServiceUnavailable, errInvalidRequest("preferences not configured"))
		return
	}
	var req sessionNamingPreferenceRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest(err.Error()))
		return
	}
	req.Agent = strings.TrimSpace(req.Agent)
	req.Model = strings.TrimSpace(req.Model)
	if req.Agent == "" && !req.Disabled {
		respondError(w, http.StatusBadRequest, errInvalidRequest("session naming agent is required"))
		return
	}
	if !req.Disabled {
		if h.AppContext.GetProber() == nil {
			respondError(w, http.StatusServiceUnavailable, errInvalidRequest("agent status not configured"))
			return
		}
		validAgent := false
		validModel := req.Model == ""
		for _, status := range h.AppContext.GetProber().GetInstalledStatuses() {
			if strings.TrimSpace(status.Name) != req.Agent {
				continue
			}
			validAgent = true
			if req.Model != "" {
				for _, model := range status.Models {
					if strings.TrimSpace(model.ID) == req.Model {
						validModel = true
						break
					}
				}
			}
			break
		}
		if !validAgent {
			respondError(w, http.StatusBadRequest, errInvalidRequest("session naming agent is not installed"))
			return
		}
		if !validModel {
			respondError(w, http.StatusBadRequest, errInvalidRequest("session naming model is not supported by the selected agent"))
			return
		}
	}
	if err := h.AppContext.GetPreferences().UpdateSessionNamingDefaults(req.Agent, req.Model, req.Disabled); err != nil {
		respondError(w, http.StatusInternalServerError, errInvalidRequest(err.Error()))
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"agent":    req.Agent,
		"model":    req.Model,
		"disabled": req.Disabled,
	})
}

type corsPreferenceRequest struct {
	Mode         string   `json:"mode"`
	AllowOrigins []string `json:"allow_origins"`
}

func (h *HTTPHandler) handleCORSPreferenceGet(w http.ResponseWriter, _ *http.Request) {
	if h.AppContext == nil || h.AppContext.GetPreferences() == nil {
		respondError(w, http.StatusServiceUnavailable, errInvalidRequest("preferences not configured"))
		return
	}
	prefs := h.AppContext.GetPreferences()
	mode := prefs.CORSMode()
	if mode == "" {
		mode = "open"
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"mode":          mode,
		"allow_origins": prefs.CORSAllowOrigins(),
	})
}

func (h *HTTPHandler) handleCORSPreferencePut(w http.ResponseWriter, r *http.Request) {
	if h.AppContext == nil || h.AppContext.GetPreferences() == nil {
		respondError(w, http.StatusServiceUnavailable, errInvalidRequest("preferences not configured"))
		return
	}
	var req corsPreferenceRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest(err.Error()))
		return
	}
	if err := h.AppContext.GetPreferences().UpdateCORSPreferences(req.Mode, req.AllowOrigins); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest(err.Error()))
		return
	}
	prefs := h.AppContext.GetPreferences()
	mode := prefs.CORSMode()
	if mode == "" {
		mode = "open"
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"mode":          mode,
		"allow_origins": prefs.CORSAllowOrigins(),
	})
}

func (h *HTTPHandler) handleAppUpdateGet(w http.ResponseWriter, r *http.Request) {
	if h.AppContext == nil || h.AppContext.GetUpdateService() == nil {
		respondJSON(w, http.StatusOK, map[string]any{
			"status": "idle",
		})
		return
	}
	respondJSON(w, http.StatusOK, h.AppContext.GetUpdateService().GetStatus())
}

func (h *HTTPHandler) handleAppUpdatePost(w http.ResponseWriter, r *http.Request) {
	if h.AppContext == nil || h.AppContext.GetUpdateService() == nil {
		respondError(w, http.StatusServiceUnavailable, errInvalidRequest("update service not configured"))
		return
	}
	if err := h.AppContext.GetUpdateService().TriggerUpdate(r.Context()); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest(err.Error()))
		return
	}
	respondJSON(w, http.StatusOK, h.AppContext.GetUpdateService().GetStatus())
}

func (h *HTTPHandler) handleLocalDirs(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	out, err := h.service().ListLocalDirs(r.Context(), usecase.ListLocalDirsInput{
		Path: path,
	})
	if err != nil {
		status := http.StatusBadRequest
		if os.IsNotExist(err) {
			status = http.StatusNotFound
		}
		respondError(w, status, err)
		return
	}
	respondJSON(w, http.StatusOK, out)
}

func (h *HTTPHandler) handleGitHubImportStart(w http.ResponseWriter, r *http.Request) {
	if h.AppContext == nil || h.AppContext.GetGitHubImportService() == nil {
		respondError(w, http.StatusServiceUnavailable, errInvalidRequest("github import service not configured"))
		return
	}
	var req struct {
		URL        string `json:"url"`
		ParentPath string `json:"parent_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	status, err := h.AppContext.GetGitHubImportService().Start(r.Context(), githubimport.StartInput{
		URL:        req.URL,
		ParentPath: req.ParentPath,
	})
	if err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest(err.Error()))
		return
	}
	respondJSON(w, http.StatusAccepted, map[string]any{
		"task_id": status.TaskID,
		"status":  "accepted",
	})
}

func (h *HTTPHandler) handleFrontend(w http.ResponseWriter, r *http.Request) {
	// 运行节点不提供前端：StaticDir 为空时下面会落到 renderFallbackFrontend，
	// 输出一张「前端资源缺失」的提示页 —— 那会让用户以为是装坏了，而实际上是
	// 这台机器按配置就不提供 UI。给一个明确的 403。
	if h.NodeRole == nodeinfo.RoleWorker {
		respondError(w, http.StatusForbidden, errInvalidRequest("node_is_worker"))
		return
	}
	if h.serveStaticAsset(w, r) {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(renderFallbackFrontend(indexHTML, frontendAssetMissingNotice(r.URL.Path))))
}

// handleHealth 顺带报角色：前端要靠它区分「对面挂了」和「对面是运行节点，
// 本来就不提供 UI」。原来返回纯文本 "ok"，改成 JSON —— 仓库内的消费者只有
// NodeManagerPanel.probeNode（用 protectedJSON，能解析即成功），deploy-all.sh
// 不碰这个端点。
func (h *HTTPHandler) handleHealth(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"role": string(nodeinfo.Normalize(string(h.NodeRole))),
	})
}

func (h *HTTPHandler) handleNotFound(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api" || r.URL.Path == "/ws" || r.URL.Path == "/health" {
		http.NotFound(w, r)
		return
	}
	h.handleFrontend(w, r)
}

func (h *HTTPHandler) serveStaticAsset(w http.ResponseWriter, r *http.Request) bool {
	staticDir := strings.TrimSpace(h.StaticDir)
	if staticDir == "" {
		return false
	}

	cleanPath := pathForStaticAsset(r.URL.Path)
	if cleanPath == "" {
		cleanPath = "index.html"
	}

	assetPath := filepath.Join(staticDir, cleanPath)
	info, statErr := os.Stat(assetPath)
	if statErr == nil && !info.IsDir() {
		applyStaticCacheHeaders(w, cleanPath)
		if cleanPath == "index.html" {
			h.serveFrontendIndex(w, r, staticDir, assetPath)
			return true
		}
		http.ServeFile(w, r, assetPath)
		return true
	}

	if filepath.Ext(cleanPath) != "" {
		http.NotFound(w, r)
		return true
	}

	indexPath := filepath.Join(staticDir, "index.html")
	if info, err := os.Stat(indexPath); err == nil && !info.IsDir() {
		applyStaticCacheHeaders(w, "index.html")
		h.serveFrontendIndex(w, r, staticDir, indexPath)
		return true
	}

	return false
}

func (h *HTTPHandler) serveFrontendIndex(w http.ResponseWriter, r *http.Request, staticDir, indexPath string) {
	content, err := os.ReadFile(indexPath)
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(renderFallbackFrontend(indexHTML, frontendAssetMissingNotice(r.URL.Path))))
		return
	}
	if missing := missingFrontendIndexResource(staticDir, content); missing != "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(renderFallbackFrontend(indexHTML, frontendAssetMissingNotice(missing))))
		return
	}
	http.ServeFile(w, r, indexPath)
}

func missingFrontendIndexResource(staticDir string, indexContent []byte) string {
	matches := indexResourceRefPattern.FindAllSubmatch(indexContent, -1)
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		cleanPath := cleanFrontendResourcePath(string(match[1]))
		if cleanPath == "" {
			continue
		}
		info, err := os.Stat(filepath.Join(staticDir, cleanPath))
		if err != nil || info.IsDir() {
			return "/" + filepath.ToSlash(cleanPath)
		}
	}
	return ""
}

func cleanFrontendResourcePath(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" || strings.HasPrefix(value, "#") {
		return ""
	}
	lower := strings.ToLower(value)
	for _, prefix := range []string{"http://", "https://", "data:", "blob:", "mailto:", "tel:"} {
		if strings.HasPrefix(lower, prefix) {
			return ""
		}
	}
	if idx := strings.IndexAny(value, "?#"); idx >= 0 {
		value = value[:idx]
	}
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "./")
	value = strings.TrimPrefix(value, "/")
	// vite 以部署前缀为 base 时，index.html 引用形如 /<prefix>/assets/... /<prefix>/favicon.svg，
	// 需剥掉前缀再判本地文件是否存在，与 pathForStaticAsset 同构。空前缀（根部署）无需剥离。
	if prefix := deploy.NormalizedPrefix(); prefix != "" {
		value = strings.TrimPrefix(value, strings.TrimPrefix(prefix, "/")+"/")
	}
	value = filepath.Clean(value)
	if value == "." || strings.HasPrefix(value, ".."+string(filepath.Separator)) || value == ".." || filepath.IsAbs(value) {
		return ""
	}
	return value
}

func frontendAssetMissingNotice(requestPath string) string {
	cleanPath := pathForStaticAsset(requestPath)
	if cleanPath == "" {
		cleanPath = "index.html"
	}
	displayPath := filepath.ToSlash(cleanPath)
	return fmt.Sprintf("frontend assets missing: web/%s not found. Please reinstall or check the install directory.", displayPath)
}

func renderFallbackFrontend(content, notice string) string {
	out := content
	noticeHTML := ""
	if strings.TrimSpace(notice) != "" {
		noticeHTML = `<div class="notice is-visible">` + htmpl.HTMLEscapeString(notice) + `</div>`
	}
	out = strings.ReplaceAll(out, "__FALLBACK_NOTICE__", noticeHTML)
	// 兜底页文档 URL 由反代前缀决定（与运行时部署前缀一致）。
	out = strings.ReplaceAll(out, "/api/tree?", deploy.PrefixedPath("/api/tree")+"?")
	return out
}

func applyStaticCacheHeaders(w http.ResponseWriter, cleanPath string) {
	switch cleanPath {
	case "service-worker.js", "index.html":
		w.Header().Set("Cache-Control", "no-cache")
		return
	}

	if strings.HasPrefix(cleanPath, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
}

func isStandardReleaseVersion(version string) bool {
	version = strings.TrimSpace(version)
	if version == "" {
		return false
	}
	version, _ = strings.CutPrefix(version, "v")
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, ch := range part {
			if ch < '0' || ch > '9' {
				return false
			}
		}
	}
	return true
}

func pathForStaticAsset(requestPath string) string {
	// requestPath 是 URL path，分隔符固定为正斜杠。这里不能用 filepath，
	// 否则 Windows 会把前导 // 当成 UNC 路径，最终泄漏成 web/// 这类路径。
	// 部署前缀已在顶层 mux 统一剥离（StripDeployPrefix），此处收到的已是
	// /assets/...、/index.html 等去前缀路径。
	cleaned := stdpath.Clean("/" + requestPath)
	if cleaned == "/" {
		return ""
	}
	return strings.TrimPrefix(cleaned, "/")
}
