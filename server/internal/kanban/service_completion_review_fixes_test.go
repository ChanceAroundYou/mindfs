package kanban

import (
	"context"
	"strings"
	"testing"
	"time"
)

// 2026-11-21 全分支复审的 Critical / Important 修复。每条用例先红后绿，
// 钉住的是「实现真的会骗人」的那几个点，不是实现细节。

// 复审 Critical 1：直连任务没有契约，机检只能证明「对话收尾了」，证明不了
// 「事情做完了」。它必须和未声明契约的工作流一样走独立复核。
func TestCompleteTaskDirectAlwaysVerifies(t *testing.T) {
	ctx := context.Background()
	f := newDirectCompletionFixture(t)
	f.addActivity(t, TaskActivity{ID: "msg-1", Type: ActivityTypeUserMessage, Body: "看下这个问题"})
	f.addActivity(t, TaskActivity{ID: "reply-1", Type: ActivityTypeAgentReply, Status: ActivityStatusCompleted, ReplyToID: "msg-1"})
	must(t, f.store.UpdateTaskStatus(ctx, f.task.ID, StatusWaitingUser, false), "UpdateTaskStatus")
	agent := &verifierAgent{reply: `{"verdict":"pass","findings":["回复里给出了排查结论"]}`}
	f.svc.SetRouterAgent(agent)
	f.svc.SetRouterConfig(RouterConfig{Mode: RouterModeCoordinate, Agent: "claude", Model: "sonnet"})
	decision := insertVerifierDecision(t, f, "dec-direct", "答复完了")

	if err := f.svc.applyCompleteTask(ctx, f.store, f.task, decision, completionAction("答复完了")); err != nil {
		t.Fatalf("applyCompleteTask: %v", err)
	}
	waitCompletionStatus(t, f, CompletionStatusCompleted, 5*time.Second)
	if calls := agent.callCount(); calls != 1 {
		t.Fatalf("verifier calls = %d, want 1：直连任务没有契约，机检通过不等于做完", calls)
	}
}

// 复审 Critical 2：决策绑定的是「决策发生时的 Run」。apply 时若任务已经起了
// 新一轮（新 Run 的证据恰好齐全），绝不能拿新 Run 的证据替旧决策背书。
func TestCompleteTaskRejectsStaleDecisionRun(t *testing.T) {
	ctx := context.Background()
	f := newCompletionFixture(t, nodeDoneContract())
	// 决策发生时的 Run 已经收尾，任务又起了新一轮。
	f.run.NodeStates = map[string]string{"build": NodeRunSuccess}
	must(t, f.store.SaveWorkflowRun(ctx, f.run), "SaveWorkflowRun")
	next := &WorkflowRun{ID: "run-newer", TaskID: f.task.ID, Version: f.run.Version, Revision: f.run.Revision + 1, Graph: f.run.Graph}
	must(t, f.store.CreateWorkflowRunAndSetEngine(ctx, next), "CreateWorkflowRunAndSetEngine")
	must(t, f.store.UpsertWorkflowNodeRun(ctx, NodeRun{
		ID: "run-newer-build-1", RunID: next.ID, NodeID: "build", Attempt: 1,
		Status: NodeRunSuccess, Output: map[string]any{"outcome": "done", "files": "3 个文件", "summary": "改了 a.go"},
	}), "UpsertWorkflowNodeRun")

	action := completionAction("做完了")
	action.RunID = f.run.ID // 决策发生时绑定的 Run
	err := f.svc.applyCompleteTask(ctx, f.store, f.task, completionDecision(f, "dec-stale"), action)
	if err == nil {
		t.Fatal("决策绑定的 Run 已被新 Run 取代时必须拒绝，不能拿新 Run 的证据收尾")
	}
	if countTaskEvents(t, f, "completed") != 0 {
		t.Fatal("过期决策不得写完成事件")
	}
}

// 复审 Critical 2 的另一半：老决策没有 action.run_id 时按 brief 里的 Run 认，
// 同样不能回落到「当前最新 Run」。
func TestCompleteTaskUsesBriefRunWhenActionHasNone(t *testing.T) {
	ctx := context.Background()
	f := newCompletionFixture(t, nodeDoneContract())
	f.run.NodeStates = map[string]string{"build": NodeRunSuccess}
	must(t, f.store.SaveWorkflowRun(ctx, f.run), "SaveWorkflowRun")
	next := &WorkflowRun{ID: "run-newer-2", TaskID: f.task.ID, Version: f.run.Version, Revision: f.run.Revision + 1, Graph: f.run.Graph}
	must(t, f.store.CreateWorkflowRunAndSetEngine(ctx, next), "CreateWorkflowRunAndSetEngine")
	must(t, f.store.UpsertWorkflowNodeRun(ctx, NodeRun{
		ID: "run-newer-2-build-1", RunID: next.ID, NodeID: "build", Attempt: 1,
		Status: NodeRunSuccess, Output: map[string]any{"outcome": "done", "files": "3 个文件", "summary": "改了 a.go"},
	}), "UpsertWorkflowNodeRun")

	decision := completionDecision(f, "dec-brief-run")
	decision.BriefJSON = `{"version":1,"latest_run":{"id":"` + f.run.ID + `"}}`
	err := f.svc.applyCompleteTask(ctx, f.store, f.task, decision, completionAction("做完了"))
	if err == nil {
		t.Fatal("brief 指向的 Run 已被取代时必须拒绝")
	}
	if countTaskEvents(t, f, "completed") != 0 {
		t.Fatal("过期决策不得写完成事件")
	}
}

// 复审 Important 1：复核是异步的（模型可能跑一分钟）。结论回来时任务可能已经
// 被用户取消，此时 pass 不能把任务改写成完成。
func TestVerifierPassDoesNotOverwriteCancelledTask(t *testing.T) {
	ctx := context.Background()
	f := newCompletionFixture(t, nil)
	block := make(chan struct{})
	agent := &verifierAgent{reply: `{"verdict":"pass","findings":["看到了改动"]}`, block: block}
	f.svc.SetRouterAgent(agent)
	f.svc.SetRouterConfig(RouterConfig{Mode: RouterModeCoordinate, Agent: "claude", Model: "sonnet"})
	insertVerifierDecision(t, f, "dec-cancel-race", "做完了")

	done := make(chan error, 1)
	go func() { done <- f.svc.verifyCompletion(ctx, f.task.ID, f.run.ID, "dec-cancel-race") }()
	deadline := time.Now().Add(5 * time.Second)
	for agent.callCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("复核回合没有跑起来")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// 复核跑着的时候用户取消了这个任务。
	must(t, f.store.UpdateTaskStatus(ctx, f.task.ID, StatusCancelled, true), "UpdateTaskStatus")
	close(block)
	if err := <-done; err != nil {
		t.Fatalf("verifyCompletion: %v", err)
	}

	task, err := f.store.GetTask(ctx, f.task.ID)
	must(t, err, "GetTask")
	if task.Status != StatusCancelled {
		t.Fatalf("task status = %q, want cancelled：复核结论回来晚了，不能改写已取消的任务", task.Status)
	}
	if completion, found, err := f.store.GetTaskCompletion(ctx, f.task.ID); err != nil {
		t.Fatalf("GetTaskCompletion: %v", err)
	} else if found && completion.Status == CompletionStatusCompleted {
		t.Fatal("任务已取消，复核 pass 不能写成完成记录")
	}
	if rows := completionSystemRows(t, f, "completion-verifier-stale:dec-cancel-race"); len(rows) != 1 {
		t.Fatalf("stale verdict rows = %d, want 1：结论没用上要留下可核对的说明", len(rows))
	}
}

// 复审 Important 2：结果转换器兜底出来的 done 不是证据。节点照旧收尾（不阻塞
// 整块看板），但完成契约里的 node_done 不能拿它当「做完了」的证明。
func TestNodeDoneRejectsConverterFallback(t *testing.T) {
	f := newCompletionFixture(t, nodeDoneContract())
	f.addAttempt(t, "build", NodeRunSuccess, map[string]any{
		"outcome": "done", "files": "3 个文件", "summary": "改完了", "unverified": true,
	}, "")
	report := f.evaluate(t)
	if report.Passed {
		t.Fatal("转换器兜底的结论不能当作完成证据")
	}
	if len(report.Items) == 0 || report.Items[0].Pass {
		t.Fatalf("report items = %+v, want the node item to fail with a reason", report.Items)
	}
	if !strings.Contains(report.Items[0].Detail, "兜底") {
		t.Fatalf("detail = %q, want it to name the fallback", report.Items[0].Detail)
	}
}

// 复审 Important 5：没有可重试节点的缺口（缺文件、gate 没批、直连任务）不能
// 只交给 Router 碰运气 —— 必须确定性地停下来等人。
func TestCompletionWithoutRepairableGapParksForUser(t *testing.T) {
	f := newCompletionFixture(t, &CompletionContract{Requirements: []CompletionRequirement{
		{ID: "dist", Label: "产物存在", Kind: CompletionKindFileExists, Path: "dist/app.js"},
	}})
	blockCompletionOnce(t, f, "dec-needs-user")

	task, err := f.store.GetTask(context.Background(), f.task.ID)
	must(t, err, "GetTask")
	if task.Status != StatusWaitingUser {
		t.Fatalf("task status = %q, want waiting_user：没有可自动补做的缺口时必须停下等人", task.Status)
	}
	rows := completionSystemRows(t, f, "completion-needs-user:dec-needs-user")
	if len(rows) != 1 {
		t.Fatalf("needs-user rows = %d, want 1", len(rows))
	}
	if !strings.Contains(rows[0].Body, "介入") {
		t.Fatalf("row body = %q, want it to ask the user to step in", rows[0].Body)
	}
}

// 复审 Important 6：同一决策重放不能让补做预算凭空少一轮。
func TestBlockCompletionReplayKeepsRepairBudget(t *testing.T) {
	f := newCompletionFixture(t, nodeDoneContract())
	f.addAttempt(t, "build", NodeRunFail, map[string]any{"outcome": "blocked"}, "")

	blockCompletionOnce(t, f, "dec-replay")
	blockCompletionOnce(t, f, "dec-replay")
	if used := completionRecord(t, f).RepairsUsed; used != 1 {
		t.Fatalf("repairs_used = %d, want 1：同一决策重放只算一轮", used)
	}
	if rows := completionSystemRows(t, f, "completion-blocked:dec-replay"); len(rows) != 1 {
		t.Fatalf("blocked rows = %d, want 1", len(rows))
	}
}
