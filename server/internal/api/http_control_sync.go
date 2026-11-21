package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

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
