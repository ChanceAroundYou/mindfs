package kanban

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// routerAdhocWorkflowID 是临时 workflow 的占位 ID：它不进控制面目录，只为了让
// Run 的 workflow_id 列有一个可读的前缀，和目录里的条目一眼能分开。
const routerAdhocWorkflowID = "adhoc"

// Router 当场起草 workflow（W3）的边界。
//
// 用户裁定：项目级、临时使用的 workflow 可以自由生成，固化或提升到全局才需要
// 显式批准。临时使用走「这一次决策里直接给图」：Run 自己快照整张图，控制面目录
// 一个字都不写。图是模型写的，所以尺寸和结构都在这里按与编辑器同一套规则先挡。
const (
	routerInlineGraphMaxNodes = 12
	routerInlineGraphMaxEdges = 24
	// routerInlineGraphMaxBytes 是整张图的 JSON 上限。提示词与 params 都在图里，
	// 不设上限就等于允许一次决策把任意大的文本写进任务库。

	routerInlineGraphMaxBytes = 64 * 1024
)

// sanitizeRouterGraph 校验并规整 Router 起草的图，返回可以起 Run 的副本。
//
// 四条边界：
//   - 规模：节点 / 边 / 字节都有上限（12 / 24 / 64 KiB），超了直接拒绝；
//   - 结构：走 ValidateWorkflowDefinitionForAuthoring —— 与工作流编辑器同一套
//     规则（未知 kind、悬空边、环、末端 gate 都拒）；
//   - 执行：agent 节点缺 params.agent 时补上默认 harness（见 fillDefaultNodeAgent）；
//   - 作用域：RootID 一律清空。临时图不属于任何目录条目，作用域由 apply 阶段
//     按任务所在 root 填，绝不让模型自己声明「全局」。
func sanitizeRouterGraph(graph *WorkflowDefinition) (*WorkflowDefinition, error) {
	if graph == nil {
		return nil, errors.New("graph required")
	}
	if len(graph.Nodes) == 0 {
		return nil, errors.New("graph has no nodes")
	}
	if len(graph.Nodes) > routerInlineGraphMaxNodes {
		return nil, fmt.Errorf("graph has %d nodes, at most %d allowed", len(graph.Nodes), routerInlineGraphMaxNodes)
	}
	if edges := inlineGraphEdgeCount(*graph); edges > routerInlineGraphMaxEdges {
		return nil, fmt.Errorf("graph has %d edges, at most %d allowed", edges, routerInlineGraphMaxEdges)
	}
	raw, err := json.Marshal(graph)
	if err != nil {
		return nil, fmt.Errorf("encode graph: %w", err)
	}
	if len(raw) > routerInlineGraphMaxBytes {
		return nil, fmt.Errorf("graph is %d bytes, at most %d allowed", len(raw), routerInlineGraphMaxBytes)
	}
	if err := ValidateWorkflowDefinitionForAuthoring(*graph); err != nil {
		return nil, fmt.Errorf("workflow graph invalid: %w", err)
	}
	out := cloneWorkflowDefinition(*graph)
	out.RootID = ""
	fillDefaultNodeAgent(out.Nodes)
	// 默认模型是模型写的字符串：去空白，空串等于没设（否则运行期会多一个假的
	// 空档，把「全局默认」变成「显式空模型」）。拷贝一份，不改调用方的图。
	if graph.Defaults != nil {
		defaults := *graph.Defaults
		defaults.Model = strings.TrimSpace(defaults.Model)
		if defaults.Model == "" {
			out.Defaults = nil
		} else {
			out.Defaults = &defaults
		}
	}
	return &out, nil
}

// fillDefaultNodeAgent 给 Router 起草的图补上节点 harness。
//
// params.agent 是结构字段：缺了它图能通过校验、能固化、能发布，节点却在执行
// 时以 "agent required" 立刻失败，而 Router 起草时只写 prompt 是常态（2026-11-21
// 隔离验收里起草与固化出的两个版本都跑不起来）。缺省值取 Router 自己的默认
// harness；显式写了的节点一律不动。
func fillDefaultNodeAgent(nodes []WorkflowNode) {
	for i := range nodes {
		n := &nodes[i]
		switch n.Kind {
		case NodeKindAgent:
			if s, _ := n.Params["agent"].(string); strings.TrimSpace(s) != "" {
				continue
			}
			if n.Params == nil {
				n.Params = map[string]any{}
			}
			n.Params["agent"] = defaultRouterAgent
		case NodeKindLoop:
			// 内层图以原始 JSON（map/slice）存在 params.graph 里，重新编解码会
			// 丢掉未知键，所以就地递归。
			fillRawNodeAgent(n.Params["graph"])
		}
	}
}

func fillRawNodeAgent(raw any) {
	switch v := raw.(type) {
	case map[string]any:
		if kind, _ := v["kind"].(string); kind == NodeKindAgent {
			params, _ := v["params"].(map[string]any)
			if params == nil {
				params = map[string]any{}
				v["params"] = params
			}
			if s, _ := params["agent"].(string); strings.TrimSpace(s) == "" {
				params["agent"] = defaultRouterAgent
			}
		}
		if nodes, ok := v["nodes"]; ok {
			fillRawNodeAgent(nodes)
		}
		if params, ok := v["params"].(map[string]any); ok {
			fillRawNodeAgent(params["graph"])
		}
	case []any:
		for _, child := range v {
			fillRawNodeAgent(child)
		}
	}
}

// cloneWorkflowDefinition 深拷贝节点参数。补 agent 只改副本，不能把改动漏回
// 调用方（决策里存的图、卡片载荷里的图都指向原来那份 map）。
func cloneWorkflowDefinition(in WorkflowDefinition) WorkflowDefinition {
	out := in
	out.Nodes = make([]WorkflowNode, len(in.Nodes))
	for i, n := range in.Nodes {
		if n.Params != nil {
			n.Params, _ = cloneWorkflowParams(n.Params).(map[string]any)
		}
		out.Nodes[i] = n
	}
	return out
}

// cloneWorkflowParams 深拷贝 JSON 形状的节点参数。
func cloneWorkflowParams(raw any) any {
	switch v := raw.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, child := range v {
			out[k] = cloneWorkflowParams(child)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			out[i] = cloneWorkflowParams(child)
		}
		return out
	default:
		return v
	}
}

// adhocVersionID 按图内容生成版本号：同一次决策重放会得到同一个 ID，日志与
// 任务详情里也能看出两次执行是不是同一张图。
func adhocVersionID(graph WorkflowDefinition) string {
	raw, err := json.Marshal(graph)
	if err != nil {
		return "adhoc:" + routerAdhocWorkflowID
	}
	sum := sha256.Sum256(raw)
	return "adhoc:" + hex.EncodeToString(sum[:8])
}

// inlineGraphEdgeCount 数一张图里所有的边，包括 Loop 子图里的 —— 子图也是模型
// 写的，不数就会留下一个绕过上限的口子。
func inlineGraphEdgeCount(graph WorkflowDefinition) int {
	total := len(graph.Edges)
	for _, node := range graph.Nodes {
		inner, ok := node.Params["graph"].(map[string]any)
		if !ok {
			continue
		}
		nodes, _ := inner["nodes"].([]any)
		edges, _ := inner["edges"].([]any)
		total += len(edges)
		total += len(nodes)
	}
	return total
}
