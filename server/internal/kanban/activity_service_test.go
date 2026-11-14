package kanban

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mindfs/server/internal/fs"
)

// Task 5 (Phase 2): ActivityService and workflow integration.
//
// The plan sketches an ActivityService that calls WorkflowStore.CreateRun/Execute.
// In this repository workflow runs are data-plane records owned by TaskStore and
// executed by the existing workflow runtime (Service + driver), so ActivityService
// resolves the version through the control-plane WorkflowStore and persists the
// run in the task's TaskStore. Execution is an injected port; without it the agent
// activity stays pending until the runtime reports a terminal state through
// OnWorkflowRunFailed / OnWorkflowRunCompleted (Review Focus #3).
//
// Tests use plain testing and real stores, matching the rest of the package.

type recordingActivityBroadcaster struct {
	mu      sync.Mutex
	created []string
	updated []string
}

func (b *recordingActivityBroadcaster) ActivityCreated(a *TaskActivity) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.created = append(b.created, a.ID)
}

func (b *recordingActivityBroadcaster) ActivityUpdated(a *TaskActivity, _ map[string]any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.updated = append(b.updated, a.ID)
}

func (b *recordingActivityBroadcaster) createdIDs() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.created...)
}

type activityServiceFixture struct {
	service   *ActivityService
	tasks     *TaskStore
	workflows *WorkflowStore
	root      fs.RootInfo
	broadcast *recordingActivityBroadcaster
}

// setupActivityService builds a task node with one task ("task-1") and two
// published workflows ("workflow-debug", "workflow-feature"). With a nil LLM the
// recommender deterministically picks "workflow-debug" (lowest id).
func setupActivityService(t *testing.T) *activityServiceFixture {
	t.Helper()
	ctx := context.Background()
	root := fs.NewRootInfo("root", "root", t.TempDir())
	tasks, err := NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	t.Cleanup(func() { _ = tasks.Close() })
	if _, err := tasks.CreateTask(ctx,
		Task{ID: "task-1", RootID: root.ID, Status: StatusPending},
		TaskEvent{ID: "event-1", TaskID: "task-1", Type: "created"},
	); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	workflows := newWorkflowStore(t.TempDir())
	for _, id := range []string{"workflow-debug", "workflow-feature"} {
		if _, err := workflows.CreateDraft(ctx, id, "", "tester", wfDef(wfNode("agent-1", NodeKindAgent))); err != nil {
			t.Fatalf("CreateDraft(%s): %v", id, err)
		}
		if _, err := workflows.PublishDraft(ctx, id, "r1", "tester"); err != nil {
			t.Fatalf("PublishDraft(%s): %v", id, err)
		}
	}

	svc := NewActivityService(tasks, workflows, NewWorkflowRecommender(workflows, nil))
	broadcast := &recordingActivityBroadcaster{}
	svc.SetBroadcaster(broadcast)
	return &activityServiceFixture{service: svc, tasks: tasks, workflows: workflows, root: root, broadcast: broadcast}
}

func TestActivityService_CreateUserActivity_WithWorkflow(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)

	userAct, agentAct, err := f.service.CreateUserActivity(ctx, "task-1", "修复 bug", CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	if userAct == nil || agentAct == nil {
		t.Fatalf("activities = %#v / %#v, want both", userAct, agentAct)
	}
	if userAct.Type != ActivityTypeUserMessage || userAct.Role != RoleUser {
		t.Fatalf("user activity type/role = %s/%s", userAct.Type, userAct.Role)
	}
	if userAct.Body != "修复 bug" {
		t.Fatalf("user body = %q", userAct.Body)
	}
	if agentAct.Type != ActivityTypeAgentReply || agentAct.Role != RoleAgent {
		t.Fatalf("agent activity type/role = %s/%s", agentAct.Type, agentAct.Role)
	}
	if agentAct.Status != ActivityStatusPending {
		t.Fatalf("agent status = %q, want pending", agentAct.Status)
	}
	if agentAct.RunID == "" {
		t.Fatal("agent activity has no workflow run id")
	}
	if agentAct.WorkflowVersionID != "workflow-debug:v1" {
		t.Fatalf("workflow version = %q, want workflow-debug:v1 (recommender default)", agentAct.WorkflowVersionID)
	}
	if agentAct.WorkflowChoice != CommentWorkflowChoiceEmpty {
		t.Fatalf("workflow choice = %q, want empty", agentAct.WorkflowChoice)
	}

	// The run must be persisted on the task node with its origin recorded: the
	// triggering user activity and the trigger source (plan step 1).
	run, err := f.tasks.WorkflowRunByID(ctx, agentAct.RunID)
	if err != nil {
		t.Fatalf("WorkflowRunByID: %v", err)
	}
	if run.TriggeredBy != WorkflowTriggeredByUser {
		t.Fatalf("run triggered_by = %q, want user", run.TriggeredBy)
	}
	if run.ActivityID != userAct.ID {
		t.Fatalf("run activity_id = %q, want triggering user activity %q", run.ActivityID, userAct.ID)
	}

	// Both activities must exist in the conversation store and be broadcast.
	stored, err := f.service.activities.Get(ctx, agentAct.ID)
	if err != nil {
		t.Fatalf("Get agent activity: %v", err)
	}
	if stored.Status != ActivityStatusPending || stored.RunID != agentAct.RunID {
		t.Fatalf("stored agent activity = %#v", stored)
	}
	if got := len(f.broadcast.createdIDs()); got != 2 {
		t.Fatalf("broadcast created = %d, want 2 (user + agent)", got)
	}
}

func TestActivityService_CreateUserActivity_SpecifiedWorkflow(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)

	versionID := "workflow-feature:v1"
	_, agentAct, err := f.service.CreateUserActivity(ctx, "task-1", "加一个登录接口", CommentWorkflowChoiceSpecified, &versionID)
	if err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	if agentAct.WorkflowVersionID != versionID {
		t.Fatalf("workflow version = %q, want %q (specified must win over recommendation)", agentAct.WorkflowVersionID, versionID)
	}
	if agentAct.WorkflowChoice != CommentWorkflowChoiceSpecified {
		t.Fatalf("workflow choice = %q, want specified", agentAct.WorkflowChoice)
	}
}

// 选择器给的是 workflow id + name；只传 workflow id 时必须解析到最新发布版本，
// 而不是当成版本 id 查不到就失败。
func TestActivityService_CreateUserActivity_SpecifiedByWorkflowID(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)

	workflowID := "workflow-feature"
	_, agentAct, err := f.service.CreateUserActivity(ctx, "task-1", "加一个登录接口", CommentWorkflowChoiceSpecified, &workflowID)
	if err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	if agentAct.WorkflowVersionID != "workflow-feature:v1" {
		t.Fatalf("workflow version = %q, want latest published version", agentAct.WorkflowVersionID)
	}
	if agentAct.Status != ActivityStatusPending {
		t.Fatalf("agent status = %q, want pending with a persisted run", agentAct.Status)
	}
}

func TestActivityService_CreateUserActivity_Disabled(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)

	_, agentAct, err := f.service.CreateUserActivity(ctx, "task-1", "手动执行", CommentWorkflowChoiceDisabled, nil)
	if err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	if agentAct == nil {
		t.Fatal("disabled choice must still create an agent activity")
	}
	if agentAct.RunID != "" {
		t.Fatalf("disabled choice created a run %q, want none", agentAct.RunID)
	}
	if agentAct.WorkflowChoice != CommentWorkflowChoiceDisabled {
		t.Fatalf("workflow choice = %q, want disabled", agentAct.WorkflowChoice)
	}
	// 没装配直连执行端口时必须显式失败，不能留下永久 pending 的占位回复。
	stored, err := f.service.activities.Get(ctx, agentAct.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Status != ActivityStatusFailed || !strings.Contains(stored.Summary, "direct executor") {
		t.Fatalf("stored activity = %#v, want failed with direct executor error", stored)
	}
	runs, err := f.tasks.ListWorkflowRunsForTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("ListWorkflowRunsForTask: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("disabled choice persisted %d runs, want 0", len(runs))
	}
}

type fakeDirectExecutor struct {
	reply string
	err   error
	got   string
	done  chan struct{}
}

func (f *fakeDirectExecutor) RunDirectActivity(_ context.Context, _ Task, body string) (string, error) {
	f.got = body
	defer close(f.done)
	return f.reply, f.err
}

func TestActivityService_CreateUserActivity_Disabled_WithExecutor(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)
	direct := &fakeDirectExecutor{reply: "好的，已处理", done: make(chan struct{})}
	f.service.SetDirectExecutor(direct)

	_, agentAct, err := f.service.CreateUserActivity(ctx, "task-1", "手动执行", CommentWorkflowChoiceDisabled, nil)
	if err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	select {
	case <-direct.done:
	case <-time.After(2 * time.Second):
		t.Fatal("direct executor was never invoked")
	}
	if direct.got != "手动执行" {
		t.Fatalf("executor body = %q, want the user message", direct.got)
	}
	var stored *TaskActivity
	for i := 0; i < 50; i++ {
		stored, err = f.service.activities.Get(ctx, agentAct.ID)
		if err == nil && stored.Status == ActivityStatusCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if stored == nil || stored.Status != ActivityStatusCompleted || stored.Body != "好的，已处理" {
		t.Fatalf("stored activity = %#v, want completed with the reply as body", stored)
	}
}

// Router 拥有对话流：节点进度只改这一轮回复的正文，绝不新增活动行。
func TestService_WorkflowReplyProgressStaysOnRouterReply(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)

	userAct, agentAct, err := f.service.CreateUserActivity(ctx, "task-1", "跑一下", CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	run, err := f.tasks.WorkflowRunByID(ctx, agentAct.RunID)
	if err != nil {
		t.Fatalf("WorkflowRunByID: %v", err)
	}
	run.Graph = WorkflowDefinition{Nodes: []WorkflowNode{
		{ID: "step-1", Name: "分析", Kind: NodeKindNotify},
		{ID: "step-2", Name: "收尾", Kind: NodeKindNotify},
	}}
	if err := f.service.activities.UpdateStatus(ctx, agentAct.ID, ActivityStatusRunning, nil); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	svc := NewService(testRoots{root: f.root})
	svc.SetActivityBroadcaster(f.broadcast)
	svc.updateWorkflowReplyProgress(ctx, f.service.tasks, run, "step-1")

	stored, err := f.service.activities.Get(ctx, agentAct.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Body != "步骤 1/2: 分析" {
		t.Fatalf("body = %q, want 步骤 1/2: 分析", stored.Body)
	}
	list, err := f.service.activities.List(ctx, "task-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("activities = %d, want 2 (user + one router reply); node progress must not append rows", len(list))
	}
	if list[0].ID != userAct.ID || list[1].ID != agentAct.ID {
		t.Fatalf("unexpected activity order: %#v", list)
	}
}

// 任务进入真实终态时，Router 把结果结算到这一轮的回复上（completed/failed/
// waiting_user）。运行中不结算。
func TestService_ProjectWorkflowTaskStatusSettlesRouterReply(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name       string
		nodeState  string
		nodeStatus string
		output     map[string]any
		wantStatus string
		wantInSum  string
	}{
		{"success", NodeRunSuccess, NodeRunSuccess, map[string]any{"summary": "改完了"}, ActivityStatusCompleted, "改完了"},
		{"fail", NodeRunFail, NodeRunFail, map[string]any{"error": "测试没过"}, ActivityStatusFailed, "测试没过"},
		{"waiting_user", NodeRunWaitingUser, NodeRunWaitingUser, map[string]any{"reason": "缺权限"}, ActivityStatusWaitingUser, "缺权限"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := setupActivityService(t)
			_, agentAct, err := f.service.CreateUserActivity(ctx, "task-1", "跑一下", CommentWorkflowChoiceEmpty, nil)
			if err != nil {
				t.Fatalf("CreateUserActivity: %v", err)
			}
			run, err := f.tasks.WorkflowRunByID(ctx, agentAct.RunID)
			if err != nil {
				t.Fatalf("WorkflowRunByID: %v", err)
			}
			if err := f.service.activities.UpdateStatus(ctx, agentAct.ID, ActivityStatusRunning, nil); err != nil {
				t.Fatalf("UpdateStatus: %v", err)
			}
			run.NodeStates["agent-1"] = tc.nodeState
			run.NodeOutputs["agent-1"] = tc.output
			if err := f.tasks.UpsertWorkflowNodeRun(ctx, NodeRun{
				ID: run.ID + ":agent-1::1:1", RunID: run.ID, NodeID: "agent-1",
				Attempt: 1, Status: tc.nodeStatus, Output: tc.output, FinishedAt: time.Now().UTC(),
			}); err != nil {
				t.Fatalf("UpsertWorkflowNodeRun: %v", err)
			}

			svc := NewService(testRoots{root: f.root})
			svc.SetActivityBroadcaster(f.broadcast)
			if err := svc.projectWorkflowTaskStatus(ctx, f.service.tasks, run); err != nil {
				t.Fatalf("projectWorkflowTaskStatus: %v", err)
			}
			stored, err := f.service.activities.Get(ctx, agentAct.ID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if stored.Status != tc.wantStatus {
				t.Fatalf("status = %q, want %q", stored.Status, tc.wantStatus)
			}
			if !strings.Contains(stored.Summary, tc.wantInSum) {
				t.Fatalf("summary = %q, want it to contain %q", stored.Summary, tc.wantInSum)
			}
		})
	}
}

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
