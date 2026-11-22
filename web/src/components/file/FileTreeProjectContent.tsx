import React from "react";
import { NodeBadgeHeader } from "../account/NodeBadgeHeader";
import type { NodesAvailability } from "../../shared/nodeAvailability";
import type { MessageKey } from "../../i18n/types";
import type { FileTreeNodeGroup } from "./FileTreeEntryFilters";

const noticeStyle: React.CSSProperties = {
  margin: "2px 8px 6px",
  padding: "6px 8px",
  borderRadius: "8px",
  border: "1px dashed var(--border-color)",
  color: "var(--text-secondary)",
  fontSize: "11px",
  fontWeight: 600,
  lineHeight: 1.5,
};

/**
 * 项目分组列表。空状态分三种：真空 / 所有节点都连不上 / 节点表都没拉到 ——
 * 后两种以前都显示成「添加一个项目开始 vibe」，等于把「不知道」说成「没有」。
 */
export function FileTreeProjectContent({ groups, unreachableNodes = [], nodesAvailability = "ok", creatingRootName, t, renderEntries, rootId }: {
  groups: FileTreeNodeGroup[];
  unreachableNodes?: Array<{ id: string; name: string }>;
  nodesAvailability?: NodesAvailability;
  creatingRootName: string | null;
  t: (key: MessageKey) => string;
  renderEntries: (items: FileTreeNodeGroup["items"], depth: number, root: string, color?: string, nodeId?: string) => React.ReactNode;
  rootId: string | null | undefined;
}) {
  if (groups.length === 0 && creatingRootName === null && unreachableNodes.length === 0) {
    const emptyKey: MessageKey = nodesAvailability === "table-unreachable"
      ? "fileTree.nodesTableUnreachable"
      : nodesAvailability === "all-unreachable"
        ? "fileTree.nodesUnreachable"
        : "fileTree.emptyProjectHint";
    return <div style={{ flex: 1, minHeight: 0, display: "flex", alignItems: "center", justifyContent: "center", padding: "20px", textAlign: "center", color: "var(--text-secondary)", fontSize: "13px", fontWeight: 800, lineHeight: 1.5 }}>{t(emptyKey)}</div>;
  }
  return (
    <>
      {unreachableNodes.map((node) => (
        <div key={`dead:${node.id}`} style={noticeStyle}>{t("fileTree.nodeUnreachable")} · {node.name}</div>
      ))}
      {groups.map((group) => (
        <div key={group.name}>
          <NodeBadgeHeader color={group.color} label={group.name} notice={group.unreachable ? t("fileTree.nodeUnreachable") : undefined} />
          {renderEntries(group.items, 0, rootId || "", group.color, group.nid)}
        </div>
      ))}
    </>
  );
}