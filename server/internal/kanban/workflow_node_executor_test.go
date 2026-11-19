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

func TestWorkflowAgentNodeUsesWorkflowRunner(t *testing.T) {
	ctx := context.Background()
	root := fs.NewRootInfo("wf-runner-root", "wf-runner-root", t.TempDir())
	store, err := NewTaskStore(root)
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}
	defer store.Close()
	if _, err := store.CreateTask(ctx, Task{ID: "wf-runner-task", RootID: root.ID, Status: StatusPending},
		TaskEvent{ID: "wf-runner-event", TaskID: "wf-runner-task", Type: "created"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	run := &WorkflowRun{ID: "wf-runner-run", TaskID: "wf-runner-task", Revision: 1,
		Graph: WorkflowDefinition{Nodes: []WorkflowNode{{ID: "build", Kind: NodeKindAgent, Params: map[string]any{"prompt": "实现登录页"}}}}}
	// session_reuse_policy=run 的「先到先得」要写回 workflow_runs.session_key，
	// 所以 Run 必须是持久化的行——生产路径里它本来就由 driver 从库里读出来。
	if err := store.CreateWorkflowRunAndSetEngine(ctx, run); err != nil {
		t.Fatalf("CreateWorkflowRunAndSetEngine: %v", err)
	}
	node := run.Graph.Nodes[0]
	nodeRun := NodeRun{ID: "nr-build", RunID: run.ID, NodeID: "build", LoopPath: "l/2", Iteration: 1, Attempt: 2, Status: NodeRunQueued}

	wf := &fakeWorkflowNodeRunner{result: WorkflowNodeResult{Outcome: WorkflowNodeDone, Output: map[string]any{"summary": "ok"}}}
	svc := NewService(nil)
	svc.SetRunner(&fakeRunner{})
	svc.SetWorkflowNodeRunner(wf)

	result, err := svc.StartWorkflowNode(ctx, store, run, node, nodeRun)
	if err != nil {
		t.Fatalf("StartWorkflowNode: %v", err)
	}
	if wf.ensureCalls != 1 || wf.runCalls != 1 {
		t.Fatalf("workflow runner calls ensure=%d run=%d, want 1/1", wf.ensureCalls, wf.runCalls)
	}
	got := wf.exec
	if got.NodeRun.ID != nodeRun.ID || got.NodeRun.RunID != run.ID || got.NodeRun.NodeID != node.ID ||
		got.NodeRun.LoopPath != "l/2" || got.NodeRun.Iteration != 1 || got.NodeRun.Attempt != 2 {
		t.Fatalf("runner received NodeRun %#v, want exact identity %#v", got.NodeRun, nodeRun)
	}
	if got.NodeRun.SessionKey != "wf-session-1" || got.Prompt != "实现登录页" || got.SessionPolicy != SessionReuseRun {
		t.Fatalf("runner exec = %#v", got)
	}
	if got.Task.ID != "wf-runner-task" || got.RootID != root.ID {
		t.Fatalf("runner task scope = %q root=%q", got.Task.ID, got.RootID)
	}
	if result.Outcome != WorkflowNodeDone || result.Output["summary"] != "ok" || result.SessionKey != "wf-session-1" {
		t.Fatalf("result = %#v", result)
	}
}

func TestWorkflowNodeSessionPolicyDefaultsToRun(t *testing.T) {
	cases := []struct {
		param string
		want  string
	}{
		// 2026-11-20 用户决策：一轮 Run 一条会话，缺省即 run；旧的 per_node 仍是
		// 显式可选项。
		{"", SessionReuseRun},
		{"run", SessionReuseRun},
		{"per_node", SessionReusePerNode},
		{"task_main", SessionReuseTaskMain},
		{"same_stage", SessionReuseSameStage},
		{"always_new", SessionReuseAlwaysNew},
		{"something_else", SessionReuseRun},
	}
	for _, tc := range cases {
		node := WorkflowNode{ID: "n", Kind: NodeKindAgent, Params: map[string]any{"session_reuse_policy": tc.param}}
		if got := WorkflowNodeSessionPolicy(node); got != tc.want {
			t.Fatalf("policy(%q) = %q, want %q", tc.param, got, tc.want)
		}
	}
}

func TestWorkflowAgentNodeRequiresPrompt(t *testing.T) {
	svc := NewService(nil)
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
	if _, err := ParseWorkflowNodeEnvelope(`{"outcome":"done","extra":1}`); err == nil {
		t.Fatal("unknown field must fail")
	}
	if _, err := ParseWorkflowNodeEnvelope(""); err == nil {
		t.Fatal("empty output must fail")
	}
}

// 真实 Agent 的回复很少是「纯净的一个 JSON」：会先解释两句、套 markdown 代码块，
// 甚至因为本机 Stop hook 追加一段而出现多个 JSON 对象（2026-11-14 隔离实例实测）。
// 解析规则因此放宽为「扫描出所有 JSON 对象，取最后一个能通过严格校验的 envelope」，
// 字段与 outcome 的严格性不变。
func TestParseWorkflowNodeEnvelopeToleratesSurroundingText(t *testing.T) {
	prose, err := ParseWorkflowNodeEnvelope("已经改好了。\n" + `{"outcome":"done","output":{"files":["a.ts"]}}` + "\n收工。")
	if err != nil {
		t.Fatalf("prose around envelope: %v", err)
	}
	if prose.Outcome != WorkflowNodeDone {
		t.Fatalf("prose result = %#v", prose)
	}

	fenced, err := ParseWorkflowNodeEnvelope("说明：\n```json\n" + `{"outcome":"done","output":{"files":["b.ts"]}}` + "\n```")
	if err != nil {
		t.Fatalf("fenced envelope: %v", err)
	}
	if fenced.Outcome != WorkflowNodeDone {
		t.Fatalf("fenced result = %#v", fenced)
	}

	multi, err := ParseWorkflowNodeEnvelope(
		`{"outcome":"done","output":{"step":1}}` + "\n\n" +
			`{"outcome":"done","output":{"step":2}}`)
	if err != nil {
		t.Fatalf("concatenated envelopes: %v", err)
	}
	if multi.Output["step"] != float64(2) {
		t.Fatalf("concatenated result = %#v, want the last valid envelope", multi)
	}

	// 最后一个 JSON 对象不是 envelope（例如 hook 的附加反馈）时，退回前一个合法的。
	fallback, err := ParseWorkflowNodeEnvelope(`{"outcome":"done","output":{"n":1}}{"note":"hook feedback"}`)
	if err != nil {
		t.Fatalf("invalid trailing object: %v", err)
	}
	if fallback.Output["n"] != float64(1) {
		t.Fatalf("fallback result = %#v", fallback)
	}

	// 字符串里的花括号不能把对象提前截断。
	braces, err := ParseWorkflowNodeEnvelope(`{"outcome":"done","output":{"note":"a } b { c"}}`)
	if err != nil {
		t.Fatalf("braces inside string: %v", err)
	}
	if braces.Output["note"] != "a } b { c" {
		t.Fatalf("braces result = %#v", braces)
	}

	// 没有任何 JSON 对象仍然是错，且错误要说清楚没有找到 envelope。
	if _, err := ParseWorkflowNodeEnvelope("只有一段大白话"); err == nil {
		t.Fatal("text without any JSON object must fail")
	}
	// 有 JSON 对象但都不合法也仍然是错：宽松只针对包裹文本，不放宽 schema。
	if _, err := ParseWorkflowNodeEnvelope(`{"note":"x"}{"outcome":"maybe"}`); err == nil {
		t.Fatal("invalid envelope candidates must fail")
	}
}
