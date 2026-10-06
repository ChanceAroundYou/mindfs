package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

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
	// root 缺省 = 只看全局模板。模板库只有主节点一份（docs/multi-node-control-plane.md），
	// 前端建任务时总带着当前项目，所以这里按项目过滤而不是全量返回。
	items, err := svc.ListTaskTemplatesForRoot(r.Context(), r.URL.Query().Get("root"))
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSONConditional(w, r, map[string]any{"items": items})
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
	// 看板列表：任务自带 stage_runs / events 流水，载荷随任务数线性增长（实测单任务带
	// 三段流水就 53KB），**不能**套 respondJSONList 瘦身 —— 详情面板直接读这里的
	// task.stages，少一个键就是少一段流水。只做协商。
	respondJSONConditional(w, r, map[string]any{"items": items})
}

func (h *HTTPHandler) handleKanbanTaskCreate(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	var req struct {
		RootID             string                  `json:"root_id"`
		TaskTemplateID     string                  `json:"task_template_id"`
		TaskTemplateName   string                  `json:"task_template_name"`
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
		TaskTemplateName:   req.TaskTemplateName,
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

// handleKanbanTaskDelete 删掉一张任务卡（终态任务的「删除」按钮）。
//
// 与「取消」是两回事：取消只改状态，卡片还留在板上；删除把它从板上拿走。
// 只删卡片 —— worktree / 分支 / 会话都不动。
//
// root_id 走 query 而不是 body：DELETE 带 body 不是所有中间层都转发，
// 而这个接口只吃一个 id 参数，query 更省事也更直白。
func (h *HTTPHandler) handleKanbanTaskDelete(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	rootID := strings.TrimSpace(r.URL.Query().Get("root_id"))
	taskID := strings.TrimSpace(chi.URLParam(r, "id"))
	if rootID == "" || taskID == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("root_id and id required"))
		return
	}
	if err := svc.DeleteTask(r.Context(), kanban.MoveInput{RootID: rootID, TaskID: taskID}); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		respondError(w, status, err)
		return
	}
	// 广播给所有客户端：卡片「还在不在」是结构问题，就地更新那条路径看不见它。
	if h.AppContext != nil {
		h.AppContext.TaskDeleted(rootID, taskID)
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
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

// overviewTaskProjection 是 `/api/tasks/overview` 的**卡片视图**投影。
//
// 这个端点是跨节点扇出的（工作台常驻、每节点一份），体积直接决定手机端流量。
// 实测 45 条任务 = 163KB，其中 task.stages 一项就占 43%（70KB 是每个阶段的完整
// prompt_template 正文）；而工作台组件对 stages / aux_flags / labels / worktree_*
// 这些字段**一个读取都没有** —— 卡片只画状态、阶段名、任务号、模板名。
//
// 字段名与 kanban.Task 保持一致，只是不下发：
//   - 前端 KanbanTask.stages 本就是可选的（`stages?: StageTemplate[]`），类型不受影响；
//   - 要某个任务的完整流水走 GET /api/tasks/{root}/{id}（单任务详情，本就带）。
//
// 淘汰的字段都列在 G-AN 的清单里；丢它们的症状是工作台卡片少字段，而不是报错 ——
// 所以测试要钉「投影后工作台读的字段必须全在」，见 http_tasks_overview_test.go。
type overviewTaskProjection struct {
	ID                string    `json:"id"`
	TaskNumber        int       `json:"task_number,omitempty"`
	RootID            string    `json:"root_id"`
	Name              string    `json:"name,omitempty"`
	TaskTemplateID    string    `json:"task_template_id,omitempty"`
	TaskTemplateName  string    `json:"task_template_name,omitempty"`
	CurrentStageIndex int       `json:"current_stage_index"`
	CurrentStageName  string    `json:"current_stage_name,omitempty"`
	Status            string    `json:"status"`
	MainSessionKey    string    `json:"main_session_key,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	CompletedAt       string    `json:"completed_at,omitempty"`
}

// projectOverviewTask 把 kanban.Task 压成工作台真正读的字段集。
// 刻意不在这里复用 kanban.Task：那个结构体是**所有**任务端点共用的，
// 给它加 omitempty 会连带改变单任务详情与项目看板的响应。
func projectOverviewTask(task kanban.Task) overviewTaskProjection {
	return overviewTaskProjection{
		ID:                task.ID,
		TaskNumber:        task.TaskNumber,
		RootID:            task.RootID,
		Name:              task.Name,
		TaskTemplateID:    task.TaskTemplateID,
		TaskTemplateName:  task.TaskTemplateName,
		CurrentStageIndex: task.CurrentStageIndex,
		CurrentStageName:  task.CurrentStageName,
		Status:            task.Status,
		MainSessionKey:    task.MainSessionKey,
		CreatedAt:         task.CreatedAt,
		UpdatedAt:         task.UpdatedAt,
		CompletedAt:       task.CompletedAt,
	}
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
	// 投影在响应边界做：不改 kanban.Task 本身，也不改单任务详情与项目看板的形状。
	projected := make([]map[string]any, 0, len(items))
	for _, item := range items {
		projected = append(projected, map[string]any{
			"root_id":   item.RootID,
			"root_name": item.RootName,
			"task":      projectOverviewTask(item.Task),
		})
	}
	respondJSONList(w, r, map[string]any{"items": projected})
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
	// 转发给 AppContext.TaskUpdated 而不是就地拼 payload：同一个事件类型曾经有两种
	// 形状（这里只有 task，AppContext 那边还带 detail），前端因此要在两处分别兜。
	// 少带 detail 的那次推送会让详情面板拿不到 stage_runs/events。
	h.AppContext.TaskUpdated(rootID, detail)
}

// handleKanbanTaskBeginFinish 收尾按钮的唯一入口：按**真实状态**决定这次点击该做什么。
//
// 四个分支，判据全是「此刻的客观事实」，与任务处于哪个列、什么 status 无关：
//
//	① 会话正在回复        → 什么都不做，回 session_running（收尾本来就要等它停，不是错误）
//	② 分支已合进主干      → 直接机械清场（agent 那半已经做完，再跑一遍只会多一个空提交）
//	③ 已有收尾段但还没成  → 重跑那段（nudge），不追加第二段
//	④ 其余                → 追加收尾段并跑起来（第一次收尾）
//
// 为什么必须这么分：以前这里无条件调 BeginFinishWorktree，于是「agent 已经把活合完了、
// 只差机械清场」的任务会撞上「该任务已在收尾流程中」被 409 顶回来，界面上表现为收尾
// 一直转、点不动、也结束不了。②③ 都是幂等的 —— 连点几次不会多出提交或多出收尾段。
//
// 刻意放在文件末尾：worktree-finish.test.mjs 按
// 「handleKanbanTaskFinishWorktree → handleKanbanTaskMove」切片断言那个 handler 的
// 函数体（整文件跑正则会因为 `[\s\S]*?` 一路跳到文件后面而恒真）。插在中间会让
// 新 handler 的内容落进那个切片里。
func (h *HTTPHandler) handleKanbanTaskBeginFinish(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	var req struct {
		RootID string `json:"root_id"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req)
	rootID := strings.TrimSpace(req.RootID)
	taskID := strings.TrimSpace(chi.URLParam(r, "id"))
	if rootID == "" || taskID == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("root_id and id required"))
		return
	}
	ctx := r.Context()

	// ① agent 此刻在动。收尾要拆掉它的 cwd，必须等它停 —— 但这是正常等待，
	// 不是失败，所以回 200 而不是 409：前端据此只提示一句，不渲染成红色报错。
	if svc.TaskSessionRunning(ctx, rootID, taskID) {
		respondJSON(w, http.StatusOK, map[string]any{"action": "session_running"})
		return
	}

	// ② 分支已经在主干里 → 只剩机械清场。查不到分支（从没建过树）就往下走正常流程。
	if merged, err := svc.TaskWorktreeBranchMerged(ctx, rootID, taskID, ""); err == nil && merged {
		report := h.AppContext.FinishWorktreeAndRepoint(rootID, taskID)
		h.AppContext.BroadcastTaskFinishTeardown(rootID, report)
		payload := map[string]any{
			"action": "teardown",
			"report": report,
		}
		if report.Error != "" {
			// 清场失败（典型：worktree 里还有没提交的活、或合并撞冲突）得让用户看见，
			// 回 200 会让前端以为收尾完成了。冲突文件一并带上，前端能列出可点的清单。
			payload["error"] = report.Error
			payload["conflict_files"] = report.ConflictFiles
			respondJSON(w, http.StatusConflict, payload)
			return
		}
		respondJSON(w, http.StatusOK, payload)
		return
	}

	// ③ 收尾段已经在流水里但还没跑成 → 重跑它。不追加第二段：那会堆出一串收尾段。
	if index, exists, err := svc.TaskFinishStageIndex(ctx, rootID, taskID); err == nil && exists {
		detail, rerunErr := svc.RerunStage(ctx, kanban.MoveInput{
			RootID:     rootID,
			TaskID:     taskID,
			StageIndex: index,
		})
		if rerunErr != nil {
			respondJSON(w, http.StatusConflict, map[string]any{"error": rerunErr.Error()})
			return
		}
		respondJSON(w, http.StatusOK, map[string]any{
			"action": "nudged",
			"detail": detail,
			"task":   detail.Task,
		})
		return
	}

	// ④ 头一次收尾：追加收尾段，让 agent 自己 commit + merge。
	detail, err := svc.BeginFinishWorktree(ctx, kanban.BeginFinishInput{
		RootID: rootID,
		TaskID: taskID,
	})
	// 409 而不是 400：这里的每一条错都是「仓库/任务正处在某个需要人处理的状态」
	// —— 正在跑、已在收尾、worktree 目录已失效。请求本身是合法的，是**状态**不答应，
	// 和 finish-worktree 把冲突也归 409 是同一个口径。
	if err != nil {
		respondJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"action": "stage_added",
		"detail": detail,
		"task":   detail.Task,
	})
}
