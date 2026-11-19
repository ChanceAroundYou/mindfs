package kanban

import (
	"context"
	"testing"

	"mindfs/server/internal/fs"
)

// 删卡必须把任务拥有的所有数据一起带走：留下孤儿活动/运行/工作树登记，
// 「按 task_id 捞出来的证据」就指向一个已不存在的任务（实测 WSL 上删卡后
// 6 条活动仍在）。这里逐表钉住 DeleteTask 的级联范围。
func TestDeleteTask_RemovesTaskOwnedRows(t *testing.T) {
	ctx := context.Background()
	root := fs.NewRootInfo("root", "root", t.TempDir())
	store, err := NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for _, id := range []string{"task-gone", "task-kept"} {
		if _, err := store.CreateTask(ctx,
			Task{ID: id, RootID: root.ID, Status: StatusPending},
			TaskEvent{ID: "event-" + id, TaskID: id, Type: "created"},
		); err != nil {
			t.Fatalf("CreateTask(%s): %v", id, err)
		}
		seedTaskOwnedRows(t, store, id)
	}

	if err := store.DeleteTask(ctx, "task-gone"); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	for _, table := range taskOwnedTables {
		if got := countSQLRows(t, store, table.count); got != 0 {
			t.Errorf("%s: %d rows left for deleted task", table.name, got)
		}
		if got := countSQLRows(t, store, table.kept); got == 0 {
			t.Errorf("%s: the other task's rows were deleted too", table.name)
		}
	}
}

// taskOwnedTables 是 DeleteTask 必须覆盖的表。count 绑定 task-gone，kept 绑定
// task-kept —— 后者钉住「只删该任务的数据」，防止按表清空这种过度删除。
var taskOwnedTables = []struct {
	name string
	count, kept string
}{
	{"task_activities", `SELECT COUNT(*) FROM task_activities WHERE task_id = 'task-gone'`, `SELECT COUNT(*) FROM task_activities WHERE task_id = 'task-kept'`},
	{"continue_requests", `SELECT COUNT(*) FROM continue_requests WHERE task_id = 'task-gone'`, `SELECT COUNT(*) FROM continue_requests WHERE task_id = 'task-kept'`},
	{"node_runs", `SELECT COUNT(*) FROM node_runs WHERE task_id = 'task-gone'`, `SELECT COUNT(*) FROM node_runs WHERE task_id = 'task-kept'`},
	{"outbox_items", `SELECT COUNT(*) FROM outbox_items WHERE task_id = 'task-gone'`, `SELECT COUNT(*) FROM outbox_items WHERE task_id = 'task-kept'`},
	{"task_worktrees", `SELECT COUNT(*) FROM task_worktrees WHERE task_id = 'task-gone'`, `SELECT COUNT(*) FROM task_worktrees WHERE task_id = 'task-kept'`},
	{"task_execution_engines", `SELECT COUNT(*) FROM task_execution_engines WHERE task_id = 'task-gone'`, `SELECT COUNT(*) FROM task_execution_engines WHERE task_id = 'task-kept'`},
	{"workflow_runs", `SELECT COUNT(*) FROM workflow_runs WHERE task_id = 'task-gone'`, `SELECT COUNT(*) FROM workflow_runs WHERE task_id = 'task-kept'`},
	{"workflow_node_runs", `SELECT COUNT(*) FROM workflow_node_runs WHERE run_id LIKE 'run-gone%'`, `SELECT COUNT(*) FROM workflow_node_runs WHERE run_id LIKE 'run-kept%'`},
	{"workflow_run_events", `SELECT COUNT(*) FROM workflow_run_events WHERE run_id LIKE 'run-gone%'`, `SELECT COUNT(*) FROM workflow_run_events WHERE run_id LIKE 'run-kept%'`},
}

func seedTaskOwnedRows(t *testing.T, store *TaskStore, taskID string) {
	t.Helper()
	suffix := taskID[len("task-"):]
	runID := "run-" + suffix
	stmts := []string{
		`INSERT INTO task_activities (id, task_id, created_at) VALUES ('act-` + suffix + `', '` + taskID + `', '2026-01-01T00:00:00Z')`,
		`INSERT INTO continue_requests (request_id, task_id, comment_id, created_at, updated_at) VALUES ('req-` + suffix + `', '` + taskID + `', 'act-` + suffix + `', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		`INSERT INTO node_runs (id, task_id, created_at, updated_at) VALUES ('nrun-` + suffix + `', '` + taskID + `', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		`INSERT INTO outbox_items (id, task_id, request_id, created_at, updated_at) VALUES ('obx-` + suffix + `', '` + taskID + `', 'req-` + suffix + `', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		`INSERT INTO task_worktrees (task_id, worktree_id, created_at, updated_at) VALUES ('` + taskID + `', 'wt-` + suffix + `', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		`INSERT INTO task_execution_engines (task_id, updated_at) VALUES ('` + taskID + `', '2026-01-01T00:00:00Z')`,
		`INSERT INTO workflow_runs (id, task_id, graph_json, created_at, updated_at) VALUES ('` + runID + `', '` + taskID + `', '{}', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		`INSERT INTO workflow_node_runs (id, run_id, node_id, status, created_at, updated_at) VALUES ('nr-` + suffix + `', '` + runID + `', 'agent1', 'success', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		`INSERT INTO workflow_run_events (run_id, seq, type, created_at) VALUES ('` + runID + `', 1, 'node_agent_done', '2026-01-01T00:00:00Z')`,
	}
	for _, stmt := range stmts {
		if _, err := store.db.Exec(stmt); err != nil {
			t.Fatalf("seed %s: %v\n%s", taskID, err, stmt)
		}
	}
}

func countSQLRows(t *testing.T, store *TaskStore, query string) int {
	t.Helper()
	var n int
	if err := store.db.QueryRow(query).Scan(&n); err != nil {
		t.Fatalf("count: %v\n%s", err, query)
	}
	return n
}
