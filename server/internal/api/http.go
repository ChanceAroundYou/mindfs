package api

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

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
	maxUploadMemoryBytes  = 32 << 20
	maxUploadFileCount    = 20
	sessionListPageSize   = 50
	multiRootSessionLimit = 6
	childSessionPageSize  = 20
	childSessionMaxLimit  = 50
	clientIDHeaderName    = "X-MindFS-Client-ID"
	localCLIHeaderName    = "X-MindFS-Local-CLI-Token"
)

// maxUploadFileRequestBytes 是文件上传（/api/upload）整个请求体的上限。
// 10g 是为了跟 nginx 的 client_max_body_size 10g 对齐 —— 上游放行多少，后端就
// 收多少，否则超限请求会得到一句没头没脑的 "invalid multipart form"。
// 做成 var 是为了测试里能临时改小（见 http_files_upload_limit_test.go）。
var maxUploadFileRequestBytes int64 = 10 << 30

func (h *HTTPHandler) service() *usecase.Service {
	return &usecase.Service{Registry: h.AppContext}
}

// corsPolicyAllows 是跨源判定的唯一实现：REST 中间件与 WebSocket 升级都走它。
// 两边必须同口径 —— 分开写就会出现「REST 放行、WS 被拒」这种只坏一半的状态，
// 而跨节点时页面在控制节点、WS 打到 worker，Origin 与 Host 必然不同。
func corsPolicyAllows(mode, origin string, isListed func(string) bool) bool {
	if mode == "" {
		mode = "open"
	}
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "open", "auto", "allow_all", "all", "*":
		return origin != ""
	case "allowlist", "whitelist":
		return isListed(origin)
	case "disabled", "off", "closed", "same_origin":
		return false
	default:
		return origin != ""
	}
}

func corsOriginAllowed(prefs *preferences.Store, origin string) bool {
	mode := ""
	if prefs != nil {
		mode = prefs.CORSMode()
	}
	return corsPolicyAllows(mode, origin, func(o string) bool {
		return prefs != nil && prefs.IsCORSOriginAllowed(o)
	})
}

func (h *HTTPHandler) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		emitOrigin := origin
		shouldEmit := corsOriginAllowed(h.AppContext.GetPreferences(), origin)
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

func (h *HTTPHandler) handleSessionRepoint(w http.ResponseWriter, r *http.Request) {
	// path 里的 key 可选：wt-finish 这类脚本手里只有 CLAUDE_CODE_SESSION_ID，
	// 没有 mindfs 的 session key，所以也允许放 agent_session_id 让服务端反查。
	key := strings.TrimSpace(chi.URLParam(r, "key"))
	var req struct {
		RootID         string `json:"root_id"`
		AgentSessionID string `json:"agent_session_id"`
		// TargetPath 是会话搬过去之后干活的那个目录。留空 = 项目主 checkout（改造前唯一的目标）。
		// 会话标题栏菜单的「修改目录」传它；wt-finish 那条收尾路径不传。
		TargetPath string `json:"target_path"`
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
		TargetPath:     strings.TrimSpace(req.TargetPath),
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
