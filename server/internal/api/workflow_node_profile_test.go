package api

import (
	"testing"

	"mindfs/server/internal/kanban"
)

// W3 T4：exec.Profile 是 prepare 解析好的三层结果（节点 params.model > workflow
// defaults.model > harness 全局默认）。适配层必须读它，否则层级只在 kanban 单元
// 层面成立、真正跑起来仍然只看节点 params。
func TestWorkflowNodeProfilePrefersPreparedProfile(t *testing.T) {
	exec := kanban.WorkflowNodeExecution{
		Node:    kanban.WorkflowNode{ID: "build", Kind: kanban.NodeKindAgent, Params: map[string]any{"model": "opus"}},
		Profile: kanban.WorkflowNodeAgentProfile{Model: "sonnet"},
	}
	if got := workflowNodeProfile(exec).Model; got != "sonnet" {
		t.Fatalf("model = %q, want the prepared profile (workflow default)", got)
	}
}

// 直接构造 exec 的调用方（老路径与测试）没经过 prepare，Profile 为零值：
// 退回只看节点 params，保持既有行为。
func TestWorkflowNodeProfileFallsBackToNodeParamsWhenNotPrepared(t *testing.T) {
	exec := kanban.WorkflowNodeExecution{
		Node: kanban.WorkflowNode{ID: "build", Kind: kanban.NodeKindAgent, Params: map[string]any{"model": "opus"}},
	}
	if got := workflowNodeProfile(exec).Model; got != "opus" {
		t.Fatalf("model = %q, want the node param", got)
	}
}
