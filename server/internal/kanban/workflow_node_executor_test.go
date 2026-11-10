package kanban

import (
	"context"
	"strings"
	"testing"

	"mindfs/server/internal/fs"
)

type fakeWorkflowNodeRunner struct {
	ensureCalls int
	runCalls    int
	exec        WorkflowNodeExecution
	result      WorkflowNodeResult
}

func (f *fakeWorkflowNodeRunner) EnsureWorkflowNodeSession(_ context.Context, exec WorkflowNodeExecution) (string, error) {
	f.ensureCalls++
	f.exec = exec
	return "wf-session-1", nil
}

func (f *fakeWorkflowNodeRunner) RunWorkflowNode(_ context.Context, exec WorkflowNodeExecution) (WorkflowNodeResult, error) {
	f.runCalls++
	f.exec = exec
	return f.result, nil
}

type countingLegacyRunner struct {
	runStageCalls int
}

func (r *countingLegacyRunner) CreateTaskWorktree(context.Context, string, string, string, string) (WorktreeInfo, error) {
	return WorktreeInfo{}, nil
}
func (r *countingLegacyRunner) EnsureAgentSession(context.Context, AgentStageExecution) (string, error) {
	return "", nil
}
func (r *countingLegacyRunner) RunAgentStage(context.Context, AgentStageExecution) (StageResult, error) {
	r.runStageCalls++
	return StageResult{Outcome: StageOutcomeDone}, nil
}
func (r *countingLegacyRunner) TaskUpdated(string, TaskDetail) {}

func TestWorkflowAgentNodeUsesWorkflowRunnerNotLegacyStageRunner(t *testing.T) {
	ctx := context.Background()
	root := fs.NewRootInfo("wf-runner-root", "wf-runner-root", t.TempDir())
	store, err := NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	defer store.Close()
	if _, err := store.CreateTask(ctx, Task{ID: "wf-runner-task", RootID: root.ID, Status: StatusPending, Stages: []StageTemplate{userStage("input")}},
		StageRun{ID: "wf-runner-stage", TaskID: "wf-runner-task", StageIndex: 0, StageName: "input", Role: RoleUser, Status: StageStatusPending},
		TaskEvent{ID: "wf-runner-event", TaskID: "wf-runner-task", Type: "created"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	run := &WorkflowRun{ID: "wf-runner-run", TaskID: "wf-runner-task", Revision: 1,
		Graph: WorkflowDefinition{Nodes: []WorkflowNode{{ID: "build", Kind: NodeKindAgent, Params: map[string]any{"prompt": "实现登录页"}}}}}
	node := run.Graph.Nodes[0]
	nodeRun := NodeRun{ID: "nr-build", RunID: run.ID, NodeID: "build", LoopPath: "l/2", Iteration: 1, Attempt: 2, Status: NodeRunQueued}

	wf := &fakeWorkflowNodeRunner{result: WorkflowNodeResult{Outcome: WorkflowNodeDone, Output: map[string]any{"summary": "ok"}}}
	legacy := &countingLegacyRunner{}
	svc := NewService(nil, nil)
	svc.SetRunner(legacy)
	svc.SetWorkflowNodeRunner(wf)

	result, err := svc.StartWorkflowNode(ctx, store, run, node, nodeRun)
	if err != nil {
		t.Fatalf("StartWorkflowNode: %v", err)
	}
	if legacy.runStageCalls != 0 {
		t.Fatalf("legacy RunAgentStage called %d times; workflow must not dual-write stages", legacy.runStageCalls)
	}
	if wf.ensureCalls != 1 || wf.runCalls != 1 {
		t.Fatalf("workflow runner calls ensure=%d run=%d, want 1/1", wf.ensureCalls, wf.runCalls)
	}
	got := wf.exec
	if got.NodeRun.ID != nodeRun.ID || got.NodeRun.RunID != run.ID || got.NodeRun.NodeID != node.ID ||
		got.NodeRun.LoopPath != "l/2" || got.NodeRun.Iteration != 1 || got.NodeRun.Attempt != 2 {
		t.Fatalf("runner received NodeRun %#v, want exact identity %#v", got.NodeRun, nodeRun)
	}
	if got.NodeRun.SessionKey != "wf-session-1" || got.Prompt != "实现登录页" || got.SessionPolicy != SessionReusePerNode {
		t.Fatalf("runner exec = %#v", got)
	}
	if got.Task.ID != "wf-runner-task" || got.RootID != root.ID {
		t.Fatalf("runner task scope = %q root=%q", got.Task.ID, got.RootID)
	}
	if result.Outcome != WorkflowNodeDone || result.Output["summary"] != "ok" || result.SessionKey != "wf-session-1" {
		t.Fatalf("result = %#v", result)
	}
}

func TestWorkflowNodeSessionPolicyDefaultsToPerNode(t *testing.T) {
	cases := []struct {
		param string
		want  string
	}{
		{"", SessionReusePerNode},
		{"per_node", SessionReusePerNode},
		{"task_main", SessionReuseTaskMain},
		{"same_stage", SessionReuseSameStage},
		{"always_new", SessionReuseAlwaysNew},
		{"something_else", SessionReusePerNode},
	}
	for _, tc := range cases {
		node := WorkflowNode{ID: "n", Kind: NodeKindAgent, Params: map[string]any{"session_reuse_policy": tc.param}}
		if got := WorkflowNodeSessionPolicy(node); got != tc.want {
			t.Fatalf("policy(%q) = %q, want %q", tc.param, got, tc.want)
		}
	}
}

func TestWorkflowAgentNodeRequiresPrompt(t *testing.T) {
	svc := NewService(nil, nil)
	svc.SetWorkflowNodeRunner(&fakeWorkflowNodeRunner{})
	_, err := svc.StartWorkflowNode(context.Background(), &TaskStore{}, &WorkflowRun{ID: "r", TaskID: "t"}, WorkflowNode{ID: "n", Kind: NodeKindAgent}, NodeRun{ID: "nr", RunID: "r", NodeID: "n"})
	if err == nil || !strings.Contains(err.Error(), "params.prompt") {
		t.Fatalf("missing prompt err = %v", err)
	}
}

func TestParseWorkflowNodeEnvelopeStrictness(t *testing.T) {
	valid, err := ParseWorkflowNodeEnvelope(`{"outcome":"done","output":{"files":2},"reason":""}`)
	if err != nil {
		t.Fatalf("valid envelope: %v", err)
	}
	if valid.Outcome != WorkflowNodeDone || valid.Output["files"] != float64(2) {
		t.Fatalf("valid result = %#v", valid)
	}
	blocked, err := ParseWorkflowNodeEnvelope(`{"outcome":"blocked","reason":"缺权限"}`)
	if err != nil || blocked.Outcome != WorkflowNodeBlocked || blocked.Reason != "缺权限" {
		t.Fatalf("blocked = %#v err=%v", blocked, err)
	}
	if _, err := ParseWorkflowNodeEnvelope(`{"outcome":"blocked"}`); err == nil {
		t.Fatal("blocked without reason must fail")
	}
	if _, err := ParseWorkflowNodeEnvelope(`{"outcome":"maybe"}`); err == nil {
		t.Fatal("invalid outcome must fail")
	}
	if _, err := ParseWorkflowNodeEnvelope("完成了"); err == nil {
		t.Fatal("plain text must not be auto-wrapped")
	}
	if _, err := ParseWorkflowNodeEnvelope(`{"outcome":"done"}{"outcome":"done"}`); err == nil {
		t.Fatal("trailing JSON must fail")
	}
	if _, err := ParseWorkflowNodeEnvelope(`{"outcome":"done","extra":1}`); err == nil {
		t.Fatal("unknown field must fail")
	}
	if _, err := ParseWorkflowNodeEnvelope(""); err == nil {
		t.Fatal("empty output must fail")
	}
}
