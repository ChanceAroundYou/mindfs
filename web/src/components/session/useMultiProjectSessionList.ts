import { useEffect, useMemo, useState, useSyncExternalStore } from "react";
import { type DirectorySortMode, sortDirectoryEntries } from "../../services/prefs/directorySort";
import { getNodes, PALETTE } from "../../services/net/nodeRegistry";
import { resolveGroupColor } from "../../shared/sessionGroupDisplay";
import { mergeSessionItems, orderProjectGroupsByNode } from "../../services/session/sessionListMerge";
import { scopeKey } from "../../shared/scope";
import {
  optimisticProjectPin,
  readPins,
  refreshPinsFromServer,
  setProjectPin,
  subscribePins,
  writePins,
} from "../../services/pins";

import type {
  ProjectSessionGroup,
  SessionItem,
  VisibleSessionRow,
} from "./sessionListTypes";
import { useSessionChildTree } from "./useSessionChildTree";

type UseMultiProjectSessionListOptions = {
  groups: ProjectSessionGroup[];
  selectedKey: string;
  selectedRootId: string;
  selectedNodeId: string;
  projectSortMode: DirectorySortMode;
  onLoadMoreProject?: (group: ProjectSessionGroup) => Promise<void> | void;
  onLoadChildren?: (
    session: SessionItem,
    options?: { beforeTime?: string },
  ) => Promise<{ hasMore?: boolean } | void> | { hasMore?: boolean } | void;
};

const EXPANDED_PROJECTS_STORAGE_KEY = "mindfs:sessionPanelExpandedProjects:v1";
const EXPANDED_CHILDREN_STORAGE_KEY = "mindfs:sessionPanelExpandedChildren:v1";

function loadExpandedMap(key: string): Record<string, boolean> {
  if (typeof window === "undefined") return {};
  try {
    const raw = window.localStorage.getItem(key);
    if (!raw) return {};
    const parsed = JSON.parse(raw);
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return {};
    const out: Record<string, boolean> = {};
    for (const [k, v] of Object.entries(parsed as Record<string, unknown>)) {
      if (k && typeof v === "boolean") out[k] = v;
    }
    return out;
  } catch {
    return {};
  }
}

function persistExpandedMap(key: string, value: Record<string, boolean>): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(key, JSON.stringify(value || {}));
  } catch {}
}
export function useMultiProjectSessionList({
  groups,
  selectedKey,
  selectedRootId,
  selectedNodeId,
  projectSortMode,
  onLoadMoreProject,
  onLoadChildren,
}: UseMultiProjectSessionListOptions) {
  const [expandedProjects, setExpandedProjects] = useState<Record<string, boolean>>(
    () => loadExpandedMap(EXPANDED_PROJECTS_STORAGE_KEY),
  );
  const [expandedChildren, setExpandedChildren] = useState<Record<string, boolean>>(
    () => loadExpandedMap(EXPANDED_CHILDREN_STORAGE_KEY),
  );
  const [loadingProjects, setLoadingProjects] = useState<Record<string, boolean>>({});

  useEffect(() => {
    persistExpandedMap(EXPANDED_PROJECTS_STORAGE_KEY, expandedProjects);
  }, [expandedProjects]);

  useEffect(() => {
    persistExpandedMap(EXPANDED_CHILDREN_STORAGE_KEY, expandedChildren);
  }, [expandedChildren]);


  useEffect(() => {
    void refreshPinsFromServer().catch(() => {
      /* 拉不到就保留 store 里那份，不闪成全不置顶 */
    });
  }, [selectedRootId, selectedNodeId]);

  const pinsSnapshot = useSyncExternalStore(subscribePins, readPins, readPins);
  const pinnedProjects = pinsSnapshot.projects;
  const nodes = getNodes();

  const groupScopeKey = (group: ProjectSessionGroup) =>
    scopeKey(String((group as any)?._nodeId || "").trim(), group.rootId);

  const orderedGroups = useMemo(() => {
    const orderByName = new Map(nodes.map((n, i) => [String(n.name), i] as const));
    const nodeKey = (group: ProjectSessionGroup): string => {
      const nid = String((group as any)?._nodeId || "").trim();
      if (nid && nodes.some((node) => String(node.id || "").trim() === nid)) return nid;
      const nname = String((group as any)?._nodeName || "").trim();
      if (nname && orderByName.has(nname)) {
        return String(nodes[orderByName.get(nname)!]?.id || "").trim() || `unknown-name:${nname}`;
      }
      return nid ? `unknown-id:${nid}` : nname ? `unknown-name:${nname}` : "";
    };
    const compareByProjectSort = (a: ProjectSessionGroup, b: ProjectSessionGroup): number => {
      const ea = { name: a.rootName || a.rootId, path: a.rootId, is_dir: true } as Parameters<typeof sortDirectoryEntries>[0][number];
      const eb = { name: b.rootName || b.rootId, path: b.rootId, is_dir: true } as Parameters<typeof sortDirectoryEntries>[0][number];
      const sorted = sortDirectoryEntries([ea, eb], projectSortMode);
      if (sorted[0] === ea && sorted[1] === eb) return -1;
      if (sorted[0] === eb && sorted[1] === ea) return 1;
      return 0;
    };
    return orderProjectGroupsByNode(
      groups,
      nodes.map((node) => String(node.id || "").trim()).filter(Boolean),
      nodeKey,
      (group) => (pinnedProjects[groupScopeKey(group)] || 0) > 0,
      compareByProjectSort,
    );
  }, [groups, nodes, pinnedProjects, projectSortMode]);

  const togglePinnedProject = (group: ProjectSessionGroup) => {
    const key = groupScopeKey(group);
    const pinned = !readPins().projects[key];
    const rollback = optimisticProjectPin(key, pinned);
    void setProjectPin(key, pinned).then(
      (next) => {
        writePins(next);
      },
      () => {
        rollback();
      },
    );
  };

  const sessionByKey = useMemo(() => {
    const byKey = new Map<string, SessionItem>();
    for (const group of groups) {
      for (const item of group.sessions) {
        const sessionRoot = item.root_id || group.rootId;
        const itemNodeId = String((item as any)?._nodeId || (group as any)?._nodeId || "").trim();
        byKey.set(`${sessionRoot}:${item.key}`, item);
        byKey.set(scopeKey(itemNodeId, sessionRoot ? `${sessionRoot}:${item.key}` : item.key), item);
        byKey.set(item.key, item);
      }
    }
    return byKey;
  }, [groups]);

  const childStateKey = (session: SessionItem, fallbackRootId = "", fallbackNodeId = "") => {
    const rootId = session.root_id || fallbackRootId;
    const nodeId = String((session as any)?._nodeId || fallbackNodeId || "").trim();
    return `${scopeKey(nodeId, rootId)}:${session.key}`;
  };
  const childTree = useSessionChildTree({
    sessions: groups.flatMap((group) => group.sessions),
    selectedKey,
    selectedScope: { rootId: selectedRootId, nodeId: selectedNodeId },
    stateKey: (session, scope) => childStateKey(session, scope.rootId, scope.nodeId),
    initialExpandedChildren: expandedChildren,
    onExpandedChildrenChange: setExpandedChildren,
    onLoadChildren,
  });
  const { loadingChildren, childrenHasMore } = childTree;
  const buildRows = (items: SessionItem[], rootId: string, nodeId = "") =>
    childTree.buildRows(items, { rootId, nodeId });
  const handleChildToggle = (
    row: Extract<VisibleSessionRow, { type: "child-toggle" }>,
    items: SessionItem[],
    rootId: string,
    nodeId = "",
  ) => childTree.toggleChild(row, items, { rootId, nodeId });
  const collapseChild = (parent: SessionItem, rootId: string, nodeId = "") =>
    childTree.collapseChild(parent, { rootId, nodeId });

  const topLevelSessionsForGroup = (sessions: SessionItem[]) =>
    sessions.filter((session) => !String(session.parent_session_key || "").trim());

  const groupDefaultExpanded = (_group: ProjectSessionGroup) => true;

  const loadMoreProject = async (group: ProjectSessionGroup) => {
    const groupKey = groupScopeKey(group);
    const topLevelCount = topLevelSessionsForGroup(group.sessions).length;
    const remaining = Math.max(0, group.totalCount - topLevelCount);
    if (remaining <= 0 || !onLoadMoreProject) return;
    setLoadingProjects((prev) => ({ ...prev, [groupKey]: true }));
    try {
      await onLoadMoreProject(group);
    } finally {
      setLoadingProjects((prev) => ({ ...prev, [groupKey]: false }));
    }
  };

  const handleProjectHeaderToggle = async (group: ProjectSessionGroup) => {
    const key = groupScopeKey(group);
    const expanded = expandedProjects[key] ?? groupDefaultExpanded(group);
    if (expanded) {
      setExpandedProjects((prev) => ({ ...prev, [key]: false }));
      return;
    }
    setExpandedProjects((prev) => ({ ...prev, [key]: true }));
    const topLevelCount = topLevelSessionsForGroup(group.sessions).length;
    const remaining = Math.max(0, group.totalCount - topLevelCount);
    if (remaining > 0 && onLoadMoreProject) {
      setLoadingProjects((prev) => ({ ...prev, [key]: true }));
      try {
        await onLoadMoreProject(group);
      } finally {
        setLoadingProjects((prev) => ({ ...prev, [key]: false }));
      }
    }
  };

  return {
    nodes,
    pinsSnapshot,
    pinnedProjects,
    orderedGroups,
    sessionByKey,
    togglePinnedProject,
    loadMoreProject,
    handleProjectHeaderToggle,
    expandedProjects,
    loadingProjects,
    groupDefaultExpanded,
    groupScopeKey,
    buildRows,
    handleChildToggle,
    expandedChildren,
    collapseChild,
    loadingChildren,
    childrenHasMore,
    childStateKey,
    topLevelSessionsForGroup,
  };
}
