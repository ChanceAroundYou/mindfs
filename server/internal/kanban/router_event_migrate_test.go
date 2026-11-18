package kanban

import (
	"context"
	"testing"

	"mindfs/server/internal/fs"
)

// TaskStore 打开时就该建好 router_events。仓库约定（task_store_migrate.go）是
// 迁移全部接在 migrateSchema 上、「不在任何运行时路径上懒建表」；T5 的实现把建表
// 放在第一次 InsertEvent 的惰性路径里，启动对账若先读表就会撞 no such table。
func TestTaskStoreOpenCreatesRouterEventTable(t *testing.T) {
	root := fs.NewRootInfo("root", "root", t.TempDir())
	tasks, err := NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	t.Cleanup(func() { _ = tasks.Close() })

	var name string
	err = tasks.db.QueryRowContext(context.Background(),
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'router_events'`).Scan(&name)
	if err != nil {
		t.Fatalf("router_events missing after open: %v", err)
	}
	if name != "router_events" {
		t.Fatalf("table name = %q", name)
	}
}

// 删除任务必须连带清掉事件账本行：否则任务重建同 ID 时会命中旧 unique_key，
// 新 Run 的事件永远插不进去，协调消费端就会一直看不到这个任务。
func TestDeleteTaskRouterDataRemovesEvents(t *testing.T) {
	root := fs.NewRootInfo("root", "root", t.TempDir())
	tasks, err := NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	t.Cleanup(func() { _ = tasks.Close() })
	store := NewRouterStore(tasks.db)
	ctx := context.Background()

	mustInsertRouterEvent(t, store, baseRouterEvent(RouterEventRunSettled, "run-1"))
	if err := store.DeleteTaskRouterData(ctx, "task-1"); err != nil {
		t.Fatalf("DeleteTaskRouterData: %v", err)
	}
	events, err := store.ListEventsByTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("ListEventsByTask: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events left after delete = %d, want 0", len(events))
	}
}
