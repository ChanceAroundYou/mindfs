import { useCallback, useEffect, useRef } from "react";
import { sessionService } from "../services/session";
import type { Session } from "../services/session";
import { scopeSessionKey, treeKey } from "../shared/scope";
import { mergeSessionItems } from "../services/sessionListMerge";
import { fetchAgents, restartAgent } from "../services/agents";
import type { AgentStatus } from "../services/agents";
import { ManagedRootPayload, SESSION_PAGE_SIZE } from "./appMisc";
import { useAppRootResolution } from "./useAppRootResolution";
import { useAppSessionSelection } from "./useAppSessionSelection";
import { useAppSessionKeyState } from "./useAppSessionKeyState";
import { useAppSessionReload } from "./useAppSessionReload";
import { useAppBootstrapActions } from "./useAppBootstrapActions";
import { useAppFormatDirectoryLoadError } from "./useAppFormatDirectoryLoadError";
import { installBackNavigation } from "../hooks/useBackNavigation";
import { AttachedFileContext, MultiProjectSessionGroup, PendingSend, SessionItem, SlashCommandResult, WSStatus, toSessionItem } from "./appSession";
import { MainViewMode, rootNodeKey } from "./appPath";
import { useSendMessage } from "./useSendMessage";
import { useSessionLifecycle } from "./useSessionLifecycle";
import { useExternalSessionImport } from "./useExternalSessionImport";
import { useGitActions } from "./useGitActions";
import { useSessionSearch } from "./useSessionSearch";
import { useAppAgentActions } from "./useAppAgentActions";
import { useSessionStreamCache } from "./useSessionStreamCache";
import { useMultiProjectSessions } from "./useMultiProjectSessions";
import { useSessionArchive } from "./useSessionArchive";
import { useFileRefresh } from "./useFileRefresh";
import { useArchiveSync } from "./useArchiveSync";
import { useReplyingSessionPoll } from "./useReplyingSessionPoll";
import { useSessionPending } from "./useSessionPending";
import { useSessionMetadata } from "./useSessionMetadata";
import { useSessionListActions } from "./useSessionListActions";
import { FileEditStore } from "../services/fileEditing";
import type { FileEntry } from "../services/prefs/directorySort";
import type { FilePayload } from "../services/file";
import type { GitDiffPayload } from "../services/git";
import type { MessageKey, MessageParams } from "../i18n";
import { PluginManager } from "../plugins/manager";
import type { UpdateState } from "../services/update";
import type { UploadProgress } from "../services/upload";

/**
 * 见 docs/plans/app-tsx-under-1000.md：分层聚合 hook。
 */
export type UseAppSessionLayerParams = {
  MULTI_PROJECT_RELOAD_DEBOUNCE_MS: 300;
  applyPinSnapshot: <T extends { root_id?: string; pinned_at?: string | null; }>(items: T[], rootId: string) => T[];
  archiveOpen: boolean;
  archiveReloadToken: number;
  attachedFileContext: AttachedFileContext | null;
  boundSessionByRootRef: React.RefObject<Record<string, string | null>>;
  cacheVersion: number;
  cancelRequestedBySessionRef: React.RefObject<Record<string, boolean>>;
  currentRootId: string | null;
  currentRootIdRef: React.RefObject<string | null>;
  currentRootNodeId: string | null;
  currentRootNodeIdRef: React.RefObject<string | null>;
  currentSessionRef: React.RefObject<SessionItem | null>;
  didInitRef: React.RefObject<boolean>;
  directoryUploadAbortRef: React.RefObject<AbortController | null>;
  drawerOpenByRootRef: React.RefObject<Record<string, boolean>>;
  drawerSessionByRootRef: React.RefObject<Record<string, SessionItem | null>>;
  enforceSessionCacheBound: () => void;
  file: FilePayload | null;
  fileCursorRef: React.RefObject<number>;
  fileEditStore: FileEditStore;
  fileOpenRequestRef: React.RefObject<number>;
  fileRef: React.RefObject<FilePayload | null>;
  fileScrollPositionsRef: React.RefObject<Record<string, number>>;
  getNodeIdForRoot: (rootId: string) => string | undefined;
  gitDiff: GitDiffPayload | null;
  handleSelectSessionRef: React.RefObject<((session: any, options?: { preserveTaskSelection?: boolean; preserveMainView?: boolean; }) => Promise<void>) | null>;
  interactionModeRef: React.RefObject<"main" | "drawer">;
  invalidTreeCacheKeysRef: React.RefObject<Set<string>>;
  isMobile: boolean;
  lastMainSessionSnapshotRef: React.RefObject<Session | null>;
  listLoadErrorAtRef: React.RefObject<number>;
  loadedSessionRef: React.RefObject<Record<string, boolean>>;
  loadingOlderSessions: boolean;
  loadingSessionRef: React.RefObject<Partial<Record<string, Promise<import("/home/xiaokubao/projects/mindfs/.worktree/task-35/web/src/services/sessionTypes").SyncSessionResult>>>>;
  mainViewRef: React.RefObject<MainViewMode>;
  managedRootByIdRef: React.RefObject<Record<string, ManagedRootPayload>>;
  managedRootByKeyRef: React.RefObject<Record<string, ManagedRootPayload>>;
  managedRootIdsRef: React.RefObject<Set<string>>;
  multiProjectLoadSeqRef: React.RefObject<number>;
  multiProjectPendingByKey: Record<string, boolean>;
  multiProjectPendingRef: React.RefObject<Record<string, boolean>>;
  multiProjectSessionGroupsRef: React.RefObject<MultiProjectSessionGroup[]>;
  multiProjectSessionsEnabled: true;
  notifyNodeLoadFailedRef: React.RefObject<(node: { id: string; name: string; }) => void>;
  pendingBySessionRef: React.RefObject<Record<string, PendingSend>>;
  pendingDraftRef: React.RefObject<PendingSend | null>;
  pendingPlanMode: boolean;
  pendingRequestRef: React.RefObject<Record<string, PendingSend>>;
  pluginBypassRef: React.RefObject<boolean>;
  pluginManagerRef: React.RefObject<PluginManager>;
  pluginQuery: Record<string, string>;
  pluginQueryRef: React.RefObject<Record<string, string>>;
  refreshGitHistory: (rootID: string, options?: { force?: boolean; waitForIncremental?: boolean; }) => Promise<import("/home/xiaokubao/projects/mindfs/.worktree/task-35/web/src/services/git").GitHistoryPayload | null>;
  replaceURLState: (next: import("/home/xiaokubao/projects/mindfs/.worktree/task-35/web/src/app/appPath").URLState) => void;
  resetLocksForRootTransition: (targetRoot: string | null | undefined) => void;
  resetSessionLockForRoot: (rootID: string | null | undefined) => void;
  scopedRootKey: (rootId: string) => string;
  selectRootNode: (rootId: string, nodeId?: string) => void;
  selectedDirRef: React.RefObject<string | null>;
  selectedSessionByRootRef: React.RefObject<Record<string, string | null>>;
  selectedSessionRef: React.RefObject<SessionItem | null>;
  sessionCacheAccessRef: React.RefObject<Record<string, number>>;
  sessionCacheRef: React.RefObject<Record<string, Session>>;
  sessionListAbortRef: React.RefObject<AbortController | null>;
  sessionListLoadSeqRef: React.RefObject<number>;
  sessionListMode: "local" | "import";
  // 由 useAppStateLayer（状态/ref 层）持有并传入：该层的跨节点切换回调需要清掉它，见那里的注释。
  sessionListReloadTimerRef: React.RefObject<number | null>;
  sessionLoadFailureRef: React.RefObject<Record<string, { at: number; status: number; }>>;
  sessionSearchTargetCounterRef: React.RefObject<number>;
  sessions: SessionItem[];
  sessionsRef: React.RefObject<SessionItem[]>;
  setAgentsVersion: React.Dispatch<React.SetStateAction<number>>;
  setArchiveLoading: React.Dispatch<React.SetStateAction<boolean>>;
  setArchiveReloadToken: React.Dispatch<React.SetStateAction<number>>;
  setArchivedGroups: React.Dispatch<React.SetStateAction<MultiProjectSessionGroup[]>>;
  setAvailableAgents: React.Dispatch<React.SetStateAction<AgentStatus[]>>;
  setBoundSessionForRoot: (rootID: string | null | undefined, key: string | null) => void;
  setCacheVersion: React.Dispatch<React.SetStateAction<number>>;
  setCurrentRootId: React.Dispatch<React.SetStateAction<string | null>>;
  setDirectoryUploadProgress: React.Dispatch<React.SetStateAction<UploadProgress | null>>;
  setDrawerOpenForRoot: (rootID: string | null | undefined, open: boolean) => void;
  setDrawerSessionForRoot: (rootID: string | null | undefined, session: Session | SessionItem | null) => void;
  setEntriesByPath: React.Dispatch<React.SetStateAction<Record<string, FileEntry[]>>>;
  setExpanded: React.Dispatch<React.SetStateAction<string[]>>;
  setFile: React.Dispatch<React.SetStateAction<FilePayload | null>>;
  setGitDiff: React.Dispatch<React.SetStateAction<GitDiffPayload | null>>;
  setGitStatus: React.Dispatch<React.SetStateAction<import("/home/xiaokubao/projects/mindfs/.worktree/task-35/web/src/services/git").GitStatusPayload | null>>;
  setGitStatusByRoot: React.Dispatch<React.SetStateAction<Record<string, import("/home/xiaokubao/projects/mindfs/.worktree/task-35/web/src/services/git").GitStatusPayload | null>>>;
  setHasMoreSessions: React.Dispatch<React.SetStateAction<boolean>>;
  setInteractionMode: React.Dispatch<React.SetStateAction<"main" | "drawer">>;
  setIsLeftOpen: React.Dispatch<React.SetStateAction<boolean>>;
  setIsRightOpen: React.Dispatch<React.SetStateAction<boolean>>;
  setLoadingOlderSessions: React.Dispatch<React.SetStateAction<boolean>>;
  setMainDirectoryError: React.Dispatch<React.SetStateAction<string>>;
  setMainEntries: React.Dispatch<React.SetStateAction<FileEntry[]>>;
  setMultiProjectPendingByKey: React.Dispatch<React.SetStateAction<Record<string, boolean>>>;
  setMultiProjectSessionGroups: React.Dispatch<React.SetStateAction<MultiProjectSessionGroup[]>>;
  setMultiProjectSessionsLoading: React.Dispatch<React.SetStateAction<boolean>>;
  setOnboardingMainContentViewRoot: React.Dispatch<React.SetStateAction<string | null>>;
  setPendingPlanMode: React.Dispatch<React.SetStateAction<boolean>>;
  setPluginQuery: React.Dispatch<React.SetStateAction<Record<string, string>>>;
  setRelatedSelectedFileKey: React.Dispatch<React.SetStateAction<string>>;
  setSelectedDir: React.Dispatch<React.SetStateAction<string | null>>;
  setSelectedDirKey: React.Dispatch<React.SetStateAction<string | null>>;
  setSelectedKanbanTaskId: React.Dispatch<React.SetStateAction<string>>;
  setSelectedSession: React.Dispatch<React.SetStateAction<SessionItem | null>>;
  setSelectedSessionLoading: React.Dispatch<React.SetStateAction<boolean>>;
  setSessionListMode: React.Dispatch<React.SetStateAction<"local" | "import">>;
  setSessions: React.Dispatch<React.SetStateAction<SessionItem[]>>;
  setSlashCommandResults: React.Dispatch<React.SetStateAction<Record<string, SlashCommandResult>>>;
  setSyncingSessionKeys: React.Dispatch<React.SetStateAction<Set<string>>>;
  setUpdateState: React.Dispatch<React.SetStateAction<UpdateState>>;
  setUpdateSubmitting: React.Dispatch<React.SetStateAction<boolean>>;
  slashCommandResults: Record<string, SlashCommandResult>;
  staleSessionKeysRef: React.RefObject<Set<string>>;
  status: WSStatus;
  suppressedAutoBindSessionByRootRef: React.RefObject<Record<string, string | null>>;
  switchMainView: (mode: MainViewMode) => void;
  t: (key: MessageKey, params?: MessageParams) => string;
  treeFetchSeqRef: React.RefObject<Record<string, number>>;
  updateState: UpdateState;
};

/**
 * 本层由 App.tsx 原样搬出，hook 调用顺序与搬出前逐字一致。
 */
export function useAppSessionLayer(params: UseAppSessionLayerParams) {
  const { MULTI_PROJECT_RELOAD_DEBOUNCE_MS, applyPinSnapshot, archiveOpen, archiveReloadToken,
    attachedFileContext, boundSessionByRootRef, cacheVersion, cancelRequestedBySessionRef,
    currentRootId, currentRootIdRef, currentRootNodeId, currentRootNodeIdRef,
    currentSessionRef, didInitRef, directoryUploadAbortRef, drawerOpenByRootRef,
    drawerSessionByRootRef, enforceSessionCacheBound, file,
    fileCursorRef, fileEditStore, fileOpenRequestRef, fileRef,
    fileScrollPositionsRef, getNodeIdForRoot, gitDiff, handleSelectSessionRef,
    interactionModeRef, invalidTreeCacheKeysRef, isMobile, lastMainSessionSnapshotRef,
    listLoadErrorAtRef, loadedSessionRef, loadingOlderSessions, loadingSessionRef,
    mainViewRef, managedRootByIdRef, managedRootByKeyRef, managedRootIdsRef,
    multiProjectLoadSeqRef, multiProjectPendingByKey, multiProjectPendingRef, multiProjectSessionGroupsRef,
    multiProjectSessionsEnabled, notifyNodeLoadFailedRef, pendingBySessionRef, pendingDraftRef,
    pendingPlanMode, pendingRequestRef, pluginBypassRef, pluginManagerRef,
    pluginQuery, pluginQueryRef, refreshGitHistory, replaceURLState,
    resetLocksForRootTransition, resetSessionLockForRoot, scopedRootKey, selectRootNode,
    selectedDirRef, selectedSessionByRootRef, selectedSessionRef, sessionCacheAccessRef,
    sessionCacheRef, sessionListAbortRef, sessionListLoadSeqRef, sessionListMode,
    sessionListReloadTimerRef, sessionLoadFailureRef, sessionSearchTargetCounterRef, sessions, sessionsRef,
    setAgentsVersion, setArchiveLoading, setArchiveReloadToken, setArchivedGroups,
    setAvailableAgents, setBoundSessionForRoot, setCacheVersion, setCurrentRootId,
    setDirectoryUploadProgress, setDrawerOpenForRoot, setDrawerSessionForRoot, setEntriesByPath, setExpanded,
    setFile, setGitDiff, setGitStatus, setGitStatusByRoot,
    setHasMoreSessions, setInteractionMode, setIsLeftOpen, setIsRightOpen,
    setLoadingOlderSessions, setMainDirectoryError, setMainEntries, setMultiProjectPendingByKey,
    setMultiProjectSessionGroups, setMultiProjectSessionsLoading, setOnboardingMainContentViewRoot, setPendingPlanMode,
    setPluginQuery, setRelatedSelectedFileKey, setSelectedDir, setSelectedDirKey,
    setSelectedKanbanTaskId, setSelectedSession, setSelectedSessionLoading, setSessionListMode,
    setSessions, setSlashCommandResults, setSyncingSessionKeys, setUpdateState,
    setUpdateSubmitting, slashCommandResults, staleSessionKeysRef, status,
    suppressedAutoBindSessionByRootRef, switchMainView, t, treeFetchSeqRef,
    updateState } = params;

  const {
    handleStartUpdate,
    handleOnboardingStepChange,
  } = useAppBootstrapActions({
    currentRootId,
    didInitRef,
    isMobile,
    replaceURLState,
    selectedSessionRef,
    setFile,
    setGitDiff,
    setIsLeftOpen,
    setIsRightOpen,
    setOnboardingMainContentViewRoot,
    setSelectedDir,
    setSelectedSession,
    setSelectedSessionLoading,
    setUpdateState,
    setUpdateSubmitting,
    t,
    updateState,
  });

  /* Android 硬件返回键。刻意不接回调：installBackNavigation 内部一律走
     history.back()，让返回键汇进 popstate 那条路，和浏览器后退 / iOS 侧滑
     保持同一套语义。以前这里是独立的一套「关层 / 退视图」逻辑，两套各走一遍，
     一次返回要按两下才对。 */
  useEffect(() => installBackNavigation(), []);


  const rootSessionKey = useCallback(
    (rootId: string, sessionKey: string) =>
      scopeSessionKey(getNodeIdForRoot(rootId) ?? "", rootId, sessionKey),
    [getNodeIdForRoot],
  );
  const bumpCacheVersion = useCallback(() => setCacheVersion((v) => v + 1), []);
  // 流式高频 bump 合并：30ms 窗口内多次 bump 只触发一次 setState（渲染 1 次而非 N 次）。
  const bumpCacheVersionDebouncedRef = useRef<number | null>(null);
  const bumpCacheVersionDebounced = useCallback(() => {
    if (bumpCacheVersionDebouncedRef.current !== null) return;
    bumpCacheVersionDebouncedRef.current = window.setTimeout(() => {
      bumpCacheVersionDebouncedRef.current = null;
      bumpCacheVersion();
    }, 30);
  }, [bumpCacheVersion]);
  useEffect(
    () => () => {
      if (bumpCacheVersionDebouncedRef.current !== null) {
        window.clearTimeout(bumpCacheVersionDebouncedRef.current);
      }
    },
    [],
  );
  const applyPendingToMultiProjectGroups = useCallback(
    (groups: MultiProjectSessionGroup[], pendingByKey: Record<string, boolean>) =>
      groups.map((group) => ({
        ...group,
        sessions: group.sessions.map((session) => ({
          ...(session as any),
          pending: !!pendingByKey[rootSessionKey(group.rootId, session.key || session.session_key)],
        }) as SessionItem),
      })),
    [rootSessionKey],
  );
  const {
    clearSlashCommandResultForSession,
    setMultiProjectSessionPending,
  } = useAppSessionKeyState({
    rootSessionKey,
    setSlashCommandResults,
    setMultiProjectPendingByKey,
    multiProjectPendingRef,
    setMultiProjectSessionGroups,
    applyPendingToMultiProjectGroups,
  });
  const { resolveRootForSessionKey } = useAppRootResolution({
    currentRootIdRef,
    sessionCacheRef,
    rootSessionKey,
    boundSessionByRootRef,
    drawerSessionByRootRef,
  });
  const {
    getSessionSnapshot,
    resolvePendingForSession,

    clearLocalPendingForSession,
    markSessionStale,
    clearSessionStale,
    isSessionStale,
  } = useSessionPending({
    bumpCacheVersion,
    cacheVersion,
    currentRootIdRef,
    drawerSessionByRootRef,
    multiProjectPendingByKey,
    multiProjectPendingRef,
    pendingBySessionRef,
    rootSessionKey,
    scopedRootKey,
    selectedSessionRef,
    sessionCacheRef,
    sessionLoadFailureRef,
    setDrawerSessionForRoot,
    setMultiProjectSessionPending,
    setSelectedSession,
    setSessions,
    staleSessionKeysRef,
  });


  // 方案 C 重锚定序号：每次 restoreActiveSession 换窗时递增，viewer 按 _anchoredAt 一次性应用。
  const anchorSeqRef = useRef(0);

  const {
    restoreActiveSession,
    syncSessionHeaderFromListItem,
    loadSessionsForRoot,
    loadChildSessionsForParent,
    handleSelectSession,
    tryShowBoundSessionForRoot,
    handleRenameSession,
    handleSyncSession,
  } = useSessionLifecycle({
    anchorSeqRef,
    applyPendingToMultiProjectGroups,
    applyPinSnapshot,
    boundSessionByRootRef,
    bumpCacheVersion,
    clearLocalPendingForSession,
    clearSessionStale,
    currentRootId,
    currentRootIdRef,
    currentRootNodeIdRef,
    drawerSessionByRootRef,
    enforceSessionCacheBound,
    file,
    fileCursorRef,
    getNodeIdForRoot,
    isSessionStale,
    listLoadErrorAtRef,
    loadedSessionRef,
    loadingSessionRef,
    mainViewRef,
    multiProjectPendingRef,
    pluginQuery,
    replaceURLState,
    resolvePendingForSession,
    rootSessionKey,
    scopedRootKey,
    selectRootNode,
    selectedSessionByRootRef,
    selectedSessionRef,
    sessionCacheAccessRef,
    sessionCacheRef,
    sessionListAbortRef,
    sessionListLoadSeqRef,
    sessionLoadFailureRef,
    sessionSearchTargetCounterRef,
    sessions,
    setBoundSessionForRoot,
    setCurrentRootId,
    setDrawerOpenForRoot,
    setDrawerSessionForRoot,
    setFile,
    setGitDiff,
    setHasMoreSessions,
    setInteractionMode,
    setIsLeftOpen,
    setIsRightOpen,
    setMainEntries,
    setMultiProjectSessionGroups,
    setPluginQuery,
    setSelectedDir,
    setSelectedDirKey,
    setSelectedKanbanTaskId,
    setSelectedSession,
    setSelectedSessionLoading,
    setSessions,
    setSyncingSessionKeys,
    status,
    switchMainView,
  });

  const {
    updateSessionRelatedFilesForKey,
    updateSessionRelatedWorktreeForKey,
    updateSessionAgentForKey,
    getInputSession,
    handleSetPlanMode,
  } = useSessionMetadata({
    bumpCacheVersion,
    currentRootIdRef,
    drawerOpenByRootRef,
    drawerSessionByRootRef,
    lastMainSessionSnapshotRef,
    rootSessionKey,
    scopedRootKey,
    selectedSessionRef,
    sessionCacheRef,
    sessions,
    setDrawerSessionForRoot,
    setPendingPlanMode,
    setSelectedSession,
    syncSessionHeaderFromListItem,
    t,
  });


  const {
    promotePendingSessionForRoot,
    loadMultiProjectSessionGroups,
    loadMoreMultiProjectSessions,
  } = useMultiProjectSessions({
    applyPendingToMultiProjectGroups,
    applyPinSnapshot,
    boundSessionByRootRef,
    bumpCacheVersion,
    currentRootIdRef,
    currentRootNodeId,
    drawerSessionByRootRef,
    getNodeIdForRoot,
    loadedSessionRef,
    loadingSessionRef,
    managedRootByIdRef,
    managedRootByKeyRef,
    multiProjectLoadSeqRef,
    multiProjectPendingRef,
    multiProjectSessionsEnabled,
    notifyNodeLoadFailedRef,
    rootSessionKey,
    scopedRootKey,
    selectRootNode,
    selectedSessionByRootRef,
    selectedSessionRef,
    sessionCacheRef,
    sessions,
    setBoundSessionForRoot,
    setDrawerSessionForRoot,
    setMultiProjectSessionGroups,
    setMultiProjectSessionPending,
    setMultiProjectSessionsLoading,
    setSelectedSession,
    setSessions,
  });

  const {
    resolveRuntimeMetaForSession,
    appendAgentChunkForSession,
    appendThoughtChunkForSession,
    appendToolCallForSession,
    appendTodoUpdateForSession,
    appendPlanUpdateForSession,
    appendCompactNoticeForSession,
    attachContextWindowToLatestAssistant,
  } = useSessionStreamCache({
    sessionCacheRef,
    rootSessionKey,
    bumpCacheVersionDebounced,
    currentSessionRef,
    selectedSessionRef,
    bumpCacheVersion,
    currentRootIdRef,
    drawerSessionByRootRef,
    scopedRootKey,
    setDrawerSessionForRoot,
    setSelectedSession,
    setSessions,
  });

  const { formatDirectoryLoadError } = useAppFormatDirectoryLoadError({ t });

  const treeCacheKey = useCallback(
    (rootID: string, dirPath: string) =>
      treeKey(getNodeIdForRoot(rootID) ?? "", rootID, dirPath || "."),
    [getNodeIdForRoot],
  );




  const {
    refreshTreeDir,
    refreshCurrentFileContent,
  } = useFileRefresh({
    currentRootIdRef,
    file,
    fileCursorRef,
    fileEditStore,
    fileRef,
    formatDirectoryLoadError,
    getNodeIdForRoot,
    invalidTreeCacheKeysRef,
    pluginBypassRef,
    pluginManagerRef,
    pluginQueryRef,
    selectedDirRef,
    setEntriesByPath,
    setFile,
    setMainDirectoryError,
    setMainEntries,
    treeCacheKey,
    treeFetchSeqRef,
  });

  // WS 高频事件（session.done / ws.reconnected / session.imported / session.created）都会触发会话列表全量重拉，
  // 合并 300ms 窗口内的多次调用为最后一次，避免 agent 密集工具调用时连发 /api/sessions。
  const {
    scheduleSessionListReload,
    handleLoadOlderSessions,
  } = useAppSessionReload({
    sessionListReloadTimerRef,
    loadSessionsForRoot,
    currentRootIdRef,
    sessionsRef,
    loadingOlderSessions,
    setLoadingOlderSessions,
    getNodeIdForRoot,
    setHasMoreSessions,
    setSessions,
    applyPinSnapshot,
  });



  const multiProjectReloadTimerRef = useRef<number | null>(null);

  /**
   * 合并窗口内多次「多项目会话重拉」为最后一次。
   *
   * 什么时候会连发：session.created 现在是**所有**建会话路径的统一信号了
   * （看板任务、定时任务、子会话、fork），而一次看板动作可能连带建多个会话；
   * agent 密集起子代理时更是一串 created。原来这些路径压根不发 created，所以
   * 裸调 loadMultiProjectSessionGroups 不觉得多；统一信号一加，
   * 每次 created 都会扇出一次 `/api/sessions?multi_root=1`（逐节点各一次），
   * 密集建会话时能把接口打爆。
   *
   * 窗口取 300ms，与 scheduleSessionListReload 同一档：单个节点的
   * `/api/sessions?multi_root=1` 实测 100–400ms，300ms 足够合并一串事件，
   * 又短到用户感知不到延迟。
   */
  const scheduleMultiProjectSessionReload = useCallback(() => {
    if (multiProjectReloadTimerRef.current !== null) {
      window.clearTimeout(multiProjectReloadTimerRef.current);
    }
    multiProjectReloadTimerRef.current = window.setTimeout(() => {
      multiProjectReloadTimerRef.current = null;
      void loadMultiProjectSessionGroups();
    }, MULTI_PROJECT_RELOAD_DEBOUNCE_MS);
  }, [loadMultiProjectSessionGroups]);

  // 卸载时清掉挂着的定时器：否则切走页面后还会打一次扇出
  useEffect(
    () => () => {
      if (multiProjectReloadTimerRef.current !== null) {
        window.clearTimeout(multiProjectReloadTimerRef.current);
        multiProjectReloadTimerRef.current = null;
      }
    },
    [],
  );


  const { refreshMultiProjectReplyingSessions } = useReplyingSessionPoll({
    applyPendingToMultiProjectGroups,
    currentRootNodeIdRef,
    loadMultiProjectSessionGroups,
    managedRootByKeyRef,
    multiProjectPendingRef,
    multiProjectSessionsEnabled,
    setMultiProjectPendingByKey,
    setMultiProjectSessionGroups,
  });


  const {
    sessionSearchOpen,
    sessionSearchResultsMode,
    sessionSearchQuery,
    sessionSearchResults,
    sessionSearchLoading,
    executeSessionSearch,
    openSessionSearch,
    closeSessionSearch,
    toggleSessionSearch,
    handleSearchQueryChange,
    setSessionSearchResults,
  } = useSessionSearch({
    currentRootId,
    sessionListMode,
    multiProjectSessionsEnabled,
    getNodeIdForRoot,
  });

  const {
    openGitDiff,
    openGitCommitDiff,
    switchGitBranch,
    handleGitPull,
    handleGitPush,
    handleGitCommit,
    handleGitStageItem,
    handleGitUnstageItem,
    handleGitDiscardItem,
  } = useGitActions({
    currentRootIdRef,
    getNodeIdForRoot,
    scopedRootKey,
    selectedDirRef,
    fileOpenRequestRef,
    refreshGitHistory,
    refreshTreeDir,
    resetLocksForRootTransition,
    switchMainView,
    replaceURLState,
    isMobile,
    setGitStatus,
    setGitStatusByRoot,
    setGitDiff,
    setFile,
    setCurrentRootId,
    setIsLeftOpen,
    setRelatedSelectedFileKey,
    setSelectedSession,
    setSelectedSessionLoading,
  });


  const {
    handleCreateBlankFile,
    handleTreeUpload,
    handleSelectSessionAndClose,
    handlePinSession,
    pruneSubtreeFromSessionState,
  } = useSessionListActions({
    currentRootIdRef,
    directoryUploadAbortRef,
    fileRef,
    getNodeIdForRoot,
    isMobile,
    multiProjectSessionGroupsRef,
    selectedDirRef,
    sessionsRef,
    setDirectoryUploadProgress,
    setExpanded,
    setIsRightOpen,
    setMultiProjectSessionGroups,
    setSessions,
    handleSelectSession,
    refreshTreeDir,
    t,
  });

  const {
    handleDeleteSession,
    handleArchiveSession,
  } = useSessionArchive({
    boundSessionByRootRef,
    bumpCacheVersion,
    cancelRequestedBySessionRef,
    currentRootIdRef,
    drawerSessionByRootRef,
    file,
    fileCursorRef,
    fileRef,
    getNodeIdForRoot,
    loadedSessionRef,
    loadingSessionRef,
    managedRootByIdRef,
    multiProjectSessionGroupsRef,
    pendingBySessionRef,
    pluginQuery,
    pruneSubtreeFromSessionState,
    replaceURLState,
    resetSessionLockForRoot,
    rootSessionKey,
    scopedRootKey,
    selectedSessionByRootRef,
    selectedSessionRef,
    sessionCacheRef,
    sessions,
    sessionsRef,
    setArchiveReloadToken,
    setArchivedGroups,
    setBoundSessionForRoot,
    setDrawerOpenForRoot,
    setDrawerSessionForRoot,
    setMultiProjectSessionGroups,
    setMultiProjectSessionPending,
    setSelectedSession,
    setSelectedSessionLoading,
    setSessionSearchResults,
    setSessions,
    staleSessionKeysRef,
  });


  useArchiveSync({SESSION_PAGE_SIZE, archiveOpen, archiveReloadToken, getNodeIdForRoot, managedRootByKeyRef, managedRootIdsRef, rootNodeKey, sessionService, setArchiveLoading, setArchivedGroups, toSessionItem, useEffect});



  useEffect(() => {
    handleSelectSessionRef.current = handleSelectSession;
  }, [handleSelectSession]);

  const refreshSessionsAfterExternalImport = useCallback(async (rootID: string) => {
    const listNodeId = getNodeIdForRoot(rootID);
    const payload = await sessionService.fetchSessions(rootID, { nodeId: listNodeId });
    const next = [...payload.items, ...payload.pinnedItems]
      // 节点归属必须打：主列表首屏加载打了 _nodeId，这里不打就会与既有条目
      // 并存成重复的 React key（同一 mergeSessionItems 归并口径）。
      .map((item) => toSessionItem(rootID, { ...(item as any), _nodeId: (item as any)._nodeId || listNodeId }))
      .filter((item): item is SessionItem => !!item);
    setHasMoreSessions(payload.totalCount > payload.items.length);
    setSessions(applyPinSnapshot(mergeSessionItems([], next), rootID));
  }, [getNodeIdForRoot]);

  const {
    externalSessions,
    setExternalSessions,
    externalSessionsRef,
    hasMoreExternalSessions,
    loadingOlderExternalSessions,
    loadingExternalSessions,
    externalSessionsError,
    externalSelectedKey,
    setExternalSelectedKey,
    externalImportAgent,
    setExternalImportAgent,
    externalFilterBound,
    setExternalFilterBound,
    selectedExternalImportKeys,
    importingExternalSessionKeys,
    confirmingExternalImport,
    importMenuOpen,
    setImportMenuOpen,
    importMenuRef,
    loadExternalSessions,
    enterImportMode,
    exitImportMode,
    handleLoadOlderExternalSessions,
    toggleExternalImportSelection,
    toggleAllExternalImportSelection,
    handleConfirmExternalImport,
    handleImportedSessionConfirmed,
  } = useExternalSessionImport({
    currentRootIdRef,
    getNodeIdForRoot,
    sessionListMode,
    setSessionListMode,
    isMobile,
    onSelectSession: handleSelectSession,
    onImported: refreshSessionsAfterExternalImport,
    closeRightSidebar: () => setIsRightOpen(false),
  });

  const { handleSendMessage } = useSendMessage({
    boundSessionByRootRef,
    currentRootIdRef,
    currentSessionRef,
    drawerSessionByRootRef,
    fileRef,
    interactionModeRef,
    mainViewRef,
    managedRootByIdRef,
    pendingDraftRef,
    pendingRequestRef,
    pluginBypassRef,
    pluginManagerRef,
    pluginQueryRef,
    selectedSessionByRootRef,
    selectedSessionRef,
    sessionCacheRef,
    setSessions,
    setSlashCommandResults,
    setMultiProjectSessionGroups,
    setInteractionMode,
    setPendingPlanMode,
    setSelectedSession,
    attachedFileContext,
    resolvePendingForSession,
    pendingPlanMode,
    slashCommandResults,
    setBoundSessionForRoot,
    setDrawerSessionForRoot,
    setMultiProjectSessionPending,
    setDrawerOpenForRoot,
    bumpCacheVersion,
    clearLocalPendingForSession,
    clearSlashCommandResultForSession,
    rootSessionKey,
    scopedRootKey,
    updateSessionAgentForKey,
  });


  const { handleForkAgentMessage, handleRunAgentLifecycleCommand
  } = useAppAgentActions({
    boundSessionByRootRef, bumpCacheVersion, currentRootIdRef, currentSessionRef,
    getNodeIdForRoot, handleSelectSession, handleSendMessage, loadSessionsForRoot,
    loadedSessionRef, rootSessionKey, scopedRootKey, selectedSessionByRootRef,
    selectedSessionRef, sessionCacheRef, setBoundSessionForRoot, setDrawerOpenForRoot,
    setDrawerSessionForRoot, setInteractionMode, setSelectedSession, setSessions,
    suppressedAutoBindSessionByRootRef, t,
  });
  const handleRestartAgent = useCallback(async (agentName: string) => {
    const nid = getNodeIdForRoot(currentRootIdRef.current || "") as any;
    await restartAgent(agentName, nid);
    const items = await fetchAgents(true, nid);
    setAvailableAgents(items);
    setAgentsVersion((v) => v + 1);
  }, []);

  const {
    handleCancelCurrentTurn,
    handleNewSession,
    currentSelectionSource,
    rememberCurrentFileScroll,
  } = useAppSessionSelection({
    currentRootIdRef,
    boundSessionByRootRef,
    scopedRootKey,
    suppressedAutoBindSessionByRootRef,
    switchMainView,
    selectedSessionRef,
    currentSessionRef,
    interactionModeRef,
    setSelectedSession,
    selectedSessionByRootRef,
    resetSessionLockForRoot,
    rootSessionKey,
    cancelRequestedBySessionRef,
    clearLocalPendingForSession,
    file,
    gitDiff,
    fileRef,
    fileScrollPositionsRef,
  });

  return {
    appendAgentChunkForSession, appendCompactNoticeForSession, appendPlanUpdateForSession, appendThoughtChunkForSession,
    appendTodoUpdateForSession, appendToolCallForSession, attachContextWindowToLatestAssistant, bumpCacheVersion,
    clearSessionStale, closeSessionSearch, confirmingExternalImport, currentSelectionSource,
    enterImportMode, executeSessionSearch, exitImportMode, externalFilterBound,
    externalImportAgent, externalSelectedKey, externalSessions, externalSessionsError,
    formatDirectoryLoadError, getSessionSnapshot, handleArchiveSession, handleCancelCurrentTurn,
    handleConfirmExternalImport, handleCreateBlankFile, handleDeleteSession, handleForkAgentMessage,
    handleGitCommit, handleGitDiscardItem, handleGitPull, handleGitPush,
    handleGitStageItem, handleGitUnstageItem, handleImportedSessionConfirmed, handleLoadOlderExternalSessions,
    handleLoadOlderSessions, handleNewSession, handleOnboardingStepChange, handlePinSession,
    handleRenameSession, handleRestartAgent, handleRunAgentLifecycleCommand, handleSearchQueryChange,
    handleSelectSession, handleSelectSessionAndClose, handleSendMessage, handleSetPlanMode,
    handleStartUpdate, handleSyncSession, handleTreeUpload, hasMoreExternalSessions,
    importMenuOpen, importMenuRef, importingExternalSessionKeys, isSessionStale,
    loadChildSessionsForParent, loadMoreMultiProjectSessions, loadMultiProjectSessionGroups, loadSessionsForRoot,
    loadingExternalSessions, loadingOlderExternalSessions, markSessionStale, openGitCommitDiff,
    openGitDiff, openSessionSearch, promotePendingSessionForRoot, refreshCurrentFileContent,
    refreshMultiProjectReplyingSessions, refreshTreeDir, rememberCurrentFileScroll, resolvePendingForSession,
    resolveRootForSessionKey, restoreActiveSession, rootSessionKey, scheduleMultiProjectSessionReload,
    scheduleSessionListReload, selectedExternalImportKeys, sessionSearchLoading,
    sessionSearchOpen, sessionSearchQuery, sessionSearchResults, sessionSearchResultsMode,
    setExternalFilterBound, setExternalImportAgent, setExternalSelectedKey, setExternalSessions,
    setImportMenuOpen, setMultiProjectSessionPending,
    switchGitBranch, toggleAllExternalImportSelection, toggleExternalImportSelection, toggleSessionSearch,
    treeCacheKey, tryShowBoundSessionForRoot, updateSessionAgentForKey, updateSessionRelatedFilesForKey,
    updateSessionRelatedWorktreeForKey
  };
}
