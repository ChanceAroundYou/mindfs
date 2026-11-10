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

// taskWorkflowStore resolves the root's own task SQLite store. Workflow runtime state is
// data-plane state and must never use the shared control-plane WorkflowStore.
func (h *HTTPHandler) taskWorkflowStore(w http.ResponseWriter, r *http.Request) (*kanban.Service, *kanban.TaskStore, string, bool) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return nil, nil, "", false
	}
	rootID := strings.TrimSpace(r.URL.Query().Get("root_id"))
	if rootID == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("root_id required"))
		return nil, nil, "", false
	}
	if _, err := svc.Status(r.Context(), rootID, chi.URLParam(r, "id")); err != nil {
		respondError(w, http.StatusNotFound, err)
		return nil, nil, "", false
	}
	store, err := svc.TaskStoreForRoot(rootID)
	if err != nil {
		respondError(w, http.StatusNotFound, err)
		return nil, nil, "", false
	}
	return svc, store, rootID, true
}

func (h *HTTPHandler) handleTaskWorkflowRunGet(w http.ResponseWriter, r *http.Request) {
	_, store, _, ok := h.taskWorkflowStore(w, r)
	if !ok {
		return
	}
	taskID := chi.URLParam(r, "id")
	engine, err := store.TaskEngine(r.Context(), taskID)
	if err != nil {
		respondError(w, http.StatusNotFound, err)
		return
	}
	if engine.Engine != kanban.EngineWorkflow || engine.WorkflowRunID == "" {
		respondError(w, http.StatusNotFound, kanban.ErrWorkflowRunNotFound)
		return
	}
	run, err := store.WorkflowRunByID(r.Context(), engine.WorkflowRunID)
	if err != nil {
		respondError(w, http.StatusNotFound, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"run": run})
}

func (h *HTTPHandler) handleTaskWorkflowRunEdit(w http.ResponseWriter, r *http.Request) {
	_, store, _, ok := h.taskWorkflowStore(w, r)
	if !ok {
		return
	}
	var req struct {
		Revision int                       `json:"revision"`
		Graph    kanban.WorkflowDefinition `json:"graph"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	engine, err := store.TaskEngine(r.Context(), chi.URLParam(r, "id"))
	if err != nil || engine.Engine != kanban.EngineWorkflow {
		respondError(w, http.StatusNotFound, kanban.ErrWorkflowRunNotFound)
		return
	}
	err = store.EditWorkflowRunGraph(r.Context(), engine.WorkflowRunID, req.Revision, req.Graph)
	if errors.Is(err, kanban.ErrRunConflict) || errors.Is(err, kanban.ErrFrozenNodeEdit) {
		respondJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *HTTPHandler) handleTaskWorkflowNodeRunsGet(w http.ResponseWriter, r *http.Request) {
	_, store, _, ok := h.taskWorkflowStore(w, r)
	if !ok {
		return
	}
	engine, err := store.TaskEngine(r.Context(), chi.URLParam(r, "id"))
	if err != nil || engine.Engine != kanban.EngineWorkflow {
		respondError(w, http.StatusNotFound, kanban.ErrWorkflowRunNotFound)
		return
	}
	nodes, err := store.ListWorkflowNodeRuns(r.Context(), engine.WorkflowRunID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"node_runs": nodes})
}

// handleTaskWorkflowNodeRunRetry is the explicit D9 recovery action: it creates
// a new attempt linked to the failed/unknown NodeRun. It never rewrites the
// original row, its output, or the comment that was bound to it.
func (h *HTTPHandler) handleTaskWorkflowNodeRunRetry(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	var req struct {
		RootID string `json:"root_id"`
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req)
	if strings.TrimSpace(req.RootID) == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("root_id required"))
		return
	}
	nodeRun, err := svc.CreateWorkflowNodeAttempt(r.Context(), req.RootID, chi.URLParam(r, "id"), chi.URLParam(r, "nodeRunID"), req.Reason)
	if err != nil {
		switch {
		case errors.Is(err, kanban.ErrWorkflowNodeRunNotFound):
			respondError(w, http.StatusNotFound, err)
		case errors.Is(err, kanban.ErrWorkflowNodeRunNotActionable):
			respondError(w, http.StatusConflict, err)
		default:
			respondError(w, http.StatusBadRequest, err)
		}
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"node_run": nodeRun})
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
