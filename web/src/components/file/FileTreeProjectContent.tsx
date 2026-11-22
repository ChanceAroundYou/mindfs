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
 *
 * 「节点表没拉到」不能只在空列表时提示：它说的是**屏幕上这份清单**可能已过时，
 * 而陈旧缓存恰恰意味着列表还在（还显示着已删除的节点、或少了刚加的）。只在零项目
 * 时说，等于最该提醒的时刻一声不响 —— 所以有分组时也在顶部挂一条。
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
      {nodesAvailability === "table-unreachable" ? (
        <div style={noticeStyle} data-testid="file-tree-node-table-stale">{t("fileTree.nodesTableUnreachable")}</div>
      ) : null}
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