package kanban

import (
	"context"
	"testing"
)

// 建任务 = 建一张对话卡 + 把首条留言交给 Router。
// 任务本身没有阶段：状态是这一轮运行的投影，初始停在 pending。
func TestCreateTaskCreatesConversationWithFirstMessage(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID: root.ID,
		Name:   "修登录按钮",
		Input:  "broken button",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if detail.Task.Status != StatusPending {
		t.Fatalf("task status=%s, want pending", detail.Task.Status)
	}
	if detail.Task.Name != "修登录按钮" {
		t.Fatalf("task name=%q, want 修登录按钮", detail.Task.Name)
	}
	// 新任务不记录模板来源（模板库已下线），也绝不写阶段字段。
	if detail.Task.TaskTemplateID != "" || detail.Task.TaskTemplateName != "" {
		t.Fatalf("new tasks must not record a template source, got %q/%q",
			detail.Task.TaskTemplateID, detail.Task.TaskTemplateName)
	}
	activities, err := svc.ListTaskActivities(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("ListTaskActivities: %v", err)
	}
	if len(activities) == 0 {
		t.Fatal("首条留言必须落成 user_message 活动")
	}
	if activities[0].Type != ActivityTypeUserMessage || activities[0].Body != "broken button" {
		t.Fatalf("first activity = %+v, want user_message/broken button", activities[0])
	}
}

// 没给输入就只建一张空卡：不产生任何活动，请求也不报错
// （面板允许先建卡、后留言）。
func TestCreateTaskWithoutInputCreatesEmptyCard(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{RootID: root.ID, Name: "空卡"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if detail.Task.Status != StatusPending {
		t.Fatalf("task status=%s, want pending", detail.Task.Status)
	}
	activities, err := svc.ListTaskActivities(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("ListTaskActivities: %v", err)
	}
	if len(activities) != 0 {
		t.Fatalf("activities = %d, want 0（没有输入就没有留言）", len(activities))
	}
}

func TestCreateTaskRequiresRootID(t *testing.T) {
	svc, _ := newTestService(t, &fakeRunner{})
	if _, err := svc.CreateTask(context.Background(), CreateTaskInput{Input: "go"}); err == nil {
		t.Fatal("CreateTask must reject an empty root_id")
	}
}

// worktree 选项原样落库：它们决定这一轮对话在哪个工作树里跑。
func TestCreateTaskStoresWorktreeOptions(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID:             root.ID,
		CreateWorktree:     true,
		WorktreeBranchMode: "existing",
		WorktreeBranch:     "task-login",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if !detail.Task.CreateWorktree || detail.Task.WorktreeBranch != "task-login" {
		t.Fatalf("worktree options lost: %+v", detail.Task)
	}
}

// 勾掉「立即执行」：任务先落卡、留言以 queued 进对话流，但这一轮不派发
// （没有 Agent 回复、没有 Run），等用户之后点「立即执行」再开跑。
func TestCreateTaskStartImmediatelyFalseQueuesFirstMessage(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	start := false
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID:           root.ID,
		Input:            "晚点再跑",
		StartImmediately: &start,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if detail.Task.Status != StatusPending {
		t.Fatalf("task status=%s, want pending", detail.Task.Status)
	}
	activities, err := svc.ListTaskActivities(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("ListTaskActivities: %v", err)
	}
	if len(activities) != 1 {
		t.Fatalf("activities = %d, want 1（只有排队的用户留言）", len(activities))
	}
	if activities[0].Type != ActivityTypeUserMessage || activities[0].Body != "晚点再跑" {
		t.Fatalf("first activity = %+v, want user_message", activities[0])
	}
	if activities[0].Status != ActivityStatusQueued {
		t.Fatalf("queued activity status=%q, want %q", activities[0].Status, ActivityStatusQueued)
	}
}

// 「立即执行」只把最早一条排队留言派发一次；重复点击是幂等空操作。
func TestRunNowDispatchesQueuedFirstMessageOnce(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	start := false
	created, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID:           root.ID,
		Input:            "晚点再跑",
		StartImmediately: &start,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.RunNow(ctx, MoveInput{RootID: root.ID, TaskID: created.Task.ID}); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	activities, err := svc.ListTaskActivities(ctx, root.ID, created.Task.ID)
	if err != nil {
		t.Fatalf("ListTaskActivities: %v", err)
	}
	if len(activities) != 2 || activities[0].Status == ActivityStatusQueued {
		t.Fatalf("after RunNow activities = %+v, want a dispatched user message and one agent reply", activities)
	}
	if activities[1].Type != ActivityTypeAgentReply {
		t.Fatalf("second activity = %+v, want agent_reply", activities[1])
	}
	if _, err := svc.RunNow(ctx, MoveInput{RootID: root.ID, TaskID: created.Task.ID}); err != nil {
		t.Fatalf("second RunNow: %v", err)
	}
	again, err := svc.ListTaskActivities(ctx, root.ID, created.Task.ID)
	if err != nil {
		t.Fatalf("ListTaskActivities: %v", err)
	}
	if len(again) != len(activities) {
		t.Fatalf("second RunNow produced another turn: activities %d -> %d", len(activities), len(again))
	}
}

// 新建面板的「选择工作流」：首条留言的工作流选择要跟着留言一起落库。
// 排队路径尤其重要 —— 用户建完卡过一阵才点「立即执行」，那时选择只能从留言
// 行里恢复，不能重新问一遍界面。
func TestCreateTaskQueuedFirstMessageKeepsWorkflowChoice(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	start := false
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID:            root.ID,
		Input:             "跑 debug 流程",
		StartImmediately:  &start,
		WorkflowChoice:    CommentWorkflowChoiceSpecified,
		WorkflowVersionID: "workflow-debug:v1",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	activities, err := svc.ListTaskActivities(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("ListTaskActivities: %v", err)
	}
	if len(activities) != 1 {
		t.Fatalf("activities = %d, want 1", len(activities))
	}
	act := activities[0]
	if act.Status != ActivityStatusQueued {
		t.Fatalf("status=%q, want queued", act.Status)
	}
	if act.WorkflowChoice != CommentWorkflowChoiceSpecified {
		t.Fatalf("workflow choice=%q, want specified", act.WorkflowChoice)
	}
	if act.WorkflowVersionID != "workflow-debug:v1" {
		t.Fatalf("workflow version=%q, want workflow-debug:v1", act.WorkflowVersionID)
	}
}

// 空选择归一化成 empty；此时即使带了版本 id 也不保存（只有 specified 才有意义）。
func TestCreateTaskBlankWorkflowChoiceNormalizesToEmpty(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	start := false
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID:            root.ID,
		Input:             "随便走",
		StartImmediately:  &start,
		WorkflowChoice:    "   ",
		WorkflowVersionID: "workflow-debug:v1",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	activities, err := svc.ListTaskActivities(ctx, root.ID, detail.Task.ID)
	if err != nil {
		t.Fatalf("ListTaskActivities: %v", err)
	}
	if len(activities) != 1 {
		t.Fatalf("activities = %d, want 1", len(activities))
	}
	if activities[0].WorkflowChoice != CommentWorkflowChoiceEmpty {
		t.Fatalf("workflow choice=%q, want empty", activities[0].WorkflowChoice)
	}
	if activities[0].WorkflowVersionID != "" {
		t.Fatalf("workflow version=%q, want empty for an auto choice", activities[0].WorkflowVersionID)
	}
}

// 非法选择不许被悄悄吞成 empty：界面传错值时要当场报出来，否则用户选了工作流、
// 实际却走了 Router 自动挑选。
func TestCreateTaskRejectsUnknownWorkflowChoice(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	start := false
	_, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID:           root.ID,
		Input:            "x",
		StartImmediately: &start,
		WorkflowChoice:   "bogus",
	})
	if err == nil {
		t.Fatal("CreateTask accepted workflow_choice=bogus")
	}
}

// 排队留言真的带着当初选的工作流跑起来：派发时从留言行恢复版本，不是重新问
// Router 挑一个。这里用一条只有 notify 节点的 workflow —— 不派 Agent 也能看出
// Run 建在了那个版本上。
func TestRunNowDispatchesQueuedFirstMessageWithSavedWorkflowVersion(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	svc.Workflows = NewWorkflowStoreAt(t.TempDir())
	versionID := publishTestVersion(t, svc, WorkflowDefinition{
		Nodes: []WorkflowNode{wfNode("notify", NodeKindNotify)},
	})
	start := false
	detail, err := svc.CreateTask(ctx, CreateTaskInput{
		RootID:            root.ID,
		Input:             "跑这个流程",
		StartImmediately:  &start,
		WorkflowChoice:    CommentWorkflowChoiceSpecified,
		WorkflowVersionID: versionID,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := svc.RunNow(ctx, MoveInput{RootID: root.ID, TaskID: detail.Task.ID, Reason: "test"}); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	if err := svc.WaitWorkflowRunIdle(ctx, root.ID, detail.Task.ID); err != nil {
		t.Fatalf("WaitWorkflowRunIdle: %v", err)
	}
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	runs, err := store.ListWorkflowRunsForTask(ctx, detail.Task.ID)
	if err != nil {
		t.Fatalf("ListWorkflowRunsForTask: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
	if runs[0].Version != versionID {
		t.Fatalf("run version=%q, want %q (choice must be replayed from the queued message)", runs[0].Version, versionID)
	}
}
