package api

import (
	"context"
	"encoding/json"
	"errors"
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

// handleKanbanTaskRebuildWorktree 重建已删除的任务 worktree（显式入口，不自动触发）。
func (h *HTTPHandler) handleKanbanTaskRebuildWorktree(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	var req struct {
		RootID string `json:"root_id"`
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req)
	rootID := strings.TrimSpace(req.RootID)
	taskID := strings.TrimSpace(chi.URLParam(r, "id"))
	if rootID == "" || taskID == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("root_id and id required"))
		return
	}
	detail, err := svc.RebuildTaskWorktree(r.Context(), kanban.MoveInput{
		RootID: rootID,
		TaskID: taskID,
		Reason: strings.TrimSpace(req.Reason),
	})
	// 重建失败（典型是分支还在、-b 报 branch already exists）已经把原因记到任务上了，
	// 卡片会渲染 session_error，所以这里回详情而不是裸 400 —— 前端一次刷新就能看到。
	if err != nil {
		respondJSON(w, http.StatusOK, detail)
		return
	}
	h.broadcastTaskUpdated(rootID, detail)
	respondJSON(w, http.StatusOK, detail)
}

// handleKanbanTaskFinishWorktree 收尾任务在 worktree 里的活：合回主 checkout →
// 拆 worktree → 删分支 → 列残留。
//
// 冲突走 409 而不是 400：合并撞上冲突是「需要人工处理」，不是「请求写错了」。
// body 里带 conflict_files 让前端能列出可点的文件清单 —— 冲突要人工解决这件事
// 藏在一句 error 字符串里等于没有。
func (h *HTTPHandler) handleKanbanTaskFinishWorktree(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	var req struct {
		RootID       string `json:"root_id"`
		Target       string `json:"target"`
		DeleteBranch bool   `json:"delete_branch"`
		PruneOrphans bool   `json:"prune_orphans"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req)
	rootID := strings.TrimSpace(req.RootID)
	taskID := strings.TrimSpace(chi.URLParam(r, "id"))
	if rootID == "" || taskID == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("root_id and id required"))
		return
	}
	result, err := svc.FinishTaskWorktree(r.Context(), kanban.FinishWorktreeInput{
		RootID:       rootID,
		TaskID:       taskID,
		Target:       strings.TrimSpace(req.Target),
		DeleteBranch: req.DeleteBranch,
		PruneOrphans: req.PruneOrphans,
	})
	// 合并已经成功、只是后面某步失败时（典型：worktree 里有未跟踪的 .mindfs/
	// 会话库，git worktree remove 拒绝），result 带着已完成的进度一起回去，
	// 前端能显示「合并已完成，卡在拆 worktree」而不是笼统一句失败。
	var conflict *kanban.FinishWorktreeConflict
	if errors.As(err, &conflict) {
		respondJSON(w, http.StatusConflict, map[string]any{
			"error":          conflict.Error(),
			"conflict_files": conflict.ConflictFiles,
			"output":         conflict.Output,
			"result":         result,
		})
		return
	}
	if err != nil {
		// 400 而不是 500：到这里的错是「root/task 找不到」「worktree 身份对不上」
		// 这类请求本身不成立，和同文件里 next/run-now 等动词一个口径（它们也是 400）。
		// 回 500 会让前端按「服务端炸了」处理，还顺带吐一个全零的 result 出去。
		respondJSON(w, http.StatusBadRequest, map[string]any{
			"error":  err.Error(),
			"result": result,
		})
		return
	}
	// 广播已经在 service 末尾做过（Runner.TaskUpdated → task.updated），这里不再重复。
	respondJSON(w, http.StatusOK, result)
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
	go h.bindTaskSessionNames(context.WithoutCancel(r.Context()), req.RootID, detail)
	respondJSON(w, http.StatusOK, detail)
}

// bindTaskSessionNames（任务→会话）：任务绑定到的所有 agent 会话统一改成
// 「任务名 / #编号」——和建会话那步（EnsureAgentSession）同一条派生，改名不丢后缀。
// 名字从 detail 现场派生，不接调用方的裸名入参。
// 单个会话改名失败（会话被删等）不影响其余，也不让任务改名本身失败。
func (h *HTTPHandler) bindTaskSessionNames(ctx context.Context, rootID string, detail kanban.TaskDetail) {
	if h == nil || h.AppContext == nil {
		return
	}
	name := kanban.TaskSessionName(detail.Task.Name, detail.Task.TaskNumber)
	if strings.TrimSpace(name) == "" {
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

// detachTaskFromSession（会话→任务）：会话被删除时，把任务里指向这些 key 的
// main_session_key / StageRun.session_key 清空并留痕，避免任务跳进空会话。
//
// 与 syncTaskNameFromSession 同一层同一形状：usecase 只管会话树，跨层协调放在 HTTP 层。
// keys 要传整棵被删子树的 key，不只是被点的那个 —— 子会话也可能绑着别的任务。
func (h *HTTPHandler) detachTaskFromSession(ctx context.Context, rootID string, keys []string) {
	if h == nil || h.AppContext == nil || len(keys) == 0 {
		return
	}
	svc, err := h.AppContext.GetKanbanService()
	if err != nil {
		return
	}
	detail, changed := svc.DetachFromSession(ctx, rootID, keys)
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
