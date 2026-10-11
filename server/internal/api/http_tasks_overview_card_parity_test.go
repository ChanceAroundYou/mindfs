package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"mindfs/server/internal/kanban"
)

// 投影 ↔ 共享卡片的**字段级**守卫。
//
// 为什么需要它：`http_tasks_overview_test.go` 钉的是「体积大头不在 + 几个关键字段在」，
// 但那份「关键字段」清单是**手抄**的，而手抄清单正是 2026-10-07 那次漂移的成因 ——
// 工作台与项目看板引用同一个组件 `TaskCardRows.tsx`，投影却按「工作台自己读什么」
// 列清单，漏了 aux_flags / stages / current_stage_status 三个字段，测试全绿、界面不对。
//
// 这条测试反过来做：**从卡片源码里抽出它读取的每一个字段**，逐条断言投影带。
// 组件多读一个字段而投影没加 → 红；投影多带一个前端零读取的键 → 红（体积）。
// 这样「加字段」和「改投影」被绑在同一批改动里，没法再各走各的。
//
// 卡片把一部分判据委托给 appTask.ts 的纯函数（isFinishStageActive / hasLaterStage /
// canAdvanceCard），所以两个文件一起扫。
func TestOverviewProjectionCoversEveryFieldTheSharedCardReads(t *testing.T) {
	cardPath := filepath.Join("..", "..", "..", "web", "src", "components", "task", "TaskCardRows.tsx")
	appTaskPath := filepath.Join("..", "..", "..", "web", "src", "app", "appTask.ts")
	cardSrc, err := os.ReadFile(cardPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	appTaskSrc, err := os.ReadFile(appTaskPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	// ① 抽出卡片读取的 task.<field>。
	//    t("task.xxx") 里那些是 i18n key 不是字段读取，先减掉 —— 减不干净的话
	//    这条测试会去断言一堆文案键，红得毫无意义。
	i18n := regexp.MustCompile(`t\("task\.([A-Za-z]+)"`)
	i18nKeys := map[string]bool{}
	for _, m := range i18n.FindAllStringSubmatch(string(cardSrc), -1) {
		i18nKeys[m[1]] = true
	}
	for _, m := range i18n.FindAllStringSubmatch(string(appTaskSrc), -1) {
		i18nKeys[m[1]] = true
	}

	taskReads := map[string]bool{}
	stageReads := map[string]bool{}
	// stage.snapshot 是 TaskTemplateStage（新建任务时的模板段），不是任务自己的段，
	// 投影里没有也不该有。
	const stageSnapshot = "snapshot"
	// 前缀必须是行首或非标识符字符：`worktree_finish_stage.go` 里的 `stage.go`
	// 是文件名不是字段读取，不挡掉的话这条测试会去断言一个叫 "go" 的键。
	taskFieldRe := regexp.MustCompile(`(?:^|[^A-Za-z0-9_])task\.([A-Za-z_][A-Za-z0-9_]*)`)
	stageFieldRe := regexp.MustCompile(`(?:^|[^A-Za-z0-9_])stage\.([A-Za-z_][A-Za-z0-9_]*)`)
	for _, src := range []string{string(cardSrc), string(appTaskSrc)} {
		for _, m := range taskFieldRe.FindAllStringSubmatch(src, -1) {
			if i18nKeys[m[1]] {
				continue
			}
			taskReads[m[1]] = true
		}
		for _, m := range stageFieldRe.FindAllStringSubmatch(src, -1) {
			if m[1] == stageSnapshot {
				continue
			}
			stageReads[m[1]] = true
		}
	}
	if len(taskReads) == 0 || len(stageReads) == 0 {
		t.Fatal("一条字段读取都没抽到 —— 正则或文件路径变了，先修这条测试再动投影")
	}

	// ② 造一个「什么都有」的任务，投影后逐字段断言。
	task := kanban.Task{
		ID:                "task_parity",
		TaskNumber:        7,
		RootID:            "mindfs",
		Name:              "卡片字段守卫",
		TaskTemplateID:    "tpl_1",
		TaskTemplateName:  "默认模板",
		CurrentStageIndex: 1,
		CurrentStageName:  "执行",
		Status:            "waiting_user",
		MainSessionKey:    "1791109592-9f9b6b12c4f1",
		CreatedAt:         time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:         time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC),
		Stages: []kanban.StageTemplate{
			{Name: "任务输入", Role: "user"},
			{Name: "执行", Role: "agent", Kind: kanban.StageKindWorktreeFinish},
		},
		CreateWorktree:     true,
		WorktreeBuilt:      true,
		WorktreePath:       "/tmp/worktree/task_parity",
		CurrentStageStatus: "waiting_user",
		AuxFlags: kanban.TaskAuxFlags{
			AskUserWaiting: true,
			HasPlan:        true,
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

	// ③ 卡片读的每个 task.<field> 都必须是投影里的一个键。
	missing := []string{}
	for field := range taskReads {
		key := jsonKeyForCardField(field)
		if _, ok := got[key]; !ok {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("投影缺了共享卡片读取的字段 %v —— 卡片会静默降级（徽标空、按钮不给），而别的测试全绿。投影：%s", missing, raw)
	}

	// ④ 卡片读的每个 stage.<field> 都必须是投影里每一段的一个键。
	stages, ok := got["stages"].([]any)
	if !ok || len(stages) == 0 {
		t.Fatalf("投影没带 stages，卡片读不了流水：%s", raw)
	}
	for i, rawStage := range stages {
		stage, ok := rawStage.(map[string]any)
		if !ok {
			t.Fatalf("stages[%d] 不是对象：%v", i, rawStage)
		}
		for field := range stageReads {
			key := jsonKeyForCardField(field)
			if _, ok := stage[key]; !ok {
				t.Fatalf("stages[%d] 缺 %q（卡片读 stage.%s）：%v", i, key, field, stage)
			}
		}
	}

	// ⑤ 反向：投影里 task 层的键，卡片一个都不许多读 —— 多一个就是体积。
	//    允许的键 = 卡片读的 + 投影刻意保留的几个（见 overviewTaskProjection 注释）。
	allowed := map[string]bool{}
	for field := range taskReads {
		allowed[jsonKeyForCardField(field)] = true
	}
	// 这几个不是卡片读的，但投影必须带：工作台分组/排序/派发要用。
	for _, key := range []string{
		"id", "task_number", "root_id", "task_template_id", "task_template_name",
		"current_stage_index", "current_stage_name", "status", "main_session_key",
		"created_at", "updated_at", "completed_at",
		"create_worktree", "worktree_path", "worktree_built", "worktree_missing",
	} {
		allowed[key] = true
	}
	extra := []string{}
	for key := range got {
		if !allowed[key] {
			extra = append(extra, key)
		}
	}
	if len(extra) > 0 {
		t.Fatalf("投影多带了前端零读取的键 %v —— 那是体积，且下次还会漂。投影：%s", extra, raw)
	}
}

// jsonKeyForCardField 把卡片里的字段名翻成投影的 JSON 键。
// 卡片源码里两种风格混用：task.root_id 已经是 snake_case，
// task.currentStageStatus 是 camelCase —— 后者要转成后者。
func jsonKeyForCardField(field string) string {
	if !strings.Contains(field, "_") && strings.ToLower(field) != field {
		return camelToSnake(field)
	}
	return field
}

func camelToSnake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r - 'A' + 'a')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
