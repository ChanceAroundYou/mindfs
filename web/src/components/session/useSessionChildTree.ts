import { useEffect, useState } from "react";
import { pruneChildState } from "../../shared/sessionTree";
import { COLLAPSED_CHILD_SESSION_LIMIT, parseForkSessionSource } from "./sessionListShared";
import { orderTaskGroupedRows, rememberTaskSessions, taskGroupShowsMembers, taskSessionSeenCount } from "./sessionTaskGroups";
import type { SessionItem, VisibleSessionRow } from "./sessionListTypes";

type Scope = { rootId?: string; nodeId?: string };
type LoadChildren = (parent: SessionItem, options?: { beforeTime?: string }) => Promise<{ hasMore?: boolean } | void> | { hasMore?: boolean } | void;

/**
 * 这条子会话此刻算不算「在跑」。
 *
 * pending 是服务端真相表 + 本地事件给同一把灯；presence 是列表侧一次性算好的四态
 * （delegating 不算自己在跑，它的后代仍会独立命中）。两处判据任一为真即可 ——
 * 在跑的子会话必须可见，不能因为父会话没被选中、或折叠只取前 3 条而被埋掉。
 */
function isRunningSession(item: SessionItem): boolean {
  return (
    item.pending === true ||
    item.presence === "running" ||
    item.presence === "awaiting"
  );
}

/**
 * 折叠态要显示的 children：常规是前 N 条，另外把在跑的子会话一并纳入。
 *
 * 负责只取前 N 条时，一条 80 个子会话的父会话会把真正在跑的那条埋在第 4 名之外；
 * 用户点开父会话只能看到「展开 N 条」，看不到哪一个子代理还活着。
 */
function collapsedChildSessions(children: SessionItem[]): SessionItem[] {
  const head = children.slice(0, COLLAPSED_CHILD_SESSION_LIMIT);
  if (head.length === children.length) {
    return head;
  }
  const shown = new Set(head.map((child) => child.key));
  const running = children.filter((child) => !shown.has(child.key) && isRunningSession(child));
  return running.length ? [...head, ...running] : head;
}

export function useSessionChildTree({
  sessions,
  selectedKey,
  selectedScope,
  stateKey,
  liveKeys,
  initialExpandedChildren = {},
  onExpandedChildrenChange,
  onLoadChildren,
}: {
  sessions: SessionItem[];
  selectedKey: string;
  selectedScope?: Scope;
  stateKey: (session: SessionItem, scope: Scope) => string;
  liveKeys?: () => Set<string>;
  initialExpandedChildren?: Record<string, boolean>;
  onExpandedChildrenChange?: (value: Record<string, boolean>) => void;
  onLoadChildren?: LoadChildren;
}) {
  const [expandedChildren, setExpandedChildren] = useState<Record<string, boolean>>(() => initialExpandedChildren);
  // 任务分组默认折叠（2026-11-20 用户决策：和子会话的展开一个逻辑）。
  // 用「显式展开集合」而不是「显式收起集合」：默认态就是没有键，列表重排、
  // 新任务出现都不会把用户的意图带偏。
  const [expandedTaskGroups, setExpandedTaskGroups] = useState<Record<string, boolean>>({});
  const [loadingChildren, setLoadingChildren] = useState<Record<string, boolean>>({});
  const [childrenHasMore, setChildrenHasMore] = useState<Record<string, boolean>>({});
  const [collapsedBaseCount, setCollapsedBaseCount] = useState<Record<string, number>>({});

  useEffect(() => {
    const live = liveKeys?.() || new Set(sessions.map((item) => item.key).filter(Boolean));
    setExpandedChildren((prev) => pruneChildState(prev, live));
    setLoadingChildren((prev) => pruneChildState(prev, live));
    setChildrenHasMore((prev) => pruneChildState(prev, live));
  }, [sessions, stateKey, liveKeys]);

  useEffect(() => {
    onExpandedChildrenChange?.(expandedChildren);
  }, [expandedChildren, onExpandedChildrenChange]);

  const buildRows = (items: SessionItem[], scope: Scope = {}): VisibleSessionRow[] => {
    if (!items.length) return [];
    const childrenByParent = new Map<string, SessionItem[]>();
    const parentByKey = new Map<string, string>();
    const keys = new Set(items.map((item) => item.key));
    const topLevel: SessionItem[] = [];
    for (const item of items) {
      if (parseForkSessionSource(item.source)) {
        topLevel.push(item);
        continue;
      }
      const parentKey = String(item.parent_session_key || "").trim();
      if (!parentKey || !keys.has(parentKey)) {
        topLevel.push(item);
        continue;
      }
      const children = childrenByParent.get(parentKey) || [];
      children.push(item);
      childrenByParent.set(parentKey, children);
      parentByKey.set(item.key, parentKey);
    }
    const activeParents = new Set<string>();
    const scopeMatches = !selectedScope ||
      (selectedScope.rootId === scope.rootId && selectedScope.nodeId === scope.nodeId);
    if (selectedKey && scopeMatches) {
      activeParents.add(selectedKey);
      let parentKey = parentByKey.get(selectedKey) || "";
      while (parentKey) {
        activeParents.add(parentKey);
        parentKey = parentByKey.get(parentKey) || "";
      }
    }
    // 在跑的子会话连同它的祖先链一起可见 —— 侧栏不是在跑会话的唯一入口，
    // 父会话没被选中时也要能把活着的那条子代理露出来。
    for (const item of items) {
      if (!isRunningSession(item)) continue;
      activeParents.add(item.key);
      let parentKey = parentByKey.get(item.key) || "";
      while (parentKey && !activeParents.has(parentKey)) {
        activeParents.add(parentKey);
        parentKey = parentByKey.get(parentKey) || "";
      }
    }
    const rows: VisibleSessionRow[] = [];
    const append = (item: SessionItem, nested = false, stripNamePrefix?: string) => {
      rows.push({ type: "session", session: item, nested, stripNamePrefix });
      const children = childrenByParent.get(item.key) || [];
      const key = stateKey(item, scope);
      const expanded = !!expandedChildren[key];
      const active = activeParents.has(item.key);
      const visible = active ? (expanded ? children : collapsedChildSessions(children)) : [];
      // 真正的子会话不带任务前缀：它们以自己的名字挂在父会话下面。
      visible.forEach((child) => append(child, nested));
      const collapsedBase = collapsedBaseCount[key] ?? children.length;
      // 展开基线只用于「加载更多」的计数；隐藏数必须按实际条数兜底 ——
      // 折叠期间轮询补进来的新子会话不能因为基线没变就被少报。
      const hiddenCount = expanded
        ? 0
        : Math.max(0, Math.max(collapsedBase, children.length) - visible.length);
      if (active && (hiddenCount > 0 || expanded || childrenHasMore[key])) {
        rows.push({ type: "child-toggle", parent: item, loadedChildCount: collapsedBase, hiddenCount, expanded });
      }
    };
    // 任务分组（2026-11-20 用户决策）：同一 task_id 下的顶层会话先出分组头，
    // 成员紧随其后并缩进一级，且剥掉与分组头重复的共享前缀。分组只在有多条
    // 会话时出现，单条会话的展示不变。
    // 折叠（同日第二次决策）：默认收起，点分组头展开；选中的会话在组内时
    // 强制展开 —— 否则「点进一条会话」会让它自己从列表里消失。
    const membersByTask = new Map<string, SessionItem[]>();
    for (const item of topLevel) {
      const taskId = String(item.task_id || "").trim();
      if (!taskId) continue;
      const members = membersByTask.get(taskId) || [];
      members.push(item);
      membersByTask.set(taskId, members);
    }
    const hiddenTaskIds = new Set<string>();
    // 分组判据里的「这个任务一共几条会话」要跨刷新活着：列表每次重建都只是一个
    // 窗口（多项目侧栏每根 6 条），成员滑出去分组就凭空消失。后端
    // task_session_count 缺席时用这份进程内记忆兜底，作用域按 节点::项目 分开。
    const tallyScope = `${String(scope.nodeId || (topLevel[0] as any)?._nodeId || "").trim()}\u0000${String(scope.rootId || topLevel[0]?.root_id || "").trim()}`;
    rememberTaskSessions(tallyScope, topLevel);
    for (const entry of orderTaskGroupedRows(topLevel, (taskId) => taskSessionSeenCount(tallyScope, taskId))) {
      if (entry.kind === "group") {
        const members = membersByTask.get(entry.taskId) || [];
        const expanded = taskGroupShowsMembers(expandedTaskGroups, entry.taskId, members, selectedKey);
        if (!expanded) hiddenTaskIds.add(entry.taskId);
        rows.push({ type: "task-group", taskId: entry.taskId, label: entry.label, number: entry.number, expanded, members });
        continue;
      }
      if (entry.taskId && hiddenTaskIds.has(entry.taskId)) continue;
      append(entry.session, entry.nested, entry.groupLabel);
    }
    return rows;
  };

  const toggleTaskGroup = (taskId: string) => {
    setExpandedTaskGroups((prev) => ({ ...prev, [taskId]: !prev[taskId] }));
  };

  const loadChildren = async (parent: SessionItem, scope: Scope = {}, beforeTime?: string) => {
    const key = stateKey(parent, scope);
    if (!onLoadChildren || loadingChildren[key]) return;
    setLoadingChildren((prev) => ({ ...prev, [key]: true }));
    try {
      const result = await onLoadChildren(parent, beforeTime ? { beforeTime } : undefined);
      setChildrenHasMore((prev) => ({ ...prev, [key]: !!result?.hasMore }));
    } finally {
      setLoadingChildren((prev) => ({ ...prev, [key]: false }));
    }
  };

  const toggleChild = async (row: Extract<VisibleSessionRow, { type: "child-toggle" }>, items: SessionItem[], scope: Scope = {}) => {
    const key = stateKey(row.parent, scope);
    if (!row.expanded) {
      setCollapsedBaseCount((prev) => ({ ...prev, [key]: items.filter((item) => item.parent_session_key === row.parent.key).length }));
      setExpandedChildren((prev) => ({ ...prev, [key]: true }));
      await loadChildren(row.parent, scope);
      return;
    }
    if (childrenHasMore[key]) {
      const lastChild = items.filter((item) => item.parent_session_key === row.parent.key)
        .sort((left, right) => (Date.parse(left.updated_at || "") || 0) - (Date.parse(right.updated_at || "") || 0))[0];
      await loadChildren(row.parent, scope, lastChild?.updated_at);
      return;
    }
    setExpandedChildren((prev) => ({ ...prev, [key]: false }));
  };

  const collapseChild = (parent: SessionItem, scope: Scope = {}) => {
    const key = stateKey(parent, scope);
    setExpandedChildren((prev) => ({ ...prev, [key]: false }));
  };

  return { buildRows, toggleChild, collapseChild, toggleTaskGroup, expandedChildren, expandedTaskGroups, loadingChildren, childrenHasMore, stateKey, setExpandedChildren };
}
