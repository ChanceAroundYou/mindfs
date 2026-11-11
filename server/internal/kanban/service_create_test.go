package kanban

import (
	"context"
	"testing"

	"mindfs/server/internal/fs"
)

func TestCreateTaskCopiesTaskStagesAndWaitsForUser(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Name:   "修登录按钮",
		Stages: []StageTemplate{
			userStage("Describe"),
			agentStage("Fix", "Fix this:\n{previous_input}"),
		},
		Input: "broken button",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	// 新建任务是未开始态：要有用户输入才开跑（用户点「开始」→ RunNow）。
	if detail.Task.Status != StatusPending {
		t.Fatalf("task status=%s, want pending（新建任务未开始）", detail.Task.Status)
	}
	if detail.Task.Name != "修登录按钮" {
		t.Fatalf("task name=%q, want 修登录按钮", detail.Task.Name)
	}
	if len(detail.Task.Stages) != 2 || detail.Task.Stages[1].Name != "Fix" {
		t.Fatalf("task stages not stored on task: %#v", detail.Task.Stages)
	}
	if len(detail.StageRuns) != 1 || detail.StageRuns[0].Input != "broken button" {
		t.Fatalf("stage input not stored in run: %#v", detail.StageRuns)
	}
}

// 首段勾了「立即执行」：创建后不用手动点「立即执行」就自己跑起来。
func TestCreateTaskStartsImmediatelyWhenFirstStageSaysSo(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	first := userStage("Describe")
	first.StartImmediately = true
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{first, agentStage("Fix", "Fix this:\n{previous_input}")},
		Input:  "broken button",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	// 等**自动落到 agent 段**（index==1）这个稳定结果，别等 Status==running。
	// running 是瞬态：推进阶段时同步置 running，随后异步 executeTask 跑完 agent 段
	// 就转 waiting_user。高并行负载下（go test ./... 各包并行）它可能在首轮轮询
	// （动作返回后 ~10ms）之前就翻过去 —— 实测 300 次高压下 12 次从头到尾只见
	// waiting_user，条件永不成立 → 2s 超时。index==1 在 running 与 waiting_user
	// 两态下都成立，且正是本用例要断言的结果。
	waitForCondition(t, func() bool {
		d, err := svc.GetTask(ctx, root.ID, detail.Task.ID)
		return err == nil && d.Task.CurrentStageIndex == 1
	})
	detail, err = svc.GetTask(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if detail.Task.CurrentStageIndex != 1 {
		t.Fatalf("current stage=%d, want 1（首段勾了立即执行就该直接进 agent 段）", detail.Task.CurrentStageIndex)
	}
	// 开跑这一次就把 user 段批准掉了，不该留在待审核。
	for _, run := range detail.StageRuns {
		if run.StageIndex == 0 && run.Status == StageStatusWaitingUser {
			t.Fatalf("stage 0 still waiting_user after start immediately")
		}
	}
}

// 没勾「立即执行」：安静停在「未开始」，等用户手动点「立即执行」。
func TestCreateTaskStaysPendingWithoutStartImmediately(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{userStage("Describe"), agentStage("Fix", "Fix this:\n{previous_input}")},
		Input:  "broken button",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if detail.Task.Status != StatusPending || detail.Task.CurrentStageIndex != 0 {
		t.Fatalf("task stage/status = %d/%s, want 0/%s（没勾就该停在未开始）",
			detail.Task.CurrentStageIndex, detail.Task.Status, StatusPending)
	}
}

// user 段的 AutoAdvance 是死字段（引擎对 user 段一律推进，不读它）：
// 它为 true 也不能触发开跑，否则模板里一个改不动的值就能决定任务跑不跑。
func TestCreateTaskIgnoresUserStageAutoAdvance(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	first := userStage("Describe")
	first.AutoAdvance = true
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{first, agentStage("Fix", "Fix this:\n{previous_input}")},
		Input:  "broken button",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if detail.Task.Status != StatusPending || detail.Task.CurrentStageIndex != 0 {
		t.Fatalf("task stage/status = %d/%s, want 0/%s（user 段的 auto_advance 管不了开跑）",
			detail.Task.CurrentStageIndex, detail.Task.Status, StatusPending)
	}
}

// 面板上「立即执行」被取消时，前端显式发回 start_immediately=false，
// 任务必须尊重它、停在「未开始」（不能因为是内置默认流水就自己跑起来）。
func TestCreateTaskRespectsExplicitStartImmediatelyOff(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Input:  "broken button",
		Stages: []StageTemplate{
			{Name: "Describe", Role: RoleUser, StartImmediately: false},
			agentStage("Fix", "Fix this:\n{previous_input}"),
		},
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if detail.Task.Status != StatusPending || detail.Task.CurrentStageIndex != 0 {
		t.Fatalf("task stage/status = %d/%s, want 0/%s（面板显式取消就该停在未开始）",
			detail.Task.CurrentStageIndex, detail.Task.Status, StatusPending)
	}
}

// 勾了立即执行但没给输入：停在「未开始」，创建请求不能因此报错
// （目标段引用 {previous_input}，RunNow 本会因「input required」失败）。
func TestCreateTaskStartImmediatelyRequiresNonEmptyInput(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	first := userStage("Describe")
	first.StartImmediately = true
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Stages: []StageTemplate{first, agentStage("Fix", "Fix this:\n{previous_input}")},
	})
	if err != nil {
		t.Fatalf("CreateTask with empty input returned error: %v", err)
	}
	if detail.Task.Status != StatusPending || detail.Task.CurrentStageIndex != 0 {
		t.Fatalf("task stage/status = %d/%s, want 0/%s（没有输入不该立即执行）",
			detail.Task.CurrentStageIndex, detail.Task.Status, StatusPending)
	}
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// 建任务只认随包带上的流水：模板库已下线（Phase 3，Scope: G-LJ），
// 新任务不再记录任何模板来源，历史任务的冻结列保持只读展示。
func TestCreateTaskUsesInlinedStagesOnly(t *testing.T) {
	registry := fs.NewRootInfo("root", "root", t.TempDir())
	svc := NewService(testRoots{root: registry})

	detail, err := svc.CreateTask(context.Background(), CreateTaskInput{
		RootID: registry.ID,
		Stages: []StageTemplate{{Name: "输入", Role: RoleUser, PromptTemplate: "do {previous_input}"}, agentStage("Fix", "Fix:\n{previous_input}")},
		Input:  "review",
	})
	if err != nil {
		t.Fatalf("CreateTask with inlined stages: %v", err)
	}
	if len(detail.Task.Stages) != 2 {
		t.Fatalf("stages = %d, want 2 (the payload's)", len(detail.Task.Stages))
	}
	if detail.Task.TaskTemplateID != "" || detail.Task.TaskTemplateName != "" {
		t.Fatalf("new tasks must not record a template source, got %q/%q",
			detail.Task.TaskTemplateID, detail.Task.TaskTemplateName)
	}
}

// 模板库已下线（Phase 3，Scope: G-LJ）：CreateTask 不再回查任何模板库。
func TestCreateTaskRequiresInlineStages(t *testing.T) {
	registry := fs.NewRootInfo("root", "root", t.TempDir())
	svc := NewService(testRoots{root: registry})

	// 不给 stages 必须**报错**，而不是静默建出一个没有流水的任务 ——
	// 空流水既跑不起来，看板也渲染成空卡，错误要到用户点开才暴露。
	_, err := svc.CreateTask(context.Background(), CreateTaskInput{
		RootID: registry.ID,
		Input:  "go",
	})
	if err == nil {
		t.Fatal("CreateTask must reject an empty stages list — templates are gone, there is no fallback")
	}
}
