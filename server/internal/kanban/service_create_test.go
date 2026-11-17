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
