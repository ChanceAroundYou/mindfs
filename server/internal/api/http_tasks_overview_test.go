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
// prompt_template 正文），而工作台组件对 stages / aux_flags / labels 这些字段
// 一个读取都没有 —— 卡片只画状态、阶段名、任务号、模板名。
//
// 但 worktree 字段（create_worktree / worktree_path / worktree_built / worktree_missing）
// 是**例外**：前端 TaskCardRows 读取这些字段来显示 worktree 徽标。
// 2026-10-07 修复：之前投影丢弃了这些字段，导致工作台所有任务都显示没有 worktree。
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
		WorktreePath:       "/tmp/worktree/task_9",
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
		"stages", "aux_flags", "labels",
		"worktree_branch_mode", "worktree_branch",
		"current_stage_status", "worktree_root_id",
	} {
		if _, ok := got[dropped]; ok {
			t.Fatalf("%q 不该出现在 overview 响应里 —— 它是体积大头且前端零读取", dropped)
		}
	}

	// ④ worktree 字段必须**在**响应里 —— 前端 TaskCardRows 读取这些字段来显示 worktree 徽标。
	//    2026-10-07 修复：之前投影丢弃了这些字段，导致工作台所有任务都显示没有 worktree。
	for _, key := range []string{
		"create_worktree", "worktree_path", "worktree_built", "worktree_missing",
	} {
		if _, ok := got[key]; !ok {
			t.Fatalf("worktree 字段 %q 被投影丢掉了：%s", key, raw)
		}
	}
	if got["create_worktree"] != true {
		t.Fatalf("create_worktree 丢了值：%v", got["create_worktree"])
	}
	if got["worktree_built"] != true {
		t.Fatalf("worktree_built 丢了值：%v", got["worktree_built"])
	}

	// ⑤ has_agent_stage 必须在且为 true —— 它是 stages 被丢掉之后前端收尾键唯一的判据。
	//    少它 = 工作台任务的收尾键永远不出现（stages 缺席 ⇒ 判据恒假）。
	if got["has_agent_stage"] != true {
		t.Fatalf("has_agent_stage 必须是 true（任务里有 role=agent 的段）：%v", got["has_agent_stage"])
	}

	// ⑥ 没有 agent 段的任务必须投影成 false，不能因为 omitempty 缺席而让前端误判。
	//    （缺席时前端读 `=== true` 得 false，与这里的 false 等价；但显式钉住值本身。）
	noAgent := projectOverviewTask(kanban.Task{
		ID:     "task_10",
		RootID: "mindfs",
		Stages: []kanban.StageTemplate{{Name: "任务输入", Role: "user"}},
	})
	if noAgent.HasAgentStage {
		t.Fatal("只有 user 段的任务不该判成有 agent 段")
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
