package kanban

import (
	"context"
	"fmt"
	"strings"
)

// {previous_output}：把上游节点的产出显式带进下游节点的 prompt。
//
// 为什么需要显式注入：一轮 Run 默认共用一条会话（session_reuse_policy=run），
// 上游写了什么只是「隐式地还在对话里」；节点一旦另起会话（always_new/per_node）、
// 换了 harness/model，或者被单独重试，下游就完全看不到上游做了什么，只能闷头
// 重做。占位符把这件事变成一条显式的数据契约。
//
// 取值口径与审批卡一致（upstreamOutputSummaries）：先读 run 投影 NodeOutputs ——
// 那正是执行引擎给下游看的同一份事实；投影里没有时回查 NodeRun 行。Loop 内部
// 节点按 LoopPath + 当前迭代走 workflowLoopIterationRun 的同一套投影。
//
// 解析不出来是硬错误，绝不把字面量发给 agent：与 {previous_input} 同样的理由，
// 模型对着 `{previous_output}` 会自由发挥出一份「上一轮结果」。
const (
	// 单个上游节点的产出上限；超出部分截断并显式标注。
	workflowPreviousOutputPerParentLimit = 12000
	// 多个上游加起来的预算；分支收敛时入边可能很多。
	workflowPreviousOutputTotalLimit = 24000
)

// workflowPreviousOutputPrompt 解析一个节点的 {previous_output}。
func workflowPreviousOutputPrompt(ctx context.Context, store *TaskStore, run *WorkflowRun, node WorkflowNode, nodeRun NodeRun) (string, error) {
	if run == nil {
		return "", fmt.Errorf("workflow agent node %q 的 {previous_output} 无法解析：没有运行记录", node.ID)
	}
	graph, outputs, err := workflowPreviousOutputGraph(ctx, store, run, nodeRun)
	if err != nil {
		return "", fmt.Errorf("workflow agent node %q 的 {previous_output} 无法解析：%w", node.ID, err)
	}
	incoming := make([]WorkflowEdge, 0, 2)
	for _, edge := range graph.Edges {
		if edge.To == node.ID {
			incoming = append(incoming, edge)
		}
	}
	if len(incoming) == 0 {
		return "", fmt.Errorf("workflow agent node %q 的 {previous_output} 无法解析：节点没有上游（起始节点请用 {previous_input}）", node.ID)
	}

	summaries := previousOutputSummaries(graph, outputs, incoming)
	if len(summaries) == 0 {
		// 投影里没有产出时回查 NodeRun 行：崩溃恢复后投影可能落后于账本。
		rows, err := listWorkflowNodeRunsOrNil(ctx, store, run.ID)
		if err != nil {
			return "", err
		}
		summaries = previousOutputSummariesFromRuns(graph, rows, incoming, nodeRun)
	}
	if len(summaries) == 0 {
		names := make([]string, 0, len(incoming))
		for _, edge := range incoming {
			names = append(names, edge.From)
		}
		return "", fmt.Errorf("workflow agent node %q 的 {previous_output} 无法解析：上游节点 %s 还没有产出",
			node.ID, strings.Join(names, "、"))
	}

	total := 0
	for i := range summaries {
		budget := workflowPreviousOutputPerParentLimit
		if rest := workflowPreviousOutputTotalLimit - total; rest < budget {
			budget = rest
		}
		if budget <= 0 {
			summaries = summaries[:i]
			break
		}
		text := truncateRouterText(summaries[i].Summary, budget)
		summaries[i].Summary = text
		total += len(text)
	}
	if len(summaries) == 1 {
		return summaries[0].Summary, nil
	}
	blocks := make([]string, 0, len(summaries))
	for _, item := range summaries {
		blocks = append(blocks, "「"+item.Name+"」的产出：\n"+item.Summary)
	}
	return strings.Join(blocks, "\n\n"), nil
}

type previousOutputSummary struct {
	Name    string
	Summary string
}

// workflowPreviousOutputGraph 返回「这个节点所属的图」以及它的产出投影。
// 顶层节点直接读 run；Loop 内部节点读当前迭代的子运行投影。
func workflowPreviousOutputGraph(ctx context.Context, store *TaskStore, run *WorkflowRun, nodeRun NodeRun) (WorkflowDefinition, map[string]map[string]any, error) {
	loopPath := strings.TrimSpace(nodeRun.LoopPath)
	if loopPath == "" {
		return run.Graph, run.NodeOutputs, nil
	}
	loopNode, ok := run.Graph.node(loopPath)
	if !ok {
		return WorkflowDefinition{}, nil, fmt.Errorf("loop %q 不在运行快照里", loopPath)
	}
	inner, err := workflowLoopGraph(loopNode)
	if err != nil {
		return WorkflowDefinition{}, nil, err
	}
	rows, err := listWorkflowNodeRunsOrNil(ctx, store, run.ID)
	if err != nil {
		return WorkflowDefinition{}, nil, err
	}
	iteration := nodeRun.Iteration
	if iteration <= 0 {
		iteration = 1
	}
	sub := workflowLoopIterationRun(inner, rows, loopPath, iteration)
	return sub.Graph, sub.NodeOutputs, nil
}

func previousOutputSummaries(graph WorkflowDefinition, outputs map[string]map[string]any, incoming []WorkflowEdge) []previousOutputSummary {
	out := make([]previousOutputSummary, 0, len(incoming))
	for _, edge := range incoming {
		summary := nodeRunOutputSummary(NodeRun{NodeID: edge.From, Output: outputs[edge.From]})
		if summary == "" {
			continue
		}
		out = append(out, previousOutputSummary{Name: workflowNodeDisplayName(graph, edge.From), Summary: summary})
	}
	return out
}

// previousOutputSummariesFromRuns 是投影缺失时的兜底：按节点取本次 Run 里
// attempt 最大的那条终态行。Loop 内部节点已经在 workflowPreviousOutputGraph
// 里由迭代投影覆盖，这里只处理顶层。
func previousOutputSummariesFromRuns(graph WorkflowDefinition, runs []NodeRun, incoming []WorkflowEdge, nodeRun NodeRun) []previousOutputSummary {
	if len(runs) == 0 || strings.TrimSpace(nodeRun.LoopPath) != "" {
		return nil
	}
	out := make([]previousOutputSummary, 0, len(incoming))
	for _, edge := range incoming {
		var best *NodeRun
		for i := range runs {
			candidate := runs[i]
			if candidate.NodeID != edge.From || candidate.LoopPath != "" {
				continue
			}
			if len(candidate.Output) == 0 {
				continue
			}
			if best == nil || candidate.Attempt > best.Attempt {
				best = &candidate
			}
		}
		if best == nil {
			continue
		}
		if summary := nodeRunOutputSummary(*best); summary != "" {
			out = append(out, previousOutputSummary{Name: workflowNodeDisplayName(graph, edge.From), Summary: summary})
		}
	}
	return out
}

// workflowNodeDisplayName 与审批卡一致：优先节点名，缺失时退回 id。
func workflowNodeDisplayName(graph WorkflowDefinition, nodeID string) string {
	if node, ok := graph.node(nodeID); ok {
		if name := strings.TrimSpace(node.Name); name != "" {
			return name
		}
	}
	return nodeID
}

func listWorkflowNodeRunsOrNil(ctx context.Context, store *TaskStore, runID string) ([]NodeRun, error) {
	if store == nil {
		return nil, nil
	}
	runs, err := store.ListWorkflowNodeRuns(ctx, runID)
	if err != nil {
		return nil, err
	}
	return runs, nil
}
