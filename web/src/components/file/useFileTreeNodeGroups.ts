import { getNodes } from "../../services/net/nodeRegistry";
import { useNodeHealth } from "../../hooks/useNodeHealth";
import { classifyNodesAvailability, isNodeUnreachable } from "../../shared/nodeAvailability";
import { groupEntriesByNode } from "./FileTreeEntryFilters";
import type { FileEntry } from "../../shared/directorySort";

/**
 * 文件树的分组 + 「谁连不上」。
 *
 * 连不上的节点不会因为「拉不到」而消失：有缓存条目时它照常成组，只是组头挂提示；
 * 一条缓存都没有时单独列进 `unreachableNodes`，否则界面上只剩一句「还没有项目」——
 * 那是在把「不知道」说成「没有」。
 */
export function useFileTreeNodeGroups(entries: FileEntry[]) {
  const health = useNodeHealth();
  const nodes = getNodes();
  const unreachableOf = (nodeId: string) => isNodeUnreachable(nodeId, health.nodes);
  const groups = groupEntriesByNode(entries, () => nodes, unreachableOf);
  const grouped = new Set(groups.map((group) => group.nid));
  const unreachableNodes = nodes
    .map((node) => ({ id: String(node.id), name: String(node.name), color: String(node.color || "") }))
    .filter((node) => unreachableOf(node.id) && !grouped.has(node.id));
  const nodesAvailability = classifyNodesAvailability({
    nodeIds: nodes.map((node) => String(node.id)),
    nodes: health.nodes,
    nodeTable: health.nodeTable,
  });
  return { groups, unreachableNodes, nodesAvailability };
}