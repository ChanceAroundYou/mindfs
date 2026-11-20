package kanban

import (
	"context"
	"testing"
)

// 启动对账把 NodeRun 标成 unknown 时，NodeStates 从来没被写过（投影只在节点
// 终态时写）。#86 因此同时坏了三件事：
//  1. 时间线 0 个节点（空投影）；
//  2. 挂在这一轮上的回复活动永远 running（转圈不停）；
//  3. Router 被空投影判成「有活动执行」，永久拒绝新回合。
//
// 恢复路径必须把这半写状态补齐：投影写 unknown、回复活动结算掉。
func TestStartupRecoveryUnknownWritesProjectionAndSettlesReply(t *testing.T) {
	ctx := context.Background()
	store, svc, run, root := newWorkflowCommentFixture(t, "task-ghost", "run-ghost", WorkflowDefinition{
		Nodes: []WorkflowNode{{ID: "execute", Kind: NodeKindAgent, Params: map[string]any{"prompt": "归档会话"}}},
	})
	nr := startWorkflowAgentNode(t, store, run.ID, "execute", "sess-execute")
	acts := NewActivityStore(store.db)
	if err := acts.Insert(ctx, &TaskActivity{
		ID: "act-ghost", TaskID: "task-ghost", RootID: root.ID, Type: ActivityTypeAgentReply,
		Role: RoleAgent, Status: ActivityStatusRunning, RunID: run.ID, Body: "步骤 1/1: 归档",
	}); err != nil {
		t.Fatalf("Insert activity: %v", err)
	}

	plan, err := svc.RecoverOnStartupForRoot(ctx, root.ID, fakeSessionProbe{found: true, resumable: false})
	if err != nil {
		t.Fatalf("RecoverOnStartupForRoot: %v", err)
	}
	if len(plan.UnknownIDs) != 1 || plan.UnknownIDs[0] != nr.ID {
		t.Fatalf("unknown ids = %v, want [%s]", plan.UnknownIDs, nr.ID)
	}

	reloaded, err := store.WorkflowRunByID(ctx, run.ID)
	if err != nil {
		t.Fatalf("WorkflowRunByID: %v", err)
	}
	if got := reloaded.NodeStates["execute"]; got != NodeRunUnknown {
		t.Fatalf("node projection = %q, want %q（否则时间线永远是 0 个节点）", got, NodeRunUnknown)
	}

	activity, err := acts.Get(ctx, "act-ghost")
	if err != nil {
		t.Fatalf("Get activity: %v", err)
	}
	if activity.Status != ActivityStatusCancelled {
		t.Fatalf("回复活动状态 = %q, want cancelled（否则一直转圈）", activity.Status)
	}
	if activity.Summary == "" {
		t.Fatal("结算必须留一句可读说明")
	}
	if activity.Body != "" {
		t.Fatalf("进度正文要清空，got %q", activity.Body)
	}
}

// unknown 补齐之后，Router 看到的 brief 必须已经放行新回合 —— 两半修复要合得上。
func TestStartupRecoveryUnknownUnblocksRouterTurn(t *testing.T) {
	ctx := context.Background()
	store, svc, run, root := newWorkflowCommentFixture(t, "task-ghost", "run-ghost", WorkflowDefinition{
		Nodes: []WorkflowNode{{ID: "execute", Kind: NodeKindAgent, Params: map[string]any{"prompt": "归档会话"}}},
	})
	startWorkflowAgentNode(t, store, run.ID, "execute", "sess-execute")
	if _, err := svc.RecoverOnStartupForRoot(ctx, root.ID, fakeSessionProbe{found: true, resumable: false}); err != nil {
		t.Fatalf("RecoverOnStartupForRoot: %v", err)
	}
	task, err := store.GetTask(ctx, "task-ghost")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != StatusWaitingUser {
		t.Fatalf("task status = %q, want waiting_user", task.Status)
	}
	brief := briefFor(t, store, task.ID)
	if brief.ActiveExecution != nil {
		t.Fatalf("恢复后不能再挂活动执行：%+v", brief.ActiveExecution)
	}
	if err := runWorkflowActionError(brief); err != nil {
		t.Fatalf("恢复后必须放行新一轮，got %v", err)
	}
}

// 还有节点被重排时（wake），这一轮会继续跑：不能把回复结算掉。
func TestStartupRecoveryRequeueKeepsReplyRunning(t *testing.T) {
	ctx := context.Background()
	store, svc, run, root := newWorkflowCommentFixture(t, "task-requeue", "run-requeue", WorkflowDefinition{
		Nodes: []WorkflowNode{
			{ID: "execute", Kind: NodeKindAgent, Params: map[string]any{"prompt": "归档会话"}},
			{ID: "verify", Kind: NodeKindAgent, Params: map[string]any{"prompt": "复核"}},
		},
		Edges: []WorkflowEdge{{From: "execute", To: "verify"}},
	})
	// execute 已经跑完，verify 只是排了队（还没发出去）：重启后重排，这一轮继续。
	commitTestNodeStatus(t, store, run.ID, "execute", NodeRunSuccess)
	queued := NodeRun{ID: workflowNodeRunID(run.ID, "verify", "", 0, 1), RunID: run.ID, NodeID: "verify", Attempt: 1, Status: NodeRunQueued}
	if _, won, err := store.ClaimWorkflowNodeRun(ctx, queued); err != nil || !won {
		t.Fatalf("ClaimWorkflowNodeRun(verify) won=%v err=%v", won, err)
	}
	acts := NewActivityStore(store.db)
	if err := acts.Insert(ctx, &TaskActivity{
		ID: "act-requeue", TaskID: "task-requeue", RootID: root.ID, Type: ActivityTypeAgentReply,
		Role: RoleAgent, Status: ActivityStatusRunning, RunID: run.ID,
	}); err != nil {
		t.Fatalf("Insert activity: %v", err)
	}

	if _, err := svc.RecoverOnStartupForRoot(ctx, root.ID, fakeSessionProbe{found: true, resumable: false}); err != nil {
		t.Fatalf("RecoverOnStartupForRoot: %v", err)
	}
	activity, err := acts.Get(ctx, "act-requeue")
	if err != nil {
		t.Fatalf("Get activity: %v", err)
	}
	if activity.Status != ActivityStatusRunning {
		t.Fatalf("这一轮还会继续跑，回复不能被结算，got %q", activity.Status)
	}
}
