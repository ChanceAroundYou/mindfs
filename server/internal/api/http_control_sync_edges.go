package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"mindfs/server/internal/controlsync"
)

// ControlSyncEdges 是主节点侧的边控端名单。
//
// 注册是**自动的**：任何一条过了签名校验的同步请求都记一行，因为那已经证明
// 对方持有同一份 token。再加一个「先调 /register」的步骤，只会多一次可能失败的
// 网络往返，而不会多出任何安全性。
//
// 必须说清边界：名单里的instance_id 来自 `X-MindFS-Instance` 头，它是调用方的
// **自称**，不在签名覆盖范围内（见 controlsync/auth.go）。所以这张表是运维索引，
// 用来看「谁在同步」；踢人（Revoke）让「这台机器别再同步」变成可执行的动作，
// 但它挡不住拿着同一份 token 换个名字重新接入的客户端 —— 真正的凭证始终是 token。
type ControlSyncEdges interface {
	ObserveEdge(ctx context.Context, instanceID string, at time.Time) (controlsync.EdgeInfo, error)
	ListEdges(ctx context.Context) ([]controlsync.EdgeInfo, error)
	RevokeEdge(ctx context.Context, instanceID string, at time.Time) (controlsync.EdgeInfo, error)
	RestoreEdge(ctx context.Context, instanceID string) (controlsync.EdgeInfo, error)
}

// errEdgeRevoked 是 403 的唯一响应体：这台边控端被主节点踢了。
//
// 不回显任何策略细节（谁踢的、什么时候）：调用方需要知道的只有「别再试了，
// 去找主节点的人处理」。
var errEdgeRevoked = errors.New("control_sync: edge revoked")

// noteRequestingEdge 在认证通过之后把调用方记进名单，并在它已被踢掉时拒绝。
//
// 放在这里而不是各 handler 里：所有同步端点都走 requireSyncToken，一处判定就
// 覆盖 /head /changes /push 与前端镜像，不会出现「push 被拦但 changes 还能拉」
// 这种半截状态。
//
// 没有 instance 头时直接放行（不登记也不拦）：那是改造前的客户端形态，而
// token 校验已经过了，没有额外的信息可以据以拒绝。
func (h *HTTPHandler) noteRequestingEdge(w http.ResponseWriter, r *http.Request) bool {
	if h.ControlSyncEdges == nil {
		return true
	}
	instanceID := strings.TrimSpace(r.Header.Get(controlsync.SyncInstanceHeader))
	if instanceID == "" {
		return true
	}
	_, err := h.ControlSyncEdges.ObserveEdge(r.Context(), instanceID, time.Now())
	if errors.Is(err, controlsync.ErrEdgeRevoked) {
		respondError(w, http.StatusForbidden, errEdgeRevoked)
		return false
	}
	if err != nil {
		// 名单写不进去不该拦同步：它是运维索引，不是数据通路的一部分。
		// 只记日志会漏掉（api 包不持有 logger），因此让 handler 自己决定 ——
		// 这里按放行处理，下一次请求会再试一次。
		return true
	}
	return true
}

// handleControlSyncEdgesList 返回所有出现过的边控端（被踢的也列出，只是带 revoked_at）。
func (h *HTTPHandler) handleControlSyncEdgesList(w http.ResponseWriter, r *http.Request) {
	if h.ControlSyncEdges == nil {
		respondError(w, http.StatusNotFound, errInvalidRequest("control sync edges not configured"))
		return
	}
	edges, err := h.ControlSyncEdges.ListEdges(r.Context())
	if err != nil {
		respondError(w, http.StatusInternalServerError, err)
		return
	}
	if edges == nil {
		edges = []controlsync.EdgeInfo{}
	}
	respondJSON(w, http.StatusOK, map[string]any{"edges": edges})
}

// handleControlSyncEdgeRevoke 踢掉一台边控端（重复踢幂等，保留首次时间）。
func (h *HTTPHandler) handleControlSyncEdgeRevoke(w http.ResponseWriter, r *http.Request) {
	h.updateEdge(w, r, "revoke")
}

// handleControlSyncEdgeRestore 撤回踢人。单独一个动作：注册是自动的，
// 把恢复也做成自动的就等于踢人没有意义了。
func (h *HTTPHandler) handleControlSyncEdgeRestore(w http.ResponseWriter, r *http.Request) {
	h.updateEdge(w, r, "restore")
}

func (h *HTTPHandler) updateEdge(w http.ResponseWriter, r *http.Request, action string) {
	if h.ControlSyncEdges == nil {
		respondError(w, http.StatusNotFound, errInvalidRequest("control sync edges not configured"))
		return
	}
	// instance id 可能是任意自称串（主机名、容器名），前端只能整体
	// encodeURIComponent —— 与冲突卡同一条处理，否则带 / 的名字点不动。
	id, err := url.PathUnescape(chi.URLParam(r, "id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid edge id"))
		return
	}
	if strings.TrimSpace(id) == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("edge id required"))
		return
	}
	var edge controlsync.EdgeInfo
	if action == "revoke" {
		edge, err = h.ControlSyncEdges.RevokeEdge(r.Context(), id, time.Now())
	} else {
		edge, err = h.ControlSyncEdges.RestoreEdge(r.Context(), id)
	}
	if err != nil {
		// 「没登记过」是 404：踢一台从没连上来的机器不该写成成功，
		// 否则界面上会冒出一条永远等不到请求的封禁。
		if errors.Is(err, controlsync.ErrEdgeNotFound) {
			respondError(w, http.StatusNotFound, err)
			return
		}
		respondError(w, http.StatusInternalServerError, err)
		return
	}
	respondJSON(w, http.StatusOK, edge)
}
