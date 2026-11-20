package kanban

import (
	"context"
	"strings"
	"testing"
	"time"
)

// #86 现场（2026-11-19 看板归档）：
//
//	90s 回合超时 → 旧路径新建 Run → 进程重启把唯一节点标成 unknown，但
//	node_states_json 只在节点终态时写，于是投影一直是空的。
//	workflowRunHasOpenNodes 把空投影当成「刚建的 Run」，ActiveExecution 永久挂着，
//	Router 只能反复说「我不能新开一轮」。
//
// 用户裁定：unknown 算已收尾（节点已经不在跑），但仍然要少出 unknown。
// 「已收尾」与 workflowNodeStateTerminal 是两套语义 —— 后者必须继续把 unknown
// 当非终态，否则被取代时不会冻结成 cancelled。

func newSettledGateFixture(t *testing.T) (*TaskStore, Task, *WorkflowRun) {
	t.Helper()
	ctx := context.Background()
	store, _, run, _ := newWorkflowCommentFixture(t, "task-settled", "run-settled", WorkflowDefinition{
		Nodes: []WorkflowNode{{ID: "execute", Kind: NodeKindAgent, Params: map[string]any{"prompt": "归档会话"}}},
	})
	// 重启对账把任务推到 waiting_user（unknown 要人看），这正是 #86 的形态。
	if err := store.UpdateTaskStatus(ctx, "task-settled", StatusWaitingUser, false); err != nil {
		t.Fatalf("UpdateTaskStatus: %v", err)
	}
	task, err := store.GetTask(ctx, "task-settled")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	return store, task, run
}

func markNodeRunUnknown(t *testing.T, store *TaskStore, nr NodeRun) {
	t.Helper()
	changed, err := store.MarkWorkflowNodeRunUnknown(context.Background(), nr, "startup_ambiguous_side_effect")
	if err != nil {
		t.Fatalf("MarkWorkflowNodeRunUnknown: %v", err)
	}
	if !changed {
		t.Fatalf("node run %s was not transitioned to unknown", nr.ID)
	}
}

func runWorkflowActionError(brief *SituationBrief) error {
	brief.Workflows = []RouterBriefWorkflow{{ID: "wf-1", Name: "归档"}}
	return validateRouterActions(brief, []RouterAction{{
		Kind: RouterActionRunWorkflow, WorkflowID: "wf-1", SessionMode: RouterSessionModeNew,
	}}, RouterConfig{}.Normalized())
}

func TestSituationBriefUnknownNodeRunDoesNotBlockNewExecution(t *testing.T) {
	store, task, run := newSettledGateFixture(t)
	nr := startWorkflowAgentNode(t, store, run.ID, "execute", "sess-execute")
	markNodeRunUnknown(t, store, nr)

	brief := briefFor(t, store, task.ID)
	if brief.ActiveExecution != nil {
		t.Fatalf("unknown 节点已经不在跑，不能再挂活动执行：%+v", brief.ActiveExecution)
	}
	if err := runWorkflowActionError(brief); err != nil {
		t.Fatalf("unknown 节点必须放行新一轮执行，got %v", err)
	}
	if brief.LatestRun == nil || brief.LatestRun.NodeSummary["unknown"] != 1 {
		t.Fatalf("node_summary 要如实报 unknown，got %+v", brief.LatestRun)
	}
}

func TestSituationBriefUnknownProjectionDoesNotBlockNewExecution(t *testing.T) {
	ctx := context.Background()
	store, task, run := newSettledGateFixture(t)
	nr := startWorkflowAgentNode(t, store, run.ID, "execute", "sess-execute")
	markNodeRunUnknown(t, store, nr)
	// 恢复路径补写投影之后同样必须放行（两种形态都要成立）。
	loaded, err := store.WorkflowRunByID(ctx, run.ID)
	if err != nil {
		t.Fatalf("WorkflowRunByID: %v", err)
	}
	loaded.NodeStates["execute"] = NodeRunUnknown
	if err := store.SaveWorkflowRunWithEvent(ctx, loaded, WorkflowRunEvent{Type: WorkflowEventNodeUnknown}); err != nil {
		t.Fatalf("SaveWorkflowRunWithEvent: %v", err)
	}

	brief := briefFor(t, store, task.ID)
	if brief.ActiveExecution != nil {
		t.Fatalf("unknown 投影也不能挂活动执行：%+v", brief.ActiveExecution)
	}
	if err := runWorkflowActionError(brief); err != nil {
		t.Fatalf("unknown 投影必须放行新一轮执行，got %v", err)
	}
}

func TestSituationBriefRunningNodeRunStillBlocksNewExecution(t *testing.T) {
	store, task, run := newSettledGateFixture(t)
	startWorkflowAgentNode(t, store, run.ID, "execute", "sess-execute")

	brief := briefFor(t, store, task.ID)
	if brief.ActiveExecution == nil {
		t.Fatal("running 节点必须仍然是活动执行")
	}
	err := runWorkflowActionError(brief)
	if err == nil || !strings.Contains(err.Error(), "execution is active") {
		t.Fatalf("running 节点必须挡住新一轮，got %v", err)
	}
}

// 投影说 fail、但已经排了 attempt+1 的 queued 行（重试）：节点其实还会动，
// 不能读投影就判「已收尾」，否则 Router 会在重试在飞时再开一条执行。
func TestSituationBriefQueuedRetryOverridesSettledProjection(t *testing.T) {
	ctx := context.Background()
	store, task, run := newSettledGateFixture(t)
	commitTestNodeStatus(t, store, run.ID, "execute", NodeRunFail)
	retry := NodeRun{
		ID:      workflowNodeRunID(run.ID, "execute", "", 0, 2),
		RunID:   run.ID,
		NodeID:  "execute",
		Attempt: 2,
		Status:  NodeRunQueued,
	}
	if _, won, err := store.ClaimWorkflowNodeRun(ctx, retry); err != nil || !won {
		t.Fatalf("ClaimWorkflowNodeRun(retry) won=%v err=%v", won, err)
	}

	brief := briefFor(t, store, task.ID)
	if brief.ActiveExecution == nil {
		t.Fatal("排队的重试仍会让这个节点动起来，必须算活动执行")
	}
	if err := runWorkflowActionError(brief); err == nil {
		t.Fatal("重试在排队时必须挡住新一轮")
	}
}

// markMergeAmbiguousNodeRun 造一条「独立 worktree 已绑定、续合并失败」的 unknown
// 行：output 里带 worktree_error，正是 resumeWorkflowNodeMergeAfterRestart 的落库形状。
func markMergeAmbiguousNodeRun(t *testing.T, store *TaskStore, runID, nodeID string) NodeRun {
	t.Helper()
	ctx := context.Background()
	nr := NodeRun{
		ID: workflowNodeRunID(runID, nodeID, "", 0, 1), RunID: runID, NodeID: nodeID, Attempt: 1,
		Status: NodeRunQueued, WorktreeID: "wt-" + nodeID, WorktreePath: "/tmp/wt-" + nodeID, WorktreeBranch: "task-merge",
	}
	claimed, won, err := store.ClaimWorkflowNodeRun(ctx, nr)
	if err != nil || !won {
		t.Fatalf("ClaimWorkflowNodeRun(merge) won=%v err=%v", won, err)
	}
	if err := store.BeginWorkflowNodeRun(ctx, claimed, time.Now().UTC()); err != nil {
		t.Fatalf("BeginWorkflowNodeRun(merge): %v", err)
	}
	if err := store.FinishWorkflowNodeRun(ctx, claimed, NodeRunUnknown,
		map[string]any{"worktree_error": "merge conflict in kanban.go", "worktree": map[string]any{"merged": false}},
		time.Now().UTC(), WorkflowEventNodeUnknown, map[string]any{"reason": "merge conflict in kanban.go"}); err != nil {
		t.Fatalf("FinishWorkflowNodeRun(merge unknown): %v", err)
	}
	return claimed
}

// 用户裁定（2026-11-20）4：merge 来的 unknown 与重启来的 unknown 不是一回事。
// 前者分支可能只合了一半，主树状态未收敛；此时放行新一轮，新节点会在不明状态
// 的主树上继续写。它必须继续挡闸门。
func TestSituationBriefMergeUnknownNodeRunStillBlocksNewExecution(t *testing.T) {
	store, task, run := newSettledGateFixture(t)
	markMergeAmbiguousNodeRun(t, store, run.ID, "execute")

	brief := briefFor(t, store, task.ID)
	if brief.ActiveExecution == nil {
		t.Fatal("worktree 未收敛的 unknown 必须仍然是活动执行")
	}
	err := runWorkflowActionError(brief)
	if err == nil || !strings.Contains(err.Error(), "execution is active") {
		t.Fatalf("合并不明的 unknown 必须挡住新一轮，got %v", err)
	}
}

// 挡住的条件只对「最新一次尝试」成立：同节点重试成功后，旧 attempt 的合并不明
// 行不能再把闸门永远锁死。
func TestSituationBriefMergeUnknownSupersededByRetryAllowsNewExecution(t *testing.T) {
	ctx := context.Background()
	store, task, run := newSettledGateFixture(t)
	markMergeAmbiguousNodeRun(t, store, run.ID, "execute")
	retry := NodeRun{
		ID:      workflowNodeRunID(run.ID, "execute", "", 0, 2),
		RunID:   run.ID,
		NodeID:  "execute",
		Attempt: 2,
		Status:  NodeRunQueued,
	}
	claimed, won, err := store.ClaimWorkflowNodeRun(ctx, retry)
	if err != nil || !won {
		t.Fatalf("ClaimWorkflowNodeRun(retry) won=%v err=%v", won, err)
	}
	if err := store.BeginWorkflowNodeRun(ctx, claimed, time.Now().UTC()); err != nil {
		t.Fatalf("BeginWorkflowNodeRun(retry): %v", err)
	}
	if err := store.FinishWorkflowNodeRun(ctx, claimed, NodeRunSuccess, map[string]any{"outcome": "done"},
		time.Now().UTC(), WorkflowEventNodeSucceeded, map[string]any{"status": NodeRunSuccess}); err != nil {
		t.Fatalf("FinishWorkflowNodeRun(retry success): %v", err)
	}

	brief := briefFor(t, store, task.ID)
	if brief.ActiveExecution != nil {
		t.Fatalf("重试成功后旧 attempt 的合并不明不该再挡：%+v", brief.ActiveExecution)
	}
	if err := runWorkflowActionError(brief); err != nil {
		t.Fatalf("重试成功后必须放行新一轮，got %v", err)
	}
}

// 「已收尾」不能顺手改掉 workflowNodeStateTerminal：unknown 仍然不是终态，
// 被新留言取代时必须冻结成 cancelled。
func TestUnknownIsSettledButNotTerminal(t *testing.T) {
	if workflowNodeStateTerminal(NodeRunUnknown) {
		t.Fatal("unknown 不是终态：被取代时要冻结成 cancelled")
	}
	if !workflowNodeStateSettled(NodeRunUnknown) {
		t.Fatal("unknown 必须算已收尾，否则 #86 的幽灵执行永远挡着新回合")
	}
	for _, state := range []string{NodeRunRunning, NodeRunQueued, NodeRunWaitingUser, NodeRunPending} {
		if workflowNodeStateSettled(state) {
			t.Fatalf("%s 还会自己变化，不能算已收尾", state)
		}
	}
}

// 恢复补写 unknown 投影之后，Router 必须看得到这个节点的状态，而不是「一个节点
// 都没有」——#86 的时间线就显示 0 个节点。
func TestSituationBriefReportsUnknownFromProjection(t *testing.T) {
	ctx := context.Background()
	store, task, run := newSettledGateFixture(t)
	commitTestNodeStatus(t, store, run.ID, "execute", NodeRunFail)
	loaded, err := store.WorkflowRunByID(ctx, run.ID)
	if err != nil {
		t.Fatalf("WorkflowRunByID: %v", err)
	}
	loaded.NodeStates["execute"] = NodeRunUnknown
	if err := store.SaveWorkflowRunWithEvent(ctx, loaded, WorkflowRunEvent{Type: WorkflowEventNodeUnknown}); err != nil {
		t.Fatalf("SaveWorkflowRunWithEvent: %v", err)
	}
	brief := briefFor(t, store, task.ID)
	if brief.LatestRun == nil {
		t.Fatal("latest_run missing")
	}
	if got := brief.LatestRun.NodeSummary["total"]; got != 1 {
		t.Fatalf("node_summary total = %d, want 1", got)
	}
	if got := brief.LatestRun.NodeSummary["unknown"]; got != 1 {
		t.Fatalf("node_summary unknown = %d, want 1", got)
	}
}
