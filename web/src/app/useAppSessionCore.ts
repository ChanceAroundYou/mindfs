import { useCallback, useEffect, useRef } from "react";
import { scopeSessionKey } from "../shared/scope";
import type { MultiProjectSessionGroup, SessionItem } from "./appSession";
import type { UseAppSessionLayerParams } from "./useAppSessionLayerTypes";
import { useAppRootResolution } from "./useAppRootResolution";
import { useAppSessionKeyState } from "./useAppSessionKeyState";
import { useSessionPending } from "./useSessionPending";
import { useSessionLifecycle } from "./useSessionLifecycle";
import { useSessionMetadata } from "./useSessionMetadata";
import { useMultiProjectSessions } from "./useMultiProjectSessions";
import { useSessionStreamCache } from "./useSessionStreamCache";

export function useAppSessionCore(params: UseAppSessionLayerParams) {
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



  return {
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
  };
}
