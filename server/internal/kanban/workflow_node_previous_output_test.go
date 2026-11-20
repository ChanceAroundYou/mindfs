package kanban

import (
	"context"
	"strings"
	"testing"
)

// 用户 2026-11-20 的原话：「方案设计/执行里面，也没有接受上一轮结果的地方，
// 岂不是闷头空做？」——一轮 Run 默认共用一条会话，上游产出只是「隐式还在对话
// 里」；节点一旦另起会话或单独重试，下游就彻底看不到了。{previous_output} 把
// 这件事变成显式数据契约。

func previousOutputTestGraph(prompt string) (WorkflowDefinition, WorkflowNode) {
	plan := WorkflowNode{ID: "plan", Name: "设计", Kind: NodeKindAgent, Params: map[string]any{"prompt": "设计方案"}}
	build := WorkflowNode{ID: "build", Name: "实现", Kind: NodeKindAgent, Params: map[string]any{"prompt": prompt}}
	graph := WorkflowDefinition{
		Nodes: []WorkflowNode{plan, build},
		Edges: []WorkflowEdge{{From: "plan", To: "build"}},
	}
	return graph, build
}

func TestWorkflowNodePromptResolvesPreviousOutputFromUpstreamProjection(t *testing.T) {
	ctx := context.Background()
	store, svc, run, _ := newWorkflowAttemptFixture(t, "wf-po-task", "wf-po-run")
	graph, build := previousOutputTestGraph("按照这个方案实现：\n{previous_output}")
	run.Graph = graph
	run.NodeOutputs = map[string]map[string]any{"plan": {"summary": "方案 A：先建表再写接口"}}
	svc.SetWorkflowNodeRunner(&scriptedWorkflowNodeRunner{})

	exec, err := svc.PrepareWorkflowNode(ctx, store, run, build, attemptNodeRun(run.ID))
	if err != nil {
		t.Fatalf("PrepareWorkflowNode: %v", err)
	}
	want := "按照这个方案实现：\n方案 A：先建表再写接口"
	if exec.Prompt != want {
		t.Fatalf("prompt = %q, want %q", exec.Prompt, want)
	}
	if strings.Contains(exec.Prompt, "{previous_output}") {
		t.Fatal("literal placeholder leaked into the agent prompt")
	}
}

func TestWorkflowNodePromptFailsClosedWithoutUpstreamOutput(t *testing.T) {
	ctx := context.Background()
	store, svc, run, _ := newWorkflowAttemptFixture(t, "wf-po-task-2", "wf-po-run-2")
	graph, build := previousOutputTestGraph("{previous_output}")
	run.Graph = graph
	runner := &scriptedWorkflowNodeRunner{}
	svc.SetWorkflowNodeRunner(runner)

	_, err := svc.PrepareWorkflowNode(ctx, store, run, build, attemptNodeRun(run.ID))
	if err == nil {
		t.Fatal("want an error instead of sending the raw placeholder")
	}
	if !strings.Contains(err.Error(), "previous_output") || !strings.Contains(err.Error(), "plan") {
		t.Fatalf("err = %v, want it to name the placeholder and the upstream node", err)
	}
	if runner.runCalls != 0 {
		t.Fatalf("runner must not run, ran %d times", runner.runCalls)
	}
}

func TestWorkflowNodePromptFailsClosedWithoutUpstreamEdge(t *testing.T) {
	ctx := context.Background()
	store, svc, run, _ := newWorkflowAttemptFixture(t, "wf-po-task-3", "wf-po-run-3")
	// fixture 的图只有一个节点：起始节点用 {previous_output} 是配置错误。
	node := run.Graph.Nodes[0]
	node.Params = map[string]any{"prompt": "{previous_output}"}
	svc.SetWorkflowNodeRunner(&scriptedWorkflowNodeRunner{})

	_, err := svc.PrepareWorkflowNode(ctx, store, run, node, attemptNodeRun(run.ID))
	if err == nil || !strings.Contains(err.Error(), "没有上游") {
		t.Fatalf("err = %v, want a no-upstream explanation", err)
	}
}

func TestWorkflowNodePromptJoinsMultipleUpstreamOutputsByName(t *testing.T) {
	ctx := context.Background()
	store, svc, run, _ := newWorkflowAttemptFixture(t, "wf-po-task-4", "wf-po-run-4")
	graph, build := previousOutputTestGraph("{previous_output}")
	graph.Nodes = append(graph.Nodes, WorkflowNode{ID: "research", Name: "调研", Kind: NodeKindAgent})
	graph.Edges = append(graph.Edges, WorkflowEdge{From: "research", To: "build"})
	run.Graph = graph
	run.NodeOutputs = map[string]map[string]any{
		"plan":     {"summary": "方案 A"},
		"research": {"summary": "竞品都这么做"},
	}
	svc.SetWorkflowNodeRunner(&scriptedWorkflowNodeRunner{})

	exec, err := svc.PrepareWorkflowNode(ctx, store, run, build, attemptNodeRun(run.ID))
	if err != nil {
		t.Fatalf("PrepareWorkflowNode: %v", err)
	}
	for _, want := range []string{"「设计」的产出：\n方案 A", "「调研」的产出：\n竞品都这么做"} {
		if !strings.Contains(exec.Prompt, want) {
			t.Fatalf("prompt = %q, want it to contain %q", exec.Prompt, want)
		}
	}
}

// 投影落后于账本时（崩溃恢复）仍要能读到上游产出。
func TestWorkflowNodePromptFallsBackToNodeRunRows(t *testing.T) {
	ctx := context.Background()
	store, svc, run, _ := newWorkflowAttemptFixture(t, "wf-po-task-5", "wf-po-run-5")
	graph, build := previousOutputTestGraph("{previous_output}")
	run.Graph = graph
	if err := store.UpsertWorkflowNodeRun(ctx, NodeRun{
		ID: "nr-plan-1", RunID: run.ID, NodeID: "plan", Iteration: 1, Attempt: 1,
		Status: NodeRunSuccess, Output: map[string]any{"summary": "行内产出"},
	}); err != nil {
		t.Fatalf("UpsertWorkflowNodeRun: %v", err)
	}
	svc.SetWorkflowNodeRunner(&scriptedWorkflowNodeRunner{})

	exec, err := svc.PrepareWorkflowNode(ctx, store, run, build, attemptNodeRun(run.ID))
	if err != nil {
		t.Fatalf("PrepareWorkflowNode: %v", err)
	}
	if exec.Prompt != "行内产出" {
		t.Fatalf("prompt = %q, want the NodeRun fallback output", exec.Prompt)
	}
}

// Loop 内部节点的上游产出按「当前迭代」取，不能串到别的迭代。
func TestWorkflowNodePromptResolvesPreviousOutputInsideLoop(t *testing.T) {
	ctx := context.Background()
	store, svc, run, _ := newWorkflowAttemptFixture(t, "wf-po-task-6", "wf-po-run-6")
	inner := WorkflowDefinition{
		Nodes: []WorkflowNode{
			{ID: "draft", Name: "草稿", Kind: NodeKindAgent, Params: map[string]any{"prompt": "写草稿"}},
			{ID: "review", Name: "评审", Kind: NodeKindAgent, Params: map[string]any{"prompt": "评审这份草稿：{previous_output}"}},
		},
		Edges: []WorkflowEdge{{From: "draft", To: "review"}},
	}
	loop := WorkflowNode{ID: "loop", Kind: NodeKindLoop, Params: map[string]any{
		"max_iterations":  3,
		"exit_expression": "true",
		"graph":           inner,
	}}
	run.Graph = WorkflowDefinition{Nodes: []WorkflowNode{loop}, Edges: nil}
	for iteration, summary := range map[int]string{1: "第一版草稿", 2: "第二版草稿"} {
		if err := store.UpsertWorkflowNodeRun(ctx, NodeRun{
			ID: "nr-draft-" + summary, RunID: run.ID, NodeID: "draft", LoopPath: "loop",
			Iteration: iteration, Attempt: 1, Status: NodeRunSuccess,
			Output: map[string]any{"summary": summary},
		}); err != nil {
			t.Fatalf("UpsertWorkflowNodeRun: %v", err)
		}
	}
	svc.SetWorkflowNodeRunner(&scriptedWorkflowNodeRunner{})

	nodeRun := NodeRun{ID: "nr-review-1", RunID: run.ID, NodeID: "review", LoopPath: "loop", Iteration: 2, Attempt: 1, Status: NodeRunQueued}
	exec, err := svc.PrepareWorkflowNode(ctx, store, run, inner.Nodes[1], nodeRun)
	if err != nil {
		t.Fatalf("PrepareWorkflowNode: %v", err)
	}
	if exec.Prompt != "评审这份草稿：第二版草稿" {
		t.Fatalf("prompt = %q, want the current iteration's draft", exec.Prompt)
	}
}

// 老工作流没写这个占位符时不许因为「上游没产出」而失败，也不许多查一次库。
func TestWorkflowNodePromptIgnoresPreviousOutputWhenUnused(t *testing.T) {
	ctx := context.Background()
	store, svc, run, _ := newWorkflowAttemptFixture(t, "wf-po-task-7", "wf-po-run-7")
	graph, build := previousOutputTestGraph("直接实现")
	run.Graph = graph
	svc.SetWorkflowNodeRunner(&scriptedWorkflowNodeRunner{})

	exec, err := svc.PrepareWorkflowNode(ctx, store, run, build, attemptNodeRun(run.ID))
	if err != nil {
		t.Fatalf("PrepareWorkflowNode: %v", err)
	}
	if exec.Prompt != "直接实现" {
		t.Fatalf("prompt = %q, want it untouched", exec.Prompt)
	}
}
