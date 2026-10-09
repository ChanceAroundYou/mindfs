import { useEffect, useMemo } from "react";
import { resolveLockedSessionKey } from "../services/sessionLock";
import { toPluginInput } from "./appMisc";
import { sessionInputHistory, hasSessionExchanges, toSessionItem, type ViewerSelection } from "./appSession";
import { useAppPluginRender } from "./useAppPluginRender";
import { useAppPluginRenderEffects } from "./useAppPluginRenderEffects";
import { useAppSessionSnapshots } from "./useAppSessionSnapshots";
import { useSessionRestore } from "./useSessionRestore";
import type { ActionHandlers } from "./useActionHandlers";
import type { UseAppDomainLayerParams } from "./useAppDomainLayer";
import type { Session } from "../services/session";

export type UseAppDomainDerivedParams = Pick<UseAppDomainLayerParams,
  | "activeBoundSessionKey" | "currentFileEditing" | "currentRootId" | "currentSession"
  | "currentSessionRef" | "drawerSessionByRootRef" | "file" | "fileCursorRef"
  | "fullUpgradeAttemptRef" | "getSessionSnapshot" | "interactionMode" | "isDrawerOpen"
  | "lastPluginChapterRef"
  | "lastMainSessionSnapshotRef" | "mainView" | "pluginBypass" | "pluginContentRef"
  | "pluginManagerRef" | "pluginQuery" | "pluginVersion" | "queueVersion"
  | "queuedMessagesBySessionRef" | "rootSessionKey" | "selectedKanbanTaskId"
  | "selectedSession" | "selectedSessionLoading" | "sessionCacheRef" | "sessionLoadFailureRef"
  | "sessions" | "taskDetailsById" | "taskSessionKeysById" | "multiProjectSessionGroups"
  | "kanbanTasks" | "currentRootIdRef" | "boundSessionByRootRef" | "bumpCacheVersion"
  | "clearSessionStale" | "isSessionStale" | "loadedSessionRef"
  | "loadingSessionRef" | "resolvePendingForSession" | "restoreActiveSession"
  | "scopedRootKey" | "setDrawerSessionForRoot" | "setSelectedSession"
> & {
  actionHandlers: ActionHandlers;
  handleViewerSelectionChange: (selection: ViewerSelection | null) => void;
};

export function useAppDomainDerived({
  activeBoundSessionKey, actionHandlers, boundSessionByRootRef, bumpCacheVersion,
  clearSessionStale, currentFileEditing, currentRootId, currentRootIdRef, currentSession,
  drawerSessionByRootRef, file, fileCursorRef, fullUpgradeAttemptRef, getSessionSnapshot,
  handleViewerSelectionChange, interactionMode, isDrawerOpen, kanbanTasks, lastMainSessionSnapshotRef,
  lastPluginChapterRef, mainView, multiProjectSessionGroups, pluginBypass, pluginContentRef,
  pluginManagerRef, pluginQuery, pluginVersion, queueVersion, queuedMessagesBySessionRef,
  rootSessionKey, scopedRootKey, selectedKanbanTaskId, selectedSession, selectedSessionLoading,
  sessionCacheRef, sessionLoadFailureRef, sessions, taskDetailsById, taskSessionKeysById,
  currentSessionRef, loadedSessionRef, loadingSessionRef, isSessionStale, resolvePendingForSession,
  restoreActiveSession, setDrawerSessionForRoot, setSelectedSession,
}: UseAppDomainDerivedParams) {
  const selectedRoot =
    (selectedSession?.root_id as string | undefined) || currentRootId || "";
  const selectedInCurrentRoot =
    !!selectedSession && !!currentRootId && selectedRoot === currentRootId;
  const selectedKey =
    selectedSession?.key || selectedSession?.session_key || "";
  const lockedSessionKey = resolveLockedSessionKey(activeBoundSessionKey);
  const lockedSessionSnapshot = lockedSessionKey && currentRootId
    ? getSessionSnapshot(
        currentRootId,
        drawerSessionByRootRef.current[scopedRootKey(currentRootId)] ||
          sessionCacheRef.current[rootSessionKey(currentRootId, lockedSessionKey)] ||
          null,
      )
    : null;
  const boundFromSelected =
    selectedInCurrentRoot && selectedKey === activeBoundSessionKey
      ? (selectedSession as any)
      : null;
  const boundFromCache =
    activeBoundSessionKey && currentRootId
      ? (sessionCacheRef.current[
          rootSessionKey(currentRootId, activeBoundSessionKey)
        ] as any)
      : null;
  const isDetachedMainSessionTarget =
    !!activeBoundSessionKey &&
    selectedInCurrentRoot &&
    !!selectedKey &&
    selectedKey !== activeBoundSessionKey &&
    interactionMode !== "drawer";
  const actionBarSession = lockedSessionKey
    ? isDetachedMainSessionTarget
      ? (selectedSession as any)
      : (lockedSessionSnapshot as any) ||
        (currentSession as any) ||
        boundFromCache ||
        boundFromSelected
    : selectedInCurrentRoot
      ? (selectedSession as any)
      : null;
  const actionBarSessionKey =
    (actionBarSession as any)?.key ||
    (actionBarSession as any)?.session_key ||
    "";
  const actionBarInputHistory = sessionInputHistory(
    getSessionSnapshot(
      ((actionBarSession as any)?.root_id as string | undefined) || currentRootId,
      actionBarSession as any,
    ) || (actionBarSession as any),
  );
  const isBoundSessionInMain =
    !!activeBoundSessionKey &&
    selectedKey === activeBoundSessionKey &&
    interactionMode !== "drawer" &&
    mainView === "chat";
  const canOpenSessionDrawer = !!activeBoundSessionKey && !isBoundSessionInMain;
  const detachedBoundSession =
    isDetachedMainSessionTarget && !isDrawerOpen;
  const actionBarQueuedMessages = useMemo(() => {
    void queueVersion;
    if (!currentRootId || !actionBarSessionKey) return [];
    return (
      queuedMessagesBySessionRef.current[
        rootSessionKey(currentRootId, actionBarSessionKey)
      ] || []
    );
  }, [actionBarSessionKey, currentRootId, queueVersion, rootSessionKey]);

  const matchedPlugin = useMemo(() => {
    if (!currentRootId || !file) return null;
    const input = toPluginInput(file, pluginQuery);
    return pluginManagerRef.current.match(currentRootId, input);
  }, [currentRootId, file, pluginVersion, pluginQuery]);

  const { pluginRender, pluginThemeVars } = useAppPluginRender({
    file, pluginBypass, matchedPlugin, pluginQuery, pluginManagerRef,
  });

  useAppPluginRenderEffects({
    file, currentFileEditing, pluginBypass, matchedPlugin,
    pluginRender, pluginQuery, currentRootId, actionHandlers,
    fileCursorRef, handleViewerSelectionChange, pluginContentRef,
    lastPluginChapterRef, fullUpgradeAttemptRef,
  });

  const pluginRendererKey = `${currentRootId || ""}:${file?.path || ""}:${fileCursorRef.current}:${JSON.stringify(pluginQuery)}`;

  const {
    selectedSessionSnapshot, sessionByKey, boardSessionByKey, selectedKanbanTask,
    selectedKanbanTaskSessionKey, selectedKanbanTaskSessionSnapshot,
  } = useAppSessionSnapshots({
    selectedSession, selectedSessionLoading, currentRootId, sessions,
    multiProjectSessionGroups, selectedKanbanTaskId, kanbanTasks,
    taskDetailsById, taskSessionKeysById, getSessionSnapshot,
  });

  useEffect(() => {
    if (selectedSessionSnapshot) {
      lastMainSessionSnapshotRef.current = selectedSessionSnapshot as Session;
    }
  }, [selectedSessionSnapshot]);

  const loadSessionKey = selectedSession?.key || selectedSession?.session_key || "";
  const loadRootID =
    (selectedSession?.root_id as string | undefined) || currentRootId || "";
  const loadSnapshotHasExchanges = hasSessionExchanges(
    selectedSessionSnapshot as Session | null,
  );

  useEffect(() => {
    sessionLoadFailureRef.current = {};
  }, [loadSessionKey, loadRootID]);

  useSessionRestore({boundSessionByRootRef, bumpCacheVersion, clearSessionStale, currentRootId, currentRootIdRef, hasSessionExchanges, isSessionStale, loadRootID, loadSessionKey, loadSnapshotHasExchanges, loadedSessionRef, loadingSessionRef, resolvePendingForSession, restoreActiveSession, rootSessionKey, scopedRootKey, selectedSessionLoading, sessionCacheRef, sessionLoadFailureRef, setDrawerSessionForRoot, setSelectedSession, toSessionItem, useEffect});

  return {
    actionBarInputHistory, actionBarQueuedMessages, actionBarSession, boardSessionByKey,
    canOpenSessionDrawer, detachedBoundSession, matchedPlugin, pluginRender, pluginRendererKey,
    pluginThemeVars, selectedKanbanTask, selectedKanbanTaskSessionSnapshot, selectedSessionSnapshot,
  };
}
