package kanban

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// 运行期汇报的确定 effect 标识、silent 理由与端到端可追溯（R1 T8）。

// wantRouterEffectID 在测试里独立重算冻结规则，避免「实现即预期」。
func wantRouterEffectID(decisionID string, seq int) string {
	sum := sha256.Sum256([]byte(decisionID + ":" + strconv.Itoa(seq)))
	return "act-rtev-" + hex.EncodeToString(sum[:])[:20]
}

// eventPromptRouterAgent 记录 Router 回合真正看到的 brief，再按脚本动作执行。
type eventPromptRouterAgent struct {
	*scriptedRouterAgent
	mu      sync.Mutex
	prompts []string
}

func newEventPromptRouterAgent(calls ...scriptedRouterCall) *eventPromptRouterAgent {
	return &eventPromptRouterAgent{scriptedRouterAgent: &scriptedRouterAgent{calls: calls}}
}

func (a *eventPromptRouterAgent) RunRouterTurn(ctx context.Context, req RouterAgentRequest) (RouterAgentResult, error) {
	a.mu.Lock()
	a.prompts = append(a.prompts, req.Prompt)
	a.mu.Unlock()
	return a.scriptedRouterAgent.RunRouterTurn(ctx, req)
}

func (a *eventPromptRouterAgent) lastPrompt(t *testing.T) string {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.prompts) == 0 {
		t.Fatal("router turn saw no prompt")
	}
	return a.prompts[len(a.prompts)-1]
}

// 行为 6：重复应用同一决策/动作只留一条可见汇报，ID 是确定的，且旧 Run 的结果
// 活动一个字都不能改。
func TestRuntimeReportDeterministicEffect(t *testing.T) {
	ctx := context.Background()
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.stopLoop(t)
	user := f.addUserActivity(t, "uact-effect")
	task, err := f.store.GetTask(ctx, "task-1")
	must(t, err, "GetTask")
	activities := NewActivityStore(f.store.db)
	must(t, activities.Insert(ctx, &TaskActivity{
		ID: "old-run-result", TaskID: "task-1", RootID: f.root.ID,
		Type: ActivityTypeAgentReply, Role: RoleAgent, Body: "旧 Run 的结果正文",
		RunID: "run-old", Status: ActivityStatusCompleted, Purpose: "result", ReplyToID: user,
	}), "insert old result activity")
	userAct, err := activities.Get(ctx, user)
	must(t, err, "load user activity")
	decision := &RouterDecision{
		ID: "rdec-effect", TaskID: "task-1", RootID: f.root.ID, UserActivityID: user,
		TriggerKind: RouterTriggerRunSettled, TriggerRef: "run-old", TurnID: "revt-turn:effect",
		Status: RouterDecisionApplying,
	}
	action := RouterAction{Seq: 1, Kind: RouterActionSay, Body: "两个节点都成功了"}
	decision.Actions = []RouterAction{action}

	if got := routerEventEffectID("", 1); got != "" {
		t.Fatalf("routerEventEffectID(empty) = %q, want empty", got)
	}
	if got := routerEventEffectID("rdec-effect", -1); got != "" {
		t.Fatalf("routerEventEffectID(negative seq) = %q, want empty", got)
	}
	for i := 0; i < 2; i++ {
		if err := f.activity.applyRouteSay(ctx, decision, task, userAct, &routeApplyState{}, action); err != nil {
			t.Fatalf("applyRouteSay run %d: %v", i+1, err)
		}
	}
	var reports []*TaskActivity
	visible, err := activities.List(ctx, "task-1")
	must(t, err, "List activities")
	for _, act := range visible {
		if act.RouterDecisionID == decision.ID {
			reports = append(reports, act)
		}
	}
	if len(reports) != 1 {
		t.Fatalf("visible reports for one decision = %d, want exactly 1: %#v", len(reports), reports)
	}
	report := reports[0]
	if want := wantRouterEffectID(decision.ID, action.Seq); report.ID != want {
		t.Fatalf("report id = %q, want deterministic %q", report.ID, want)
	}
	if report.RunID != "" {
		t.Fatalf("report run_id = %q, want empty (must not bind to the old run)", report.RunID)
	}
	if report.Purpose != "progress" {
		t.Fatalf("report purpose = %q, want progress for runtime events", report.Purpose)
	}
	if report.Body != action.Body || report.Status != ActivityStatusCompleted || report.ReplyToID != user {
		t.Fatalf("report = %#v, want completed body reply to the original message", report)
	}
	old, err := activities.Get(ctx, "old-run-result")
	must(t, err, "reload old result")
	if old.Body != "旧 Run 的结果正文" || old.Status != ActivityStatusCompleted ||
		old.RunID != "run-old" || old.RouterDecisionID != "" {
		t.Fatalf("old run result activity was rewritten: %#v", old)
	}
}

// 行为 7：运行期 silent 必须把理由落库可读，事件 done，且不新增可见活动；它的
// 判断依据是事件 brief 里的证据，不是另一份世界摘要。
func TestRuntimeSilentReason(t *testing.T) {
	ctx := context.Background()
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.stopLoop(t)
	const reason = "启动对账没有变化，无需打扰用户"
	agent := newEventPromptRouterAgent(scriptedRouterCall{tool: "router_silent", args: map[string]any{"reason": reason}})
	f.svc.SetRouterAgent(agent)
	f.addRun(t, "task-1", "run-silent", WorkflowTriggeredByUser, map[string]string{"agent-1": NodeRunSuccess})
	f.addNodeRun(t, NodeRun{ID: "nr-silent", RunID: "run-silent", NodeID: "agent-1", Attempt: 1,
		Status: NodeRunSuccess, Output: map[string]any{"summary": "启动对账完成"}})
	f.setTaskStatus(t, "task-1", StatusSuccess)
	user := f.addUserActivity(t, "uact-silent")
	ev := f.addEvent(t, RouterEvent{
		Kind: RouterEventRunSettled, TurnID: "silent-turn", UserActivityID: user, RunID: "run-silent",
		PayloadJSON: `{"run_id":"run-silent","status":"success"}`,
	})
	if _, err := f.router.ConsumeRouterEvents(ctx, time.Now().UTC()); err != nil {
		t.Fatalf("ConsumeRouterEvents: %v", err)
	}
	f.waitEvent(t, ev.ID, RouterEventDone)

	prompt := agent.lastPrompt(t)
	if !strings.Contains(prompt, ev.ID) {
		t.Fatalf("router prompt does not carry the event id %q:\n%s", ev.ID, prompt)
	}
	if !strings.Contains(prompt, "启动对账完成") {
		t.Fatalf("router prompt does not carry the node output summary:\n%s", prompt)
	}
	decision, err := NewRouterStore(f.store.db).DecisionByTurn(ctx, "task-1", ev.TurnID)
	must(t, err, "DecisionByTurn")
	if len(decision.Actions) != 1 || decision.Actions[0].Kind != RouterActionSilent {
		t.Fatalf("decision actions = %#v, want a single silent", decision.Actions)
	}
	if decision.Actions[0].Reason != reason {
		t.Fatalf("silent reason = %q, want %q", decision.Actions[0].Reason, reason)
	}
	visible, err := NewActivityStore(f.store.db).List(ctx, "task-1")
	must(t, err, "List activities")
	if len(visible) != 1 || visible[0].ID != user {
		t.Fatalf("silent added visible activities: %#v", visible)
	}
}

// 行为 8b：模型只说话、没调写入工具时，合成出来的 say 在事件回合里也必须是
// progress。`synthesizeActions` 是用户对话的默认 chat，事件回合要改写成汇报。
func TestRuntimeEventSynthesizedSayIsProgress(t *testing.T) {
	ctx := context.Background()
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.stopLoop(t)
	const body = "两个节点都成功了，没有别的要说的"
	agent := &scriptedRouterAgent{final: body}
	f.svc.SetRouterAgent(agent)
	f.addRun(t, "task-1", "run-synth", WorkflowTriggeredByUser, map[string]string{"agent-1": NodeRunSuccess})
	f.addNodeRun(t, NodeRun{ID: "nr-synth", RunID: "run-synth", NodeID: "agent-1", Attempt: 1,
		Status: NodeRunSuccess, Output: map[string]any{"summary": "完成"}})
	f.setTaskStatus(t, "task-1", StatusSuccess)
	user := f.addUserActivity(t, "uact-synth")
	ev := f.addEvent(t, RouterEvent{
		Kind: RouterEventRunSettled, TurnID: "synth-turn", UserActivityID: user, RunID: "run-synth",
		PayloadJSON: `{"run_id":"run-synth","status":"success"}`,
	})
	if _, err := f.router.ConsumeRouterEvents(ctx, time.Now().UTC()); err != nil {
		t.Fatalf("ConsumeRouterEvents: %v", err)
	}
	f.waitEvent(t, ev.ID, RouterEventDone)

	decision, err := NewRouterStore(f.store.db).DecisionByTurn(ctx, "task-1", ev.TurnID)
	must(t, err, "DecisionByTurn")
	if len(decision.Actions) != 1 || decision.Actions[0].Kind != RouterActionSay || decision.Actions[0].Body != body {
		t.Fatalf("decision actions = %#v, want one synthesized say", decision.Actions)
	}
	if decision.Actions[0].Purpose != "progress" {
		t.Fatalf("synthesized say purpose = %q, want progress for runtime events", decision.Actions[0].Purpose)
	}
}

// 出现在原始留言线程里并指向原始留言。
func TestRuntimeEventPromptTraceability(t *testing.T) {
	ctx := context.Background()
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.stopLoop(t)
	const body = "两个节点都完成了：登录页已改好"
	agent := newEventPromptRouterAgent(scriptedRouterCall{tool: "router_say", args: map[string]any{"body": body}})
	f.svc.SetRouterAgent(agent)
	f.addRun(t, "task-1", "run-trace", WorkflowTriggeredByUser, map[string]string{
		"agent-1": NodeRunSuccess, "agent-2": NodeRunSuccess,
	})
	f.addNodeRun(t, NodeRun{ID: "nr-trace-1", RunID: "run-trace", NodeID: "agent-1", Attempt: 1,
		Status: NodeRunSuccess, Output: map[string]any{"summary": "登录页已改好"}})
	f.addNodeRun(t, NodeRun{ID: "nr-trace-2", RunID: "run-trace", NodeID: "agent-2", Attempt: 1,
		Status: NodeRunSuccess, Output: map[string]any{"summary": "补了三条测试"}})
	f.setTaskStatus(t, "task-1", StatusSuccess)
	user := f.addUserActivity(t, "uact-trace")
	ev := f.addEvent(t, RouterEvent{
		Kind: RouterEventRunSettled, TurnID: "trace-turn", UserActivityID: user, RunID: "run-trace",
		PayloadJSON: `{"run_id":"run-trace","status":"success"}`,
	})
	if _, err := f.router.ConsumeRouterEvents(ctx, time.Now().UTC()); err != nil {
		t.Fatalf("ConsumeRouterEvents: %v", err)
	}
	f.waitEvent(t, ev.ID, RouterEventDone)

	prompt := agent.lastPrompt(t)
	for _, want := range []string{ev.ID, "登录页已改好", "补了三条测试"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("router prompt is missing %q:\n%s", want, prompt)
		}
	}
	decision, err := NewRouterStore(f.store.db).DecisionByTurn(ctx, "task-1", ev.TurnID)
	must(t, err, "DecisionByTurn")
	if decision.Status != RouterDecisionApplied {
		t.Fatalf("decision status = %q (%s), want applied", decision.Status, decision.Error)
	}
	visible, err := NewActivityStore(f.store.db).List(ctx, "task-1")
	must(t, err, "List activities")
	var reply *TaskActivity
	for _, act := range visible {
		if act.RouterDecisionID == decision.ID {
			reply = act
		}
	}
	if reply == nil {
		t.Fatalf("final say is not visible in the task thread: %#v", visible)
	}
	if reply.Body != body || reply.ReplyToID != user || reply.RunID != "" {
		t.Fatalf("final say = %#v, want body %q replying to %q without a run binding", reply, body, user)
	}
	// 模型没写 purpose 时，运行期 say 的默认必须是 progress（用户留言仍是 chat）。
	// 只能在工具默认值层做：apply 层看到的已经是工具填好的值。
	if reply.Purpose != "progress" {
		t.Fatalf("final say purpose = %q, want progress for runtime events", reply.Purpose)
	}
}
