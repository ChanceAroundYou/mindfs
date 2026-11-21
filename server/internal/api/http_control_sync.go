package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"mindfs/server/internal/controlsync"
)

// ControlSyncService 是 HTTP 层用到的控制面同步能力。
//
// 只收「人要看/人要按」的三件事；转发与追平由中间件和 Replicator 各自驱动，
// 不从这里穿过去 —— 否则 HTTP 层会变成同步内核的第二张装配图。
type ControlSyncService interface {
	Status(ctx context.Context) (controlsync.Status, error)
	SwitchLocal(ctx context.Context) error
	Retry(ctx context.Context) (controlsync.Mode, error)
}

// ControlSyncPusher 是主节点侧接收边缘推送的入口（spec §6.1）。
// 与 ControlSyncService 分开：边缘没有 Pusher，主节点也不一定有模式控制器。
type ControlSyncPusher interface {
	Push(ctx context.Context, req controlsync.PushRequest) (controlsync.PushBatch, error)
}

// handleControlSyncStatus 返回四态模式、水位与最后一次探测信息。
// 两种模式（online / offline-local）都要能读到它，否则断线时前端连横幅都点不出来。
func (h *HTTPHandler) handleControlSyncStatus(w http.ResponseWriter, r *http.Request) {
	if h.ControlSync == nil {
		respondError(w, http.StatusNotFound, errInvalidRequest("control sync not configured"))
		return
	}
	status, err := h.ControlSync.Status(r.Context())
	if err != nil {
		respondError(w, http.StatusInternalServerError, err)
		return
	}
	respondJSON(w, http.StatusOK, status)
}

// handleControlSyncMode 接受用户动作：{action:"local"|"retry"}。
//
// retry 探不通时仍回 200 + 最新 status：动作本身被接受了，结果（仍是 offline-local、
// last_error 写明原因）由 status 表达。用 5xx 表达「主节点不通」会让前端把
// 「自己按的按钮失败了」和「页面请求失败了」混成一件事。
func (h *HTTPHandler) handleControlSyncMode(w http.ResponseWriter, r *http.Request) {
	if h.ControlSync == nil {
		respondError(w, http.StatusNotFound, errInvalidRequest("control sync not configured"))
		return
	}
	var body struct {
		Action string `json:"action"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10))
	if err := dec.Decode(&body); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid body"))
		return
	}
	action := strings.TrimSpace(body.Action)
	switch action {
	case "local":
		if err := h.ControlSync.SwitchLocal(r.Context()); err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
	case "retry":
		if _, err := h.ControlSync.Retry(r.Context()); err != nil {
			// 探不通不是接口错误：状态里已经写明 reason，照常回 status。
			_ = err
		}
	default:
		respondError(w, http.StatusBadRequest, errInvalidRequest("action must be local or retry"))
		return
	}
	status, err := h.ControlSync.Status(r.Context())
	if err != nil {
		respondError(w, http.StatusInternalServerError, err)
		return
	}
	respondJSON(w, http.StatusOK, status)
}

// handleControlSyncPush 接收边缘的待同步变更，逐条回结论。
//
// 请求体上限按一整批 tombstone/小对象估算给 8 MiB：超限直接 413，
// 而不是让解码器在超大 JSON 上慢慢吃内存。
func (h *HTTPHandler) handleControlSyncPush(w http.ResponseWriter, r *http.Request) {
	if h.ControlSyncPush == nil {
		respondError(w, http.StatusNotFound, errInvalidRequest("control sync push not configured"))
		return
	}
	var req controlsync.PushRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20))
	if err := dec.Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid push body"))
		return
	}
	batch, err := h.ControlSyncPush.Push(r.Context(), req)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err)
		return
	}
	respondJSON(w, http.StatusOK, batch)
}

// ControlSyncConflicts 是冲突卡的查询与裁决端口。冲突只在「两侧都改过」时产生，
// 裁决永远由人做出 —— 这里不提供自动合并。
type ControlSyncConflicts interface {
	List(ctx context.Context) ([]controlsync.ConflictView, error)
	Resolve(ctx context.Context, id, choice string, payload []byte) error
}

type controlSyncConflictList struct {
	Conflicts []controlsync.ConflictView `json:"conflicts"`
}

func (h *HTTPHandler) handleControlSyncConflicts(w http.ResponseWriter, r *http.Request) {
	views, err := h.ControlSyncConflicts.List(r.Context())
	if err != nil {
		respondError(w, http.StatusInternalServerError, err)
		return
	}
	if views == nil {
		views = []controlsync.ConflictView{}
	}
	respondJSON(w, http.StatusOK, controlSyncConflictList{Conflicts: views})
}

type controlSyncResolveRequest struct {
	Choice  string          `json:"choice"`
	Payload json.RawMessage `json:"payload"`
}

// 冲突卡裁决：`{"choice":"local"|"primary"|"payload","payload":{...}}`。
// 404 / 409 / 400 分别对应「没有这张卡」「已经裁决过」「选择或载荷不合法」。
func (h *HTTPHandler) handleControlSyncConflictResolve(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if strings.TrimSpace(id) == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("conflict id required"))
		return
	}
	var req controlSyncResolveRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	err := h.ControlSyncConflicts.Resolve(r.Context(), id, req.Choice, req.Payload)
	switch {
	case err == nil:
		respondJSON(w, http.StatusOK, map[string]any{"id": id, "choice": req.Choice})
	case errors.Is(err, controlsync.ErrConflictNotFound):
		respondError(w, http.StatusNotFound, err)
	case errors.Is(err, controlsync.ErrConflictResolved):
		respondError(w, http.StatusConflict, err)
	case errors.Is(err, controlsync.ErrConflictChoice):
		respondError(w, http.StatusBadRequest, err)
	default:
		respondError(w, http.StatusInternalServerError, err)
	}
}
