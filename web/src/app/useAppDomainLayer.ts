import { useEffect, useMemo } from "react";
import { sessionService } from "../services/session";
import type { QueuedUserMessage, RelatedFile, RelatedWorktree, SyncSessionResult, TokenUsage } from "../services/session";
import { bootstrapService } from "../services/net/bootstrap";
import type { BootstrapState } from "../services/net/bootstrap";
import { reportError } from "../services/net/error";
import { setRootNodeMap } from "../services/net/rootNode";
import { resolveLockedSessionKey } from "../services/session/sessionLock";
import { useAppPluginRenderEffects } from "./useAppPluginRenderEffects";
import { useAppBootstrapEffects } from "./useAppBootstrapEffects";
import { useAppSessionSnapshots } from "./useAppSessionSnapshots";
import { useAppDomainDerived } from "./useAppDomainDerived";
import { useAppPluginTrust } from "./useAppPluginTrust";
import { useAppPluginRender } from "./useAppPluginRender";
import { closeTopBackLayer, consumeViewHistoryEntry, hasBackLayer } from "../services/platform/backNavigation";
import { indexManagedRoots, mapManagedRootsToEntries, toPluginInput } from "./appMisc";
import { readURLState } from "./appPath";
import { AttachedFileContext, MultiProjectSessionGroup, PendingSend, SessionItem, SlashCommandResult, ViewerSelection, WSStatus, hasSessionExchanges, sessionInputHistory, toSessionItem } from "./appSession";
import { loadLastRootId, loadLastRootNodeId } from "./appStorage";
import { useAppTaskDrawer } from "./useAppTaskDrawer";
import { useRealtimeEvents } from "./useRealtimeEvents";
import { useTaskWorkspaceEffects } from "./useTaskWorkspaceEffects";
import { usePluginHandlers } from "./usePluginHandlers";
import { usePopStateSync } from "./usePopStateSync";
import { useSessionRestore } from "./useSessionRestore";
import { useAppInit } from "./useAppInit";
import { useAttachedFileContext } from "./useAttachedFileContext";
import { useSessionQueueActions } from "./useSessionQueueActions";
import type { Session } from "../services/session";
import type { DirectorySortMode, FileEntry } from "../services/prefs/directorySort";
import { FileEditStore } from "../services/fileEditing";
import type { FilePayload } from "../services/file";
import type { GitDiffPayload } from "../services/git";
import type { GitHistoryItem, GitHistoryPayload, GitStatusItem, GitStatusPayload } from "../services/git";
import type { KanbanTask, TaskDetail } from "../services/tasks";
import type { MessageKey, MessageParams } from "../i18n";
import type { PluginManager, PluginSourceBundle } from "../plugins/manager";
import type { UpdateState } from "../services/update";
import type { SessionRuntimeMeta } from "./useSessionStreamCache";
import type { UseAppDomainLayerParams } from "./useAppDomainLayerTypes";
export type { UseAppDomainLayerParams } from "./useAppDomainLayerTypes";
import { useAppProjectManagement } from "./useAppProjectManagement";

/**
 * 见 docs/plans/app-tsx-under-1000.md：分层聚合 hook。
 */


/**
 * 本层由 App.tsx 原样搬出，hook 调用顺序与搬出前逐字一致。
 */
export function useAppDomainLayer(params: UseAppDomainLayerParams) {
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

  const {
    markSessionPending,
    handleRemoveQueuedMessage,
    handleUpdateQueuedMessage,
    handleSendQueuedMessageNow,
  } = useSessionQueueActions({
    boundSessionByRootRef,
    bumpCacheVersion,
    currentRootIdRef,
    drawerSessionByRootRef,
    interactionModeRef,
    multiProjectPendingRef,
    optimisticDequeuedIdsRef,
    queuedMessagesBySessionRef,
    rootSessionKey,
    scopedRootKey,
    selectedSessionRef,
    sessionCacheRef,
    setDrawerSessionForRoot,
    setMultiProjectSessionPending,
    setQueueVersion,
    setSessions,
  });




  const {
    buildAttachedFileContext,
    handleRequestFileContext,
    handleClearFileContext,
    handleViewerSelectionChange,
  } = useAttachedFileContext({
    currentSelectionSource,
    viewerSelection,
    dismissedSelectionFileRef,
    viewerSelectionRef,
    lastViewerSelectionRef,
    setAttachedFileContext,
    setViewerSelection,
  });



  const {
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
  } = useAppProjectManagement(params);


  const {
    ensurePluginsLoaded,
    pluginHandlers,
  } = usePluginHandlers({
    actionHandlers,
    currentRootIdRef,
    file,
    fileCursorRef,
    fileRef,
    getNodeIdForRoot,
    handleSelectSession,
    managedRootByIdRef,
    pluginManagerRef,
    pluginQuery,
    pluginsLoadedByRootRef,
    pluginsLoadingByRootRef,
    pluginsTrustPendingByRootRef,
    replaceURLState,
    scopedRootKey,
    sessions,
    setPendingPluginTrust,
    setPluginLoading,
    setPluginQuery,
    setPluginVersion,
  });

  const {
    handleTrustPendingPlugins,
    handleDisablePendingPlugins,
    invalidatePluginsForRoot,
  } = useAppPluginTrust({
    pendingPluginTrust,
    setPluginLoading,
    pluginManagerRef,
    pluginsLoadedByRootRef,
    scopedRootKey,
    pluginsTrustPendingByRootRef,
    setPendingPluginTrust,
    setPluginVersion,
    pluginsLoadingByRootRef,
    currentRootIdRef,
    ensurePluginsLoaded,
  });




  const { handleSessionChipClick,
    handleTaskSessionDrawerOpen,
    refreshTaskRelatedFiles,
    refreshTasksForRelatedSession,
    handleSelectKanbanTask,
  } = useAppTaskDrawer({
    currentRootIdRef, file, getNodeIdForRoot, handleSelectSession,
    rootSessionKey, sessionCacheRef, sessions, setSelectedKanbanTaskId,
    setTaskRelatedFilesById, taskDetailsByIdRef, taskSessionKeysById, taskSessionKeysByIdRef,
    updateSessionRelatedFilesForKey,
  });





	  useEffect(() => {
    function openReplySession(detail: any) {
      const rootId = typeof detail?.rootId === "string" ? detail.rootId.trim() : "";
      const sessionKey = typeof detail?.sessionKey === "string" ? detail.sessionKey.trim() : "";
      if (!rootId || !sessionKey) {
        return;
      }
      handleSessionChipClick(sessionKey, rootId);
    }

    function handleOpenReplySession(event: Event) {
      openReplySession((event as CustomEvent).detail);
    }

    window.addEventListener("mindfs:open-reply-session", handleOpenReplySession);
    openReplySession((window as any).__mindfsPendingReplySession);
    delete (window as any).__mindfsPendingReplySession;
    return () => {
      window.removeEventListener("mindfs:open-reply-session", handleOpenReplySession);
    };
  }, [handleSessionChipClick]);





  const visibleMainEntries = useMemo(
    () =>
      showHiddenFiles
        ? mainEntries
        : mainEntries.filter((entry) => !entry.name.startsWith(".")),
    [mainEntries, showHiddenFiles],
  );


  useAppBootstrapEffects({
    currentRootId, currentRootNodeId, refreshGitStatus, refreshGitHistory,
    setGitStatus, setGitHistory, setGitDiff, scopedRootKey,
    ensurePluginsLoaded, pluginsLoadedByRootRef, setBootstrapState, setSessionListMode,
    setExternalSessions, setExternalSelectedKey, getNodeIdForRoot,
  });

  useEffect(
    () => () => {
      sessionService.disconnect();
      setStatus("disconnected");
    },
    [],
  );


  // 2026-09 App.tsx 拆分：25 个 WS 事件处理器（含 9 个 handleSessionStream 内部 case）
  // 与其 13 个内部 helper 整体搬到 app/useRealtimeEvents.ts。参数按用途分四组、组内同名简写，
  // 所以搬过去的 1600 行代码一个标识符都没改。
  useRealtimeEvents({
    refs: { // App 的 ref 池
      actionHandlersRef,
      boundSessionByRootRef,
      cancelRequestedBySessionRef,
      currentRootIdRef,
      currentRootNodeIdRef,
      currentSessionRef,
      drawerSessionByRootRef,
      fileRef,
      invalidTreeCacheKeysRef,
      loadedSessionRef,
      managedRootByIdRef,
      managedRootByKeyRef,
      managedRootIdsRef,
      optimisticDequeuedIdsRef,
      pendingBySessionRef,
      pendingDraftRef,
      pendingRequestRef,
      queueFrozenBySessionRef,
      queuedMessagesBySessionRef,
      selectedDirRef,
      selectedSessionByRootRef,
      selectedSessionRef,
      sessionCacheRef,
      sessionListReloadTimerRef,
      sessionsRef,
      suppressedAutoBindSessionByRootRef,
      taskDetailsByIdRef,
      workspaceOpenRef,
    },
    setters: { // App 的 setState
      setAgentsVersion,
      setBoundSessionForRoot,
      setCodexRateLimitsRefreshToken,
      setDrawerSessionForRoot,
      setGitDiff,
      setGitHubImportState,
      setMultiProjectSessionGroups,
      setMultiProjectSessionPending,
      setProjectAddMode,
      setQueueVersion,
      setRootEntries,
      setSelectedSession,
      setSessions,
      setSlashCommandResults,
      setStatus,
      setTaskSessionErrorDialog,
      setUpdateState,
    },
    actions: { // App 的动作
      appendAgentChunkForSession,
      appendCompactNoticeForSession,
      appendPlanUpdateForSession,
      appendThoughtChunkForSession,
      appendTodoUpdateForSession,
      appendToolCallForSession,
      applyManagedRootRename,
      applyTaskDetails,
      attachContextWindowToLatestAssistant,
      bumpCacheVersion,
      clearSessionStale,
      getNodeIdForRoot,
      invalidatePluginsForRoot,
      loadManagedRootPayloads,
      loadMultiProjectSessionGroups,
      scheduleMultiProjectSessionReload,
      loadSessionsForRoot,
      markSessionPending,
      markSessionStale,
      playCompletionSound,
      pruneTaskDetails,
      promotePendingSessionForRoot,
      refreshCurrentFileContent,
      refreshGitStatus,
      refreshManagedRoots,
      refreshMultiProjectReplyingSessions,
      refreshTaskWorktree,
      refreshTasksForRelatedSession,
      refreshTreeDir,
      refreshWorkspaceBoard,
      resolveRootForSessionKey,
      restoreActiveSession,
      scheduleSessionListReload,
      updateSessionAgentForKey,
      updateSessionRelatedFilesForKey,
      updateSessionRelatedWorktreeForKey,
    },
    values: { // 稳定值
      currentRootId,
      gitDiff,
      handleImportedSessionConfirmed,
      multiProjectSessionsEnabled,
      rootSessionKey,
      scopedRootKey,
      t,
      treeCacheKey,
    },
  });



  useAppInit({actionHandlersRef, bootstrapService, bootstrapState, didInitRef, ensurePluginsLoaded, handleSelectSessionRef, indexManagedRoots, loadLastRootId, loadLastRootNodeId, loadManagedRootPayloads, loadMultiProjectSessionGroups, loadSessionsForRoot, managedRootByIdRef, managedRootByKeyRef, managedRootIdsRef, mapManagedRootsToEntries, multiProjectSessionsEnabled, readURLState, refreshTreeDir, reportError, selectRootNode, setCurrentRootId, setManagedRootIds, setPluginQuery, setRootEntries, setRootNodeMap, showBoundSessionOrRootDir, switchMainView, t, useEffect});






  useTaskWorkspaceEffects({
    bootstrapPhase: bootstrapState.phase,
    currentRootIdRef,
    setAgentsVersion,
    loadTaskTemplates,
    loadKanbanTasks,
    loadMultiProjectSessionGroups,
    multiProjectSessionsEnabled,
  });



  usePopStateSync({actionHandlers, closeTopBackLayer, consumeViewHistoryEntry, currentRootIdRef, fileCursorRef, fileRef, handleSelectSessionRef, hasBackLayer, lastNonChatViewRef, loadSessionsForRoot, mainViewRef, readURLState, refreshTreeDir, replaceURLState, selectRootNode, selectedSessionRef, setCurrentRootId, setMainView, setPluginQuery, showBoundSessionOrRootDir, switchMainView, useEffect});

  const derived = useAppDomainDerived({
    activeBoundSessionKey, actionHandlers, boundSessionByRootRef, bumpCacheVersion,
    clearSessionStale, currentFileEditing, currentRootId, currentRootIdRef, currentSession,
    currentSessionRef, drawerSessionByRootRef, file, fileCursorRef, fullUpgradeAttemptRef,
    getSessionSnapshot, handleViewerSelectionChange, interactionMode, isDrawerOpen,
    kanbanTasks, lastMainSessionSnapshotRef, lastPluginChapterRef, mainView,
    multiProjectPendingByKey, multiProjectSessionGroups, pluginBypass, pluginContentRef, pluginManagerRef, pluginQuery,
    pluginVersion, queueVersion, queuedMessagesBySessionRef, rootSessionKey, scopedRootKey,
    selectedKanbanTaskId, selectedSession, selectedSessionLoading, sessionCacheRef,
    sessionLoadFailureRef, sessions, taskDetailsById, taskSessionKeysById,
    loadedSessionRef, loadingSessionRef, isSessionStale, resolvePendingForSession,
    restoreActiveSession, setDrawerSessionForRoot, setSelectedSession,
  });

  return {
    ...derived,
    actionHandlers, actionHandlersRef,
    creatingRootBusy, creatingRootKind, creatingRootName, findManagedRootByPath,
    gitFileStatsByPath, handleClearFileContext, handleCreateRootCancel, handleCreateRootStart,
    handleCreateRootSubmit, handleDirectoryPathClick, handleDisablePendingPlugins,
    handleFileViewerFileClick, handleFileViewerPathClick, handleGitDiffPathClick,
    handleOpenProjectAdd, handleOpenWorktreeLocation, handleRemoveCurrentRoot,
    handleRemoveCurrentWorktree, handleRemoveQueuedMessage, handleRenameCurrentRoot,
    handleRequestFileContext, handleSelectKanbanTask, handleSendQueuedMessageNow,
    handleSessionChipClick, handleSwitchWorktreeStart, handleTaskSessionDrawerOpen,
    handleTrustPendingPlugins, handleUpdateQueuedMessage, handleViewerSelectionChange,
    matchedPlugin: derived.matchedPlugin, openRelatedFileDiff, pluginHandlers,
    pluginRender: derived.pluginRender, pluginRendererKey: derived.pluginRendererKey,
    pluginThemeVars: derived.pluginThemeVars, projectAddMode, projectAddOverlay,
    refreshManagedRoots, refreshTaskRelatedFiles, selectedKanbanTask: derived.selectedKanbanTask,
    selectedKanbanTaskSessionSnapshot: derived.selectedKanbanTaskSessionSnapshot,
    selectedSessionSnapshot: derived.selectedSessionSnapshot, setCreatingRootName,
    visibleMainEntries, worktreeCreateOverlay, worktreeSwitchOpen, worktreeSwitchOverlay,
  };

}
