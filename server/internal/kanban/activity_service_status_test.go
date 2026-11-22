package kanban

// 活动输入校验、Run 终态投影与旧库迁移列。
// 从 activity_service_test.go 拆出（硬线 500，2026-11-22）。

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

func TestActivityService_CreateUserActivity_EmptyBody(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)

	if _, _, err := f.service.CreateUserActivity(ctx, "task-1", "   ", CommentWorkflowChoiceEmpty, nil); !errors.Is(err, ErrEmptyActivityBody) {
		t.Fatalf("err = %v, want ErrEmptyActivityBody", err)
	}
	list, err := f.service.activities.List(ctx, "task-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("empty body persisted %d activities, want 0", len(list))
	}
}

func TestActivityService_CreateUserActivity_InvalidChoice(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)

	if _, _, err := f.service.CreateUserActivity(ctx, "task-1", "hello", "bogus", nil); err == nil {
		t.Fatal("invalid workflow_choice accepted, want error")
	}
}

// Review Focus #3: a failed WorkflowRun must move its Activity to failed and keep
// the error text visible in the summary.
func TestActivityService_WorkflowRunFailure_UpdatesActivity(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)

	_, agentAct, err := f.service.CreateUserActivity(ctx, "task-1", "test", CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	if agentAct.RunID == "" {
		t.Fatal("precondition: agent activity must own a run")
	}

	if err := f.service.OnWorkflowRunFailed(ctx, agentAct.RunID, "execution error"); err != nil {
		t.Fatalf("OnWorkflowRunFailed: %v", err)
	}

	updated, err := f.service.activities.Get(ctx, agentAct.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if updated.Status != ActivityStatusFailed {
		t.Fatalf("status = %q, want failed", updated.Status)
	}
	if !strings.Contains(updated.Summary, "execution error") {
		t.Fatalf("summary = %q, want it to contain the run error", updated.Summary)
	}
}

func TestActivityService_WorkflowRunCompleted_UpdatesActivity(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)

	_, agentAct, err := f.service.CreateUserActivity(ctx, "task-1", "test", CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	if err := f.service.OnWorkflowRunCompleted(ctx, agentAct.RunID, "已完成：修好了按钮"); err != nil {
		t.Fatalf("OnWorkflowRunCompleted: %v", err)
	}
	updated, err := f.service.activities.Get(ctx, agentAct.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if updated.Status != ActivityStatusCompleted {
		t.Fatalf("status = %q, want completed", updated.Status)
	}
	if updated.Summary != "已完成：修好了按钮" {
		t.Fatalf("summary = %q", updated.Summary)
	}
}

func TestActivityService_WorkflowRunFailure_UnknownRun(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)

	if err := f.service.OnWorkflowRunFailed(ctx, "run-does-not-exist", "boom"); !errors.Is(err, ErrActivityNotFound) {
		t.Fatalf("err = %v, want ErrActivityNotFound", err)
	}
}

// Step 1 migration: an existing workflow_runs table must gain triggered_by and
// activity_id without losing its rows. This is the Go-migration equivalent of
// the plan's 007_workflow_runs_activity.sql (this repo has no SQL migrations).
func TestMigrationWorkflowRunsActivityColumns(t *testing.T) {
	root := fs.NewRootInfo("legacy-run-root", "legacy-run-root", t.TempDir())
	path, err := taskDBPath(root)
	if err != nil {
		t.Fatalf("taskDBPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create db dir: %v", err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	if _, err := raw.Exec(`
CREATE TABLE workflow_runs (
 id TEXT PRIMARY KEY, task_id TEXT NOT NULL, version TEXT NOT NULL DEFAULT '',
 revision INTEGER NOT NULL DEFAULT 1, graph_json TEXT NOT NULL,
 node_states_json TEXT NOT NULL DEFAULT '{}', node_outputs_json TEXT NOT NULL DEFAULT '{}',
 selected_routes_json TEXT NOT NULL DEFAULT '{}', loop_state_json TEXT NOT NULL DEFAULT '{}',
 loop_exit_expr_json TEXT NOT NULL DEFAULT '{}', created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
INSERT INTO workflow_runs(id,task_id,version,revision,graph_json,created_at,updated_at)
VALUES('legacy-run','legacy-task','',1,'{"nodes":[{"id":"n","kind":"agent"}]}','2026-11-10T00:00:00Z','2026-11-10T00:00:00Z');
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
	defer store.Close()
	columns := tableColumns(t, store.db, "workflow_runs")
	for _, col := range []string{"triggered_by", "activity_id"} {
		if !columns[col] {
			t.Fatalf("workflow_runs missing migrated column %q", col)
		}
	}
	old, err := store.WorkflowRunByID(context.Background(), "legacy-run")
	if err != nil {
		t.Fatalf("legacy run lost: %v", err)
	}
	if old.TaskID != "legacy-task" || old.TriggeredBy != "" || old.ActivityID != "" {
		t.Fatalf("legacy run = %#v, want preserved row with empty origin", old)
	}
}

// 已结束任务不再接受新执行；必须给出可见原因，而不是建一条永远不推进的 Run。
func TestActivityService_NewMessageOnTerminalTaskIsRejected(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)
	f.wireRunLifecycle(t)
	if err := f.tasks.UpdateTaskStatus(ctx, "task-1", StatusSuccess, true); err != nil {
		t.Fatalf("UpdateTaskStatus: %v", err)
	}

	_, agentAct, err := f.service.CreateUserActivity(ctx, "task-1", "再来一轮", CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	if agentAct.Status != ActivityStatusFailed || !strings.Contains(agentAct.Summary, "已结束") {
		t.Fatalf("agent reply = %#v, want a visible 'task closed' refusal", agentAct)
	}
	runs, err := f.tasks.ListWorkflowRunsForTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("ListWorkflowRunsForTask: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("terminal task persisted %d runs, want 0", len(runs))
	}
}
