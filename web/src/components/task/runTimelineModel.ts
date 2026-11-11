// 运行视图的纯派生规则（无 React、无副作用）。
//
// NodeRun 本身不带节点类型，HumanGate 的审批入口必须回查 run 快照里的
// graph.nodes[].kind —— 只看 status="waiting_user" 会把条件求值失败、Agent
// 追问等同样停住的节点误当成可批准的网关。

export type RunGraphLike = { nodes?: Array<{ id?: string; kind?: string }> } | null | undefined;

export function runNodeKinds(graph: RunGraphLike): Record<string, string> {
  const kinds: Record<string, string> = {};
  for (const node of graph?.nodes ?? []) {
    if (!node?.id || !node.kind) continue;
    kinds[node.id] = node.kind;
  }
  return kinds;
}

export function isWaitingHumanGate(
  nodeRun: { node_id: string; status: string },
  nodeKinds: Record<string, string> | null | undefined,
): boolean {
  return nodeRun.status === "waiting_user" && nodeKinds?.[nodeRun.node_id] === "human_gate";
}