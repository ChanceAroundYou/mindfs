package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"mindfs/server/internal/api/usecase"
	"mindfs/server/internal/kanban"

	"github.com/go-chi/chi/v5"
)

const workflowAuthoringTimeout = 2 * time.Minute

// workflowAuthoringRequest is the seam between the HTTP gateway and whichever
// Agent harness produces the proposal text. Tests replace only this call.
type workflowAuthoringRequest struct {
	Agent  string
	Model  string
	Prompt string
}

type workflowAuthoringBody struct {
	BaseRevision string `json:"base_revision"`
	Instruction  string `json:"instruction"`
	Agent        string `json:"agent"`
	Model        string `json:"model"`
}

type workflowAuthoringResult struct {
	Proposal *kanban.WorkflowAuthoringProposal `json:"proposal,omitempty"`
	Diff     *kanban.WorkflowDiff              `json:"diff,omitempty"`
	Issues   []kanban.WorkflowPatchIssue       `json:"issues,omitempty"`
	DryRun   *kanban.WorkflowDryRunResult      `json:"dry_run,omitempty"`
}

// handleWorkflowDraftAuthoringProposal turns a natural-language goal into a
// reviewable typed patch for one exact Draft revision. It validates and
// statically dry-runs the candidate but never applies it.
func (h *HTTPHandler) handleWorkflowDraftAuthoringProposal(w http.ResponseWriter, r *http.Request) {
	s, ok := h.workflowStore(w)
	if !ok {
		return
	}
	body, ok := decodeWorkflowAuthoringBody(w, r)
	if !ok {
		return
	}
	d, err := s.GetDraft(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		respondWorkflowReadFailure(w, err, kanban.ErrDraftNotFound)
		return
	}
	if strings.TrimSpace(body.BaseRevision) != d.Revision {
		respondJSON(w, http.StatusConflict, map[string]any{
			"error":            kanban.ErrDraftConflict.Error(),
			"current_revision": d.Revision,
		})
		return
	}
	prompt := buildWorkflowAuthoringPrompt("draft", d.Revision, d.Graph, body.Instruction, "")
	raw, err := h.runWorkflowAuthoring(r.Context(), body, prompt)
	if err != nil {
		respondError(w, http.StatusBadGateway, errInvalidRequest("agent generation failed: "+err.Error()))
		return
	}
	proposal, issues := kanban.ParseWorkflowAuthoringProposal(raw, d.Revision)
	if proposal == nil {
		respondJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "agent proposal invalid", "issues": issues})
		return
	}
	patch := kanban.WorkflowPatch{BaseRevision: d.Revision, Author: "agent", Operations: proposal.Operations}
	result := reviewAuthoringCandidate(d.Graph, patch, nil, nil)
	result.Proposal = proposal
	respondJSON(w, http.StatusOK, result)
}

// handleTaskWorkflowRunAuthoringProposal is the Run-scope twin: only the
// untriggered future subgraph may be proposed, and frozen history is checked by
// the same engine the apply path uses.
func (h *HTTPHandler) handleTaskWorkflowRunAuthoringProposal(w http.ResponseWriter, r *http.Request) {
	store, runID, ok := h.taskWorkflowRunTarget(w, r)
	if !ok {
		return
	}
	body, ok := decodeWorkflowAuthoringBody(w, r)
	if !ok {
		return
	}
	revision, err := strconv.Atoi(strings.TrimSpace(body.BaseRevision))
	if err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("base_revision must be the run revision integer"))
		return
	}
	run, err := store.WorkflowRunByID(r.Context(), runID)
	if err != nil {
		respondWorkflowReadFailure(w, err, kanban.ErrWorkflowRunNotFound)
		return
	}
	if run.Revision != revision {
		respondJSON(w, http.StatusConflict, map[string]any{
			"error":            kanban.ErrRunConflict.Error(),
			"current_revision": strconv.Itoa(run.Revision),
		})
		return
	}
	constraint := "Nodes that already have a NodeRun are frozen. Do not modify or delete them or their incident edges."
	prompt := buildWorkflowAuthoringPrompt("run", strconv.Itoa(run.Revision), run.Graph, body.Instruction, constraint)
	raw, err := h.runWorkflowAuthoring(r.Context(), body, prompt)
	if err != nil {
		respondError(w, http.StatusBadGateway, errInvalidRequest("agent generation failed: "+err.Error()))
		return
	}
	proposal, issues := kanban.ParseWorkflowAuthoringProposal(raw, strconv.Itoa(run.Revision))
	if proposal == nil {
		respondJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "agent proposal invalid", "issues": issues})
		return
	}
	patch := kanban.WorkflowPatch{BaseRevision: strconv.Itoa(run.Revision), Author: "agent", Operations: proposal.Operations}
	graph, diff, issues, err := store.PreviewWorkflowRunPatch(r.Context(), chi.URLParam(r, "id"), runID, revision, patch)
	if err != nil {
		respondWorkflowPatchFailure(w, err)
		return
	}
	result := workflowAuthoringResult{Diff: &diff, Issues: issues}
	if len(issues) == 0 {
		dry, _ := kanban.RunWorkflowDryRun(kanban.WorkflowDryRunInput{
			Graph:          graph,
			Outputs:        run.NodeOutputs,
			LoopState:      run.LoopState,
			NodeStates:     run.NodeStates,
			SelectedRoutes: run.SelectedRoutes,
		})
		result.DryRun = &dry
	}
	result.Proposal = proposal
	respondJSON(w, http.StatusOK, result)
}

func decodeWorkflowAuthoringBody(w http.ResponseWriter, r *http.Request) (workflowAuthoringBody, bool) {
	var body workflowAuthoringBody
	if !decodeWorkflowAgentJSON(w, r, &body, false) {
		return body, false
	}
	body.Instruction = strings.TrimSpace(body.Instruction)
	body.Agent = strings.TrimSpace(body.Agent)
	body.Model = strings.TrimSpace(body.Model)
	if body.Instruction == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("instruction required"))
		return body, false
	}
	return body, true
}

// reviewAuthoringCandidate is the draft-scope read-only path: validate, build
// the candidate graph, then statically dry-run it. Nothing is persisted.
func reviewAuthoringCandidate(base kanban.WorkflowDefinition, patch kanban.WorkflowPatch, outputs map[string]map[string]any, loopState map[string]*kanban.LoopState) workflowAuthoringResult {
	graph, diff, issues := kanban.ApplyWorkflowPatchToGraph(base, patch, nil)
	result := workflowAuthoringResult{Diff: &diff, Issues: issues}
	if len(issues) > 0 {
		return result
	}
	dry, _ := kanban.RunWorkflowDryRun(kanban.WorkflowDryRunInput{Graph: graph, Outputs: outputs, LoopState: loopState})
	result.DryRun = &dry
	return result
}

func (h *HTTPHandler) runWorkflowAuthoring(ctx context.Context, body workflowAuthoringBody, prompt string) (string, error) {
	if h == nil || h.AppContext == nil {
		return "", errors.New("app context unavailable")
	}
	agentName, model := body.Agent, body.Model
	if agentName == "" && h.AppContext.Prefs != nil {
		defaults := h.AppContext.Prefs.SessionNamingDefaults()
		if !defaults.Disabled {
			agentName, model = strings.TrimSpace(defaults.Agent), strings.TrimSpace(defaults.Model)
		}
	}
	if agentName == "" {
		return "", errors.New("agent required")
	}
	req := workflowAuthoringRequest{Agent: agentName, Model: model, Prompt: prompt}
	if h.AppContext.workflowAuthoringOverride != nil {
		return h.AppContext.workflowAuthoringOverride(ctx, req)
	}
	if h.AppContext.Agents == nil {
		return "", errors.New("agent pool unavailable")
	}
	callCtx, cancel := context.WithTimeout(ctx, workflowAuthoringTimeout)
	defer cancel()
	return usecase.RunWorkflowAuthoringPrompt(callCtx, h.AppContext.Agents, req.Agent, req.Model, req.Prompt)
}

func buildWorkflowAuthoringPrompt(scope, revision string, graph kanban.WorkflowDefinition, instruction, constraint string) string {
	graphJSON, _ := json.Marshal(graph)
	lines := []string{
		"You revise MindFS workflow graphs. Reply with ONE JSON object and nothing else.",
		"Do not wrap the JSON in prose. Do not publish, apply, or invent fields.",
		"",
		"Required shape:",
		`{"summary":"one line","rationale":"why","base_revision":"` + revision + `","operations":[{"id":"op-1","op":"add_node","node":{...}}]}`,
		"",
		"Allowed operations: add_node, update_future_node, remove_future_node, add_edge, remove_future_edge.",
		"Operation ids must be unique. Keep the graph valid: every edge endpoint must exist.",
		"Loop nodes use params.max_iterations (1-20), params.exit_expression, and params.graph.",
		"Nested Loop nodes and human_gate nodes inside a Loop are not supported.",
		fmt.Sprintf("Patch limits: %d operations, %d nodes, %d bytes per node.", kanban.WorkflowPatchMaxOperations, kanban.WorkflowPatchMaxNodes, kanban.WorkflowPatchMaxNodeBytes),
		fmt.Sprintf("Scope: %s. Current revision: %s.", scope, revision),
	}
	if constraint != "" {
		lines = append(lines, constraint)
	}
	lines = append(lines, "", "Current graph JSON:", string(graphJSON), "", "User goal:", instruction)
	return strings.Join(lines, "\n")
}
