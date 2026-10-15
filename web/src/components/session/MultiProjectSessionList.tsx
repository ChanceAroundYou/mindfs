import React from "react";
import { useI18n } from "../../i18n/index";
import { NodeBadgeHeader } from "../account/NodeBadgeHeader";
import { ViewHeader } from "../../layout/ViewHeader";
import { PALETTE } from "../../shared/nodePalette";
import { resolveGroupColor } from "../../shared/sessionGroupDisplay";
import { SessionCardMemo } from "./SessionCard";
import {
  ArchiveHeaderButton,
  MAIN_SESSION_ICON_OFFSET,
  MULTI_PROJECT_VISIBLE_LIMIT,
  PinIcon,
  SUB_SESSION_ICON_OFFSET,
  ToggleRowButton,
  isSessionSyncing,
} from "./sessionListShared";
import type {
  ProjectSessionGroup,
  ProjectSessionListProps,
  SessionItem,
  VisibleSessionRow,
} from "./sessionListTypes";
import { useMultiProjectSessionList } from "./useMultiProjectSessionList";

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
  const {
    pinnedProjects,
    nodes,
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
    setExpandedChildren,
    loadingChildren,
    childrenHasMore,
    childStateKey,
    topLevelSessionsForGroup,
  } = useMultiProjectSessionList({
    groups,
    selectedKey,
    selectedRootId,
    selectedNodeId,
    projectSortMode,
    onLoadMoreProject,
    onLoadChildren,
  });

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
              const groupNodeId = String((group as any)?._nodeId || "").trim();
              const groupKey = groupScopeKey(group);
              const expanded = expandedProjects[groupKey] ?? groupDefaultExpanded(group);
              const pinned = !!pinnedProjects[groupKey];
              const groupColor = String((group as any)._nodeColor || resolveGroupColor(group as any, {}, nodes as any) || PALETTE[0]);
              const topLevelSessions = topLevelSessionsForGroup(group.sessions);
              const sessions = expanded ? group.sessions : [];
              const rows = buildRows(sessions, group.rootId, groupNodeId);
              const remaining = Math.max(0, group.totalCount - topLevelSessions.length);
              const projectLoading = !!loadingProjects[groupKey];
              const groupUnreachable = (group as any)._unreachable === true;
              return (
                <section key={groupKey} style={{ minWidth: 0 }}>
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
                  <div style={{ display: "flex", flexDirection: "column", gap: "6px", paddingTop: 0 }}>
                    {rows.map((row) => {
                      if (row.type === "child-toggle") {
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
