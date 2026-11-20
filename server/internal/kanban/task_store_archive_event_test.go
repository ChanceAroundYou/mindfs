package kanban

import (
	"context"
	"testing"
)

// 归档/取消归档是任务状态的一次真实变化，但它刻意不动 updated_at、也不经过
// 状态机，于是 task_events 里一条记录都没有 —— 事后追「这张卡什么时候被收走
// 的、是谁收的」只能靠猜。事件账本要留痕。
func TestSetTaskArchivedAppendsTaskEvents(t *testing.T) {
	ctx := context.Background()
	store, _, _ := newDirectReplyRecoveryFixture(t, "task-archive-event", "sess-archive-event", StatusSuccess)

	if _, err := store.SetTaskArchived(ctx, "task-archive-event", true); err != nil {
		t.Fatalf("SetTaskArchived(true): %v", err)
	}
	if _, err := store.SetTaskArchived(ctx, "task-archive-event", false); err != nil {
		t.Fatalf("SetTaskArchived(false): %v", err)
	}

	events, err := store.ListEvents(ctx, "task-archive-event")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var archived, unarchived bool
	for _, event := range events {
		switch event.Type {
		case "archived":
			archived = true
		case "unarchived":
			unarchived = true
		}
	}
	if !archived || !unarchived {
		t.Fatalf("归档事件缺失：archived=%v unarchived=%v（events=%d）", archived, unarchived, len(events))
	}
}

// 幂等调用不能刷屏：已归档再归档、未归档再取消，都不该再写一条事件。
func TestSetTaskArchivedDoesNotLogNoopEvents(t *testing.T) {
	ctx := context.Background()
	store, _, _ := newDirectReplyRecoveryFixture(t, "task-archive-noop", "sess-archive-noop", StatusSuccess)

	if _, err := store.SetTaskArchived(ctx, "task-archive-noop", true); err != nil {
		t.Fatalf("SetTaskArchived(true): %v", err)
	}
	if _, err := store.SetTaskArchived(ctx, "task-archive-noop", true); err != nil {
		t.Fatalf("SetTaskArchived(true) 二次: %v", err)
	}
	events, err := store.ListEvents(ctx, "task-archive-noop")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	count := 0
	for _, event := range events {
		if event.Type == "archived" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("archived 事件 = %d 条，want 1（幂等调用不应重复记账）", count)
	}
}
