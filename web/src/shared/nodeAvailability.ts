/**
 * 节点可达性的纯判定：把「观测到的健康状态」翻译成界面能用的三种情形。
 *
 * 放在 shared 而不是组件里，是因为它要被逻辑层（文件树分组、空状态）和
 * 测试同时用到；这一层不碰 React、不碰网络，只做判断。
 */

export type NodeHealthLike = { status: "online" | "offline" };

export type NodesAvailability = "ok" | "all-unreachable" | "table-unreachable";

export type NodesAvailabilityInput = {
  /** 当前已知的节点 id（含 `local`）。 */
  nodeIds: readonly string[];
  /** 每个节点的最近一次观测；没有条目 = 还没观测过（不能当成离线）。 */
  nodes: Readonly<Record<string, NodeHealthLike | undefined>>;
  /** 节点表本身的拉取结果；null = 还没拉过。 */
  nodeTable: NodeHealthLike | null;
};

/**
 * - `table-unreachable`：节点表都拉不到，屏幕上这份清单本身就不可信，
 *   此时说「没有项目」等于把「不知道」说成「没有」；
 * - `all-unreachable`：清单是可信的，但清单上每一台都探不通；
 * - `ok`：至少有一台是通的（或还没观测过 —— 未观测不等于离线）。
 *
 * 顺序不能换：节点表失败比「每台都失败」更根本，先报它。
 */
export function classifyNodesAvailability(input: NodesAvailabilityInput): NodesAvailability {
  if (input.nodeTable?.status === "offline") return "table-unreachable";
  const ids = input.nodeIds.map((id) => String(id ?? "").trim()).filter(Boolean);
  if (ids.length === 0) return "ok";
  const everyOffline = ids.every((id) => input.nodes[id]?.status === "offline");
  return everyOffline ? "all-unreachable" : "ok";
}

/** 单台节点是不是「明确不可达」（没观测过不算）。 */
export function isNodeUnreachable(
  nodeId: string | null | undefined,
  nodes: Readonly<Record<string, NodeHealthLike | undefined>>,
): boolean {
  const id = String(nodeId ?? "").trim();
  if (!id) return false;
  return nodes[id]?.status === "offline";
}