package kanban

import (
	"context"
	"testing"
)

// legacy 导入留下的占位行是第三种「永远转圈」：它的 run_id 不为空，所以
// 直连对账（run_id=''）和 Run 侧结算（只有活 Run 才会被结算）都不会碰它。
// 现场：act-legacy-reply-task_ebe31569ee25f4ff（2026-11-12）绑着
// run-legacy-…，任务早已 success+归档，详情页却一直显示「正在想」。
func TestStartupRecoverySettlesLegacyImportReply(t *testing.T) {
	ctx := context.Background()
	store, svc, root := newDirectReplyRecoveryFixture(t, "task-legacy-reply", "sess-legacy-reply", StatusSuccess)
	run := seedLegacyImportRun(t, ctx, store, "task-legacy-reply", map[string]string{"agent-1": NodeRunPending},
		"nr-legacy-task-legacy-reply-1", NodeRunPending)
	acts := NewActivityStore(store.db)
	if err := acts.Insert(ctx, &TaskActivity{
		ID: "act-legacy-reply", TaskID: "task-legacy-reply", RootID: root.ID,
		Type: ActivityTypeAgentReply, Role: RoleAgent, Status: ActivityStatusPending, RunID: run.ID,
	}); err != nil {
		t.Fatalf("Insert activity: %v", err)
	}
	// 会话活跃位在这里不适用：legacy Run 的执行早就结束了，探针说什么都不能
	// 让一条 11 月的占位回复继续转圈。
	svc.SetSessionRunningProbe(func(string) bool { return true })

	if _, err := svc.RecoverOnStartupForRoot(ctx, root.ID, nil); err != nil {
		t.Fatalf("RecoverOnStartupForRoot: %v", err)
	}

	activity, err := acts.Get(ctx, "act-legacy-reply")
	if err != nil {
		t.Fatalf("Get activity: %v", err)
	}
	if activity.Status != ActivityStatusCancelled {
		t.Fatalf("legacy 占位回复状态 = %q, want cancelled（否则永远显示正在想）", activity.Status)
	}
	if activity.Summary == "" {
		t.Fatal("结算必须留一句可读说明")
	}
}

// legacy Run 的 NodeRun 同样会永远停在 pending/running（现场最早停在 09-28）。
// 任务已经终态，这些行是历史快照，必须和投影一起冻结成 cancelled；否则重开
// 这张历史任务时，Router brief 会看到「还有开放节点」而拒绝新动作。
func TestStartupRecoveryFreezesLegacyImportNodeRunsAndProjection(t *testing.T) {
	ctx := context.Background()
	store, svc, root := newDirectReplyRecoveryFixture(t, "task-legacy-run", "sess-legacy-run", StatusCancelled)
	run := seedLegacyImportRun(t, ctx, store, "task-legacy-run", map[string]string{"agent-1": NodeRunRunning},
		"nr-legacy-task-legacy-run-1", NodeRunRunning)

	if _, err := svc.RecoverOnStartupForRoot(ctx, root.ID, nil); err != nil {
		t.Fatalf("RecoverOnStartupForRoot: %v", err)
	}

	nodes, err := store.ListWorkflowNodeRuns(ctx, run.ID)
	if err != nil {
		t.Fatalf("ListWorkflowNodeRuns: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("node runs = %d, want 1", len(nodes))
	}
	if nodes[0].Status != NodeRunCancelled {
		t.Fatalf("legacy NodeRun 状态 = %q, want cancelled", nodes[0].Status)
	}
	reloaded, err := store.WorkflowRunByID(ctx, run.ID)
	if err != nil {
		t.Fatalf("WorkflowRunByID: %v", err)
	}
	if got := reloaded.NodeStates["agent-1"]; got != NodeRunCancelled {
		t.Fatalf("legacy Run 投影 = %q, want cancelled", got)
	}
}

// 任务还没结束（暂停/运行/待审核）的 legacy Run 一律不碰：那可能是用户正在
// 继续处理的历史任务，冻结它的节点等于替用户做决定。
func TestStartupRecoveryKeepsLegacyRunOfNonTerminalTask(t *testing.T) {
	ctx := context.Background()
	store, svc, root := newDirectReplyRecoveryFixture(t, "task-legacy-paused", "sess-legacy-paused", StatusPaused)
	run := seedLegacyImportRun(t, ctx, store, "task-legacy-paused", map[string]string{"agent-1": NodeRunPending},
		"nr-legacy-task-legacy-paused-1", NodeRunPending)

	if _, err := svc.RecoverOnStartupForRoot(ctx, root.ID, nil); err != nil {
		t.Fatalf("RecoverOnStartupForRoot: %v", err)
	}

	nodes, err := store.ListWorkflowNodeRuns(ctx, run.ID)
	if err != nil {
		t.Fatalf("ListWorkflowNodeRuns: %v", err)
	}
	if len(nodes) != 1 || nodes[0].Status != NodeRunPending {
		t.Fatalf("非终态任务的 legacy NodeRun 被改写了：%+v", nodes)
	}
}

// seedLegacyImportRun 造一条一次性迁移留下的审计 Run（triggered_by=legacy_import）
// 和一个停在 nodeStatus 的顶层 NodeRun。
func seedLegacyImportRun(t *testing.T, ctx context.Context, store *TaskStore, taskID string, states map[string]string, nodeRunID, nodeStatus string) *WorkflowRun {
	t.Helper()
	run := &WorkflowRun{
		ID:          "run-legacy-" + taskID,
		TaskID:      taskID,
		Version:     "legacy",
		Revision:    1,
		Graph:       wfDef(wfNode("agent-1", NodeKindAgent)),
		NodeStates:  states,
		TriggeredBy: WorkflowTriggeredByLegacyImport,
	}
	if err := store.CreateWorkflowRunAndSetEngine(ctx, run); err != nil {
		t.Fatalf("CreateWorkflowRunAndSetEngine: %v", err)
	}
	if _, _, err := store.ClaimWorkflowNodeRun(ctx, NodeRun{
		ID: nodeRunID, RunID: run.ID, NodeID: "agent-1", Attempt: 1, Status: nodeStatus,
	}); err != nil {
		t.Fatalf("ClaimWorkflowNodeRun: %v", err)
	}
	return run
}
