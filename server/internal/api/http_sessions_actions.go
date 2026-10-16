package api

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/api/usecase"
	"mindfs/server/internal/session"

	"github.com/go-chi/chi/v5"
)

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
