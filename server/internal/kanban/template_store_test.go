package kanban

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTaskTemplateStoreSeedsBundledTemplatesWhenUserFileMissing(t *testing.T) {
	dir := t.TempDir()
	bundledPath := filepath.Join(t.TempDir(), taskTemplateFile)
	bundled := []TaskTemplate{{
		ID:   "tmpl_default",
		Name: "Default task",
		Stages: []TaskTemplateStage{{
			ID:       "stage_default",
			Position: 0,
			Snapshot: StageTemplate{
				ID:   "stage_user",
				Name: "Describe",
				Role: RoleUser,
			},
		}},
	}}
	data, err := json.MarshalIndent(bundled, "", "  ")
	if err != nil {
		t.Fatalf("marshal bundled templates: %v", err)
	}
	if err := os.WriteFile(bundledPath, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write bundled templates: %v", err)
	}

	previous := bundledTaskTemplatePaths
	bundledTaskTemplatePaths = func() []string { return []string{bundledPath} }
	defer func() { bundledTaskTemplatePaths = previous }()

	store := NewTemplateStoreAt(dir)
	items, err := store.ListTaskTemplates()
	if err != nil {
		t.Fatalf("ListTaskTemplates: %v", err)
	}
	if len(items) != 1 || items[0].ID != "tmpl_default" {
		t.Fatalf("templates = %#v, want bundled default", items)
	}
	if _, err := os.Stat(filepath.Join(dir, taskTemplateFile)); err != nil {
		t.Fatalf("user task template file not seeded: %v", err)
	}
}

func TestTaskTemplateStoreDoesNotSeedWhenUserFileExists(t *testing.T) {
	cases := []struct {
		name     string
		contents string
		wantFile string
	}{
		{name: "empty array", contents: "[]\n", wantFile: "[]"},
		{name: "empty file", contents: "", wantFile: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, taskTemplateFile), []byte(tc.contents), 0o644); err != nil {
				t.Fatalf("write user templates: %v", err)
			}
			bundledPath := filepath.Join(t.TempDir(), taskTemplateFile)
			if err := os.WriteFile(bundledPath, []byte(`[{"id":"tmpl_default","name":"Default task","stages":[{"id":"stage_default","position":0,"snapshot":{"id":"stage_user","name":"Describe","role":"user"}}]}]`), 0o644); err != nil {
				t.Fatalf("write bundled templates: %v", err)
			}

			previous := bundledTaskTemplatePaths
			bundledTaskTemplatePaths = func() []string { return []string{bundledPath} }
			defer func() { bundledTaskTemplatePaths = previous }()

			store := NewTemplateStoreAt(dir)
			items, err := store.ListTaskTemplates()
			if err != nil {
				t.Fatalf("ListTaskTemplates: %v", err)
			}
			if len(items) != 0 {
				t.Fatalf("templates = %#v, want no seed", items)
			}
			data, err := os.ReadFile(filepath.Join(dir, taskTemplateFile))
			if err != nil {
				t.Fatalf("read user templates: %v", err)
			}
			if got := strings.TrimSpace(string(data)); got != tc.wantFile {
				t.Fatalf("user file = %q, want %q", got, tc.wantFile)
			}
		})
	}
}

func TestTemplateStoreJSONAndFirstStageValidation(t *testing.T) {
	dir := t.TempDir()
	store := NewTemplateStoreAt(dir)
	stage, err := store.SaveStageTemplate(StageTemplate{
		Name:           "Describe",
		Role:           RoleUser,
		PromptTemplate: "Describe the task",
	})
	if err != nil {
		t.Fatalf("SaveStageTemplate: %v", err)
	}
	if stage.ID == "" {
		t.Fatalf("stage ID empty")
	}
	if _, err := os.Stat(filepath.Join(dir, stageTemplateFile)); err != nil {
		t.Fatalf("stage template file missing: %v", err)
	}
	_, err = store.SaveTaskTemplate(TaskTemplate{
		Name: "Bad",
		Stages: []TaskTemplateStage{{
			Position: 0,
			Snapshot: StageTemplate{
				Name: "Agent",
				Role: RoleAgent,
			},
		}},
	})
	if err == nil {
		t.Fatalf("SaveTaskTemplate accepted non-user first stage")
	}
	_, err = store.SaveTaskTemplate(TaskTemplate{
		Name: "Good",
		Stages: []TaskTemplateStage{{
			Position: 0,
			Snapshot: stage,
		}},
	})
	if err != nil {
		t.Fatalf("SaveTaskTemplate: %v", err)
	}
}

// 存模板时把 user 段的 auto_advance 归一成 true：引擎对 user 段一律推进，
// 面板上那颗开关是灰的、改不了，存成 false 只会让 JSON 里躺着个假值。
// 存量模板（AutoAdvance 缺失 = false）下次一存就自愈。
func TestSaveTaskTemplateNormalizesUserStageAutoAdvance(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t, nil)
	tmpl, err := svc.SaveTaskTemplate(ctx, TaskTemplate{
		Name: "Legacy",
		Stages: []TaskTemplateStage{
			{Position: 0, Snapshot: StageTemplate{Name: "Describe", Role: RoleUser, AutoAdvance: false, StartImmediately: true}},
			{Position: 1, Snapshot: agentStage("Fix", "Fix this:\n{previous_input}")},
		},
	})
	if err != nil {
		t.Fatalf("SaveTaskTemplate: %v", err)
	}
	if !tmpl.Stages[0].Snapshot.AutoAdvance {
		t.Fatalf("user stage auto_advance=%v, want true（user 段一律推进，存 false 是假值）",
			tmpl.Stages[0].Snapshot.AutoAdvance)
	}
	// 首段的「立即执行」不该被这次归一化碰到。
	if !tmpl.Stages[0].Snapshot.StartImmediately {
		t.Fatalf("user stage start_immediately=%v, want true", tmpl.Stages[0].Snapshot.StartImmediately)
	}
	// agent 段的 auto_advance 仍由用户自己管。
	agent := agentStage("Fix", "Fix this:\n{previous_input}")
	agent.AutoAdvance = false
	tmpl.Stages[1].Snapshot = agent
	saved, err := svc.SaveTaskTemplate(ctx, tmpl)
	if err != nil {
		t.Fatalf("SaveTaskTemplate again: %v", err)
	}
	if saved.Stages[1].Snapshot.AutoAdvance {
		t.Fatalf("agent stage auto_advance was forced true, want false")
	}
}

// 模板是预设：创建时拷贝快照，之后改/删预设与在途任务完全无关。
func TestPresetSnapshotIndependentOfTask(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	tmpl, err := svc.SaveTaskTemplate(ctx, TaskTemplate{
		Name: "Bug fix",
		Stages: []TaskTemplateStage{{
			Position: 0,
			Snapshot: userStage("Describe"),
		}},
	})
	if err != nil {
		t.Fatalf("SaveTaskTemplate: %v", err)
	}
	detail, err := svc.CreateTask(ctx, CreateTaskInput{RootID: root.ID, TaskTemplateID: tmpl.ID, Input: "one"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if detail.Task.Name != "Bug fix" || detail.Task.TaskTemplateName != "Bug fix" {
		t.Fatalf("task name=%q/%q, want Bug fix", detail.Task.Name, detail.Task.TaskTemplateName)
	}

	// 在途任务依然在：改预设名、删预设都应成功（解耦），任务身上的快照不变。
	tmpl.Name = "Renamed"
	if _, err := svc.SaveTaskTemplate(ctx, tmpl); err != nil {
		t.Fatalf("SaveTaskTemplate renamed returned error (template should be freely editable): %v", err)
	}
	if err := svc.DeleteTaskTemplate(ctx, tmpl.ID); err != nil {
		t.Fatalf("DeleteTaskTemplate with in-flight task returned error: %v", err)
	}
	got, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Task.Name != "Bug fix" || got.Task.TaskTemplateName != "Bug fix" {
		t.Fatalf("task snapshot changed after preset edits: %q/%q", got.Task.Name, got.Task.TaskTemplateName)
	}
	if len(got.Task.Stages) != 1 || got.Task.Stages[0].Name != "Describe" {
		t.Fatalf("task stages not snapshotted at creation: %#v", got.Task.Stages)
	}
}

// 兼容：老任务（存预设时代，无任务流水）推进时从预设惰性回填一次并落盘。
func TestLegacyTaskStagesBackfilledFromTemplate(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	tmpl, err := svc.SaveTaskTemplate(ctx, TaskTemplate{
		Name: "Legacy",
		Stages: []TaskTemplateStage{{
			Position: 0,
			Snapshot: userStage("Old Describe"),
		}},
	})
	if err != nil {
		t.Fatalf("SaveTaskTemplate: %v", err)
	}
	// 模拟老数据：直接用 store 插一个没有任务流水、只有预设 ID 的任务。
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	now := time.Now().UTC()
	legacy := Task{
		ID:                "task_legacy",
		RootID:            root.ID,
		TaskTemplateID:    tmpl.ID,
		Stages:            nil,
		CurrentStageIndex: 0,
		Status:            StatusWaitingUser,
		Labels:            []string{},
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if _, err := store.CreateTask(ctx, legacy, StageRun{
		ID:         "run_legacy",
		TaskID:     "task_legacy",
		StageIndex: 0,
		Role:       RoleUser,
		Status:     StageStatusWaitingUser,
		Input:      "first",
		CreatedAt:  now,
		UpdatedAt:  now,
	}, TaskEvent{ID: "event_legacy", TaskID: "task_legacy", Type: "task_created", CreatedAt: now}); err != nil {
		t.Fatalf("store.CreateTask legacy: %v", err)
	}

	before, err := svc.GetTask(ctx, root.ID, "task_legacy")
	if err != nil {
		t.Fatalf("GetTask before move: %v", err)
	}
	if len(before.Task.Stages) != 0 {
		t.Fatalf("legacy task unexpectedly has stages: %#v", before.Task.Stages)
	}
	if _, err := svc.RerunStage(ctx, MoveInput{RootID: root.ID, TaskID: "task_legacy", StageIndex: 0}); err != nil {
		t.Fatalf("RerunStage on legacy task: %v", err)
	}
	after, err := svc.GetTask(ctx, root.ID, "task_legacy")
	if err != nil {
		t.Fatalf("GetTask after move: %v", err)
	}
	if len(after.Task.Stages) != 1 || after.Task.Stages[0].Name != "Old Describe" {
		t.Fatalf("legacy task stages not backfilled from preset: %#v", after.Task.Stages)
	}
	if after.Task.Status != StatusWaitingUser {
		t.Fatalf("legacy task status=%s, want waiting_user after rerun", after.Task.Status)
	}
}

// 模板库只有主节点一份，但模板可以限定到某个项目（TaskTemplate.RootID）。
// 列表按项目过滤时要给出「全局 + 本项目」的并集：全局模板在哪儿都能用，
// 而**别的**项目的专用模板不该出现在这个项目的下拉框里 —— 那会让菜单变成一张
// 「这张模板根本不适用于本项目」的名单。
func TestListTaskTemplatesForRootScopesByProject(t *testing.T) {
	store := NewTemplateStoreAt(t.TempDir())
	save := func(id, name, rootID string) {
		t.Helper()
		_, err := store.SaveTaskTemplate(TaskTemplate{
			ID:   id,
			Name: name,
			// 第一段必须是 user 段，SaveTaskTemplate 会校验。
			Stages: []TaskTemplateStage{{
				ID:       id + "_s0",
				Position: 0,
				Snapshot: StageTemplate{ID: id + "_u", Name: "输入", Role: RoleUser, PromptTemplate: "do"},
			}},
			RootID: rootID,
		})
		if err != nil {
			t.Fatalf("SaveTaskTemplate(%s): %v", id, err)
		}
	}
	save("tmpl_global", "全局", "")
	save("tmpl_mine", "本项目", "CMAI")
	save("tmpl_theirs", "别人的", "mindfs")

	ids := func(items []TaskTemplate) []string {
		out := make([]string, 0, len(items))
		for _, item := range items {
			out = append(out, item.ID)
		}
		return out
	}

	items, err := store.ListTaskTemplatesForRoot("CMAI")
	if err != nil {
		t.Fatalf("ListTaskTemplatesForRoot(CMAI): %v", err)
	}
	got := ids(items)
	if len(got) != 2 {
		t.Fatalf("templates for CMAI = %v, want global + own", got)
	}
	// 排序按名字（中文按 unicode 码位），所以别依赖顺序，只看集合。
	if !contains(got, "tmpl_global") || !contains(got, "tmpl_mine") {
		t.Fatalf("templates for CMAI = %v, want [tmpl_global tmpl_mine]", got)
	}
	if contains(got, "tmpl_theirs") {
		t.Fatalf("another project's template leaked into CMAI: %v", got)
	}

	// 没带项目 = 只给全局的。建任务面板总是带着项目来，但工作台这种没有项目上下文的
	// 地方不该看到一堆「只对某个项目成立」的模板。
	globals, err := store.ListTaskTemplatesForRoot("")
	if err != nil {
		t.Fatalf("ListTaskTemplatesForRoot(\"\"): %v", err)
	}
	if ids := ids(globals); len(ids) != 1 || ids[0] != "tmpl_global" {
		t.Fatalf("unscoped templates = %v, want [tmpl_global]", ids)
	}
}
