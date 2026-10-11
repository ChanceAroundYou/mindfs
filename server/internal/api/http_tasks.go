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
	// 三类「需要人处理」的失败走 409 并带上结构化清单，其余（root/task 找不到、
	// worktree 身份对不上）走 400。清单**不进句子** —— 那是 UI 列表渲染的输入，
	// 拼进 error 字符串的话三十个文件就是一句读不完的话。
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
	var dirty *kanban.FinishWorktreeDirty
	if errors.As(err, &dirty) {
		respondJSON(w, http.StatusConflict, map[string]any{
			"error":       dirty.Error(),
			"dirty_files": dirty.Files,
			"result":      result,
		})
		return
	}
	var userChanges *kanban.FinishWorktreeUserChanges
	if errors.As(err, &userChanges) {
		respondJSON(w, http.StatusConflict, map[string]any{
			"error":        userChanges.Error(),
			"user_changes": userChanges.Files,
			"result":       result,
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
// prompt_template 正文）；工作台组件真正读的只有 stages 的 role/kind/name，
// prompt_template 那部分**一个读取都没有** —— 所以 stages 保留、但只保留三个键。
//
// **worktree_* 不在这张淘汰名单里**：卡片右下角的 worktree 徽标就是靠
// create_worktree / worktree_path / worktree_built / worktree_missing 推出来的
// （TaskCardRows 的 worktreeTagState）。早先这里的注释断言「对 worktree_* 一个读取
// 都没有」是错的，投影照着它把四个字段全丢了 —— 症状是「工作台所有任务都显示没有
// worktree」（2026-10-07 修）。加字段时先确认前端真的不读，别照抄这份注释。
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
	// worktree 字段：前端 TaskCardRows 读取这些字段来显示 worktree 徽标。
	// 2026-10-07 修复：之前投影丢弃了这些字段，导致工作台所有任务都显示没有 worktree。
	CreateWorktree  bool   `json:"create_worktree,omitempty"`
	WorktreePath    string `json:"worktree_path,omitempty"`
	WorktreeBuilt   bool   `json:"worktree_built,omitempty"`
	WorktreeMissing bool   `json:"worktree_missing,omitempty"`
	// Stages 是**瘦身后的**流水快照：只带卡片真正读的 role / kind / name。
	//
	// 为什么不能整个丢（2026-10-07）：卡片的收尾键判据是「当前段是 worktree_finish 段」，
	// 而那个判据只能从 stages 来。原先投影丢掉 stages、改用派生布尔 has_agent_stage，
	// 结果判据退化了一层（「有 agent 段」≠「当前段是收尾段」），且同一个任务在项目看板
	// （完整任务）与工作台（投影）两边一个有收尾键一个没有。
	// 现在投影带上 stages，卡片直接按 `stages[current_stage_index].kind` 判，两边一致；
	// has_agent_stage 随之删除 —— 两份真相留着只会让下一个人猜以哪个为准。
	//
	// 不带 prompt_template：那正是 43% 的体积来源，而卡片零读取。
	Stages []overviewStageProjection `json:"stages,omitempty"`
	// AuxFlags 是卡片的辅助徽标来源（has_plan / has_todos / session_error / ask_user_waiting）。
	//
	// 指针 + omitempty：全空时整个键不出现。用值类型的话 kanban.TaskAuxFlags 的四个
	// 布尔都没有 omitempty，每条任务都会挂一个 {"ask_user_waiting":false,...} 的固定
	// 开销 —— 跨节点扇出下这是纯浪费，而且前端本来就读 `?? false`。
	AuxFlags *overviewAuxFlags `json:"aux_flags,omitempty"`
	// CurrentStageStatus 是当前段的执行状态，卡片用它区分「在跑」和「等你」。
	CurrentStageStatus string `json:"current_stage_status,omitempty"`
}

// overviewAuxFlags 是辅助徽标的卡片视图。
//
// 刻意不复用 kanban.TaskAuxFlags：那个结构体的布尔没有 omitempty（单任务详情与
// 项目看板共用，加 omitempty 会连带改变那两处的响应形状）。
type overviewAuxFlags struct {
	AskUserWaiting bool   `json:"ask_user_waiting,omitempty"`
	HasPlan        bool   `json:"has_plan,omitempty"`
	HasTodos       bool   `json:"has_todos,omitempty"`
	HasTask        bool   `json:"has_task,omitempty"`
	SessionError   string `json:"session_error,omitempty"`
}

// overviewStageProjection 是 stages 的卡片视图：只留卡片读得到的三个键。
//
// 刻意不复用 kanban.StageTemplate：那个结构体的 prompt_template 正文才是体积大头
// （实测 70KB / 45 条任务），给它加 omitempty 会连带改变单任务详情与项目看板的响应。
type overviewStageProjection struct {
	Name string `json:"name"`
	Role string `json:"role"`
	Kind string `json:"kind,omitempty"`
}

// projectOverviewTask 把 kanban.Task 压成工作台真正读的字段集。
// 刻意不在这里复用 kanban.Task：那个结构体是**所有**任务端点共用的，
// 给它加 omitempty 会连带改变单任务详情与项目看板的响应。
func projectOverviewTask(task kanban.Task) overviewTaskProjection {
	return overviewTaskProjection{
		ID:                 task.ID,
		TaskNumber:         task.TaskNumber,
		RootID:             task.RootID,
		Name:               task.Name,
		TaskTemplateID:     task.TaskTemplateID,
		TaskTemplateName:   task.TaskTemplateName,
		CurrentStageIndex:  task.CurrentStageIndex,
		CurrentStageName:   task.CurrentStageName,
		Status:             task.Status,
		MainSessionKey:     task.MainSessionKey,
		CreatedAt:          task.CreatedAt,
		UpdatedAt:          task.UpdatedAt,
		CompletedAt:        task.CompletedAt,
		CreateWorktree:     task.CreateWorktree,
		WorktreePath:       task.WorktreePath,
		WorktreeBuilt:      task.WorktreeBuilt,
		WorktreeMissing:    task.WorktreeMissingNow(),
		Stages:             projectOverviewStages(task.Stages),
		AuxFlags:           projectOverviewAuxFlags(task.AuxFlags),
		CurrentStageStatus: task.CurrentStageStatus,
	}
}

// projectOverviewAuxFlags 把辅助标记压成卡片视图；一个标记都没有时返回 nil
// （omitempty 让键整个消失，而不是留一个空对象）。
func projectOverviewAuxFlags(flags kanban.TaskAuxFlags) *overviewAuxFlags {
	if !flags.AskUserWaiting && !flags.HasPlan && !flags.HasTodos && !flags.HasTask &&
		strings.TrimSpace(flags.SessionError) == "" {
		return nil
	}
	return &overviewAuxFlags{
		AskUserWaiting: flags.AskUserWaiting,
		HasPlan:        flags.HasPlan,
		HasTodos:       flags.HasTodos,
		HasTask:        flags.HasTask,
		SessionError:   flags.SessionError,
	}
}

// projectOverviewStages 把流水快照瘦身成卡片真正读的三个键。
func projectOverviewStages(stages []kanban.StageTemplate) []overviewStageProjection {
	if len(stages) == 0 {
		return nil
	}
	out := make([]overviewStageProjection, 0, len(stages))
	for _, stage := range stages {
		out = append(out, overviewStageProjection{
			Name: stage.Name,
			Role: stage.Role,
			Kind: stage.Kind,
		})
	}
	return out
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

	// 收尾是「agent + 机械清场」两个阶段的整合，分流把两者编成一条幂等的流水：
	//
	//	① 执行体在动            → session_running（正常等待，不是错）
	//	② 机械清场能直接做完    → teardown（**机械优先**：不等 agent）
	//	②.5 机械清场没做成       → 落回 ③/④，交给 agent（不报错）
	//	③ 已有收尾段            → nudged（不追加第二段）
	//	④ 有 agent 段可继承     → stage_added（agent 先 commit + merge）
	//	④.5 没有 agent 段        → teardown（没有 agent 可继承）
	//
	// ② 的判定是**只读**的（PlanFinishWorktree）：worktree 里没有未提交的改动、
	// 合并不会冲突，就直接把机械清场做掉。以前只要有 agent 段就一定先跑 agent，
	// 于是「agent 早把活合完了、只差机械清场」的任务要白等一轮、还多一个空提交。
	//
	// ②.5 是「两个阶段整合」的兜底：机械那条路是**加速**，不是**门槛**。判定说能做
	// 但执行时被挡下（主 checkout 有改动、撞冲突、worktree 里还有没提交的活）时，
	// 不能把失败直接抛给用户 —— 那等于把整合退化成「机械不成 = 失败」，而 agent
	// 明明还能 commit、能解冲突、能判断哪些改动该留。所以这里落回 ③/④。

	// ① 执行体在动。收尾要拆掉它的 cwd，必须等它停 —— 但这是正常等待，
	// 不是失败，所以回 200 而不是 409：前端据此只提示一句，不渲染成红色报错。
	//
	// 判据是 TaskBusy（会话在回复，或当前段还在跑），不是只看会话探针：
	// 段在跑时只看探针会漏过去，让请求撞进收尾准入变成 409。
	if svc.TaskBusy(ctx, rootID, taskID) {
		respondJSON(w, http.StatusOK, map[string]any{"action": "session_running"})
		return
	}

	// ② 机械清场能直接做完 → 当场做掉。判定失败（读不到 git 状态等）不当成错误：
	// 退给 agent 那条路，让人去看一眼。
	plan, planErr := svc.PlanFinishWorktree(ctx, rootID, taskID)
	if planErr != nil {
		plan = kanban.FinishPlan{Mechanical: false, Reason: "收尾判定失败：" + planErr.Error()}
	}
	// teardownTried 记住「机械清场已经试过一次」，免得 ④.5 再试第二遍。
	teardownTried := false
	var teardownReport FinishTeardownReport
	if plan.Mechanical {
		teardownTried = true
		done, report := h.teardownFinishWorktree(w, rootID, taskID, plan)
		if done {
			return
		}
		// ②.5 没做成 → 换一条路，不报错。
		teardownReport = report
		plan = agentFallbackPlan(report)
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
			"plan":   plan,
			"detail": detail,
			"task":   detail.Task,
		})
		return
	}

	// ④ 有 agent 段可继承 → 追加收尾段，让 agent 自己 commit + merge。
	// ④.5 没有 agent 段 → 没有 agent 可继承，只能靠机械清场。
	hasAgent, hasAgentErr := svc.TaskHasAgentStage(ctx, rootID, taskID)
	if hasAgentErr != nil {
		respondError(w, http.StatusBadRequest, hasAgentErr)
		return
	}
	if !hasAgent {
		// ② 已经试过一遍就不再试：重复跑清场会把同一段 git 操作做两次。
		if teardownTried {
			h.respondFinishTeardownFailure(w, plan, teardownReport)
			return
		}
		if done, report := h.teardownFinishWorktree(w, rootID, taskID, plan); !done {
			h.respondFinishTeardownFailure(w, plan, report)
		}
		return
	}
	detail, err := svc.BeginFinishWorktree(ctx, kanban.BeginFinishInput{
		RootID: rootID,
		TaskID: taskID,
	})
	// 409 而不是 400：这里的每一条错都是「仓库/任务正处在某个需要人处理的状态」
	// —— 正在跑、已在收尾、worktree 目录已失效。请求本身是合法的，是**状态**不答应，
	// 和 finish-worktree 把冲突也归 409 是同一个口径。
	if err != nil {
		// agent 这条路也走不通 —— 两条路都断了，只能让人来决策。
		//
		// **主 error 用 agent 那条**：它是最后失败的一步，也是「任务此刻为什么不答应」
		// 的直接答案。反过来把机械清场的失败当主 error 会误导：那份报告带着
		// user_changes / dirty_files / conflict_files，前端按「哪个清单非空」挑弹窗，
		// 于是渲染成「合并已成功，但 worktree 里还有没提交的东西」+ 一长串清单，
		// 而真正的拦路虎根本不是那个（2026-10-08 实测 task-9：真原因是当前 agent 段
		// 停在 waiting_user，机械那份失败只是前因）。
		//
		// 所以机械那份只留在 report 里当上下文，**不把它的文件清单提到顶层** ——
		// 提到顶层就等于让前端挑错弹窗。这条口径与「清单进弹窗、句子保持短」一致，
		// 只是这里连弹窗都不该有：能动手处理的原因是一句话，不是一列文件。
		if teardownTried {
			respondJSON(w, http.StatusConflict, map[string]any{
				"error":  err.Error(),
				"plan":   plan,
				"report": teardownReport,
			})
			return
		}
		respondJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "plan": plan})
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"action": "stage_added",
		"plan":   plan,
		"detail": detail,
		"task":   detail.Task,
	})
}

// teardownFinishWorktree 当场做机械清场。
//
// 做成了：广播 task.finish_teardown + 回 200，返回 done=true。
// 没做成：**什么都不写、什么都不播**，返回 done=false 和那份 report —— 调用方要
// 把这次失败落回 agent 那条路（见 agentFallbackPlan）。机械清场是「两个阶段整合」
// 里的加速段，不是门槛：它没成，结论不是「收尾失败」，而是「让 agent 去做」。
// 在这里就把 409 抛出去的话，用户拿到的是一句他无从下手的报错，而机器其实还有
// 一条路没走（2026-10-08 用户实测：「不应该报错，而是应该把 comment 提交给 agent，
// 进入 agent 合并流程」）。
func (h *HTTPHandler) teardownFinishWorktree(w http.ResponseWriter, rootID, taskID string, plan kanban.FinishPlan) (bool, FinishTeardownReport) {
	report := h.AppContext.FinishWorktreeAndRepoint(rootID, taskID)
	if report.Error != "" {
		return false, report
	}
	h.AppContext.BroadcastTaskFinishTeardown(rootID, report)
	respondJSON(w, http.StatusOK, map[string]any{
		"action": "teardown",
		"plan":   plan,
		"report": report,
	})
	return true, report
}

// respondFinishTeardownFailure 在**没有 agent 可接手**时，把清场失败如实回给调用方。
//
// 走到这里说明两条路都断了：机械清场做不了，而且任务里没有 agent 段可供收尾段继承
// （BeginFinishWorktree 会拒绝「没有 agent 阶段」）。这时只能让人来决策，所以回
// 409 + 结构化清单 —— 清单**不进句子**，它是 UI 列表渲染的输入。
// 失败**不广播**：这次调用有 HTTP 响应可带结论，WS 那条通道留给异步路径
// （收尾段跑完后的钩子），否则点击者会连着同一个 hub 收到两遍。
func (h *HTTPHandler) respondFinishTeardownFailure(w http.ResponseWriter, plan kanban.FinishPlan, report FinishTeardownReport) {
	respondJSON(w, http.StatusConflict, map[string]any{
		"action":         "teardown",
		"plan":           plan,
		"report":         report,
		"error":          report.Error,
		"conflict_files": report.ConflictFiles,
		"output":         report.Output,
		"dirty_files":    report.DirtyFiles,
		"user_changes":   report.UserChanges,
	})
}

// agentFallbackPlan 把一次失败的机械清场翻译成「交给 agent」的判定。
//
// 这是「agent + 机械合并两个阶段整合」的关键一步：机械那条路能省掉一轮 agent，
// 省不掉的时候就得把活交出去 —— agent 能 commit、能解冲突、能判断哪些改动该留。
// 只把原因收进一句人话 + 一份文件清单：前端会把它们拼成
// 「收尾已发起：agent 正在提交并合并 — 涉及文件：… — <原因>」。
func agentFallbackPlan(report FinishTeardownReport) kanban.FinishPlan {
	// 三个清单在实现里互斥（一次清场只会因为一个原因停），所以取第一个非空的。
	// 原因写在这里而不是直接搬 report.Error：那些句子是**对用户说的**
	// （「先提交或暂存后再收尾」），拼进「已交给 agent」里会自相矛盾。
	cause := strings.TrimSpace(report.Error)
	var files []string
	switch {
	case len(report.ConflictFiles) > 0:
		cause, files = "合并会冲突", report.ConflictFiles
	case len(report.DirtyFiles) > 0:
		cause, files = "主 checkout 有未提交改动", report.DirtyFiles
	case len(report.UserChanges) > 0:
		cause, files = "worktree 里还有没提交的改动", report.UserChanges
	}
	return kanban.FinishPlan{
		Mechanical: false,
		Reason:     "机械合并没能直接做完（" + cause + "）",
		Files:      files,
	}
}
