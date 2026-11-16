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

func (b *recordingActivityBroadcaster) updatedIDs() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.updated...)
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

// ActivityServiceForRoot 必须把装配好的 workflow LLM 传给推荐器；否则任务创建
// 永远走「第一个可用 workflow」的默认路径，自动选择等于没实现。
func TestServiceActivityServiceForRootWiresWorkflowLLM(t *testing.T) {
	f := setupActivityService(t)
	svc := NewService(testRoots{root: f.root})
	svc.Workflows = f.workflows
	svc.SetWorkflowLLM(fakeWorkflowLLM{response: `{"workflow_id":"workflow-feature","reason":"新需求","match_score":0.9}`})

	activitySvc, err := svc.ActivityServiceForRoot(f.root.ID)
	if err != nil {
		t.Fatalf("ActivityServiceForRoot: %v", err)
	}
	rec, err := activitySvc.recommender.RecommendWorkflow(context.Background(), "新增一个页面")
	if err != nil {
		t.Fatalf("RecommendWorkflow: %v", err)
	}
	if rec.WorkflowID != "workflow-feature" {
		t.Fatalf("workflow_id = %q, want workflow-feature (LLM must be wired)", rec.WorkflowID)
	}
}

type fakeWorkflowLLM struct {
	response string
	err      error
}

func (f fakeWorkflowLLM) Call(context.Context, string) (string, error) {
	return f.response, f.err
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

// seedRunState 造出「上一轮已经存在」的现场：一个绑定的 Run、一条对应
// NodeRun，以及任务状态。nodeState 写进 Run 投影，nodeRunStatus 写进
// NodeRun 行（两者在真实路径里由同一终态投影产生）。
func (f *activityServiceFixture) seedRunState(t *testing.T, nodeState, nodeRunStatus, taskStatus string) (*WorkflowRun, *TaskActivity) {
	t.Helper()
	ctx := context.Background()
	_, agentAct, err := f.service.CreateUserActivity(ctx, "task-1", "跑一下", CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		t.Fatalf("seed CreateUserActivity: %v", err)
	}
	run, err := f.tasks.WorkflowRunByID(ctx, agentAct.RunID)
	if err != nil {
		t.Fatalf("WorkflowRunByID: %v", err)
	}
	run.NodeStates["agent-1"] = nodeState
	if err := f.tasks.SaveWorkflowRun(ctx, run); err != nil {
		t.Fatalf("SaveWorkflowRun: %v", err)
	}
	if err := f.tasks.UpsertWorkflowNodeRun(ctx, NodeRun{
		ID: run.ID + ":agent-1::1:1", RunID: run.ID, NodeID: "agent-1",
		Attempt: 1, Status: nodeRunStatus, FinishedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertWorkflowNodeRun: %v", err)
	}
	if err := f.tasks.UpdateTaskStatus(ctx, "task-1", taskStatus, isTerminalStatus(taskStatus)); err != nil {
		t.Fatalf("UpdateTaskStatus: %v", err)
	}
	if err := f.service.activities.UpdateStatus(ctx, agentAct.ID, ActivityStatusWaitingUser, nil); err != nil {
		t.Fatalf("seed activity status: %v", err)
	}
	return run, agentAct
}

func (f *activityServiceFixture) wireRunLifecycle(t *testing.T) {
	t.Helper()
	// 生产装配是 ActivityServiceForRoot 把长生命周期的 Service 挂进来；这里只
	// 挂上状态机本身，不装 executor，避免后台 driver 干扰断言。
	f.service.SetRunLifecycle(NewService(testRoots{root: f.root}))
}

// 用户在一个停在 waiting_user（节点 unknown）的任务上继续发消息时，必须新开
// 一轮：旧 Run 冻结为 superseded，新 Run 绑定到任务，旧回复结算为 cancelled。
func TestActivityService_NewMessageSupersedesWaitingRun(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)
	f.wireRunLifecycle(t)
	oldRun, oldReply := f.seedRunState(t, NodeRunUnknown, NodeRunUnknown, StatusWaitingUser)

	_, agentAct, err := f.service.CreateUserActivity(ctx, "task-1", "换个方法再试", CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	if agentAct.Status == ActivityStatusFailed {
		t.Fatalf("continuation failed: %s", agentAct.Summary)
	}
	if agentAct.RunID == "" || agentAct.RunID == oldRun.ID {
		t.Fatalf("new turn run = %q, want a fresh run (old %q)", agentAct.RunID, oldRun.ID)
	}

	frozen, err := f.tasks.WorkflowRunByID(ctx, oldRun.ID)
	if err != nil {
		t.Fatalf("reload old run: %v", err)
	}
	if got := ProjectStatusFromRun(frozen); got != StatusCancelled {
		t.Fatalf("old run projects as %q, want cancelled", got)
	}
	events, err := f.tasks.ListWorkflowRunEvents(ctx, oldRun.ID)
	if err != nil {
		t.Fatalf("ListWorkflowRunEvents: %v", err)
	}
	if len(events) == 0 || events[len(events)-1].Type != WorkflowEventRunSuperseded {
		t.Fatalf("old run events = %#v, want a trailing run_superseded", events)
	}

	stored, err := f.service.activities.Get(ctx, oldReply.ID)
	if err != nil {
		t.Fatalf("reload old reply: %v", err)
	}
	if stored.Status != ActivityStatusCancelled {
		t.Fatalf("old reply status = %q, want cancelled", stored.Status)
	}

	task, err := f.tasks.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status == StatusWaitingUser || isTerminalStatus(task.Status) {
		t.Fatalf("task status = %q, want a fresh, drivable status", task.Status)
	}
	engine, err := taskEngineOf(ctx, f.tasks.db, "task-1")
	if err != nil {
		t.Fatalf("taskEngineOf: %v", err)
	}
	if engine.WorkflowRunID != agentAct.RunID {
		t.Fatalf("engine bound to %q, want the new run %q", engine.WorkflowRunID, agentAct.RunID)
	}
}

// 节点仍在跑时，新消息不能开第二个非终态 Run：占位回复给出可见拒绝，
// 仍只有一条 Run。
func TestActivityService_NewMessageWhileNodeRunningIsRejected(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)
	f.wireRunLifecycle(t)
	f.seedRunState(t, NodeRunRunning, NodeRunRunning, StatusRunning)

	_, agentAct, err := f.service.CreateUserActivity(ctx, "task-1", "插一句", CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	if agentAct.Status != ActivityStatusFailed || !strings.Contains(agentAct.Summary, "还在执行") {
		t.Fatalf("agent reply = %#v, want a visible 'still running' refusal", agentAct)
	}
	runs, err := f.tasks.ListWorkflowRunsForTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("ListWorkflowRunsForTask: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want the single in-flight run", len(runs))
	}
}

// HumanGate 停住时留言不能绕过审批：拒绝并提示去批准/驳回。
func TestActivityService_NewMessageWhileHumanGateWaitsIsRejected(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)
	f.wireRunLifecycle(t)
	run, _ := f.seedRunState(t, NodeRunWaitingUser, NodeRunWaitingUser, StatusWaitingUser)

	running, err := f.tasks.WorkflowRunByID(ctx, run.ID)
	if err != nil {
		t.Fatalf("reload run: %v", err)
	}
	running.Graph = WorkflowDefinition{Nodes: []WorkflowNode{{ID: "gate-1", Kind: NodeKindHumanGate}}}
	running.NodeStates = map[string]string{"gate-1": NodeRunWaitingUser}
	if err := f.tasks.SaveWorkflowRun(ctx, running); err != nil {
		t.Fatalf("SaveWorkflowRun: %v", err)
	}
	if err := f.tasks.UpsertWorkflowNodeRun(ctx, NodeRun{
		ID: run.ID + ":gate-1::1:1", RunID: run.ID, NodeID: "gate-1",
		Attempt: 1, Status: NodeRunWaitingUser, FinishedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertWorkflowNodeRun: %v", err)
	}

	_, agentAct, err := f.service.CreateUserActivity(ctx, "task-1", "先跳过审批", CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	if agentAct.Status != ActivityStatusFailed || !strings.Contains(agentAct.Summary, "审批") {
		t.Fatalf("agent reply = %#v, want an approval-pointing refusal", agentAct)
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

// 一轮成功后的回复要给人看得懂的结果，而不是把节点 output 原样 JSON 出来
// （2026-11-14 隔离实例里用户看到的就是 {"files":[...]} 这种转储）。
func TestWorkflowReplySummaryUsesReadableOutputText(t *testing.T) {
	cases := []struct {
		name   string
		output map[string]any
		want   string
	}{
		{name: "summary wins", output: map[string]any{"summary": "已完成", "result": "次要"}, want: "已完成"},
		{name: "result is human text", output: map[string]any{"result": "登录页已经改好了", "files": []any{"a.ts"}}, want: "登录页已经改好了"},
		{name: "message key", output: map[string]any{"message": "全部通过"}, want: "全部通过"},
		{name: "lone string value", output: map[string]any{"note": "只剩一句"}, want: "只剩一句"},
		// 执行器会往 output 里塞元数据（outcome/reason/worktree_*），它们不是给人
		// 看的结果，参与「唯一字符串」判断会让真实正文永远被判为歧义。
		{name: "envelope outcome is metadata", output: map[string]any{"outcome": "done", "sentence": "本次演示已完成"}, want: "本次演示已完成"},
		{name: "empty reason is metadata", output: map[string]any{"outcome": "done", "reason": "", "note": "只有这句"}, want: "只有这句"},
		{name: "worktree error is metadata", output: map[string]any{"outcome": "done", "worktree_error": "merge failed", "note": "结果"}, want: "结果"},
		// 真实样本（2026-11-16 任务 #66）：模型把结果放在 changes 字符串数组里，
		// 顶层只有 outcome 元数据和 verification 对象。数组不能参与「输出原样
		// JSON」的转储，但每条都是给人看的变更说明，必须成为这一轮的总结。
		{
			name: "changes list becomes the summary",
			output: map[string]any{
				"changes":      []any{"去掉与发布重复的按钮", "新增删除工作流入口"},
				"outcome":      "done",
				"verification": map[string]any{"go_tests": "pass", "web_tests": "pass"},
			},
			want: "• 去掉与发布重复的按钮\n• 新增删除工作流入口",
		},
		{
			name:   "single change item",
			output: map[string]any{"changes": []string{"只改了一处"}, "outcome": "done"},
			want:   "• 只改了一处",
		},
		{name: "never raw json", output: map[string]any{"files": []any{"a.ts"}, "count": float64(2)}, want: ""},
		// 真实样本：节点 output 没有 canonical 键时，把文本字段按 key 列出来；
		// 只报一句、或用「最长」猜一条都会丢掉模型实际汇报的另一个要点。
		{name: "multiple strings render with keys", output: map[string]any{"a": "x", "b": "y"}, want: "a: x；b: y"},
		{name: "keys are sorted for determinism", output: map[string]any{"file": "a.txt", "content": "hello"}, want: "content: hello；file: a.txt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			f := setupActivityService(t)
			run, _ := f.seedRunState(t, NodeRunSuccess, NodeRunSuccess, StatusSuccess)
			if err := f.tasks.UpsertWorkflowNodeRun(ctx, NodeRun{
				ID: run.ID + ":agent-1::1:1", RunID: run.ID, NodeID: "agent-1",
				Attempt: 1, Status: NodeRunSuccess, Output: tc.output, FinishedAt: time.Now().UTC(),
			}); err != nil {
				t.Fatalf("UpsertWorkflowNodeRun: %v", err)
			}
			if got := workflowReplySummary(ctx, f.tasks, run); got != tc.want {
				t.Fatalf("summary = %q, want %q", got, tc.want)
			}
		})
	}
}

// 旧版本已经结算过的回复（summary 为空）在升级后打开仍应看到结果，而不是
// 永远停在「已完成 <workflow>」。启动恢复只补这一类行，且幂等。
func TestBackfillWorkflowReplySummariesForCompletedRuns(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)
	run, act := f.seedRunState(t, NodeRunSuccess, NodeRunSuccess, StatusSuccess)
	if err := f.tasks.UpsertWorkflowNodeRun(ctx, NodeRun{
		ID: run.ID + ":agent-1::1:1", RunID: run.ID, NodeID: "agent-1",
		Attempt: 1, Status: NodeRunSuccess, FinishedAt: time.Now().UTC(),
		Output: map[string]any{"changes": []any{"修复了 A", "补了 B"}, "outcome": "done"},
	}); err != nil {
		t.Fatalf("UpsertWorkflowNodeRun: %v", err)
	}
	empty := ""
	if err := f.service.activities.UpdateStatus(ctx, act.ID, ActivityStatusCompleted, &empty); err != nil {
		t.Fatalf("seed empty summary: %v", err)
	}

	svc := NewService(testRoots{root: f.root})
	svc.backfillWorkflowReplySummaries(ctx, f.tasks)

	stored, err := f.service.activities.Get(ctx, act.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Summary != "• 修复了 A\n• 补了 B" {
		t.Fatalf("summary = %q, want readable changes", stored.Summary)
	}

	// 二次执行不再改行（幂等）。
	svc.backfillWorkflowReplySummaries(ctx, f.tasks)
	again, err := f.service.activities.Get(ctx, act.ID)
	if err != nil {
		t.Fatalf("Get again: %v", err)
	}
	if again.Summary != stored.Summary || !again.UpdatedAt.Equal(stored.UpdatedAt) {
		t.Fatalf("backfill must be idempotent: before=%#v after=%#v", stored, again)
	}
}

// 运行期的「步骤 i/n」正文在结算时必须清掉：终态文案由 summary 提供，
// 留着旧进度会被前端当成结果展示。
func TestSettleWorkflowReplyClearsStaleProgressBody(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)
	run, act := f.seedRunState(t, NodeRunSuccess, NodeRunSuccess, StatusSuccess)
	if err := f.service.activities.UpdateBody(ctx, act.ID, "步骤 2/3: 实现"); err != nil {
		t.Fatalf("UpdateBody: %v", err)
	}
	if err := f.tasks.UpsertWorkflowNodeRun(ctx, NodeRun{
		ID: run.ID + ":agent-1::1:1", RunID: run.ID, NodeID: "agent-1",
		Attempt: 1, Status: NodeRunSuccess, Output: map[string]any{"result": "已经改好了"}, FinishedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertWorkflowNodeRun: %v", err)
	}

	// 生产里由 driver 的 projectWorkflowTaskStatus 调到这里；单测直接走同一入口。
	NewService(testRoots{root: f.root}).settleWorkflowReply(ctx, f.tasks, run)

	stored, err := f.service.activities.Get(ctx, act.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Status != ActivityStatusCompleted {
		t.Fatalf("status = %q, want completed", stored.Status)
	}
	if stored.Summary != "已经改好了" {
		t.Fatalf("summary = %q, want the readable node result", stored.Summary)
	}
	if stored.Body != "" {
		t.Fatalf("body = %q, want stale progress cleared", stored.Body)
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
