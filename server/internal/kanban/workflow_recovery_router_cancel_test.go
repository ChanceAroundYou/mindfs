package kanban

import (
	"context"
	"testing"
	"time"
)

// W1 T4：取消类故障的两个崩溃窗口，加上预算的重启口径。
//
//  1. cancel_run 写到一半崩（行已 cancelled、投影/任务状态没落）—— 重启把这一轮
//     幂等重放成「已取消」，不能留着 running 的投影把 Router 永久挡住。
//  2. cancel_node 写完行就崩，driver 没被唤醒 —— 下游节点还该继续跑，重启唤醒它。
//  3. 预算只从 router_decisions 现算，重启（新 Service）看到的用量必须不变。

func TestStartupRecoveryReplaysInterruptedRouterCancelRun(t *testing.T) {
	ctx := context.Background()
	store, svc, run, root := newWorkflowCommentFixture(t, "task-cancel", "run-cancel", WorkflowDefinition{
		Nodes: []WorkflowNode{
			{ID: "execute", Kind: NodeKindAgent, Params: map[string]any{"prompt": "改代码"}},
			{ID: "verify", Kind: NodeKindAgent, Params: map[string]any{"prompt": "验证"}},
		},
		Edges: []WorkflowEdge{{From: "execute", To: "verify"}},
	})
	nr := startWorkflowAgentNode(t, store, run.ID, "execute", "sess-execute")
	// 第一遍取消已经把行改成 cancelled 并落了 node_cancelled 事件，run_cancelled
	// 也写进了账本 —— 然后进程死在写投影之前。
	if _, err := store.CancelWorkflowNodeRun(ctx, nr, "router_cancel_run"); err != nil {
		t.Fatalf("CancelWorkflowNodeRun: %v", err)
	}
	if _, err := store.AppendWorkflowRunEvent(ctx, WorkflowRunEvent{
		RunID: run.ID, Type: WorkflowEventRunCancelled,
		Payload: map[string]any{"reason": "router_cancel_run"},
	}); err != nil {
		t.Fatalf("AppendWorkflowRunEvent: %v", err)
	}
	if err := store.UpdateTaskStatus(ctx, "task-cancel", StatusRunning, false); err != nil {
		t.Fatalf("UpdateTaskStatus: %v", err)
	}
	acts := NewActivityStore(store.db)
	if err := acts.Insert(ctx, &TaskActivity{
		ID: "act-cancel", TaskID: "task-cancel", RootID: root.ID, Type: ActivityTypeAgentReply,
		Role: RoleAgent, Status: ActivityStatusRunning, RunID: run.ID, Body: "步骤 1/2: 改代码",
	}); err != nil {
		t.Fatalf("Insert activity: %v", err)
	}

	if _, err := svc.RecoverOnStartupForRoot(ctx, root.ID, fakeSessionProbe{found: true, resumable: false}); err != nil {
		t.Fatalf("RecoverOnStartupForRoot: %v", err)
	}

	reloaded, err := store.WorkflowRunByID(ctx, run.ID)
	if err != nil {
		t.Fatalf("WorkflowRunByID: %v", err)
	}
	for _, id := range []string{"execute", "verify"} {
		if got := reloaded.NodeStates[id]; got != NodeRunCancelled {
			t.Fatalf("node %s projection = %q, want cancelled (states=%v)", id, got, reloaded.NodeStates)
		}
	}
	task, err := store.GetTask(ctx, "task-cancel")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != StatusWaitingUser {
		t.Fatalf("task status = %q, want waiting_user", task.Status)
	}
	activity, err := acts.Get(ctx, "act-cancel")
	if err != nil {
		t.Fatalf("Get activity: %v", err)
	}
	if activity.Status != ActivityStatusCancelled || activity.Summary == "" {
		t.Fatalf("reply activity = %+v, want cancelled with a readable summary", activity)
	}

	// 幂等：同一状态再启动一次，不得新增取消事件、也不得改任务状态。
	before, err := store.ListWorkflowRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatalf("ListWorkflowRunEvents: %v", err)
	}
	if _, err := svc.RecoverOnStartupForRoot(ctx, root.ID, fakeSessionProbe{found: true, resumable: false}); err != nil {
		t.Fatalf("second RecoverOnStartupForRoot: %v", err)
	}
	after, err := store.ListWorkflowRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatalf("ListWorkflowRunEvents: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("第二次启动不该新增事件：%d → %d", len(before), len(after))
	}
}

// 节点级取消不是「这一轮不要了」：下游节点还要继续跑。崩在唤醒之前时，重启负责唤醒。
func TestStartupRecoveryWakesRunAfterRouterNodeCancel(t *testing.T) {
	ctx := context.Background()
	store, svc, run, root := newWorkflowCommentFixture(t, "task-node-cancel", "run-node-cancel", WorkflowDefinition{
		Nodes: []WorkflowNode{
			{ID: "execute", Kind: NodeKindAgent, Params: map[string]any{"prompt": "改代码"}},
			{ID: "verify", Kind: NodeKindAgent, Params: map[string]any{"prompt": "验证"}},
		},
		Edges: []WorkflowEdge{{From: "execute", To: "verify"}},
	})
	nr := startWorkflowAgentNode(t, store, run.ID, "execute", "sess-execute")
	if _, err := store.CancelWorkflowNodeRun(ctx, nr, "router_cancel_node"); err != nil {
		t.Fatalf("CancelWorkflowNodeRun: %v", err)
	}
	// 没有 run_cancelled 事件：这一轮没被放弃，只是 driver 没被唤醒。
	if err := store.UpdateTaskStatus(ctx, "task-node-cancel", StatusRunning, false); err != nil {
		t.Fatalf("UpdateTaskStatus: %v", err)
	}
	runner := &scriptedWorkflowNodeRunner{outcomes: []WorkflowNodeResult{{Outcome: WorkflowNodeDone, Output: map[string]any{"summary": "ok"}}}}
	svc.SetWorkflowNodeRunner(runner)

	if _, err := svc.RecoverOnStartupForRoot(ctx, root.ID, fakeSessionProbe{found: true, resumable: false}); err != nil {
		t.Fatalf("RecoverOnStartupForRoot: %v", err)
	}
	if err := svc.WaitWorkflowRunIdle(ctx, root.ID, "task-node-cancel"); err != nil {
		t.Fatalf("WaitWorkflowRunIdle: %v", err)
	}
	if runner.runCalls != 1 {
		t.Fatalf("下游节点被派发 %d 次，want 1（取消一个节点不能让整轮停摆）", runner.runCalls)
	}
	reloaded, err := store.WorkflowRunByID(ctx, run.ID)
	if err != nil {
		t.Fatalf("WorkflowRunByID: %v", err)
	}
	if got := reloaded.NodeStates["verify"]; got != NodeRunSuccess {
		t.Fatalf("verify projection = %q, want success (states=%v)", got, reloaded.NodeStates)
	}
}

// 节点级取消的在线路径：取消一行之后，投影必须同步成 cancelled，driver 才能
// 越过它继续跑下游。只改行不改投影时，driver 会把这个节点当成「还没跑」反复
// 派发一个已取消的行（空转到步数上限），下游永远不动。
func TestRouterCancelNodeProjectsAndAdvancesDownstream(t *testing.T) {
	ctx := context.Background()
	store, svc, run, root := newWorkflowCommentFixture(t, "task-cancel-node-live", "run-cancel-node-live", WorkflowDefinition{
		Nodes: []WorkflowNode{
			{ID: "execute", Kind: NodeKindAgent, Params: map[string]any{"prompt": "改代码"}},
			{ID: "verify", Kind: NodeKindAgent, Params: map[string]any{"prompt": "验证"}},
		},
		Edges: []WorkflowEdge{{From: "execute", To: "verify"}},
	})
	nr := startWorkflowAgentNode(t, store, run.ID, "execute", "sess-execute")
	runner := &scriptedWorkflowNodeRunner{outcomes: []WorkflowNodeResult{{Outcome: WorkflowNodeDone, Output: map[string]any{"summary": "ok"}}}}
	svc.SetWorkflowNodeRunner(runner)

	if err := svc.CancelWorkflowNodeForRouter(ctx, root.ID, "task-cancel-node-live", nr.ID, "router_cancel_node"); err != nil {
		t.Fatalf("CancelWorkflowNodeForRouter: %v", err)
	}
	if err := svc.WaitWorkflowRunIdle(ctx, root.ID, "task-cancel-node-live"); err != nil {
		t.Fatalf("WaitWorkflowRunIdle: %v", err)
	}
	reloaded, err := store.WorkflowRunByID(ctx, run.ID)
	if err != nil {
		t.Fatalf("WorkflowRunByID: %v", err)
	}
	if got := reloaded.NodeStates["execute"]; got != NodeRunCancelled {
		t.Fatalf("execute projection = %q, want cancelled (states=%v)", got, reloaded.NodeStates)
	}
	if got := reloaded.NodeStates["verify"]; got != NodeRunSuccess {
		t.Fatalf("verify projection = %q, want success（取消一个节点不能让下游停摆）", got)
	}
	if runner.runCalls != 1 {
		t.Fatalf("下游节点被派发 %d 次，want 1", runner.runCalls)
	}
}

// 预算不信任内存：重启后新 Service 从 router_decisions 现算出的用量必须与重启前一致。
func TestSupervisionBudgetIsRecomputedFromPersistedDecisions(t *testing.T) {
	f := supervisionFixture(t, RouterSupervisionOn)
	seedSupervisionActions(t, f, "run-1", RouterTriggerRunFailed, 3)
	ctx := context.Background()
	now := time.Now().UTC()
	thisRun, lastHour := f.svc.supervisionActionUsage(ctx, f.store, "task-1", "run-1", now)
	if thisRun != 3 || lastHour != 3 {
		t.Fatalf("usage = %d/%d, want 3/3", thisRun, lastHour)
	}
	restarted := NewService(f.svc.Roots)
	thisRun2, lastHour2 := restarted.supervisionActionUsage(ctx, f.store, "task-1", "run-1", now)
	if thisRun2 != thisRun || lastHour2 != lastHour {
		t.Fatalf("重启后用量 = %d/%d, want %d/%d（预算只能从持久化决策现算）", thisRun2, lastHour2, thisRun, lastHour)
	}
}
