import { useCallback, useEffect, useRef } from "react";
import { sessionService } from "../services/session";
import { treeKey } from "../shared/scope";
import { mergeSessionItems } from "../services/session/sessionListMerge";
import { fetchAgents, restartAgent } from "../services/agents";
import { SESSION_PAGE_SIZE } from "./appMisc";
import { useAppSessionSelection } from "./useAppSessionSelection";
import { useAppSessionReload } from "./useAppSessionReload";
import { useAppBootstrapActions } from "./useAppBootstrapActions";
import { useAppFormatDirectoryLoadError } from "./useAppFormatDirectoryLoadError";
import { installBackNavigation } from "../hooks/useBackNavigation";
import { toSessionItem } from "./appSession";
import type { SessionItem } from "./appSession";
import { rootNodeKey } from "./appPath";
import { useSendMessage } from "./useSendMessage";
import { useExternalSessionImport } from "./useExternalSessionImport";
import { useGitActions } from "./useGitActions";
import { useSessionSearch } from "./useSessionSearch";
import { useAppAgentActions } from "./useAppAgentActions";
import { useSessionArchive } from "./useSessionArchive";
import { useFileRefresh } from "./useFileRefresh";
import { useArchiveSync } from "./useArchiveSync";
import { useSessionListActions } from "./useSessionListActions";
import { useAppSessionCore } from "./useAppSessionCore";
import { useAppMultiProjectReload } from "./useAppMultiProjectReload";
import type { UseAppSessionLayerParams } from "./useAppSessionLayerTypes";

/**
 * 见 docs/plans/app-tsx-under-1000.md：分层聚合 hook。
 */


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


  const {
    rootSessionKey, bumpCacheVersion, bumpCacheVersionDebounced, applyPendingToMultiProjectGroups,
    clearSlashCommandResultForSession, setMultiProjectSessionPending, resolveRootForSessionKey,
    getSessionSnapshot, resolvePendingForSession, clearLocalPendingForSession,
    markSessionStale, clearSessionStale, isSessionStale,
    restoreActiveSession, syncSessionHeaderFromListItem, loadSessionsForRoot,
    loadChildSessionsForParent, handleSelectSession, tryShowBoundSessionForRoot,
    handleRenameSession, handleSyncSession,
    updateSessionRelatedFilesForKey, updateSessionRelatedWorktreeForKey,
    updateSessionAgentForKey, getInputSession, handleSetPlanMode,
    promotePendingSessionForRoot, loadMultiProjectSessionGroups, loadMoreMultiProjectSessions,
    resolveRuntimeMetaForSession, appendAgentChunkForSession, appendThoughtChunkForSession,
    appendToolCallForSession, appendTodoUpdateForSession, appendPlanUpdateForSession,
    appendCompactNoticeForSession, attachContextWindowToLatestAssistant,
  } = useAppSessionCore(params);

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



  const { scheduleMultiProjectSessionReload, refreshMultiProjectReplyingSessions } = useAppMultiProjectReload({
    MULTI_PROJECT_RELOAD_DEBOUNCE_MS,
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
