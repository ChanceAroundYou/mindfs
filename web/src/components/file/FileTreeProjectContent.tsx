import React from "react";
import { NodeBadgeHeader } from "../account/NodeBadgeHeader";
import { groupEntriesByNode } from "./FileTreeEntryFilters";
export function FileTreeProjectContent({ entries, creatingRootName, getNodes, t, renderEntries, rootId }: any) {
  if (entries.length === 0 && creatingRootName === null) return <div style={{ flex: 1, minHeight: 0, display: "flex", alignItems: "center", justifyContent: "center", padding: "20px", textAlign: "center", color: "var(--text-secondary)", fontSize: "13px", fontWeight: 800, lineHeight: 1.5 }}>{t("fileTree.emptyProjectHint")}</div>;
  const groups = groupEntriesByNode(entries, getNodes);
  return <>{groups.map((group: any) => <div key={group.name}><NodeBadgeHeader color={group.color} label={group.name} />{renderEntries(group.items, 0, rootId || "", group.color, group.nid)}</div>)}</>;
}
