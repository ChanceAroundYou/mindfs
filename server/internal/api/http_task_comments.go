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
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	act, req2, err := svc.PostComment(r.Context(), kanban.PostCommentServiceInput{
		RootID: req.RootID, TaskID: taskID, CommentID: req.CommentID, Author: req.Author,
		Role: req.Role, Body: req.Body, Type: req.Type,
		WorkflowChoice: req.WorkflowChoice, WorkflowVersionID: req.WorkflowVersionID,
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
