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
// 卡片把一部分判据委托给别的模块，所以这些文件一起扫：
//   - appTask.ts：isFinishStageActive / hasLaterStage / canAdvanceCard 等纯函数；
//   - taskCardActions.ts：卡片真正渲染哪几个动作键（含 archived_at 这个终态判据）。
//
// 动作模块是 2026-11-22 从 appTask 里拆出来的；拆完这里没跟着扫，投影里的
// archived_at 就没人认领，守卫会误报「前端零读取」。
func TestOverviewProjectionCoversEveryFieldTheSharedCardReads(t *testing.T) {
	sources := []string{
		filepath.Join("..", "..", "..", "web", "src", "components", "task", "TaskCardRows.tsx"),
		filepath.Join("..", "..", "..", "web", "src", "app", "appTask.ts"),
		filepath.Join("..", "..", "..", "web", "src", "components", "task", "taskCardActions.ts"),
	}
	srcs := make([]string, 0, len(sources))
	for _, path := range sources {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		srcs = append(srcs, string(raw))
	}

	// ① 抽出卡片读取的 task.<field>。
	//    t("task.xxx") 里那些是 i18n key 不是字段读取，先减掉 —— 减不干净的话
	//    这条测试会去断言一堆文案键，红得毫无意义。
	i18n := regexp.MustCompile(`t\("task\.([A-Za-z]+)"`)
	i18nKeys := map[string]bool{}
	for _, src := range srcs {
		for _, m := range i18n.FindAllStringSubmatch(src, -1) {
			i18nKeys[m[1]] = true
		}
	}

	taskReads := map[string]bool{}
	// 前缀必须是行首或非标识符字符：文件名里的 `task.go` 不是字段读取。
	// 引号后面的一律跳过：`"task.finishWorktree"` 这类是 labelKey 字面量，
	// 字段读取永远不写在字符串里。
	taskFieldRe := regexp.MustCompile(`(?:^|[^A-Za-z0-9_'"` + "`" + `])task\.([A-Za-z_][A-Za-z0-9_]*)`)
	for _, src := range srcs {
		for _, m := range taskFieldRe.FindAllStringSubmatch(src, -1) {
			if i18nKeys[m[1]] {
				continue
			}
			taskReads[m[1]] = true
		}
	}
	if len(taskReads) == 0 {
		t.Fatal("一条字段读取都没抽到 —— 正则或文件路径变了，先修这条测试再动投影")
	}

	// ② 造一个「什么都有」的任务，投影后逐字段断言。
	task := kanban.Task{
		ID:               "task_parity",
		TaskNumber:       7,
		RootID:           "mindfs",
		Name:             "卡片字段守卫",
		TaskTemplateID:   "tpl_1",
		TaskTemplateName: "默认模板",
		Status:           "waiting_user",
		MainSessionKey:   "1791109592-9f9b6b12c4f1",
		ResultSummary:    "登录页已经改好了",
		CreatedAt:        time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:        time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC), CreateWorktree: true,
		ArchivedAt:    "2026-11-18T00:00:00Z",
		WorktreeBuilt: true,
		WorktreePath:  "/tmp/worktree/task_parity",
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

	// ⑤ 反向：投影里 task 层的键，卡片一个都不许多读 —— 多一个就是体积。
	//    允许的键 = 卡片读的 + 投影刻意保留的几个（见 overviewTaskProjection 注释）。
	allowed := map[string]bool{}
	for field := range taskReads {
		allowed[jsonKeyForCardField(field)] = true
	}
	// 这几个不是卡片读的，但投影必须带：工作台分组/排序/派发要用。
	for _, key := range []string{
		"id", "task_number", "root_id", "task_template_id", "task_template_name",
		"status", "main_session_key",
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
