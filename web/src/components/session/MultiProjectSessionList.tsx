import React, { useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { pruneChildState } from "../../services/sessionTree";
import { type DirectorySortMode, sortDirectoryEntries } from "../../services/prefs/directorySort";
import { NodeBadgeHeader } from "../account/NodeBadgeHeader";
import { ViewHeader } from "../../layout/ViewHeader";
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
import { useI18n } from "../../i18n/index";
import { SessionCardMemo } from "./SessionCard";
import {
  ArchiveHeaderButton,
  COLLAPSED_CHILD_SESSION_LIMIT,
  MAIN_SESSION_ICON_OFFSET,
  MULTI_PROJECT_VISIBLE_LIMIT,
  PinIcon,
  SUB_SESSION_ICON_OFFSET,
  ToggleRowButton,
  isSessionSyncing,
  parseForkSessionSource,
} from "./sessionListShared";
import type {
  ProjectSessionGroup,
  ProjectSessionListProps,
  SessionItem,
  VisibleSessionRow,
} from "./sessionListTypes";

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
  // 置顶以 **store** 为唯一来源（services/pins.ts），本组件只订阅、不持有。
  //
  // 首帧没有本地缓存可读（localStorage 层已删：偏好表几十字节、服务端有权威副本，
  // 持久化只会制造双份真相），所以首帧置顶区为空、服务端到达时排一次 ——
  // 那是**一次**跳变，不是狂跳。
  //
  // 不再自己 useState + useEffect 拉一份：那是「第二份能重排列表的状态」，
  // 与 store 到达时的那次各排一次，就是右侧狂跳的两个来源。
  //
  // 依赖 selectedRootId / selectedNodeId：切项目或切节点时顺带刷新一次，另一台
  // 设备上的置顶改动就此可见（用户不要跨设备实时，导航时刷新就是约定的时机）。
  // 依赖写空数组的旧版本只在挂载时拉一次 —— 切项目后看到的还是首次那份。
  useEffect(() => {
    void refreshPinsFromServer().catch(() => {
      /* 拉不到就保留 store 里那份，不闪成全不置顶 */
    });
  }, [selectedRootId, selectedNodeId]);
  const pinsSnapshot = useSyncExternalStore(subscribePins, readPins, readPins);
  const pinnedProjects = pinsSnapshot.projects;
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
    // 意图只算一次：readPins() 在 await 之后可能已经是另一份表（并发点击/刷新），
    // 那时再读一次会得到与乐观更新相反的值，回滚就会把状态改成错的。
    const pinned = !readPins().projects[key];
    const rollback = optimisticProjectPin(key, pinned);
    // setProjectPin 回的是服务端落库后的**整张表**，直接写进 store 即可；
    // 不再额外 refreshPinsFromServer —— 那是一次纯浪费的往返（写路径刚拿过真相）。
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
      <ViewHeader
        data-onboarding="session-actions"
        padding="0 10px 0 2px"
        showToggles={false}
        style={{ display: hideHeader ? "none" : "flex", justifyContent: "space-between" }}
        innerStyle={{ justifyContent: "space-between" }}
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
          <ArchiveHeaderButton onOpen={onOpenArchivePanel} />
        </div>
        {headerAction ? <div style={{ display: "inline-flex", alignItems: "center" }}>{headerAction}</div> : null}
      </ViewHeader>
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
              // 节点本轮没拉到：列表里这批是上次的旧快照（保留是对的），但要点破 ——
              // 断联时点开只有会话名、没有正文，不说明的话看着就像会话坏了。
              const groupUnreachable = (group as any)._unreachable === true;
              return (
                <section key={`${(group as any)._nodeId || ""}::${group.rootId}`} style={{ minWidth: 0 }}>
                  <div style={{ position: "relative" }}>
                    <NodeBadgeHeader
                      color={groupColor}
                      label={group.rootName || group.rootId}
                      collapsed={!expanded}
                      onClick={() => void handleProjectHeaderToggle(group)}
                      notice={groupUnreachable ? t("sessionList.nodeUnreachable") : undefined}
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
