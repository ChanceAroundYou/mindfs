package kanban

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── P1 Router：route 模式真正接管 ─────────────────────────────────────
//
// 用户要求（2026-11-17）：Router 不再只是记录 shadow 样本，而是成为工头——
// 用户留言先交给 Router，由它决定回一句话、直连 agent、开 workflow、提问还是
// 静默；Router 不可用/给出非法动作时才回落到旧路径。

type scriptedRouterCall struct {
	tool string
	args map[string]any
}

// scriptedRouterAgent 按脚本顺序调用 Router 写入工具，走真实工具收集/校验链路。
type scriptedRouterAgent struct {
	mu       sync.Mutex
	calls    []scriptedRouterCall
	err      error
	final    string
	sessions []string
}

func (a *scriptedRouterAgent) RunRouterTurn(ctx context.Context, req RouterAgentRequest) (RouterAgentResult, error) {
	a.mu.Lock()
	calls := append([]scriptedRouterCall(nil), a.calls...)
	err := a.err
	final := a.final
	a.sessions = append(a.sessions, req.SessionKey)
	a.mu.Unlock()
	if err != nil {
		return RouterAgentResult{}, err
	}
	for _, call := range calls {
		var spec *RouterToolSpec
		for i := range req.Tools {
			if req.Tools[i].Name == call.tool {
				spec = &req.Tools[i]
				break
			}
		}
		if spec == nil {
			return RouterAgentResult{}, errors.New("tool not exposed: " + call.tool)
		}
		raw, err := json.Marshal(call.args)
		if err != nil {
			return RouterAgentResult{}, err
		}
		if _, err := spec.Handler(ctx, raw); err != nil {
			return RouterAgentResult{}, err
		}
	}
	if final == "" && len(calls) == 0 {
		final = "空回合"
	}
	return RouterAgentResult{
		SessionKey: "router-route-test",
		FinalText:  final,
		Meta:       RouterTurnMeta{FinalText: final, PromptTokens: 11, CompletionTokens: 5},
	}, nil
}

func (a *scriptedRouterAgent) CloseRouterSession(context.Context, string, string) error { return nil }

type routeFixture struct {
	svc        *Service
	rootID     string
	store      *TaskStore
	activity   *ActivityService
	workflows  *WorkflowStore
	agent      *scriptedRouterAgent
	direct     *fakeDirectExecutor
	nodeRunner *scriptedWorkflowNodeRunner
}

func newRouteFixture(t *testing.T, mode RouterMode) *routeFixture {
	t.Helper()
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	if _, err := store.CreateTask(ctx, Task{ID: "task-1", RootID: root.ID, Status: StatusPending},
		TaskEvent{ID: "task-1-event", TaskID: "task-1", Type: "created"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	workflows := newWorkflowStore(t.TempDir())
	for _, id := range []string{"workflow-debug", "workflow-feature"} {
		node := wfNode("agent-1", NodeKindAgent)
		node.Params["prompt"] = "执行任务"
		if _, err := workflows.CreateDraft(ctx, id, "", "tester", wfDef(node)); err != nil {
			t.Fatalf("CreateDraft(%s): %v", id, err)
		}
		if _, err := workflows.PublishDraft(ctx, id, "r1", "tester"); err != nil {
			t.Fatalf("PublishDraft(%s): %v", id, err)
		}
	}
	svc.Workflows = workflows
	agent := &scriptedRouterAgent{}
	direct := &fakeDirectExecutor{reply: "agent 直连回复", done: make(chan struct{})}
	nodeRunner := &scriptedWorkflowNodeRunner{outcomes: []WorkflowNodeResult{{Outcome: WorkflowNodeDone}}}
	svc.SetRouterConfig(RouterConfig{Mode: mode, IdleClose: time.Hour})
	svc.SetRouterAgent(agent)
	svc.SetDirectExecutor(direct)
	svc.SetWorkflowNodeRunner(nodeRunner)
	router := svc.RouterServiceForRoot(root.ID)
	t.Cleanup(router.Close)
	activity, err := svc.ActivityServiceForRoot(root.ID)
	if err != nil {
		t.Fatalf("ActivityServiceForRoot: %v", err)
	}
	return &routeFixture{svc: svc, rootID: root.ID, store: store, activity: activity, workflows: workflows, agent: agent, direct: direct, nodeRunner: nodeRunner}
}

func (f *routeFixture) waitDecision(t *testing.T, status RouterDecisionStatus) *RouterDecision {
	t.Helper()
	var last *RouterDecision
	waitForCondition(t, func() bool {
		decisions, err := NewRouterStore(f.store.db).ListDecisions(context.Background(), "task-1", 10, time.Time{})
		if err != nil || len(decisions) != 1 {
			return false
		}
		last = decisions[0]
		return last.Status == status
	})
	if last == nil {
		t.Fatal("no router decision recorded")
	}
	return last
}

func (f *routeFixture) reloadActivity(t *testing.T, id string) *TaskActivity {
	t.Helper()
	act, err := NewActivityStore(f.store.db).Get(context.Background(), id)
	if err != nil {
		t.Fatalf("load activity %s: %v", id, err)
	}
	return act
}

func (f *routeFixture) runs(t *testing.T) []*WorkflowRun {
	t.Helper()
	runs, err := f.store.ListWorkflowRunsForTask(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("ListWorkflowRunsForTask: %v", err)
	}
	return runs
}

func TestRouterRouteSayRepliesWithoutLegacyDispatch(t *testing.T) {
	f := newRouteFixture(t, RouterModeRoute)
	f.agent.calls = []scriptedRouterCall{{tool: "router_say", args: map[string]any{"body": "收到，我先梳理一下"}}}
	ctx := context.Background()

	_, placeholder, err := f.activity.CreateUserActivity(ctx, "task-1", "帮我改登录", CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	if placeholder == nil || placeholder.Status != ActivityStatusPending {
		t.Fatalf("placeholder = %#v, want a pending agent activity", placeholder)
	}
	decision := f.waitDecision(t, RouterDecisionApplied)
	if placeholder.ID != decision.PlaceholderActivityID {
		t.Fatalf("placeholder id = %q, decision placeholder = %q", placeholder.ID, decision.PlaceholderActivityID)
	}
	if decision.Agreement != RouterAgreementNoRouter || decision.LegacyAction != nil {
		t.Fatalf("route decision kept shadow fields: agreement=%q legacy=%#v", decision.Agreement, decision.LegacyAction)
	}
	act := f.reloadActivity(t, placeholder.ID)
	if act.Status != ActivityStatusCompleted || act.Body != "收到，我先梳理一下" {
		t.Fatalf("applied say = status %q body %q", act.Status, act.Body)
	}
	// 回了话就是「等人看」：任务头停在待审核，不能退回未开始（任务 #87）。
	task, err := f.store.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != StatusWaitingUser {
		t.Fatalf("task status = %q, want waiting_user after a router say", task.Status)
	}
	if runs := f.runs(t); len(runs) != 0 {
		t.Fatalf("route say started legacy runs: %#v", runs)
	}
	select {
	case <-f.direct.done:
		t.Fatal("say must not start a direct agent turn")
	default:
	}
	decisions, err := NewRouterStore(f.store.db).ListDecisions(ctx, "task-1", 10, time.Time{})
	if err != nil || len(decisions) != 1 {
		t.Fatalf("decisions = %d err=%v", len(decisions), err)
	}
	if decisions[0].AppliedSeq != 1 {
		t.Fatalf("applied_seq = %d, want 1", decisions[0].AppliedSeq)
	}
}

func TestRouterRouteRunWorkflowStartsRunWithPlaceholder(t *testing.T) {
	f := newRouteFixture(t, RouterModeRoute)
	f.agent.calls = []scriptedRouterCall{{
		tool: "router_run_workflow",
		args: map[string]any{"workflow_id": "workflow-feature", "reason": "多步骤需求"},
	}}
	ctx := context.Background()

	_, placeholder, err := f.activity.CreateUserActivity(ctx, "task-1", "做一个设置页", CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	decision := f.waitDecision(t, RouterDecisionApplied)
	if len(decision.Actions) != 1 || decision.Actions[0].Kind != RouterActionRunWorkflow {
		t.Fatalf("actions = %#v", decision.Actions)
	}
	runs := f.runs(t)
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
	if !strings.Contains(runs[0].Version, "workflow-feature") {
		t.Fatalf("run version = %q, want the router-chosen workflow", runs[0].Version)
	}
	act := f.reloadActivity(t, placeholder.ID)
	if act.RunID != runs[0].ID {
		t.Fatalf("placeholder run id = %q, want %q", act.RunID, runs[0].ID)
	}
	// 运行层真实跑完后占位回复必须是终态，而不是永远 pending。
	if err := f.svc.WaitWorkflowRunIdle(ctx, f.rootID, "task-1"); err != nil {
		t.Fatalf("WaitWorkflowRunIdle: %v", err)
	}
	waitForCondition(t, func() bool {
		act, err := NewActivityStore(f.store.db).Get(ctx, placeholder.ID)
		return err == nil && act.Status == ActivityStatusCompleted
	})
}

// session_mode：continue 把新一轮 Run 直接绑到任务最近在用的会话；缺省 new
// 留空，由第一个节点惰性建新会话（2026-11-20 用户决策）。
func TestRouterRouteRunWorkflowSessionMode(t *testing.T) {
	cases := []struct {
		name        string
		args        map[string]any
		wantMain    bool
		description string
	}{
		{
			name:        "continue binds the latest task session",
			args:        map[string]any{"workflow_id": "workflow-feature", "session_mode": "continue"},
			wantMain:    true,
			description: "continue 必须接续任务主会话",
		},
		{
			name:        "default starts a new session",
			args:        map[string]any{"workflow_id": "workflow-feature"},
			wantMain:    false,
			description: "默认 new 不能落在任务主会话里",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRouteFixture(t, RouterModeRoute)
			ctx := context.Background()
			if err := f.svc.SetTaskMainSessionKey(ctx, f.rootID, "task-1", "sess-main"); err != nil {
				t.Fatalf("SetTaskMainSessionKey: %v", err)
			}
			f.agent.calls = []scriptedRouterCall{{tool: "router_run_workflow", args: tc.args}}
			if _, _, err := f.activity.CreateUserActivity(ctx, "task-1", "接着上一轮做", CommentWorkflowChoiceEmpty, nil); err != nil {
				t.Fatalf("CreateUserActivity: %v", err)
			}
			f.waitDecision(t, RouterDecisionApplied)
			runs := f.runs(t)
			if len(runs) != 1 {
				t.Fatalf("runs = %d, want 1", len(runs))
			}
			// new 的会话是第一个节点惰性建的，这里只钉「不接续主会话」这条
			// 用户可见契约；建会话/写回 Run 由 prepare 层的用例覆盖。
			got := runs[0].SessionKey
			if tc.wantMain && got != "sess-main" {
				t.Fatalf("run session = %q, want sess-main (%s)", got, tc.description)
			}
			if !tc.wantMain && got == "sess-main" {
				t.Fatalf("run session = %q, must not reuse the task main session (%s)", got, tc.description)
			}
		})
	}
}

func TestRouterRouteMessageAgentUsesRouterBody(t *testing.T) {
	f := newRouteFixture(t, RouterModeRoute)
	f.agent.calls = []scriptedRouterCall{{
		tool: "router_message_agent",
		args: map[string]any{"body": "检查登录接口的超时配置", "reason": "直接改代码"},
	}}
	ctx := context.Background()

	_, placeholder, err := f.activity.CreateUserActivity(ctx, "task-1", "登录有问题", CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	f.waitDecision(t, RouterDecisionApplied)
	select {
	case <-f.direct.done:
	case <-time.After(2 * time.Second):
		t.Fatal("direct executor was not started by message_agent")
	}
	if f.direct.got != "检查登录接口的超时配置" {
		t.Fatalf("direct body = %q, want the router's body", f.direct.got)
	}
	waitForCondition(t, func() bool {
		act, err := NewActivityStore(f.store.db).Get(ctx, placeholder.ID)
		return err == nil && act.Status == ActivityStatusCompleted && act.Body == "agent 直连回复"
	})
	if runs := f.runs(t); len(runs) != 0 {
		t.Fatalf("message_agent must not create a run: %#v", runs)
	}
}

func TestRouterRouteAskUserWaitsForReply(t *testing.T) {
	f := newRouteFixture(t, RouterModeRoute)
	f.agent.calls = []scriptedRouterCall{{
		tool: "router_ask_user",
		args: map[string]any{"question": "要改哪个登录入口？", "options": []any{"Web", "App"}},
	}}
	ctx := context.Background()

	_, placeholder, err := f.activity.CreateUserActivity(ctx, "task-1", "改登录", CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	f.waitDecision(t, RouterDecisionApplied)
	act := f.reloadActivity(t, placeholder.ID)
	if act.Status != ActivityStatusWaitingUser || !strings.Contains(act.Body, "要改哪个登录入口") {
		t.Fatalf("ask_user activity = status %q body %q", act.Status, act.Body)
	}
}

func TestRouterRouteSilentHidesPlaceholder(t *testing.T) {
	f := newRouteFixture(t, RouterModeRoute)
	f.agent.calls = []scriptedRouterCall{{tool: "router_silent", args: map[string]any{"reason": "无需回复"}}}
	ctx := context.Background()

	_, placeholder, err := f.activity.CreateUserActivity(ctx, "task-1", "（补充）", CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	f.waitDecision(t, RouterDecisionApplied)
	visible, err := NewActivityStore(f.store.db).List(ctx, "task-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, act := range visible {
		if act.ID == placeholder.ID {
			t.Fatalf("silent placeholder is still visible: %#v", act)
		}
	}
	all, err := NewActivityStore(f.store.db).ListIncludingHidden(ctx, "task-1")
	if err != nil {
		t.Fatalf("ListIncludingHidden: %v", err)
	}
	found := false
	for _, act := range all {
		if act.ID == placeholder.ID && act.Hidden {
			found = true
		}
	}
	if !found {
		t.Fatal("silent placeholder must stay as a hidden audit row")
	}
}

func TestRouterRouteAgentFailureFallsBackToLegacy(t *testing.T) {
	f := newRouteFixture(t, RouterModeRoute)
	f.agent.err = errors.New("router cli crashed")
	ctx := context.Background()

	_, placeholder, err := f.activity.CreateUserActivity(ctx, "task-1", "修复登录", CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	decision := f.waitDecision(t, RouterDecisionFallback)
	if !strings.Contains(decision.Error, "router cli crashed") {
		t.Fatalf("fallback error = %q", decision.Error)
	}
	// 决策状态先落库、回落分发随后执行；等回落真的建出 Run 再断言。
	var runs []*WorkflowRun
	waitForCondition(t, func() bool {
		runs = f.runs(t)
		return len(runs) == 1
	})
	act := f.reloadActivity(t, placeholder.ID)
	if act.RunID != runs[0].ID {
		t.Fatalf("fallback must reuse the placeholder: run id %q want %q", act.RunID, runs[0].ID)
	}
	all, err := NewActivityStore(f.store.db).ListIncludingHidden(ctx, "task-1")
	if err != nil {
		t.Fatalf("ListIncludingHidden: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("activities = %d, want user + one reused placeholder", len(all))
	}
}

// TestRouterRouteEmptyFinalTextFallsBackInsteadOfSilent pins T4 D-2: a model
// turn with no tool call and no visible text is a failed decision, not a
// deliberate silence. Turning it into `silent` hides the user's message behind a
// hidden placeholder, so route mode must fall back to the legacy path instead.
func TestRouterRouteEmptyFinalTextFallsBackInsteadOfSilent(t *testing.T) {
	f := newRouteFixture(t, RouterModeRoute)
	f.agent.final = "   " // 无工具调用、空最终文本
	ctx := context.Background()

	_, placeholder, err := f.activity.CreateUserActivity(ctx, "task-1", "帮我看看这个报错", CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	decision := f.waitDecision(t, RouterDecisionFallback)
	if !strings.Contains(decision.Error, "no decision") {
		t.Fatalf("fallback error = %q, want an explicit empty-decision failure", decision.Error)
	}
	var runs []*WorkflowRun
	waitForCondition(t, func() bool {
		runs = f.runs(t)
		return len(runs) == 1
	})
	act := f.reloadActivity(t, placeholder.ID)
	if act.RunID != runs[0].ID {
		t.Fatalf("fallback must reuse the placeholder: run id %q want %q", act.RunID, runs[0].ID)
	}
	all, err := NewActivityStore(f.store.db).ListIncludingHidden(ctx, "task-1")
	if err != nil {
		t.Fatalf("ListIncludingHidden: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("activities = %d, want user + one reused placeholder", len(all))
	}
}

func TestRouterRouteInvalidActionsRejectAndFallBack(t *testing.T) {
	f := newRouteFixture(t, RouterModeRoute)
	f.agent.calls = []scriptedRouterCall{
		{tool: "router_message_agent", args: map[string]any{"body": "直连"}},
		{tool: "router_run_workflow", args: map[string]any{"workflow_id": "workflow-debug"}},
	}
	ctx := context.Background()

	_, _, err := f.activity.CreateUserActivity(ctx, "task-1", "修复登录", CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	decision := f.waitDecision(t, RouterDecisionRejected)
	if !strings.Contains(decision.Error, "at most one execution") {
		t.Fatalf("rejected error = %q", decision.Error)
	}
	waitForCondition(t, func() bool { return len(f.runs(t)) == 1 })
}

func TestRouterRouteKeepsExplicitWorkflowChoiceOnLegacyPath(t *testing.T) {
	f := newRouteFixture(t, RouterModeRoute)
	f.agent.calls = []scriptedRouterCall{{tool: "router_say", args: map[string]any{"body": "不应该出现"}}}
	ctx := context.Background()

	listed, err := f.workflows.ListVersions(ctx)
	if err != nil || len(listed) == 0 {
		t.Fatalf("ListVersions: %v", err)
	}
	explicit := ""
	for _, v := range listed {
		if strings.Contains(v.Version, "workflow-debug") {
			explicit = v.Version
			break
		}
	}
	if explicit == "" {
		t.Fatal("no published workflow-debug version")
	}
	if _, agentAct, err := f.activity.CreateUserActivity(ctx, "task-1", "跑调试流程", CommentWorkflowChoiceSpecified, &explicit); err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	} else if agentAct == nil || agentAct.RunID == "" {
		t.Fatalf("explicit choice did not take the legacy path: %#v", agentAct)
	}
	time.Sleep(100 * time.Millisecond)
	decisions, err := NewRouterStore(f.store.db).ListDecisions(ctx, "task-1", 10, time.Time{})
	if err != nil || len(decisions) != 0 {
		t.Fatalf("explicit choice reached the router: %d decisions err=%v", len(decisions), err)
	}
}

func TestRouterStartupRecoveryFailsInterruptedApply(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	routerStore := NewRouterStore(store.db)
	decision := &RouterDecision{
		ID: "dec-interrupted", TaskID: "task-1", RootID: root.ID, TurnID: "turn-interrupted",
		Status: RouterDecisionApplying,
	}
	if err := routerStore.InsertDecision(ctx, decision); err != nil {
		t.Fatalf("InsertDecision: %v", err)
	}
	svc.SetRouterConfig(RouterConfig{Mode: RouterModeRoute, IdleClose: time.Hour})
	router := svc.RouterServiceForRoot(root.ID)
	if router == nil {
		t.Fatal("route mode must create a router lane")
	}
	t.Cleanup(router.Close)
	got, err := routerStore.GetDecision(ctx, decision.ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if got.Status != RouterDecisionFailed || !strings.Contains(got.Error, "interrupted") {
		t.Fatalf("interrupted decision = status %q error %q", got.Status, got.Error)
	}
}

func TestRouterClaimAppliedSeqIsCompareAndSwap(t *testing.T) {
	ctx := context.Background()
	svc, root := newTestService(t, &fakeRunner{})
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	routerStore := NewRouterStore(store.db)
	if err := routerStore.InsertDecision(ctx, &RouterDecision{
		ID: "dec-cas", TaskID: "task-1", RootID: root.ID, TurnID: "turn-cas",
		Status: RouterDecisionApplying,
	}); err != nil {
		t.Fatalf("InsertDecision: %v", err)
	}
	ok, err := routerStore.ClaimAppliedSeq(ctx, "dec-cas", 0)
	if err != nil || !ok {
		t.Fatalf("first claim = %v err=%v", ok, err)
	}
	ok, err = routerStore.ClaimAppliedSeq(ctx, "dec-cas", 0)
	if err != nil || ok {
		t.Fatalf("second claim of the same seq = %v err=%v, want false", ok, err)
	}
	ok, err = routerStore.ClaimAppliedSeq(ctx, "dec-cas", 1)
	if err != nil || !ok {
		t.Fatalf("claim next seq = %v err=%v", ok, err)
	}
	got, err := routerStore.GetDecision(ctx, "dec-cas")
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if got.AppliedSeq != 2 {
		t.Fatalf("applied_seq = %d, want 2", got.AppliedSeq)
	}
}
