package kanban

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// coordinate 模式的消费侧（R1 T7）：模式矩阵、用户留言、串行/并行、预算、
// 链深度、stall 重查与有界扫描。模型处只用假 agent，其余走生产链路。

type coordinateFixture struct {
	*routerEventFixture
	agent    *scriptedRouterAgent
	direct   *fakeDirectExecutor
	router   *RouterService
	activity *ActivityService
}

func newCoordinateFixture(t *testing.T, mode RouterMode) *coordinateFixture {
	t.Helper()
	f := newRouterEventFixture(t)
	f.svc.SetRouterConfig(RouterConfig{Mode: mode, IdleClose: time.Hour})
	agent := &scriptedRouterAgent{}
	direct := &fakeDirectExecutor{reply: "直连回复", done: make(chan struct{})}
	f.svc.SetRouterAgent(agent)
	f.svc.SetDirectExecutor(direct)
	router := f.svc.RouterServiceForRoot(f.root.ID)
	if router != nil {
		t.Cleanup(router.Close)
	}
	activity, err := f.svc.ActivityServiceForRoot(f.root.ID)
	must(t, err, "ActivityServiceForRoot")
	return &coordinateFixture{routerEventFixture: f, agent: agent, direct: direct, router: router, activity: activity}
}

// stopLoop 停掉 30s 事件循环：需要手动驱动 ConsumeRouterEvents 的用例必须
// 独占消费权，否则后台第一拍会与断言抢同一批事件（假时钟与真实时钟不同步
// 时尤其明显）。
func (f *coordinateFixture) stopLoop(t *testing.T) {
	t.Helper()
	if f.router == nil {
		return
	}
	f.router.Close()
	waitForCondition(t, func() bool { return !f.router.coordinateLoopActive() })
}

func (f *coordinateFixture) addUserActivity(t *testing.T, id string) string {
	t.Helper()
	must(t, NewActivityStore(f.store.db).Insert(context.Background(), &TaskActivity{
		ID: id, TaskID: "task-1", RootID: f.root.ID,
		Type: ActivityTypeUserMessage, Author: "user", Role: "user", Body: "原始需求",
	}), "insert user activity %s", id)
	return id
}

func (f *coordinateFixture) addEvent(t *testing.T, ev RouterEvent) RouterEvent {
	t.Helper()
	if ev.RootID == "" {
		ev.RootID = f.root.ID
	}
	if ev.TaskID == "" {
		ev.TaskID = "task-1"
	}
	if ev.RunID == "" {
		ev.RunID = "run-" + ev.TurnID
	}
	if ev.InstanceKey == "" {
		ev.InstanceKey = ev.TurnID
	}
	inserted, _, err := f.events.InsertEvent(context.Background(), ev)
	must(t, err, "InsertEvent(%s)", ev.TurnID)
	return inserted
}

func (f *coordinateFixture) eventByID(t *testing.T, taskID, eventID string) RouterEvent {
	t.Helper()
	events, err := f.events.ListEventsByTask(context.Background(), taskID)
	must(t, err, "ListEventsByTask")
	for _, ev := range events {
		if ev.ID == eventID {
			return ev
		}
	}
	t.Fatalf("event %s not found on task %s", eventID, taskID)
	return RouterEvent{}
}

func (f *coordinateFixture) decisions(t *testing.T, taskID string) []*RouterDecision {
	t.Helper()
	decisions, err := NewRouterStore(f.store.db).ListDecisions(context.Background(), taskID, 20, time.Time{})
	must(t, err, "ListDecisions")
	return decisions
}

func (f *coordinateFixture) waitDecision(t *testing.T, taskID, turnID string, status RouterDecisionStatus) *RouterDecision {
	t.Helper()
	var got *RouterDecision
	waitForCondition(t, func() bool {
		decision, err := NewRouterStore(f.store.db).DecisionByTurn(context.Background(), taskID, turnID)
		if err != nil {
			return false
		}
		got = decision
		return decision.Status == status
	})
	if got == nil {
		t.Fatalf("decision for turn %s not recorded", turnID)
	}
	return got
}

func (f *coordinateFixture) waitEvent(t *testing.T, eventID, status string) RouterEvent {
	t.Helper()
	var got RouterEvent
	waitForCondition(t, func() bool {
		ev := f.eventByID(t, "task-1", eventID)
		got = ev
		return ev.Status == status
	})
	return got
}

func (f *coordinateFixture) publishWorkflow(t *testing.T, workflowID string) {
	t.Helper()
	workflows := newWorkflowStore(t.TempDir())
	node := wfNode("agent-1", NodeKindAgent)
	node.Params["prompt"] = "执行任务"
	if _, err := workflows.CreateDraft(context.Background(), workflowID, "", "tester", wfDef(node)); err != nil {
		t.Fatalf("CreateDraft(%s): %v", workflowID, err)
	}
	if _, err := workflows.PublishDraft(context.Background(), workflowID, "r1", "tester"); err != nil {
		t.Fatalf("PublishDraft(%s): %v", workflowID, err)
	}
	f.svc.Workflows = workflows
}

func (f *coordinateFixture) runs(t *testing.T) []*WorkflowRun {
	t.Helper()
	runs, err := f.store.ListWorkflowRunsForTask(context.Background(), "task-1")
	must(t, err, "ListWorkflowRunsForTask")
	return runs
}

// 行为 1：模式矩阵。off 无 runtime；shadow/route 消费零读写、事件仍 pending、
// 带 Event 的触发被忽略；coordinate 起循环，Close 后退出。
func TestCoordinatorModeMatrix(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

	t.Run("off", func(t *testing.T) {
		f := newCoordinateFixture(t, RouterModeOff)
		if f.router != nil {
			t.Fatal("off mode must not build a Router runtime")
		}
		var nilRouter *RouterService
		res, err := nilRouter.ConsumeRouterEvents(ctx, now)
		if err != nil || res != (RouterCoordinateConsumeResult{}) {
			t.Fatalf("nil consume = %+v err=%v", res, err)
		}
	})

	for _, mode := range []RouterMode{RouterModeShadow, RouterModeRoute} {
		t.Run(string(mode), func(t *testing.T) {
			f := newCoordinateFixture(t, mode)
			f.addUserActivity(t, "uact-matrix")
			ev := f.addEvent(t, RouterEvent{Kind: RouterEventRunSettled, TurnID: "matrix-" + string(mode), UserActivityID: "uact-matrix"})
			res, err := f.router.ConsumeRouterEvents(ctx, now)
			if err != nil || res.Enqueued != 0 || res.Skipped != 0 {
				t.Fatalf("consume = %+v err=%v", res, err)
			}
			if got := f.eventByID(t, "task-1", ev.ID).Status; got != RouterEventPending {
				t.Fatalf("event status = %q, want pending", got)
			}
			if f.router.coordinateLoopActive() {
				t.Fatal("non-coordinate mode must not run the event loop")
			}
			f.router.Enqueue(RouterTrigger{Kind: RouterTriggerRunSettled, TaskID: "task-1", TurnID: "manual-" + string(mode), Mode: mode, Event: &ev})
			time.Sleep(50 * time.Millisecond)
			if got := len(f.decisions(t, "task-1")); got != 0 {
				t.Fatalf("event trigger in %s mode produced %d decisions", mode, got)
			}
		})
	}

	t.Run("coordinate", func(t *testing.T) {
		f := newCoordinateFixture(t, RouterModeCoordinate)
		if !f.router.coordinateLoopActive() {
			t.Fatal("coordinate mode must start the event loop")
		}
		f.router.Close()
		waitForCondition(t, func() bool { return !f.router.coordinateLoopActive() })
	})
}

// 行为 2：coordinate 用户留言沿用 route 的同步占位 + 动作应用；specified /
// disabled 是用户明确指令，严格绕过 Router（零决策）。
func TestCoordinatorUserMessageUsesRoutePath(t *testing.T) {
	ctx := context.Background()
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.stopLoop(t)
	f.agent.calls = []scriptedRouterCall{{tool: "router_say", args: map[string]any{"body": "收到，我先梳理一下"}}}

	userAct, placeholder, err := f.activity.CreateUserActivity(ctx, "task-1", "帮我改登录", CommentWorkflowChoiceEmpty, nil)
	must(t, err, "CreateUserActivity")
	if placeholder == nil || placeholder.Status != ActivityStatusPending {
		t.Fatalf("placeholder = %#v, want pending", placeholder)
	}
	decision := f.waitDecision(t, "task-1", "msg:"+userAct.ID, RouterDecisionApplied)
	if decision.TriggerKind != RouterTriggerUserMessage || decision.PlaceholderActivityID != placeholder.ID {
		t.Fatalf("decision = kind %q placeholder %q", decision.TriggerKind, decision.PlaceholderActivityID)
	}
	act, err := NewActivityStore(f.store.db).Get(ctx, placeholder.ID)
	must(t, err, "load placeholder")
	if act.Status != ActivityStatusCompleted || act.Body != "收到，我先梳理一下" || act.ReplyToID != userAct.ID {
		t.Fatalf("applied say = status %q body %q reply_to %q", act.Status, act.Body, act.ReplyToID)
	}
	if runs := f.runs(t); len(runs) != 0 {
		t.Fatalf("coordinate say created legacy runs: %d", len(runs))
	}
	if task := f.task(t, "task-1"); task.ResultSummary != "收到，我先梳理一下" {
		t.Fatalf("result summary = %q, want the user-turn say", task.ResultSummary)
	}

	f.addTask(t, "task-2")
	f.publishWorkflow(t, "workflow-debug")
	// ActivityService 在装配期捕获 workflow 目录；换了目录必须重取。
	f.activity, err = f.svc.ActivityServiceForRoot(f.root.ID)
	must(t, err, "ActivityServiceForRoot after workflows")
	listed, err := f.svc.Workflows.ListVersions(ctx)
	must(t, err, "ListVersions")
	explicit := ""
	for _, version := range listed {
		if strings.Contains(version.Version, "workflow-debug") {
			explicit = version.Version
		}
	}
	if explicit == "" {
		t.Fatal("no published workflow-debug version")
	}
	_, agentAct, err := f.activity.CreateUserActivity(ctx, "task-2", "跑调试流程", CommentWorkflowChoiceSpecified, &explicit)
	must(t, err, "specified CreateUserActivity")
	if agentAct == nil || agentAct.RunID == "" {
		t.Fatalf("specified choice did not take the legacy path: %#v", agentAct)
	}
	time.Sleep(100 * time.Millisecond)
	if got := len(f.decisions(t, "task-2")); got != 0 {
		t.Fatalf("specified choice reached the router: %d decisions", got)
	}

	f.addTask(t, "task-3")
	if _, _, err := f.activity.CreateUserActivity(ctx, "task-3", "纯对话", CommentWorkflowChoiceDisabled, nil); err != nil {
		t.Fatalf("disabled CreateUserActivity: %v", err)
	}
	select {
	case <-f.direct.done:
	case <-time.After(2 * time.Second):
		t.Fatal("disabled choice did not start the direct turn")
	}
	if got := len(f.decisions(t, "task-3")); got != 0 {
		t.Fatalf("disabled choice reached the router: %d decisions", got)
	}
}

// concurrencyRouterAgent 只用于钉住「同任务串行、跨任务并行」：它记录同时在
// 跑的回合数，并为每个任务单独记峰值。
type concurrencyRouterAgent struct {
	mu          sync.Mutex
	calls       int
	inFlight    int
	maxInFlight int
	inTask      map[string]int
	maxInTask   map[string]int
	delay       time.Duration
}

func (a *concurrencyRouterAgent) RunRouterTurn(ctx context.Context, req RouterAgentRequest) (RouterAgentResult, error) {
	a.mu.Lock()
	a.calls++
	a.inFlight++
	if a.inFlight > a.maxInFlight {
		a.maxInFlight = a.inFlight
	}
	if a.inTask == nil {
		a.inTask = map[string]int{}
		a.maxInTask = map[string]int{}
	}
	a.inTask[req.TaskID]++
	if a.inTask[req.TaskID] > a.maxInTask[req.TaskID] {
		a.maxInTask[req.TaskID] = a.inTask[req.TaskID]
	}
	a.mu.Unlock()
	if a.delay > 0 {
		time.Sleep(a.delay)
	}
	a.mu.Lock()
	a.inFlight--
	a.inTask[req.TaskID]--
	a.mu.Unlock()
	return RouterAgentResult{SessionKey: "concurrency", FinalText: "事件汇报", Meta: RouterTurnMeta{FinalText: "事件汇报"}}, nil
}

func (a *concurrencyRouterAgent) CloseRouterSession(context.Context, string, string) error {
	return nil
}

func (a *concurrencyRouterAgent) snapshot() (calls, maxInFlight int, maxInTask map[string]int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make(map[string]int, len(a.maxInTask))
	for taskID, n := range a.maxInTask {
		out[taskID] = n
	}
	return a.calls, a.maxInFlight, out
}

// 行为 4：同一任务的事件串行（lane），不同任务的事件可重叠。
func TestCoordinatorSameTaskSerialAcrossTaskParallel(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

	t.Run("same task serial", func(t *testing.T) {
		f := newCoordinateFixture(t, RouterModeCoordinate)
		f.stopLoop(t)
		agent := &concurrencyRouterAgent{delay: 120 * time.Millisecond}
		f.svc.SetRouterAgent(agent)
		f.addUserActivity(t, "uact-serial-1")
		f.addUserActivity(t, "uact-serial-2")
		f.addEvent(t, RouterEvent{Kind: RouterEventRunSettled, TurnID: "serial-1", UserActivityID: "uact-serial-1"})
		f.addEvent(t, RouterEvent{Kind: RouterEventRunSettled, TurnID: "serial-2", UserActivityID: "uact-serial-2"})
		if _, err := f.router.ConsumeRouterEvents(ctx, now); err != nil {
			t.Fatalf("ConsumeRouterEvents: %v", err)
		}
		waitForCondition(t, func() bool { calls, _, _ := agent.snapshot(); return calls == 2 })
		calls, _, maxInTask := agent.snapshot()
		if calls != 2 || maxInTask["task-1"] != 1 {
			t.Fatalf("calls=%d maxPerTask=%v, want 2 sequential turns", calls, maxInTask)
		}
	})

	t.Run("cross task parallel", func(t *testing.T) {
		f := newCoordinateFixture(t, RouterModeCoordinate)
		f.stopLoop(t)
		agent := &concurrencyRouterAgent{delay: 150 * time.Millisecond}
		f.svc.SetRouterAgent(agent)
		f.addTask(t, "task-2")
		f.addUserActivity(t, "uact-par-1")
		must(t, NewActivityStore(f.store.db).Insert(ctx, &TaskActivity{
			ID: "uact-par-2", TaskID: "task-2", RootID: f.root.ID,
			Type: ActivityTypeUserMessage, Author: "user", Role: "user", Body: "另一个任务",
		}), "insert task-2 user activity")
		f.addEvent(t, RouterEvent{Kind: RouterEventRunSettled, TurnID: "par-1", UserActivityID: "uact-par-1"})
		f.addEvent(t, RouterEvent{Kind: RouterEventRunSettled, TaskID: "task-2", TurnID: "par-2", UserActivityID: "uact-par-2"})
		if _, err := f.router.ConsumeRouterEvents(ctx, now); err != nil {
			t.Fatalf("ConsumeRouterEvents: %v", err)
		}
		waitForCondition(t, func() bool { calls, _, _ := agent.snapshot(); return calls == 2 })
		_, maxInFlight, _ := agent.snapshot()
		if maxInFlight < 2 {
			t.Fatalf("max in flight = %d, want overlapping turns across tasks", maxInFlight)
		}
	})
}

// 行为 5：同一条原始留言已经有 3 条决策 → 第 4 个事件 skipped，零模型调用。
func TestCoordinatorTurnBudgetExhausted(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.stopLoop(t)
	f.addUserActivity(t, "uact-budget")
	routerStore := NewRouterStore(f.store.db)
	for _, turnID := range []string{"budget-t0", "budget-t1", "budget-t2"} {
		must(t, routerStore.InsertDecision(ctx, &RouterDecision{
			ID: "dec-" + turnID, TaskID: "task-1", RootID: f.root.ID, UserActivityID: "uact-budget",
			TriggerKind: RouterTriggerUserMessage, TurnID: turnID, Status: RouterDecisionApplied,
		}), "InsertDecision(%s)", turnID)
	}
	ev := f.addEvent(t, RouterEvent{Kind: RouterEventRunSettled, TurnID: "budget-event", UserActivityID: "uact-budget"})
	if _, err := f.router.ConsumeRouterEvents(ctx, now); err != nil {
		t.Fatalf("ConsumeRouterEvents: %v", err)
	}
	got := f.waitEvent(t, ev.ID, RouterEventSkipped)
	if !strings.Contains(got.LastError, "turn budget exhausted") {
		t.Fatalf("skipped reason = %q", got.LastError)
	}
	if calls := len(f.agent.sessions); calls != 0 {
		t.Fatalf("model calls = %d, want 0", calls)
	}
}

// 行为 6：链深度 3 正常处理、4 直接 skipped、缺少原始留言 skipped；后两者零模型调用。
func TestCoordinatorChainDepthAndMissingOrigin(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

	t.Run("depth 3 processed", func(t *testing.T) {
		f := newCoordinateFixture(t, RouterModeCoordinate)
		f.stopLoop(t)
		f.agent.calls = []scriptedRouterCall{{tool: "router_say", args: map[string]any{"body": "第三层汇报"}}}
		f.addUserActivity(t, "uact-depth")
		ev := f.addEvent(t, RouterEvent{Kind: RouterEventRunSettled, TurnID: "depth-3", UserActivityID: "uact-depth", ChainDepth: 3})
		if _, err := f.router.ConsumeRouterEvents(ctx, now); err != nil {
			t.Fatalf("ConsumeRouterEvents: %v", err)
		}
		f.waitEvent(t, ev.ID, RouterEventDone)
		if calls := len(f.agent.sessions); calls != 1 {
			t.Fatalf("model calls = %d, want 1", calls)
		}
	})

	t.Run("depth 4 skipped", func(t *testing.T) {
		f := newCoordinateFixture(t, RouterModeCoordinate)
		f.stopLoop(t)
		f.addUserActivity(t, "uact-depth")
		ev := f.addEvent(t, RouterEvent{Kind: RouterEventRunSettled, TurnID: "depth-4", UserActivityID: "uact-depth", ChainDepth: 4})
		if _, err := f.router.ConsumeRouterEvents(ctx, now); err != nil {
			t.Fatalf("ConsumeRouterEvents: %v", err)
		}
		got := f.waitEvent(t, ev.ID, RouterEventSkipped)
		if !strings.Contains(got.LastError, "chain depth 4") {
			t.Fatalf("skipped reason = %q", got.LastError)
		}
		if calls := len(f.agent.sessions); calls != 0 {
			t.Fatalf("model calls = %d, want 0", calls)
		}
	})

	t.Run("missing origin skipped", func(t *testing.T) {
		f := newCoordinateFixture(t, RouterModeCoordinate)
		f.stopLoop(t)
		ev := f.addEvent(t, RouterEvent{Kind: RouterEventRunSettled, TurnID: "no-origin"})
		if _, err := f.router.ConsumeRouterEvents(ctx, now); err != nil {
			t.Fatalf("ConsumeRouterEvents: %v", err)
		}
		got := f.waitEvent(t, ev.ID, RouterEventSkipped)
		if !strings.Contains(got.LastError, "missing originating user activity") {
			t.Fatalf("skipped reason = %q", got.LastError)
		}
		if calls := len(f.agent.sessions); calls != 0 {
			t.Fatalf("model calls = %d, want 0", calls)
		}
	})
}
