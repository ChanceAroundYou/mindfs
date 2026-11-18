package kanban

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── R0 三项行为回归（审计 F1/F2/F3）────────────────────────────────────
//
// 这三条用例只钉住「用户可见行为应该是什么」，不复制现状：
//   - 运行中的 Agent 节点收到补充留言时，Router 必须先看到它，再把留言交给
//     唯一的活动 Agent NodeRun，并在 continue_requests 留下可消费的绑定
//     （F1：现状是 Router 之前同步拒绝）；
//   - HumanGate 等待期 Router 选了 deliver 时，动作必须被拒绝/解释：用户能看到
//     无法投递的说明，原留言没有投递目标，gate 保持 waiting_user（F2：现状是
//     决策 failed 而占位永久 pending）；
//   - route 触发携带的排队 brief 在消费时必须反映实时活动执行，同时原留言
//     ID/正文保持不变（F3：现状是入队快照原样进 prompt）。
//
// 仅新增测试：不改产品源码，不把现状写成预期。

// waitRouteDecisionTerminal 有界等待该任务唯一一条 Router 决策走到终态。
// 所有数据库查询共用同一个 deadline context；超时报告最后一轮的错误与条数，
// 避免把「fixture 故障」和「产品回归」都折叠成一句 timeout。
func waitRouteDecisionTerminal(t *testing.T, store *TaskStore, taskID string) *RouterDecision {
	t.Helper()
	const budget = 2 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	routerStore := NewRouterStore(store.db)
	var last *RouterDecision
	var lastErr error
	count := 0
	for {
		decisions, err := routerStore.ListDecisions(ctx, taskID, 10, time.Time{})
		lastErr = err
		count = len(decisions)
		if err == nil && count == 1 {
			last = decisions[0]
			if last.Status != RouterDecisionApplying && last.Status != RouterDecisionShadow {
				return last
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("router produced no terminal decision for task %s within %s (decisions=%d lastErr=%v last=%#v)",
				taskID, budget, count, lastErr, last)
			return nil
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// routerLaneIdle reports whether the task lane has no running or queued turn。
// 决策行落终态不等于副作用结束：校验拒绝之后生产代码还会执行 fallbackRoute。
func routerLaneIdle(router *RouterService, taskID string) bool {
	if router == nil {
		return true
	}
	router.mu.Lock()
	lane := router.lanes[taskID]
	router.mu.Unlock()
	if lane == nil {
		return true
	}
	lane.mu.Lock()
	defer lane.mu.Unlock()
	return !lane.running && len(lane.queue) == 0
}

// waitRouterLaneIdle 有界等待本任务 lane 收尾。fatal=true 用于读取最终状态前的
// 屏障；cleanup 里用 fatal=false，只报告而不阻止 fixture 继续关库。
func waitRouterLaneIdle(t *testing.T, router *RouterService, taskID string, fatal bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if routerLaneIdle(router, taskID) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if fatal {
		t.Fatalf("router lane for task %s is still running after its decision reached a terminal state", taskID)
	}
	t.Errorf("router lane for task %s is still running; closing the fixture store anyway", taskID)
}

func reloadTestActivity(t *testing.T, store *TaskStore, id string) *TaskActivity {
	t.Helper()
	act, err := NewActivityStore(store.db).Get(context.Background(), id)
	if err != nil {
		t.Fatalf("load activity %s: %v", id, err)
	}
	return act
}

// F1：agent 节点在跑时发来的补充留言必须进入 Router，并被投递给唯一活动节点。
func TestRouterRunningAgentMessageReachesRouter(t *testing.T) {
	ctx := context.Background()
	const body = "补充：先把文案改了"
	store, svc, run, root := newWorkflowCommentFixture(t, "task-r0-running", "run-r0-running", workflowCommentTestGraph)
	build := startWorkflowAgentNode(t, store, run.ID, "build", "sess-build")
	if err := store.UpdateTaskStatus(ctx, run.TaskID, StatusRunning, false); err != nil {
		t.Fatalf("UpdateTaskStatus: %v", err)
	}

	agent := &scriptedRouterAgent{calls: []scriptedRouterCall{{
		tool: "router_deliver_to_active_run",
		args: map[string]any{"body": body, "reason": "节点正在跑"},
	}}}
	svc.SetRouterConfig(RouterConfig{Mode: RouterModeRoute, IdleClose: time.Hour})
	svc.SetRouterAgent(agent)
	router := svc.RouterServiceForRoot(root.ID)
	if router == nil {
		t.Fatal("route mode must create a router lane")
	}
	t.Cleanup(router.Close)
	t.Cleanup(func() { waitRouterLaneIdle(t, router, run.TaskID, false) })
	activity, err := svc.ActivityServiceForRoot(root.ID)
	if err != nil {
		t.Fatalf("ActivityServiceForRoot: %v", err)
	}

	userAct, placeholder, err := activity.CreateUserActivity(ctx, run.TaskID, body, CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	if userAct == nil || placeholder == nil {
		t.Fatalf("activities = %#v / %#v, want user + placeholder", userAct, placeholder)
	}
	// 运行中留言不能被 Router 之前的同步守卫拒绝：Router 必须先看到它。
	if placeholder.Status == ActivityStatusFailed {
		t.Fatalf("running-agent message was rejected before the router ran: status=%q summary=%q", placeholder.Status, placeholder.Summary)
	}

	decision := waitRouteDecisionTerminal(t, store, run.TaskID)
	if decision.Status != RouterDecisionApplied {
		t.Fatalf("decision = %q (%s), want applied deliver", decision.Status, decision.Error)
	}
	if len(decision.Actions) != 1 || decision.Actions[0].Kind != RouterActionDeliver {
		t.Fatalf("actions = %#v, want a single deliver_to_active_run", decision.Actions)
	}
	waitRouterLaneIdle(t, router, run.TaskID, true)

	// 活动投影保留：对话流要能看出这条留言属于哪个 Run/节点。
	bound := reloadTestActivity(t, store, userAct.ID)
	if bound.NodeRunID != build.ID {
		t.Fatalf("message bound to %q, want the running agent node %q", bound.NodeRunID, build.ID)
	}
	// 真正的投递依据是 continue_requests：原留言必须恰好一条 queued 请求，
	// 绑定运行中节点的 id 与会话，后续节点回合结束才可消费。
	requests, err := store.ListOpenWorkflowComments(ctx, run.TaskID)
	if err != nil {
		t.Fatalf("ListOpenWorkflowComments: %v", err)
	}
	if len(requests) != 1 {
		t.Fatalf("open workflow comments = %#v, want exactly the delivered user message", requests)
	}
	req := requests[0]
	if req.CommentID != userAct.ID || req.Status != ContinueRequestQueued ||
		req.NodeRunID != build.ID || req.SessionKey != build.SessionKey || req.Body != body {
		t.Fatalf("delivery request = %#v, want queued %q on %s/%s", req, body, build.ID, build.SessionKey)
	}
}

func explainsUndeliverable(text string) bool {
	return strings.Contains(text, "等待你审批") ||
		strings.Contains(text, "等待审批") ||
		strings.Contains(text, "no single active agent node run to deliver to") ||
		strings.Contains(text, "无法转交") ||
		strings.Contains(text, "无法投递")
}

func TestExplainsUndeliverable(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want bool
	}{
		{name: "approval required", text: ErrWorkflowWaitingApproval.Error(), want: true},
		{name: "waiting for your approval", text: "等待你审批，留言不会显示为已转交。", want: true},
		{name: "waiting for your approval with quoted status", text: "工作流正在等待你审批，留言不会显示为“已转交”。", want: true},
		{name: "waiting for approval", text: "工作流正在等待审批。", want: true},
		{name: "no delivery target", text: "no single active agent node run to deliver to", want: true},
		{name: "explicit delivery failure", text: "当前无法投递留言", want: true},
		{name: "quotes success in failure explanation", text: "没有转交：“已转交”是错误提示，当前无法转交", want: true},
		{name: "successful delivery", text: "已转交", want: false},
		{name: "unrelated workflow availability", text: "没有可用的工作流", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := explainsUndeliverable(tc.text); got != tc.want {
				t.Errorf("explainsUndeliverable(%q) = %t, want %t", tc.text, got, tc.want)
			}
		})
	}
}

// F2：HumanGate 等待期 Router 选择 deliver 时必须被拒绝/解释：用户看到无法投递
// 的说明，原留言没有投递目标，gate 不被普通留言批准也不另开一次执行。
func TestRouterHumanGateDeliverTerminatesPlaceholder(t *testing.T) {
	ctx := context.Background()
	const body = "补充一句"
	store, svc, run, root := newWorkflowCommentFixture(t, "task-r0-gate", "run-r0-gate", humanGateGraph())
	if err := svc.AdvanceWorkflowRun(ctx, store, run.TaskID); err != nil {
		t.Fatalf("AdvanceWorkflowRun: %v", err)
	}
	gate, err := store.WorkflowNodeRunByKey(ctx, run.ID, "gate", "", 0, 1)
	if err != nil || gate.Status != NodeRunWaitingUser {
		t.Fatalf("gate = %#v err=%v, want waiting_user", gate, err)
	}

	agent := &scriptedRouterAgent{calls: []scriptedRouterCall{{
		tool: "router_deliver_to_active_run",
		args: map[string]any{"body": body, "reason": "审批等待中"},
	}}}
	svc.SetRouterConfig(RouterConfig{Mode: RouterModeRoute, IdleClose: time.Hour})
	svc.SetRouterAgent(agent)
	router := svc.RouterServiceForRoot(root.ID)
	if router == nil {
		t.Fatal("route mode must create a router lane")
	}
	t.Cleanup(router.Close)
	t.Cleanup(func() { waitRouterLaneIdle(t, router, run.TaskID, false) })
	activity, err := svc.ActivityServiceForRoot(root.ID)
	if err != nil {
		t.Fatalf("ActivityServiceForRoot: %v", err)
	}

	userAct, placeholder, err := activity.CreateUserActivity(ctx, run.TaskID, body, CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	if userAct == nil || placeholder == nil {
		t.Fatalf("activities = %#v / %#v, want user + placeholder", userAct, placeholder)
	}

	// 先等决策终态，再有界等 lane 收尾：校验拒绝之后还有 fallbackRoute 等副作用，
	// 决策行不是占位/副作用的完成屏障。
	waitRouteDecisionTerminal(t, store, run.TaskID)
	waitRouterLaneIdle(t, router, run.TaskID, true)

	act := reloadTestActivity(t, store, placeholder.ID)
	if act.Status == ActivityStatusPending || act.Status == ActivityStatusRunning {
		t.Fatalf("placeholder is still %q after the deliver decision (%#v)", act.Status, act)
	}
	if act.Hidden {
		t.Fatalf("deliver during HumanGate was applied as a hidden hand-off: %#v", act)
	}
	// 校验拒绝和运行期拒绝都需要向用户说明留言无法投递或工作流正在等待审批。
	visible := strings.TrimSpace(strings.Join([]string{act.Summary, act.Body}, "\n"))
	if !explainsUndeliverable(visible) {
		t.Fatalf("placeholder does not explain why delivery was rejected: status=%q summary=%q body=%q", act.Status, act.Summary, act.Body)
	}

	// 原留言不能被转交给任何节点：活动投影与 continue_requests 两条都查。
	user := reloadTestActivity(t, store, userAct.ID)
	if user.NodeRunID != "" || user.SessionKey != "" {
		t.Fatalf("user message was bound to a delivery target: %#v", user)
	}
	requests, err := store.ListOpenWorkflowComments(ctx, run.TaskID)
	if err != nil {
		t.Fatalf("ListOpenWorkflowComments: %v", err)
	}
	for _, req := range requests {
		if req.CommentID == userAct.ID && (req.NodeRunID != "" || req.SessionKey != "") {
			t.Fatalf("message was queued for delivery during HumanGate: %#v", req)
		}
	}

	gate, err = store.WorkflowNodeRunByKey(ctx, run.ID, "gate", "", 0, 1)
	if err != nil || gate.Status != NodeRunWaitingUser {
		t.Fatalf("gate after deliver = %#v err=%v, want untouched waiting_user", gate, err)
	}
	runs, err := store.ListWorkflowRunsForTask(ctx, run.TaskID)
	if err != nil {
		t.Fatalf("ListWorkflowRunsForTask: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want the original run only (no new execution)", len(runs))
	}
	task, err := store.GetTask(ctx, run.TaskID)
	if err != nil || task.Status != StatusWaitingUser {
		t.Fatalf("task = %#v err=%v, want still waiting_user", task, err)
	}
}

// promptRecordingRouterAgent 只记录 Router 回合看到的 Prompt（brief JSON），
// 不做任何动作；用于验证「消费排队 brief 时世界已经刷新」。
type promptRecordingRouterAgent struct {
	mu      sync.Mutex
	prompts []string
}

func (a *promptRecordingRouterAgent) RunRouterTurn(_ context.Context, req RouterAgentRequest) (RouterAgentResult, error) {
	a.mu.Lock()
	a.prompts = append(a.prompts, req.Prompt)
	a.mu.Unlock()
	return RouterAgentResult{SessionKey: "router-r0-audit", Meta: RouterTurnMeta{FinalText: ""}}, nil
}

func (a *promptRecordingRouterAgent) CloseRouterSession(context.Context, string, string) error {
	return nil
}

func (a *promptRecordingRouterAgent) lastPrompt() (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.prompts) == 0 {
		return "", false
	}
	return a.prompts[len(a.prompts)-1], true
}

// F3：route 触发入队时带的 brief 是快照；真正消费时必须反映实时活动执行，
// 同时原始留言的 ID/正文不能丢。
func TestRouterRouteRefreshesQueuedBrief(t *testing.T) {
	ctx := context.Background()
	store, svc, run, root := newWorkflowCommentFixture(t, "task-r0-brief", "run-r0-brief", workflowCommentTestGraph)
	activity, err := svc.ActivityServiceForRoot(root.ID)
	if err != nil {
		t.Fatalf("ActivityServiceForRoot: %v", err)
	}
	const body = "继续实现登录页"
	userAct, err := activity.QueueUserActivity(ctx, run.TaskID, body)
	if err != nil {
		t.Fatalf("QueueUserActivity: %v", err)
	}

	task, err := store.GetTask(ctx, run.TaskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	config := RouterConfig{Mode: RouterModeRoute, IdleClose: time.Hour}.Normalized()
	queuedBrief, err := buildSituationBrief(ctx, store, nil, config, task, RouterTriggerUserMessage, userAct.ID)
	if err != nil {
		t.Fatalf("buildSituationBrief: %v", err)
	}
	if queuedBrief.ActiveExecution != nil {
		t.Fatalf("fixture precondition: queued brief already has an active execution: %#v", queuedBrief.ActiveExecution)
	}

	// 旧 brief 构建后、入队前：一个 agent 节点开始跑，世界已经变化。
	startWorkflowAgentNode(t, store, run.ID, "build", "sess-build")
	if err := store.UpdateTaskStatus(ctx, run.TaskID, StatusRunning, false); err != nil {
		t.Fatalf("UpdateTaskStatus: %v", err)
	}

	agent := &promptRecordingRouterAgent{}
	svc.SetRouterConfig(config)
	svc.SetRouterAgent(agent)
	router := svc.RouterServiceForRoot(root.ID)
	if router == nil {
		t.Fatal("route mode must create a router lane")
	}
	t.Cleanup(router.Close)
	t.Cleanup(func() { waitRouterLaneIdle(t, router, run.TaskID, false) })
	router.Enqueue(RouterTrigger{
		Kind:           RouterTriggerUserMessage,
		TaskID:         run.TaskID,
		RootID:         root.ID,
		UserActivityID: userAct.ID,
		TurnID:         "msg:" + userAct.ID,
		Mode:           RouterModeRoute,
		Brief:          queuedBrief,
	})

	// 先等 lane 收尾再读 prompt：agent 一定在回合结束前被调用，这样测试失败时
	// 也不会留下仍在写库的 Router goroutine。
	waitRouteDecisionTerminal(t, store, run.TaskID)
	waitRouterLaneIdle(t, router, run.TaskID, true)
	waitForCondition(t, func() bool {
		_, ok := agent.lastPrompt()
		return ok
	})
	prompt, _ := agent.lastPrompt()
	var live SituationBrief
	if err := json.Unmarshal([]byte(prompt), &live); err != nil {
		t.Fatalf("decode router prompt as SituationBrief: %v", err)
	}
	if live.ActiveExecution == nil {
		t.Fatalf("router prompt was built from the stale queued brief: active_execution missing\nprompt=%s", prompt)
	}
	if live.ActiveExecution.RunID != run.ID {
		t.Fatalf("active run = %q, want %q", live.ActiveExecution.RunID, run.ID)
	}
	var buildRunning bool
	for _, node := range live.ActiveExecution.CurrentNodes {
		if node.NodeID == "build" && node.Status == NodeRunRunning {
			buildRunning = true
		}
	}
	if !buildRunning {
		t.Fatalf("active_execution.current_nodes = %#v, want the live running agent node build", live.ActiveExecution.CurrentNodes)
	}
	if live.Trigger.UserMessage == nil || live.Trigger.UserMessage.ID != userAct.ID || live.Trigger.UserMessage.Body != body {
		t.Fatalf("trigger user message = %#v, want original %q/%q", live.Trigger.UserMessage, userAct.ID, body)
	}
}
