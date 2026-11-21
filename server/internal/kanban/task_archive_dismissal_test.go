package kanban

import (
	"context"
	"testing"
	"time"
)

// 用户手动「取消归档」之后，下一个整点扫描不能把它再自动收起来。
//
// 触发条件很具体：自动归档的判据是 archived_at 为空 + 老 completed_at/updated_at。
// 取消归档恰好只把 archived_at 清空，时间戳一个不动（刻意的：它们是看板排序键与
// 自动归档的兜底起算点），于是老任务在下一拍就命中同一条查询 —— 用户眼前的行为
// 是「点取消归档，一小时后卡片又自己消失了」。
//
// 语义上取消归档就是用户的明确取舍：这张卡我要留在板上。它与「从没归档过」不同，
// 所以需要一个独立的「别再自动收我」标记，而不是复用/推动时间戳。
func TestUnarchivedTaskIsNotAutoArchivedAgain(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, nil)
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	now := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	old := now.Add(-8 * 24 * time.Hour)
	seedArchiveTask(t, store, "task-1", StatusSuccess, old, old.Format(time.RFC3339Nano))

	// 第一拍：超过 7 天的终态任务被自动归档（前置事实，防止用例在「压根没归档」时空转）。
	if n := svc.SweepAutoArchivedTasks(ctx); n != 1 {
		t.Fatalf("first sweep archived %d, want 1", n)
	}

	// 用户取消归档。
	if _, err := svc.ArchiveTask(ctx, ArchiveTaskInput{RootID: root.ID, TaskID: "task-1", Archived: false}); err != nil {
		t.Fatalf("unarchive: %v", err)
	}
	if got := mustTask(t, store, "task-1").ArchivedAt; got != "" {
		t.Fatalf("archived_at = %q after unarchive, want empty", got)
	}

	// 下一拍：不能再次归档 —— 否则取消归档只是「消失一小时」。
	if n := svc.SweepAutoArchivedTasks(ctx); n != 0 {
		t.Fatalf("second sweep archived %d, want 0 (manual unarchive must be respected)", n)
	}
	if got := mustTask(t, store, "task-1").ArchivedAt; got != "" {
		t.Fatalf("archived_at = %q after second sweep, want empty (task must stay on the board until the user archives it)", got)
	}
}
