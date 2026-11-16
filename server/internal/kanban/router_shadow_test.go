package kanban

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── P0 Router：配置 / 迁移 / shadow 只读 ─────────────────────────────

func TestRouterConfigModePrecedence(t *testing.T) {
	config := RouterConfig{}.Normalized()
	if config.Mode != RouterModeOff {
		t.Fatalf("zero mode = %q, want off", config.Mode)
	}
	if config.Agent != defaultRouterAgent || config.Effort != defaultRouterEffort {
		t.Fatalf("defaults = agent %q effort %q", config.Agent, config.Effort)
	}
	if config.TurnTimeout != defaultRouterTurnTimeout || config.IdleClose != defaultRouterIdleClose {
		t.Fatalf("default durations = %s / %s", config.TurnTimeout, config.IdleClose)
	}

	config = RouterConfig{
		Mode:      RouterModeShadow,
		RootModes: map[string]RouterMode{"project-b": RouterModeOff, "project-a": "ROUTE", "": RouterModeShadow},
	}.Normalized()
	if config.ModeForRoot("project-a") != RouterModeRoute {
		t.Fatalf("root override = %q, want route", config.ModeForRoot("project-a"))
	}
	if config.ModeForRoot("project-b") != RouterModeOff {
		t.Fatalf("explicit off override lost: %q", config.ModeForRoot("project-b"))
	}
	if config.ModeForRoot("other") != RouterModeShadow {
		t.Fatalf("fallback mode = %q, want shadow", config.ModeForRoot("other"))
	}
	if _, ok := config.RootModes[""]; ok {
		t.Fatal("empty root id must be dropped")
	}

	unknown := RouterConfig{Mode: "bogus", RootModes: map[string]RouterMode{"x": "bogus"}}.Normalized()
	if unknown.ModeForRoot("x") != RouterModeOff {
		t.Fatalf("unknown root mode = %q, want off", unknown.ModeForRoot("x"))
	}
}

func TestRouterMigrationIdempotent(t *testing.T) {
	f := setupActivityService(t)
	if err := migrateRouterTables(f.tasks.db); err != nil {
		t.Fatalf("second migration: %v", err)
	}
	for _, table := range []string{"router_sessions", "router_decisions", "router_task_state"} {
		var name string
		err := f.tasks.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
		if err != nil {
			t.Fatalf("table %s: %v", table, err)
		}
	}
}

// fakeRouterAgent 模拟一次真实 Router 回合：调用 router_say 写工具，并返回
// 同一段文本；这样工具收集、校验、记账都走生产路径。
type fakeRouterAgent struct {
	mu       sync.Mutex
	requests []RouterAgentRequest
	action   string
}

func (f *fakeRouterAgent) RunRouterTurn(ctx context.Context, req RouterAgentRequest) (RouterAgentResult, error) {
	f.mu.Lock()
	f.requests = append(f.requests, req)
	action := f.action
	f.mu.Unlock()
	if action == "" {
		action = `{"body":"我先看看","purpose":"chat"}`
	}
	for _, tool := range req.Tools {
		if tool.Name != "router_say" {
			continue
		}
		if _, err := tool.Handler(ctx, json.RawMessage(action)); err != nil {
			return RouterAgentResult{}, err
		}
	}
	return RouterAgentResult{
		SessionKey: "router-test",
		FinalText:  "我先看看",
		Meta:       RouterTurnMeta{FinalText: "我先看看", PromptTokens: 21, CompletionTokens: 7},
	}, nil
}

func (f *fakeRouterAgent) CloseRouterSession(context.Context, string, string) error { return nil }

func (f *fakeRouterAgent) lastRequest() (RouterAgentRequest, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		return RouterAgentRequest{}, false
	}
	return f.requests[len(f.requests)-1], true
}

func TestRouterShadowDoesNotMutateAndSnapshotsBriefBeforeDispatch(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)
	svc, root := newTestService(t, &fakeRunner{})
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	if _, err := store.CreateTask(ctx, Task{ID: "task-1", RootID: root.ID, Status: StatusPending}, TaskEvent{ID: "e1", TaskID: "task-1", Type: "created"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	// 复用同一批 workflow 数据：借助 ActivityService fixture 的 store 让推荐器有货。
	activitySvc := NewActivityService(store, f.workflows, NewWorkflowRecommender(f.workflows, nil))
	agent := &fakeRouterAgent{}
	svc.SetRouterConfig(RouterConfig{Mode: RouterModeShadow, IdleClose: time.Hour})
	svc.SetRouterAgent(agent)
	router := svc.RouterServiceForRoot(root.ID)
	t.Cleanup(router.Close)
	activitySvc.SetRouter(router)

	userAct, agentAct, err := activitySvc.CreateUserActivity(ctx, "task-1", "修复登录", CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	if agentAct == nil || agentAct.RunID == "" {
		t.Fatalf("legacy dispatch did not create a run: %#v", agentAct)
	}
	waitForCondition(t, func() bool {
		decisions, err := NewRouterStore(store.db).ListDecisions(ctx, "task-1", 10, time.Time{})
		return err == nil && len(decisions) == 1
	})
	decisions, err := NewRouterStore(store.db).ListDecisions(ctx, "task-1", 10, time.Time{})
	if err != nil {
		t.Fatalf("ListDecisions: %v", err)
	}
	decision := decisions[0]
	if decision.Status != RouterDecisionShadow {
		t.Fatalf("status = %q, want shadow (err=%q)", decision.Status, decision.Error)
	}
	if len(decision.Actions) != 1 || decision.Actions[0].Kind != RouterActionSay || decision.Actions[0].Body != "我先看看" {
		t.Fatalf("actions = %#v", decision.Actions)
	}
	if decision.LegacyAction == nil || decision.LegacyAction.Kind != "workflow" || decision.LegacyAction.RunID != agentAct.RunID {
		t.Fatalf("legacy action = %#v", decision.LegacyAction)
	}
	if decision.Agreement != RouterAgreementMismatch {
		t.Fatalf("agreement = %q, want mismatch", decision.Agreement)
	}
	if decision.UserActivityID != userAct.ID || decision.PlaceholderActivityID != agentAct.ID {
		t.Fatalf("decision links = %q / %q", decision.UserActivityID, decision.PlaceholderActivityID)
	}
	if decision.PromptTokens != 21 || decision.CompletionTokens != 7 {
		t.Fatalf("tokens = %d/%d", decision.PromptTokens, decision.CompletionTokens)
	}
	if decision.RouterSessionKey != "router-test" {
		t.Fatalf("router_session_key = %q, want router-test", decision.RouterSessionKey)
	}

	// brief 必须是 legacy 分发前的世界：此时还没有 Run。
	var brief SituationBrief
	if err := json.Unmarshal([]byte(decision.BriefJSON), &brief); err != nil {
		t.Fatalf("decode brief: %v", err)
	}
	if brief.LatestRun != nil || brief.ActiveExecution != nil {
		t.Fatalf("brief was taken after legacy dispatch: run=%#v active=%#v", brief.LatestRun, brief.ActiveExecution)
	}
	if brief.Trigger.UserMessage == nil || brief.Trigger.UserMessage.Body != "修复登录" {
		t.Fatalf("brief trigger = %#v", brief.Trigger.UserMessage)
	}

	// shadow 不改用户可见数据：仍然只有一条 user + 一条 agent，一个 Run。
	activities, err := NewActivityStore(store.db).ListIncludingHidden(ctx, "task-1")
	if err != nil {
		t.Fatalf("List activities: %v", err)
	}
	if len(activities) != 2 {
		t.Fatalf("activities = %d, want 2 (shadow must not write activities)", len(activities))
	}

	// 同一 turn 再入队一次：唯一约束保证仍是一条决策。
	router.Enqueue(RouterTrigger{
		Kind: RouterTriggerUserMessage, TaskID: "task-1", RootID: root.ID,
		UserActivityID: userAct.ID, PlaceholderID: agentAct.ID,
		TurnID: "msg:" + userAct.ID, Brief: &brief,
		LegacyAction: decision.LegacyAction,
	})
	time.Sleep(100 * time.Millisecond)
	again, err := NewRouterStore(store.db).ListDecisions(ctx, "task-1", 10, time.Time{})
	if err != nil {
		t.Fatalf("ListDecisions again: %v", err)
	}
	if len(again) != 1 {
		t.Fatalf("decisions after duplicate enqueue = %d, want 1", len(again))
	}
}

func TestRouterModeOffDoesNotTouchRouterTables(t *testing.T) {
	ctx := context.Background()
	f := setupActivityService(t)
	svc, root := newTestService(t, &fakeRunner{})
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	if _, err := store.CreateTask(ctx, Task{ID: "task-1", RootID: root.ID, Status: StatusPending}, TaskEvent{ID: "e1", TaskID: "task-1", Type: "created"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	activitySvc := NewActivityService(store, f.workflows, NewWorkflowRecommender(f.workflows, nil))
	if router := svc.RouterServiceForRoot(root.ID); router != nil {
		t.Fatalf("mode=off must not create a Router lane: %#v", router)
	}
	activitySvc.SetRouter(nil)
	if _, agentAct, err := activitySvc.CreateUserActivity(ctx, "task-1", "修复登录", CommentWorkflowChoiceEmpty, nil); err != nil || agentAct == nil || agentAct.RunID == "" {
		t.Fatalf("legacy path changed under mode=off: agent=%#v err=%v", agentAct, err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM router_decisions`).Scan(&count); err != nil {
		t.Fatalf("count decisions: %v", err)
	}
	if count != 0 {
		t.Fatalf("router_decisions = %d, want 0", count)
	}
}

func TestRouterValidateMatrix(t *testing.T) {
	brief := &SituationBrief{
		Workflows: []RouterBriefWorkflow{{ID: "wf-1"}},
		ActiveExecution: &RouterBriefExecution{
			Kind:         "workflow",
			CurrentNodes: []RouterBriefNode{{NodeID: "n1"}},
		},
	}
	config := RouterConfig{}.Normalized()
	idleBrief := &SituationBrief{Workflows: []RouterBriefWorkflow{{ID: "wf-1"}}}
	cases := []struct {
		name    string
		brief   *SituationBrief
		actions []RouterAction
		wantErr string
	}{
		{"empty", brief, nil, "no actions"},
		{"ask plus say", brief, []RouterAction{{Kind: RouterActionAskUser, Question: "q"}, {Kind: RouterActionSay, Body: "x"}}, "only action"},
		{"two executions", idleBrief, []RouterAction{{Kind: RouterActionRunWorkflow, WorkflowID: "wf-1"}, {Kind: RouterActionMessageAgent, Body: "go"}}, "at most one"},
		{"unknown workflow", idleBrief, []RouterAction{{Kind: RouterActionRunWorkflow, WorkflowID: "nope"}}, "not available"},
		{"run while active", brief, []RouterAction{{Kind: RouterActionRunWorkflow, WorkflowID: "wf-1"}}, "active"},
		{"message while active", brief, []RouterAction{{Kind: RouterActionMessageAgent, Body: "go"}}, "active"},
		{"single say too large", idleBrief, []RouterAction{{Kind: RouterActionSay, Body: strings.Repeat("a", routerActionBodyMaxBytes+1)}}, "exceeds"},
		{"total bytes", idleBrief, []RouterAction{
			{Kind: RouterActionSay, Body: strings.Repeat("a", 3000)},
			{Kind: RouterActionSay, Body: strings.Repeat("b", 3000)},
			{Kind: RouterActionSay, Body: strings.Repeat("c", 3000)},
		}, "exceed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for index := range tc.actions {
				tc.actions[index].Seq = index
			}
			err := validateRouterActions(tc.brief, tc.actions, config)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want contains %q", err, tc.wantErr)
			}
		})
	}

	// deliver 只在唯一活动节点下合法。
	brief.ActiveExecution.CurrentNodes = []RouterBriefNode{{NodeID: "n1"}, {NodeID: "n2"}}
	if err := validateRouterActions(brief, []RouterAction{{Kind: RouterActionDeliver, Body: "x", Seq: 0}}, config); err == nil || !strings.Contains(err.Error(), "exactly one active node") {
		t.Fatalf("deliver with two active nodes: %v", err)
	}
}

// laneProbeAgent 记录每个任务同时在跑的回合数；release 关闭前所有回合都阻塞，
// 用来验证「同任务串行、跨任务并行」。
type laneProbeAgent struct {
	mu      sync.Mutex
	active  map[string]int
	maxSeen map[string]int
	started chan string
	release chan struct{}
}

func newLaneProbeAgent() *laneProbeAgent {
	return &laneProbeAgent{
		active:  map[string]int{},
		maxSeen: map[string]int{},
		started: make(chan string, 16),
		release: make(chan struct{}),
	}
}

func (a *laneProbeAgent) RunRouterTurn(ctx context.Context, req RouterAgentRequest) (RouterAgentResult, error) {
	a.mu.Lock()
	a.active[req.TaskID]++
	if a.active[req.TaskID] > a.maxSeen[req.TaskID] {
		a.maxSeen[req.TaskID] = a.active[req.TaskID]
	}
	a.mu.Unlock()
	a.started <- req.TaskID
	select {
	case <-a.release:
	case <-ctx.Done():
	}
	a.mu.Lock()
	a.active[req.TaskID]--
	a.mu.Unlock()
	return RouterAgentResult{SessionKey: "lane", Meta: RouterTurnMeta{FinalText: ""}}, nil
}

func (a *laneProbeAgent) CloseRouterSession(context.Context, string, string) error { return nil }

func (a *laneProbeAgent) maxFor(taskID string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.maxSeen[taskID]
}

func TestRouterLaneSerialPerTaskParallelAcrossTasks(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	for _, taskID := range []string{"task-a", "task-b"} {
		if _, err := store.CreateTask(ctx, Task{ID: taskID, RootID: root.ID, Status: StatusPending}, TaskEvent{ID: "e-" + taskID, TaskID: taskID, Type: "created"}); err != nil {
			t.Fatalf("CreateTask %s: %v", taskID, err)
		}
	}
	probe := newLaneProbeAgent()
	svc.SetRouterConfig(RouterConfig{Mode: RouterModeShadow, IdleClose: time.Hour})
	svc.SetRouterAgent(probe)
	router := svc.RouterServiceForRoot(root.ID)
	t.Cleanup(router.Close)

	brief := func(taskID string) *SituationBrief {
		return &SituationBrief{Version: 1, Task: RouterBriefTask{ID: taskID, RootID: root.ID}}
	}
	enqueue := func(taskID, turnID string) {
		router.Enqueue(RouterTrigger{
			Kind: RouterTriggerUserMessage, TaskID: taskID, RootID: root.ID,
			TurnID: turnID, Brief: brief(taskID),
		})
	}
	enqueue("task-a", "a1")
	enqueue("task-a", "a2")
	enqueue("task-b", "b1")

	// 前两个真正开始的回合必须来自两个不同任务：同任务的第二个被 lane 挡住。
	first := <-probe.started
	second := <-probe.started
	if first == second {
		t.Fatalf("same-task turns ran concurrently: %q then %q", first, second)
	}
	select {
	case third := <-probe.started:
		t.Fatalf("third turn started before release: %q", third)
	case <-time.After(100 * time.Millisecond):
	}

	close(probe.release)
	select {
	case <-probe.started:
	case <-time.After(2 * time.Second):
		t.Fatal("queued same-task turn never started after release")
	}
	waitForCondition(t, func() bool {
		decisions, err := NewRouterStore(store.db).ListDecisions(ctx, "task-a", 10, time.Time{})
		if err != nil || len(decisions) != 2 {
			return false
		}
		decisions, err = NewRouterStore(store.db).ListDecisions(ctx, "task-b", 10, time.Time{})
		return err == nil && len(decisions) == 1
	})
	if got := probe.maxFor("task-a"); got != 1 {
		t.Fatalf("task-a max concurrency = %d, want 1", got)
	}
	if got := probe.maxFor("task-b"); got != 1 {
		t.Fatalf("task-b max concurrency = %d, want 1", got)
	}
}

func TestRouterActivityPurposeBackfill(t *testing.T) {
	f := setupActivityService(t)
	ctx := context.Background()
	rows := []struct{ id, kind, runID string }{
		{"act-result", "agent_reply", "run-1"},
		{"act-reply", "agent_reply", ""},
		{"act-user", "user_comment", ""},
	}
	for _, row := range rows {
		if _, err := f.tasks.db.ExecContext(ctx, `
INSERT INTO task_activities (id, task_id, type, run_id, purpose, created_at)
VALUES (?, 'task-1', ?, ?, '', '2026-11-14T00:00:00Z')`, row.id, row.kind, row.runID); err != nil {
			t.Fatalf("seed %s: %v", row.id, err)
		}
	}
	if err := migrateTaskActivities(f.tasks.db); err != nil {
		t.Fatalf("rerun migration: %v", err)
	}
	want := map[string]string{"act-result": "result", "act-reply": "reply", "act-user": ""}
	for id, expected := range want {
		var purpose string
		if err := f.tasks.db.QueryRowContext(ctx, `SELECT purpose FROM task_activities WHERE task_id='task-1' AND id=?`, id).Scan(&purpose); err != nil {
			t.Fatalf("read %s: %v", id, err)
		}
		if purpose != expected {
			t.Fatalf("%s purpose = %q, want %q", id, purpose, expected)
		}
	}
	// 二次执行不改写已有值。
	if _, err := f.tasks.db.ExecContext(ctx, `UPDATE task_activities SET purpose='chat' WHERE id='act-result'`); err != nil {
		t.Fatal(err)
	}
	if err := migrateTaskActivities(f.tasks.db); err != nil {
		t.Fatalf("third migration: %v", err)
	}
	var purpose string
	if err := f.tasks.db.QueryRowContext(ctx, `SELECT purpose FROM task_activities WHERE id='act-result'`).Scan(&purpose); err != nil {
		t.Fatal(err)
	}
	if purpose != "chat" {
		t.Fatalf("backfill overwrote explicit purpose: %q", purpose)
	}
}

func TestRouterSessionKeySuffix(t *testing.T) {
	if got := RouterSessionKeyForTask("task-42"); got != "router-task-42" {
		t.Fatalf("key = %q", got)
	}
}

// 确保 sql 驱动与表在空库上也可查询（迁移顺序回归）。
func TestRouterDecisionStoreRoundTrip(t *testing.T) {
	f := setupActivityService(t)
	store := NewRouterStore(f.tasks.db)
	decision := &RouterDecision{
		ID: "d1", TaskID: "task-1", RootID: f.root.ID, TurnID: "t1",
		Status:       RouterDecisionShadow,
		LegacyAction: &RouterLegacyAction{Kind: "direct"},
	}
	if err := store.InsertDecision(context.Background(), decision); err != nil {
		t.Fatalf("InsertDecision: %v", err)
	}
	got, err := store.DecisionByTurn(context.Background(), "task-1", "t1")
	if err != nil {
		t.Fatalf("DecisionByTurn: %v", err)
	}
	if got.ID != "d1" || got.LegacyAction == nil || got.LegacyAction.Kind != "direct" {
		t.Fatalf("round trip = %#v", got)
	}
	if err := store.InsertDecision(context.Background(), decision); err != ErrRouterDecisionExists {
		t.Fatalf("duplicate insert err = %v, want ErrRouterDecisionExists", err)
	}
	if _, err := store.GetDecision(context.Background(), "missing"); err != sql.ErrNoRows && err != ErrRouterDecisionNotFound {
		t.Fatalf("missing decision err = %v", err)
	}
}
