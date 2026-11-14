package api

import (
	"encoding/json"
	"testing"
	"time"

	"mindfs/server/internal/kanban"
)

// /api/tasks/overview 的投影守卫。
//
// 这条测试钉住的是**工作台真正读的字段必须全在、体积字段必须不在**。
// 任务没有阶段字段（2026-11 决策），投影也不再带 stages / current_stage_*。
//
// worktree 字段（create_worktree / worktree_path / worktree_built / worktree_missing）
// 是**例外**：前端 TaskCardRows 读取这些字段来显示 worktree 徽标。
// 2026-10-07 修复：之前投影丢弃了这些字段，导致工作台所有任务都显示没有 worktree。
//
// 丢字段的症状是「卡片少字段」而不是报错，所以靠断言而不是靠运行时反馈来守：
// 有人把投影改成 `items` 原样下发，这条会红。
func TestOverviewProjectionKeepsBoardFieldsAndDropsPromptBodies(t *testing.T) {
	task := kanban.Task{
		ID:               "task_9",
		TaskNumber:       9,
		RootID:           "mindfs",
		Name:             "改前端",
		TaskTemplateID:   "tpl_1",
		TaskTemplateName: "默认模板",
		Status:           "running",
		MainSessionKey:   "1791109592-9f9b6b12c4f1",
		CreatedAt:        time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:        time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC),
		CompletedAt:      "", Labels: []string{"wip", "p1"},
		CreateWorktree:     true,
		WorktreeBranchMode: "new",
		WorktreeBuilt:      true,
		WorktreePath:       "/tmp/worktree/task_9",
		WorktreeMissing:    false,
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
		"id", "task_number", "root_id", "task_template_name", "status",
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
	if got["task_template_name"] != "默认模板" {
		t.Fatalf("task_template_name 丢了值：%v", got["task_template_name"])
	}
	if got["status"] != "running" || got["id"] != "task_9" {
		t.Fatalf("status/id 丢了值：%v", got)
	}

	// ③ 前端零读取的字段必须不在响应里。
	for _, dropped := range []string{
		"labels",
		"worktree_branch_mode", "worktree_branch", "worktree_root_id",
		"stages", "current_stage_index", "current_stage_name", "current_stage_status",
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

	// ⑤ aux_flags 必须在：它是卡片徽标的来源。
	auxFlags, ok := got["aux_flags"].(map[string]any)
	if !ok {
		t.Fatalf("aux_flags 必须在：%v", got["aux_flags"])
	}
	if auxFlags["has_plan"] != true || auxFlags["has_todos"] != true {
		t.Fatalf("aux_flags 丢了值：%v", auxFlags)
	}

}
