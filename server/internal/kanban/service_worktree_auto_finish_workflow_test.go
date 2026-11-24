package kanban

import (
	"context"
	"os"
	"testing"
	"time"
)

// workflowAutoFinishTwoNodeGraph 是竞态的最小图：build → review。上游落定后引擎
// 才会为 review 建树（PrepareWorkflowNodeWorktree 见 WorktreePath 为空）。
func workflowAutoFinishTwoNodeGraph() WorkflowDefinition {
	return WorkflowDefinition{
		Nodes: []WorkflowNode{
			{ID: "build", Kind: NodeKindAgent, Params: map[string]any{"prompt": "实现"}},
			{ID: "review", Kind: NodeKindAgent, Params: map[string]any{"prompt": "审查"}},
		},
		Edges: []WorkflowEdge{{From: "build", To: "review"}},
	}
}

// attachWorkflowRun 把 finishFixture 建出的任务绑到一个真实 WorkflowRun 上。
// 引擎绑定是 workflowRunForTask 的入口，缺它 autoFinishRunStillOpen 会当普通
// 对话任务放行，测试就测不到被测判据。
func attachWorkflowRun(t *testing.T, store *TaskStore, taskID, runID string, graph WorkflowDefinition) *WorkflowRun {
	t.Helper()
	run := &WorkflowRun{ID: runID, TaskID: taskID, Revision: 1, Graph: graph, TriggeredBy: WorkflowTriggeredByUser}
	if err := store.CreateWorkflowRunAndSetEngine(context.Background(), run); err != nil {
		t.Fatalf("CreateWorkflowRunAndSetEngine: %v", err)
	}
	return run
}

// loadWorkflowRun 读回权威 Run（测试里推进节点时用它，避免手上是过期快照）。
func loadWorkflowRun(t *testing.T, store *TaskStore, runID string) *WorkflowRun {
	t.Helper()
	run, err := store.WorkflowRunByID(context.Background(), runID)
	if err != nil {
		t.Fatalf("WorkflowRunByID: %v", err)
	}
	return run
}

// countFinishAutoEvents 数任务事件里的自动收尾排队次数。
func countFinishAutoEvents(t *testing.T, store *TaskStore, taskID, eventType string) int {
	t.Helper()
	events, err := store.ListEvents(context.Background(), taskID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	count := 0
	for _, event := range events {
		if event.Type == eventType {
			count++
		}
	}
	return count
}

// finishWorktreeGone 轮询到 worktree 目录被拆掉为止。
func finishWorktreeGone(t *testing.T, path string) {
	t.Helper()
	waitForCondition(t, func() bool {
		_, err := os.Stat(path)
		return err != nil
	})
}

// settleWorkflowNode 走真实的终态推进（running→terminal），这样 finalize 里的
// 重试点才会按生产路径被触发。
func settleWorkflowNode(t *testing.T, svc *Service, store *TaskStore, runID string, node WorkflowNode, nodeRun NodeRun) {
	t.Helper()
	run := loadWorkflowRun(t, store, runID)
	if _, err := svc.finishWorkflowNodeAttempt(context.Background(), store, run, node, nodeRun,
		NodeRunSuccess, WorkflowEventNodeSucceeded, map[string]any{"summary": "ok"}, ""); err != nil {
		t.Fatalf("finish %s: %v", node.ID, err)
	}
}

// 回归：任务被显式置为终态时，Run 里还有节点在跑。这一拍里清场就是
// 「清了又建」—— 引擎紧接着会为下游节点建树。守卫必须以「Run 已无 open 节点」
// 为准，而不是只看 task.Status / 末节点是否在跑。
func TestAutoFinishWorktreeWaitsForTerminalWorkflowNode(t *testing.T) {
	ctx := context.Background()
	svc, root, _, worktreePath := finishFixture(t, "")
	taskID := mustTaskID(t, svc, root.ID)
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	graph := workflowAutoFinishTwoNodeGraph()
	attachWorkflowRun(t, store, taskID, "run-auto-finish-live", graph)
	// 两个节点都已越过状态检查、在跑（in-flight 窗口）：终态提交后引擎仍可能
	// 为它们补建树。只要求「有 running 行」即可，不必等 DAG 自己推进。
	build := startWorkflowAgentNode(t, store, "run-auto-finish-live", "build", "sess-build")
	review := startWorkflowAgentNode(t, store, "run-auto-finish-live", "review", "sess-review")

	svc.SetSessionRunningProbe(func(string) bool { return false })
	svc.SetAutoFinishEnabled(true)
	exec := &fakeFinishExecutor{reply: "done\n" + finishTurnDoneMarker}
	svc.SetDirectExecutor(exec)

	if _, err := svc.setTaskStatus(ctx, root.ID, taskID, StatusSuccess, "task_completed", "", true); err != nil {
		t.Fatalf("set status: %v", err)
	}
	if n := countFinishAutoEvents(t, store, taskID, "finish_auto_queued"); n != 0 {
		t.Fatalf("finish_auto_queued = %d while a node is still open, want 0", n)
	}
	if _, err := os.Stat(worktreePath); err != nil {
		t.Fatalf("worktree must survive while the run still owns nodes: %v", err)
	}
	if exec.prompt != "" {
		t.Fatalf("a finish turn must not start while the run is open, prompt=%q", exec.prompt)
	}

	// 只剩一个 open 节点时仍然算开。
	settleWorkflowNode(t, svc, store, "run-auto-finish-live", graph.Nodes[0], build)
	if n := countFinishAutoEvents(t, store, taskID, "finish_auto_queued"); n != 0 {
		t.Fatalf("finish_auto_queued = %d with one open node left, want 0", n)
	}
	if _, err := os.Stat(worktreePath); err != nil {
		t.Fatalf("worktree must survive a partially open run: %v", err)
	}

	// 最后一个节点落定的那一拍，收敛点必须自己补一次触发。
	settleWorkflowNode(t, svc, store, "run-auto-finish-live", graph.Nodes[1], review)
	waitForCondition(t, func() bool {
		return countFinishAutoEvents(t, store, taskID, "finish_auto_queued") == 1
	})
	finishWorktreeGone(t, worktreePath)
}

// 回归的另一半：上游已落定、下游还没有行（"两边都没有"）也算 open。这正是
// 引擎刚要 claim 下游、还没落 activate 的那一秒。
func TestAutoFinishWorktreeWaitsForUnclaimedDownstreamNode(t *testing.T) {
	ctx := context.Background()
	svc, root, _, worktreePath := finishFixture(t, "")
	taskID := mustTaskID(t, svc, root.ID)
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	graph := workflowAutoFinishTwoNodeGraph()
	attachWorkflowRun(t, store, taskID, "run-auto-finish-gap", graph)
	build := startWorkflowAgentNode(t, store, "run-auto-finish-gap", "build", "sess-build")

	svc.SetSessionRunningProbe(func(string) bool { return false })
	svc.SetAutoFinishEnabled(true)
	svc.SetDirectExecutor(&fakeFinishExecutor{reply: "done\n" + finishTurnDoneMarker})

	if _, err := svc.setTaskStatus(ctx, root.ID, taskID, StatusSuccess, "task_completed", "", true); err != nil {
		t.Fatalf("set status: %v", err)
	}
	settleWorkflowNode(t, svc, store, "run-auto-finish-gap", graph.Nodes[0], build)
	if n := countFinishAutoEvents(t, store, taskID, "finish_auto_queued"); n != 0 {
		t.Fatalf("finish_auto_queued = %d in the claim gap, want 0", n)
	}
	if _, err := os.Stat(worktreePath); err != nil {
		t.Fatalf("worktree must survive the claim gap: %v", err)
	}
}

// 收敛的对偶面：没有 Run 的纯对话任务不能被新守卫误伤。
func TestAutoFinishWorktreeIgnoresConversationTask(t *testing.T) {
	ctx := context.Background()
	svc, root, _, worktreePath := finishFixture(t, "")
	taskID := mustTaskID(t, svc, root.ID)
	svc.SetSessionRunningProbe(func(string) bool { return false })
	svc.SetAutoFinishEnabled(true)
	svc.SetDirectExecutor(&fakeFinishExecutor{reply: "done\n" + finishTurnDoneMarker})

	if _, err := svc.setTaskStatus(ctx, root.ID, taskID, StatusSuccess, "task_completed", "", true); err != nil {
		t.Fatalf("set status: %v", err)
	}
	store, _ := svc.taskStore(root.ID)
	waitForCondition(t, func() bool {
		return countFinishAutoEvents(t, store, taskID, "finish_auto_queued") == 1
	})
	finishWorktreeGone(t, worktreePath)
}

// legacy_import 的 Run 是迁移时的审计快照，引擎永远不会再驱动它；
// 它的 open 投影不能把自动收尾永久钉死。
func TestAutoFinishWorktreeIgnoresLegacyImportRun(t *testing.T) {
	ctx := context.Background()
	svc, root, _, worktreePath := finishFixture(t, "")
	taskID := mustTaskID(t, svc, root.ID)
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	run := &WorkflowRun{
		ID: "run-legacy-import", TaskID: taskID, Revision: 1,
		Graph: workflowAutoFinishTwoNodeGraph(), TriggeredBy: WorkflowTriggeredByLegacyImport,
	}
	if err := store.CreateWorkflowRunAndSetEngine(ctx, run); err != nil {
		t.Fatalf("CreateWorkflowRunAndSetEngine: %v", err)
	}

	svc.SetSessionRunningProbe(func(string) bool { return false })
	svc.SetAutoFinishEnabled(true)
	svc.SetDirectExecutor(&fakeFinishExecutor{reply: "done\n" + finishTurnDoneMarker})

	if _, err := svc.setTaskStatus(ctx, root.ID, taskID, StatusSuccess, "task_completed", "", true); err != nil {
		t.Fatalf("set status: %v", err)
	}
	waitForCondition(t, func() bool {
		return countFinishAutoEvents(t, store, taskID, "finish_auto_queued") == 1
	})
	finishWorktreeGone(t, worktreePath)
}

// 收敛点的重试只在任务已终态/已归档时补排：还在跑的 Run 由既有终态路径负责。
func TestAutoFinishRetryTriggerNeedsTerminalTask(t *testing.T) {
	svc, root, _, _ := finishFixture(t, "")
	taskID := mustTaskID(t, svc, root.ID)
	store, err := svc.taskStore(root.ID)
	if err != nil {
		t.Fatalf("taskStore: %v", err)
	}
	graph := workflowAutoFinishTwoNodeGraph()
	attachWorkflowRun(t, store, taskID, "run-not-terminal", graph)
	build := startWorkflowAgentNode(t, store, "run-not-terminal", "build", "sess-build")

	svc.SetSessionRunningProbe(func(string) bool { return false })
	svc.SetAutoFinishEnabled(true)
	svc.SetDirectExecutor(&fakeFinishExecutor{reply: "done\n" + finishTurnDoneMarker})

	settleWorkflowNode(t, svc, store, "run-not-terminal", graph.Nodes[0], build)
	time.Sleep(100 * time.Millisecond)
	if n := countFinishAutoEvents(t, store, taskID, "finish_auto_queued"); n != 0 {
		t.Fatalf("a running task must not be queued for auto finish, got %d", n)
	}
}
