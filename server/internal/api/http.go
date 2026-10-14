package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
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
	"strconv"
	"strings"

	"mindfs/internal/deploy"
	"mindfs/server/internal/agent"
	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/api/usecase"
	"mindfs/server/internal/commandexec"
	"mindfs/server/internal/githubimport"
	"mindfs/server/internal/nodeinfo"
	"mindfs/server/internal/preferences"
	"mindfs/server/internal/session"

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

func (h *HTTPHandler) handleSessions(w http.ResponseWriter, r *http.Request) {
	rootID := r.URL.Query().Get("root")
	if truthyQuery(r, "multi_root") {
		h.handleMultiRootSessions(w, r)
		return
	}
	beforeTime, err := parseOptionalTimeQuery(r, "before_time")
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	afterTime, err := parseOptionalTimeQuery(r, "after_time")
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	if !beforeTime.IsZero() && !afterTime.IsZero() {
		respondError(w, http.StatusBadRequest, errInvalidRequest("before_time and after_time are mutually exclusive"))
		return
	}
	limit, err := parsePositiveIntQuery(r, "limit")
	if err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("limit must be a positive integer"))
		return
	}
	if limit <= 0 {
		limit = sessionListPageSize
	}
	uc := h.service()
	out, err := uc.ListSessions(r.Context(), usecase.ListSessionsInput{
		RootID:          rootID,
		BeforeTime:      beforeTime,
		AfterTime:       afterTime,
		Limit:           limit,
		TopLevelOnly:    truthyQuery(r, "top_level"),
		IncludeChildren: truthyQuery(r, "include_children"),
		// 默认排除已归档（主面板）；archived=only 时只返回归档区的那批。
		ArchivedOnly: strings.TrimSpace(r.URL.Query().Get("archived")) == "only",
	})
	if err != nil {
		respondError(w, http.StatusServiceUnavailable, err)
		return
	}
	payload := make([]map[string]any, 0, len(out.Sessions))
	for _, s := range out.Sessions {
		payload = append(payload, h.sessionListResponse(s))
	}
	// 置顶的权威只有主节点的 pins 表：会话库里的 pinned_at 已退役（存量实测为 0 行，
	// 不做回填）。刻意**不**把两者并起来 —— 两份真相并集等于两份真相迟早分叉，
	// 而 nodes.json 当年就是这么漂的。
	pinnedItems, pinnedKeys := h.pinnedSessionsForRoot(r.Context(), rootID, true)
	pinnedPayload := make([]map[string]any, 0, len(pinnedItems))
	for _, s := range pinnedItems {
		pinnedPayload = append(pinnedPayload, h.sessionListResponse(s))
	}
	respondJSONList(w, r, map[string]any{
		"items":        payload,
		"pinned_items": pinnedPayload,
		"pinned_keys":  pinnedKeys,
		"total_count":  out.TotalCount,
	})
}

func (h *HTTPHandler) handleMultiRootSessions(w http.ResponseWriter, r *http.Request) {
	limit, err := parsePositiveIntQuery(r, "limit_per_root")
	if err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("limit_per_root must be a positive integer"))
		return
	}
	if limit <= 0 {
		limit = multiRootSessionLimit
	}
	out, err := h.service().ListMultiRootSessions(r.Context(), usecase.ListMultiRootSessionsInput{
		LimitPerRoot: limit,
	})
	if err != nil {
		respondError(w, http.StatusServiceUnavailable, err)
		return
	}
	groups := make([]map[string]any, 0, len(out.Groups))
	for _, group := range out.Groups {
		items := make([]map[string]any, 0, len(group.Sessions))
		for _, s := range group.Sessions {
			item := h.sessionListResponse(s)
			item["root_id"] = group.RootID
			items = append(items, item)
		}
		// 同 handleSessionList：置顶只认 pins 表，会话库的 pinned_at 不再参与。
		pinnedSessions, pinnedKeys := h.pinnedSessionsForRoot(r.Context(), group.RootID, true)
		pinnedItems := make([]map[string]any, 0, len(pinnedSessions))
		for _, s := range pinnedSessions {
			item := h.sessionListResponse(s)
			item["root_id"] = group.RootID
			pinnedItems = append(pinnedItems, item)
		}
		groups = append(groups, map[string]any{
			"root_id":             group.RootID,
			"root_name":           group.RootName,
			"latest_session_time": group.LatestSessionTime,
			"items":               items,
			"pinned_items":        pinnedItems,
			"pinned_keys":         pinnedKeys,
			"total_count":         group.TotalCount,
		})
	}
	respondJSONList(w, r, map[string]any{"groups": groups})
}

func (h *HTTPHandler) handleSessionChildren(w http.ResponseWriter, r *http.Request) {
	rootID := r.URL.Query().Get("root")
	parentSessionKey := strings.TrimSpace(r.URL.Query().Get("parent_session_key"))
	if parentSessionKey == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("parent_session_key required"))
		return
	}
	beforeTime, err := parseOptionalTimeQuery(r, "before_time")
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	limit, err := parsePositiveIntQuery(r, "limit")
	if err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("limit must be a positive integer"))
		return
	}
	if limit <= 0 {
		limit = childSessionPageSize
	} else if limit > childSessionMaxLimit {
		limit = childSessionMaxLimit
	}
	out, err := h.service().ListChildSessions(r.Context(), usecase.ListChildSessionsInput{
		RootID:           rootID,
		ParentSessionKey: parentSessionKey,
		BeforeTime:       beforeTime,
		Limit:            limit,
	})
	if err != nil {
		respondError(w, http.StatusServiceUnavailable, err)
		return
	}
	payload := make([]map[string]any, 0, len(out.Sessions))
	for _, s := range out.Sessions {
		payload = append(payload, h.sessionListResponse(s))
	}
	respondJSON(w, http.StatusOK, payload)
}

func (h *HTTPHandler) handleReplyingSessions(w http.ResponseWriter, r *http.Request) {
	if h.AppContext == nil || h.AppContext.GetSessionStreamHub() == nil {
		respondJSONConditional(w, r, map[string]any{"sessions": []map[string]any{}})
		return
	}
	items := h.AppContext.GetSessionStreamHub().ListReplyingSessions()
	payload := make([]map[string]any, 0, len(items))
	for _, item := range items {
		rootTitle := item.RootID
		sessionTitle := strings.TrimSpace(item.SessionTitle)
		if root, err := h.AppContext.GetRoot(item.RootID); err == nil {
			if strings.TrimSpace(root.Name) != "" {
				rootTitle = root.Name
			}
			if sessionTitle == "" {
				if manager, err := h.AppContext.GetSessionManager(item.RootID); err == nil {
					// 只需 name，走 SQLite meta-only，避免全量加载 JSONL（agent 活跃时该端点高频轮询）。
					if sess, err := manager.GetMeta(r.Context(), item.SessionKey); err == nil && sess != nil {
						sessionTitle = sess.Name
					}
				}
			}
		}
		payload = append(payload, map[string]any{
			"rootId":       item.RootID,
			"rootTitle":    rootTitle,
			"sessionKey":   item.SessionKey,
			"sessionTitle": sessionTitle,
			"status":       item.Status,
			"summary":      item.Summary,
			"updatedAt":    item.UpdatedAt,
		})
	}
	respondJSONConditional(w, r, map[string]any{"sessions": payload})
}

func (h *HTTPHandler) handleSessionSearch(w http.ResponseWriter, r *http.Request) {
	rootID := strings.TrimSpace(r.URL.Query().Get("root"))
	query := r.URL.Query().Get("q")
	limit, err := parsePositiveIntQuery(r, "limit")
	if err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("limit must be a positive integer"))
		return
	}
	out, err := h.service().SearchSessions(r.Context(), usecase.SearchSessionsInput{
		RootID:    rootID,
		Query:     query,
		Limit:     limit,
		MultiRoot: truthyQuery(r, "multi_root"),
	})
	if err != nil {
		respondError(w, http.StatusServiceUnavailable, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"items": out.Items,
	})
}

func (h *HTTPHandler) handleCandidates(w http.ResponseWriter, r *http.Request) {
	rootID := strings.TrimSpace(r.URL.Query().Get("root"))
	candidateType := usecase.CandidateType(strings.TrimSpace(r.URL.Query().Get("type")))
	agent := strings.TrimSpace(r.URL.Query().Get("agent"))
	uc := h.service()
	out, err := uc.SearchCandidates(r.Context(), usecase.SearchCandidatesInput{
		RootID: rootID,
		Type:   candidateType,
		Query:  r.URL.Query().Get("q"),
		Agent:  agent,
	})
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "root not found") {
			status = http.StatusNotFound
		}
		respondError(w, status, err)
		return
	}
	respondJSON(w, http.StatusOK, out.Items)
}

func (h *HTTPHandler) handlePromptSave(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&input); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid prompt payload"))
		return
	}
	out, err := h.service().SavePrompt(r.Context(), usecase.SavePromptInput{
		Text: input.Text,
	})
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, out)
}

func (h *HTTPHandler) handlePromptDelete(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&input); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid prompt payload"))
		return
	}
	out, err := h.service().DeletePrompt(r.Context(), usecase.DeletePromptInput{
		Text: input.Text,
	})
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, out)
}

func (h *HTTPHandler) handleExternalSessionsList(w http.ResponseWriter, r *http.Request) {
	rootID := strings.TrimSpace(r.URL.Query().Get("root"))
	agentName := strings.TrimSpace(r.URL.Query().Get("agent"))
	if rootID == "" || agentName == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("root and agent are required"))
		return
	}
	beforeTime, err := parseOptionalTimeQuery(r, "before_time")
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	afterTime, err := parseOptionalTimeQuery(r, "after_time")
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	if !beforeTime.IsZero() && !afterTime.IsZero() {
		respondError(w, http.StatusBadRequest, errInvalidRequest("before_time and after_time are mutually exclusive"))
		return
	}
	limit, err := parsePositiveIntQuery(r, "limit")
	if err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("limit must be a positive integer"))
		return
	}
	filterBound := false
	switch strings.TrimSpace(r.URL.Query().Get("filter_bound")) {
	case "1", "true", "TRUE", "True":
		filterBound = true
	}
	uc := h.service()
	out, err := uc.ListExternalSessions(r.Context(), usecase.ListExternalSessionsInput{
		RootID:      rootID,
		Agent:       agentName,
		BeforeTime:  beforeTime,
		AfterTime:   afterTime,
		Limit:       limit,
		FilterBound: filterBound,
	})
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	payload := make([]map[string]any, 0, len(out.Items))
	for _, item := range out.Items {
		payload = append(payload, externalSessionListResponse(item))
	}
	respondJSON(w, http.StatusOK, payload)
}

func (h *HTTPHandler) handleExternalSessionImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RootID         string `json:"root_id"`
		Agent          string `json:"agent"`
		AgentSessionID string `json:"agent_session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	req.RootID = strings.TrimSpace(req.RootID)
	req.Agent = strings.TrimSpace(req.Agent)
	req.AgentSessionID = strings.TrimSpace(req.AgentSessionID)
	if req.RootID == "" || req.Agent == "" || req.AgentSessionID == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("root_id, agent, agent_session_id are required"))
		return
	}
	uc := h.service()
	out, err := uc.ImportExternalSession(r.Context(), usecase.ImportExternalSessionInput{
		RootID:         req.RootID,
		Agent:          req.Agent,
		AgentSessionID: req.AgentSessionID,
	})
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	if h.AppContext != nil {
		h.AppContext.GetSessionStreamHub().BroadcastAll(WSResponse{
			Type: "session.imported",
			Payload: map[string]any{
				"root_id":          req.RootID,
				"session_key":      out.SessionKey,
				"agent":            out.Agent,
				"agent_session_id": out.AgentSessionID,
				"imported_count":   out.ImportedCount,
			},
		})
	}
	respondJSON(w, http.StatusOK, out)
}

func (h *HTTPHandler) handleExternalSessionImportBatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RootID          string   `json:"root_id"`
		Agent           string   `json:"agent"`
		AgentSessionIDs []string `json:"agent_session_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	req.RootID = strings.TrimSpace(req.RootID)
	req.Agent = strings.TrimSpace(req.Agent)
	if req.RootID == "" || req.Agent == "" || len(req.AgentSessionIDs) == 0 {
		respondError(w, http.StatusBadRequest, errInvalidRequest("root_id, agent, agent_session_ids are required"))
		return
	}
	uc := h.service()
	out, err := uc.ImportExternalSessionsBatch(r.Context(), usecase.ImportExternalSessionsBatchInput{
		RootID:          req.RootID,
		Agent:           req.Agent,
		AgentSessionIDs: req.AgentSessionIDs,
	})
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	if h.AppContext != nil {
		for _, item := range out.Items {
			if !item.Success || strings.TrimSpace(item.SessionKey) == "" {
				continue
			}
			h.AppContext.GetSessionStreamHub().BroadcastAll(WSResponse{
				Type: "session.imported",
				Payload: map[string]any{
					"root_id":          req.RootID,
					"session_key":      item.SessionKey,
					"agent":            req.Agent,
					"agent_session_id": item.AgentSessionID,
					"imported_count":   item.ImportedCount,
				},
			})
		}
	}
	respondJSON(w, http.StatusOK, out)
}

func (h *HTTPHandler) handleSessionGet(w http.ResponseWriter, r *http.Request) {
	rootID := r.URL.Query().Get("root")
	key := chi.URLParam(r, "key")
	if strings.TrimSpace(key) == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("session key required"))
		return
	}
	afterSeq, err := parsePositiveIntQuery(r, "seq")
	if err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("seq must be a positive integer"))
		return
	}
	beforeSeq, err := parsePositiveIntQuery(r, "before_seq")
	if err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("before_seq must be a positive integer"))
		return
	}
	latest, err := parsePositiveIntQuery(r, "latest")
	if err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("latest must be a positive integer"))
		return
	}
	// seq 与 before_seq/latest 互斥：增量语义与窗口语义不可同时请求
	if afterSeq > 0 && (beforeSeq > 0 || latest > 0) {
		respondError(w, http.StatusBadRequest, errInvalidRequest("seq 与 before_seq/latest 互斥"))
		return
	}
	limit, err := parsePositiveIntQuery(r, "limit")
	if err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("limit must be a positive integer"))
		return
	}
	if limit <= 0 {
		limit = 50
	} else if limit > 200 {
		limit = 200
	}
	uc := h.service()
	if _, err := uc.SyncExternalSessionDelta(r.Context(), usecase.SyncExternalSessionDeltaInput{
		RootID: rootID,
		Key:    key,
	}); err != nil {
		log.Printf("[session/sync] external delta best-effort failed root=%s session=%s err=%v", strings.TrimSpace(rootID), strings.TrimSpace(key), err)
	}
	out, windowMeta, err := uc.GetSession(r.Context(), usecase.GetSessionInput{
		RootID:    rootID,
		Key:       key,
		Seq:       afterSeq,
		BeforeSeq: beforeSeq,
		Limit:     limit,
		Latest:    latest,
	})
	if err != nil {
		respondError(w, http.StatusNotFound, err)
		return
	}
	contextWindow, _ := uc.GetSessionContextWindow(r.Context(), usecase.GetSessionContextWindowInput{
		RootID: rootID,
		Key:    key,
	})
	// 窗口请求只需窗口内 seq 的 aux：原先传 Seq=0 会让 manager 全量读整个 aux 文件
	// （实测某会话 11.8MB/约 300ms 且持 m.mu），再在下面按 windowSeqs 过滤掉绝大部分。
	var exchangeAux map[int][]session.ExchangeAux
	if windowMeta != nil {
		windowSeqs := make(map[int]bool, len(out.Exchanges))
		for _, ex := range out.Exchanges {
			if ex.Seq > 0 {
				windowSeqs[ex.Seq] = true
			}
		}
		exchangeAux, _ = uc.GetSessionExchangeAuxWindow(r.Context(), usecase.GetSessionExchangeAuxWindowInput{
			RootID: rootID,
			Key:    key,
			Seqs:   windowSeqs,
		})
	} else {
		exchangeAux, _ = uc.GetSessionExchangeAux(r.Context(), usecase.GetSessionExchangeAuxInput{
			RootID: rootID,
			Key:    key,
			Seq:    afterSeq,
		})
	}
	// 正文块：seq 集合与上面 aux 的窗口口径一致 —— 用**回包里的** exchange seq，
	// 而不是请求参数推算的窗口边界，这样窗口/增量/全量三条路共用同一段逻辑，
	// 也不会出现「块挂到了不在回包里的 seq 上」。
	blocks, _ := uc.GetSessionBlocksWindow(r.Context(), usecase.GetSessionBlocksWindowInput{
		RootID: rootID,
		Key:    key,
		Seqs:   windowSeqsForExchanges(out.Exchanges),
		// 回填输入（S2d-1）：这两份数据响应里本来就要带着，回填不该为此再读一遍盘。
		Exchanges: out.Exchanges,
		Aux:       exchangeAux,
	})
	// 条件请求：这是全站**最重**的端点 —— 窗口化尾部拉取（`?latest=20`）
	// 实测单次 1.14 MB，客户端按 ~1.7 秒一次轮询，两台机器各自量到 60 分钟里
	// 4,746 次、合计约 1.1 GB/h（占全部请求的 70%）。会话没在跑时这些回包
	// **逐字节相同**（连续四次 sha1 一致），所以 304 能把它们压到零字节。
	//
	// 只协商、不瘦身：载荷本身就是详情（exchanges / exchange_aux / window_meta
	// 少一个键就是少一段对话），而且 `seq` 增量语义要求客户端**恰好**收到新行，
	// 删空字段会破坏 `appendSessionDelta` 的判据。
	respondJSONConditional(w, r, h.sessionResponse(out, contextWindow, exchangeAux, blocks, windowMeta))
}

func (h *HTTPHandler) handleSessionSync(w http.ResponseWriter, r *http.Request) {
	rootID := r.URL.Query().Get("root")
	key := chi.URLParam(r, "key")
	if strings.TrimSpace(key) == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("session key required"))
		return
	}
	afterSeq, err := parsePositiveIntQuery(r, "seq")
	if err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("seq must be a positive integer"))
		return
	}
	// 窗口参数与 handleSessionGet 同义，透传至 usecase；与 seq 互斥
	beforeSeq, err := parsePositiveIntQuery(r, "before_seq")
	if err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("before_seq must be a positive integer"))
		return
	}
	latest, err := parsePositiveIntQuery(r, "latest")
	if err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("latest must be a positive integer"))
		return
	}
	if afterSeq > 0 && (beforeSeq > 0 || latest > 0) {
		respondError(w, http.StatusBadRequest, errInvalidRequest("seq 与 before_seq/latest 互斥"))
		return
	}
	limit, err := parsePositiveIntQuery(r, "limit")
	if err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("limit must be a positive integer"))
		return
	}
	if limit <= 0 {
		limit = 50
	} else if limit > 200 {
		limit = 200
	}
	uc := h.service()
	if _, err := uc.SyncExternalSessionDelta(r.Context(), usecase.SyncExternalSessionDeltaInput{
		RootID: rootID,
		Key:    key,
		Full:   true,
	}); err != nil {
		// 外部转录同步只是「尽力而为」的补充，请求的目的是刷新会话视图，不该被它拖垮 ——
		// GET 路径（handleSessionGet）一直就是这个语义，这里却把错误直接变成 400。
		// 实测 2026-09-22 会话 1790064991（root=mindfs）：agent 跑在 <root>/.worktree/task-10，
		// 任务结束后工作树被移除，而转录仍在 ~/.claude/projects/-home-...-mindfs--worktree-task-10/
		// 下且还在写；worktreeCandidateRoots 只枚举**现存**的 .worktree/* 目录，于是永远找不到
		// → external session not found → 「同步」按钮永久 400。刷新视图不该因此失败。
		// 注意这里**不**去修「按残留 slug 目录找转录」：live-owned 会话的字节游标是冻结的
		// （external_source_* 全空），一旦找到就会从 offset 0 整份重读，正是 2026-09-16 那次
		// 「ask 下面又渲染了一轮出现过的文字」的成因。找不到反而是安全的。
		// 补充（repoint 落地后）：对**已 repoint** 的会话，本条路径的触发条件已经消失——
		// related_worktree 被清空、转录落在主 slug 目录下，worktreeCandidateRoots 的第一项
		// 就是主 checkout，所以一定找得到；cursor 也在 repoint 时被推进到了新转录末尾，
		// 找到之后也不会从 0 重读。没 repoint 的会话仍旧走「找不到」这条安全路径。
		log.Printf("[session/sync] external sync failed root=%s session=%s err=%v", strings.TrimSpace(rootID), strings.TrimSpace(key), err)
	}
	out, windowMeta, err := uc.GetSession(r.Context(), usecase.GetSessionInput{
		RootID:    rootID,
		Key:       key,
		Seq:       afterSeq,
		BeforeSeq: beforeSeq,
		Limit:     limit,
		Latest:    latest,
	})
	if err != nil {
		respondError(w, http.StatusNotFound, err)
		return
	}
	contextWindow, _ := uc.GetSessionContextWindow(r.Context(), usecase.GetSessionContextWindowInput{
		RootID: rootID,
		Key:    key,
	})
	// 窗口请求只需窗口内 seq 的 aux：原先无条件传 Seq=0，会让 manager 全量读整个 aux
	// 文件（实测某会话 11.8MB/约 300ms 且持 m.mu），再按 windowSeqs 过滤掉绝大部分。
	// 与 handleSessionGet 保持同一套选择逻辑。
	var exchangeAux map[int][]session.ExchangeAux
	if windowMeta != nil {
		windowSeqs := make(map[int]bool, len(out.Exchanges))
		for _, ex := range out.Exchanges {
			if ex.Seq > 0 {
				windowSeqs[ex.Seq] = true
			}
		}
		exchangeAux, _ = uc.GetSessionExchangeAuxWindow(r.Context(), usecase.GetSessionExchangeAuxWindowInput{
			RootID: rootID,
			Key:    key,
			Seqs:   windowSeqs,
		})
	} else {
		exchangeAux, _ = uc.GetSessionExchangeAux(r.Context(), usecase.GetSessionExchangeAuxInput{
			RootID: rootID,
			Key:    key,
			Seq:    afterSeq,
		})
	}
	// 正文块与 aux 同一口径（用回包里的 exchange seq），与 handleSessionGet 共用
	// windowSeqsForExchanges。
	blocks, _ := uc.GetSessionBlocksWindow(r.Context(), usecase.GetSessionBlocksWindowInput{
		RootID: rootID,
		Key:    key,
		Seqs:   windowSeqsForExchanges(out.Exchanges),
		// 回填输入（S2d-1）：这两份数据响应里本来就要带着，回填不该为此再读一遍盘。
		Exchanges: out.Exchanges,
		Aux:       exchangeAux,
	})
	// 注：sync 先做全量外部增量拉取（Full:true），再按窗口切片，保证窗口数据最新。
	respondJSON(w, http.StatusOK, h.sessionResponse(out, contextWindow, exchangeAux, blocks, windowMeta))
}

func (h *HTTPHandler) handleSessionToolCallGet(w http.ResponseWriter, r *http.Request) {
	rootID := r.URL.Query().Get("root")
	key := chi.URLParam(r, "key")
	callID := chi.URLParam(r, "callID")
	if strings.TrimSpace(key) == "" || strings.TrimSpace(callID) == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("session key and tool call id required"))
		return
	}
	toolCall, err := h.service().GetSessionToolCall(r.Context(), usecase.GetSessionToolCallInput{
		RootID: rootID,
		Key:    key,
		CallID: callID,
	})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			respondError(w, http.StatusNotFound, err)
			return
		}
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"toolcall": toolCall})
}

func (h *HTTPHandler) handleSessionRelatedFilesGet(w http.ResponseWriter, r *http.Request) {
	rootID := r.URL.Query().Get("root")
	key := chi.URLParam(r, "key")
	if strings.TrimSpace(key) == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("session key required"))
		return
	}
	uc := h.service()
	out, err := uc.GetSessionRelatedFiles(r.Context(), usecase.GetSessionRelatedFilesInput{
		RootID: rootID,
		Key:    key,
	})
	if err != nil {
		respondError(w, http.StatusNotFound, err)
		return
	}
	respondJSON(w, http.StatusOK, out)
}

func (h *HTTPHandler) handleSessionRelatedFilesDelete(w http.ResponseWriter, r *http.Request) {
	rootID := r.URL.Query().Get("root")
	key := chi.URLParam(r, "key")
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	head := strings.TrimSpace(r.URL.Query().Get("head"))
	repoPath := strings.TrimSpace(r.URL.Query().Get("repo_path"))
	repoKind := strings.TrimSpace(r.URL.Query().Get("repo_kind"))
	if strings.TrimSpace(key) == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("session key required"))
		return
	}
	if path == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("path required"))
		return
	}
	uc := h.service()
	if err := uc.RemoveSessionRelatedFile(r.Context(), usecase.RemoveSessionRelatedFileInput{
		RootID:   rootID,
		Key:      key,
		Path:     path,
		Head:     head,
		RepoPath: repoPath,
		RepoKind: repoKind,
	}); err != nil {
		respondError(w, http.StatusNotFound, err)
		return
	}
	if h.AppContext != nil {
		h.AppContext.GetSessionStreamHub().BroadcastAll(WSResponse{
			Type: "session.related_files.updated",
			Payload: map[string]any{
				"root_id":     rootID,
				"session_key": key,
			},
		})
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTPHandler) handleSessionFork(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RootID     string `json:"root_id"`
		SessionKey string `json:"session_key"`
		Seq        int    `json:"seq"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	if strings.TrimSpace(req.RootID) == "" || strings.TrimSpace(req.SessionKey) == "" || req.Seq <= 0 {
		respondError(w, http.StatusBadRequest, errInvalidRequest("root_id, session_key and seq are required"))
		return
	}
	out, err := h.service().ForkSession(r.Context(), usecase.ForkSessionInput{
		RootID: req.RootID,
		Key:    req.SessionKey,
		Seq:    req.Seq,
	})
	if err != nil {
		log.Printf("[session/fork] failed root=%s parent=%s seq=%d err=%v", req.RootID, req.SessionKey, req.Seq, err)
		respondError(w, http.StatusBadRequest, err)
		return
	}
	if h.AppContext != nil && out.Session != nil {
		h.AppContext.GetSessionStreamHub().BroadcastAll(WSResponse{
			Type: "session.created",
			Payload: map[string]any{
				"root_id": req.RootID,
				"session": h.sessionListResponse(out.Session),
			},
		})
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"session_key": out.Session.Key,
		"session":     h.sessionResponse(out.Session, agenttypes.ContextWindow{}, nil, nil, nil),
	})
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

func (h *HTTPHandler) handleSessionRename(w http.ResponseWriter, r *http.Request) {
	rootID := r.URL.Query().Get("root")
	key := chi.URLParam(r, "key")
	if strings.TrimSpace(key) == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("session key required"))
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	uc := h.service()
	renamed, err := uc.RenameSession(r.Context(), usecase.RenameSessionInput{
		RootID: rootID,
		Key:    key,
		Name:   req.Name,
	})
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	if h.AppContext != nil {
		h.AppContext.GetSessionStreamHub().BroadcastAll(WSResponse{
			Type: "session.meta.updated",
			Payload: map[string]any{
				"root_id": rootID,
				"session": map[string]any{
					"key":          renamed.Key,
					"name":         renamed.Name,
					"model":        renamed.Model,
					"mode":         session.InferModeFromSession(renamed),
					"effort":       session.InferEffortFromSession(renamed),
					"fast_service": session.InferFastServiceFromSession(renamed),
					"plan_mode":    renamed.PlanMode,
					"pinned_at":    renamed.PinnedAt,
					"updated_at":   renamed.UpdatedAt,
				},
			},
		})
	}
	// 任务名与会话名双向绑定：任务主会话改名时同步任务名。
	if strings.TrimSpace(req.Name) != "" {
		h.syncTaskNameFromSession(r.Context(), rootID, renamed.Key, strings.TrimSpace(req.Name))
	}
	respondJSON(w, http.StatusOK, h.sessionListResponse(renamed))
}

// handleSessionPin 是 POST /api/sessions/{key}/pin —— **旧路径的别名**，转调 /api/pins/session。
//
// 保留它是为了前端迁移期间不用同时改两端：同一个动作，两条 URL 写同一个置顶表。
// 前端改用 /api/pins/session 后即可删掉本函数与那条路由。
//
// 这里**不再**写会话库的 pinned_at（已退役）：那次写会让置顶回到按机器分的
// 存储里，而权威在主节点 —— 于是「在 A 机器置顶、B 机器看不到」的老问题又回来了。
func (h *HTTPHandler) handleSessionPin(w http.ResponseWriter, r *http.Request) {
	var req sessionPinRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	req.RootID = r.URL.Query().Get("root")
	req.Key = chi.URLParam(r, "key")
	h.pinsSessionPut(w, r, req)
}

func (h *HTTPHandler) handleSessionDelete(w http.ResponseWriter, r *http.Request) {
	rootID := r.URL.Query().Get("root")
	key := chi.URLParam(r, "key")
	if strings.TrimSpace(key) == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("session key required"))
		return
	}
	uc := h.service()
	deletedKeys, err := uc.DeleteSession(r.Context(), usecase.DeleteSessionInput{
		RootID: rootID,
		Key:    key,
	})
	if err != nil {
		respondError(w, http.StatusNotFound, err)
		return
	}
	// 会话没了，任务里指向它的链接必须一并清掉，否则任务点进去是空白。
	// 要传整棵子树的 key：子会话也可能绑着别的任务。
	h.detachTaskFromSession(r.Context(), rootID, deletedKeys)
	w.WriteHeader(http.StatusNoContent)
}

// handleSessionArchive 归档/取消归档。
//
// 归档会把子会话真删掉（见 usecase.ArchiveSession），所以这里**要**碰任务：
// 和删除一样，被删掉的子会话可能绑着别的任务，不解绑任务点进去就是空白。
// 被点的那个会话自己不碰任务 —— 它只是归档，还能打开，链接必须继续有效。
func (h *HTTPHandler) handleSessionArchive(w http.ResponseWriter, r *http.Request) {
	rootID := r.URL.Query().Get("root")
	key := chi.URLParam(r, "key")
	if strings.TrimSpace(key) == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("session key required"))
		return
	}
	var req struct {
		Archived bool `json:"archived"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	out, err := h.service().ArchiveSession(r.Context(), usecase.ArchiveSessionInput{
		RootID:   rootID,
		Key:      key,
		Archived: req.Archived,
	})
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	// 被删的是子会话，任务里指向它们的链接一并清掉。
	if len(out.DeletedKeys) > 0 {
		h.detachTaskFromSession(r.Context(), rootID, out.DeletedKeys)
	}
	if h.AppContext != nil {
		h.AppContext.GetSessionStreamHub().BroadcastAll(WSResponse{
			Type: "session.meta.updated",
			Payload: map[string]any{
				"root_id": rootID,
				"session": h.sessionListResponse(out.Session),
			},
		})
	}
	respondJSON(w, http.StatusOK, h.sessionListResponse(out.Session))
}

// windowSeqsForExchanges 是「取块 / 取 aux 时用哪些 seq」的唯一定义：
// **回包里真实存在的** exchange seq（seq>0）。不用请求参数推算窗口边界，
// 于是窗口 / 增量 / 全量三条路共用一段逻辑，也不会出现「块挂到了不在回包里的 seq 上」。
func windowSeqsForExchanges(exchanges []session.Exchange) map[int]bool {
	seqs := make(map[int]bool, len(exchanges))
	for _, ex := range exchanges {
		if ex.Seq > 0 {
			seqs[ex.Seq] = true
		}
	}
	return seqs
}

func (h *HTTPHandler) sessionResponse(
	s *session.Session,
	contextWindow agenttypes.ContextWindow,
	exchangeAux map[int][]session.ExchangeAux,
	blocks map[int][]session.Block,
	windowMeta *session.SessionWindowMeta,
) map[string]any {
	if s == nil {
		return map[string]any{}
	}
	exchanges := append([]session.Exchange{}, s.Exchanges...)
	auxPayload := make(map[string][]session.ExchangeAux, len(exchangeAux))
	if windowMeta != nil {
		// 窗口模式：仅保留窗口内 exchange 的 aux，避免 aux.line 错位
		windowSeqs := make(map[int]struct{}, len(exchanges))
		for _, ex := range exchanges {
			if ex.Seq > 0 {
				windowSeqs[ex.Seq] = struct{}{}
			}
		}
		for seq, items := range exchangeAux {
			if seq <= 0 || len(items) == 0 {
				continue
			}
			if _, ok := windowSeqs[seq]; !ok {
				continue
			}
			auxPayload[strconv.Itoa(seq)] = append([]session.ExchangeAux(nil), items...)
		}
	} else {
		for seq, items := range exchangeAux {
			if seq <= 0 || len(items) == 0 {
				continue
			}
			auxPayload[strconv.Itoa(seq)] = append([]session.ExchangeAux(nil), items...)
		}
	}
	// 正文块（CUSTOM(G-BH)）：与 exchange_aux 同形，按 seq 分组。
	// 只下发窗口内的 seq，且**只压工具卡详情、绝不压正文** —— 块流是正文的唯一载体。
	// 历史会话没有块流时这里是空 map，前端回退旧格式。
	blockPayload := make(map[string][]session.Block, len(blocks))
	windowSeqs := make(map[int]struct{}, len(exchanges))
	for _, ex := range exchanges {
		if ex.Seq > 0 {
			windowSeqs[ex.Seq] = struct{}{}
		}
	}
	for seq, items := range blocks {
		if seq <= 0 || len(items) == 0 {
			continue
		}
		if _, ok := windowSeqs[seq]; !ok {
			continue
		}
		blockPayload[strconv.Itoa(seq)] = session.CompactBlocksLight(items)
	}
	resp := map[string]any{
		"key":                 s.Key,
		"type":                s.Type,
		"parent_session_key":  s.ParentSessionKey,
		"parent_tool_call_id": s.ParentToolCallID,
		"source":              s.Source,
		"task_id":             s.TaskID,
		"agent":               session.InferAgentFromSession(s),
		"model":               s.Model,
		"mode":                session.InferModeFromSession(s),
		"effort":              session.InferEffortFromSession(s),
		"fast_service":        session.InferFastServiceFromSession(s),
		"plan_mode":           s.PlanMode,
		"shell":               h.commandShellForResponse(s, exchangeAux),
		"name":                s.Name,
		"exchanges":           exchanges,
		"exchange_aux":        auxPayload,
		"blocks":              blockPayload,
		"related_files":       s.RelatedFiles,
		"related_worktree":    s.RelatedWorktree,
		"worktree_missing":    s.RelatedWorktree.WorktreeMissing(),
		"context_window":      contextWindow,
		"pinned_at":           s.PinnedAt,
		"archived_at":         s.ArchivedAt,
		"created_at":          s.CreatedAt,
		"updated_at":          s.UpdatedAt,
		"closed_at":           s.ClosedAt,
	}
	if windowMeta != nil {
		resp["window_meta"] = windowMeta
	}
	return resp
}

// sessionListResponse 是会话的「列表行」形状（/api/sessions 每条、以及 session.created
// 广播的 payload）。抽成包级函数是为了让 AppContext 的广播路径能用上同一份构造 ——
// 它只有 AppContext、没有 HTTPHandler。resolvedShell 是 command 类会话的 shell 解析结果
// （HTTPHandler 那侧来自 commandShellForSession）；非 command 会话忽略它。
func sessionListResponse(s *session.Session, resolvedShell string) map[string]any {
	if s == nil {
		return map[string]any{}
	}
	return map[string]any{
		"key":                 s.Key,
		"type":                s.Type,
		"parent_session_key":  s.ParentSessionKey,
		"parent_tool_call_id": s.ParentToolCallID,
		"source":              s.Source,
		"task_id":             s.TaskID,
		"agent":               session.InferAgentFromSession(s),
		"model":               s.Model,
		"mode":                session.InferModeFromSession(s),
		"effort":              session.InferEffortFromSession(s),
		"fast_service":        session.InferFastServiceFromSession(s),
		"plan_mode":           s.PlanMode,
		"shell":               commandShellField(s, resolvedShell),
		"name":                s.Name,
		"related_worktree":    s.RelatedWorktree,
		"worktree_missing":    s.RelatedWorktree.WorktreeMissing(),
		"pinned_at":           s.PinnedAt,
		"archived_at":         s.ArchivedAt,
		"created_at":          s.CreatedAt,
		"updated_at":          s.UpdatedAt,
		"closed_at":           s.ClosedAt,
	}
}

func commandShellField(s *session.Session, resolvedShell string) string {
	if s == nil || s.Type != session.TypeCommand {
		return ""
	}
	if shell := strings.TrimSpace(s.Shell); shell != "" {
		return shell
	}
	return strings.TrimSpace(resolvedShell)
}

func (h *HTTPHandler) sessionListResponse(s *session.Session) map[string]any {
	return sessionListResponse(s, h.resolveCommandShell())
}

// resolveCommandShell 算出配置里解析出的默认 shell（command 类会话没显式指定时用）。
// AppContext 侧的广播要能填 shell 字段，但那边没有 HTTPHandler，所以这个解析独立成方法。
func (s *AppContext) ResolveCommandShell() string {
	if s == nil {
		return ""
	}
	pool := s.GetAgentPool()
	if pool == nil {
		return ""
	}
	cfg := pool.Config()
	shells := make([]commandexec.ShellSpec, 0, len(cfg.Shells))
	for _, shell := range cfg.Shells {
		shells = append(shells, commandexec.ShellSpec{
			Command:       shell.Command,
			Args:          append([]string(nil), shell.Args...),
			LongShellArgs: append([]string(nil), shell.LongShellArgs...),
			CommandPrefix: shell.CommandPrefix,
		})
	}
	return commandexec.ResolveShell(shells)
}

func (h *HTTPHandler) resolveCommandShell() string {
	return h.AppContext.ResolveCommandShell()
}

func (h *HTTPHandler) configuredShells() []commandexec.ShellSpec {
	if h == nil || h.AppContext == nil || h.AppContext.GetAgentPool() == nil {
		return nil
	}
	cfg := h.AppContext.GetAgentPool().Config()
	shells := make([]commandexec.ShellSpec, 0, len(cfg.Shells))
	for _, shell := range cfg.Shells {
		shells = append(shells, commandexec.ShellSpec{
			Command:       shell.Command,
			Args:          append([]string(nil), shell.Args...),
			LongShellArgs: append([]string(nil), shell.LongShellArgs...),
			CommandPrefix: shell.CommandPrefix,
		})
	}
	return shells
}

func (h *HTTPHandler) commandShellForSession(s *session.Session) string {
	if s == nil || s.Type != session.TypeCommand {
		return ""
	}
	if strings.TrimSpace(s.Shell) != "" {
		return strings.TrimSpace(s.Shell)
	}
	return commandexec.ResolveShell(h.configuredShells())
}

func (h *HTTPHandler) commandShellForResponse(s *session.Session, aux map[int][]session.ExchangeAux) string {
	if s == nil || s.Type != session.TypeCommand {
		return ""
	}
	if shell := session.InferCommandShellFromAux(aux); strings.TrimSpace(shell) != "" {
		return strings.TrimSpace(shell)
	}
	return h.commandShellForSession(s)
}

func externalSessionListResponse(s agenttypes.ExternalSessionSummary) map[string]any {
	// Title already overlaid with manual alias or stripped 20-char prefix in ListExternalSessions;
	// never expose raw REPLY_TIPS to clients — fall back to stripped firstUserText only.
	name := strings.TrimSpace(s.Title)
	if name == "" {
		name = shortExternalSessionTitleForAPI(s.FirstUserText)
	}
	if name == "" {
		cleaned := strings.TrimSpace(stripReplyTipsPrefixForAPI(stripExternalSessionPrefixForAPI(s.FirstUserText)))
		if cleaned != "" {
			if idx := strings.Index(cleaned, "\n"); idx >= 0 {
				cleaned = strings.TrimSpace(cleaned[:idx])
			}
			name = cleaned
		}
	}
	if name == "" {
		name = s.AgentSessionID
	}
	return map[string]any{
		"key":              s.AgentSessionID,
		"type":             session.TypeChat,
		"agent":            s.Agent,
		"model":            "",
		"name":             name,
		"title":            strings.TrimSpace(s.Title),
		"created_at":       s.UpdatedAt,
		"updated_at":       s.UpdatedAt,
		"closed_at":        nil,
		"agent_session_id": s.AgentSessionID,
	}
}

func stripReplyTipsPrefixForAPI(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "[REPLY_TIPS]") {
		if idx := strings.Index(text, "[USER_PROMPT]"); idx >= 0 {
			return strings.TrimSpace(text[idx+len("[USER_PROMPT]"):])
		}
		if idx := strings.Index(text, "\n\n"); idx >= 0 {
			return strings.TrimSpace(text[idx:])
		}
	}
	return text
}

func shortExternalSessionTitleForAPI(text string) string {
	text = strings.TrimSpace(stripReplyTipsPrefixForAPI(stripExternalSessionPrefixForAPI(text)))
	if text == "" {
		return ""
	}
	if idx := strings.Index(text, "\n"); idx >= 0 {
		text = strings.TrimSpace(text[:idx])
	}
	runes := []rune(text)
	if len(runes) > 20 {
		text = string(runes[:20])
	}
	return strings.TrimSpace(text)
}

func stripExternalSessionPrefixForAPI(text string) string {
	text = strings.TrimSpace(text)
	const prefix = "This session was migrated from elsewhere. Your context may lag behind this session;"
	const tail = "Only if reading fails, output a brief error and stop."
	normalized := strings.ReplaceAll(text, "\\n", "\n")
	if !strings.HasPrefix(normalized, prefix) {
		return text
	}
	idx := strings.Index(normalized, tail)
	if idx < 0 {
		return text
	}
	return strings.TrimSpace(normalized[idx+len(tail):])
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
