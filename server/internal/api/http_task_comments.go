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

func (h *HTTPHandler) handleKanbanTaskCommentsPost(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	taskID := chi.URLParam(r, "id")
	var req struct {
		RootID            string `json:"root_id"`
		CommentID         string `json:"comment_id"`
		Author            string `json:"author"`
		Role              string `json:"role"`
		Body              string `json:"body"`
		Type              string `json:"type"`
		WorkflowChoice    string `json:"workflow_choice"`
		WorkflowVersionID string `json:"workflow_version_id"`
		ExpectedNodeRunID string `json:"expected_node_run_id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	act, req2, err := svc.PostComment(r.Context(), kanban.PostCommentServiceInput{
		RootID: req.RootID, TaskID: taskID, CommentID: req.CommentID, Author: req.Author,
		Role: req.Role, Body: req.Body, Type: req.Type,
		WorkflowChoice: req.WorkflowChoice, WorkflowVersionID: req.WorkflowVersionID,
		ExpectedNodeRunID: req.ExpectedNodeRunID,
	})
	if err != nil {
		if errors.Is(err, kanban.ErrCommentConflict) {
			respondError(w, http.StatusConflict, err)
			return
		}
		if strings.Contains(err.Error(), "required") || strings.Contains(err.Error(), "invalid") {
			respondError(w, http.StatusBadRequest, errInvalidRequest(err.Error()))
			return
		}
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"activity": act, "continue_request": req2})
}

func (h *HTTPHandler) handleKanbanTaskCommentsGet(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	taskID := chi.URLParam(r, "id")
	rootID := r.URL.Query().Get("root_id")
	items, err := svc.ListTaskComments(r.Context(), rootID, taskID)
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"activities": items})
}

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
