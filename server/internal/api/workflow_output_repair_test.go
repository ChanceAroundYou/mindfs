package api

import (
	"context"
	"strings"
	"testing"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/kanban"
)

// repairExec builds the execution a repair-round test needs: an agent node whose
// params decide whether a "done" claim has to be backed by observed writes.
func repairExec(writes bool) kanban.WorkflowNodeExecution {
	node := kanban.WorkflowNode{
		ID: "build", Name: "实现", Kind: kanban.NodeKindAgent,
		Params: map[string]any{"writes_workspace": writes, "prompt": "把登录页实现出来"},
	}
	return workflowAdapterExec("root-repair", kanban.Task{ID: "task-repair"}, node,
		kanban.NodeRun{ID: "nr-repair", RunID: "run-repair", NodeID: "build", Attempt: 1, SessionKey: "sess-repair"})
}

// 转换器偶尔不按约定回 JSON（回一段自然语言）。这时应先要一次格式修复，
// 而不是直接把原文当结论兜底。
func TestRepairEnvelopeRecoversMalformedReply(t *testing.T) {
	app, _ := newWorkflowAdapterTestApp(t)
	llm := &fakeWorkflowOutputLLM{responses: []string{
		"我把 a.go、b.go、c.go 都改好了，测试也过了。",
		`{"outcome":"done","output":{"summary":"改了 a.go、b.go、c.go，go test 通过"},"reason":""}`,
	}}
	app.SetWorkflowOutputLLM(llm)

	result := app.normalizeWorkflowNodeOutput(context.Background(), repairExec(true), "三个文件都改完了，测试通过", 3)

	if result.Outcome != kanban.WorkflowNodeDone {
		t.Fatalf("outcome = %q, want done", result.Outcome)
	}
	if got, _ := result.Output["summary"].(string); got != "改了 a.go、b.go、c.go，go test 通过" {
		t.Fatalf("summary = %q, want the repaired envelope's summary", got)
	}
	if len(llm.prompts) != 2 {
		t.Fatalf("normalizer calls = %d, want 1 normalize + 1 repair", len(llm.prompts))
	}
	if !strings.Contains(llm.prompts[1], "不是约定的格式") {
		t.Fatalf("repair prompt lost the format complaint:\n%s", llm.prompts[1])
	}
	if !strings.Contains(llm.prompts[1], "三个文件都改完了") {
		t.Fatalf("repair prompt lost the original reply:\n%s", llm.prompts[1])
	}
}

// 修复轮有预算：连着两次还读不懂就停手，回到原文摘要兜底，绝不无限重问。
func TestRepairEnvelopeGivesUpAfterTwo(t *testing.T) {
	app, _ := newWorkflowAdapterTestApp(t)
	llm := &fakeWorkflowOutputLLM{responses: []string{
		"不是 JSON 一", "不是 JSON 二", "不是 JSON 三",
		`{"outcome":"done","output":{"summary":"第四个响应不该被用到"}}`,
	}}
	app.SetWorkflowOutputLLM(llm)

	raw := "我把登录页写完了，测试全绿。"
	result := app.normalizeWorkflowNodeOutput(context.Background(), repairExec(true), raw, 4)

	if len(llm.prompts) != 3 {
		t.Fatalf("normalizer calls = %d, want 1 normalize + 2 repairs", len(llm.prompts))
	}
	if result.Outcome != kanban.WorkflowNodeDone {
		t.Fatalf("outcome = %q, want fallback done", result.Outcome)
	}
	if got, _ := result.Output["summary"].(string); got != raw {
		t.Fatalf("summary = %q, want the raw reply %q", got, raw)
	}
	// 兜底结论不是证据：完成契约必须能认出这是「没核对过」的 done。
	if unverified, _ := result.Output["unverified"].(bool); !unverified {
		t.Fatalf("output = %+v, want unverified=true：兜底出来的 done 不能被当成做完的证据", result.Output)
	}
}

// 修复轮可能「补一个漂亮的信封」而不是补工作：声称 done、节点允许写、而本轮
// 一次写操作都没有时，不接受 done。
func TestRepairEnvelopeRejectsDoneWithoutWrites(t *testing.T) {
	app, _ := newWorkflowAdapterTestApp(t)
	app.SetWorkflowOutputLLM(&fakeWorkflowOutputLLM{responses: []string{
		"改完了。",
		`{"outcome":"done","output":{"summary":"全部改完"}}`,
	}})

	result := app.normalizeWorkflowNodeOutput(context.Background(), repairExec(true), "改完了。", 0)
	if result.Outcome != kanban.WorkflowNodeBlocked {
		t.Fatalf("outcome = %q, want blocked for a done claim without writes", result.Outcome)
	}
	if !strings.Contains(result.Reason, "没有观察到写操作") {
		t.Fatalf("reason = %q, want it to say no write was observed", result.Reason)
	}

	// 同样的回复，只要本轮确实写过，就照常接受。
	app2, _ := newWorkflowAdapterTestApp(t)
	app2.SetWorkflowOutputLLM(&fakeWorkflowOutputLLM{responses: []string{
		"改完了。",
		`{"outcome":"done","output":{"summary":"全部改完"}}`,
	}})
	accepted := app2.normalizeWorkflowNodeOutput(context.Background(), repairExec(true), "改完了。", 1)
	if accepted.Outcome != kanban.WorkflowNodeDone {
		t.Fatalf("outcome = %q, want done when writes were observed", accepted.Outcome)
	}
}

func TestEnvelopeConsistentWithFacts(t *testing.T) {
	writable := kanban.WorkflowNode{ID: "build", Kind: kanban.NodeKindAgent, Params: map[string]any{"writes_workspace": true}}
	readOnly := kanban.WorkflowNode{ID: "verify", Kind: kanban.NodeKindAgent, Params: map[string]any{"writes_workspace": false}}
	cases := []struct {
		name       string
		outcome    kanban.WorkflowNodeOutcome
		node       kanban.WorkflowNode
		toolWrites int
		want       bool
	}{
		{"done without writes on a writable node", kanban.WorkflowNodeDone, writable, 0, false},
		{"done with writes on a writable node", kanban.WorkflowNodeDone, writable, 1, true},
		{"done without writes on a read-only node", kanban.WorkflowNodeDone, readOnly, 0, true},
		{"blocked without writes on a writable node", kanban.WorkflowNodeBlocked, writable, 0, true},
		{"silent without writes on a writable node", kanban.WorkflowNodeSilent, writable, 0, true},
	}
	for _, tc := range cases {
		res := kanban.WorkflowNodeResult{Outcome: tc.outcome, Output: map[string]any{"summary": "x"}}
		if got := envelopeConsistentWithFacts(res, tc.node, tc.toolWrites); got != tc.want {
			t.Fatalf("%s: consistent = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// 写操作证据只认「已完成」的写类工具，且同一个调用只算一次：tool_call 与
// tool_update 会把同一调用报两遍。
func TestWorkflowWriteCounter(t *testing.T) {
	var c workflowWriteCounter
	c.observe(agenttypes.ToolCall{CallID: "t1", Kind: agenttypes.ToolKindEdit, Status: "running"})
	if c.writes() != 0 {
		t.Fatalf("running write counted: %d", c.writes())
	}
	c.observe(agenttypes.ToolCall{CallID: "t1", Kind: agenttypes.ToolKindEdit, Status: "complete"})
	c.observe(agenttypes.ToolCall{CallID: "t1", Kind: agenttypes.ToolKindEdit, Status: "complete"})
	c.observe(agenttypes.ToolCall{CallID: "t2", Kind: agenttypes.ToolKindRead, Status: "complete"})
	c.observe(agenttypes.ToolCall{CallID: "t3", Kind: agenttypes.ToolKindMove, Status: "complete"})
	if got := c.writes(); got != 2 {
		t.Fatalf("writes = %d, want 2 (t1 deduped, t2 read ignored, t3 move counted)", got)
	}
}
