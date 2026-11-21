package kanban

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// W3 T1：Router 自设计 workflow 的「临时使用」形态。
//
// 用户裁定：项目级、临时使用的 workflow 可以自由生成（免批），固化或提升到全局
// 才需要显式批准。临时使用的落点就是**直接在这一次决策里给出图**，起一个
// ad-hoc Run —— 不进控制面目录、不需要任何人批准，Run 自己快照整张图。
//
// 图是模型写的，所以三条边界必须先挡住：来源唯一（不能同时给 id 和图）、
// 规模有上限、结构要过与编辑器同一套校验。

func inlineGraphArgs() map[string]any {
	return map[string]any{
		"reason": "两段式：先定位再修",
		"graph": map[string]any{
			"nodes": []any{
				map[string]any{"id": "locate", "kind": NodeKindAgent, "params": map[string]any{"prompt": "定位问题"}},
				map[string]any{"id": "fix", "kind": NodeKindAgent, "params": map[string]any{"prompt": "修复并验证"}},
			},
			"edges": []any{map[string]any{"from": "locate", "to": "fix"}},
		},
	}
}

func latestDecision(t *testing.T, f *routeFixture) *RouterDecision {
	t.Helper()
	var decision *RouterDecision
	waitForCondition(t, func() bool {
		decisions, err := NewRouterStore(f.store.db).ListDecisions(context.Background(), "task-1", 10, time.Time{})
		if err != nil || len(decisions) == 0 {
			return false
		}
		decision = decisions[0]
		return decision.Status != RouterDecisionApplying
	})
	return decision
}

func TestRouterRouteRunsInlineGraph(t *testing.T) {
	f := newRouteFixture(t, RouterModeRoute)
	f.agent.calls = []scriptedRouterCall{{tool: "router_run_workflow", args: inlineGraphArgs()}}
	ctx := context.Background()
	versionsBefore, err := f.workflows.ListVersions(ctx)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}

	_, placeholder, err := f.activity.CreateUserActivity(ctx, "task-1", "先定位再修", CommentWorkflowChoiceEmpty, nil)
	if err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	decision := f.waitDecision(t, RouterDecisionApplied)
	if len(decision.Actions) != 1 || decision.Actions[0].Kind != RouterActionRunWorkflow {
		t.Fatalf("actions = %#v", decision.Actions)
	}
	if decision.Actions[0].Graph == nil {
		t.Fatalf("inline graph was dropped from the applied action: %#v", decision.Actions[0])
	}
	runs := f.runs(t)
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
	if got := len(runs[0].Graph.Nodes); got != 2 {
		t.Fatalf("run graph nodes = %d, want the two the router wrote", got)
	}
	// Router 起草时只写 prompt 是常态。params.agent 必须在这一步就补成具体
	// harness：图能起 Run、节点却在执行时以 "agent required" 立刻失败，验收
	// 实测过两次（起草的图固化以后同样跑不起来）。
	for _, n := range runs[0].Graph.Nodes {
		if got, _ := n.Params["agent"].(string); strings.TrimSpace(got) != defaultRouterAgent {
			t.Fatalf("node %q agent = %q, want %q", n.ID, got, defaultRouterAgent)
		}
	}
	if !strings.HasPrefix(runs[0].Version, "adhoc:") {
		t.Fatalf("run version = %q, want an adhoc run", runs[0].Version)
	}
	// 作用域必须落在任务所在的项目上：图里的 root_id 为空等于「全局」，那正是
	// 需要显式批准的那一档，不能让模型一句话就跨过去。
	if runs[0].Graph.RootID != f.rootID {
		t.Fatalf("run graph root = %q, want the task's root %q", runs[0].Graph.RootID, f.rootID)
	}
	// 临时 workflow 不进控制面目录：它只活在这一轮 Run 里。
	versionsAfter, err := f.workflows.ListVersions(ctx)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versionsAfter) != len(versionsBefore) {
		t.Fatalf("versions %d -> %d, want the adhoc graph kept out of the catalog", len(versionsBefore), len(versionsAfter))
	}
	if err := f.svc.WaitWorkflowRunIdle(ctx, f.rootID, "task-1"); err != nil {
		t.Fatalf("WaitWorkflowRunIdle: %v", err)
	}
	act := f.reloadActivity(t, placeholder.ID)
	if act.RunID != runs[0].ID {
		t.Fatalf("placeholder run = %q, want %q", act.RunID, runs[0].ID)
	}
}

// 来源必须唯一：既给 workflow_id 又给 graph 会让人分不清到底跑了哪一张图。
func TestRouterInlineGraphRejectsMixedSources(t *testing.T) {
	f := newRouteFixture(t, RouterModeRoute)
	args := inlineGraphArgs()
	args["workflow_id"] = "workflow-feature"
	f.agent.calls = []scriptedRouterCall{{tool: "router_run_workflow", args: args}}
	ctx := context.Background()

	if _, _, err := f.activity.CreateUserActivity(ctx, "task-1", "跑一个流程", CommentWorkflowChoiceEmpty, nil); err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	decision := latestDecision(t, f)
	if decision == nil || decision.Status == RouterDecisionApplied {
		t.Fatalf("decision = %#v, want the mixed sources refused", decision)
	}
	if runs := f.runs(t); len(runs) != 0 {
		t.Fatalf("runs = %d, want none", len(runs))
	}
}

// 图是模型写的：规模、结构都由 Router 侧先挡，不能让它把半张图塞进 Run。
// 边界用字面量钉住 —— 12 个节点是产品上限（提示词里也这么写），13 个必须拒。
func TestRouterInlineGraphRejectsOversizedAndInvalidGraphs(t *testing.T) {
	f := newRouteFixture(t, RouterModeRoute)
	ctx := context.Background()
	env := &routerToolEnv{store: f.store, brief: briefFor(t, f.store, "task-1"), collector: newRouterTurnCollector(RouterConfig{}.Normalized())}
	tool := toolByName(t, env, "router_run_workflow")

	chain := func(n int) map[string]any {
		nodes := make([]any, 0, n)
		edges := make([]any, 0, n)
		for i := 0; i < n; i++ {
			nodes = append(nodes, map[string]any{"id": nodeIDForInlineTest(i), "kind": NodeKindAgent, "params": map[string]any{"prompt": "x"}})
			if i > 0 {
				edges = append(edges, map[string]any{"from": nodeIDForInlineTest(i - 1), "to": nodeIDForInlineTest(i)})
			}
		}
		return map[string]any{"nodes": nodes, "edges": edges}
	}
	call := func(graph map[string]any) error {
		raw, err := json.Marshal(map[string]any{"reason": "x", "graph": graph})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		_, err = tool.Handler(ctx, raw)
		return err
	}

	if err := call(chain(12)); err != nil {
		t.Fatalf("12-node graph rejected: %v", err)
	}
	if err := call(chain(13)); err == nil {
		t.Fatal("13-node graph accepted")
	}
	// 结构非法的图（引用了不存在的节点）同样在工具层拒绝。
	invalid := chain(2)
	invalid["edges"] = append(invalid["edges"].([]any), map[string]any{"from": "n1", "to": "ghost"})
	if err := call(invalid); err == nil {
		t.Fatal("dangling edge accepted")
	}

	// 走完整决策链路时，超限的图不能起 Run。
	f.agent.calls = []scriptedRouterCall{{tool: "router_run_workflow", args: map[string]any{"reason": "x", "graph": chain(13)}}}
	if _, _, err := f.activity.CreateUserActivity(ctx, "task-1", "节点太多", CommentWorkflowChoiceEmpty, nil); err != nil {
		t.Fatalf("CreateUserActivity: %v", err)
	}
	decision := latestDecision(t, f)
	if decision == nil || decision.Status == RouterDecisionApplied {
		t.Fatalf("oversized graph accepted: %#v", decision)
	}
	if runs := f.runs(t); len(runs) != 0 {
		t.Fatalf("runs = %d, want none", len(runs))
	}
}

func nodeIDForInlineTest(i int) string {
	return fmt.Sprintf("n%d", i)
}

// 补默认 agent 的三条边界：显式写的 harness 不动、循环内层节点也补、
// 不改调用方传进来的原图（sanitize 的返回值才是起 Run 用的那份）。
func TestSanitizeRouterGraphFillsMissingNodeAgent(t *testing.T) {
	graph := WorkflowDefinition{
		Nodes: []WorkflowNode{
			{ID: "a", Kind: NodeKindAgent, Params: map[string]any{"prompt": "x"}},
			{ID: "b", Kind: NodeKindAgent, Params: map[string]any{"prompt": "y", "agent": "codex"}},
			{ID: "loop", Kind: NodeKindLoop, Params: map[string]any{
				"max_iterations":  float64(2),
				"exit_expression": "n1.output.done == true",
				"graph": map[string]any{
					"nodes": []any{
						map[string]any{"id": "n1", "kind": NodeKindAgent, "params": map[string]any{"prompt": "inner"}},
					},
					"edges": []any{},
				},
			}},
		},
	}
	out, err := sanitizeRouterGraph(&graph)
	if err != nil {
		t.Fatalf("sanitizeRouterGraph: %v", err)
	}
	if got, _ := out.Nodes[0].Params["agent"].(string); got != defaultRouterAgent {
		t.Fatalf("missing agent filled with %q, want %q", got, defaultRouterAgent)
	}
	if got, _ := out.Nodes[1].Params["agent"].(string); got != "codex" {
		t.Fatalf("explicit agent was overwritten: %q", got)
	}
	innerGraph, _ := out.Nodes[2].Params["graph"].(map[string]any)
	innerNodes, _ := innerGraph["nodes"].([]any)
	if len(innerNodes) != 1 {
		t.Fatalf("loop inner nodes = %d, want 1", len(innerNodes))
	}
	innerParams, _ := innerNodes[0].(map[string]any)["params"].(map[string]any)
	if got, _ := innerParams["agent"].(string); got != defaultRouterAgent {
		t.Fatalf("loop inner node agent = %q, want %q", got, defaultRouterAgent)
	}
	if _, ok := graph.Nodes[0].Params["agent"]; ok {
		t.Fatal("sanitizeRouterGraph mutated the caller's graph")
	}
}
