package kanban

import (
	"context"
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

// 一轮成功结束后任务停在待审核。用户继续发消息是**新的一轮**，不是对上一轮
// 的取代：已完成的 Run、回复与摘要都必须原样保留，不能被冻结成 cancelled、
// 把用户刚看到的结果改写成「已取消」。
func TestActivityService_NewMessageAfterCompletedRunStartsFreshTurn(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)
	f.wireRunLifecycle(t)
	oldRun, oldReply := f.seedRunState(t, NodeRunSuccess, NodeRunSuccess, StatusRunning)
	output := map[string]any{"summary": "接口已经加好了"}
	oldRun.NodeOutputs["agent-1"] = output
	if err := f.tasks.SaveWorkflowRun(ctx, oldRun); err != nil {
		t.Fatalf("SaveWorkflowRun: %v", err)
	}
	if err := f.tasks.UpsertWorkflowNodeRun(ctx, NodeRun{
		ID: oldRun.ID + ":agent-1::1:1", RunID: oldRun.ID, NodeID: "agent-1",
		Attempt: 1, Status: NodeRunSuccess, Output: output, FinishedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertWorkflowNodeRun: %v", err)
	}
	projector := NewService(testRoots{root: f.root})
	projector.SetActivityBroadcaster(f.broadcast)
	if err := projector.projectWorkflowTaskStatus(ctx, f.service.tasks, oldRun); err != nil {
		t.Fatalf("projectWorkflowTaskStatus: %v", err)
	}
	settled, err := f.tasks.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if settled.Status != StatusWaitingUser || !strings.Contains(settled.ResultSummary, "接口已经加好了") {
		t.Fatalf("settled task = %#v, want waiting_user carrying the result summary", settled)
	}

	_, agentAct, err := f.service.CreateUserActivity(ctx, "task-1", "再加个测试", CommentWorkflowChoiceEmpty, nil)
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
	if got := ProjectStatusFromRun(frozen); got != StatusSuccess {
		t.Fatalf("old run projects as %q, want success (a finished turn is not superseded)", got)
	}
	events, err := f.tasks.ListWorkflowRunEvents(ctx, oldRun.ID)
	if err != nil {
		t.Fatalf("ListWorkflowRunEvents: %v", err)
	}
	for _, event := range events {
		if event.Type == WorkflowEventRunSuperseded {
			t.Fatalf("finished run got a run_superseded event: %#v", events)
		}
	}

	stored, err := f.service.activities.Get(ctx, oldReply.ID)
	if err != nil {
		t.Fatalf("reload old reply: %v", err)
	}
	if stored.Status != ActivityStatusCompleted || !strings.Contains(stored.Summary, "接口已经加好了") {
		t.Fatalf("old reply = %#v, want the completed result left untouched", stored)
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
