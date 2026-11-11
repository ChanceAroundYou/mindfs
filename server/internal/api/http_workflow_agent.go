package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"mindfs/server/internal/kanban"

	"github.com/go-chi/chi/v5"
)

const workflowAgentBodyLimit = 1 << 20

// handleWorkflowDraftSnapshot is the read-only façade an Agent uses before
// producing a typed patch. It exposes no credentials and grants no write power.
func (h *HTTPHandler) handleWorkflowDraftSnapshot(w http.ResponseWriter, r *http.Request) {
	s, ok := h.workflowStore(w)
	if !ok {
		return
	}
	d, err := s.GetDraft(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		respondWorkflowReadFailure(w, err, kanban.ErrDraftNotFound)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"draft": d, "limits": workflowPatchLimits()})
}

// respondWorkflowReadFailure distinguishes "absent" from "unreadable". A
// corrupted store or decode failure must not be reported as a missing resource.
func respondWorkflowReadFailure(w http.ResponseWriter, err, notFound error) {
	if errors.Is(err, notFound) {
		respondError(w, http.StatusNotFound, err)
		return
	}
	respondError(w, http.StatusInternalServerError, err)
}

func workflowPatchLimits() map[string]any {
	return map[string]any{
		"max_operations": kanban.WorkflowPatchMaxOperations,
		"max_nodes":      kanban.WorkflowPatchMaxNodes,
		"max_node_bytes": kanban.WorkflowPatchMaxNodeBytes,
	}
}

// decodeWorkflowAgentJSON enforces the body cap before decoding. A streaming
// json.Decoder would stop at the end of the first value and never notice
// trailing padding, so the whole body is bounded and size-checked first.
func decodeWorkflowAgentJSON(w http.ResponseWriter, r *http.Request, dst any, allowEmpty bool) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, workflowAgentBodyLimit+1))
	if err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("cannot read request body"))
		return false
	}
	if len(body) > workflowAgentBodyLimit {
		respondError(w, http.StatusRequestEntityTooLarge, errInvalidRequest(fmt.Sprintf("request body exceeds %d bytes", workflowAgentBodyLimit)))
		return false
	}
	if allowEmpty && len(bytes.TrimSpace(body)) == 0 {
		return true
	}
	if err := json.Unmarshal(body, dst); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return false
	}
	return true
}

func decodeWorkflowPatch(w http.ResponseWriter, r *http.Request) (kanban.WorkflowPatch, bool) {
	var patch kanban.WorkflowPatch
	if !decodeWorkflowAgentJSON(w, r, &patch, false) {
		return patch, false
	}
	return patch, true
}

func respondWorkflowPatchFailure(w http.ResponseWriter, err error) {
	var patchErr *kanban.WorkflowPatchError
	switch {
	case errors.As(err, &patchErr):
		respondJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "workflow patch invalid", "issues": patchErr.Issues})
	case errors.Is(err, kanban.ErrDraftConflict):
		respondJSON(w, http.StatusConflict, map[string]any{"error": kanban.ErrDraftConflict.Error()})
	case errors.Is(err, kanban.ErrRunConflict):
		respondJSON(w, http.StatusConflict, map[string]any{"error": kanban.ErrRunConflict.Error()})
	case errors.Is(err, kanban.ErrPublishStale):
		respondJSON(w, http.StatusConflict, map[string]any{"error": kanban.ErrPublishStale.Error()})
	case errors.Is(err, kanban.ErrDraftNotFound), errors.Is(err, kanban.ErrWorkflowRunNotFound),
		errors.Is(err, kanban.ErrPublishProposalNotFound), errors.Is(err, kanban.ErrVersionNotFound):
		respondError(w, http.StatusNotFound, err)
	default:
		respondError(w, http.StatusBadRequest, err)
	}
}

func (h *HTTPHandler) handleWorkflowDraftPatchValidate(w http.ResponseWriter, r *http.Request) {
	s, ok := h.workflowStore(w)
	if !ok {
		return
	}
	patch, ok := decodeWorkflowPatch(w, r)
	if !ok {
		return
	}
	d, err := s.GetDraft(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		respondWorkflowReadFailure(w, err, kanban.ErrDraftNotFound)
		return
	}
	if strings.TrimSpace(patch.BaseRevision) != d.Revision {
		respondJSON(w, http.StatusConflict, map[string]any{"error": kanban.ErrDraftConflict.Error(), "current_revision": d.Revision})
		return
	}
	diff, issues, err := kanban.ValidateWorkflowPatch(d.Graph, patch)
	if err != nil {
		if len(issues) > 0 {
			respondWorkflowPatchFailure(w, err)
			return
		}
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"diff": diff})
}

func (h *HTTPHandler) handleWorkflowDraftPatchApply(w http.ResponseWriter, r *http.Request) {
	s, ok := h.workflowStore(w)
	if !ok {
		return
	}
	patch, ok := decodeWorkflowPatch(w, r)
	if !ok {
		return
	}
	d, diff, err := s.ApplyWorkflowDraftPatch(r.Context(), chi.URLParam(r, "id"), patch)
	if err != nil {
		if errors.Is(err, kanban.ErrDraftConflict) {
			respondJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "current": d})
			return
		}
		respondWorkflowPatchFailure(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"draft": d, "diff": diff})
}

// handleWorkflowPublishProposal creates a reviewable proposal only. It never
// publishes; confirmation is a separate human action.
func (h *HTTPHandler) handleWorkflowPublishProposal(w http.ResponseWriter, r *http.Request) {
	s, ok := h.workflowStore(w)
	if !ok {
		return
	}
	var req struct {
		Revision string `json:"revision"`
		Proposer string `json:"proposer"`
	}
	if !decodeWorkflowAgentJSON(w, r, &req, false) {
		return
	}
	if strings.TrimSpace(req.Revision) == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("revision required"))
		return
	}
	p, err := s.ProposeWorkflowPublish(r.Context(), chi.URLParam(r, "id"), req.Revision, req.Proposer)
	if err != nil {
		respondWorkflowPatchFailure(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"proposal": p})
}

func (h *HTTPHandler) handleWorkflowPublishProposalConfirm(w http.ResponseWriter, r *http.Request) {
	s, ok := h.workflowStore(w)
	if !ok {
		return
	}
	var req struct {
		PublishedBy string `json:"published_by"`
	}
	if !decodeWorkflowAgentJSON(w, r, &req, true) {
		return
	}
	v, p, err := s.ConfirmWorkflowPublishProposal(r.Context(), chi.URLParam(r, "proposalID"), req.PublishedBy)
	if err != nil {
		respondWorkflowPatchFailure(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"version": v, "proposal": p})
}

func (h *HTTPHandler) handleWorkflowPublishProposalList(w http.ResponseWriter, r *http.Request) {
	s, ok := h.workflowStore(w)
	if !ok {
		return
	}
	items, err := s.ListPublishProposals(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		respondWorkflowReadFailure(w, err, kanban.ErrDraftNotFound)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *HTTPHandler) handleWorkflowDryRun(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Graph          kanban.WorkflowDefinition    `json:"graph"`
		Outputs        map[string]map[string]any    `json:"outputs"`
		LoopState      map[string]*kanban.LoopState `json:"loop_state"`
		NodeStates     map[string]string            `json:"node_states"`
		SelectedRoutes map[string]string            `json:"selected_routes"`
	}
	if !decodeWorkflowAgentJSON(w, r, &req, false) {
		return
	}
	result, err := kanban.RunWorkflowDryRun(kanban.WorkflowDryRunInput{
		Graph: req.Graph, Outputs: req.Outputs, LoopState: req.LoopState,
		NodeStates: req.NodeStates, SelectedRoutes: req.SelectedRoutes,
	})
	if err != nil {
		respondJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error(), "result": result})
		return
	}
	respondJSON(w, http.StatusOK, result)
}

// taskWorkflowRunTarget resolves the task's current run without trusting the
// caller's root_id beyond the existing task ownership check.
func (h *HTTPHandler) taskWorkflowRunTarget(w http.ResponseWriter, r *http.Request) (*kanban.TaskStore, string, bool) {
	_, store, _, ok := h.taskWorkflowStore(w, r)
	if !ok {
		return nil, "", false
	}
	taskID := chi.URLParam(r, "id")
	engine, err := store.TaskEngine(r.Context(), taskID)
	if err != nil || engine.Engine != kanban.EngineWorkflow || engine.WorkflowRunID == "" {
		respondError(w, http.StatusNotFound, kanban.ErrWorkflowRunNotFound)
		return nil, "", false
	}
	return store, engine.WorkflowRunID, true
}

func workflowPatchRevision(patch kanban.WorkflowPatch) (int, bool) {
	rev, err := strconv.Atoi(strings.TrimSpace(patch.BaseRevision))
	return rev, err == nil
}

func (h *HTTPHandler) handleTaskWorkflowRunPatchValidate(w http.ResponseWriter, r *http.Request) {
	store, runID, ok := h.taskWorkflowRunTarget(w, r)
	if !ok {
		return
	}
	patch, ok := decodeWorkflowPatch(w, r)
	if !ok {
		return
	}
	rev, ok := workflowPatchRevision(patch)
	if !ok {
		respondError(w, http.StatusBadRequest, errInvalidRequest("base_revision must be the run revision integer"))
		return
	}
	diff, _, err := store.ValidateWorkflowRunPatch(r.Context(), chi.URLParam(r, "id"), runID, rev, patch)
	if err != nil {
		respondWorkflowPatchFailure(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"diff": diff})
}

func (h *HTTPHandler) handleTaskWorkflowRunPatchApply(w http.ResponseWriter, r *http.Request) {
	store, runID, ok := h.taskWorkflowRunTarget(w, r)
	if !ok {
		return
	}
	patch, ok := decodeWorkflowPatch(w, r)
	if !ok {
		return
	}
	rev, ok := workflowPatchRevision(patch)
	if !ok {
		respondError(w, http.StatusBadRequest, errInvalidRequest("base_revision must be the run revision integer"))
		return
	}
	run, diff, err := store.ApplyWorkflowRunPatch(r.Context(), chi.URLParam(r, "id"), runID, rev, patch)
	if err != nil {
		respondWorkflowPatchFailure(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"run": run, "diff": diff})
}

// handleTaskWorkflowRunSuggestion exposes the success suggestion as reviewable
// data. It never publishes: the UI/user must create a draft (or use the existing
// publish path) and confirm the exact revision explicitly.
func (h *HTTPHandler) handleTaskWorkflowRunSuggestion(w http.ResponseWriter, r *http.Request) {
	store, runID, ok := h.taskWorkflowRunTarget(w, r)
	if !ok {
		return
	}
	run, err := store.WorkflowRunByID(r.Context(), runID)
	if err != nil {
		respondWorkflowReadFailure(w, err, kanban.ErrWorkflowRunNotFound)
		return
	}
	author := strings.TrimSpace(r.URL.Query().Get("author"))
	if author == "" {
		author = "user"
	}
	respondJSON(w, http.StatusOK, map[string]any{"suggestion": kanban.SuggestTemplateFromRun(run, author)})
}

// handleTaskWorkflowRunDryRun simulates the current run graph only. It writes no
// node state, no activity and no session; supplied outputs stand in for Agents.
func (h *HTTPHandler) handleTaskWorkflowRunDryRun(w http.ResponseWriter, r *http.Request) {
	store, runID, ok := h.taskWorkflowRunTarget(w, r)
	if !ok {
		return
	}
	var req struct {
		Outputs   map[string]map[string]any    `json:"outputs"`
		LoopState map[string]*kanban.LoopState `json:"loop_state"`
	}
	if !decodeWorkflowAgentJSON(w, r, &req, true) {
		return
	}
	run, err := store.WorkflowRunByID(r.Context(), runID)
	if err != nil {
		respondWorkflowReadFailure(w, err, kanban.ErrWorkflowRunNotFound)
		return
	}
	// Start from the run's persisted state and let caller-supplied outputs/loop
	// state stand in for nodes that have not run yet. Passing only the request
	// would replay completed nodes and reset loop budgets in the simulation.
	result, err := kanban.RunWorkflowDryRun(kanban.WorkflowDryRunInput{
		Graph:          run.Graph,
		Outputs:        mergeDryRunOutputs(run.NodeOutputs, req.Outputs),
		LoopState:      mergeDryRunLoopState(run.LoopState, req.LoopState),
		NodeStates:     run.NodeStates,
		SelectedRoutes: run.SelectedRoutes,
	})
	if err != nil {
		respondJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error(), "result": result})
		return
	}
	respondJSON(w, http.StatusOK, result)
}

func mergeDryRunOutputs(base, override map[string]map[string]any) map[string]map[string]any {
	out := make(map[string]map[string]any, len(base)+len(override))
	for id, output := range base {
		out[id] = output
	}
	for id, output := range override {
		out[id] = output
	}
	return out
}

func mergeDryRunLoopState(base, override map[string]*kanban.LoopState) map[string]*kanban.LoopState {
	out := make(map[string]*kanban.LoopState, len(base)+len(override))
	for id, state := range base {
		out[id] = state
	}
	for id, state := range override {
		out[id] = state
	}
	return out
}
