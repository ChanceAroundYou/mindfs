import { useCallback, useEffect, useRef } from "react";
import { useManagedRoots } from "./useManagedRoots";
import { useActionHandlers } from "./useActionHandlers";
import type { ActionHandlers } from "./useActionHandlers";
import { useAppBoundSessionDir } from "./useAppBoundSessionDir";
import { useRelatedFileDiff } from "./useRelatedFileDiff";
import { useAppFileNavigation } from "./useAppFileNavigation";
import { useAppProjectRemoval } from "./useAppProjectRemoval";
import { useProjectLifecycle } from "./useProjectLifecycle";
import type { UseAppDomainLayerParams } from "./useAppDomainLayerTypes";

export function useAppProjectManagement(params: UseAppDomainLayerParams) {
  const { activeBoundSessionKey, appendAgentChunkForSession, appendCompactNoticeForSession, appendPlanUpdateForSession,
    appendThoughtChunkForSession, appendTodoUpdateForSession, appendToolCallForSession, applyTaskDetails,
    attachContextWindowToLatestAssistant, bootstrapState, boundSessionByRootRef, bumpCacheVersion,
    cancelRequestedBySessionRef, clearSessionStale, currentFileEditing, currentRootId,
    currentRootIdRef, currentRootNodeId, currentRootNodeIdRef, currentSelectionSource,
    currentSession, currentSessionRef, didInitRef, dismissedSelectionFileRef,
    drawerOpenByRootRef, drawerSessionByRootRef, entriesByPathRef,
    expandedRef, file, fileCursorRef, fileEditStore,
    fileOpenRequestRef, fileRef, fileScrollPositionsRef, formatDirectoryLoadError,
    fullUpgradeAttemptRef, getNodeIdForRoot, getSessionSnapshot, gitDiff,
    gitStatus, handleImportedSessionConfirmed, handleSelectSession, handleSelectSessionRef,
    interactionMode, interactionModeRef, invalidTreeCacheKeysRef, isDrawerOpen,
    isSessionStale, kanbanTasks, lastMainSessionSnapshotRef, lastNonChatViewRef,
    lastPluginChapterRef, lastViewerSelectionRef, loadKanbanTasks, loadMultiProjectSessionGroups,
    loadSessionsForRoot, loadTaskTemplates, loadedSessionRef, loadingSessionRef,
    mainDirectoryErrorRef, mainEntries, mainEntriesRef, mainView,
    mainViewRef, managedRootByIdRef, managedRootByKeyRef, managedRootIdsRef,
    managedRootsRequestRef, markSessionStale, multiProjectPendingByKey, multiProjectPendingRef, multiProjectSessionGroups,
    multiProjectSessionsEnabled, nodeLoadFailuresRef, notifiedNodeFailuresRef, notifyNodeLoadFailedRef,
    openGitDiff, optimisticDequeuedIdsRef, pendingBySessionRef, pendingDraftRef,
    pendingPluginTrust, pendingRequestRef, playCompletionSound, pluginBypass,
    pluginBypassRef, pluginContentRef, pluginManagerRef, pluginQuery,
    pluginVersion, pluginsLoadedByRootRef, pluginsLoadingByRootRef, pluginsTrustPendingByRootRef,
    promotePendingSessionForRoot, pruneTaskDetails, queueFrozenBySessionRef, queueVersion,
    queuedMessagesBySessionRef, refreshCurrentFileContent, refreshGitHistory, refreshGitStatus,
    refreshMultiProjectReplyingSessions, refreshTaskWorktree, refreshTreeDir, refreshWorkspaceBoard,
    rememberCurrentFileScroll, replaceURLState, resetLocksForRootTransition, resolvePendingForSession,
    resolveRootForSessionKey, restoreActiveSession, rootEntriesRef, rootSessionKey,
    scheduleMultiProjectSessionReload, scheduleSessionListReload, scopedRootKey, selectRootNode,
    selectedDirRef, selectedKanbanTaskId, selectedSession, selectedSessionByRootRef,
    selectedSessionLoading, selectedSessionRef, sessionCacheRef, sessionListReloadTimerRef,
    sessionLoadFailureRef, sessions, sessionsRef, setActiveBoundSessionKey,
    setAgentsVersion, setAttachedFileContext, setBootstrapState, setBoundSessionForRoot,
    setCodexRateLimitsRefreshToken, setCurrentRootId, setCurrentSession, setDirectorySortOverrides,
    setDrawerOpenForRoot, setDrawerSessionForRoot, setEntriesByPath, setExpanded, setExternalSelectedKey,
    setExternalSessions, setFile, setGitDiff, setGitHistory,
    setGitHistoryExpandedByRoot, setGitStatus, setGitStatusExpandedByRoot, setInteractionMode,
    setIsDrawerOpen, setIsLeftOpen, setMainDirectoryError, setMainEntries,
    setMainView, setManagedRootIds, setMultiProjectPendingByKey, setMultiProjectSessionGroups,
    setMultiProjectSessionPending, setPendingPluginTrust, setPluginBypass, setPluginLoading,
    setPluginQuery, setPluginVersion, setQueueVersion, setRelatedSelectedFileKey,
    setRootEntries, setSelectedDir, setSelectedDirKey, setSelectedKanbanTaskId,
    setSelectedSession, setSelectedSessionLoading, setSessionListMode, setSessions, setSlashCommandResults, setStatus, setTaskRelatedFilesById,
    setTaskSessionErrorDialog, setUpdateState, setViewerSelection, showHiddenFiles,
    staleSessionKeysRef, suppressedAutoBindSessionByRootRef, switchMainView, t,
    taskDetailsById, taskDetailsByIdRef, taskSessionKeysById, taskSessionKeysByIdRef,
    treeCacheKey, tryShowBoundSessionForRoot, updateSessionAgentForKey, updateSessionRelatedFilesForKey,
    updateSessionRelatedWorktreeForKey, viewerSelection, viewerSelectionRef, workspaceOpenRef } = params;

  const actionHandlersRef = useRef<ActionHandlers>({
    open: async () => {},
    open_dir: async () => {},
  });
  const {
    clearRootScopedClientState,
    loadManagedRootPayloads,
    refreshManagedRoots,
    applyManagedRootRename,
    handleRenameCurrentRoot,
  } = useManagedRoots({
    actionHandlersRef,
    bootstrapState,
    boundSessionByRootRef,
    bumpCacheVersion,
    cancelRequestedBySessionRef,
    currentRootIdRef,
    currentRootNodeIdRef,
    drawerOpenByRootRef,
    drawerSessionByRootRef,
    entriesByPathRef,
    file,
    fileScrollPositionsRef,
    getNodeIdForRoot,
    invalidTreeCacheKeysRef,
    loadMultiProjectSessionGroups,
    loadedSessionRef,
    loadingSessionRef,
    managedRootByIdRef,
    managedRootByKeyRef,
    managedRootIdsRef,
    managedRootsRequestRef,
    multiProjectPendingRef,
    multiProjectSessionsEnabled,
    nodeLoadFailuresRef,
    notifiedNodeFailuresRef,
    notifyNodeLoadFailedRef,
    optimisticDequeuedIdsRef,
    pendingBySessionRef,
    pendingRequestRef,
    pluginQuery,
    pluginsLoadedByRootRef,
    pluginsLoadingByRootRef,
    pluginsTrustPendingByRootRef,
    queueFrozenBySessionRef,
    queuedMessagesBySessionRef,
    refreshMultiProjectReplyingSessions,
    replaceURLState,
    scopedRootKey,
    selectedSessionByRootRef,
    sessionCacheRef,
    setActiveBoundSessionKey,
    setCurrentRootId,
    setCurrentSession,
    setDirectorySortOverrides,
    setEntriesByPath,
    setFile,
    setGitHistoryExpandedByRoot,
    setGitStatusExpandedByRoot,
    setInteractionMode,
    setIsDrawerOpen,
    setMainEntries,
    setManagedRootIds,
    setMultiProjectPendingByKey,
    setMultiProjectSessionGroups,
    setQueueVersion,
    setRootEntries,
    setSelectedDir,
    setSelectedDirKey,
    setSelectedSession,
    setSelectedSessionLoading,
    setSessions,
    setSlashCommandResults,
    staleSessionKeysRef,
    suppressedAutoBindSessionByRootRef,
  });
  const { handlers: actionHandlers } = useActionHandlers({
    actionHandlersRef,
    fileEditStore,
    currentRootIdRef,
    currentRootNodeIdRef,
    entriesByPathRef,
    expandedRef,
    fileCursorRef,
    fileOpenRequestRef,
    fileRef,
    interactionModeRef,
    invalidTreeCacheKeysRef,
    mainDirectoryErrorRef,
    mainEntriesRef,
    mainViewRef,
    managedRootByIdRef,
    pluginBypassRef,
    pluginManagerRef,
    rootEntriesRef,
    setCurrentRootId,
    setEntriesByPath,
    setExpanded,
    setFile,
    setGitDiff,
    setInteractionMode,
    setIsLeftOpen,
    setMainDirectoryError,
    setMainEntries,
    setPluginBypass,
    setPluginQuery,
    setRelatedSelectedFileKey,
    setSelectedDir,
    setSelectedDirKey,
    setDrawerOpenForRoot,
    resetLocksForRootTransition,
    switchMainView,
    replaceURLState,
    formatDirectoryLoadError,
    treeCacheKey,
    refreshTreeDir,
    loadSessionsForRoot,
    handleSelectSession,
    tryShowBoundSessionForRoot,
    rememberCurrentFileScroll,
    getNodeIdForRoot,
    selectRootNode,
  });
  useEffect(() => {
    actionHandlersRef.current = actionHandlers;
  }, [actionHandlers]);

  // 会话恢复入口（open_dir 开根 / 首屏 boot / popstate）共用：
  // 会话在场就直接返回，目录一次都不加载——带着 view=files 进来时主区就是空的。
  const { showBoundSessionOrRootDir } = useAppBoundSessionDir({
    actionHandlersRef,
    loadSessionsForRoot,
    mainDirectoryErrorRef,
    mainEntriesRef,
    mainViewRef,
    refreshTreeDir,
    tryShowBoundSessionForRoot,
  });




  const {
    openRelatedFileDiff,
  } = useRelatedFileDiff({
    actionHandlers,
    currentRootIdRef,
    file,
    fileOpenRequestRef,
    getNodeIdForRoot,
    gitStatus,
    managedRootByIdRef,
    openGitDiff,
    pluginQuery,
    replaceURLState,
    resetLocksForRootTransition,
    setCurrentRootId,
    setFile,
    setGitDiff,
    setIsLeftOpen,
    setRelatedSelectedFileKey,
    setSelectedSession,
    setSelectedSessionLoading,
    switchMainView,
  });
  useEffect(() => {
    const h = () => { void refreshManagedRoots(); };
    window.addEventListener("mindfs:nodes-changed", h);
    return () => window.removeEventListener("mindfs:nodes-changed", h);
  }, [refreshManagedRoots]);

  // 同一节点在一次故障里只提示一次（刷新很频繁，每次都弹会淹掉界面）。
  const {
    notifyNodeLoadFailed,
    handleFileViewerPathClick,
    handleFileViewerFileClick,
    handleGitDiffPathClick,
    handleDirectoryPathClick,
    gitFileStatsByPath,
  } = useAppFileNavigation({
    file,
    currentRootIdRef,
    actionHandlers,
    setGitDiff,
    gitStatus,
    getNodeIdForRoot,
    notifiedNodeFailuresRef,
    refreshManagedRoots,
    t,
  });
  useEffect(() => {
    notifyNodeLoadFailedRef.current = notifyNodeLoadFailed;
  }, [notifyNodeLoadFailed]);


  const { findManagedRootByPath, handleRemoveCurrentRoot, handleRemoveCurrentWorktree
  } = useAppProjectRemoval({
    clearRootScopedClientState, currentRootIdRef, getNodeIdForRoot, managedRootByIdRef,
    refreshManagedRoots, t,
  });

  const openManagedDir = useCallback(
    (payload: { path: string; root: string; isRoot: boolean; forceDirectory?: boolean }) =>
      actionHandlersRef.current.open_dir(payload),
    [],
  );

  const {
    projectAddOverlay,
    worktreeCreateOverlay,
    worktreeSwitchOverlay,
    creatingRootName,
    setCreatingRootName,
    creatingRootKind,
    creatingRootBusy,
    worktreeSwitchOpen,
    projectAddMode,
    handleCreateRootStart,
    handleSwitchWorktreeStart,
    handleOpenProjectAdd,
    handleOpenWorktreeLocation,
    handleCreateRootCancel,
    handleCreateRootSubmit,
    setGitHubImportState,
    setProjectAddMode,
  } = useProjectLifecycle({
    currentRootId,
    currentRootIdRef,
    getNodeIdForRoot,
    refreshManagedRoots,
    findManagedRootByPath,
    managedRootIdsRef,
    managedRootByIdRef,
    openManagedDir,
  });



  return {
    actionHandlersRef, actionHandlers,
    loadManagedRootPayloads, refreshManagedRoots, applyManagedRootRename, handleRenameCurrentRoot,
    showBoundSessionOrRootDir, openRelatedFileDiff,
    handleFileViewerPathClick, handleFileViewerFileClick, handleGitDiffPathClick,
    handleDirectoryPathClick, gitFileStatsByPath,
    findManagedRootByPath, handleRemoveCurrentRoot, handleRemoveCurrentWorktree,
    projectAddOverlay, worktreeCreateOverlay, worktreeSwitchOverlay,
    creatingRootName, setCreatingRootName, creatingRootKind, creatingRootBusy,
    worktreeSwitchOpen, projectAddMode,
    handleCreateRootStart, handleSwitchWorktreeStart, handleOpenProjectAdd,
    handleOpenWorktreeLocation, handleCreateRootCancel, handleCreateRootSubmit,
    setGitHubImportState, setProjectAddMode,
  };
}
