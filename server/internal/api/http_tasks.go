package api

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"mindfs/server/internal/api/usecase"
	"mindfs/server/internal/kanban"

	"github.com/go-chi/chi/v5"
)

func (h *HTTPHandler) kanbanService(w http.ResponseWriter) (*kanban.Service, bool) {
	if h == nil || h.AppContext == nil {
		respondError(w, http.StatusServiceUnavailable, errInvalidRequest("app context unavailable"))
		return nil, false
	}
	svc, err := h.AppContext.GetKanbanService()
	if err != nil {
		respondError(w, http.StatusServiceUnavailable, err)
		return nil, false
	}
	return svc, true
}

func (h *HTTPHandler) handleTaskStageTemplatesList(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	items, err := svc.ListStageTemplates(r.Context())
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *HTTPHandler) handleTaskStageTemplateSave(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	var req kanban.StageTemplate
	if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	item, err := svc.SaveStageTemplate(r.Context(), req)
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, item)
}

func (h *HTTPHandler) handleTaskStageTemplateDelete(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	if err := svc.DeleteStageTemplate(r.Context(), chi.URLParam(r, "id")); err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTPHandler) handleTaskTemplatesList(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	items, err := svc.ListTaskTemplates(r.Context())
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *HTTPHandler) handleTaskTemplateSave(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	var req kanban.TaskTemplate
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	if id := strings.TrimSpace(chi.URLParam(r, "id")); id != "" {
		req.ID = id
	}
	item, err := svc.SaveTaskTemplate(r.Context(), req)
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, item)
}

func (h *HTTPHandler) handleTaskTemplateDelete(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	if err := svc.DeleteTaskTemplate(r.Context(), chi.URLParam(r, "id")); err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTPHandler) handleKanbanTasksList(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	rootID := r.URL.Query().Get("root")
	opts := kanban.ListTasksOptions{
		TemplateID: r.URL.Query().Get("template_id"),
		Status:     r.URL.Query().Get("status"),
		After:      r.URL.Query().Get("after"),
		Before:     r.URL.Query().Get("before"),
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 0 {
			respondError(w, http.StatusBadRequest, errInvalidRequest("invalid limit"))
			return
		}
		opts.Limit = limit
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("task_number")); raw != "" {
		taskNumber, err := strconv.Atoi(strings.TrimPrefix(raw, "#"))
		if err != nil || taskNumber <= 0 {
			respondError(w, http.StatusBadRequest, errInvalidRequest("invalid task_number"))
			return
		}
		opts.TaskNumber = taskNumber
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("stage")); raw != "" {
		stage, err := strconv.Atoi(raw)
		if err != nil {
			respondError(w, http.StatusBadRequest, errInvalidRequest("invalid stage"))
			return
		}
		opts.Stage = stage
		opts.HasStage = true
	}
	items, err := svc.ListTaskDetails(r.Context(), rootID, opts)
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *HTTPHandler) handleKanbanTaskCreate(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	var req struct {
		RootID             string                  `json:"root_id"`
		TaskTemplateID     string                  `json:"task_template_id"`
		Input              string                  `json:"input"`
		Name               string                  `json:"name"`
		Stages             *[]kanban.StageTemplate `json:"stages"`
		CreateWorktree     bool                    `json:"create_worktree"`
		WorktreeBranchMode string                  `json:"worktree_branch_mode"`
		WorktreeBranch     string                  `json:"worktree_branch"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	create := kanban.CreateTaskInput{
		RootID:             req.RootID,
		TaskTemplateID:     req.TaskTemplateID,
		Input:              req.Input,
		Name:               req.Name,
		CreateWorktree:     req.CreateWorktree,
		WorktreeBranchMode: req.WorktreeBranchMode,
		WorktreeBranch:     req.WorktreeBranch,
	}
	if req.Stages != nil {
		create.Stages = *req.Stages
	}
	detail, err := svc.CreateTask(r.Context(), create)
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	h.broadcastTaskUpdated(req.RootID, detail)
	respondJSON(w, http.StatusOK, detail)
}

func (h *HTTPHandler) handleKanbanTaskInputUpdate(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	var req struct {
		RootID             string `json:"root_id"`
		Input              string `json:"input"`
		CreateWorktree     *bool  `json:"create_worktree"`
		WorktreeBranchMode string `json:"worktree_branch_mode"`
		WorktreeBranch     string `json:"worktree_branch"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	detail, err := svc.UpdateCurrentInput(r.Context(), kanban.UpdateTaskInput{
		RootID:             req.RootID,
		TaskID:             chi.URLParam(r, "id"),
		Input:              req.Input,
		CreateWorktree:     req.CreateWorktree,
		WorktreeBranchMode: req.WorktreeBranchMode,
		WorktreeBranch:     req.WorktreeBranch,
	})
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	h.broadcastTaskUpdated(req.RootID, detail)
	respondJSON(w, http.StatusOK, detail)
}

func (h *HTTPHandler) handleKanbanTaskNext(w http.ResponseWriter, r *http.Request) {
	h.handleKanbanTaskMove(w, r, "next")
}

func (h *HTTPHandler) handleKanbanTaskRunNow(w http.ResponseWriter, r *http.Request) {
	h.handleKanbanTaskMove(w, r, "run-now")
}

func (h *HTTPHandler) handleKanbanTaskPrev(w http.ResponseWriter, r *http.Request) {
	h.handleKanbanTaskMove(w, r, "prev")
}

func (h *HTTPHandler) handleKanbanTaskJump(w http.ResponseWriter, r *http.Request) {
	h.handleKanbanTaskMove(w, r, "jump")
}

func (h *HTTPHandler) handleKanbanTaskPause(w http.ResponseWriter, r *http.Request) {
	h.handleKanbanTaskMove(w, r, "pause")
}

func (h *HTTPHandler) handleKanbanTaskResume(w http.ResponseWriter, r *http.Request) {
	h.handleKanbanTaskMove(w, r, "resume")
}

func (h *HTTPHandler) handleKanbanTaskComplete(w http.ResponseWriter, r *http.Request) {
	h.handleKanbanTaskMove(w, r, "complete")
}

func (h *HTTPHandler) handleKanbanTaskCancel(w http.ResponseWriter, r *http.Request) {
	h.handleKanbanTaskMove(w, r, "cancel")
}

func (h *HTTPHandler) handleKanbanTaskFail(w http.ResponseWriter, r *http.Request) {
	h.handleKanbanTaskMove(w, r, "fail")
}

func (h *HTTPHandler) handleKanbanTaskMove(w http.ResponseWriter, r *http.Request, action string) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	var req struct {
		RootID     string `json:"root_id"`
		Reason     string `json:"reason"`
		StageIndex int    `json:"stage_index"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req)
	in := kanban.MoveInput{RootID: req.RootID, TaskID: chi.URLParam(r, "id"), Reason: req.Reason, StageIndex: req.StageIndex}
	var (
		detail kanban.TaskDetail
		err    error
	)
	switch action {
	case "next":
		detail, err = svc.Next(r.Context(), in)
	case "run-now":
		detail, err = svc.RunNow(r.Context(), in)
	case "prev":
		detail, err = svc.Prev(r.Context(), in)
	case "jump":
		detail, err = svc.Jump(r.Context(), in)
	case "pause":
		detail, err = svc.Pause(r.Context(), in)
	case "resume":
		detail, err = svc.Resume(r.Context(), in)
	case "complete":
		detail, err = svc.Complete(r.Context(), in)
	case "cancel":
		detail, err = svc.Cancel(r.Context(), in)
	case "fail":
		detail, err = svc.Fail(r.Context(), in)
	default:
		err = errInvalidRequest("unsupported task action")
	}
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	h.broadcastTaskUpdated(req.RootID, detail)
	respondJSON(w, http.StatusOK, detail)
}

func (h *HTTPHandler) handleKanbanTasksOverview(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	items, err := svc.Overview(r.Context())
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *HTTPHandler) handleKanbanTaskRename(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	var req struct {
		RootID string `json:"root_id"`
		Name   string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	detail, err := svc.RenameTask(r.Context(), req.RootID, chi.URLParam(r, "id"), req.Name)
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	h.broadcastTaskUpdated(req.RootID, detail)
	// 任务名与会话名双向绑定：任务改名把名字同步到绑定的全部会话
	//（请求返回后再同步的小尾巴用 WithoutCancel，避免 response 结束即 ctx 取消）。
	go h.bindTaskSessionNames(context.WithoutCancel(r.Context()), req.RootID, req.Name, detail)
	respondJSON(w, http.StatusOK, detail)
}

// bindTaskSessionNames（任务→会话）：任务绑定到的所有 agent 会话统一改成任务名。
// 单个会话改名失败（会话被删等）不影响其余，也不让任务改名本身失败。
func (h *HTTPHandler) bindTaskSessionNames(ctx context.Context, rootID, name string, detail kanban.TaskDetail) {
	if h == nil || h.AppContext == nil || strings.TrimSpace(name) == "" {
		return
	}
	seen := map[string]bool{}
	keys := []string{}
	if key := strings.TrimSpace(detail.Task.MainSessionKey); key != "" {
		keys = append(keys, key)
		seen[key] = true
	}
	for _, run := range detail.StageRuns {
		key := strings.TrimSpace(run.SessionKey)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		keys = append(keys, key)
	}
	uc := &usecase.Service{Registry: h.AppContext}
	for _, key := range keys {
		renamed, err := uc.RenameSession(ctx, usecase.RenameSessionInput{RootID: rootID, Key: key, Name: name})
		if err != nil {
			log.Printf("[task/rename] sync session name skipped root=%s session=%s err=%v", rootID, key, err)
			continue
		}
		h.AppContext.BroadcastSessionMetaUpdated(rootID, renamed)
	}
}

// syncTaskNameFromSession（会话→任务）：任务主会话改名时同步任务名。
func (h *HTTPHandler) syncTaskNameFromSession(ctx context.Context, rootID, sessionKey, name string) {
	if h == nil || h.AppContext == nil || strings.TrimSpace(name) == "" {
		return
	}
	svc, err := h.AppContext.GetKanbanService()
	if err != nil {
		return
	}
	detail, changed := svc.TaskNameFromSession(ctx, rootID, sessionKey, name)
	if changed {
		h.broadcastTaskUpdated(rootID, detail)
	}
}

func (h *HTTPHandler) handleKanbanTaskRerun(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	var req struct {
		RootID     string `json:"root_id"`
		Reason     string `json:"reason"`
		StageIndex int    `json:"stage_index"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	detail, err := svc.RerunStage(r.Context(), kanban.MoveInput{RootID: req.RootID, TaskID: chi.URLParam(r, "id"), Reason: req.Reason, StageIndex: req.StageIndex})
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	h.broadcastTaskUpdated(req.RootID, detail)
	respondJSON(w, http.StatusOK, detail)
}

func (h *HTTPHandler) handleKanbanTaskAddStage(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	var req struct {
		RootID string               `json:"root_id"`
		Stage  kanban.StageTemplate `json:"stage"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	detail, err := svc.AddStage(r.Context(), kanban.AddStageInput{RootID: req.RootID, TaskID: chi.URLParam(r, "id"), Stage: req.Stage})
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	h.broadcastTaskUpdated(req.RootID, detail)
	respondJSON(w, http.StatusOK, detail)
}

func (h *HTTPHandler) handleKanbanTaskUpdateStage(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	var req struct {
		RootID string                `json:"root_id"`
		Index  int                   `json:"index"`
		Stage  *kanban.StageTemplate `json:"stage"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	detail, err := svc.UpdateStage(r.Context(), kanban.UpdateStageInput{RootID: req.RootID, TaskID: chi.URLParam(r, "id"), Index: req.Index, Stage: req.Stage})
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	h.broadcastTaskUpdated(req.RootID, detail)
	respondJSON(w, http.StatusOK, detail)
}

func (h *HTTPHandler) handleKanbanTaskRemoveStage(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	var req struct {
		RootID string `json:"root_id"`
		Index  int    `json:"index"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	detail, err := svc.RemoveStage(r.Context(), kanban.RemoveStageInput{RootID: req.RootID, TaskID: chi.URLParam(r, "id"), Index: req.Index})
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	h.broadcastTaskUpdated(req.RootID, detail)
	respondJSON(w, http.StatusOK, detail)
}

func (h *HTTPHandler) broadcastTaskUpdated(rootID string, detail kanban.TaskDetail) {
	if h == nil || h.AppContext == nil {
		return
	}
	h.AppContext.GetSessionStreamHub().BroadcastAll(WSResponse{
		Type: "task.updated",
		Payload: map[string]any{
			"root_id": rootID,
			"task":    detail.Task,
		},
	})
}
