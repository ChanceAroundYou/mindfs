package kanban

import (
	"context"
	"testing"
)

// 线上 task_477ad2273fc64a2a（#88「收尾」）的图：最后一步是人工确认。
// 升级前发布的版本允许 gate 做最后节点，所以它也是生产里真实存在的形状。
func task88SettledGraph() WorkflowDefinition {
	return WorkflowDefinition{
		Nodes: []WorkflowNode{
			{ID: "build", Kind: NodeKindAgent, Params: map[string]any{"prompt": "实现收尾"}},
			{ID: "verify", Kind: NodeKindHumanGate, Params: map[string]any{"prompt": "确认继续"}},
		},
		Edges: []WorkflowEdge{{From: "build", To: "verify"}},
	}
}

// commitTestNodeStatus 写一个终态 NodeRun 并返回推进后的 Run 投影
// （CommitWorkflowNodeTransition 只动库里的行，内存里那份 revision 会过期）。
func commitTestNodeStatus(t *testing.T, store *TaskStore, runID, nodeID, status string) *WorkflowRun {
	t.Helper()
	ctx := context.Background()
	run, err := store.WorkflowRunByID(ctx, runID)
	if err != nil {
		t.Fatalf("WorkflowRunByID: %v", err)
	}
	nr := NodeRun{
		ID:      workflowNodeRunID(runID, nodeID, "", 0, 1),
		RunID:   runID,
		NodeID:  nodeID,
		Attempt: 1,
		Status:  status,
	}
	if _, _, err := store.CommitWorkflowNodeTransition(ctx, run, WorkflowNodeTransition{
		NodeRun: nr, EventType: "node_" + status,
	}); err != nil {
		t.Fatalf("CommitWorkflowNodeTransition(%s=%s): %v", nodeID, status, err)
	}
	run, err = store.WorkflowRunByID(ctx, runID)
	if err != nil {
		t.Fatalf("reload WorkflowRunByID: %v", err)
	}
	return run
}

// #88 现场：四个节点全部 success、任务停在 waiting_user（待审核），
// Router 却被告知「存在活动执行」，于是 router_run_workflow / message_agent
// 被校验层硬拒，用户的下一条留言只能得到「请先结算 Run」——界面上又根本没有
// 这个入口。整轮已经结束的 Run 不是活动执行。
func TestSituationBriefSettledRunIsNotActiveExecution(t *testing.T) {
	ctx := context.Background()
	taskID, runID := "task-settled", "run-settled"
	store, _, _, _ := newWorkflowCommentFixture(t, taskID, runID, task88SettledGraph())
	commitTestNodeStatus(t, store, runID, "build", NodeRunSuccess)
	run := commitTestNodeStatus(t, store, runID, "verify", NodeRunSuccess)
	if status := ProjectStatusFromRun(run); status != StatusSuccess {
		t.Fatalf("run projection = %q, want %q", status, StatusSuccess)
	}
	if err := store.UpdateTaskStatus(ctx, taskID, StatusWaitingUser, false); err != nil {
		t.Fatalf("UpdateTaskStatus: %v", err)
	}

	brief := briefFor(t, store, taskID)
	if brief.ActiveExecution != nil {
		t.Fatalf("settled run reported as active execution: %#v", brief.ActiveExecution)
	}
	// 同时说明这条 brief 对 Router 是什么样子：任务行 waiting_user、Run 无待批节点。
	if brief.LatestRun == nil || len(brief.LatestRun.WaitingUserNodes) != 0 {
		t.Fatalf("latest_run = %#v, want no waiting_user nodes", brief.LatestRun)
	}
	brief.Workflows = []RouterBriefWorkflow{{ID: "wf-x", Name: "x"}}
	action := RouterAction{Kind: RouterActionRunWorkflow, WorkflowID: "wf-x"}
	if err := validateRouterActions(brief, []RouterAction{action}, RouterConfig{}.Normalized()); err != nil {
		t.Fatalf("run_workflow after a settled run = %v, want accepted", err)
	}
}

// 真正的活动执行仍然必须挡住新开一轮：gate 停在 waiting_user 时只能说明、
// 由用户点批准/驳回，Router 不得 run_workflow。
func TestSituationBriefWaitingHumanGateStillBlocksNewExecution(t *testing.T) {
	ctx := context.Background()
	taskID, runID := "task-gate", "run-gate"
	store, _, _, _ := newWorkflowCommentFixture(t, taskID, runID, task88SettledGraph())
	commitTestNodeStatus(t, store, runID, "build", NodeRunSuccess)
	commitTestNodeStatus(t, store, runID, "verify", NodeRunWaitingUser)
	if err := store.UpdateTaskStatus(ctx, taskID, StatusWaitingUser, false); err != nil {
		t.Fatalf("UpdateTaskStatus: %v", err)
	}

	brief := briefFor(t, store, taskID)
	if brief.ActiveExecution == nil {
		t.Fatal("waiting gate lost its active execution; Router would start a new round over it")
	}
	if len(brief.ActiveExecution.CurrentNodes) != 0 {
		t.Fatalf("gate execution current_nodes = %#v, want none", brief.ActiveExecution.CurrentNodes)
	}
	brief.Workflows = []RouterBriefWorkflow{{ID: "wf-x", Name: "x"}}
	action := RouterAction{Kind: RouterActionRunWorkflow, WorkflowID: "wf-x"}
	if err := validateRouterActions(brief, []RouterAction{action}, RouterConfig{}.Normalized()); err == nil {
		t.Fatal("run_workflow accepted while a human gate is waiting")
	}
}

// 刚建出来、一个节点都还没写投影的 Run 仍然是活动执行：NodeStates 为空
// 不能被当成「已收尾」。
func TestSituationBriefUnstartedRunStaysActiveExecution(t *testing.T) {
	ctx := context.Background()
	taskID, runID := "task-fresh", "run-fresh"
	store, _, _, _ := newWorkflowCommentFixture(t, taskID, runID, task88SettledGraph())
	if err := store.UpdateTaskStatus(ctx, taskID, StatusRunning, false); err != nil {
		t.Fatalf("UpdateTaskStatus: %v", err)
	}

	brief := briefFor(t, store, taskID)
	if brief.ActiveExecution == nil {
		t.Fatal("unstarted run lost its active execution")
	}
}

// 暂停是操作员状态，不是「这一轮跑完了」：即使节点都收了，
// 暂停中的任务也不能被 Router 当成可以新开一轮。
func TestSituationBriefPausedTaskKeepsActiveExecution(t *testing.T) {
	ctx := context.Background()
	taskID, runID := "task-paused", "run-paused"
	store, _, _, _ := newWorkflowCommentFixture(t, taskID, runID, task88SettledGraph())
	commitTestNodeStatus(t, store, runID, "build", NodeRunSuccess)
	commitTestNodeStatus(t, store, runID, "verify", NodeRunSuccess)
	if err := store.UpdateTaskStatus(ctx, taskID, StatusPaused, false); err != nil {
		t.Fatalf("UpdateTaskStatus: %v", err)
	}

	brief := briefFor(t, store, taskID)
	if brief.ActiveExecution == nil {
		t.Fatal("paused task lost its active execution")
	}
	brief.Workflows = []RouterBriefWorkflow{{ID: "wf-x", Name: "x"}}
	action := RouterAction{Kind: RouterActionRunWorkflow, WorkflowID: "wf-x"}
	if err := validateRouterActions(brief, []RouterAction{action}, RouterConfig{}.Normalized()); err == nil {
		t.Fatal("run_workflow accepted on a paused task")
	}
}

