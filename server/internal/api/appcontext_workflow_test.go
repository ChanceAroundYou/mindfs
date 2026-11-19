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

	// 默认策略是 run（2026-11-20）：一轮 Run 一条会话。首次没有 Run 会话时建
	// 一条 —— 名字不带节点名，它不属于某个节点，只带这轮留言片段。
	runExec := workflowAdapterExec(rootID, task, node, nodeRun)
	runExec.UserInput = "把这轮做完"
	first, err := app.EnsureWorkflowNodeSession(ctx, runExec)
	if err != nil {
		t.Fatalf("run first: %v", err)
	}
	if got := sessionNameForTest(t, app, rootID, first); got != "工作流任务 / #7 · 把这轮做完" {
		t.Fatalf("run session name = %q, want 工作流任务 / #7 · 把这轮做完", got)
	}
	// 同一轮 Run 的另一个节点拿到的是同一条会话（prepare 已把 key 写回 Run）。
	second := workflowAdapterExec(rootID, task, node, kanban.NodeRun{ID: "nr-2", RunID: "run-1", NodeID: "review", Attempt: 1})
	second.RunSessionKey = first
	got, err := app.EnsureWorkflowNodeSession(ctx, second)
	if err != nil || got != first {
		t.Fatalf("run second node = %q err=%v, want %q", got, err, first)
	}
	// 升级前的 Run 没有共享会话，但节点已绑过会话：恢复时必须继续它，不能被
	// 切到一段新对话。
	legacy := workflowAdapterExec(rootID, task, node, kanban.NodeRun{ID: "nr-legacy", RunID: "run-1", NodeID: "build", Attempt: 1, SessionKey: first})
	got, err = app.EnsureWorkflowNodeSession(ctx, legacy)
	if err != nil || got != first {
		t.Fatalf("run legacy resume = %q err=%v, want %q", got, err, first)
	}

	// 显式 per_node：每个节点实例自己的会话，名字带上节点名以区分。
	perNode := func(runID, nodeID string) string {
		t.Helper()
		exec := workflowAdapterExec(rootID, task, node, kanban.NodeRun{ID: "nr-" + nodeID, RunID: runID, NodeID: nodeID, Attempt: 1})
		exec.SessionPolicy = kanban.SessionReusePerNode
		key, err := app.EnsureWorkflowNodeSession(ctx, exec)
		if err != nil {
			t.Fatalf("per_node %s: %v", nodeID, err)
		}
		return key
	}
	nodeFirst := perNode("run-per-node", "build")
	if got := sessionNameForTest(t, app, rootID, nodeFirst); got != "工作流任务 / #7 · 实现" {
		t.Fatalf("per_node session name = %q, want 工作流任务 / #7 · 实现", got)
	}
	if nodeSecond := perNode("run-per-node", "review"); nodeSecond == nodeFirst {
		t.Fatal("per_node reused another node run's session")
	}
	// Resume: the NodeRun already has a bound session, so keep it.
	resumed := workflowAdapterExec(rootID, task, node, kanban.NodeRun{ID: "nr-1", RunID: "run-1", NodeID: "build", Attempt: 1, SessionKey: nodeFirst})
	resumed.SessionPolicy = kanban.SessionReusePerNode
	got, err = app.EnsureWorkflowNodeSession(ctx, resumed)
	if err != nil || got != nodeFirst {
		t.Fatalf("per_node resume = %q err=%v, want %q", got, err, nodeFirst)
	}
	// same_stage continues the earlier iteration/attempt session.
	same := workflowAdapterExec(rootID, task, node, kanban.NodeRun{ID: "nr-3", RunID: "run-1", NodeID: "build", Attempt: 2})
	same.SessionPolicy = kanban.SessionReuseSameStage
	same.ReuseSessionKey = nodeFirst
	got, err = app.EnsureWorkflowNodeSession(ctx, same)
	if err != nil || got != nodeFirst {
		t.Fatalf("same_stage = %q err=%v, want %q", got, err, nodeFirst)
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

// 重试是同一条留言、同一个节点、新的 attempt：节点名与留言都一样，只有 attempt
// 不同。线上出现过（task_94ec32d 的 execute 重试），两个会话名必须仍能分开。
func TestWorkflowNodeAdapterSessionNamesDifferPerAttempt(t *testing.T) {
	ctx := context.Background()
	app, rootID := newWorkflowAdapterTestApp(t)
	task := kanban.Task{ID: "task-wf-attempt", TaskNumber: 13, Name: "收尾"}
	node := kanban.WorkflowNode{ID: "execute", Name: "执行", Kind: kanban.NodeKindAgent}

	attempt := func(nodeRunID string, n int) string {
		t.Helper()
		exec := workflowAdapterExec(rootID, task, node, kanban.NodeRun{ID: nodeRunID, RunID: "run-attempt", NodeID: "execute", Attempt: n})
		// 重试标记属于「每节点一条会话」的语义；run 策略下重试回到同一条 Run
		// 会话（那正是默认要的行为），所以这里显式 per_node 来钉命名规则。
		exec.SessionPolicy = kanban.SessionReusePerNode
		exec.UserInput = "请提交修改"
		key, err := app.EnsureWorkflowNodeSession(ctx, exec)
		if err != nil {
			t.Fatalf("ensure %s: %v", nodeRunID, err)
		}
		return sessionNameForTest(t, app, rootID, key)
	}

	first := attempt("nr-a1", 1)
	retry := attempt("nr-a2", 2)
	if first == retry {
		t.Fatalf("retry reused the first attempt's name %q", first)
	}
	if !strings.Contains(retry, "第2次") {
		t.Fatalf("retry name %q must carry the attempt marker", retry)
	}
	if strings.Contains(first, "第1次") {
		t.Fatalf("first attempt name %q must stay clean", first)
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
