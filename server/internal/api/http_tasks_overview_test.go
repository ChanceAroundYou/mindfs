package api

import (
	"encoding/json"
	"testing"
	"time"

	"mindfs/server/internal/kanban"
)

// /api/tasks/overview 的投影守卫。
//
// 这条测试钉住的是**体积大头必须不在、工作台读的字段必须全在**：
// 实测 45 条任务 = 163KB，其中 task.stages 一项占 43%（70KB 是每个阶段的完整
// prompt_template 正文），而工作台组件对 stages / aux_flags / labels / worktree_*
// 一个读取都没有 —— 卡片只画状态、阶段名、任务号、模板名。
//
// 丢字段的症状是「卡片少字段」而不是报错，所以靠断言而不是靠运行时反馈来守：
// 有人把投影改成 `items` 原样下发，这条会红。
func TestOverviewProjectionKeepsBoardFieldsAndDropsStages(t *testing.T) {
	task := kanban.Task{
		ID:                "task_9",
		TaskNumber:        9,
		RootID:            "mindfs",
		Name:              "改前端",
		TaskTemplateID:    "tpl_1",
		TaskTemplateName:  "默认模板",
		CurrentStageIndex: 1,
		CurrentStageName:  "修 bug",
		Status:            "running",
		MainSessionKey:    "1791109592-9f9b6b12c4f1",
		CreatedAt:         time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:         time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC),
		CompletedAt:       "",
		Stages: []kanban.StageTemplate{
			{Name: "任务输入", Role: "user", PromptTemplate: "做：{previous_input}"},
			{Name: "执行", Role: "agent", PromptTemplate: "执行一段很长的正文，用来撑大体积以便断言它不该出现在响应里"},
		},
		Labels:             []string{"wip", "p1"},
		CreateWorktree:     true,
		WorktreeBranchMode: "new",
		WorktreeBuilt:      true,
		WorktreeMissing:    false,
		CurrentStageStatus: "running",
	}

	raw, err := json.Marshal(projectOverviewTask(task))
	if err != nil {
		t.Fatalf("marshal projection: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal projection: %v", err)
	}

	// ① 工作台**实际读取**的字段一个都不能丢（丢一个就是卡片少字段）。
	//    这份清单是 grep `web/src/app/useWorkspaceBoard.ts` 与 `components/workspace/*` 的结果。
	for _, key := range []string{
		"id", "task_number", "root_id", "task_template_name",
		"current_stage_index", "current_stage_name", "status",
		"main_session_key", "updated_at",
	} {
		if _, ok := got[key]; !ok {
			t.Fatalf("工作台读的字段 %q 被投影丢掉了：%s", key, raw)
		}
	}

	// ② 值要真的带过去，不能只留个键。
	if got["main_session_key"] != "1791109592-9f9b6b12c4f1" {
		t.Fatalf("main_session_key 丢了值：%v", got["main_session_key"])
	}
	if got["current_stage_name"] != "修 bug" {
		t.Fatalf("current_stage_name 丢了值：%v", got["current_stage_name"])
	}
	if got["task_template_name"] != "默认模板" {
		t.Fatalf("task_template_name 丢了值：%v", got["task_template_name"])
	}
	if got["status"] != "running" || got["id"] != "task_9" {
		t.Fatalf("status/id 丢了值：%v", got)
	}

	// ③ 体积大头与前端零读取的字段必须不在响应里。
	//    stages 就是那 70KB / 43%。
	for _, dropped := range []string{
		"stages", "aux_flags", "labels", "create_worktree",
		"worktree_branch_mode", "worktree_built", "worktree_missing",
		"current_stage_status", "worktree_path", "worktree_root_id", "worktree_branch",
	} {
		if _, ok := got[dropped]; ok {
			t.Fatalf("%q 不该出现在 overview 响应里 —— 它是体积大头且前端零读取", dropped)
		}
	}

	// ④ 投影必须真的更小，不是只是少几个键。
	full, err := json.Marshal(kanban.TaskOverviewItem{
		RootID:   "mindfs",
		RootName: "mindfs",
		Task:     task,
	})
	if err != nil {
		t.Fatalf("marshal full: %v", err)
	}
	projItem, err := json.Marshal(map[string]any{
		"root_id": "mindfs", "root_name": "mindfs", "task": projectOverviewTask(task),
	})
	if err != nil {
		t.Fatalf("marshal projected item: %v", err)
	}
	if len(projItem)*2 >= len(full) {
		t.Fatalf("投影只从 %dB 压到 %dB —— 至少要砍掉一半，否则这次投影不值当", len(full), len(projItem))
	}
	t.Logf("单条：full=%dB  projected=%dB（%.0f%%）", len(full), len(projItem), 100*float64(len(projItem))/float64(len(full)))
}
