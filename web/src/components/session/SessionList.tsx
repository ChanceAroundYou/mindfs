import React, { useEffect, useMemo, useRef, useState } from "react";
import { ViewHeader } from "../../layout/ViewHeader";
import { pruneChildState } from "../../shared/sessionTree";
import { useI18n } from "../../i18n/index";
import { SessionCardMemo } from "./SessionCard";
import { ChevronLeftIcon, iconButtonStyle } from "./sessionCardHelpers";
import {
  ArchiveHeaderButton,
  COLLAPSED_CHILD_SESSION_LIMIT,
  MAIN_SESSION_ICON_OFFSET,
  SUB_SESSION_ICON_OFFSET,
  ToggleRowButton,
  forkSessionDisplayName,
  isSessionSyncing,
  parseForkSessionSource,
} from "./sessionListShared";
import type { SessionItem, SessionListProps, VisibleSessionRow } from "./sessionListTypes";

export type {
  SessionType,
  SessionItem,
  SessionListProps,
  ProjectSessionGroup,
  ProjectSessionListProps,
} from "./sessionListTypes";
export { MultiProjectSessionList } from "./MultiProjectSessionList";

export function SessionList({
  sessions,
  selectedKey = "",
  headerAction,
  searchOpen = false,
  searchResultsMode = false,
  searchQuery = "",
  searchLoading = false,
  emptyText = "",
  onSearchToggle,
  onSearchBack,
  onSearchQueryChange,
  onSearchSubmit,
  onSearchBlur,
  syncingSessionKeys,
  onSelect,
  onSync,
  onPin,
  onRename,
  onDelete,
  onLoadChildren,
  onLoadOlder,
  loadingOlder = false,
  hasMore = false,
  onArchive,
  onOpenArchivePanel,
}: SessionListProps) {
  const { t } = useI18n();
  const effectiveEmptyText = emptyText || t("sessionList.empty");
  const searchInputRef = useRef<HTMLInputElement | null>(null);
  const searchBlurTimerRef = useRef<number | null>(null);
  const [expandedChildren, setExpandedChildren] = useState<Record<string, boolean>>({});
  const [loadingChildren, setLoadingChildren] = useState<Record<string, boolean>>({});
  const [childrenHasMore, setChildrenHasMore] = useState<Record<string, boolean>>({});
  // 折叠态的子会话基数：展开**之前**记下当时的已加载数，折叠态的「还有几个」用它算。
  // 展开会再拉一批子会话合并进 sessions，若按新的总数算，收起后的数字会大于展开前。
  const [collapsedBaseCount, setCollapsedBaseCount] = useState<Record<string, number>>({});
  // 展开态回收：会话被删除/归档后，其条目必须随之消失（见 pruneChildState 注释）
  useEffect(() => {
    const live = new Set(sessions.map((item) => item.key).filter(Boolean));
    setExpandedChildren((prev) => pruneChildState(prev, live));
    setLoadingChildren((prev) => pruneChildState(prev, live));
    setChildrenHasMore((prev) => pruneChildState(prev, live));
  }, [sessions]);
  const visibleSessions = useMemo(() => {
    if (searchResultsMode) {
      return sessions.map((session): VisibleSessionRow => ({ type: "session", session }));
    }
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
    if (selectedKey) {
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
      const active = activeParentKeys.has(item.key);
      const expanded = !!expandedChildren[item.key];
      const visibleChildren = active
        ? expanded
          ? children
          : children.slice(0, COLLAPSED_CHILD_SESSION_LIMIT)
        : [];
      for (const child of visibleChildren) {
        append(child);
      }
      // 折叠态显示的是「**展开那一刻**已加载的子会话里还剩几个」，不是当前总数：
      // 展开会再拉一批（每批最多 50 条）合并进 children，总数只增不减。用总数算，
      // 收起后的数字就会大于展开前的（用户报的 a=2 收起变 102 就是这么来的）。
      // 基数在**展开前**钉一次（见 handleChildToggle），来回切换多少次都稳定。
      const collapsedBase = collapsedBaseCount[item.key] ?? children.length;
      const hiddenCount = Math.max(0, collapsedBase - COLLAPSED_CHILD_SESSION_LIMIT);
      if (active && (hiddenCount > 0 || expanded || childrenHasMore[item.key])) {
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
  }, [collapsedBaseCount, childrenHasMore, expandedChildren, searchResultsMode, selectedKey, sessions]);
  const selectedParentKey = useMemo(() => {
    if (!selectedKey) return "";
    return sessions.find((item) => item.key === selectedKey)?.parent_session_key || "";
  }, [selectedKey, sessions]);
  const sessionByKey = useMemo(() => {
    const byKey = new Map<string, SessionItem>();
    for (const item of sessions) {
      byKey.set(item.key, item);
    }
    return byKey;
  }, [sessions]);

  useEffect(() => {
    if (!searchOpen) return;
    searchInputRef.current?.focus();
    searchInputRef.current?.select();
  }, [searchOpen]);

  useEffect(() => {
    return () => {
      if (searchBlurTimerRef.current) {
        window.clearTimeout(searchBlurTimerRef.current);
      }
    };
  }, []);

  const loadChildren = async (parent: SessionItem, beforeTime?: string) => {
    if (!onLoadChildren || loadingChildren[parent.key]) {
      return;
    }
    setLoadingChildren((prev) => ({ ...prev, [parent.key]: true }));
    try {
      const result = await onLoadChildren(parent, beforeTime ? { beforeTime } : undefined);
      setChildrenHasMore((prev) => ({ ...prev, [parent.key]: !!result?.hasMore }));
    } finally {
      setLoadingChildren((prev) => ({ ...prev, [parent.key]: false }));
    }
  };


  const handleChildToggle = async (row: Extract<VisibleSessionRow, { type: "child-toggle" }>) => {
    const parentKey = row.parent.key;
    if (!row.expanded) {
      // 折叠基数在**展开前**钉，不是收起时：展开会再拉一批子会话合并进 sessions，
      // 收起后若按新的总数算，「还有 N 个」必然大于展开前（用户报的 a=2 → b=102）。
      setCollapsedBaseCount((prev) => ({
        ...prev,
        [parentKey]: sessions.filter((item) => item.parent_session_key === parentKey).length,
      }));
      setExpandedChildren((prev) => ({ ...prev, [parentKey]: true }));
      await loadChildren(row.parent);
      return;
    }
    if (childrenHasMore[parentKey]) {
      const lastChild = sessions
        .filter((item) => item.parent_session_key === parentKey)
        .sort((left, right) => (Date.parse(left.updated_at || "") || 0) - (Date.parse(right.updated_at || "") || 0))[0];
      await loadChildren(row.parent, lastChild?.updated_at);
    } else {
      setExpandedChildren((prev) => ({ ...prev, [parentKey]: false }));
    }
  };

  return (
    <div
      style={{
        flex: 1,
        width: "100%",
        minWidth: 0,
        minHeight: 0,
        display: "flex",
        flexDirection: "column",
        background: "transparent",
      }}
    >
      {/* 统一的 Header 边栏 */}
      <ViewHeader
        data-onboarding="session-actions"
        padding={searchResultsMode ? "0 10px 0 4px" : "0 10px 0 2px"}
        showToggles={false}
        style={{ justifyContent: "space-between" }}
        innerStyle={{ justifyContent: "space-between" }}
      >
        {searchResultsMode ? (
          <button
            type="button"
            onClick={onSearchBack}
            aria-label={t("sessionList.back")}
            style={iconButtonStyle(false)}
          >
            <ChevronLeftIcon />
          </button>
        ) : (
          <div style={{ display: "inline-flex", alignItems: "center", gap: "4px" }}>
            {onSearchToggle ? (
              <button
                type="button"
                aria-label={searchOpen ? t("sessionList.closeSearch") : t("sessionList.search")}
                title={searchOpen ? t("sessionList.closeSearch") : t("sessionList.search")}
                onClick={onSearchToggle}
                style={{
                  width: "34px",
                  height: "34px",
                  minWidth: "34px",
                  border: "none",
                  borderRadius: "8px",
                  padding: 0,
                  background: "transparent",
                  color: searchOpen ? "var(--accent-color)" : "var(--text-secondary)",
                  display: "inline-flex",
                  alignItems: "center",
                  justifyContent: "center",
                  cursor: "pointer",
                  transition: "all 0.15s ease",
                }}
              >
                <svg xmlns="http://www.w3.org/2000/svg" width="17" height="17" viewBox="0 0 24 24" aria-hidden="true">
                  <path fill="currentColor" d="M15.5 14h-.79l-.28-.27A6.47 6.47 0 0 0 16 9.5A6.5 6.5 0 1 0 9.5 16c1.61 0 3.09-.59 4.23-1.57l.27.28v.79l5 4.99L20.49 19zm-6 0C7.01 14 5 11.99 5 9.5S7.01 5 9.5 5S14 7.01 14 9.5S11.99 14 9.5 14" />
                </svg>
              </button>
            ) : null}
            <ArchiveHeaderButton onOpen={onOpenArchivePanel} />
          </div>
        )}
        {headerAction ? (
          <div style={{ display: "inline-flex", alignItems: "center" }}>
            {headerAction}
          </div>
        ) : null}
      </ViewHeader>

      {searchOpen ? (
        <div
          style={{
            padding: "10px 12px",
            borderBottom: "1px solid var(--border-color)",
            flexShrink: 0,
            background: "var(--mindfs-topbar-bg, transparent)",
          }}
        >
          <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: "8px",
            border: "1px solid rgba(148,163,184,0.22)",
            borderRadius: "10px",
            padding: "0 10px",
            height: "34px",
            background: "transparent",
          }}
        >
            {searchLoading ? (
              <span
                aria-label={t("sessionList.searching")}
                style={{
                  width: "14px",
                  height: "14px",
                  borderRadius: "50%",
                  border: "1.5px solid rgba(100,116,139,0.45)",
                  borderTopColor: "var(--accent-color)",
                  display: "inline-block",
                  flexShrink: 0,
                  animation: "spin 0.8s linear infinite",
                }}
              />
            ) : (
              <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" aria-hidden="true" style={{ color: "var(--text-secondary)", flexShrink: 0 }}>
                <path fill="currentColor" d="M15.5 14h-.79l-.28-.27A6.47 6.47 0 0 0 16 9.5A6.5 6.5 0 1 0 9.5 16c1.61 0 3.09-.59 4.23-1.57l.27.28v.79l5 4.99L20.49 19zm-6 0C7.01 14 5 11.99 5 9.5S7.01 5 9.5 5S14 7.01 14 9.5S11.99 14 9.5 14" />
              </svg>
            )}
            <input
              ref={searchInputRef}
              type="text"
              value={searchQuery}
              placeholder={t("sessionList.searchPlaceholder")}
              onChange={(e) => onSearchQueryChange?.(e.target.value)}
              onBlur={() => {
                if (searchResultsMode) {
                  return;
                }
                if (searchBlurTimerRef.current) {
                  window.clearTimeout(searchBlurTimerRef.current);
                }
                searchBlurTimerRef.current = window.setTimeout(() => {
                  onSearchBlur?.();
                }, 120);
              }}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  onSearchSubmit?.();
                }
              }}
              style={{
                flex: 1,
                minWidth: 0,
                border: "none",
                outline: "none",
                background: "transparent",
                color: "var(--text-primary)",
                fontSize: "13px",
              }}
            />
            {searchQuery ? (
              <button
                type="button"
                onClick={() => onSearchQueryChange?.("")}
                onMouseDown={(e) => e.preventDefault()}
                aria-label={t("sessionList.clearSearch")}
                style={{
                  width: "18px",
                  height: "18px",
                  border: "none",
                  borderRadius: "999px",
                  padding: 0,
                  background: "rgba(148,163,184,0.18)",
                  color: "var(--text-secondary)",
                  display: "inline-flex",
                  alignItems: "center",
                  justifyContent: "center",
                  cursor: "pointer",
                  flexShrink: 0,
                }}
              >
                <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  <line x1="18" y1="6" x2="6" y2="18" />
                  <line x1="6" y1="6" x2="18" y2="18" />
                </svg>
              </button>
            ) : null}
          </div>
        </div>
      ) : null}

      <div style={{ flex: 1, minHeight: 0, overflow: "auto", padding: "8px" }}>
        {!sessions.length ? (
          emptyText ? (
            <div
              style={{
                fontSize: "12px",
                color: "var(--text-secondary)",
                minHeight: "100%",
                padding: "12px 18px",
                display: "flex",
                alignItems: "center",
                justifyContent: "center",
                textAlign: "center",
                lineHeight: 1.6,
              }}
            >
              {effectiveEmptyText}
            </div>
          ) : null
        ) : (
          <div style={{ display: "flex", flexDirection: "column", gap: "2px" }}>
            {visibleSessions.map((row) => {
              if (row.type === "child-toggle") {
                const loading = !!loadingChildren[row.parent.key];
                const hasMoreChildren = !!childrenHasMore[row.parent.key];
                const label = loading
                  ? t("sessionList.loading")
                  : row.expanded
                    ? hasMoreChildren
                      ? t("sessionList.loadMoreChildren")
                      : t("common.collapse")
                    : row.hiddenCount > 0
                      ? t("sessionList.remainingChildren", { count: row.hiddenCount })
                      : t("sessionList.expandChildren");
                return (
                  <ToggleRowButton
                    key={`children-toggle-${row.parent.key}`}
                    loading={loading}
                    label={label}
                    showExpandIcon={!loading && !row.expanded}
                    showCollapseIcon={!loading && row.expanded}
                    marginLeft={SUB_SESSION_ICON_OFFSET}
                    onClick={() => void handleChildToggle(row)}
                    onCollapse={() =>
                      // 整行点 = 收起。折叠基数已在展开那一刻钉好，这里不再动它 ——
                      // 收起时按当时的总数钉，钉进去的就是展开拉回来的新批次（b > a）。
                      setExpandedChildren((prev) => ({
                        ...prev,
                        [row.parent.key]: false,
                      }))
                    }
                  />
                );
              }
              const session = row.session;
              return (
                <SessionCardMemo
                  key={`${String((session as any)._nodeId || "")}::${session.key}`}
                  session={session}
                  sessionByKey={sessionByKey}
                  selected={session.key === selectedKey}
                  parentHighlighted={!!selectedParentKey && session.key === selectedParentKey}
                  highlightQuery={searchResultsMode ? searchQuery : ""}
                  syncing={isSessionSyncing(session, syncingSessionKeys)}
                  onSelect={onSelect}
                  onSync={onSync}
                  onPin={onPin}
                  onRename={onRename}
                  onDelete={onDelete}
                  onArchive={onArchive}
                />
              );
            })}
            {hasMore ? (
              <button
                type="button"
                onClick={onLoadOlder}
                disabled={loadingOlder}
                style={{
                  marginTop: "8px",
                  border: "1px solid var(--border-color)",
                  background: "transparent",
                  color: "var(--text-secondary)",
                  borderRadius: "8px",
                  padding: "8px 10px",
                  cursor: loadingOlder ? "default" : "pointer",
                  fontSize: "12px",
                }}
              >
                {loadingOlder ? t("sessionList.loading") : t("sessionList.loadMore")}
              </button>
            ) : null}
          </div>
        )}
      </div>
      <style>{`
        @keyframes mindfs-bound-pulse {
          0%, 100% { opacity: 1; box-shadow: 0 0 0 1.5px rgba(37,99,235,0.14); }
          50% { opacity: 0.18; box-shadow: 0 0 0 4px rgba(37,99,235,0.08); }
        }
        @keyframes spin {
          from { transform: rotate(0deg); }
          to { transform: rotate(360deg); }
        }
      `}</style>
    </div>
  );
}
