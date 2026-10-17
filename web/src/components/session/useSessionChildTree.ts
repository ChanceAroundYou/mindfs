import { useEffect, useState } from "react";
import { pruneChildState } from "../../shared/sessionTree";
import { COLLAPSED_CHILD_SESSION_LIMIT, parseForkSessionSource } from "./sessionListShared";
import type { SessionItem, VisibleSessionRow } from "./sessionListTypes";

type Scope = { rootId?: string; nodeId?: string };
type LoadChildren = (parent: SessionItem, options?: { beforeTime?: string }) => Promise<{ hasMore?: boolean } | void> | { hasMore?: boolean } | void;

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
    const rows: VisibleSessionRow[] = [];
    const append = (item: SessionItem) => {
      rows.push({ type: "session", session: item });
      const children = childrenByParent.get(item.key) || [];
      const key = stateKey(item, scope);
      const expanded = !!expandedChildren[key];
      const active = activeParents.has(item.key);
      const visible = active ? (expanded ? children : children.slice(0, COLLAPSED_CHILD_SESSION_LIMIT)) : [];
      visible.forEach(append);
      const collapsedBase = collapsedBaseCount[key] ?? children.length;
      const hiddenCount = Math.max(0, collapsedBase - COLLAPSED_CHILD_SESSION_LIMIT);
      if (active && (hiddenCount > 0 || expanded || childrenHasMore[key])) {
        rows.push({ type: "child-toggle", parent: item, loadedChildCount: collapsedBase, hiddenCount, expanded });
      }
    };
    topLevel.forEach(append);
    return rows;
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

  return { buildRows, toggleChild, collapseChild, expandedChildren, loadingChildren, childrenHasMore, stateKey, setExpandedChildren };
}
