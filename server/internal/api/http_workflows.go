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

func (h *HTTPHandler) workflowStore(w http.ResponseWriter) (*kanban.WorkflowStore, bool) {
	if h == nil || h.AppContext == nil || h.AppContext.Kanban == nil {
		respondError(w, http.StatusServiceUnavailable, errInvalidRequest("app context unavailable"))
		return nil, false
	}
	s := h.AppContext.Kanban.Workflows
	if s == nil {
		respondError(w, http.StatusServiceUnavailable, errInvalidRequest("workflow store unavailable"))
		return nil, false
	}
	return s, true
}

func (h *HTTPHandler) handleWorkflowsList(w http.ResponseWriter, r *http.Request) {
	s, ok := h.workflowStore(w)
	if !ok {
		return
	}
	items, err := s.ListDrafts(r.Context())
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *HTTPHandler) handleWorkflowDraftCreate(w http.ResponseWriter, r *http.Request) {
	s, ok := h.workflowStore(w)
	if !ok {
		return
	}
	var req struct {
		WorkflowID string                    `json:"workflow_id"`
		RootID     string                    `json:"root_id"`
		Author     string                    `json:"author"`
		Graph      kanban.WorkflowDefinition `json:"graph"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	d, err := s.CreateDraft(r.Context(), req.WorkflowID, req.RootID, req.Author, req.Graph)
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"draft": d})
}

func (h *HTTPHandler) handleWorkflowDraftUpdate(w http.ResponseWriter, r *http.Request) {
	s, ok := h.workflowStore(w)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	var req struct {
		BaseRevision string                    `json:"base_revision"`
		Author       string                    `json:"author"`
		Graph        kanban.WorkflowDefinition `json:"graph"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	d, err := s.UpdateDraft(r.Context(), id, req.BaseRevision, req.Author, req.Graph)
	if err != nil {
		if errors.Is(err, kanban.ErrDraftConflict) {
			respondJSON(w, http.StatusConflict, map[string]any{"error": "draft conflict: base_revision stale", "current": d})
			return
		}
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"draft": d})
}

func (h *HTTPHandler) handleWorkflowDraftPublish(w http.ResponseWriter, r *http.Request) {
	s, ok := h.workflowStore(w)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	var req struct {
		ConfirmedRevision string `json:"confirmed_revision"`
		PublishedBy       string `json:"published_by"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	if strings.TrimSpace(req.ConfirmedRevision) == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("confirmed_revision required"))
		return
	}
	v, err := s.PublishDraft(r.Context(), id, req.ConfirmedRevision, req.PublishedBy)
	if err != nil {
		if errors.Is(err, kanban.ErrPublishStale) {
			respondJSON(w, http.StatusConflict, map[string]any{"error": "stale confirmation: draft changed since confirm"})
			return
		}
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"version": v})
}

func (h *HTTPHandler) handleWorkflowVersionGet(w http.ResponseWriter, r *http.Request) {
	s, ok := h.workflowStore(w)
	if !ok {
		return
	}
	version := chi.URLParam(r, "version")
	v, err := s.GetVersion(r.Context(), version)
	if err != nil {
		respondError(w, http.StatusNotFound, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"version": v})
}

func (h *HTTPHandler) handleWorkflowRunCreate(w http.ResponseWriter, r *http.Request) {
	s, ok := h.workflowStore(w)
	if !ok {
		return
	}
	var req struct {
		TaskID  string `json:"task_id"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	if req.TaskID == "" || req.Version == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("task_id and version required"))
		return
	}
	ver, err := s.GetVersion(r.Context(), req.Version)
	if err != nil {
		respondError(w, http.StatusNotFound, err)
		return
	}
	run, err := s.CreateRun(r.Context(), req.TaskID, ver.Version, ver.Graph)
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"run": run})
}

func (h *HTTPHandler) handleWorkflowRunEdit(w http.ResponseWriter, r *http.Request) {
	s, ok := h.workflowStore(w)
	if !ok {
		return
	}
	runID := chi.URLParam(r, "runId")
	var req struct {
		Revision int                       `json:"revision"`
		Graph    kanban.WorkflowDefinition `json:"graph"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	if err := s.EditRunGraph(r.Context(), runID, req.Revision, req.Graph); err != nil {
		if errors.Is(err, kanban.ErrRunConflict) || errors.Is(err, kanban.ErrFrozenNodeEdit) {
			respondJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
			return
		}
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}
