import React from "react";
import { GitStatusPanel } from "../components/GitStatusPanel";
import { GitHistoryPanel } from "../components/GitHistoryPanel";
import { useI18n } from "../i18n";
import type {
  GitHistoryItem,
  GitHistoryPayload,
  GitStatusItem,
  GitStatusPayload,
} from "../services/git";
import { ManagedRootPayload } from "../app/appMisc";
import type { DirectorySortMode } from "../services/directorySort";

/**
 * 项目树「git」页签的内容：状态面板 + 历史面板。
 *
 * 纯视图——所有数据与回调由 App 传入，自己不做任何取数与副作用。
 * 「当前 root 用 state、其余用 per-root map」这条判据与原实现一致。
 */
export function RootGitContentView({
  root,
  currentRootId,
  scopedRootKey,
  managedRootByIdRef,
  gitStatus,
  gitStatusByRoot,
  gitStatusLoading,
  gitStatusLoadingByRoot,
  gitHistory,
  gitHistoryByRoot,
  gitHistoryLoading,
  gitHistoryLoadingMore,
  gitHistoryLoadingByRoot,
  gitStatusExpandedByRoot,
  gitHistoryExpandedByRoot,
  setGitStatusExpandedByRoot,
  setGitHistoryExpandedByRoot,
  refreshGitStatus,
  refreshGitHistory,
  loadMoreGitHistory,
  openGitDiff,
  openGitCommitDiff,
  handleGitPull,
  handleGitPush,
  handleGitCommit,
  handleGitStageItem,
  handleGitUnstageItem,
  handleGitDiscardItem,
  switchGitBranch,
  actionHandlers,
  projectSortMode,
}: {
  root: string;
  currentRootId: string | null;
  scopedRootKey: (rootId: string) => string;
  managedRootByIdRef: React.MutableRefObject<Record<string, ManagedRootPayload>>;
  gitStatus: GitStatusPayload | null;
  gitStatusByRoot: Record<string, GitStatusPayload | null>;
  gitStatusLoading: boolean;
  gitStatusLoadingByRoot: Record<string, boolean>;
  gitHistory: GitHistoryPayload | null;
  gitHistoryByRoot: Record<string, GitHistoryPayload | null>;
  gitHistoryLoading: boolean;
  gitHistoryLoadingMore: boolean;
  gitHistoryLoadingByRoot: Record<string, boolean>;
  gitStatusExpandedByRoot: Record<string, boolean>;
  gitHistoryExpandedByRoot: Record<string, Record<string, boolean>>;
  setGitStatusExpandedByRoot: React.Dispatch<
    React.SetStateAction<Record<string, boolean>>
  >;
  setGitHistoryExpandedByRoot: React.Dispatch<
    React.SetStateAction<Record<string, Record<string, boolean>>>
  >;
  refreshGitStatus: (rootId: string, options?: { force?: boolean }) => Promise<unknown>;
  refreshGitHistory: (
    rootId: string,
    options?: { force?: boolean; waitForIncremental?: boolean },
  ) => Promise<unknown>;
  loadMoreGitHistory: (rootId: string) => Promise<unknown>;
  openGitDiff: (
    rootId: string,
    item: GitStatusItem,
    options?: { preserveRelatedSelection?: boolean; repoPath?: string },
  ) => Promise<void>;
  openGitCommitDiff: (rootId: string, commit: GitHistoryItem, item: GitStatusItem) => Promise<void>;
  handleGitPull: (rootId: string) => Promise<void>;
  handleGitPush: (rootId: string) => Promise<void>;
  handleGitCommit: (rootId: string, message: string) => Promise<void>;
  handleGitStageItem: (rootId: string, item: GitStatusItem) => Promise<void>;
  handleGitUnstageItem: (rootId: string, item: GitStatusItem) => Promise<void>;
  handleGitDiscardItem: (rootId: string, item: GitStatusItem) => Promise<void>;
  switchGitBranch: (rootId: string, branch: string) => Promise<void>;
  actionHandlers: {
    open: (payload: { path: string; root: string; nodeId?: string }) => void;
  };
  projectSortMode?: DirectorySortMode;
}) {
  const { t } = useI18n();

    const rootGitStatus = root === currentRootId ? gitStatus : gitStatusByRoot[scopedRootKey(root)] || null;
    const rootGitHistory = root === currentRootId ? gitHistory : gitHistoryByRoot[scopedRootKey(root)] || null;
    const rootGitStatusLoading =
      root === currentRootId ? gitStatusLoading : gitStatusLoadingByRoot[scopedRootKey(root)] === true;
    const rootGitHistoryLoading =
      root === currentRootId ? gitHistoryLoading : gitHistoryLoadingByRoot[scopedRootKey(root)] === true;
    const rootGitStatusAvailable = rootGitStatus?.available === true;
    const rootGitHistoryAvailable = rootGitHistory?.available === true;
    const rootGitStatusExpanded = gitStatusExpandedByRoot[scopedRootKey(root)] !== false;
    const rootGitHistoryExpandedCommits = gitHistoryExpandedByRoot[scopedRootKey(root)] || {};
    const rootShouldRenderGitPanel = rootGitStatusLoading || rootGitStatusAvailable;
    const rootShouldRenderGitHistoryPanel =
      rootGitHistoryLoading || (rootGitHistoryAvailable && (rootGitHistory?.items.length || 0) > 0);
    if (managedRootByIdRef.current[root]?.is_git_repo !== true) {
      return (
        <div style={{ padding: "8px 4px", fontSize: "12px", color: "var(--text-secondary)" }}>
          {t("git.notRepository")}
        </div>
      );
    }
    if (!rootGitStatus && !rootGitHistory && !rootGitStatusLoading && !rootGitHistoryLoading) {
      return (
        <button
          type="button"
          onClick={() => {
            void refreshGitStatus(root);
            void refreshGitHistory(root);
          }}
          style={{
            border: "none",
            background: "transparent",
            color: "var(--text-secondary)",
            borderRadius: "6px",
            padding: "8px 4px",
            fontSize: "12px",
            cursor: "pointer",
            textAlign: "left",
            width: "100%",
          }}
        >
          {t("git.loadingStatus")}
        </button>
      );
    }
    if (!rootGitStatusLoading && !rootGitHistoryLoading && !rootShouldRenderGitPanel && !rootShouldRenderGitHistoryPanel) {
      return (
        <div style={{ padding: "8px 4px", fontSize: "12px", color: "var(--text-secondary)" }}>
          {t("git.emptyChangesOrHistory")}
        </div>
      );
    }

  return (
      <div style={{ display: "flex", flexDirection: "column", gap: "14px", minWidth: 0 }}>
        {rootShouldRenderGitPanel ? (
          <GitStatusPanel
            rootId={root}
            status={rootGitStatus}
            loading={rootGitStatusLoading}
            compact
            expanded={rootGitStatusExpanded}
            onExpandedChange={(expanded) => {
              if (!root) {
                return;
              }
              setGitStatusExpandedByRoot((prev) => ({ ...prev, [scopedRootKey(root)]: expanded }));
            }}
            onSelectItem={(item) => {
              if (!root) {
                return;
              }
              void openGitDiff(root, item);
            }}
            onOpenItem={(item) => {
              if (!root || item.is_dir === true) {
                return;
              }
              actionHandlers.open({ path: item.path, root });
            }}
            onDiscardItem={(item) => {
              if (!root) {
                return;
              }
              return handleGitDiscardItem(root, item);
            }}
            onStageItem={(item) => {
              if (!root) {
                return;
              }
              return item.staged === true
                ? handleGitUnstageItem(root, item)
                : handleGitStageItem(root, item);
            }}
            onPull={() => {
              if (!root) {
                return;
              }
              return handleGitPull(root);
            }}
            onPush={() => {
              if (!root) {
                return;
              }
              return handleGitPush(root);
            }}
            onCommit={(message) => {
              if (!root) {
                return;
              }
              return handleGitCommit(root, message);
            }}
            onSwitchBranch={(branch) => {
              if (!root) {
                return;
              }
              return switchGitBranch(root, branch);
            }}
          />
        ) : null}
        {rootShouldRenderGitHistoryPanel ? (
          <GitHistoryPanel
            rootId={root}
            items={rootGitHistory?.items || []}
            loading={rootGitHistoryLoading}
            loadingMore={gitHistoryLoadingMore}
            hasMore={rootGitHistory?.has_more === true}
            compact
            expandedCommits={rootGitHistoryExpandedCommits}
            onToggleCommit={(hash) => {
              if (!root) {
                return;
              }
              setGitHistoryExpandedByRoot((prev) => {
                const current = prev[scopedRootKey(root)] || {};
                return {
                  ...prev,
                  [scopedRootKey(root)]: {
                    ...current,
                    [hash]: current[hash] !== true,
                  },
                };
              });
            }}
            onLoadMore={() => {
              void loadMoreGitHistory(root);
            }}
            onSelectFile={(commit, item) => {
              if (!root) {
                return;
              }
              void openGitCommitDiff(root, commit, item);
            }}
          />
        ) : null}
      </div>
  );
}
