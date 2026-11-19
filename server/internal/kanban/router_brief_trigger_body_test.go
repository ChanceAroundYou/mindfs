package kanban

import (
	"context"
	"strings"
	"testing"

	"mindfs/server/internal/fs"
)

// 触发这一轮的留言是 Router 唯一的决策输入；`routerBriefActivityExcerpt`（500
// 字节）只是「最近活动」的摘录上限，套到 Trigger 上会把需求砍成半句话。实测一条
// 574 字节的需求被截在 189 个字符（"…5. 移动端下…"），Router 于是去问用户第 5 条
// 要什么——用户明明已经写完了。
func TestSituationBrief_TriggerUserMessageIsNotCutAtActivityExcerpt(t *testing.T) {
	ctx := context.Background()
	store, task, activityID := newBriefStoreWithUserMessage(t, strings.Repeat("需", 240)) // 720 字节

	brief, err := buildSituationBrief(ctx, store, nil, RouterConfig{}, task, RouterTriggerUserMessage, activityID)
	if err != nil {
		t.Fatalf("buildSituationBrief: %v", err)
	}
	if brief.Trigger.UserMessage == nil {
		t.Fatal("trigger user message missing")
	}
	if got := brief.Trigger.UserMessage.Body; got != strings.Repeat("需", 240) {
		t.Fatalf("trigger body len=%d (%q), want the full 720-byte message", len(got), got)
	}
}

// 最近活动的摘录仍按 500 字节压：同一段正文在列表里只作背景，不能因为
// Trigger 放宽就整段搬两遍。
func TestSituationBrief_RecentActivityExcerptStaysCapped(t *testing.T) {
	ctx := context.Background()
	store, task, activityID := newBriefStoreWithUserMessage(t, strings.Repeat("需", 240))

	brief, err := buildSituationBrief(ctx, store, nil, RouterConfig{}, task, RouterTriggerUserMessage, activityID)
	if err != nil {
		t.Fatalf("buildSituationBrief: %v", err)
	}
	if len(brief.RecentActivities) == 0 {
		t.Fatal("recent activities missing")
	}
	excerpt := brief.RecentActivities[len(brief.RecentActivities)-1].BodyExcerpt
	if len(excerpt) > routerBriefActivityExcerpt+3 {
		t.Fatalf("excerpt len=%d, want it capped near %d", len(excerpt), routerBriefActivityExcerpt)
	}
}

// 放宽不等于不限：一条几兆的粘贴仍要在 brief 里被收口，否则单个请求就能
// 把 24KB 的 brief 预算撑破。
func TestSituationBrief_TriggerUserMessageStaysBounded(t *testing.T) {
	ctx := context.Background()
	store, task, activityID := newBriefStoreWithUserMessage(t, strings.Repeat("需", 4000)) // 12000 字节

	brief, err := buildSituationBrief(ctx, store, nil, RouterConfig{}, task, RouterTriggerUserMessage, activityID)
	if err != nil {
		t.Fatalf("buildSituationBrief: %v", err)
	}
	if brief.Trigger.UserMessage == nil {
		t.Fatal("trigger user message missing")
	}
	if got := len(brief.Trigger.UserMessage.Body); got > routerBriefTriggerBodyLimit+3 {
		t.Fatalf("trigger body len=%d, want <= %d", got, routerBriefTriggerBodyLimit)
	}
}

func newBriefStoreWithUserMessage(t *testing.T, body string) (*TaskStore, Task, string) {
	t.Helper()
	ctx := context.Background()
	root := fs.NewRootInfo("root", "root", t.TempDir())
	store, err := NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateTask(ctx,
		Task{ID: "task-brief", RootID: root.ID, Status: StatusPending, Name: "需求"},
		TaskEvent{ID: "event-brief", TaskID: "task-brief", Type: "created"},
	); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	task, err := store.GetTask(ctx, "task-brief")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	activityID := "act-user-brief"
	if err := NewActivityStore(store.db).Insert(ctx, &TaskActivity{
		ID:     activityID,
		TaskID: task.ID,
		RootID: task.RootID,
		Type:   ActivityTypeUserMessage,
		Role:   RoleUser,
		Body:   body,
		Status: ActivityStatusCompleted,
	}); err != nil {
		t.Fatalf("Insert activity: %v", err)
	}
	return store, task, activityID
}
