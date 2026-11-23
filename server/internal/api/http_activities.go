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
// handleTaskQuestionAnswer handles POST /api/tasks/{id}/activities/{activityId}/answer.
//
// Router 发起的提问卡（type=question，payload.source=router）在这里作答：答案
// 落成一条用户留言后 Router 才有 user_message 回合，同时卡片收口、输入框解锁。
// 会话里的提问卡不走这里 —— 它们的答案必须回到那个会话的 tool call（前端用
// WS 的 answerQuestion），服务层会用 ErrQuestionSessionBound 拒掉。
func (h *HTTPHandler) handleTaskQuestionAnswer(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	taskID := strings.TrimSpace(chi.URLParam(r, "id"))
	activityID := strings.TrimSpace(chi.URLParam(r, "activityId"))
	if taskID == "" || activityID == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("task_id and activity_id required"))
		return
	}
	var req struct {
		RootID  string            `json:"root_id"`
		Answers map[string]string `json:"answers"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxActivityRequestBytes)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	rootID := strings.TrimSpace(req.RootID)
	if rootID == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("root_id required"))
		return
	}
	activityService, err := svc.ActivityServiceForRoot(rootID)
	if err != nil {
		respondActivityError(w, err)
		return
	}
	userAct, agentAct, err := activityService.AnswerQuestionCard(r.Context(), taskID, activityID, req.Answers)
	if err != nil {
		respondQuestionAnswerError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"user_activity":  userAct,
		"agent_activity": agentAct,
	})
}

// respondQuestionAnswerError 把作答失败映射成前端能分辨的状态码：
// 已答过是 409（别让用户以为没提交上），会话卡/空答案是 400（点错了地方）。
func respondQuestionAnswerError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, kanban.ErrQuestionAlreadyAnswered):
		respondError(w, http.StatusConflict, err)
	case errors.Is(err, kanban.ErrQuestionSessionBound),
		errors.Is(err, kanban.ErrQuestionAnswerRequired):
		respondError(w, http.StatusBadRequest, err)
	default:
		respondActivityError(w, err)
	}
}

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
	includeHidden := false
	if raw := strings.TrimSpace(r.URL.Query().Get("include_hidden")); raw != "" {
		includeHidden = raw == "1" || strings.EqualFold(raw, "true")
	}
	items, err := svc.ListTaskActivitiesWithHidden(r.Context(), rootID, taskID, includeHidden)
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
