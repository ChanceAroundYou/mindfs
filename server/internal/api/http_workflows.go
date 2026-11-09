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

// handleTaskMigrateToWorkflow 把一个存量任务显式迁移到 WorkflowRun 引擎。
//
// 迁移是单向、显式的用户动作（spec「新旧执行模型互斥」）：成功后该任务的 legacy
// Stages 引擎不再推进；不带 version 时按任务现有 Stages 生成线性图快照。
func (h *HTTPHandler) handleTaskMigrateToWorkflow(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	taskID := chi.URLParam(r, "id")
	var req struct {
		RootID  string `json:"root_id"`
		Version string `json:"version"`
		Author  string `json:"author"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	if strings.TrimSpace(req.RootID) == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("root_id required"))
		return
	}
	run, engine, err := svc.MigrateTaskToWorkflowRun(r.Context(), req.RootID, taskID, req.Version, req.Author)
	if err != nil {
		if errors.Is(err, kanban.ErrTaskAlreadyWorkflow) {
			respondJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "engine": engine})
			return
		}
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"run": run, "engine": engine})
}

// handleWorkflowRunGet 返回 Run 快照供运行视图只读展示。
func (h *HTTPHandler) handleWorkflowRunGet(w http.ResponseWriter, r *http.Request) {
	s, ok := h.workflowStore(w)
	if !ok {
		return
	}
	runID := chi.URLParam(r, "runId")
	run, err := s.GetRun(r.Context(), runID)
	if err != nil {
		respondError(w, http.StatusNotFound, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"run": run})
}

// handleTaskEngineGet 返回任务的执行引擎绑定：前端据此决定是否展示运行视图入口。
func (h *HTTPHandler) handleTaskEngineGet(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	rootID := strings.TrimSpace(r.URL.Query().Get("root_id"))
	if rootID == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("root_id required"))
		return
	}
	engine, err := svc.TaskEngineFor(r.Context(), rootID, chi.URLParam(r, "id"))
	if err != nil {
		respondError(w, http.StatusNotFound, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"engine": engine})
}
