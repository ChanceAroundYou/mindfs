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

// handleKanbanTaskHumanGate is the dedicated approve/reject endpoint (D4).
// A normal comment endpoint deliberately cannot decide a gate.
func (h *HTTPHandler) handleKanbanTaskHumanGate(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	taskID := chi.URLParam(r, "id")
	var req struct {
		RootID    string `json:"root_id"`
		NodeID    string `json:"node_id"`
		Decision  string `json:"decision"`
		CommentID string `json:"comment_id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	nodeRun, err := svc.ResolveWorkflowHumanGate(r.Context(), req.RootID, taskID, req.NodeID, req.Decision, req.CommentID)
	if err != nil {
		switch {
		case errors.Is(err, kanban.ErrWorkflowHumanGateAlreadyDecided), errors.Is(err, kanban.ErrWorkflowHumanGateNotWaiting),
			errors.Is(err, kanban.ErrWorkflowNodeRunNotActionable):
			respondError(w, http.StatusConflict, err)
		case errors.Is(err, kanban.ErrWorkflowNodeRunNotFound):
			respondError(w, http.StatusNotFound, err)
		case strings.Contains(err.Error(), "invalid") || strings.Contains(err.Error(), "required"):
			respondError(w, http.StatusBadRequest, errInvalidRequest(err.Error()))
		default:
			respondError(w, http.StatusBadRequest, err)
		}
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"node_run": nodeRun})
}
