import type { Edge, Node } from "@xyflow/react";
import type { WorkflowGraph, WorkflowNodeDef, WorkflowNodeKind } from "../../../shared/workflowModel";
import { generateReactFlowNodeStyle, getConditionalEdgeStyle, EDGE_STYLE_CONFIG } from "../workflowNodeStyles";

// ReactFlow <-> WorkflowGraph 的纯转换。顶层画布与 Loop 子画布共用，
// 保证两边对位置/边/参数的序列化完全一致（尤其是拖动不携带业务参数）。

export function flowNodes(graph: WorkflowGraph, labels: Record<string, string>): Node[] {
  return (graph.nodes || []).map((n, i) => ({
    id: n.id,
    type: 'workflowNode',
    position: n.position ?? { x: 80 + (i % 4) * 230, y: 80 + Math.floor(i / 4) * 150 },
    data: {
      label: n.name || labels[n.kind] || n.kind,
      kind: n.kind,
    },
    style: {
      ...generateReactFlowNodeStyle(n.kind),
      cursor: 'pointer',
    },
  }));
}

export function flowEdges(graph: WorkflowGraph): Edge[] {
  return (graph.edges || []).map((e) => {
    const conditionalStyle = getConditionalEdgeStyle(e.route);
    return {
      id: `${e.from}->${e.to}${e.route ? `:${e.route}` : ""}`,
      source: e.from,
      target: e.to,
      label: e.route || undefined,
      style: {
        stroke: conditionalStyle.stroke || EDGE_STYLE_CONFIG.stroke,
        strokeWidth: EDGE_STYLE_CONFIG.strokeWidth,
      },
      animated: EDGE_STYLE_CONFIG.animated,
      type: EDGE_STYLE_CONFIG.type,
    };
  });
}

/** 业务字段（kind/params/name）只从旧图取，flow 只贡献 id、位置与连线。 */
export function graphFrom(nodes: Node[], edges: Edge[], prev: WorkflowGraph): WorkflowGraph {
  const defs: WorkflowNodeDef[] = nodes.map((n) => {
    const old = (prev.nodes || []).find((d) => d.id === n.id);
    return {
      id: n.id,
      name: old?.name ?? "",
      kind: (old?.kind ?? "agent") as WorkflowNodeKind,
      params: old?.params ?? {},
      position: { x: n.position.x, y: n.position.y },
    };
  });
  return {
    ...prev,
    nodes: defs,
    edges: edges.map((e) => ({
      from: e.source,
      to: e.target,
      ...(typeof e.label === "string" && e.label ? { route: e.label } : {}),
    })),
  };
}