package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"mindfs/server/internal/kanban"
)

// /api/tasks/overview 的投影守卫。
//
// 这条测试钉住的是**体积大头必须不在、工作台读的字段必须全在**：
// 实测 45 条任务 = 163KB，其中每个阶段的 prompt_template 正文就占 43%（70KB）。
//
// 但 stages 本身**不能丢**（2026-10-07 修）：卡片的收尾键判据是「当前段是
// worktree_finish 段」，只能从 stages 来。原先投影丢掉 stages、改用派生布尔
// has_agent_stage，判据退化了一层，且工作台与项目看板同一个任务一个有收尾键一个没有。
// 现在 stages 保留、但只保留卡片读得到的 name / role / kind —— prompt_template 不进投影。
//
// worktree 字段（create_worktree / worktree_path / worktree_built / worktree_missing）
// 同样是**例外**：前端 TaskCardRows 读取这些字段来显示 worktree 徽标。
// 2026-10-07 修复：之前投影丢弃了这些字段，导致工作台所有任务都显示没有 worktree。
//
// 丢字段的症状是「卡片少字段」而不是报错，所以靠断言而不是靠运行时反馈来守：
// 有人把投影改成 `items` 原样下发，这条会红。
func TestOverviewProjectionKeepsBoardFieldsAndDropsPromptBodies(t *testing.T) {
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
			// 正文长度照着真实模板的量级来（实测每段几百字到几千字）——太短的话
			// 「投影至少砍一半」这条会变成在测 JSON 的固定开销，而不是在测正文体积。
			{Name: "执行", Role: "agent", Kind: kanban.StageKindWorktreeFinish, PromptTemplate: strings.Repeat("执行一段很长的正文，用来撑大体积以便断言它不该出现在响应里。", 30)},
		},
		Labels:             []string{"wip", "p1"},
		CreateWorktree:     true,
		WorktreeBranchMode: "new",
		WorktreeBuilt:      true,
		WorktreePath:       "/tmp/worktree/task_9",
		WorktreeMissing:    false,
		CurrentStageStatus: "running",
		AuxFlags: kanban.TaskAuxFlags{
			AskUserWaiting: true,
			HasPlan:        true,
			HasTodos:       true,
			SessionError:   `{"message":"transport is closed"}`,
		},
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
		"labels",
		"worktree_branch_mode", "worktree_branch", "worktree_root_id",
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

	// ⑤ stages 必须在，且每段只带卡片读得到的三个键 —— 收尾键的判据
	//    （`stages[current_stage_index].kind === "worktree_finish"`）只能从它来。
	//    2026-10-07：原先投影丢掉 stages、改用派生布尔 has_agent_stage，判据因此
	//    退化了一层，且工作台与项目看板同一个任务一个有收尾键一个没有。
	stages, ok := got["stages"].([]any)
	if !ok || len(stages) != 2 {
		t.Fatalf("stages 必须在且带两段：%v", got["stages"])
	}
	second, ok := stages[1].(map[string]any)
	if !ok {
		t.Fatalf("stages[1] 不是对象：%v", stages[1])
	}
	if second["kind"] != kanban.StageKindWorktreeFinish {
		t.Fatalf("stages[1].kind = %v, want %q —— 卡片靠它认收尾段",
			second["kind"], kanban.StageKindWorktreeFinish)
	}
	// 每段只留 name / role / kind，prompt_template 那 43% 体积绝不能回来。
	for i, rawStage := range stages {
		stage := rawStage.(map[string]any)
		for key := range stage {
			switch key {
			case "name", "role", "kind":
			default:
				t.Fatalf("stages[%d] 多带了 %q —— 卡片零读取，且 prompt_template 是体积大头：%v", i, key, stage)
			}
		}
	}

	// ⑥ aux_flags 与 current_stage_status 必须在：前者是徽标来源，后者区分「在跑 / 等你」。
	auxFlags, ok := got["aux_flags"].(map[string]any)
	if !ok {
		t.Fatalf("aux_flags 必须在：%v", got["aux_flags"])
	}
	if auxFlags["has_plan"] != true || auxFlags["has_todos"] != true {
		t.Fatalf("aux_flags 丢了值：%v", auxFlags)
	}
	if got["current_stage_status"] != "running" {
		t.Fatalf("current_stage_status = %v, want running", got["current_stage_status"])
	}

	// ⑦ 没有段的任务投影成空 stages（omitempty 缺席），卡片读 `?.length` 得 0。
	noStages := projectOverviewTask(kanban.Task{ID: "task_10", RootID: "mindfs"})
	if len(noStages.Stages) != 0 {
		t.Fatalf("没有段的任务不该投影出 stages：%+v", noStages.Stages)
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
