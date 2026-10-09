import { useEffect, useMemo, useState, useSyncExternalStore } from "react";
import { pruneChildState } from "../../services/sessionTree";
import { type DirectorySortMode, sortDirectoryEntries } from "../../services/prefs/directorySort";
import { getNodes, PALETTE } from "../../services/net/nodeRegistry";
import { resolveGroupColor } from "../../services/sessionGroupDisplay";
import { scopeKey } from "../../shared/scope";
import {
  optimisticProjectPin,
  readPins,
  refreshPinsFromServer,
  setProjectPin,
  subscribePins,
  writePins,
} from "../../services/pins";
import {
  COLLAPSED_CHILD_SESSION_LIMIT,
  parseForkSessionSource,
} from "./sessionListShared";
import type {
  ProjectSessionGroup,
  SessionItem,
  VisibleSessionRow,
} from "./sessionListTypes";

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

export function useMultiProjectSessionList({
  groups,
  selectedKey,
  selectedRootId,
  selectedNodeId,
  projectSortMode,
  onLoadMoreProject,
  onLoadChildren,
}: UseMultiProjectSessionListOptions) {
  const [expandedProjects, setExpandedProjects] = useState<Record<string, boolean>>({});
  const [loadingProjects, setLoadingProjects] = useState<Record<string, boolean>>({});
  const [expandedChildren, setExpandedChildren] = useState<Record<string, boolean>>({});
  const [loadingChildren, setLoadingChildren] = useState<Record<string, boolean>>({});
  const [childrenHasMore, setChildrenHasMore] = useState<Record<string, boolean>>({});
  const [collapsedBaseCount, setCollapsedBaseCount] = useState<Record<string, number>>({});

  useEffect(() => {
    void refreshPinsFromServer().catch(() => {
      /* 拉不到就保留 store 里那份，不闪成全不置顶 */
    });
  }, [selectedRootId, selectedNodeId]);

  const pinsSnapshot = useSyncExternalStore(subscribePins, readPins, readPins);
  const pinnedProjects = pinsSnapshot.projects;

  useEffect(() => {
    const live = new Set<string>();
    for (const group of groups) {
      for (const item of group.sessions || []) {
        if (item.key) live.add(item.key);
      }
    }
    setExpandedChildren((prev) => pruneChildState(prev, live));
    setLoadingChildren((prev) => pruneChildState(prev, live));
    setChildrenHasMore((prev) => pruneChildState(prev, live));
  }, [groups]);

  const groupScopeKey = (group: ProjectSessionGroup) =>
    scopeKey(String((group as any)?._nodeId || "").trim(), group.rootId);

  const orderedGroups = useMemo(() => {
    const nodes = getNodes();
    const orderById = new Map(nodes.map((n, i) => [String(n.id), i] as const));
    const orderByName = new Map(nodes.map((n, i) => [String(n.name), i] as const));
    const nodeIndex = (group: ProjectSessionGroup): number => {
      const nid = String((group as any)?._nodeId || "").trim();
      if (nid && orderById.has(nid)) return orderById.get(nid)!;
      const nname = String((group as any)?._nodeName || "").trim();
      if (nname && orderByName.has(nname)) return orderByName.get(nname)!;
      return 99;
    };
    const compareByProjectSort = (a: ProjectSessionGroup, b: ProjectSessionGroup): number => {
      const ea = { name: a.rootName || a.rootId, path: a.rootId, is_dir: true } as Parameters<typeof sortDirectoryEntries>[0][number];
      const eb = { name: b.rootName || b.rootId, path: b.rootId, is_dir: true } as Parameters<typeof sortDirectoryEntries>[0][number];
      const sorted = sortDirectoryEntries([ea, eb], projectSortMode);
      if (sorted[0] === ea && sorted[1] === eb) return -1;
      if (sorted[0] === eb && sorted[1] === ea) return 1;
      return 0;
    };
    return groups.slice().sort((left, right) => {
      const li = nodeIndex(left);
      const ri = nodeIndex(right);
      if (li !== ri) return li - ri;
      const leftPinnedAt = pinnedProjects[groupScopeKey(left)] || 0;
      const rightPinnedAt = pinnedProjects[groupScopeKey(right)] || 0;
      const leftPinned = leftPinnedAt > 0;
      const rightPinned = rightPinnedAt > 0;
      if (leftPinned !== rightPinned) return rightPinned ? 1 : -1;
      if (leftPinned && rightPinned && leftPinnedAt !== rightPinnedAt) {
        return rightPinnedAt - leftPinnedAt;
      }
      return compareByProjectSort(left, right);
    });
  }, [groups, pinnedProjects, projectSortMode]);

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

  const loadChildren = async (
    parent: SessionItem,
    beforeTime?: string,
    fallbackRootId = "",
    fallbackNodeId = "",
  ): Promise<void> => {
    const stateKey = childStateKey(parent, fallbackRootId, fallbackNodeId);
    if (!onLoadChildren || loadingChildren[stateKey]) {
      return;
    }
    setLoadingChildren((prev) => ({ ...prev, [stateKey]: true }));
    try {
      const result = await onLoadChildren(parent, beforeTime ? { beforeTime } : undefined);
      setChildrenHasMore((prev) => ({ ...prev, [stateKey]: !!result?.hasMore }));
    } finally {
      setLoadingChildren((prev) => ({ ...prev, [stateKey]: false }));
    }
  };

  const buildRows = (sessions: SessionItem[], fallbackRootId: string, fallbackNodeId = ""): VisibleSessionRow[] => {
    if (sessions.length === 0) return [];
    const isForkItem = (item: SessionItem) => !!parseForkSessionSource(item.source);
    const childrenByParent = new Map<string, SessionItem[]>();
    const topLevel: SessionItem[] = [];
    const keys = new Set(sessions.map((item) => item.key));
    const parentByKey = new Map<string, string>();
    for (const item of sessions) {
      if (isForkItem(item)) {
        topLevel.push(item);
        continue;
      }
      const parentKey = String(item.parent_session_key || "").trim();
      if (parentKey && keys.has(parentKey)) {
        const children = childrenByParent.get(parentKey) || [];
        children.push(item);
        childrenByParent.set(parentKey, children);
        parentByKey.set(item.key, parentKey);
      } else {
        topLevel.push(item);
      }
    }
    const activeParentKeys = new Set<string>();
    if (
      selectedKey &&
      selectedRootId === fallbackRootId &&
      String(selectedNodeId || "") === String(fallbackNodeId || "")
    ) {
      activeParentKeys.add(selectedKey);
      let parentKey = parentByKey.get(selectedKey) || "";
      while (parentKey) {
        activeParentKeys.add(parentKey);
        parentKey = parentByKey.get(parentKey) || "";
      }
    }
    const out: VisibleSessionRow[] = [];
    const append = (item: SessionItem) => {
      out.push({ type: "session", session: item });
      const children = childrenByParent.get(item.key) || [];
      const stateKey = childStateKey(item, fallbackRootId, fallbackNodeId);
      const active = activeParentKeys.has(item.key);
      const expanded = !!expandedChildren[stateKey];
      const visibleChildren = active
        ? expanded
          ? children
          : children.slice(0, COLLAPSED_CHILD_SESSION_LIMIT)
        : [];
      for (const child of visibleChildren) {
        append(child);
      }
      const collapsedBase = collapsedBaseCount[stateKey] ?? children.length;
      const hiddenCount = Math.max(0, collapsedBase - COLLAPSED_CHILD_SESSION_LIMIT);
      if (active && (hiddenCount > 0 || expanded || childrenHasMore[stateKey])) {
        out.push({
          type: "child-toggle",
          parent: item,
          loadedChildCount: collapsedBase,
          hiddenCount,
          expanded,
        });
      }
    };
    topLevel.forEach((item) => append(item));
    return out;
  };

  const handleChildToggle = async (
    row: Extract<VisibleSessionRow, { type: "child-toggle" }>,
    groupSessions: SessionItem[],
    fallbackRootId: string,
    fallbackNodeId = "",
  ) => {
    const parentKey = row.parent.key;
    const stateKey = childStateKey(row.parent, fallbackRootId, fallbackNodeId);
    if (!row.expanded) {
      setCollapsedBaseCount((prev) => ({
        ...prev,
        [stateKey]: groupSessions.filter((item) => item.parent_session_key === parentKey).length,
      }));
      setExpandedChildren((prev) => ({ ...prev, [stateKey]: true }));
      await loadChildren(row.parent, undefined, fallbackRootId, fallbackNodeId);
      return;
    }
    if (childrenHasMore[stateKey]) {
      const lastChild = groupSessions
        .filter((item) => item.parent_session_key === parentKey)
        .sort((left, right) => (Date.parse(left.updated_at || "") || 0) - (Date.parse(right.updated_at || "") || 0))[0];
      await loadChildren(row.parent, lastChild?.updated_at, fallbackRootId, fallbackNodeId);
    } else {
      setExpandedChildren((prev) => ({ ...prev, [stateKey]: false }));
    }
  };

  const topLevelSessionsForGroup = (sessions: SessionItem[]) =>
    sessions.filter((session) => !String(session.parent_session_key || "").trim());

  const groupIsCurrentNode = (group: ProjectSessionGroup) => {
    const groupNodeId = String((group as any)?._nodeId || "").trim();
    const selectedNid = String(selectedNodeId || "").trim();
    return !!groupNodeId && !!selectedNid && groupNodeId === selectedNid;
  };

  const groupDefaultExpanded = (group: ProjectSessionGroup) =>
    groupIsCurrentNode(group) || !!pinnedProjects[groupScopeKey(group)];

  const handleProjectToggle = async (group: ProjectSessionGroup) => {
    const groupKey = groupScopeKey(group);
    const expanded = expandedProjects[groupKey] ?? groupDefaultExpanded(group);
    const topLevelCount = topLevelSessionsForGroup(group.sessions).length;
    const remaining = Math.max(0, group.totalCount - topLevelCount);
    if (!expanded) {
      setExpandedProjects((prev) => ({ ...prev, [groupKey]: true }));
      if (remaining > 0 && onLoadMoreProject) {
        setLoadingProjects((prev) => ({ ...prev, [groupKey]: true }));
        try {
          await onLoadMoreProject(group);
        } finally {
          setLoadingProjects((prev) => ({ ...prev, [groupKey]: false }));
        }
      }
      return;
    }
    if (remaining > 0 && onLoadMoreProject) {
      setLoadingProjects((prev) => ({ ...prev, [groupKey]: true }));
      try {
        await onLoadMoreProject(group);
      } finally {
        setLoadingProjects((prev) => ({ ...prev, [groupKey]: false }));
      }
    } else {
      setExpandedProjects((prev) => ({ ...prev, [groupKey]: false }));
    }
  };

  const handleProjectCollapse = (group: ProjectSessionGroup) => {
    setExpandedProjects((prev) => ({ ...prev, [groupScopeKey(group)]: false }));
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
    pinsSnapshot,
    pinnedProjects,
    orderedGroups,
    sessionByKey,
    togglePinnedProject,
    handleProjectToggle,
    handleProjectCollapse,
    handleProjectHeaderToggle,
    expandedProjects,
    loadingProjects,
    groupDefaultExpanded,
    groupScopeKey,
    buildRows,
    handleChildToggle,
    expandedChildren,
    setExpandedChildren,
    loadingChildren,
    childrenHasMore,
    childStateKey,
    topLevelSessionsForGroup,
  };
}
