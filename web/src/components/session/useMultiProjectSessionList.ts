import { useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
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
import { MULTI_PROJECT_VISIBLE_LIMIT } from "./sessionListShared";
import { taskGroupedRowCount } from "./sessionTaskGroups";
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
  const toggleTaskGroup = (taskId: string) => childTree.toggleTaskGroup(taskId);
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

  /**
   * 这个项目在侧栏里占了几行：任务组算 1 行、组内成员与子会话不占位。
   *
   * 不需要 `task_session_count` / 进程内记忆那套阈值：它们只决定「1 条会话算不算
   * 单独成组」，而一条会话无论成不成组都占 1 行，行数不变。所以按本页成员数判
   * 分组就与渲染出来的行数一致。
   */
  const topLevelRowCount = (items: SessionItem[]) =>
    taskGroupedRowCount(topLevelSessionsForGroup(items));

  // 每个项目上一次补拉时的顶层会话条数：用来识别「这一页没有带回新会话」。
  const budgetPageRef = useRef<Record<string, number>>({});

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

  /**
   * 按行补满项目预算（2026-11-20 用户决策：「把会话组作为一个会话，不要因组里有
   * 好几个对话而显示不完」）。
   *
   * 项目预算 MULTI_PROJECT_VISIBLE_LIMIT 按行算，而每页拉回来的是「会话」：一个
   * 装了 4 条会话的任务组只占 1 行，项目里可显示的行数就短了 3 行，末尾于是冒出
   * 「还有 N 个会话」，可列表其实还有空位。这里按缺口继续往后翻页，直到补满预算
   * 或后端没有更多。
   *
   * 三条闸门缺一不可：
   *   - 一次只补一个项目（`break`），避免一圈项目同时打请求；
   *   - 正在加载的项目跳过，等这一页回来再判；
   *   - 记住上一次补拉时的顶层会话条数，没有增长就停 —— 一页回来全是已加载会话
   *     的子会话或已有分组的成员时，`remaining` 仍然大于 0，再拉只是重复请求。
   */
  useEffect(() => {
    if (!onLoadMoreProject) return;
    for (const group of orderedGroups) {
      const key = groupScopeKey(group);
      const topLevelCount = topLevelSessionsForGroup(group.sessions).length;
      const shortOfBudget =
        !!(expandedProjects[key] ?? groupDefaultExpanded(group)) &&
        group.totalCount > topLevelCount &&
        topLevelRowCount(group.sessions) < MULTI_PROJECT_VISIBLE_LIMIT;
      if (!shortOfBudget) {
        // 不需要补了（补满 / 后端没有更多 / 项目收起）：清掉记录，下次重新缺行
        // 时还能再补 —— 否则换账户、刷新换了一批会话也会被旧记录挡住。
        delete budgetPageRef.current[key];
        continue;
      }
      if (loadingProjects[key]) continue;
      if ((budgetPageRef.current[key] ?? -1) >= topLevelCount) continue;
      budgetPageRef.current[key] = topLevelCount;
      void loadMoreProject(group);
      break;
    }
  });

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
    toggleTaskGroup,
    handleChildToggle,
    expandedChildren,
    collapseChild,
    loadingChildren,
    childrenHasMore,
    childStateKey,
    topLevelSessionsForGroup,
    topLevelRowCount,
  };
}
