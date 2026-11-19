package kanban

import "strings"

// gateActivityPayload 生成审批卡的结构化内容。
//
// 用户 2026-11-19 的原话是「我都不知道要我审批什么」：只给一句 prompt 时，
// 卡片既不说这是哪个节点，也不展示上游节点到底产出了什么。节点名与上游
// 产出摘要都是图的既有事实，没有理由让用户去运行视图里自己拼。
//
// payload 只放可读文本：上游按键排序取全部入边，用 nodeRunOutputSummary
// 提取「给人看的那句」，没有产出的节点（condition/notify 的条件路由）自然
// 为空并被跳过，绝不放 output 原样 JSON。
func gateActivityPayload(run *WorkflowRun, node WorkflowNode, nr NodeRun) (string, error) {
	name := strings.TrimSpace(node.Name)
	if name == "" {
		name = node.ID
	}
	prompt, _ := node.Params["prompt"].(string)
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		prompt = name
	}
	payload := map[string]any{
		"prompt":      prompt,
		"node_id":     node.ID,
		"node_name":   name,
		"node_run_id": nr.ID,
	}
	if upstream := upstreamOutputSummaries(run, node.ID); len(upstream) > 0 {
		payload["upstream"] = upstream
	}
	return marshalWorkflowJSON(payload)
}

// upstreamOutputSummaries 按边顺序列出入边节点的可读产出。直接用 run 上的
// 投影（NodeOutputs）而不是回查 NodeRun 行：投影就是执行引擎给下游看的同一份
// 事实，边遍历顺序稳定，重复数据源只会把两边的口径拉开。
func upstreamOutputSummaries(run *WorkflowRun, nodeID string) []map[string]string {
	if run == nil {
		return nil
	}
	var out []map[string]string
	for _, edge := range run.Graph.Edges {
		if edge.To != nodeID {
			continue
		}
		from, ok := run.Graph.node(edge.From)
		if !ok {
			continue
		}
		output := run.NodeOutputs[edge.From]
		if len(output) == 0 {
			continue
		}
		summary := nodeRunOutputSummary(NodeRun{NodeID: edge.From, Output: output})
		if summary == "" {
			continue
		}
		name := strings.TrimSpace(from.Name)
		if name == "" {
			name = from.ID
		}
		out = append(out, map[string]string{
			"node_id": edge.From,
			"name":    name,
			"summary": summary,
		})
	}
	return out
}
