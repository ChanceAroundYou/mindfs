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
	ID               string `json:"id"`
	TaskNumber       int    `json:"task_number,omitempty"`
	RootID           string `json:"root_id"`
	Name             string `json:"name,omitempty"`
	TaskTemplateID   string `json:"task_template_id,omitempty"`
	TaskTemplateName string `json:"task_template_name,omitempty"`
	Status           string `json:"status"`
	MainSessionKey   string `json:"main_session_key,omitempty"`
	// ResultSummary 是共享卡片上的「这一轮结果」。没有它，工作台卡片只能显示
	// 任务名，任务列表和对话开头看不出区别。
	ResultSummary string    `json:"result_summary,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	CompletedAt   string    `json:"completed_at,omitempty"`
	// ArchivedAt 共享卡片读它来决定给「归档」还是「取消归档」键（#86）。
	// 工作台当前在 SQL 层就把归档任务滤掉了，所以这个键事实上恒为空；留着是因为
	// 卡片读什么、投影带什么必须一一对应（见 http_tasks_overview_card_parity_test.go），
	// 哪天放宽过滤也不会静默降级。
	ArchivedAt string `json:"archived_at,omitempty"`
	// worktree 字段：前端 TaskCardRows 读取这些字段来显示 worktree 徽标。
	// 2026-10-07 修复：之前投影丢弃了这些字段，导致工作台所有任务都显示没有 worktree。
	CreateWorktree  bool   `json:"create_worktree,omitempty"`
	WorktreePath    string `json:"worktree_path,omitempty"`
	WorktreeBuilt   bool   `json:"worktree_built,omitempty"`
	WorktreeMissing bool   `json:"worktree_missing,omitempty"`
	// AuxFlags 是卡片的辅助徽标来源（has_plan / has_todos / session_error / ask_user_waiting）。
	//
	// 指针 + omitempty：全空时整个键不出现。用值类型的话 kanban.TaskAuxFlags 的四个
	// 布尔都没有 omitempty，每条任务都会挂一个 {"ask_user_waiting":false,...} 的固定
	// 开销 —— 跨节点扇出下这是纯浪费，而且前端本来就读 `?? false`。
	AuxFlags *overviewAuxFlags `json:"aux_flags,omitempty"`
	// RouterAsk / LatestAttention 是卡片上的两类 Router 来源标记：
	// Router 自己派发的提问（与 aux_flags.ask_user_waiting 分源）和最新一条
	// 分级提醒。派生自 activity 行，随列表一起下发，否则工作台卡片会少标记。
	RouterAsk       *overviewRouterAsk       `json:"router_ask,omitempty"`
	LatestAttention *overviewLatestAttention `json:"latest_attention,omitempty"`
}

type overviewRouterAsk struct {
	Count    int    `json:"count"`
	Since    string `json:"since,omitempty"`
	Question string `json:"question,omitempty"`
}

type overviewLatestAttention struct {
	Body      string `json:"body"`
	Severity  string `json:"severity,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
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

// projectOverviewTask 把 kanban.Task 压成工作台真正读的字段集。
// 刻意不在这里复用 kanban.Task：那个结构体是**所有**任务端点共用的，
// 给它加 omitempty 会连带改变单任务详情与项目看板的响应。
func projectOverviewTask(task kanban.Task) overviewTaskProjection {
	return overviewTaskProjection{
		ID:               task.ID,
		TaskNumber:       task.TaskNumber,
		RootID:           task.RootID,
		Name:             task.Name,
		TaskTemplateID:   task.TaskTemplateID,
		TaskTemplateName: task.TaskTemplateName,
		Status:           task.Status,
		MainSessionKey:   task.MainSessionKey,
		ResultSummary:    task.ResultSummary,
		CreatedAt:        task.CreatedAt,
		UpdatedAt:        task.UpdatedAt,
		CompletedAt:      task.CompletedAt,
		ArchivedAt:       task.ArchivedAt,
		CreateWorktree:   task.CreateWorktree,
		WorktreePath:     task.WorktreePath,
		WorktreeBuilt:    task.WorktreeBuilt,
		WorktreeMissing:  task.WorktreeMissingNow(),
		AuxFlags:         projectOverviewAuxFlags(task.AuxFlags),
		RouterAsk:        projectOverviewRouterAsk(task.RouterAsk),
		LatestAttention:  projectOverviewLatestAttention(task.LatestAttention),
	}
}

func projectOverviewRouterAsk(ask *kanban.TaskRouterAsk) *overviewRouterAsk {
	if ask == nil {
		return nil
	}
	return &overviewRouterAsk{Count: ask.Count, Since: ask.Since, Question: ask.Question}
}

func projectOverviewLatestAttention(attention *kanban.TaskLatestAttention) *overviewLatestAttention {
	if attention == nil {
		return nil
	}
	return &overviewLatestAttention{Body: attention.Body, Severity: attention.Severity, CreatedAt: attention.CreatedAt}
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
