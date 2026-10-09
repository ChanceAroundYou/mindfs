import { RootGitContentView } from "../components/root/RootGitContentView";
import { RootRelatedContentView } from "../components/root/RootRelatedContentView";
import { RootWorktreeContentView } from "../components/root/RootWorktreeContentView";
import { normalizePath } from "./appPath";
import { relatedFileSelectionKey } from "./appSession";
import type { GitFileStat, RelatedFileClickTarget } from "./appSession";
import type { ManagedRootPayload } from "./appMisc";
import type { ActionHandlers } from "./useActionHandlers";
import type { DirectorySortMode } from "../services/prefs/directorySort";
import type { RelatedFileStat } from "../hooks/useRelatedFileStats";
import type { RelatedWorktree } from "../services/sessionTypes";
import type { GitHistoryItem, GitHistoryPayload, GitStatusItem, GitStatusPayload, GitWorktreeItem } from "../services/git";
import type { KanbanTask } from "../services/tasks";

/**
 * 文件视图三个 tab 的内容渲染器：worktree / 关联文件 / git。三者由同一个组件一次性接走。
 */
export type UseAppRootContentRenderersParams = {
  currentRootId: string | null;
  scopedRootKey: (rootId: string) => string;
  managedRootByIdRef: React.RefObject<Record<string, ManagedRootPayload>>;
  relatedWorktree: RelatedWorktree | null;
  worktreeItemsByRoot: Record<string, GitWorktreeItem[]>;
  worktreeLoadingByRoot: Record<string, boolean>;
  worktreeErrorByRoot: Record<string, string>;
  expandedWorktreeByRoot: Record<string, string>;
  worktreeStatusByPath: Record<string, GitStatusPayload | null>;
  worktreeStatusLoadingByPath: Record<string, boolean>;
  toggleProjectTreeWorktree: (root: string, worktreePath: string) => Promise<void>;
  findManagedRootByPath: (path: string) => ManagedRootPayload | null;
  openGitDiff: (rootID: string, item: GitStatusItem, options?: { preserveRelatedSelection?: boolean; repoPath?: string; }) => Promise<void>;
  actionHandlers: ActionHandlers;
  relatedSessionRootId: string | null;
  relatedSessionSnapshot: any;
  relatedSessionKey: any;
  relatedSelectedPath: string;
  relatedSelectedFileKey: string;
  selectedKanbanTask: KanbanTask | null;
  selectedSessionRelatedFiles: { path: string; name: string; head: string; repo_path: string; repo_name: string; repo_kind: string; root_id: string; }[];
  selectedSessionRelatedFileGroups: { key: string; head: string; repoPath: string; repoName: string; repoKind: string; files: { path: string; name: string; head: string; repo_path: string; repo_name: string; repo_kind: string; root_id: string; }[]; }[];
  selectedRelatedFileStatsByKey: Record<string, RelatedFileStat>;
  gitFileStatsByPath: Record<string, GitFileStat>;
  handleSelectedSessionFileClick: (target: string | RelatedFileClickTarget) => void;
  handleRemoveSessionRelatedFile: (rootID: string | null | undefined, sessionKey: string | undefined, path: string, head?: string, repoPath?: string, repoKind?: string) => Promise<void>;
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
  setGitStatusExpandedByRoot: React.Dispatch<React.SetStateAction<Record<string, boolean>>>;
  setGitHistoryExpandedByRoot: React.Dispatch<React.SetStateAction<Record<string, Record<string, boolean>>>>;
  refreshGitStatus: (rootID: string) => Promise<GitStatusPayload | null>;
  refreshGitHistory: (rootID: string, options?: { force?: boolean; waitForIncremental?: boolean; }) => Promise<GitHistoryPayload | null>;
  loadMoreGitHistory: (targetRootID?: string) => Promise<void>;
  openGitCommitDiff: (rootID: string, commit: GitHistoryItem, item: GitStatusItem) => Promise<void>;
  handleGitPull: (rootID: string) => Promise<void>;
  handleGitPush: (rootID: string) => Promise<void>;
  handleGitCommit: (rootID: string, message: string) => Promise<void>;
  handleGitStageItem: (rootID: string, item: GitStatusItem) => Promise<void>;
  handleGitUnstageItem: (rootID: string, item: GitStatusItem) => Promise<void>;
  handleGitDiscardItem: (rootID: string, item: GitStatusItem) => Promise<void>;
  switchGitBranch: (rootID: string, branch: string) => Promise<void>;
  treeSortMode: DirectorySortMode;
};

/**
 * 这三块都是纯 props 转发 —— 它们存在的意义是把「一大坨状态」收成三个具名渲染器，
 * 让上层组件不必知道这些状态的来路。放在同一个 hook 里是因为它们被同一处 JSX 消费，
 * 分开只会让 App.tsx 多两次解构。
 *
 * related 那块的两个「键」不是一回事：`relatedSelectedFileKey` 是当前选中的那个文件，
 * `relatedFileSelectionKey` 是**由选中项算出来的**归一化键（模块级纯函数），
 * 组件用后者做高亮比对，所以两个都得传。
 */
export function useAppRootContentRenderers(params: UseAppRootContentRenderersParams) {
  const { currentRootId, scopedRootKey, managedRootByIdRef, relatedWorktree, worktreeItemsByRoot, worktreeLoadingByRoot, worktreeErrorByRoot, expandedWorktreeByRoot, worktreeStatusByPath, worktreeStatusLoadingByPath, toggleProjectTreeWorktree, findManagedRootByPath, openGitDiff, actionHandlers, relatedSessionRootId, relatedSessionSnapshot, relatedSessionKey, relatedSelectedPath, relatedSelectedFileKey, selectedKanbanTask, selectedSessionRelatedFiles, selectedSessionRelatedFileGroups, selectedRelatedFileStatsByKey, gitFileStatsByPath, handleSelectedSessionFileClick, handleRemoveSessionRelatedFile, gitStatus, gitStatusByRoot, gitStatusLoading, gitStatusLoadingByRoot, gitHistory, gitHistoryByRoot, gitHistoryLoading, gitHistoryLoadingMore, gitHistoryLoadingByRoot, gitStatusExpandedByRoot, gitHistoryExpandedByRoot, setGitStatusExpandedByRoot, setGitHistoryExpandedByRoot, refreshGitStatus, refreshGitHistory, loadMoreGitHistory, openGitCommitDiff, handleGitPull, handleGitPush, handleGitCommit, handleGitStageItem, handleGitUnstageItem, handleGitDiscardItem, switchGitBranch, treeSortMode } = params;

  const renderRootWorktreeContent = (root: string): React.ReactNode => (
    <RootWorktreeContentView
      root={root}
      currentRootId={currentRootId}
      scopedRootKey={scopedRootKey}
      managedRootByIdRef={managedRootByIdRef}
      relatedWorktree={relatedWorktree}
      worktrees={{
        worktreeItemsByRoot,
        worktreeLoadingByRoot,
        worktreeErrorByRoot,
        expandedWorktreeByRoot,
        worktreeStatusByPath,
        worktreeStatusLoadingByPath,
        toggleProjectTreeWorktree,
      }}
      findManagedRootByPath={findManagedRootByPath}
      openGitDiff={openGitDiff}
      actionHandlers={actionHandlers}
    />
  );

  const renderRootRelatedContent = (root: string): React.ReactNode => (
    <RootRelatedContentView
      root={root}
      currentRootId={currentRootId}
      relatedSessionRootId={relatedSessionRootId}
      relatedSessionSnapshot={relatedSessionSnapshot}
      relatedSessionKey={relatedSessionKey}
      relatedSelectedPath={relatedSelectedPath}
      relatedSelectedFileKey={relatedSelectedFileKey}
      relatedFileSelectionKey={relatedFileSelectionKey}
      managedRootByIdRef={managedRootByIdRef}
      selectedKanbanTask={selectedKanbanTask}
      selectedSessionRelatedFiles={selectedSessionRelatedFiles}
      selectedSessionRelatedFileGroups={selectedSessionRelatedFileGroups}
      selectedRelatedFileStatsByKey={selectedRelatedFileStatsByKey}
      gitFileStatsByPath={gitFileStatsByPath}
      normalizePath={normalizePath}
      handleSelectedSessionFileClick={handleSelectedSessionFileClick}
      handleRemoveSessionRelatedFile={handleRemoveSessionRelatedFile}
    />
  );

  const renderRootGitContent = (root: string): React.ReactNode => (
    <RootGitContentView
      root={root}
      currentRootId={currentRootId}
      scopedRootKey={scopedRootKey}
      managedRootByIdRef={managedRootByIdRef}
      gitStatus={gitStatus}
      gitStatusByRoot={gitStatusByRoot}
      gitStatusLoading={gitStatusLoading}
      gitStatusLoadingByRoot={gitStatusLoadingByRoot}
      gitHistory={gitHistory}
      gitHistoryByRoot={gitHistoryByRoot}
      gitHistoryLoading={gitHistoryLoading}
      gitHistoryLoadingMore={gitHistoryLoadingMore}
      gitHistoryLoadingByRoot={gitHistoryLoadingByRoot}
      gitStatusExpandedByRoot={gitStatusExpandedByRoot}
      gitHistoryExpandedByRoot={gitHistoryExpandedByRoot}
      setGitStatusExpandedByRoot={setGitStatusExpandedByRoot}
      setGitHistoryExpandedByRoot={setGitHistoryExpandedByRoot}
      refreshGitStatus={refreshGitStatus}
      refreshGitHistory={refreshGitHistory}
      loadMoreGitHistory={loadMoreGitHistory}
      openGitDiff={openGitDiff}
      openGitCommitDiff={openGitCommitDiff}
      handleGitPull={handleGitPull}
      handleGitPush={handleGitPush}
      handleGitCommit={handleGitCommit}
      handleGitStageItem={handleGitStageItem}
      handleGitUnstageItem={handleGitUnstageItem}
      handleGitDiscardItem={handleGitDiscardItem}
      switchGitBranch={switchGitBranch}
      actionHandlers={actionHandlers}
      projectSortMode={treeSortMode}
    />
  );

  return {
    renderRootWorktreeContent,
    renderRootRelatedContent,
    renderRootGitContent,
  };
}
