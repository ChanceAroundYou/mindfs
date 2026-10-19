import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import type { FileEditStore } from "../services/fileEditing";
import { DEFAULT_DIRECTORY_SORT_MODE } from "../services/prefs/directorySort";
import { normalizeUpdateState, type ManagedRootPayload } from "./appMisc";
import { MainViewMode, isDirectorySortMode, readURLState } from "./appPath";
import { DIRECTORY_SORT_OVERRIDES_STORAGE_KEY, TREE_SORT_STORAGE_KEY } from "./appStorage";
import type { AttachedFileContext, ViewerSelection, WSStatus } from "./appSession";
import type { DirectorySortMode, FileEntry } from "../services/prefs/directorySort";
import type { GitDiffPayload } from "../services/git";
import type { FilePayload } from "../services/file";
import type { PluginSourceBundle } from "../plugins/manager";
import type { UpdateState } from "../services/update";
import type { ProjectTreeTab } from "../components/file/fileTreeTypes";
import { useGitData } from "./useGitData";
import { useProjectTreeWorktrees } from "./useProjectTreeWorktrees";

type TaskState = ReturnType<typeof import("./useTaskWorkspaceState").useTaskWorkspaceState>;
type Ref<T> = { current: T };

type UseAppFileWorkspaceStateParams = {
  currentRootId: string | null;
  currentRootIdRef: Ref<string | null>;
  currentRootNodeId: string | null;
  currentRootNodeIdRef: Ref<string | null>;
  managedRootByIdRef: Ref<Record<string, ManagedRootPayload>>;
  scopedRootKey: (rootId: string) => string;
  getNodeIdForRoot: (rootId: string) => string | undefined;
  mainView: MainViewMode;
  fileEditStore: FileEditStore;
  knownTaskWorktreePathsRef: TaskState["knownTaskWorktreePathsRef"];
  refreshTaskWorktreeRef: TaskState["refreshTaskWorktreeRef"];
};

export function useAppFileWorkspaceState({
  currentRootId,
  currentRootIdRef,
  currentRootNodeId,
  currentRootNodeIdRef,
  managedRootByIdRef,
  scopedRootKey,
  getNodeIdForRoot,
  mainView,
  fileEditStore,
  knownTaskWorktreePathsRef,
  refreshTaskWorktreeRef,
}: UseAppFileWorkspaceStateParams) {
  const [editDraftRequest, setEditDraftRequest] = useState<{
    id: number;
    content: string;
  } | null>(null);
  const [entriesByPath, setEntriesByPath] = useState<
    Record<string, FileEntry[]>
  >({});
  const entriesByPathRef = useRef<Record<string, FileEntry[]>>({});
  const [expanded, setExpanded] = useState<string[]>([]);
  const [selectedDir, setSelectedDir] = useState<string | null>(null);
  const [selectedDirKey, setSelectedDirKey] = useState<string | null>(null);
  const [mainEntries, setMainEntries] = useState<FileEntry[]>([]);
  const [mainDirectoryError, setMainDirectoryError] = useState("");
  const mainEntriesRef = useRef<FileEntry[]>([]);
  mainEntriesRef.current = mainEntries;
  const mainDirectoryErrorRef = useRef("");
  mainDirectoryErrorRef.current = mainDirectoryError;
  const isGitRepo = useCallback(
    (rootID: string) => managedRootByIdRef.current[rootID]?.is_git_repo === true,
    [],
  );

  const {
    gitStatus,
    setGitStatus,
    gitStatusLoading,
    setGitStatusLoading,
    gitHistory,
    setGitHistory,
    gitHistoryLoading,
    setGitHistoryLoading,
    gitHistoryLoadingMore,
    gitStatusByRoot,
    setGitStatusByRoot,
    gitStatusLoadingByRoot,
    gitHistoryByRoot,
    setGitHistoryByRoot,
    gitHistoryLoadingByRoot,
    gitStatusExpandedByRoot,
    setGitStatusExpandedByRoot,
    gitHistoryExpandedByRoot,
    setGitHistoryExpandedByRoot,
    refreshGitStatus,
    refreshGitHistory,
    loadMoreGitHistory,
  } = useGitData({
    currentRootId,
    currentRootIdRef,
    scopedRootKey,
    getNodeIdForRoot,
    isGitRepo,
  });
  const [gitDiff, setGitDiff] = useState<GitDiffPayload | null>(null);
  const [relatedSelectedFileKey, setRelatedSelectedFileKey] = useState("");
  const [treeSortMode, setTreeSortMode] = useState<DirectorySortMode>(() => {
    if (typeof window === "undefined") {
      return DEFAULT_DIRECTORY_SORT_MODE;
    }
    const saved = window.localStorage.getItem(TREE_SORT_STORAGE_KEY);
    return isDirectorySortMode(saved) ? saved : DEFAULT_DIRECTORY_SORT_MODE;
  });
  const [directorySortOverrides, setDirectorySortOverrides] = useState<
    Record<string, DirectorySortMode>
  >(() => {
    if (typeof window === "undefined") {
      return {};
    }
    try {
      const saved = window.localStorage.getItem(
        DIRECTORY_SORT_OVERRIDES_STORAGE_KEY,
      );
      if (!saved) {
        return {};
      }
      const parsed = JSON.parse(saved) as Record<string, string>;
      return Object.fromEntries(
        Object.entries(parsed).filter(([, value]) =>
          isDirectorySortMode(value),
        ),
      ) as Record<string, DirectorySortMode>;
    } catch {
      return {};
    }
  });
  /* 冷启动落点（2026-10-03 用户决策，取代 R10 的「恢复上次模式」）：
     **有会话 id 进那个对话，否则落工作台。** localStorage 与 URL 的 view 两条路都会
     把「上次用的模式」带进冷启动：mindfs-main-view 每次切换都写，view 也被
     replaceURLState 每次导航抄进地址栏。所以「兜底值是 workspace」从来不是启动行为，
     只是缺省时的读数 —— 唯一的启动指令是 URL 的 session，不是 view。
     用户在会话内切到文件/对话仍然完全正常，只是不跨冷启动继承。 */
  const mainViewRef = useRef<MainViewMode>(mainView);
  // 从 chat 返回时回到上一个非 chat 模式（瞬态，不落盘）。初值与冷启动落点一致。
  const lastNonChatViewRef = useRef<MainViewMode>("workspace");
  const [status, setStatus] = useState<WSStatus>("disconnected");
  const [file, setFile] = useState<FilePayload | null>(null);
  const currentFileEditing = useSyncExternalStore(fileEditStore.subscribe, () => !!file?.root && fileEditStore.has(file.root, file.path));
  const [viewerSelection, setViewerSelection] =
    useState<ViewerSelection | null>(null);
  const [attachedFileContext, setAttachedFileContext] =
    useState<AttachedFileContext | null>(null);
  const [pluginVersion, setPluginVersion] = useState(0);
  const [pluginLoading, setPluginLoading] = useState(false);
  const [pendingPluginTrust, setPendingPluginTrust] = useState<{
    rootId: string;
    bundle: PluginSourceBundle;
  } | null>(null);
  const [pluginBypass, setPluginBypass] = useState(false);
  const [pluginQuery, setPluginQuery] = useState<Record<string, string>>(
    () => readURLState().pluginQuery,
  );
  const pluginQueryRef = useRef<Record<string, string>>(
    readURLState().pluginQuery,
  );
  // 同上：返回值会被下游层与 JSX 组件按字面量 true 接收，不能拓宽成 boolean。
  const showHiddenFiles: true = true;
  const [projectTreeTab, setProjectTreeTab] = useState<ProjectTreeTab>("files");
  const {
    worktreeItemsByRoot,
    worktreeLoadingByRoot,
    worktreeErrorByRoot,
    expandedWorktreeByRoot,
    worktreeStatusByPath,
    worktreeStatusLoadingByPath,
    loadProjectTreeWorktrees,
    loadProjectTreeWorktreeStatus,
    toggleProjectTreeWorktree,
    expandProjectTreeWorktree,
    refreshProjectTreeWorktrees,
    refreshTaskWorktree,
  } = useProjectTreeWorktrees({
    currentRootId,
    currentRootIdRef,
    projectTreeTab,
    scopedRootKey,
    getNodeIdForRoot,
    knownTaskWorktreePathsRef,
  });
  // 把后置初始化的 refreshTaskWorktree 转发给 useTaskBoard（通过 ref 避免 TDZ）
  refreshTaskWorktreeRef.current = refreshTaskWorktree;
  const [updateState, setUpdateState] = useState<UpdateState>(() =>
    normalizeUpdateState(null),
  );
  const [updateSubmitting, setUpdateSubmitting] = useState(false);

  return {
    editDraftRequest, setEditDraftRequest, entriesByPath, setEntriesByPath, entriesByPathRef,
    expanded, setExpanded, selectedDir, setSelectedDir, selectedDirKey, setSelectedDirKey,
    mainEntries, setMainEntries, mainDirectoryError, setMainDirectoryError, mainEntriesRef,
    mainDirectoryErrorRef, isGitRepo, gitStatus, setGitStatus, gitStatusLoading,
    setGitStatusLoading, gitHistory, setGitHistory, gitHistoryLoading, setGitHistoryLoading,
    gitHistoryLoadingMore, gitStatusByRoot, setGitStatusByRoot, gitStatusLoadingByRoot,
    gitHistoryByRoot, setGitHistoryByRoot, gitHistoryLoadingByRoot, gitStatusExpandedByRoot,
    setGitStatusExpandedByRoot, gitHistoryExpandedByRoot, setGitHistoryExpandedByRoot,
    refreshGitStatus, refreshGitHistory, loadMoreGitHistory, gitDiff, setGitDiff,
    relatedSelectedFileKey, setRelatedSelectedFileKey, treeSortMode, setTreeSortMode,
    directorySortOverrides, setDirectorySortOverrides, mainViewRef, lastNonChatViewRef,
    status, setStatus, file, setFile, currentFileEditing, viewerSelection, setViewerSelection,
    attachedFileContext, setAttachedFileContext, pluginVersion, setPluginVersion, pluginLoading,
    setPluginLoading, pendingPluginTrust, setPendingPluginTrust, pluginBypass, setPluginBypass,
    pluginQuery, setPluginQuery, pluginQueryRef, showHiddenFiles,
    projectTreeTab, setProjectTreeTab, worktreeItemsByRoot,
    worktreeLoadingByRoot, worktreeErrorByRoot, expandedWorktreeByRoot, worktreeStatusByPath,
    worktreeStatusLoadingByPath, loadProjectTreeWorktrees, loadProjectTreeWorktreeStatus,
    toggleProjectTreeWorktree, expandProjectTreeWorktree, refreshProjectTreeWorktrees,
    refreshTaskWorktree, updateState, setUpdateState, updateSubmitting, setUpdateSubmitting,
  };
}
