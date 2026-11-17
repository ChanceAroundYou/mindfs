package kanban

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// ── R0 三项行为回归（审计 F1/F2/F3）────────────────────────────────────
//
// 这三条用例只钉住「用户可见行为应该是什么」，不复制现状：
//   - 运行中的 Agent 节点收到补充留言时，Router 必须先看到它，再把留言交给
//     唯一的活动 Agent NodeRun（F1：现状是 Router 之前同步拒绝）；
//   - HumanGate 等待期 Router 选了 deliver 时，动作必须被拒绝/解释，用户占位
//     回复必须离开 pending/running，gate 保持 waiting_user（F2：现状是决策
//     failed 而占位永久 pending）；
//   - route 触发携带的排队 brief 在消费时必须反映实时活动执行，同时原留言
//     ID/正文保持不变（F3：现状是入队快照原样进 prompt）。
//
// 仅新增测试：不改产品源码，不把现状写成预期。

// waitRouteDecisionTerminal 有界等待该任务唯一一条 Router 决策走到终态。
// 超时是明确的失败点（没有决策 / 永远 applying），不是静默通过。
func waitRouteDecisionTerminal(t *testing.T, store *TaskStore, taskID string) *RouterDecision {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last *RouterDecision
	for time.Now().Before(deadline) {
		decisions, err := NewRouterStore(store.db).ListDecisions(context.Background(), taskID, 10, time.Time{})
		if err == nil && len(decisions) == 1 {
			last = decisions[0]
			if last.Status != RouterDecisionApplying && last.Status != RouterDecisionShadow {
				return last
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("router produced no terminal decision for task %s (last=%#v)", taskID, last)
	return nil
}

func reloadTestActivity(t *testing.T, store *TaskStore, id string) *TaskActivity {
	t.Helper()
	act, err := NewActivityStore(store.db).Get(context.Background(), id)
	if err != nil {
		t.Fatalf("load activity %s: %v", id, err)
	}
	return act
}

// F1：agent 节点在跑时发来的补充留言必须进入 Router 并被投递给唯一活动节点。
func TestRouterRunningAgentMessageReachesRouter(t *testing.T) {
	ctx := context.Background()
	store, svc, run, root := newWorkflowCommentFixture(t, "task-r0-running", "run-r0-running", workflowCommentTestGraph)
	build := startWorkflowAgentNode(t, store, run.ID, "build", "sess-build")
	if err := store.UpdateTaskStatus(ctx, run.TaskID, StatusRunning, false); err != nil {
		t.Fatalf("UpdateTaskStatus: %v", err)
	}

	agent := &scriptedRouterAgent{calls: []scriptedRouterCall{{
		tool: "router_deliver_to_active_run",
		args: map[string]any{"body": "补充：先把文案改了", "reason": "节点正在跑"},
	}}}
	svc.SetRouterConfig(RouterConfig{Mode: RouterModeRoute, IdleClose: time.Hour})
	svc.SetRouterAgent(agent)
	router := svc.RouterServiceForRoot(root.ID)
	if router == nil {
		t.Fatal("route mode must create a router lane")
	}
	t.Cleanup(router.Close)
	activity, err := svc.ActivityServiceForRoot(root.ID)
	if err != nil {
		t.Fatalf("ActivityServiceForRoot: %v", err)
	}

	userAct, placeholder, err := activity.CreateUserActivity(ctx, run.TaskID, "补充：先把文案改了", CommentWorkflowChoiceEmpty, nil)
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
	waitForCondition(t, func() bool {
		act, err := NewActivityStore(store.db).Get(ctx, userAct.ID)
		return err == nil && act.NodeRunID == build.ID
	})
	bound := reloadTestActivity(t, store, userAct.ID)
	if bound.NodeRunID != build.ID {
		t.Fatalf("message bound to %q, want the running agent node %q", bound.NodeRunID, build.ID)
	}
}

// F2：HumanGate 等待期 Router 选择 deliver 时必须被拒绝/解释，用户占位回复不能
// 永远停在 pending，gate 也不能被普通留言批准或另开一次执行。
func TestRouterHumanGateDeliverTerminatesPlaceholder(t *testing.T) {
	ctx := context.Background()
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
		args: map[string]any{"body": "补充一句", "reason": "审批等待中"},
	}}}
	svc.SetRouterConfig(RouterConfig{Mode: RouterModeRoute, IdleClose: time.Hour})
	svc.SetRouterAgent(agent)
	router := svc.RouterServiceForRoot(root.ID)
	if router == nil {
		t.Fatal("route mode must create a router lane")
	}
	t.Cleanup(router.Close)
	activity, err := svc.ActivityServiceForRoot(root.ID)
	if err != nil {
		t.Fatalf("ActivityServiceForRoot: %v", err)
	}

	_, placeholder, err := activity.CreateUserActivity(ctx, run.TaskID, "补充一句", CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	if placeholder == nil {
		t.Fatal("no placeholder activity")
	}

	// 先等决策终态，再读占位：apply 失败只改决策，占位的终态化不能靠时序猜。
	waitRouteDecisionTerminal(t, store, run.TaskID)
	act := reloadTestActivity(t, store, placeholder.ID)
	if act.Status == ActivityStatusPending || act.Status == ActivityStatusRunning {
		t.Fatalf("placeholder is still %q after the deliver decision (%#v)", act.Status, act)
	}
	if act.Hidden {
		t.Fatalf("deliver during HumanGate was applied as a hidden hand-off: %#v", act)
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

	// 入队之后世界才变化：一个 agent 节点开始跑。
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
	router.Enqueue(RouterTrigger{
		Kind:           RouterTriggerUserMessage,
		TaskID:         run.TaskID,
		RootID:         root.ID,
		UserActivityID: userAct.ID,
		TurnID:         "msg:" + userAct.ID,
		Mode:           RouterModeRoute,
		Brief:          queuedBrief,
	})

	// 先等决策收尾再读 prompt：agent 一定在决策落终态之前被调用，这样测试失败时
	// 也不会留下仍在写库的 Router goroutine。
	waitRouteDecisionTerminal(t, store, run.TaskID)
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
