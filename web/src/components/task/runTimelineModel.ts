// 运行视图的纯派生规则（无 React、无副作用）。
//
// NodeRun 本身不带节点类型，HumanGate 的审批入口必须回查 run 快照里的
// graph.nodes[].kind —— 只看 status="waiting_user" 会把条件求值失败、Agent
// 追问等同样停住的节点误当成可批准的网关。

export type RunGraphLike = { nodes?: Array<{ id?: string; kind?: string }> } | null | undefined;

// 节点状态 → 颜色令牌。运行视图与历史流水（legacy stages 投影）共用这一份，
// 否则同一个「成功/失败」在两条时间线上会是两种绿/红。
// approved / rejected 是 legacy 段的状态（用户审批结果），不归一成 success。
export function nodeRunStatusColor(status: string): string {
  switch (status) {
    case "success":
    case "approved":
    case "running":
      return "var(--accent-color)";
    case "fail":
    case "cancelled":
    case "rejected":
      return "var(--danger-color, #ef4444)";
    case "waiting_user":
      return "var(--warning-color, #f59e0b)";
    case "skipped":
    case "unknown":
    case "pending":
    case "queued":
    default:
      return "var(--text-secondary)";
  }
}

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