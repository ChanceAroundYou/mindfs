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
// 本机 CLI 例外保留给白名单里的 CLI 路径（见 isLocalCLIPath）。模板库下线后
// 白名单里已没有控制面端点，这段豁免目前是空的保险；新增控制面 CLI 端点时
// 记得同步白名单，否则 worker 上的 CLI 直连会被守卫拦掉。
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

// handleNodeInfo 回答「这台机器是谁、什么角色、提不提供前端」。
//
//	id/name 是本机自报身份（主控 home/主控，边缘取 instance_id/安装时节点名）。
//	前端用它取代过去硬编码的 local 伪节点，并据此拼 scope 键。
//	role/static 供前端决定要不要给它 UI 入口：static=false 的节点点了
//	「在新窗口打开」只会撞 403，不如入口干脆不显示。
func (h *HTTPHandler) handleNodeInfo(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, map[string]any{
		"id":      strings.TrimSpace(h.NodeID),
		"name":    strings.TrimSpace(h.NodeName),
		"role":    string(nodeinfo.Normalize(string(h.NodeRole))),
		"version": h.Version,
		// 问的是「现在打得开页面吗」，所以与真实服务路径同源：只用镜像出页面的边缘
		// 也算 static=true，否则前端会把这个节点的「打开网页」入口藏掉。
		"static": h.servingStaticDir() != "",
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
	if conflict, taken := nodes.NameConflicts(store.List(), payload); taken {
		respondNodeNameTaken(w, conflict)
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
	// 按「写入后的整表」判重：先把它替换进当前表，再比历史数据。
	existing := store.List()
	next := make([]nodes.NodeConnection, 0, len(existing)+1)
	replaced := false
	for _, n := range existing {
		if n.ID == strings.TrimSpace(node.ID) {
			next = append(next, node)
			replaced = true
			continue
		}
		next = append(next, n)
	}
	if !replaced {
		next = append(next, node)
	}
	if conflict, taken := nodes.NameConflicts(existing, next); taken {
		respondNodeNameTaken(w, conflict)
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

// handleNodesValidateName 是安装向导、设置页与 CLI 共用的查重入口。
//
// 把 trim/忽略大小写的规则收口在服务端，避免三处各写一份、结论不一致。
// 只读：不改节点表；对端不可达等错误由调用方按网络错误处理，不在这里伪装成「可用」。
func (h *HTTPHandler) handleNodesValidateName(w http.ResponseWriter, r *http.Request) {
	store := h.nodesStore()
	if store == nil {
		respondError(w, http.StatusServiceUnavailable, errInvalidRequest("nodes store not configured"))
		return
	}
	var req struct {
		Name string `json:"name"`
		// ID 是要改名的那个节点（新建时留空）：它自己不算冲突。
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("name required"))
		return
	}
	excludeID := strings.TrimSpace(req.ID)
	for _, n := range store.List() {
		if n.ID == excludeID {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(n.Name), name) {
			respondJSON(w, http.StatusOK, map[string]any{
				"available": false,
				"conflict":  map[string]any{"id": n.ID, "name": n.Name},
			})
			return
		}
	}
	respondJSON(w, http.StatusOK, map[string]any{"available": true})
}

// respondNodeNameTaken 用 409 而不是 400：名字冲突是「当前状态不允许」，
// 不是请求格式错误；前端与安装向导据此提示改名而不是报「请求无效」。
func respondNodeNameTaken(w http.ResponseWriter, conflict nodes.NodeConnection) {
	respondJSON(w, http.StatusConflict, map[string]any{
		"error": "node_name_taken",
		"conflict": map[string]any{
			"id":   conflict.ID,
			"name": conflict.Name,
		},
	})
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
