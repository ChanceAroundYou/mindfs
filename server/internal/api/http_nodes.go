package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"mindfs/server/internal/nodeinfo"
	"mindfs/server/internal/nodes"

	"github.com/go-chi/chi/v5"
)

// rejectControlPlaneOnWorker 让运行节点不提供控制面。
//
// 控制面状态只在主节点有一份真相。允许 worker 也提供，两份配置就会各写各的
// —— 节点表分裂就是这么来的：同一台物理机器在两边的 nodes.json 里拿到不同 id。
//
// 本机 CLI 例外，不是可选项而是正确性要求：isLocalCLIPath 的白名单里有
// /api/task-templates（控制面），本地 CLI 拿 token 直连时必须还能用，
// 否则「从命令行读模板」会在 worker 上失效。
func (h *HTTPHandler) rejectControlPlaneOnWorker(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.NodeRole == nodeinfo.RoleWorker &&
			nodeinfo.IsControlPlane(r.URL.Path) &&
			!h.isLocalCLIRequest(r) {
			respondError(w, http.StatusForbidden, errInvalidRequest("node_is_worker"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// handleNodeInfo 只回答「这台机器是什么角色、提不提供前端」，供前端决定要不要
// 给它 UI 入口。static=false 的节点点了「在新窗口打开」只会撞 403 ——
// 不如入口干脆不显示。
func (h *HTTPHandler) handleNodeInfo(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, map[string]any{
		"role":    string(nodeinfo.Normalize(string(h.NodeRole))),
		"version": h.Version,
		"static":  strings.TrimSpace(h.StaticDir) != "",
	})
}

func (h *HTTPHandler) handleNodesList(w http.ResponseWriter, _ *http.Request) {
	store := h.nodesStore()
	if store == nil {
		respondJSON(w, http.StatusOK, []nodes.NodeConnection{})
		return
	}
	respondJSON(w, http.StatusOK, store.List())
}

func (h *HTTPHandler) handleNodesPut(w http.ResponseWriter, r *http.Request) {
	store := h.nodesStore()
	if store == nil {
		respondError(w, http.StatusServiceUnavailable, errInvalidRequest("nodes store not configured"))
		return
	}
	var payload []nodes.NodeConnection
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&payload); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid nodes payload"))
		return
	}
	if err := store.Replace(payload); err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	h.broadcastNodesChanged()
	respondJSON(w, http.StatusOK, store.List())
}

func (h *HTTPHandler) handleNodesPost(w http.ResponseWriter, r *http.Request) {
	store := h.nodesStore()
	if store == nil {
		respondError(w, http.StatusServiceUnavailable, errInvalidRequest("nodes store not configured"))
		return
	}
	var node nodes.NodeConnection
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&node); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid node payload"))
		return
	}
	saved, err := store.Upsert(node)
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	h.broadcastNodesChanged()
	respondJSON(w, http.StatusOK, saved)
}

func (h *HTTPHandler) handleNodesDelete(w http.ResponseWriter, r *http.Request) {
	store := h.nodesStore()
	if store == nil {
		respondError(w, http.StatusServiceUnavailable, errInvalidRequest("nodes store not configured"))
		return
	}
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	if id == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("id required"))
		return
	}
	removed, err := store.Remove(id)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		respondError(w, status, err)
		return
	}
	h.broadcastNodesChanged()
	respondJSON(w, http.StatusOK, removed)
}

func (h *HTTPHandler) nodesStore() *nodes.Store {
	if h == nil || h.AppContext == nil {
		return nil
	}
	return h.AppContext.Nodes
}

func (h *HTTPHandler) broadcastNodesChanged() {
	if h == nil || h.AppContext == nil {
		return
	}
	h.AppContext.GetSessionStreamHub().BroadcastAll(WSResponse{
		Type:    "nodes.changed",
		Payload: map[string]any{},
	})
}
