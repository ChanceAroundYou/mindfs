package api

import (
	"context"
	"encoding/json"
	"net/http"

	"mindfs/server/internal/kanban"

	"github.com/go-chi/chi/v5"
)

// handleListTaskActivities 获取任务的活动流
func (h *HTTPHandler) handleListTaskActivities(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	if taskID == "" {
		http.Error(w, "task_id required", http.StatusBadRequest)
		return
	}

	rootID := r.URL.Query().Get("root")
	if rootID == "" {
		http.Error(w, "root parameter required", http.StatusBadRequest)
		return
	}

	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}

	activities, err := svc.ListTaskComments(r.Context(), rootID, taskID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"activities": activities,
	})
}

// handlePostTaskActivity 发送用户留言
func (h *HTTPHandler) handlePostTaskActivity(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	if taskID == "" {
		http.Error(w, "task_id required", http.StatusBadRequest)
		return
	}

	var req struct {
		RootID            string `json:"root_id"`
		Body              string `json:"body"`
		WorkflowChoice    string `json:"workflow_choice"`
		WorkflowVersionID string `json:"workflow_version_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Body == "" {
		http.Error(w, "body cannot be empty", http.StatusBadRequest)
		return
	}

	if req.RootID == "" {
		http.Error(w, "root_id required", http.StatusBadRequest)
		return
	}

	// 默认 workflow_choice
	if req.WorkflowChoice == "" {
		req.WorkflowChoice = "empty"
	}

	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}

	// 创建用户留言
	input := kanban.PostCommentServiceInput{
		RootID:            req.RootID,
		TaskID:            taskID,
		Author:            "", // TODO: 从 session/auth 获取
		Role:              "user",
		Body:              req.Body,
		Type:              "user_message",
		WorkflowChoice:    req.WorkflowChoice,
		WorkflowVersionID: req.WorkflowVersionID,
	}

	activity, continueReq, err := svc.PostComment(context.Background(), input)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"user_activity":    activity,
		"continue_request": continueReq,
	})
}

