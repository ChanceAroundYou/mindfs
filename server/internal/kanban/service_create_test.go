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
