package kanban

import (
	"context"
	"testing"
)

// W3 T4：模型档三层 —— 节点 `params.model` > workflow `defaults.model` > harness 全局
// 默认。中间这一档就是「按任务选模型」：任务选哪个 workflow，就继承它的默认模型；
// 单个节点想换再自己覆盖。用户裁定「按任务选模型……可以融入到 workflow 里」，所以
// 这里没有单独的 Task.Model 列。
func TestWorkflowAgentProfileNodeModelWinsOverDefaults(t *testing.T) {
	def := WorkflowDefinition{Defaults: &WorkflowDefaults{Model: "sonnet"}}
	node := WorkflowNode{ID: "build", Kind: NodeKindAgent, Params: map[string]any{"model": "opus"}}
	if got := def.AgentProfileFor(node).Model; got != "opus" {
		t.Fatalf("model = %q, want the node param to win", got)
	}
}

func TestWorkflowAgentProfileFallsBackToWorkflowDefault(t *testing.T) {
	def := WorkflowDefinition{Defaults: &WorkflowDefaults{Model: "sonnet"}}
	node := WorkflowNode{ID: "build", Kind: NodeKindAgent, Params: map[string]any{"prompt": "实现"}}
	if got := def.AgentProfileFor(node).Model; got != "sonnet" {
		t.Fatalf("model = %q, want the workflow default", got)
	}
}

// 全局档不是这里的事：两档都没写就保持空，交给 harness 自己的默认模型。
func TestWorkflowAgentProfileEmptyWhenNoTierSetsAModel(t *testing.T) {
	def := WorkflowDefinition{}
	if got := def.AgentProfileFor(WorkflowNode{ID: "build", Kind: NodeKindAgent}).Model; got != "" {
		t.Fatalf("model = %q, want empty so the harness default applies", got)
	}
}

func TestWorkflowAgentProfileTrimsDefaultModel(t *testing.T) {
	def := WorkflowDefinition{Defaults: &WorkflowDefaults{Model: "  sonnet  "}}
	if got := def.AgentProfileFor(WorkflowNode{ID: "build"}).Model; got != "sonnet" {
		t.Fatalf("model = %q, want the trimmed default", got)
	}
}

// 生产路径：PrepareWorkflowNode 把解析结果放进 exec.Profile，适配层直接读它，
// 不再自己重新拼一遍 —— 否则层级只在单元层面正确、真正跑起来还是旧行为。
func TestPrepareWorkflowNodeAppliesWorkflowDefaultModel(t *testing.T) {
	ctx := context.Background()
	store, svc, run, _ := newWorkflowAttemptFixture(t, "wf-model-task", "wf-model-run")
	svc.SetWorkflowNodeRunner(&scriptedWorkflowNodeRunner{})
	run.Graph.Defaults = &WorkflowDefaults{Model: "sonnet"}

	node := WorkflowNode{ID: "build", Kind: NodeKindAgent, Params: map[string]any{"prompt": "实现"}}
	exec, err := svc.PrepareWorkflowNode(ctx, store, run, node, attemptNodeRun(run.ID))
	if err != nil {
		t.Fatalf("PrepareWorkflowNode: %v", err)
	}
	if exec.Profile.Model != "sonnet" {
		t.Fatalf("exec profile model = %q, want the workflow default", exec.Profile.Model)
	}
}

func TestPrepareWorkflowNodeKeepsNodeModelOverWorkflowDefault(t *testing.T) {
	ctx := context.Background()
	store, svc, run, _ := newWorkflowAttemptFixture(t, "wf-model-task-2", "wf-model-run-2")
	svc.SetWorkflowNodeRunner(&scriptedWorkflowNodeRunner{})
	run.Graph.Defaults = &WorkflowDefaults{Model: "sonnet"}

	node := WorkflowNode{ID: "build", Kind: NodeKindAgent, Params: map[string]any{
		"prompt": "实现",
		"model":  "opus",
	}}
	exec, err := svc.PrepareWorkflowNode(ctx, store, run, node, attemptNodeRun(run.ID))
	if err != nil {
		t.Fatalf("PrepareWorkflowNode: %v", err)
	}
	if exec.Profile.Model != "opus" {
		t.Fatalf("exec profile model = %q, want the node param", exec.Profile.Model)
	}
}

// NodeRun 行是运行视图与 Router inspect_run 的事实来源：继承来的默认模型也要
// 落在行上。否则跑的是 sonnet，界面与 Router 看到的却是「这个节点没设模型」。
func TestRunWorkflowNodeAttemptRecordsInheritedDefaultModel(t *testing.T) {
	ctx := context.Background()
	store, svc, run, node := newWorkflowAttemptFixture(t, "wf-model-record-task", "wf-model-record-run")
	svc.SetWorkflowNodeRunner(&scriptedWorkflowNodeRunner{})
	run.Graph.Defaults = &WorkflowDefaults{Model: "sonnet"}

	if _, err := svc.RunWorkflowNodeAttempt(ctx, store, run, node, attemptNodeRun(run.ID)); err != nil {
		t.Fatalf("RunWorkflowNodeAttempt: %v", err)
	}
	final, err := store.WorkflowNodeRunByKey(ctx, run.ID, "build", "", 1, 1)
	if err != nil {
		t.Fatalf("WorkflowNodeRunByKey: %v", err)
	}
	if final.Model != "sonnet" {
		t.Fatalf("node run model = %q, want the inherited workflow default", final.Model)
	}
}

// 节点自己的 params.model 仍然优先于 workflow 默认：落行的是「实际会跑的档」。
func TestRunWorkflowNodeAttemptKeepsNodeModelOnTheRow(t *testing.T) {
	ctx := context.Background()
	store, svc, run, node := newWorkflowAttemptFixture(t, "wf-model-row-task", "wf-model-row-run")
	svc.SetWorkflowNodeRunner(&scriptedWorkflowNodeRunner{})
	run.Graph.Defaults = &WorkflowDefaults{Model: "sonnet"}
	node.Params["model"] = "opus"
	run.Graph.Nodes = []WorkflowNode{node}

	if _, err := svc.RunWorkflowNodeAttempt(ctx, store, run, node, attemptNodeRun(run.ID)); err != nil {
		t.Fatalf("RunWorkflowNodeAttempt: %v", err)
	}
	final, err := store.WorkflowNodeRunByKey(ctx, run.ID, "build", "", 1, 1)
	if err != nil {
		t.Fatalf("WorkflowNodeRunByKey: %v", err)
	}
	if final.Model != "opus" {
		t.Fatalf("node run model = %q, want the node param", final.Model)
	}
}

// Router 起草的图是模型写的：默认模型要规整成可执行的形状 —— 去空白，空串
// 就当没设（否则会在运行期变成一个假的空模型档）。
func TestSanitizeRouterGraphNormalizesDefaultModel(t *testing.T) {
	graph, err := sanitizeRouterGraph(&WorkflowDefinition{
		Nodes:    []WorkflowNode{{ID: "a", Kind: NodeKindAgent, Params: map[string]any{"prompt": "做"}}},
		Defaults: &WorkflowDefaults{Model: "  sonnet  "},
	})
	if err != nil {
		t.Fatalf("sanitizeRouterGraph: %v", err)
	}
	if graph.Defaults == nil || graph.Defaults.Model != "sonnet" {
		t.Fatalf("defaults = %+v, want trimmed model", graph.Defaults)
	}
}

func TestSanitizeRouterGraphDropsEmptyDefaultModel(t *testing.T) {
	graph, err := sanitizeRouterGraph(&WorkflowDefinition{
		Nodes:    []WorkflowNode{{ID: "a", Kind: NodeKindAgent, Params: map[string]any{"prompt": "做"}}},
		Defaults: &WorkflowDefaults{Model: "   "},
	})
	if err != nil {
		t.Fatalf("sanitizeRouterGraph: %v", err)
	}
	if graph.Defaults != nil {
		t.Fatalf("defaults = %+v, want nil for an empty model", graph.Defaults)
	}
}
