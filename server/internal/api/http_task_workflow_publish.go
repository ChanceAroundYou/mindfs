package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"mindfs/server/internal/kanban"

	"github.com/go-chi/chi/v5"
)

// handleKanbanTaskWorkflowPublish 是人处理一张「固化 workflow」卡片的专用端点。
// 普通留言端点不能批准它 —— 和 HumanGate 一样的规矩：批准是显式动作。
func (h *HTTPHandler) handleKanbanTaskWorkflowPublish(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	taskID := chi.URLParam(r, "id")
	var req struct {
		RootID     string `json:"root_id"`
		ActivityID string `json:"activity_id"`
		Decision   string `json:"decision"`
		Note       string `json:"note"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	act, err := svc.ResolveWorkflowPublish(r.Context(), req.RootID, taskID, req.ActivityID, req.Decision, req.Note)
	if err != nil {
		switch {
		case errors.Is(err, kanban.ErrWorkflowPublishDecided):
			respondError(w, http.StatusConflict, err)
		case errors.Is(err, kanban.ErrWorkflowPublishStale):
			respondError(w, http.StatusConflict, err)
		case errors.Is(err, kanban.ErrWorkflowPublishNotFound):
			respondError(w, http.StatusNotFound, err)
		case strings.Contains(err.Error(), "invalid") || strings.Contains(err.Error(), "required") ||
			strings.Contains(err.Error(), "must be"):
			respondError(w, http.StatusBadRequest, errInvalidRequest(err.Error()))
		default:
			respondError(w, http.StatusBadRequest, err)
		}
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"activity": act})
}
