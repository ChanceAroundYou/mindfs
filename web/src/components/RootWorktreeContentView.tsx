import React from "react";
import { GitStatusPanel } from "./GitStatusPanel";
import { useI18n } from "../i18n";
import type { GitStatusItem, GitStatusPayload, GitWorktreeItem } from "../services/git";
import type { RelatedWorktree } from "../services/session";
import type { ManagedRootPayload } from "../app/appMisc";

/**
 * 项目树「worktrees」页签的内容：可折叠的 worktree 列表，每个展开后挂自己的 git 状态面板。
 *
 * 纯视图——数据与回调全由 App 传入。worktree 的分片状态整体走 worktrees 这一个
 * 分组 prop（就是 useProjectTreeWorktrees 的返回对象），而不是七条平铺的 prop。
 */
export function RootWorktreeContentView({
  root,
  currentRootId,
  scopedRootKey,
  managedRootByIdRef,
  relatedWorktree,
  worktrees,
  findManagedRootByPath,
  openGitDiff,
  actionHandlers,
}: {
  root: string;
  currentRootId: string | null;
  scopedRootKey: (rootId: string) => string;
  managedRootByIdRef: React.MutableRefObject<Record<string, ManagedRootPayload>>;
  relatedWorktree?: RelatedWorktree | null;
  worktrees: {
    worktreeItemsByRoot: Record<string, GitWorktreeItem[]>;
    worktreeLoadingByRoot: Record<string, boolean>;
    worktreeErrorByRoot: Record<string, string>;
    expandedWorktreeByRoot: Record<string, string>;
    worktreeStatusByPath: Record<string, GitStatusPayload | null>;
    worktreeStatusLoadingByPath: Record<string, boolean>;
    toggleProjectTreeWorktree: (rootId: string, path: string) => void;
  };
  findManagedRootByPath: (path: string) => ManagedRootPayload | null;
  openGitDiff: (
    rootId: string,
    item: GitStatusItem,
    options?: { preserveRelatedSelection?: boolean; repoPath?: string },
  ) => Promise<void>;
  actionHandlers: {
    open: (payload: { path: string; root: string; nodeId?: string }) => void;
  };
}) {
  const { t } = useI18n();
  const {
    worktreeItemsByRoot,
    worktreeLoadingByRoot,
    worktreeErrorByRoot,
    expandedWorktreeByRoot,
    worktreeStatusByPath,
    worktreeStatusLoadingByPath,
    toggleProjectTreeWorktree,
  } = worktrees;

    // 与 renderRootGitContent 同一判据：非 git 根没有 worktree，别把 git 的原始报错
    // （"fatal: not a git repository"）当错误渲染出来。
    if (managedRootByIdRef.current[root]?.is_git_repo !== true) {
      return (
        <div style={{ padding: "8px 4px", fontSize: "12px", color: "var(--text-secondary)" }}>
          {t("git.notRepository")}
        </div>
      );
    }
    const relatedPath =
      relatedWorktree?.root_id === root ? String(relatedWorktree?.path || "") : "";
    const items = [...(worktreeItemsByRoot[scopedRootKey(root)] || [])].sort((left, right) => {
      if (!relatedPath) {
        return 0;
      }
      if (left.path === relatedPath) {
        return -1;
      }
      if (right.path === relatedPath) {
        return 1;
      }
      return 0;
    });
    const loading = worktreeLoadingByRoot[scopedRootKey(root)] === true;
    const error = worktreeErrorByRoot[scopedRootKey(root)] || "";
    const expandedPath = expandedWorktreeByRoot[scopedRootKey(root)] || "";
    if (loading) {
      return (
        <div style={{ padding: "8px 4px", fontSize: "12px", color: "var(--text-secondary)" }}>
          {t("worktree.loading")}
        </div>
      );
    }
    if (error) {
      return (
        <div style={{ padding: "8px 4px", fontSize: "12px", color: "#b45309" }}>
          {error}
        </div>
      );
    }
    if (items.length === 0) {
      return (
        <div style={{ padding: "8px 4px", fontSize: "12px", color: "var(--text-secondary)" }}>
          {t("worktree.empty")}
        </div>
      );
    }
    return (
      <div style={{ display: "flex", flexDirection: "column", gap: "5px", minWidth: 0 }}>
        {items.map((item) => {
          const managed = findManagedRootByPath(item.path);
          const targetRootId = managed?.id || "";
          const selected = expandedPath === item.path;
          const status = worktreeStatusByPath[item.path] || null;
          const statusLoading = worktreeStatusLoadingByPath[item.path] === true;
          const dirtyCount = status?.items?.length || 0;
          const branchLabel = item.branch || item.head?.slice(0, 8) || item.path;
          const statusCountLabel = statusLoading ? "..." : status ? String(dirtyCount) : "";

  return (
            <div key={item.path} style={{ minWidth: 0 }}>
              <button
                type="button"
                onClick={async () => {
                  await toggleProjectTreeWorktree(root, item.path);
                }}
                style={{
                  width: "100%",
                  border: "none",
                  borderRadius: "7px",
                  background: selected ? "var(--selection-bg)" : "transparent",
                  color: selected ? "var(--accent-color)" : "var(--text-primary)",
                  padding: "6px 4px 6px 0",
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "space-between",
                  gap: "8px",
                  textAlign: "left",
                  cursor: "pointer",
                }}
              >
                <span style={{ minWidth: 0, display: "inline-flex", alignItems: "center", gap: "8px" }}>
                  <span
                    title={t("git.changes")}
                    aria-label={t("git.changes")}
                    style={{
                      width: "18px",
                      height: "18px",
                      display: "inline-flex",
                      alignItems: "center",
                      justifyContent: "center",
                      color: "var(--text-primary)",
                      flexShrink: 0,
                    }}
                  >
                    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden="true">
                      <path
                        fill="currentColor"
                        d="M7 5a2 2 0 1 1 3.763.945h.58a4 4 0 0 1 4 4v1.28a2 2 0 0 1-1.02 3.72a2 2 0 0 1-.98-3.745V9.945a2 2 0 0 0-2-2H10v9.323A2 2 0 0 1 9 21a2 2 0 0 1-1-3.732V6.732A2 2 0 0 1 7 5"
                      />
                    </svg>
                  </span>
                  <span style={{ fontSize: "12px", fontWeight: selected ? 700 : 600, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                    {branchLabel}
                  </span>
                </span>
                <span style={{ display: "inline-flex", alignItems: "center", gap: "6px", fontSize: "11px", color: selected ? "var(--accent-color)" : "var(--text-secondary)", flexShrink: 0 }}>
                  <span>{statusCountLabel}</span>
                  <svg
                    width="12"
                    height="12"
                    viewBox="0 0 20 20"
                    fill="currentColor"
                    aria-hidden="true"
                    style={{
                      transform: selected ? "rotate(180deg)" : "rotate(0deg)",
                      transition: "transform 0.15s",
                    }}
                  >
                    <path
                      fillRule="evenodd"
                      d="M5.23 7.21a.75.75 0 0 1 1.06.02L10 11.17l3.71-3.94a.75.75 0 1 1 1.08 1.04l-4.25 4.5a.75.75 0 0 1-1.08 0l-4.25-4.5a.75.75 0 0 1 .02-1.06"
                      clipRule="evenodd"
                    />
                  </svg>
                </span>
              </button>
              {selected ? (
                <div style={{ padding: "4px 0 4px 0" }}>
                  {statusLoading || status ? (
                    <GitStatusPanel
                      rootId={targetRootId || currentRootId || undefined}
                      status={status}
                      loading={statusLoading}
                      compact
                      expanded
                      showHeader={false}
                      showHeaderActions={false}
                      showExpandedToggle={false}
                      enableBranchMenu={false}
                      onSelectItem={(statusItem) => {
                        const nextRoot = targetRootId || root;
                        if (!nextRoot) {
                          return;
                        }
                        void openGitDiff(nextRoot, statusItem, targetRootId ? undefined : { repoPath: item.path });
                      }}
                      onOpenItem={(statusItem) => {
                        const nextRoot = targetRootId;
                        if (!nextRoot || statusItem.is_dir === true) {
                          return;
                        }
                        actionHandlers.open({ path: statusItem.path, root: nextRoot });
                      }}
                    />
                  ) : (
                    <div style={{ padding: "6px 4px", fontSize: "12px", color: "var(--text-secondary)" }}>
                      {t("git.loadingStatusInline")}
                    </div>
                  )}
                </div>
              ) : null}
            </div>
          );
        })}
      </div>
  );
}
