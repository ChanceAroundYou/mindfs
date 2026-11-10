package api

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"mindfs/server/internal/fs"
	"mindfs/server/internal/kanban"
)

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

	// D2 default: per_node creates its own session, named after the task.
	first, err := app.EnsureWorkflowNodeSession(ctx, workflowAdapterExec(rootID, task, node, nodeRun))
	if err != nil {
		t.Fatalf("per_node first: %v", err)
	}
	if got := sessionNameForTest(t, app, rootID, first); got != "工作流任务 / #7" {
		t.Fatalf("session name = %q, want 工作流任务 / #7", got)
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

func TestWorkflowNodeAdapterRunsTurnWithNodeProfileAndParsesEnvelope(t *testing.T) {
	app, rootID := newWorkflowAdapterTestApp(t)
	var captured workflowTurnRequest
	app.workflowTurnOverride = func(_ context.Context, req workflowTurnRequest) (string, error) {
		captured = req
		return `{"outcome":"done","output":{"files":["a.ts"]},"reason":""}`, nil
	}
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
	if files, ok := result.Output["files"].([]any); !ok || len(files) != 1 || files[0] != "a.ts" {
		t.Fatalf("output = %#v", result.Output)
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
	if !strings.Contains(captured.Prompt, "实现登录页") || !strings.Contains(captured.Prompt, "<mindfs-workflow-node-output") {
		t.Fatalf("prompt missing configured text or envelope contract: %q", captured.Prompt)
	}
}

func TestWorkflowNodeAdapterInvalidEnvelopeBecomesUnknown(t *testing.T) {
	app, rootID := newWorkflowAdapterTestApp(t)
	app.workflowTurnOverride = func(context.Context, workflowTurnRequest) (string, error) {
		return "完成了，但没有 JSON", nil
	}
	exec := workflowAdapterExec(rootID, kanban.Task{ID: "task-wf-3"}, kanban.WorkflowNode{ID: "build", Kind: kanban.NodeKindAgent},
		kanban.NodeRun{ID: "nr-10", RunID: "run-3", NodeID: "build", Attempt: 1, SessionKey: "sess-wf-10"})
	result, err := app.RunWorkflowNode(context.Background(), exec)
	if err != nil {
		t.Fatalf("invalid envelope must not be a transport error: %v", err)
	}
	if result.Outcome != kanban.WorkflowNodeUnknown || !strings.Contains(result.Reason, "JSON") {
		t.Fatalf("result = %#v, want unknown with JSON reason", result)
	}
}
