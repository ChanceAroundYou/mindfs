import React, { memo, useEffect, useMemo, useRef, useState } from "react";
import { AgentIcon } from "./AgentIcon";
import { ModeIcon } from "./ModeIcon";
import { NodeBadgeHeader } from "./NodeBadgeHeader";
import { getNodes, PALETTE, DEFAULT_NODE_COLOR } from "../services/nodeRegistry";
import { hexToRgbaApp } from "../app/taskIcons";
import { resolveGroupColor } from "../services/sessionGroupDisplay";
import { scopeKey } from "../services/scope";
import { pruneChildState } from "../services/sessionTree";
import { fetchSessionProjectPins, updateSessionProjectPins } from "../services/preferences";
import { useI18n, type Locale } from "../i18n";
import { type DirectorySortMode, sortDirectoryEntries } from "../services/directorySort";


export type SessionType = "chat" | "plugin" | "command";

export type SessionItem = {
  key: string;
  session_key: string;
  root_id?: string;
  type?: SessionType;
  parent_session_key?: string;
  parent_tool_call_id?: string;
  source?: string;
  task_id?: string;
  agent?: string;
  shell?: string;
  name?: string;
  created_at?: string;
  updated_at?: string;
  pinned_at?: string | null;
  archived_at?: string | null;
  closed_at?: string;
  pending?: boolean;
  related_files?: Array<{ path: string }>;
  search_seq?: number;
  search_snippet?: string;
  search_match_type?: "name" | "user" | "reply";
};

export type SessionListProps = {
  sessions: SessionItem[];
  selectedKey?: string;
  headerAction?: React.ReactNode;
  searchOpen?: boolean;
  searchResultsMode?: boolean;
  searchQuery?: string;
  searchLoading?: boolean;
  emptyText?: React.ReactNode;
  onSearchToggle?: () => void;
  onSearchBack?: () => void;
  onSearchQueryChange?: (query: string) => void;
  onSearchSubmit?: () => void;
  onSearchBlur?: () => void;
  syncingSessionKeys?: Set<string>;
  onSelect?: (session: SessionItem) => void;
  onSync?: (session: SessionItem) => Promise<void> | void;
  onPin?: (session: SessionItem, pinned: boolean) => Promise<boolean> | boolean;
  onRename?: (session: SessionItem, nextName: string) => Promise<boolean> | boolean;
  onDelete?: (session: SessionItem) => void;
  onArchive?: (session: SessionItem, archived: boolean) => Promise<boolean> | boolean;
  onLoadChildren?: (
    session: SessionItem,
    options?: { beforeTime?: string },
  ) => Promise<{ hasMore?: boolean } | void> | { hasMore?: boolean } | void;
  onLoadOlder?: () => void;
  loadingOlder?: boolean;
  hasMore?: boolean;
  // 归档入口行：只负责把「已归档对话」面板叫起来，内容在面板里，不在本列表。
  onOpenArchivePanel?: () => void;
};

const COLLAPSED_CHILD_SESSION_LIMIT = 3;
const MULTI_PROJECT_VISIBLE_LIMIT = 6;
const MAIN_SESSION_ICON_OFFSET = "2px";
const SUB_SESSION_ICON_OFFSET = "0px";
const PINNED_PROJECTS_STORAGE_KEY = "mindfs-pinned-session-projects";

type VisibleSessionRow =
  | { type: "session"; session: SessionItem }
  | {
      type: "child-toggle";
      parent: SessionItem;
      loadedChildCount: number;
      hiddenCount: number;
      expanded: boolean;
    };

export type ProjectSessionGroup = {
  rootId: string;
  rootName: string;
  latestSessionTime?: string;
  sessions: SessionItem[];
  totalCount: number;
};

export type ProjectSessionListProps = {
  groups: ProjectSessionGroup[];
  selectedKey?: string;
  selectedRootId?: string;
  selectedNodeId?: string;
  headerAction?: React.ReactNode;
  loading?: boolean;
  emptyText?: React.ReactNode;
  projectSortMode?: DirectorySortMode;
  syncingSessionKeys?: Set<string>;
  onSearchToggle?: () => void;
  onSelect?: (session: SessionItem) => void;
  onSync?: (session: SessionItem) => Promise<void> | void;
  onPin?: (session: SessionItem, pinned: boolean) => Promise<boolean> | boolean;
  onRename?: (session: SessionItem, nextName: string) => Promise<boolean> | boolean;
  onDelete?: (session: SessionItem) => void;
  onArchive?: (session: SessionItem, archived: boolean) => Promise<boolean> | boolean;
  onLoadMoreProject?: (group: ProjectSessionGroup) => Promise<void> | void;
  onLoadChildren?: (
    session: SessionItem,
    options?: { beforeTime?: string },
  ) => Promise<{ hasMore?: boolean } | void> | { hasMore?: boolean } | void;
  // 归档入口行：点它弹出「已归档对话」视图。
  onOpenArchivePanel?: () => void;
  // 归档视图自带标题栏，就不要再画一遍空的操作栏（那排是搜索/导入入口，视图里用不上）。
  hideHeader?: boolean;
};

function ToggleRowButton({
  label,
  loading,
  marginLeft,
  showExpandIcon = false,
  showCollapseIcon = false,
  onClick,
  onCollapse,
}: {
  label: string;
  loading?: boolean;
  marginLeft: string;
  showExpandIcon?: boolean;
  showCollapseIcon?: boolean;
  onClick: () => void;
  onCollapse?: () => void;
}) {
  const { t } = useI18n();
  const icon = (rotate = false) => (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width="1em"
      height="1em"
      viewBox="0 0 16 16"
      aria-hidden="true"
      style={{
        width: "13px",
        height: "13px",
        flexShrink: 0,
        transform: rotate ? "rotate(180deg)" : "none",
      }}
    >
      <path d="M0 0h16v16H0z" fill="none" />
      <path fill="currentColor" d="M12.146 7.146a.5.5 0 0 1 .708.708l-4.5 4.5a.5.5 0 0 1-.708 0l-4.5-4.5a.5.5 0 1 1 .708-.708L8 11.293zm0-4a.5.5 0 0 1 .708.708l-4.5 4.5a.5.5 0 0 1-.708 0l-4.5-4.5a.5.5 0 1 1 .708-.708L8 7.293z" />
    </svg>
  );

  // 展开态这一行有两个动作：拉下一批子会话 / 全部收起。所以收起图标是**整行
  // 可点**的 —— 它就长在「展开 N 条」这一行里，点这一行任何位置都该能收回去。
  // 行内不再套一个可点的子元素：嵌套 button 是非法 HTML，浏览器会把子 button
  // 从父 button 里拆出来，事件到不了父级，再叠上 stopPropagation，那个收起图标
  // 就永远按不动（只能继续展开）。所以收起 = 整行换 onCollapse，不是第二个按钮。
  const collapsed = showCollapseIcon && !!onCollapse;
  return (
    <button
      type="button"
      disabled={loading}
      onClick={collapsed ? onCollapse : onClick}
      title={collapsed ? t("common.collapse") : undefined}
      style={{
        marginLeft,
        marginTop: "-2px",
        border: "none",
        background: "transparent",
        color: "var(--text-primary)",
        borderRadius: 0,
        padding: 0,
        minHeight: "13px",
        width: `calc(100% - ${marginLeft})`,
        boxSizing: "border-box",
        cursor: loading ? "default" : "pointer",
        fontSize: "11px",
        lineHeight: 1.1,
        textAlign: "center",
        opacity: loading ? 0.6 : 1,
        display: "inline-flex",
        alignItems: "center",
        justifyContent: "center",
      }}
    >
      <span style={{ display: "inline-flex", alignItems: "center", justifyContent: "center", gap: "6px", minWidth: 0, flexShrink: 1 }}>
        <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{label}</span>
        {showExpandIcon ? icon(false) : null}
        {showCollapseIcon ? icon(true) : null}
      </span>
    </button>
  );
}


function PinIcon({ pinned }: { pinned: boolean }) {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" width="1em" height="1em" viewBox="0 0 24 24" aria-hidden="true">
      <path d="M0 0h24v24H0z" fill="none" />
      <path
        fill="currentColor"
        fillRule="evenodd"
        d="m16.219 4.838l2.964 2.967c2.012 2.014 3.018 3.021 2.784 4.107c-.235 1.085-1.567 1.585-4.23 2.586l-1.845.693c-.713.268-1.07.402-1.345.64q-.181.158-.322.352c-.212.297-.313.664-.515 1.4c-.46 1.672-.69 2.508-1.239 2.821c-.23.132-.492.2-.758.2c-.63 0-1.243-.614-2.469-1.84l-1.466-1.468l-1.079-1.08L5.285 14.8c-1.218-1.219-1.827-1.828-1.83-2.455a1.53 1.53 0 0 1 .203-.773c.313-.543 1.143-.772 2.803-1.23c.737-.203 1.105-.304 1.402-.517q.199-.144.36-.332c.236-.278.368-.637.63-1.355l.669-1.823c.987-2.693 1.48-4.04 2.568-4.28s2.102.774 4.129 2.803"
        clipRule="evenodd"
        opacity={pinned ? 1 : 0.5}
      />
      <path fill="currentColor" d="m3.302 21.776l4.476-4.48l-1.079-1.08l-4.476 4.48a.764.764 0 0 0 1.08 1.08" />
    </svg>
  );
}

function ArchiveIcon() {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width="1em"
      height="1em"
      viewBox="0 0 24 24"
      aria-hidden="true"
    >
      <path d="M0 0h24v24H0z" fill="none" />
      <path
        fill="currentColor"
        d="M3 4.5A1.5 1.5 0 0 1 4.5 3h15A1.5 1.5 0 0 1 21 4.5V6H3zm-.5 3A1.5 1.5 0 0 0 1 8.5V19a2 2 0 0 0 2 2h18a2 2 0 0 0 2-2V8.5a1.5 1.5 0 0 0-1.5-1.5zm5 3.5a1 1 0 0 0-1 1v7a1 1 0 0 0 1 1h10a1 1 0 0 0 1-1v-7a1 1 0 0 0-1-1zm2.5 3h5v2h-5z"
      />
    </svg>
  );
}

/**
 * 顶栏归档入口：只留图标，尺寸与旁边的搜索按钮一致（34×34）。
 * 2026-09-29 用户要求从列表末尾挪到搜索图标右边 —— 原来挂在列表里，不管
 * 吸不吸底都跟着项目列表跑，得滚动才看得到。两个列表组件（单项目 / 多项目）
 * 各有一个顶栏且互斥显示，抽成组件给两处共用。
 */
function ArchiveHeaderButton({ onOpen }: { onOpen?: () => void }) {
  const { t } = useI18n();
  if (!onOpen) return null;
  return (
    <div style={{ display: "inline-flex", alignItems: "center", gap: "4px" }}>
      <button
        type="button"
        data-archive-entry="open"
        aria-label={t("sessionList.archive")}
        title={t("sessionList.archive")}
        onClick={onOpen}
        style={{
          width: "34px",
          height: "34px",
          minWidth: "34px",
          border: "none",
          borderRadius: "8px",
          padding: 0,
          background: "transparent",
          color: "var(--text-secondary)",
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
          cursor: "pointer",
          transition: "all 0.15s ease",
        }}
      >
        <span style={{ fontSize: "15px", lineHeight: 1, display: "inline-flex" }}>
          <ArchiveIcon />
        </span>
      </button>
    </div>
  );
}

function shellBadgeLabel(shell?: string): string {
  const normalized = String(shell || "").trim().replace(/\\/g, "/");
  const base = (normalized.split("/").filter(Boolean).pop() || normalized || "sh").toLowerCase();
  if (base === "powershell.exe") return "ps";
  if (base === "pwsh.exe") return "pwsh";
  if (base === "cmd.exe") return "cmd";
  if (base.endsWith(".exe")) return base.slice(0, -4);
  return base;
}

type ForkSessionSource = {
  sessionKey: string;
  seq: number;
};

function parseForkSessionSource(source?: string): ForkSessionSource | null {
  const value = String(source || "").trim();
  if (!value) return null;
  try {
    const parsed = JSON.parse(value) as {
      type?: unknown;
      session_key?: unknown;
      seq?: unknown;
    };
    if (parsed?.type !== "fork") return null;
    return {
      sessionKey: String(parsed.session_key || "").trim(),
      seq: Number(parsed.seq || 0),
    };
  } catch {
    if (!value.includes('"type":"fork"') && !value.includes('"type": "fork"')) {
      return null;
    }
    return { sessionKey: "", seq: 0 };
  }
}

function isSessionSyncing(
  session: SessionItem,
  syncingSessionKeys?: Set<string>,
): boolean {
  if (!syncingSessionKeys || syncingSessionKeys.size === 0) {
    return false;
  }
  const key = session.key || session.session_key || "";
  if (!key) {
    return false;
  }
  const rootId = session.root_id || "";
  const nodeId = String((session as any)?._nodeId || "").trim();
  return (
    syncingSessionKeys.has(key) ||
    (!!rootId && syncingSessionKeys.has(`${rootId}::${key}`)) ||
    (!!rootId && !!nodeId && syncingSessionKeys.has(`${nodeId}::${rootId}::${key}`))
  );
}

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
      <div
        data-onboarding="session-actions"
        style={{
          height: "36px",
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          padding: searchResultsMode ? "0 10px 0 4px" : "0 10px 0 2px",
          borderBottom: "1px solid var(--border-color)",
          background: "var(--mindfs-topbar-bg, transparent)",
          flexShrink: 0,
          boxSizing: "border-box",
        }}
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
          </div>
        )}
        <ArchiveHeaderButton onOpen={onOpenArchivePanel} />
        {headerAction ? (
          <div style={{ display: "inline-flex", alignItems: "center" }}>
            {headerAction}
          </div>
        ) : null}
      </div>

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

// 项目置顶的本地缓存：右栏每次打开都重挂载本组件，同步读 localStorage 起始态，
// 避免「先按无置顶渲染、偏好到达后重排」的闪动；服务端偏好仍是事实源
function readLocalProjectPins(): Record<string, number> {
  if (typeof window === "undefined") return {};
  try {
    const parsed = JSON.parse(window.localStorage.getItem(PINNED_PROJECTS_STORAGE_KEY) || "{}");
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
      const next: Record<string, number> = {};
      for (const [key, value] of Object.entries(parsed)) {
        const timestamp = Number(value);
        if (key && Number.isFinite(timestamp) && timestamp > 0) next[key] = timestamp;
      }
      return next;
    }
  } catch {}
  return {};
}

function writeLocalProjectPins(pins: Record<string, number>): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(PINNED_PROJECTS_STORAGE_KEY, JSON.stringify(pins));
  } catch {}
}

export function MultiProjectSessionList({
  groups,
  selectedKey = "",
  selectedRootId = "",
  headerAction,
  loading = false,
  emptyText = "",
  projectSortMode = "name-asc",
  syncingSessionKeys,
  onSearchToggle,
  onSelect,
  onSync,
  onPin,
  onRename,
  onDelete,
  selectedNodeId = "",
  onLoadMoreProject,
  onLoadChildren,
  onArchive,
  onOpenArchivePanel,
  hideHeader = false,
}: ProjectSessionListProps) {
  const { t } = useI18n();
  const effectiveEmptyText = emptyText || t("sessionList.empty");
  const [expandedProjects, setExpandedProjects] = useState<Record<string, boolean>>({});
  const [loadingProjects, setLoadingProjects] = useState<Record<string, boolean>>({});
  const [expandedChildren, setExpandedChildren] = useState<Record<string, boolean>>({});
  const [loadingChildren, setLoadingChildren] = useState<Record<string, boolean>>({});
  const [childrenHasMore, setChildrenHasMore] = useState<Record<string, boolean>>({});
  // 折叠态的子会话基数（键与 expandedChildren 同形）：展开**之前**记下当时的已加载数，
  // 折叠态按它算，不受展开时陆续拉回来的批次影响。
  const [collapsedBaseCount, setCollapsedBaseCount] = useState<Record<string, number>>({});
  const [pinnedProjects, setPinnedProjects] = useState<Record<string, number>>(readLocalProjectPins);
  // 展开态回收：分组里的会话消失（删除/归档/切节点）后，其条目必须随之消失
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
  // 项目置顶持久化到服务端偏好（跨设备/清缓存不丢）；本地缓存先出帧，服务端返回后校正并回写
  useEffect(() => {
    let cancelled = false;
    fetchSessionProjectPins()
      .then((pins) => {
        if (cancelled) return;
        setPinnedProjects(pins);
        writeLocalProjectPins(pins);
      })
      .catch(() => {
        if (!cancelled) setPinnedProjects(readLocalProjectPins());
      });
    return () => {
      cancelled = true;
    };
  }, []);
  const groupScopeKey = (group: ProjectSessionGroup) =>
    scopeKey(String((group as any)?._nodeId || "").trim(), group.rootId);
  // 右侧多项目列表：与左侧 FileTree 保持一致的分层排序
  // 层级：节点时序(按 getNodes() 添加顺序) > 同节点内项目置顶 > 同节点内项目设置排序(默认 name-asc)
  // 项目置顶不跨节点越位，修复 hbsn 跑到其他节点上方的问题；项目内会话由 sessionListMerge 负责置顶在前+时间降序
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
    // 同节点内的项目按 DirectorySort 规则排；复用左侧同款比较，避免两处分叉
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
    const next = { ...pinnedProjects };
    if (next[key]) {
      delete next[key];
    } else {
      next[key] = Date.now();
    }
    setPinnedProjects(next);
    writeLocalProjectPins(next);
    // 置顶状态写服务端偏好；失败只回退本地 UI，下次拉取会纠正
    void updateSessionProjectPins(next).catch(() => {});
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

  // fallbackRootId / fallbackNodeId 必须一起传：读侧（render / handleChildToggle）
  // 用的是三参数版本，这里少传就会写到另一个键上 —— childrenHasMore 写进去读不回来，
  // 表现是「还有下一批」永远不出现、点收起也只能收不能继续加载。
  const loadChildren = async (
    parent: SessionItem,
    beforeTime?: string,
    fallbackRootId = "",
    fallbackNodeId = "",
  ) => {
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
      // 同单项目列表：折叠基数取「展开那一刻」的已加载数，展开后陆续拉回的批次
      // 不参与计数，所以收起后的「还有几个」不会大于展开前。
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
      // 折叠基数在**展开前**钉（不是收起时）：展开会再拉一批合并进 group.sessions，
      // 收起后按新的总数算，「还有 N 个」必然大于展开前。
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

  // 两端皆空视为节点信息未就绪：避免空串互等把全部分组误展开
  const groupIsCurrentNode = (group: ProjectSessionGroup) => {
    const groupNodeId = String((group as any)?._nodeId || "").trim();
    const selectedNid = String(selectedNodeId || "").trim();
    return !!groupNodeId && !!selectedNid && groupNodeId === selectedNid;
  };

  // 默认折叠态：当前节点 + 被图钉置顶的项目展开
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

  return (
    <div style={{ flex: 1, width: "100%", minWidth: 0, minHeight: 0, display: "flex", flexDirection: "column", background: "transparent" }}>
      <div
        data-onboarding="session-actions"
        style={{
          height: "36px",
          display: hideHeader ? "none" : "flex",
          alignItems: "center",
          justifyContent: "space-between",
          padding: "0 10px 0 2px",
          borderBottom: "1px solid var(--border-color)",
          background: "var(--mindfs-topbar-bg, transparent)",
          flexShrink: 0,
          boxSizing: "border-box",
        }}
      >
        <div style={{ display: "inline-flex", alignItems: "center", gap: "4px" }}>
          {onSearchToggle ? (
            <button
              type="button"
              aria-label={t("sessionList.searchCurrentProject")}
              title={t("sessionList.searchCurrentProject")}
              onClick={onSearchToggle}
              style={{
                width: "34px",
                height: "34px",
                minWidth: "34px",
                border: "none",
                borderRadius: "8px",
                padding: 0,
                background: "transparent",
                color: "var(--text-secondary)",
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
        </div>
        <ArchiveHeaderButton onOpen={onOpenArchivePanel} />
        {headerAction ? <div style={{ display: "inline-flex", alignItems: "center" }}>{headerAction}</div> : null}
      </div>
      <div style={{ flex: 1, minHeight: 0, overflow: "auto", padding: "8px" }}>
        {loading && groups.length === 0 ? (
          <div style={{ fontSize: "12px", color: "var(--text-secondary)", padding: "18px", textAlign: "center" }}>{t("sessionList.loading")}</div>
        ) : groups.length === 0 ? (
          <div style={{ fontSize: "12px", color: "var(--text-secondary)", minHeight: "100%", padding: "12px 18px", display: "flex", alignItems: "center", justifyContent: "center", textAlign: "center", lineHeight: 1.6 }}>
            {effectiveEmptyText}
          </div>
        ) : (
          <div style={{ display: "flex", flexDirection: "column", gap: "9px" }}>
            {orderedGroups.map((group) => {
              const groupKey = groupScopeKey(group);
              const groupNodeId = String((group as any)?._nodeId || "").trim();
              const expanded = expandedProjects[groupKey] ?? groupDefaultExpanded(group);
              const pinned = !!pinnedProjects[groupKey];
              const groupColor = String((group as any)._nodeColor || resolveGroupColor(group as any, {}, getNodes() as any) || PALETTE[0]);
              const topLevelSessions = topLevelSessionsForGroup(group.sessions);
              const sessions = expanded ? group.sessions : [];
              const rows = buildRows(sessions, group.rootId, groupNodeId);
              const remaining = Math.max(0, group.totalCount - topLevelSessions.length);
              const projectLoading = !!loadingProjects[groupKey];
              return (
                <section key={`${(group as any)._nodeId || ""}::${group.rootId}`} style={{ minWidth: 0 }}>
                  <div style={{ position: "relative" }}>
                    <NodeBadgeHeader
                      color={groupColor}
                      label={group.rootName || group.rootId}
                      collapsed={!expanded}
                      onClick={() => void handleProjectHeaderToggle(group)}
                    />
                    <button
                      type="button"
                      aria-label={pinned ? t("sessionList.unpinProject") : t("sessionList.pinProject")}
                      title={pinned ? t("sessionList.unpin") : t("sessionList.pin")}
                      onClick={() => togglePinnedProject(group)}
                      style={{
                        position: "absolute",
                        right: 0,
                        top: "50%",
                        transform: "translateY(-50%)",
                        width: "20px",
                        height: "20px",
                        border: "none",
                        borderRadius: "6px",
                        padding: 0,
                        background: "transparent",
                        color: pinned ? groupColor : "var(--text-secondary)",
                        display: "inline-flex",
                        alignItems: "center",
                        justifyContent: "center",
                        cursor: "pointer",
                        opacity: pinned ? 1 : 0.72,
                      }}
                      onMouseEnter={(e) => {
                        e.currentTarget.style.background = "rgba(0,0,0,0.05)";
                        e.currentTarget.style.opacity = "1";
                      }}
                      onMouseLeave={(e) => {
                        e.currentTarget.style.background = "transparent";
                        e.currentTarget.style.opacity = pinned ? "1" : "0.72";
                      }}
                    >
                      <PinIcon pinned={pinned} />
                    </button>
                  </div>
                  <div style={{ display: "flex", flexDirection: "column", gap: "2px", paddingTop: 0 }}>
                    {rows.map((row) => {
                      if (row.type === "child-toggle") {
                        // nodeId 必须一起传：写键的地方（handleChildToggle / onCollapse）
                        // 传的是三参数版本，这里少传一个就会算出另一个键，于是读到
                        // 未定义 —— 表现正是「行显示已展开、点它却毫无反应」。
                        const stateKey = childStateKey(row.parent, group.rootId, groupNodeId);
                        const loadingChild = !!loadingChildren[stateKey];
                        const hasMoreChildren = !!childrenHasMore[stateKey];
                        const label = loadingChild
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
                            key={`children-toggle-${group.rootId}-${row.parent.key}`}
                            loading={loadingChild}
                            label={label}
                            showExpandIcon={!loadingChild && !row.expanded}
                            showCollapseIcon={!loadingChild && row.expanded}
                            marginLeft={SUB_SESSION_ICON_OFFSET}
                            onClick={() => void handleChildToggle(row, group.sessions, group.rootId, groupNodeId)}
                            onCollapse={() =>
                              // 整行点 = 收起；折叠基数已在展开那一刻钉好，这里不动。
                              setExpandedChildren((prev) => ({
                                ...prev,
                                [childStateKey(row.parent, group.rootId, groupNodeId)]: false,
                              }))
                            }
                          />
                        );
                      }
                      const session = row.session;
                      const sessionRoot = session.root_id || group.rootId;
                      const groupColor = String((group as any)._nodeColor || "").trim();
                      return (
                        <SessionCardMemo
                          key={`${String((group as any)._nodeId || "")}::${sessionRoot}:${session.key}`}
                          session={{ ...session, root_id: sessionRoot }}
                          nodeColor={groupColor}
                          sessionByKey={sessionByKey}
                          selected={session.key === selectedKey && sessionRoot === selectedRootId && String((group as any)._nodeId || "") === String(selectedNodeId || "")}
                          parentHighlighted={false}
                          highlightQuery=""
                          syncing={isSessionSyncing({ ...session, root_id: sessionRoot }, syncingSessionKeys)}
                          onSelect={onSelect}
                          onSync={onSync}
                          onPin={onPin}
                          onRename={onRename}
                          onDelete={onDelete}
                          onArchive={onArchive}
                        />
                      );
                    })}
                    {expanded && group.totalCount > MULTI_PROJECT_VISIBLE_LIMIT ? (
                      <ToggleRowButton
                        loading={projectLoading}
                        label={
                          projectLoading
                            ? t("sessionList.loading")
                            : expanded
                              ? remaining > 0
                                ? t("sessionList.remainingSessions", { count: remaining })
                                : t("common.collapse")
                              : t("sessionList.remainingSessions", { count: Math.max(0, group.totalCount - MULTI_PROJECT_VISIBLE_LIMIT) })
                        }
                        showExpandIcon={!projectLoading && !expanded}
                        showCollapseIcon={!projectLoading && expanded}
                        marginLeft={MAIN_SESSION_ICON_OFFSET}
                        onClick={() => void handleProjectToggle(group)}
                        onCollapse={() => handleProjectCollapse(group)}
                      />
                    ) : null}
                  </div>
                </section>
              );
            })}
          </div>
        )}
        {/* 入口行在滚动容器内、但在「有无分组」这个分支之外：分组多到装不下一屏时它跟着
            内容被顶下去（要滚动才看得到），一个分组都没有时它也还在 —— 放进上面的分支
            里会连空态一起消失，归档就再也进不去了。 */}
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

function SessionCard({
  session,
  sessionByKey,
  selected,
  nodeColor,
  parentHighlighted,
  highlightQuery,
  syncing = false,
  onSelect,
  onSync,
  onPin,
  onRename,
  onDelete,
  onArchive,
}: {
  session: SessionItem;
  sessionByKey: Map<string, SessionItem>;
  selected: boolean;
  nodeColor?: string;
  parentHighlighted?: boolean;
  highlightQuery?: string;
  syncing?: boolean;
  onSelect?: (session: SessionItem) => void;
  onSync?: (session: SessionItem) => Promise<void> | void;
  onPin?: (session: SessionItem, pinned: boolean) => Promise<boolean> | boolean;
  onRename?: (session: SessionItem, nextName: string) => Promise<boolean> | boolean;
  onDelete?: (session: SessionItem) => void;
  onArchive?: (session: SessionItem, archived: boolean) => Promise<boolean> | boolean;
}) {
  const { locale, t } = useI18n();
  const isClosed = !!session.closed_at;
  const isPinned = !!session.pinned_at;
  const isArchived = !!session.archived_at;
  const isSubagent = !!session.parent_session_key && !parseForkSessionSource(session.source);
  const storedName = session.name || `Session ${session.key.slice(0, 8)}`;
  const displayName = storedName;
  const snippet = (session.search_snippet || "").trim();
  const isSearchResult = !!session.search_match_type;
  const [menuOpen, setMenuOpen] = useState(false);
  const [editing, setEditing] = useState(false);
  const [draftName, setDraftName] = useState(storedName);
  const [saving, setSaving] = useState(false);
  const effectiveNodeColor = String(
    nodeColor ||
    (session as any)?._nodeColor ||
    resolveGroupColor(
      { rootId: String((session as any)?.root_id || ""), _nodeId: String((session as any)?._nodeId || "") },
      {},
      getNodes() as any,
    ) ||
    "",
  ).trim();
  const rowBackground = selected
    ? "var(--node-row-selected-bg)"
    : parentHighlighted
      ? "rgba(0,0,0,0.03)"
      : "transparent";
  const menuRef = useRef<HTMLDivElement | null>(null);
  const inputRef = useRef<HTMLInputElement | null>(null);
  const composingRef = useRef(false);
  const submittingRef = useRef(false);

  useEffect(() => {
    if (!editing) {
      setDraftName(storedName);
    }
  }, [storedName, editing]);

  useEffect(() => {
    if (!menuOpen) return;
    const handlePointerDown = (event: MouseEvent) => {
      if (!menuRef.current?.contains(event.target as Node)) {
        setMenuOpen(false);
      }
    };
    document.addEventListener("mousedown", handlePointerDown);
    return () => document.removeEventListener("mousedown", handlePointerDown);
  }, [menuOpen]);

  useEffect(() => {
    if (!editing) return;
    inputRef.current?.focus();
    inputRef.current?.select();
  }, [editing]);

  useEffect(() => {
    if (!editing) return;
    const input = inputRef.current;
    if (!input) return;
    const atEnd = input.selectionStart === input.value.length;
    if (atEnd) {
      input.scrollLeft = input.scrollWidth;
    }
  }, [draftName, editing]);

  const cancelEditing = () => {
    setEditing(false);
    setSaving(false);
    setDraftName(storedName);
  };

  const submitRename = async () => {
    if (submittingRef.current) return;
    const trimmed = draftName.trim();
    if (!trimmed) {
      cancelEditing();
      return;
    }
    if (trimmed === storedName.trim()) {
      cancelEditing();
      return;
    }
    if (!onRename) {
      cancelEditing();
      return;
    }
    submittingRef.current = true;
    setSaving(true);
    try {
      const ok = await onRename(session, trimmed);
      if (ok === false) {
        inputRef.current?.focus();
        inputRef.current?.select();
        return;
      }
      setEditing(false);
    } finally {
      submittingRef.current = false;
      setSaving(false);
    }
  };

  return (
    <div
      style={{
        width: "100%",
        display: "flex",
        alignItems: "center",
        gap: 0,
        padding: "2px 0",
        borderRadius: "8px",
        position: "relative",
        paddingLeft: isSubagent ? "10px" : 0,
      }}
    >
      <div
        style={{
          textAlign: "left" as const,
          padding: editing ? "7px 0 7px 2px" : "7px 4px 7px 2px",
          borderRadius: "8px",
          border: "1px solid transparent",
          background: rowBackground,
          flex: 1,
          minWidth: 0,
          display: "flex",
          alignItems: "center",
          gap: isSubagent ? "3px" : "6px",
          transition: "all 0.15s ease",
        }}
      >
        {!isSearchResult ? (
          <span
            style={{
              position: "relative",
              width: "18px",
              height: "18px",
              flexShrink: 0,
              display: "inline-flex",
              alignItems: "center",
              justifyContent: "center",
            }}
          >
            {isSubagent ? <SubSessionIcon color={effectiveNodeColor ? `color-mix(in srgb, ${effectiveNodeColor} 78%, var(--text-secondary))` : undefined} /> : <ModeIcon type={session.task_id ? "task" : session.type || "chat"} size={16} color={effectiveNodeColor || undefined} />}
            {!isSubagent && session.type === "command" ? (
              <span
                title={session.shell || "shell"}
                style={{
                  position: "absolute",
                  right: "-8px",
                  bottom: "-4px",
                  minWidth: 0,
                  maxWidth: "26px",
                  height: "auto",
                  padding: "1px 3px",
                  borderRadius: "5px",
                  background: "#1d4ed8",
                  border: "none",
                  color: "#fff",
                  display: "inline-flex",
                  alignItems: "center",
                  justifyContent: "center",
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  whiteSpace: "nowrap",
                  fontSize: "7px",
                  fontWeight: 700,
                  lineHeight: 1.1,
                  letterSpacing: 0,
                }}
              >
                {shellBadgeLabel(session.shell)}
              </span>
            ) : !isSubagent ? (
              <span
                style={{
                  position: "absolute",
                  right: "-2px",
                  bottom: "-2px",
                  width: "10px",
                  height: "10px",
                  borderRadius: "999px",
                  background: "var(--content-bg, #fff)",
                  border: "1px solid rgba(255,255,255,0.9)",
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "center",
                  overflow: "hidden",
                }}
              >
                <AgentIcon
                  agentName={session.agent || ""}
                  style={{ width: "10px", height: "10px", display: "block" }}
                />
              </span>
            ) : null}
          </span>
        ) : null}

        {editing ? (
          <input
            ref={inputRef}
            value={draftName}
            disabled={saving}
            onChange={(e) => {
              setDraftName(e.target.value);
              e.currentTarget.scrollLeft = e.currentTarget.scrollWidth;
            }}
            onClick={(e) => e.stopPropagation()}
            onCompositionStart={() => {
              composingRef.current = true;
            }}
            onCompositionEnd={() => {
              composingRef.current = false;
            }}
            onKeyDown={(e) => {
              e.stopPropagation();
              if (e.key === "Escape") {
                e.preventDefault();
                cancelEditing();
                return;
              }
              if (e.key !== "Enter") {
                return;
              }
              const nativeEvent = e.nativeEvent as KeyboardEvent;
              const isComposing =
                composingRef.current ||
                nativeEvent.isComposing ||
                nativeEvent.keyCode === 229;
              if (isComposing) {
                return;
              }
              e.preventDefault();
              void submitRename();
            }}
            style={{
              minWidth: 0,
              flex: 1,
              height: "28px",
              borderRadius: "6px",
              border: "1px solid var(--accent-color)",
              background: "var(--content-bg, #fff)",
              color: "var(--text-primary)",
              fontSize: "13px",
              fontWeight: 600,
              padding: "0 10px 0 8px",
              outline: "none",
              boxSizing: "border-box",
            }}
          />
        ) : (
          <button
            type="button"
            onClick={() => onSelect?.(session)}
            style={{
              minWidth: 0,
              flex: 1,
              border: "none",
              background: "transparent",
              padding: 0,
              cursor: "pointer",
              textAlign: "left",
              color: selected ? (effectiveNodeColor || "var(--accent-color)") : "var(--text-primary)",
            }}
            onMouseEnter={(e) => {
              const container = e.currentTarget.parentElement;
              if (container && !selected && !parentHighlighted) {
                container.style.background = "rgba(0,0,0,0.03)";
              }
            }}
            onMouseLeave={(e) => {
              const container = e.currentTarget.parentElement;
              if (container && !selected && !parentHighlighted) {
                container.style.background = "transparent";
              }
            }}
          >
            <span
              style={{
                display: "block",
                fontSize: "13px",
                fontWeight: selected ? 600 : 500,
                whiteSpace: "nowrap",
                overflow: "hidden",
                textOverflow: "ellipsis",
              }}
            >
              {renderHighlightedText(displayName, highlightQuery, {
                color: selected ? (effectiveNodeColor || "var(--accent-color)") : "var(--text-primary)",
              })}
            </span>
            {snippet ? (
              <span
                style={{
                  marginTop: "2px",
                  display: "block",
                  fontSize: "11px",
                  lineHeight: 1.45,
                  color: "var(--text-secondary)",
                  whiteSpace: "nowrap",
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                }}
              >
                {renderHighlightedText(snippet, highlightQuery, {
                  color: "var(--text-secondary)",
                })}
              </span>
            ) : null}
          </button>
        )}

        {editing ? (
          <div
            style={{
              flexShrink: 0,
              display: "inline-flex",
              alignItems: "center",
              gap: 0,
            }}
          >
            <button
              type="button"
              onMouseDown={(e) => e.preventDefault()}
              onClick={(e) => {
                e.stopPropagation();
                void submitRename();
              }}
              disabled={saving}
              aria-label={t("sessionList.confirmRename")}
              style={{
                ...inlineActionStyle,
                width: "18px",
                color: "var(--accent-color)",
                opacity: saving ? 0.6 : 1,
                cursor: saving ? "default" : "pointer",
              }}
            >
              <svg
                width="13"
                height="13"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2.5"
                strokeLinecap="round"
                strokeLinejoin="round"
                aria-hidden="true"
              >
                <polyline points="20 6 9 17 4 12" />
              </svg>
            </button>
            <button
              type="button"
              onMouseDown={(e) => e.preventDefault()}
              onClick={(e) => {
                e.stopPropagation();
                cancelEditing();
              }}
              disabled={saving}
              aria-label={t("sessionList.cancelRename")}
              style={{
                ...inlineActionStyle,
                width: "18px",
                opacity: saving ? 0.6 : 1,
                cursor: saving ? "default" : "pointer",
              }}
            >
              <svg
                width="12"
                height="12"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
                strokeLinecap="round"
                strokeLinejoin="round"
                aria-hidden="true"
              >
                <line x1="18" y1="6" x2="6" y2="18" />
                <line x1="6" y1="6" x2="18" y2="18" />
              </svg>
            </button>
          </div>
        ) : null}

      </div>

      {!editing ? (
        <div
          style={{
            flexShrink: 0,
            minWidth: "24px",
            display: "flex",
            alignItems: "center",
            justifyContent: "flex-end",
            paddingLeft: "2px",
          }}
        >
          {syncing ? (
            <span
              aria-label={t("sessionList.syncing")}
              title={t("sessionList.syncing")}
              style={{
                width: "12px",
                height: "12px",
                borderRadius: "50%",
                border: "1.5px solid rgba(100,116,139,0.35)",
                borderTopColor: "var(--accent-color)",
                display: "inline-block",
                flexShrink: 0,
                animation: "spin 0.8s linear infinite",
              }}
            />
          ) : session.pending ? (
            <span
              aria-label={t("sessionList.replying")}
              title={t("sessionList.replying")}
              style={{
                width: "8px",
                height: "8px",
                borderRadius: "999px",
                flexShrink: 0,
                boxSizing: "border-box",
                border: `1.5px solid ${effectiveNodeColor || "var(--accent-color)"}`,
                background: effectiveNodeColor || "var(--accent-color)",
                animation: "mindfs-bound-pulse 2.2s ease-in-out infinite",
                boxShadow: `0 0 0 1.5px ${hexToRgbaApp(effectiveNodeColor || "var(--accent-color)", 0.14)}`,
              }}
            />
          ) : (
            <span
              style={{
                fontSize: "10px",
                color: "var(--text-secondary)",
                opacity: 0.8,
                whiteSpace: "nowrap",
                textAlign: "right",
              }}
            >
              {formatTime(
                isClosed && session.closed_at
                  ? session.closed_at
                  : session.updated_at || "",
                locale,
                t("time.justNow"),
              )}
            </span>
          )}
        </div>
      ) : null}

      <div
        ref={menuRef}
        style={{ position: "relative", flexShrink: 0, marginLeft: "2px" }}
      >
        <button
          type="button"
          aria-label={t("sessionList.menu")}
          onClick={(e) => {
            e.stopPropagation();
            setMenuOpen((open) => !open);
          }}
          style={{
            width: "28px",
            height: "28px",
            borderRadius: "8px",
            border: "none",
            background: menuOpen ? "rgba(0, 0, 0, 0.06)" : "transparent",
            color: "var(--text-secondary)",
            display: "inline-flex",
            alignItems: "center",
            justifyContent: "center",
            cursor: "pointer",
            outline: "none",
            position: "relative",
          }}
        >
          <svg
            width="14"
            height="14"
            viewBox="0 0 24 24"
            fill="currentColor"
            aria-hidden="true"
          >
            <circle cx="12" cy="5" r="1.8" />
            <circle cx="12" cy="12" r="1.8" />
            <circle cx="12" cy="19" r="1.8" />
          </svg>
          {isPinned ? (
            <span
              aria-hidden="true"
              style={{
                position: "absolute",
                top: "1px",
                right: "1px",
                width: "12px",
                height: "12px",
                borderRadius: "999px",
                background: "var(--menu-bg)",
                color: selected ? "var(--accent-color)" : "var(--text-secondary)",
                display: "inline-flex",
                alignItems: "center",
                justifyContent: "center",
                pointerEvents: "none",
                boxShadow: "0 0 0 1px var(--content-bg, #fff)",
              }}
            >
              <span style={{ width: "9px", height: "9px", display: "inline-flex" }}>
                <PinIcon pinned />
              </span>
            </span>
          ) : null}
        </button>
        {menuOpen ? (
          <div
            style={{
              position: "absolute",
              top: "calc(100% + 6px)",
              right: 0,
              minWidth: "120px",
              padding: "6px",
              borderRadius: "10px",
              border: "1px solid var(--border-color)",
              background: "var(--menu-bg)",
              boxShadow: "0 12px 30px rgba(15, 23, 42, 0.14)",
              zIndex: 20,
            }}
          >
            <button
              type="button"
              onClick={(e) => {
                e.stopPropagation();
                setMenuOpen(false);
                void onPin?.(session, !isPinned);
              }}
              style={{
                ...menuItemStyle,
                color: "var(--text-primary)",
              }}
            >
              <span style={{ width: "13px", height: "13px", display: "inline-flex" }}>
                <PinIcon pinned={isPinned} />
              </span>
              {isPinned ? t("sessionList.unpin") : t("sessionList.pin")}
            </button>
            <button
              type="button"
              disabled={syncing}
              onClick={(e) => {
                e.stopPropagation();
                setMenuOpen(false);
                void onSync?.(session);
              }}
              style={{
                ...menuItemStyle,
                color: "var(--text-primary)",
                opacity: syncing ? 0.55 : 1,
                cursor: syncing ? "default" : "pointer",
              }}
            >
              <svg
                xmlns="http://www.w3.org/2000/svg"
                width="13"
                height="13"
                viewBox="0 0 24 24"
                aria-hidden="true"
              >
                <path
                  fill="currentColor"
                  d="M19.91 15.51h-4.53a1 1 0 0 0 0 2h2.4A8 8 0 0 1 4 12a1 1 0 0 0-2 0a10 10 0 0 0 16.88 7.23V21a1 1 0 0 0 2 0v-4.5a1 1 0 0 0-.97-.99M12 2a10 10 0 0 0-6.88 2.77V3a1 1 0 0 0-2 0v4.5a1 1 0 0 0 1 1h4.5a1 1 0 0 0 0-2h-2.4A8 8 0 0 1 20 12a1 1 0 0 0 2 0A10 10 0 0 0 12 2"
                />
              </svg>
              {t("sessionList.sync")}
            </button>
            <button
              type="button"
              onClick={(e) => {
                e.stopPropagation();
                setMenuOpen(false);
                setDraftName(storedName);
                setEditing(true);
              }}
              style={{
                ...menuItemStyle,
                color: "var(--text-primary)",
              }}
            >
              <svg
                width="13"
                height="13"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
                strokeLinecap="round"
                strokeLinejoin="round"
                aria-hidden="true"
              >
                <path d="M12 20h9" />
                <path d="M16.5 3.5a2.12 2.12 0 1 1 3 3L7 19l-4 1 1-4 12.5-12.5z" />
              </svg>
              {t("sessionList.rename")}
            </button>
            {onArchive ? (
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  setMenuOpen(false);
                  void onArchive(session, !isArchived);
                }}
                style={{
                  ...menuItemStyle,
                  color: "var(--text-primary)",
                }}
              >
                <span style={{ width: "13px", height: "13px", display: "inline-flex" }}>
                  <ArchiveIcon />
                </span>
                {isArchived ? t("sessionList.unarchive") : t("sessionList.archive")}
              </button>
            ) : null}
            <button
              type="button"
              onClick={(e) => {
                e.stopPropagation();
                setMenuOpen(false);
                onDelete?.(session);
              }}
              style={{
                ...menuItemStyle,
                color: "#dc2626",
              }}
            >
              <svg
                width="13"
                height="13"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
                strokeLinecap="round"
                strokeLinejoin="round"
                aria-hidden="true"
              >
                <polyline points="3 6 5 6 21 6" />
                <path d="M19 6l-1 14H6L5 6" />
                <path d="M10 11v6" />
                <path d="M14 11v6" />
                <path d="M9 6V4h6v2" />
              </svg>
              {t("common.delete")}
            </button>
          </div>
        ) : null}
      </div>
    </div>
  );
}

function formatTime(isoString: string, locale: Locale, justNow: string): string {
  const date = new Date(isoString);
  if (Number.isNaN(date.getTime())) {
    return "";
  }
  const now = new Date();
  const diff = now.getTime() - date.getTime();
  if (diff < 60000) return justNow;
  if (diff < 3600000) return `${Math.floor(diff / 60000)}m`;
  if (diff < 86400000) return `${Math.floor(diff / 3600000)}h`;
  if (now.getFullYear() === date.getFullYear()) {
    return new Intl.DateTimeFormat(locale, { month: "numeric", day: "numeric" }).format(date);
  }
  return new Intl.DateTimeFormat(locale, { year: "2-digit", month: "numeric" }).format(date);
}

function renderHighlightedText(
  text: string,
  query?: string,
  palette?: { color?: string },
): React.ReactNode {
  const source = String(text || "");
  const needle = String(query || "").trim();
  if (!source || !needle) {
    return source;
  }
  const lowerSource = source.toLowerCase();
  const lowerNeedle = needle.toLowerCase();
  const parts: React.ReactNode[] = [];
  let cursor = 0;

  for (;;) {
    const index = lowerSource.indexOf(lowerNeedle, cursor);
    if (index < 0) {
      break;
    }
    if (index > cursor) {
      parts.push(source.slice(cursor, index));
    }
    const match = source.slice(index, index + needle.length);
    parts.push(
      <mark
        key={`${index}:${match}`}
        style={{
          background: "transparent",
          color: "var(--accent-color)",
          padding: 0,
        }}
      >
        {match}
      </mark>,
    );
    cursor = index + needle.length;
  }

  if (cursor < source.length) {
    parts.push(source.slice(cursor));
  }
  return parts.length ? parts : source;
}

function iconButtonStyle(withGap: boolean): React.CSSProperties {
  return {
    border: "none",
    background: "transparent",
    color: "var(--text-secondary)",
    display: "inline-flex",
    alignItems: "center",
    justifyContent: "center",
    gap: withGap ? "2px" : 0,
    height: "28px",
    minWidth: "28px",
    borderRadius: "8px",
    cursor: "pointer",
    padding: withGap ? "0 6px" : 0,
  };
}

function ChevronLeftIcon() {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="m15 18-6-6 6-6" />
    </svg>
  );
}

function SubSessionIcon({ color }: { color?: string }) {
  const c = String(color || "").trim() || "var(--accent-color)";
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width="18"
      height="18"
      viewBox="0 0 32 32"
      aria-hidden="true"
      style={{ color: c, display: "block" }}
    >
      <path d="M0 0h32v32H0z" fill="none" />
      <path
        fill="currentColor"
        d="M23 20c-2.41 0-4.43 1.72-4.9 4H14c-2.21 0-4-1.79-4-4v-8.1A5 5 0 1 0 4 7c0 2.41 1.72 4.43 4 4.9V20c0 3.31 2.69 6 6 6h4.1a5 5 0 1 0 4.9-6M6 7c0-1.65 1.35-3 3-3s3 1.35 3 3s-1.35 3-3 3s-3-1.35-3-3"
      />
    </svg>
  );
}

// memo 化 SessionCard：父组件（SessionList / MultiProjectSessionList）重渲染时，
// 若 props 引用未变则跳过整卡重渲染（含 useI18n / 多个 useEffect）。
const SessionCardMemo = memo(SessionCard);

const menuItemStyle: React.CSSProperties = {
  width: "100%",
  border: "none",
  background: "transparent",
  borderRadius: "8px",
  padding: "8px 10px",
  display: "flex",
  alignItems: "center",
  gap: "8px",
  textAlign: "left",
  cursor: "pointer",
  fontSize: "12px",
  fontWeight: 500,
};

const inlineActionStyle: React.CSSProperties = {
  width: "24px",
  height: "24px",
  border: "none",
  borderRadius: "6px",
  background: "transparent",
  color: "var(--text-secondary)",
  display: "inline-flex",
  alignItems: "center",
  justifyContent: "center",
  padding: 0,
};
