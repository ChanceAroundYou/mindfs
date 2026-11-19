package kanban

import (
	"context"
	"encoding/json"
	"testing"
)

// 审批卡必须自己说清「在批什么」（2026-11-19 用户裁定）：节点名 + 审批提示 +
// 上游节点产出的可读摘要。没有上游产出的卡片等于让用户盲批。
func TestHumanGateActivityCarriesNodeNameAndUpstreamSummary(t *testing.T) {
	ctx := context.Background()
	graph := WorkflowDefinition{
		Nodes: []WorkflowNode{
			{ID: "build", Name: "代码实现", Kind: NodeKindNotify, Params: map[string]any{"message": "已实现登录页"}},
			{ID: "verify", Name: "验收", Kind: NodeKindHumanGate, Params: map[string]any{"prompt": "确认可以上线"}},
			{ID: "ship", Kind: NodeKindNotify, Params: map[string]any{"message": "已上线"}},
		},
		Edges: []WorkflowEdge{{From: "build", To: "verify"}, {From: "verify", To: "ship"}},
	}
	store, svc, run, _ := newWorkflowCommentFixture(t, "wf-gate-context-task", "wf-gate-context-run", graph)
	if err := svc.AdvanceWorkflowRun(ctx, store, run.TaskID); err != nil {
		t.Fatalf("AdvanceWorkflowRun: %v", err)
	}
	gate, err := store.WorkflowNodeRunByKey(ctx, run.ID, "verify", "", 0, 1)
	if err != nil {
		t.Fatalf("gate node run: %v", err)
	}
	card, err := NewActivityStore(store.db).Get(ctx, "wf:"+gate.ID+":human_waiting")
	if err != nil {
		t.Fatalf("gate activity: %v", err)
	}
	var payload struct {
		Prompt   string `json:"prompt"`
		NodeName string `json:"node_name"`
		Upstream []struct {
			NodeID  string `json:"node_id"`
			Name    string `json:"name"`
			Summary string `json:"summary"`
		} `json:"upstream"`
	}
	if err := json.Unmarshal([]byte(card.PayloadJSON), &payload); err != nil {
		t.Fatalf("gate payload is not JSON: %v (%s)", err, card.PayloadJSON)
	}
	if payload.NodeName != "验收" {
		t.Fatalf("node_name = %q, want the gate node name", payload.NodeName)
	}
	if len(payload.Upstream) != 1 {
		t.Fatalf("upstream = %#v, want exactly the one predecessor", payload.Upstream)
	}
	up := payload.Upstream[0]
	if up.NodeID != "build" || up.Name != "代码实现" || up.Summary != "已实现登录页" {
		t.Fatalf("upstream entry = %#v, want build/代码实现/已实现登录页", up)
	}
}

// 没有名字的旧图不能产出空标题；节点 id 是唯一永远存在的标识。
func TestHumanGateActivityFallsBackToNodeID(t *testing.T) {
	ctx := context.Background()
	graph := WorkflowDefinition{
		Nodes: []WorkflowNode{
			{ID: "verify", Kind: NodeKindHumanGate, Params: map[string]any{"prompt": "确认"}},
			{ID: "after", Kind: NodeKindNotify, Params: map[string]any{"message": "done"}},
		},
		Edges: []WorkflowEdge{{From: "verify", To: "after"}},
	}
	store, svc, run, _ := newWorkflowCommentFixture(t, "wf-gate-noname-task", "wf-gate-noname-run", graph)
	if err := svc.AdvanceWorkflowRun(ctx, store, run.TaskID); err != nil {
		t.Fatalf("AdvanceWorkflowRun: %v", err)
	}
	gate, err := store.WorkflowNodeRunByKey(ctx, run.ID, "verify", "", 0, 1)
	if err != nil {
		t.Fatalf("gate node run: %v", err)
	}
	card, err := NewActivityStore(store.db).Get(ctx, "wf:"+gate.ID+":human_waiting")
	if err != nil {
		t.Fatalf("gate activity: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(card.PayloadJSON), &payload); err != nil {
		t.Fatalf("gate payload is not JSON: %v", err)
	}
	if payload["node_name"] != "verify" {
		t.Fatalf("node_name = %v, want the node id fallback", payload["node_name"])
	}
	if _, ok := payload["upstream"]; ok {
		t.Fatalf("upstream = %#v, want it omitted when there is no predecessor", payload["upstream"])
	}
}
