package api

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"mindfs/server/internal/fs"
	"mindfs/server/internal/kanban"
)

// fakeWorkflowOutputLLM stands in for the Router's one-shot model during the
// result-normalization step. It records prompts so tests can prove the worker
// never sees the JSON envelope contract.
type fakeWorkflowOutputLLM struct {
	prompts  []string
	response string
	err      error
}

func (f *fakeWorkflowOutputLLM) Call(_ context.Context, prompt string) (string, error) {
	f.prompts = append(f.prompts, prompt)
	if f.err != nil {
		return "", f.err
	}
	return f.response, nil
}

func newWorkflowAdapterTestApp(t *testing.T) (*AppContext, string) {
	t.Helper()
	parent := t.TempDir()
	registry := fs.NewRegistry(filepath.Join(parent, "registry.json"))
	projectPath := filepath.Join(parent, "project")
	if err := mkdirAllForSessionNaming(projectPath); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	root, err := registry.Upsert(projectPath)
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	return &AppContext{Dirs: registry}, root.ID
}

func workflowAdapterExec(rootID string, task kanban.Task, node kanban.WorkflowNode, nodeRun kanban.NodeRun) kanban.WorkflowNodeExecution {
	return kanban.WorkflowNodeExecution{
		RootID:        rootID,
		Task:          task,
		Node:          node,
		NodeRun:       nodeRun,
		SessionPolicy: kanban.WorkflowNodeSessionPolicy(node),
		Prompt:        "实现登录页",
	}
}

func TestWorkflowNodeAdapterSessionReusePolicies(t *testing.T) {
	ctx := context.Background()
	app, rootID := newWorkflowAdapterTestApp(t)
	task := kanban.Task{ID: "task-wf-1", TaskNumber: 7, Name: "工作流任务"}
	node := kanban.WorkflowNode{ID: "build", Name: "实现", Kind: kanban.NodeKindAgent}
	nodeRun := kanban.NodeRun{ID: "nr-1", RunID: "run-1", NodeID: "build", Attempt: 1}

	// D2 default: per_node creates its own session. 名字带上节点名 —— 同一条任务
	// 每轮都会新开 worker 会话，只叫「任务名 / #7」在会话列表里分不出谁是谁。
	first, err := app.EnsureWorkflowNodeSession(ctx, workflowAdapterExec(rootID, task, node, nodeRun))
	if err != nil {
		t.Fatalf("per_node first: %v", err)
	}
	if got := sessionNameForTest(t, app, rootID, first); got != "工作流任务 / #7 · 实现" {
		t.Fatalf("session name = %q, want 工作流任务 / #7 · 实现", got)
	}
	// A different node instance must not inherit the previous node's session.
	nodeRun2 := kanban.NodeRun{ID: "nr-2", RunID: "run-1", NodeID: "review", Attempt: 1}
	second, err := app.EnsureWorkflowNodeSession(ctx, workflowAdapterExec(rootID, task, node, nodeRun2))
	if err != nil {
		t.Fatalf("per_node second: %v", err)
	}
	if second == first {
		t.Fatal("per_node reused another node run's session")
	}
	// Resume: the NodeRun already has a bound session, so keep it.
	resumed := workflowAdapterExec(rootID, task, node, kanban.NodeRun{ID: "nr-1", RunID: "run-1", NodeID: "build", Attempt: 1, SessionKey: first})
	got, err := app.EnsureWorkflowNodeSession(ctx, resumed)
	if err != nil || got != first {
		t.Fatalf("per_node resume = %q err=%v, want %q", got, err, first)
	}
	// same_stage continues the earlier iteration/attempt session.
	same := workflowAdapterExec(rootID, task, node, kanban.NodeRun{ID: "nr-3", RunID: "run-1", NodeID: "build", Attempt: 2})
	same.SessionPolicy = kanban.SessionReuseSameStage
	same.ReuseSessionKey = second
	got, err = app.EnsureWorkflowNodeSession(ctx, same)
	if err != nil || got != second {
		t.Fatalf("same_stage = %q err=%v, want %q", got, err, second)
	}
	// always_new ignores the bound session.
	fresh := workflowAdapterExec(rootID, task, node, kanban.NodeRun{ID: "nr-4", RunID: "run-1", NodeID: "build", Attempt: 3, SessionKey: first})
	fresh.SessionPolicy = kanban.SessionReuseAlwaysNew
	got, err = app.EnsureWorkflowNodeSession(ctx, fresh)
	if err != nil {
		t.Fatalf("always_new: %v", err)
	}
	if got == first {
		t.Fatal("always_new reused the bound session")
	}
	// task_main explicitly joins the task main session.
	mainTask := task
	mainTask.MainSessionKey = first
	mainExec := workflowAdapterExec(rootID, mainTask, node, kanban.NodeRun{ID: "nr-5", RunID: "run-1", NodeID: "build", Attempt: 1})
	mainExec.SessionPolicy = kanban.SessionReuseTaskMain
	got, err = app.EnsureWorkflowNodeSession(ctx, mainExec)
	if err != nil || got != first {
		t.Fatalf("task_main = %q err=%v, want %q", got, err, first)
	}
}

// 同一条任务、同一个节点，两轮不同留言必须得到不同的会话名 —— 否则会话列表
// 里一排「任务名 / #N · 节点名」，用户分不出哪条是哪一轮（任务 #87 第 3 条）。
func TestWorkflowNodeAdapterSessionNamesDifferPerRound(t *testing.T) {
	ctx := context.Background()
	app, rootID := newWorkflowAdapterTestApp(t)
	task := kanban.Task{ID: "task-wf-name", TaskNumber: 12, Name: "任务详情面板修改"}
	node := kanban.WorkflowNode{ID: "build", Name: "实现", Kind: kanban.NodeKindAgent}

	round := func(nodeRunID, userInput string) string {
		t.Helper()
		exec := workflowAdapterExec(rootID, task, node, kanban.NodeRun{ID: nodeRunID, RunID: "run-" + nodeRunID, NodeID: "build", Attempt: 1})
		exec.UserInput = userInput
		key, err := app.EnsureWorkflowNodeSession(ctx, exec)
		if err != nil {
			t.Fatalf("ensure %s: %v", nodeRunID, err)
		}
		return sessionNameForTest(t, app, rootID, key)
	}

	first := round("nr-1", "把运行视图改成多轮展示")
	second := round("nr-2", "再补一个会话图标")
	if first == second {
		t.Fatalf("two rounds share the same session name %q", first)
	}
	if !strings.Contains(first, "把运行视图改成多轮展示") || !strings.Contains(second, "再补一个会话图标") {
		t.Fatalf("names must carry the triggering message: %q / %q", first, second)
	}
}

func TestWorkflowNodeAdapterWorkerAnswersNaturallyAndRouterNormalizes(t *testing.T) {
	app, rootID := newWorkflowAdapterTestApp(t)
	var captured workflowTurnRequest
	app.workflowTurnOverride = func(_ context.Context, req workflowTurnRequest) (string, error) {
		captured = req
		return "登录页已经实现，改了 login.tsx 和 auth.ts，验证通过。", nil
	}
	llm := &fakeWorkflowOutputLLM{response: `{"outcome":"done","output":{"summary":"登录页已实现并通过验证。"},"reason":""}`}
	app.SetWorkflowOutputLLM(llm)
	node := kanban.WorkflowNode{ID: "build", Name: "实现", Kind: kanban.NodeKindAgent, Params: map[string]any{
		"prompt":          "实现登录页",
		"agent":           "codex",
		"harness":         "codex-cli",
		"model":           "gpt-5.1-codex",
		"mode":            "edit",
		"effort":          "high",
		"fast_service":    "ON",
		"plan_mode":       true,
		"unrelated_param": "ignored",
	}}
	exec := workflowAdapterExec(rootID, kanban.Task{ID: "task-wf-2", TaskNumber: 9, Name: "登录页"}, node,
		kanban.NodeRun{ID: "nr-9", RunID: "run-2", NodeID: "build", Attempt: 1, SessionKey: "sess-wf-9"})

	result, err := app.RunWorkflowNode(context.Background(), exec)
	if err != nil {
		t.Fatalf("RunWorkflowNode: %v", err)
	}
	if result.Outcome != kanban.WorkflowNodeDone || result.SessionKey != "sess-wf-9" {
		t.Fatalf("result = %#v", result)
	}
	if got := result.Output["summary"]; got != "登录页已实现并通过验证。" {
		t.Fatalf("summary = %#v, want router-normalized summary", got)
	}
	if result.StartedAt.IsZero() || result.FinishedAt.IsZero() {
		t.Fatal("workflow result is missing start/finish evidence")
	}
	if captured.SessionKey != "sess-wf-9" || captured.RootID != rootID {
		t.Fatalf("turn scope = %#v", captured)
	}
	if captured.Agent != "codex" || captured.Model != "gpt-5.1-codex" || captured.Mode != "edit" ||
		captured.Effort != "high" || captured.FastService != "on" || !captured.PlanMode {
		t.Fatalf("node profile not forwarded: %#v", captured)
	}
	// The worker gets the task prompt only. It must not be asked to emit a JSON
	// envelope; that conversion belongs to the Router layer.
	if captured.Prompt != "实现登录页" {
		t.Fatalf("worker prompt = %q, want the configured prompt without an output contract", captured.Prompt)
	}
	if len(llm.prompts) != 1 {
		t.Fatalf("normalizer calls = %d, want 1", len(llm.prompts))
	}
	prompt := llm.prompts[0]
	for _, want := range []string{"实现登录页", "登录页已经实现", "summary"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("normalizer prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestWorkflowNodeAdapterRouterCanParkBlockedNode(t *testing.T) {
	app, rootID := newWorkflowAdapterTestApp(t)
	app.workflowTurnOverride = func(context.Context, workflowTurnRequest) (string, error) {
		return "缺少 API key，我没法继续。", nil
	}
	app.SetWorkflowOutputLLM(&fakeWorkflowOutputLLM{
		response: `{"outcome":"blocked","reason":"缺少 API key"}`,
	})
	exec := workflowAdapterExec(rootID, kanban.Task{ID: "task-wf-blocked"}, kanban.WorkflowNode{ID: "build", Kind: kanban.NodeKindAgent},
		kanban.NodeRun{ID: "nr-blocked", RunID: "run-blocked", NodeID: "build", Attempt: 1, SessionKey: "sess-blocked"})
	result, err := app.RunWorkflowNode(context.Background(), exec)
	if err != nil {
		t.Fatalf("RunWorkflowNode: %v", err)
	}
	if result.Outcome != kanban.WorkflowNodeBlocked || result.Reason != "缺少 API key" {
		t.Fatalf("result = %#v, want blocked with reason", result)
	}
}

func TestWorkflowNodeAdapterNormalizerFailureFallsBackToWorkerText(t *testing.T) {
	app, rootID := newWorkflowAdapterTestApp(t)
	app.workflowTurnOverride = func(context.Context, workflowTurnRequest) (string, error) {
		return "已经改好了，测试也过了。", nil
	}
	app.SetWorkflowOutputLLM(&fakeWorkflowOutputLLM{err: errors.New("model unavailable")})
	exec := workflowAdapterExec(rootID, kanban.Task{ID: "task-wf-3"}, kanban.WorkflowNode{ID: "build", Kind: kanban.NodeKindAgent},
		kanban.NodeRun{ID: "nr-10", RunID: "run-3", NodeID: "build", Attempt: 1, SessionKey: "sess-wf-10"})
	result, err := app.RunWorkflowNode(context.Background(), exec)
	if err != nil {
		t.Fatalf("normalizer failure must not be a transport error: %v", err)
	}
	if result.Outcome != kanban.WorkflowNodeDone {
		t.Fatalf("outcome = %q, want done fallback", result.Outcome)
	}
	if got := result.Output["summary"]; got != "已经改好了，测试也过了。" {
		t.Fatalf("summary = %#v, want raw worker text", got)
	}
}

func TestWorkflowNodeAdapterEmptyWorkerOutputIsSilent(t *testing.T) {
	app, rootID := newWorkflowAdapterTestApp(t)
	app.workflowTurnOverride = func(context.Context, workflowTurnRequest) (string, error) {
		return "   ", nil
	}
	llm := &fakeWorkflowOutputLLM{response: `{"outcome":"done","output":{"summary":"不应该被调用"}}`}
	app.SetWorkflowOutputLLM(llm)
	exec := workflowAdapterExec(rootID, kanban.Task{ID: "task-wf-empty"}, kanban.WorkflowNode{ID: "build", Kind: kanban.NodeKindAgent},
		kanban.NodeRun{ID: "nr-empty", RunID: "run-empty", NodeID: "build", Attempt: 1, SessionKey: "sess-empty"})
	result, err := app.RunWorkflowNode(context.Background(), exec)
	if err != nil {
		t.Fatalf("RunWorkflowNode: %v", err)
	}
	if result.Outcome != kanban.WorkflowNodeSilent {
		t.Fatalf("outcome = %q, want silent", result.Outcome)
	}
	if len(llm.prompts) != 0 {
		t.Fatalf("normalizer called for empty output: %d prompts", len(llm.prompts))
	}
}

func TestWorkflowNodeAdapterNormalizerSeesDownstreamConditionPaths(t *testing.T) {
	app, rootID := newWorkflowAdapterTestApp(t)
	app.workflowTurnOverride = func(context.Context, workflowTurnRequest) (string, error) {
		return "改了 3 个文件。", nil
	}
	llm := &fakeWorkflowOutputLLM{response: `{"outcome":"done","output":{"summary":"改了 3 个文件。","files":"3"},"reason":""}`}
	app.SetWorkflowOutputLLM(llm)
	exec := workflowAdapterExec(rootID, kanban.Task{ID: "task-wf-cond"}, kanban.WorkflowNode{ID: "build", Kind: kanban.NodeKindAgent},
		kanban.NodeRun{ID: "nr-cond", RunID: "run-cond", NodeID: "build", Attempt: 1, SessionKey: "sess-cond"})
	exec.Run = &kanban.WorkflowRun{ID: "run-cond", Graph: kanban.WorkflowDefinition{
		Nodes: []kanban.WorkflowNode{
			{ID: "build", Kind: kanban.NodeKindAgent},
			{ID: "decide", Kind: kanban.NodeKindCondition, Params: map[string]any{
				"expression": "build.output.files > 0",
				"routes":     []any{"yes", "no"},
			}},
		},
	}}
	if _, err := app.RunWorkflowNode(context.Background(), exec); err != nil {
		t.Fatalf("RunWorkflowNode: %v", err)
	}
	if len(llm.prompts) != 1 || !strings.Contains(llm.prompts[0], "build.output.files") {
		t.Fatalf("normalizer prompt must expose downstream condition paths:\n%v", llm.prompts)
	}
}

// 纯对话模式（workflow_choice=disabled）：Router 建一次任务主会话，之后每一轮
// 都发到同一个会话；用户留言原样作为这一轮的 prompt。
func TestRunDirectActivityCreatesAndReusesTaskMainSession(t *testing.T) {
	ctx := context.Background()
	app, rootID := newWorkflowAdapterTestApp(t)
	root, err := app.GetRoot(rootID)
	if err != nil {
		t.Fatalf("GetRoot: %v", err)
	}
	store, err := kanban.NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateTask(ctx,
		kanban.Task{ID: "task-direct", RootID: rootID, TaskNumber: 1, Name: "对话任务", Status: kanban.StatusPending},
		kanban.TaskEvent{ID: "ev-direct", TaskID: "task-direct", Type: "created"},
	); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	app.Kanban = kanban.NewService(app)
	app.Prober = testProberWithAgents("claude")

	var captured []workflowTurnRequest
	app.workflowTurnOverride = func(_ context.Context, req workflowTurnRequest) (string, error) {
		captured = append(captured, req)
		return "已经处理好了", nil
	}

	task, err := store.GetTask(ctx, "task-direct")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	reply, err := app.RunDirectActivity(ctx, task, "  帮我改一下  ")
	if err != nil {
		t.Fatalf("RunDirectActivity: %v", err)
	}
	if reply != "已经处理好了" {
		t.Fatalf("reply = %q", reply)
	}
	if len(captured) != 1 || captured[0].Prompt != "帮我改一下" || captured[0].SessionKey == "" {
		t.Fatalf("turn request = %#v, want trimmed user body on a bound session", captured)
	}
	after, err := store.GetTask(ctx, "task-direct")
	if err != nil {
		t.Fatalf("GetTask after: %v", err)
	}
	if after.MainSessionKey != captured[0].SessionKey {
		t.Fatalf("main_session_key = %q, want %q", after.MainSessionKey, captured[0].SessionKey)
	}

	task.MainSessionKey = after.MainSessionKey
	if _, err := app.RunDirectActivity(ctx, task, "再改一处"); err != nil {
		t.Fatalf("second RunDirectActivity: %v", err)
	}
	if len(captured) != 2 || captured[1].SessionKey != captured[0].SessionKey {
		t.Fatalf("second turn reused session = %#v, want %q", captured, captured[0].SessionKey)
	}
}

// TestSendWorkflowCommentCarriesNodeAgentProfile pins the T4 D-1 defect: a
// queued user comment is a continuation turn inside the node's session, so it
// must reuse the node's agent profile. Sending without Agent makes the turn
// layer reject the message with "agent required" and the user only sees a
// delivery-failed line.
func TestSendWorkflowCommentCarriesNodeAgentProfile(t *testing.T) {
	app, rootID := newWorkflowAdapterTestApp(t)
	var captured workflowTurnRequest
	app.workflowTurnOverride = func(_ context.Context, req workflowTurnRequest) (string, error) {
		captured = req
		return "", nil
	}
	node := kanban.WorkflowNode{ID: "slow", Name: "长任务", Kind: kanban.NodeKindAgent, Params: map[string]any{
		"agent":        "claude",
		"model":        "sonnet",
		"effort":       "low",
		"fast_service": "ON",
		"plan_mode":    true,
	}}
	exec := workflowAdapterExec(rootID, kanban.Task{ID: "task-wf-comment", TaskNumber: 11, Name: "长任务"}, node,
		kanban.NodeRun{ID: "nr-comment", RunID: "run-comment", NodeID: "slow", Attempt: 1, SessionKey: "sess-wf-comment"})

	if err := app.SendWorkflowComment(context.Background(), exec, "  补充：请在完成后顺便说明耗时。  "); err != nil {
		t.Fatalf("SendWorkflowComment: %v", err)
	}
	if captured.Agent != "claude" || captured.Model != "sonnet" || captured.Effort != "low" ||
		captured.FastService != "on" || !captured.PlanMode {
		t.Fatalf("comment turn lost the node agent profile: %#v", captured)
	}
	if captured.SessionKey != "sess-wf-comment" || captured.RootID != rootID {
		t.Fatalf("comment turn scope = %#v", captured)
	}
	if captured.Prompt != "补充：请在完成后顺便说明耗时。" {
		t.Fatalf("comment prompt = %q, want the trimmed user body", captured.Prompt)
	}
}
