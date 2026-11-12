package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"

	"mindfs/server/internal/kanban"

	"github.com/go-chi/chi/v5"
)

// Task 6 (Phase 2): conversation-driven task activities.
//
// Adapted from the plan's standalone ActivityHandler: the repository resolves
// services per account/root, so these are HTTPHandler methods that go through
// kanban.Service. The wire contract is unchanged: POST returns both the user
// activity and the agent reply; GET returns the ordered activity stream.

const maxActivityRequestBytes = 1 << 20

var activityWorkflowChoices = []string{
	kanban.CommentWorkflowChoiceEmpty,
	kanban.CommentWorkflowChoiceSpecified,
	kanban.CommentWorkflowChoiceDisabled,
}

// handleTaskActivitiesCreate handles POST /api/tasks/{id}/activities.
func (h *HTTPHandler) handleTaskActivitiesCreate(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	taskID := strings.TrimSpace(chi.URLParam(r, "id"))
	if taskID == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("task_id required"))
		return
	}

	var req struct {
		RootID            string  `json:"root_id"`
		Body              string  `json:"body"`
		WorkflowChoice    string  `json:"workflow_choice"`
		WorkflowVersionID *string `json:"workflow_version_id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxActivityRequestBytes)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}

	// Review Focus #1: an empty submission is rejected before touching the task
	// store, so a blank composer always reads as "body cannot be empty".
	if strings.TrimSpace(req.Body) == "" {
		respondError(w, http.StatusBadRequest, kanban.ErrEmptyActivityBody)
		return
	}
	choice := strings.TrimSpace(req.WorkflowChoice)
	if choice == "" {
		choice = kanban.CommentWorkflowChoiceEmpty
	}
	if !slices.Contains(activityWorkflowChoices, choice) {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid workflow_choice"))
		return
	}
	rootID := strings.TrimSpace(req.RootID)
	if rootID == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("root_id required"))
		return
	}

	userAct, agentAct, err := svc.CreateUserActivity(r.Context(), rootID, taskID, req.Body, choice, req.WorkflowVersionID)
	if err != nil {
		respondActivityError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"user_activity":  userAct,
		"agent_activity": agentAct,
	})
}

// handleTaskActivitiesList handles GET /api/tasks/{id}/activities?root_id=...
// The frontend uses the `root` alias, so both spellings are accepted.
func (h *HTTPHandler) handleTaskActivitiesList(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	taskID := strings.TrimSpace(chi.URLParam(r, "id"))
	rootID := taskActivityRootID(r)
	if rootID == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("root_id required"))
		return
	}
	items, err := svc.ListTaskActivities(r.Context(), rootID, taskID)
	if err != nil {
		respondActivityError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"activities": items})
}

// handleTaskActivityGet handles GET /api/tasks/{id}/activities/{activityId}.
func (h *HTTPHandler) handleTaskActivityGet(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	taskID := strings.TrimSpace(chi.URLParam(r, "id"))
	activityID := strings.TrimSpace(chi.URLParam(r, "activityId"))
	rootID := taskActivityRootID(r)
	if rootID == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("root_id required"))
		return
	}
	activity, err := svc.GetTaskActivity(r.Context(), rootID, taskID, activityID)
	if err != nil {
		respondActivityError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, activity)
}

func taskActivityRootID(r *http.Request) string {
	if rootID := strings.TrimSpace(r.URL.Query().Get("root_id")); rootID != "" {
		return rootID
	}
	return strings.TrimSpace(r.URL.Query().Get("root"))
}

// respondActivityError maps activity service/store errors onto HTTP status
// codes. "Not found" is the only ambiguous one: both the task lookup and the
// activity lookup surface sql.ErrNoRows or ErrActivityNotFound.
func respondActivityError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, kanban.ErrEmptyActivityBody):
		respondError(w, http.StatusBadRequest, err)
	case errors.Is(err, kanban.ErrActivityNotFound), errors.Is(err, sql.ErrNoRows):
		respondError(w, http.StatusNotFound, err)
	case strings.Contains(err.Error(), "invalid workflow_choice"),
		strings.Contains(err.Error(), "workflow_version_id required"):
		respondError(w, http.StatusBadRequest, err)
	default:
		respondError(w, http.StatusInternalServerError, err)
	}
}
