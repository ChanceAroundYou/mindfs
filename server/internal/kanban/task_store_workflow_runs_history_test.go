package kanban

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mindfs/server/internal/fs"
)

// D14：任务可保留终态历史 Run，但同一时刻只允许一条非终态 Run 推进。
func TestWorkflowRunHistoryAllowsNewRunOnlyAfterTerminal(t *testing.T) {
	ctx := context.Background()
	root := fs.NewRootInfo("run-history-root", "run-history-root", t.TempDir())
	store, err := NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	defer store.Close()
	if _, err := store.CreateTask(ctx, Task{ID: "run-history-task", RootID: root.ID, Status: StatusPending, Stages: []StageTemplate{userStage("input")}},
		StageRun{ID: "run-history-stage", TaskID: "run-history-task", StageIndex: 0, StageName: "input", Role: RoleUser, Status: StageStatusPending},
		TaskEvent{ID: "run-history-event", TaskID: "run-history-task", Type: "created"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	graph := WorkflowDefinition{Nodes: []WorkflowNode{{ID: "n", Kind: NodeKindAgent}}}
	first := &WorkflowRun{ID: "run-history-1", TaskID: "run-history-task", Revision: 1, Graph: graph}
	if err := store.CreateWorkflowRunAndSetEngine(ctx, first); err != nil {
		t.Fatalf("create first run: %v", err)
	}
	second := &WorkflowRun{ID: "run-history-2", TaskID: "run-history-task", Revision: 1, Graph: graph}
	if err := store.CreateWorkflowRunAndSetEngine(ctx, second); !errors.Is(err, ErrTaskAlreadyWorkflow) {
		t.Fatalf("second run while first active err = %v, want ErrTaskAlreadyWorkflow", err)
	}

	first.NodeStates = map[string]string{"n": NodeRunSuccess}
	if err := store.SaveWorkflowRun(ctx, first); err != nil {
		t.Fatalf("mark first terminal: %v", err)
	}
	if err := store.CreateWorkflowRunAndSetEngine(ctx, second); err != nil {
		t.Fatalf("create second run after terminal: %v", err)
	}
	if got, err := store.WorkflowRunForTask(ctx, "run-history-task"); err != nil || got.ID != "run-history-2" {
		t.Fatalf("current run = %#v err=%v, want run-history-2", got, err)
	}
	history, err := store.ListWorkflowRunsForTask(ctx, "run-history-task")
	if err != nil || len(history) != 2 {
		t.Fatalf("history = %d err=%v, want 2", len(history), err)
	}
	if ProjectStatusFromRun(history[0]) != StatusPending || ProjectStatusFromRun(history[1]) != StatusSuccess {
		t.Fatalf("history statuses = %q / %q, want pending / success", ProjectStatusFromRun(history[0]), ProjectStatusFromRun(history[1]))
	}

	third := &WorkflowRun{ID: "run-history-3", TaskID: "run-history-task", Revision: 1, Graph: graph}
	if err := store.CreateWorkflowRunAndSetEngine(ctx, third); !errors.Is(err, ErrTaskAlreadyWorkflow) {
		t.Fatalf("third run while second active err = %v, want ErrTaskAlreadyWorkflow", err)
	}
	second.NodeStates = map[string]string{"n": NodeRunFail}
	if err := store.SaveWorkflowRun(ctx, second); err != nil {
		t.Fatalf("mark second terminal: %v", err)
	}
	if err := store.CreateWorkflowRunAndSetEngine(ctx, third); err != nil {
		t.Fatalf("create third run after terminal: %v", err)
	}
}

// 旧库升级：原表带 UNIQUE(task_id)，重建后历史行必须原样保留，且同一任务可再建 Run。
func TestMigrateWorkflowRunsDropsTaskUniquePreservingHistory(t *testing.T) {
	ctx := context.Background()
	root := fs.NewRootInfo("run-migrate-root", "run-migrate-root", t.TempDir())
	path, err := taskDBPath(root)
	if err != nil {
		t.Fatalf("taskDBPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create task db dir: %v", err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	if _, err := raw.Exec(`
CREATE TABLE workflow_runs (
 id TEXT PRIMARY KEY, task_id TEXT NOT NULL UNIQUE, version TEXT NOT NULL DEFAULT '',
 revision INTEGER NOT NULL DEFAULT 1, graph_json TEXT NOT NULL,
 node_states_json TEXT NOT NULL DEFAULT '{}', node_outputs_json TEXT NOT NULL DEFAULT '{}',
 selected_routes_json TEXT NOT NULL DEFAULT '{}', loop_state_json TEXT NOT NULL DEFAULT '{}',
 loop_exit_expr_json TEXT NOT NULL DEFAULT '{}', created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE INDEX idx_workflow_runs_task ON workflow_runs(task_id);
INSERT INTO workflow_runs(id,task_id,version,revision,graph_json,node_states_json,node_outputs_json,selected_routes_json,loop_state_json,loop_exit_expr_json,created_at,updated_at)
VALUES('legacy-run','legacy-task','',1,'{"nodes":[{"id":"n","kind":"agent"}]}','{}','{}','{}','{}','{}','2026-11-10T00:00:00Z','2026-11-10T00:00:00Z');
`); err != nil {
		t.Fatalf("seed old schema: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw db: %v", err)
	}

	store, err := NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore after old schema: %v", err)
	}
	var ddl string
	if err := store.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='workflow_runs'`).Scan(&ddl); err != nil {
		t.Fatalf("read migrated ddl: %v", err)
	}
	if strings.Contains(ddl, "task_id TEXT NOT NULL UNIQUE") {
		t.Fatalf("UNIQUE(task_id) survived migration: %s", ddl)
	}
	old, err := store.WorkflowRunByID(ctx, "legacy-run")
	if err != nil {
		t.Fatalf("legacy run lost: %v", err)
	}
	if old.TaskID != "legacy-task" || len(old.Graph.Nodes) != 1 || old.Graph.Nodes[0].ID != "n" {
		t.Fatalf("legacy run = %#v", old)
	}
	next := &WorkflowRun{ID: "legacy-run-2", TaskID: "legacy-task", Revision: 1,
		Graph: WorkflowDefinition{Nodes: []WorkflowNode{{ID: "n", Kind: NodeKindAgent}}}}
	if err := store.CreateWorkflowRunAndSetEngine(ctx, next); err != nil {
		t.Fatalf("second run on migrated schema: %v", err)
	}
	history, err := store.ListWorkflowRunsForTask(ctx, "legacy-task")
	if err != nil || len(history) != 2 {
		t.Fatalf("history = %d err=%v, want 2", len(history), err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened, err := NewTaskStore(root)
	if err != nil {
		t.Fatalf("reopen after migration: %v", err)
	}
	defer reopened.Close()
	if got, err := reopened.ListWorkflowRunsForTask(ctx, "legacy-task"); err != nil || len(got) != 2 {
		t.Fatalf("history after reopen = %d err=%v, want 2", len(got), err)
	}
}
