package kanban

import (
	"context"
	"testing"
)

// 用户裁定（2026-11-20）1：「补回填」。上一轮修复把回填放在「本轮还有 open 行」
// 的分支里，而 ListOpenWorkflowNodeRuns 只看 queued/running —— #86 这种已经
// 停下、甚至任务早已收尾的历史 Run 永远不会再被扫到：投影 0 个节点、回复一直
// 转圈。启动对账必须按 unknown 行反查历史 Run 并补齐。
func TestStartupRecoveryBackfillsHistoricalRunProjection(t *testing.T) {
	ctx := context.Background()
	store, svc, run, root := newWorkflowCommentFixture(t, "task-hist", "run-hist", WorkflowDefinition{
		Nodes: []WorkflowNode{
			{ID: "execute", Kind: NodeKindAgent, Params: map[string]any{"prompt": "归档会话"}},
			{ID: "verify", Kind: NodeKindAgent, Params: map[string]any{"prompt": "复核"}},
		},
		Edges: []WorkflowEdge{{From: "execute", To: "verify"}},
	})
	nr := startWorkflowAgentNode(t, store, run.ID, "execute", "sess-execute")
	if _, err := store.MarkWorkflowNodeRunUnknown(ctx, nr, "startup_ambiguous_side_effect"); err != nil {
		t.Fatalf("MarkWorkflowNodeRunUnknown: %v", err)
	}
	// 这一轮之后用户已经把卡片收尾：活跃对账分支（isTerminalStatus）会整条跳过，
	// 历史 Run 只能靠按 unknown 行反查的回填路径。
	if err := store.UpdateTaskStatus(ctx, "task-hist", StatusSuccess, true); err != nil {
		t.Fatalf("UpdateTaskStatus: %v", err)
	}
	acts := NewActivityStore(store.db)
	if err := acts.Insert(ctx, &TaskActivity{
		ID: "act-hist", TaskID: "task-hist", RootID: root.ID, Type: ActivityTypeAgentReply,
		Role: RoleAgent, Status: ActivityStatusRunning, RunID: run.ID, Body: "步骤 1/2: 归档",
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
	if got := reloaded.NodeStates["execute"]; got != NodeRunUnknown {
		t.Fatalf("execute 投影 = %q, want unknown（时间线不能是 0 个节点）", got)
	}
	if got := reloaded.NodeStates["verify"]; got != NodeRunCancelled {
		t.Fatalf("没跑过的 verify 投影 = %q, want cancelled（这一轮已经没人推进）", got)
	}
	activity, err := acts.Get(ctx, "act-hist")
	if err != nil {
		t.Fatalf("Get activity: %v", err)
	}
	if activity.Status != ActivityStatusCancelled {
		t.Fatalf("历史 Run 的回复活动状态 = %q, want cancelled（否则一直转圈）", activity.Status)
	}
	if activity.Summary == "" {
		t.Fatal("结算必须留一句可读说明")
	}

	// 幂等：回填只补缺失的键，第二次启动不再写事件。
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

// 回填只补缺失的键：已有终态证据绝不能被覆盖。投影由循环/合并收尾写入时没有
// 对应的顶层行，回填不能因为「找不到行」就把它冻结成 cancelled。
func TestStartupRecoveryBackfillKeepsExistingProjection(t *testing.T) {
	ctx := context.Background()
	store, svc, run, root := newWorkflowCommentFixture(t, "task-keep", "run-keep", WorkflowDefinition{
		Nodes: []WorkflowNode{
			{ID: "execute", Kind: NodeKindAgent, Params: map[string]any{"prompt": "归档会话"}},
			{ID: "verify", Kind: NodeKindAgent, Params: map[string]any{"prompt": "复核"}},
		},
		Edges: []WorkflowEdge{{From: "execute", To: "verify"}},
	})
	projected, err := store.WorkflowRunByID(ctx, run.ID)
	if err != nil {
		t.Fatalf("WorkflowRunByID: %v", err)
	}
	projected.NodeStates = map[string]string{"verify": NodeRunSuccess}
	if err := store.SaveWorkflowRunWithEvent(ctx, projected, WorkflowRunEvent{
		Type: WorkflowEventNodeSucceeded, NodeID: "verify",
	}); err != nil {
		t.Fatalf("SaveWorkflowRunWithEvent: %v", err)
	}
	nr := startWorkflowAgentNode(t, store, run.ID, "execute", "sess-execute")
	if _, err := store.MarkWorkflowNodeRunUnknown(ctx, nr, "startup_ambiguous_side_effect"); err != nil {
		t.Fatalf("MarkWorkflowNodeRunUnknown: %v", err)
	}
	if err := store.UpdateTaskStatus(ctx, "task-keep", StatusSuccess, true); err != nil {
		t.Fatalf("UpdateTaskStatus: %v", err)
	}

	if _, err := svc.RecoverOnStartupForRoot(ctx, root.ID, fakeSessionProbe{found: true, resumable: false}); err != nil {
		t.Fatalf("RecoverOnStartupForRoot: %v", err)
	}
	reloaded, err := store.WorkflowRunByID(ctx, run.ID)
	if err != nil {
		t.Fatalf("WorkflowRunByID: %v", err)
	}
	if got := reloaded.NodeStates["verify"]; got != NodeRunSuccess {
		t.Fatalf("已有 success 投影被覆盖成 %q（没有行不代表没跑过）", got)
	}
	if got := reloaded.NodeStates["execute"]; got != NodeRunUnknown {
		t.Fatalf("execute 投影 = %q, want unknown", got)
	}
}

// 还有节点被重排（这一轮会继续跑）时，历史 unknown 行不能顺手把回复结算掉。
func TestStartupRecoveryBackfillSkipsLiveRun(t *testing.T) {
	ctx := context.Background()
	store, svc, run, root := newWorkflowCommentFixture(t, "task-live", "run-live", WorkflowDefinition{
		Nodes: []WorkflowNode{{ID: "execute", Kind: NodeKindAgent, Params: map[string]any{"prompt": "归档会话"}}},
	})
	nr := startWorkflowAgentNode(t, store, run.ID, "execute", "sess-execute")
	if _, err := store.MarkWorkflowNodeRunUnknown(ctx, nr, "startup_ambiguous_side_effect"); err != nil {
		t.Fatalf("MarkWorkflowNodeRunUnknown: %v", err)
	}
	retry := NodeRun{
		ID: workflowNodeRunID(run.ID, "execute", "", 0, 2), RunID: run.ID, NodeID: "execute", Attempt: 2, Status: NodeRunQueued,
	}
	if _, won, err := store.ClaimWorkflowNodeRun(ctx, retry); err != nil || !won {
		t.Fatalf("ClaimWorkflowNodeRun(retry) won=%v err=%v", won, err)
	}
	acts := NewActivityStore(store.db)
	if err := acts.Insert(ctx, &TaskActivity{
		ID: "act-live", TaskID: "task-live", RootID: root.ID, Type: ActivityTypeAgentReply,
		Role: RoleAgent, Status: ActivityStatusRunning, RunID: run.ID,
	}); err != nil {
		t.Fatalf("Insert activity: %v", err)
	}

	plan, err := svc.RecoverOnStartupForRoot(ctx, root.ID, fakeSessionProbe{found: true, resumable: false})
	if err != nil {
		t.Fatalf("RecoverOnStartupForRoot: %v", err)
	}
	if len(plan.ResumeNodeRunIDs) == 0 {
		t.Fatal("queued 重试行必须被重排（这一轮会继续跑）")
	}
	activity, err := acts.Get(ctx, "act-live")
	if err != nil {
		t.Fatalf("Get activity: %v", err)
	}
	if activity.Status != ActivityStatusRunning {
		t.Fatalf("这一轮还会继续跑，回复不能被结算，got %q", activity.Status)
	}
}
