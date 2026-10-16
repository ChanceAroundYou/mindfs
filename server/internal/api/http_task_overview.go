package api

import (
	"net/http"
	"strings"
	"time"

	"mindfs/server/internal/kanban"
)

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
