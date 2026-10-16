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

// 面板上把模板勾上的那颗取消掉：前端会把首段的 start_immediately 显式发回 false，
// 任务必须尊重它、停在「未开始」（不能因为模板是 true 就自己跑起来）。
func TestCreateTaskRespectsExplicitStartImmediatelyOffOverTemplate(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	tmpl, err := svc.SaveTaskTemplate(ctx, TaskTemplate{
		Name: "Autostart",
		Stages: []TaskTemplateStage{
			{Position: 0, Snapshot: StageTemplate{Name: "Describe", Role: RoleUser, StartImmediately: true}},
			{Position: 1, Snapshot: agentStage("Fix", "Fix this:\n{previous_input}")},
		},
	})
	if err != nil {
		t.Fatalf("SaveTaskTemplate: %v", err)
	}
	// 前端 applyStageOverride 的输出形状：拍平的 stages，首段 start_immediately=false。
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID:         root.ID,
		TaskTemplateID: tmpl.ID,
		Input:          "broken button",
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

// 模板首段勾了「立即执行」且客户端没发 stages（老客户端）：后端照模板的开跑。
func TestCreateTaskFollowsTemplateStartImmediatelyWhenNoStagesSent(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	tmpl, err := svc.SaveTaskTemplate(ctx, TaskTemplate{
		Name: "Autostart",
		Stages: []TaskTemplateStage{
			{Position: 0, Snapshot: StageTemplate{Name: "Describe", Role: RoleUser, StartImmediately: true}},
			{Position: 1, Snapshot: agentStage("Fix", "Fix this:\n{previous_input}")},
		},
	})
	if err != nil {
		t.Fatalf("SaveTaskTemplate: %v", err)
	}
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID:         root.ID,
		TaskTemplateID: tmpl.ID,
		Input:          "broken button",
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
		t.Fatalf("current stage=%d, want 1（没发 stages 就该照模板立即执行）", detail.Task.CurrentStageIndex)
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

// 建任务时前端会把模板的流水随包带上（worker 节点上没有模板库，回查必然失败）。
// 后端这条路径要能只靠 payload 建出任务，模板名也用前端传来的快照。
func TestCreateTaskUsesInlinedStagesAndTemplateName(t *testing.T) {
	registry := fs.NewRootInfo("root", "root", t.TempDir())
	svc := NewService(NewTemplateStoreAt(t.TempDir()), testRoots{root: registry})

	// 本机模板库里**没有**这个模板 —— 模拟 worker 节点。
	detail, err := svc.CreateTask(context.Background(), CreateTaskInput{
		RootID:           registry.ID,
		Stages:           []StageTemplate{{Name: "输入", Role: RoleUser, PromptTemplate: "do {previous_input}"}, agentStage("Fix", "Fix:\n{previous_input}")},
		TaskTemplateID:   "tmpl_only_on_primary",
		TaskTemplateName: "论文评审",
		Input:            "review",
	})
	if err != nil {
		t.Fatalf("CreateTask with inlined stages: %v", err)
	}
	if len(detail.Task.Stages) != 2 {
		t.Fatalf("stages = %d, want 2 (the payload's, not a template lookup)", len(detail.Task.Stages))
	}
	if detail.Task.TaskTemplateName != "论文评审" {
		t.Fatalf("template name = %q, want the frontend snapshot", detail.Task.TaskTemplateName)
	}
}

// 本机模板库里**有**这个模板时，本机的名字优先：前端那份快照只是兜底
// （用户在模板弹窗里改了名字，前端那份可能过期）。
func TestCreateTaskPrefersLocalTemplateName(t *testing.T) {
	registry := fs.NewRootInfo("root", "root", t.TempDir())
	templates := NewTemplateStoreAt(t.TempDir())
	svc := NewService(templates, testRoots{root: registry})

	if _, err := templates.SaveTaskTemplate(TaskTemplate{
		ID:   "tmpl_saved",
		Name: "本机名字",
		Stages: []TaskTemplateStage{{
			ID:       "tmpl_saved_s0",
			Position: 0,
			Snapshot: StageTemplate{ID: "tmpl_saved_u", Name: "输入", Role: RoleUser, PromptTemplate: "do"},
		}},
	}); err != nil {
		t.Fatalf("SaveTaskTemplate: %v", err)
	}

	detail, err := svc.CreateTask(context.Background(), CreateTaskInput{
		RootID:           registry.ID,
		TaskTemplateID:   "tmpl_saved",
		TaskTemplateName: "过期的前端快照",
		Input:            "go",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if detail.Task.TaskTemplateName != "本机名字" {
		t.Fatalf("template name = %q, want the local store's", detail.Task.TaskTemplateName)
	}
	if detail.Task.Name != "本机名字" {
		t.Fatalf("task name = %q, want the template's", detail.Task.Name)
	}
}
