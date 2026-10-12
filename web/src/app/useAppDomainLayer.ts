import { useCallback, useEffect, useMemo, useRef } from "react";
import { sessionService } from "../services/session";
import type { QueuedUserMessage, RelatedFile } from "../services/session";
import { bootstrapService } from "../services/net/bootstrap";
import type { BootstrapState } from "../services/net/bootstrap";
import { reportError } from "../services/net/error";
import { setRootNodeMap } from "../services/net/rootNode";
import { resolveLockedSessionKey } from "../services/sessionLock";
import { useAppPluginRenderEffects } from "./useAppPluginRenderEffects";
import { useAppBootstrapEffects } from "./useAppBootstrapEffects";
import { useAppBoundSessionDir } from "./useAppBoundSessionDir";
import { useAppSessionSnapshots } from "./useAppSessionSnapshots";
import { useAppDomainDerived } from "./useAppDomainDerived";
import { useAppFileNavigation } from "./useAppFileNavigation";
import { useAppPluginTrust } from "./useAppPluginTrust";
import { useAppPluginRender } from "./useAppPluginRender";
import { closeTopBackLayer, consumeViewHistoryEntry, hasBackLayer } from "./useBackNavigation";
import { ManagedRootPayload, indexManagedRoots, mapManagedRootsToEntries, toPluginInput } from "./appMisc";
import { MainViewMode, readURLState } from "./appPath";
import { useActionHandlers } from "./useActionHandlers";
import { AttachedFileContext, MultiProjectSessionGroup, PendingSend, SessionItem, SlashCommandResult, ViewerSelection, WSStatus, hasSessionExchanges, sessionInputHistory, toSessionItem } from "./appSession";
import { loadLastRootId, loadLastRootNodeId } from "./appStorage";
import { useAppTaskDrawer } from "./useAppTaskDrawer";
import { useAppProjectRemoval } from "./useAppProjectRemoval";
import { useProjectLifecycle } from "./useProjectLifecycle";
import { useRealtimeEvents } from "./useRealtimeEvents";
import { useTaskWorkspaceEffects } from "./useTaskWorkspaceEffects";
import { useManagedRoots } from "./useManagedRoots";
import { usePluginHandlers } from "./usePluginHandlers";
import { useRelatedFileDiff } from "./useRelatedFileDiff";
import { usePopStateSync } from "./usePopStateSync";
import { useSessionRestore } from "./useSessionRestore";
import { useAppInit } from "./useAppInit";
import { useAttachedFileContext } from "./useAttachedFileContext";
import { useSessionQueueActions } from "./useSessionQueueActions";
import type { Session } from "../services/session";
import type { ActionHandlers } from "./useActionHandlers";
import type { DirectorySortMode, FileEntry } from "../services/prefs/directorySort";
import { FileEditStore } from "../services/fileEditing";
import type { FilePayload } from "../services/file";
import type { GitDiffPayload } from "../services/git";
import type { KanbanTask, TaskDetail } from "../services/tasks";
import type { MessageKey, MessageParams } from "../i18n";
import type { PluginManager, PluginSourceBundle } from "../plugins/manager";
import type { UpdateState } from "../services/update";

/**
 * 见 docs/plans/app-tsx-under-1000.md：分层聚合 hook。
 */
export type UseAppDomainLayerParams = {
  activeBoundSessionKey: string | null;
  appendAgentChunkForSession: (rootID: string, sessionKey: string, content: string, runtimeHint?: import("/home/xiaokubao/projects/mindfs/.worktree/task-35/web/src/app/useSessionStreamCache").SessionRuntimeMeta, eventTimestamp?: string, mindfsID?: string) => void;
  appendCompactNoticeForSession: (rootID: string, sessionKey: string, compactNotice: any, mindfsID?: string) => void;
  appendPlanUpdateForSession: (rootID: string, sessionKey: string, planUpdate: any, mindfsID?: string) => void;
  appendThoughtChunkForSession: (rootID: string, sessionKey: string, content: string, thoughtID?: string, mindfsID?: string) => void;
  appendTodoUpdateForSession: (rootID: string, sessionKey: string, todoUpdate: any, mindfsID?: string) => void;
  appendToolCallForSession: (rootID: string, sessionKey: string, toolCall: any, update: boolean, mindfsID?: string) => void;
  applyTaskDetails: (rootId: string, details: TaskDetail[], persist?: boolean) => void;
  attachContextWindowToLatestAssistant: (rootID: string, sessionKey: string, contextWindow?: { totalTokens?: number; modelContextWindow?: number; }, tokenUsage?: import("/home/xiaokubao/projects/mindfs/.worktree/task-35/web/src/services/sessionTypes").TokenUsage) => void;
  bootstrapState: BootstrapState;
  boundSessionByRootRef: React.RefObject<Record<string, string | null>>;
  bumpCacheVersion: () => void;
  cancelRequestedBySessionRef: React.RefObject<Record<string, boolean>>;
  clearSessionStale: (rootID: string | null | undefined, sessionKey: string | null | undefined) => void;
  currentFileEditing: boolean;
  currentRootId: string | null;
  currentRootIdRef: React.RefObject<string | null>;
  currentRootNodeId: string | null;
  currentRootNodeIdRef: React.RefObject<string | null>;
  currentSelectionSource: { path: string; name: string; } | null;
  currentSession: SessionItem | null;
  currentSessionRef: React.RefObject<SessionItem | null>;
  didInitRef: React.RefObject<boolean>;
  dismissedSelectionFileRef: React.RefObject<string | null>;
  drawerOpenByRootRef: React.RefObject<Record<string, boolean>>;
  drawerSessionByRootRef: React.RefObject<Record<string, SessionItem | null>>;
  entriesByPathRef: React.RefObject<Record<string, FileEntry[]>>;
  expandedRef: React.RefObject<string[]>;
  file: FilePayload | null;
  fileCursorRef: React.RefObject<number>;
  fileEditStore: FileEditStore;
  fileOpenRequestRef: React.RefObject<number>;
  fileRef: React.RefObject<FilePayload | null>;
  fileScrollPositionsRef: React.RefObject<Record<string, number>>;
  formatDirectoryLoadError: (message?: string | null) => string;
  fullUpgradeAttemptRef: React.RefObject<string>;
  getNodeIdForRoot: (rootId: string) => string | undefined;
  getSessionSnapshot: (rootId: string | null | undefined, session: Session | SessionItem | null | undefined) => any;
  gitDiff: GitDiffPayload | null;
  gitStatus: import("/home/xiaokubao/projects/mindfs/.worktree/task-35/web/src/services/git").GitStatusPayload | null;
  handleImportedSessionConfirmed: (agentSessionID: string, agentName: string) => void;
  handleSelectSession: (session: any, options?: { preserveTaskSelection?: boolean; preserveMainView?: boolean; }) => Promise<void>;
  handleSelectSessionRef: React.RefObject<((session: any, options?: { preserveTaskSelection?: boolean; preserveMainView?: boolean; }) => Promise<void>) | null>;
  interactionMode: "main" | "drawer";
  interactionModeRef: React.RefObject<"main" | "drawer">;
  invalidTreeCacheKeysRef: React.RefObject<Set<string>>;
  isDrawerOpen: boolean;
  isSessionStale: (rootID: string | null | undefined, sessionKey: string | null | undefined) => boolean;
  kanbanTasks: KanbanTask[];
  lastMainSessionSnapshotRef: React.RefObject<Session | null>;
  lastNonChatViewRef: React.RefObject<MainViewMode>;
  lastPluginChapterRef: React.RefObject<string>;
  lastViewerSelectionRef: React.RefObject<ViewerSelection | null>;
  loadKanbanTasks: (rootId?: string | null, force?: boolean) => Promise<void>;
  loadMultiProjectSessionGroups: () => Promise<void>;
  loadSessionsForRoot: (rootID: string, options?: { beforeTime?: string; afterTime?: string; replace?: boolean; force?: boolean; }) => Promise<void>;
  loadTaskTemplates: () => Promise<void>;
  loadedSessionRef: React.RefObject<Record<string, boolean>>;
  loadingSessionRef: React.RefObject<Partial<Record<string, Promise<import("/home/xiaokubao/projects/mindfs/.worktree/task-35/web/src/services/sessionTypes").SyncSessionResult>>>>;
  mainDirectoryErrorRef: React.RefObject<string>;
  mainEntries: FileEntry[];
  mainEntriesRef: React.RefObject<FileEntry[]>;
  mainView: MainViewMode;
  mainViewRef: React.RefObject<MainViewMode>;
  managedRootByIdRef: React.RefObject<Record<string, ManagedRootPayload>>;
  managedRootByKeyRef: React.RefObject<Record<string, ManagedRootPayload>>;
  managedRootIdsRef: React.RefObject<Set<string>>;
  managedRootsRequestRef: React.RefObject<Promise<ManagedRootPayload[] | null> | null>;
  markSessionStale: (rootID: string | null | undefined, sessionKey: string | null | undefined) => void;
  multiProjectPendingByKey: Record<string, boolean>;
  multiProjectPendingRef: React.RefObject<Record<string, boolean>>;
  multiProjectSessionGroups: MultiProjectSessionGroup[];
  multiProjectSessionsEnabled: true;
  nodeLoadFailuresRef: React.RefObject<{ id: string; name: string; }[]>;
  notifiedNodeFailuresRef: React.RefObject<Set<string>>;
  notifyNodeLoadFailedRef: React.RefObject<(node: { id: string; name: string; }) => void>;
  openGitDiff: (rootID: string, item: import("/home/xiaokubao/projects/mindfs/.worktree/task-35/web/src/services/git").GitStatusItem, options?: { preserveRelatedSelection?: boolean; repoPath?: string; }) => Promise<void>;
  optimisticDequeuedIdsRef: React.RefObject<Record<string, Set<string>>>;
  pendingBySessionRef: React.RefObject<Record<string, PendingSend>>;
  pendingDraftRef: React.RefObject<PendingSend | null>;
  pendingPluginTrust: { rootId: string; bundle: PluginSourceBundle; } | null;
  pendingRequestRef: React.RefObject<Record<string, PendingSend>>;
  playCompletionSound: () => void;
  pluginBypass: boolean;
  pluginBypassRef: React.RefObject<boolean>;
  pluginContentRef: React.RefObject<HTMLDivElement | null>;
  pluginManagerRef: React.RefObject<PluginManager>;
  pluginQuery: Record<string, string>;
  pluginVersion: number;
  pluginsLoadedByRootRef: React.RefObject<Record<string, boolean>>;
  pluginsLoadingByRootRef: React.RefObject<Record<string, Promise<void>>>;
  pluginsTrustPendingByRootRef: React.RefObject<Record<string, boolean>>;
  promotePendingSessionForRoot: (rootID: string, tempKey: string | undefined, sessionKey: string, fallback?: Session | null) => void;
  pruneTaskDetails: (rootId: string, keepTaskIds: Iterable<string>) => void;
  queueFrozenBySessionRef: React.RefObject<Record<string, boolean>>;
  queueVersion: number;
  queuedMessagesBySessionRef: React.RefObject<Record<string, QueuedUserMessage[]>>;
  refreshCurrentFileContent: (rootID: string, changedPath: string) => Promise<void>;
  refreshGitHistory: (rootID: string, options?: { force?: boolean; waitForIncremental?: boolean; }) => Promise<import("/home/xiaokubao/projects/mindfs/.worktree/task-35/web/src/services/git").GitHistoryPayload | null>;
  refreshGitStatus: (rootID: string) => Promise<import("/home/xiaokubao/projects/mindfs/.worktree/task-35/web/src/services/git").GitStatusPayload | null>;
  refreshMultiProjectReplyingSessions: () => Promise<void>;
  refreshTaskWorktree: (rootId: string, worktreePath: string, force?: boolean) => Promise<void>;
  refreshTreeDir: (rootID: string, dirPath: string, syncMain: boolean) => Promise<void>;
  refreshWorkspaceBoard: () => void;
  rememberCurrentFileScroll: () => void;
  replaceURLState: (next: import("/home/xiaokubao/projects/mindfs/.worktree/task-35/web/src/app/appPath").URLState) => void;
  resetLocksForRootTransition: (targetRoot: string | null | undefined) => void;
  resolvePendingForSession: (rootID: string | null | undefined, sessionKey: string | null | undefined, fallback?: boolean) => boolean;
  resolveRootForSessionKey: (sessionKey: string) => string | null;
  restoreActiveSession: (rootID: string | null | undefined, sessionKey: string | null | undefined) => Promise<Session | null>;
  rootEntriesRef: React.RefObject<FileEntry[]>;
  rootSessionKey: (rootId: string, sessionKey: string) => string;
  scheduleMultiProjectSessionReload: () => void;
  scheduleSessionListReload: (rootID: string, options?: { beforeTime?: string; afterTime?: string; replace?: boolean; force?: boolean; }) => void;
  scopedRootKey: (rootId: string) => string;
  selectRootNode: (rootId: string, nodeId?: string) => void;
  selectedDirRef: React.RefObject<string | null>;
  selectedKanbanTaskId: string;
  selectedSession: SessionItem | null;
  selectedSessionByRootRef: React.RefObject<Record<string, string | null>>;
  selectedSessionLoading: boolean;
  selectedSessionRef: React.RefObject<SessionItem | null>;
  sessionCacheRef: React.RefObject<Record<string, Session>>;
  sessionListReloadTimerRef: React.RefObject<number | null>;
  sessionLoadFailureRef: React.RefObject<Record<string, { at: number; status: number; }>>;
  sessions: SessionItem[];
  sessionsRef: React.RefObject<SessionItem[]>;
  setActiveBoundSessionKey: React.Dispatch<React.SetStateAction<string | null>>;
  setAgentsVersion: React.Dispatch<React.SetStateAction<number>>;
  setAttachedFileContext: React.Dispatch<React.SetStateAction<AttachedFileContext | null>>;
  setBootstrapState: React.Dispatch<React.SetStateAction<BootstrapState>>;
  setBoundSessionForRoot: (rootID: string | null | undefined, key: string | null) => void;
  setCodexRateLimitsRefreshToken: React.Dispatch<React.SetStateAction<number>>;
  setCurrentRootId: React.Dispatch<React.SetStateAction<string | null>>;
  setCurrentSession: React.Dispatch<React.SetStateAction<SessionItem | null>>;
  setDirectorySortOverrides: React.Dispatch<React.SetStateAction<Record<string, DirectorySortMode>>>;
  setDrawerOpenForRoot: (rootID: string | null | undefined, open: boolean) => void;
  setDrawerSessionForRoot: (rootID: string | null | undefined, session: Session | SessionItem | null) => void;
  setEntriesByPath: React.Dispatch<React.SetStateAction<Record<string, FileEntry[]>>>;
  setExpanded: React.Dispatch<React.SetStateAction<string[]>>;
  setExternalSelectedKey: React.Dispatch<React.SetStateAction<string>>;
  setExternalSessions: React.Dispatch<React.SetStateAction<SessionItem[]>>;
  setFile: React.Dispatch<React.SetStateAction<FilePayload | null>>;
  setGitDiff: React.Dispatch<React.SetStateAction<GitDiffPayload | null>>;
  setGitHistory: React.Dispatch<React.SetStateAction<import("/home/xiaokubao/projects/mindfs/.worktree/task-35/web/src/services/git").GitHistoryPayload | null>>;
  setGitHistoryExpandedByRoot: React.Dispatch<React.SetStateAction<Record<string, Record<string, boolean>>>>;
  setGitStatus: React.Dispatch<React.SetStateAction<import("/home/xiaokubao/projects/mindfs/.worktree/task-35/web/src/services/git").GitStatusPayload | null>>;
  setGitStatusExpandedByRoot: React.Dispatch<React.SetStateAction<Record<string, boolean>>>;
  setInteractionMode: React.Dispatch<React.SetStateAction<"main" | "drawer">>;
  setIsDrawerOpen: React.Dispatch<React.SetStateAction<boolean>>;
  setIsLeftOpen: React.Dispatch<React.SetStateAction<boolean>>;
  setMainDirectoryError: React.Dispatch<React.SetStateAction<string>>;
  setMainEntries: React.Dispatch<React.SetStateAction<FileEntry[]>>;
  setMainView: React.Dispatch<React.SetStateAction<MainViewMode>>;
  setManagedRootIds: React.Dispatch<React.SetStateAction<string[]>>;
  setMultiProjectPendingByKey: React.Dispatch<React.SetStateAction<Record<string, boolean>>>;
  setMultiProjectSessionGroups: React.Dispatch<React.SetStateAction<MultiProjectSessionGroup[]>>;
  setMultiProjectSessionPending: (rootID: string | null | undefined, sessionKey: string | null | undefined, pending: boolean) => void;
  setPendingPluginTrust: React.Dispatch<React.SetStateAction<{ rootId: string; bundle: PluginSourceBundle; } | null>>;
  setPluginBypass: React.Dispatch<React.SetStateAction<boolean>>;
  setPluginLoading: React.Dispatch<React.SetStateAction<boolean>>;
  setPluginQuery: React.Dispatch<React.SetStateAction<Record<string, string>>>;
  setPluginVersion: React.Dispatch<React.SetStateAction<number>>;
  setQueueVersion: React.Dispatch<React.SetStateAction<number>>;
  setRelatedSelectedFileKey: React.Dispatch<React.SetStateAction<string>>;
  setRootEntries: React.Dispatch<React.SetStateAction<FileEntry[]>>;
  setSelectedDir: React.Dispatch<React.SetStateAction<string | null>>;
  setSelectedDirKey: React.Dispatch<React.SetStateAction<string | null>>;
  setSelectedKanbanTaskId: React.Dispatch<React.SetStateAction<string>>;

  setSelectedSession: React.Dispatch<React.SetStateAction<SessionItem | null>>;
  setSelectedSessionLoading: React.Dispatch<React.SetStateAction<boolean>>;
  setSessionListMode: React.Dispatch<React.SetStateAction<"local" | "import">>;
  setSessions: React.Dispatch<React.SetStateAction<SessionItem[]>>;
  setSlashCommandResults: React.Dispatch<React.SetStateAction<Record<string, SlashCommandResult>>>;
  setStatus: React.Dispatch<React.SetStateAction<WSStatus>>;
  setTaskRelatedFilesById: React.Dispatch<React.SetStateAction<Record<string, RelatedFile[]>>>;
  setTaskSessionErrorDialog: React.Dispatch<React.SetStateAction<{ title: string; message: string; details: string[]; } | null>>;
  setUpdateState: React.Dispatch<React.SetStateAction<UpdateState>>;
  setViewerSelection: React.Dispatch<React.SetStateAction<ViewerSelection | null>>;
  showHiddenFiles: true;
  staleSessionKeysRef: React.RefObject<Set<string>>;
  suppressedAutoBindSessionByRootRef: React.RefObject<Record<string, string | null>>;
  switchMainView: (mode: MainViewMode) => void;
  t: (key: MessageKey, params?: MessageParams) => string;
  taskDetailsById: Record<string, TaskDetail>;
  taskDetailsByIdRef: React.RefObject<Record<string, TaskDetail>>;
  taskSessionKeysById: Record<string, string[]>;
  taskSessionKeysByIdRef: React.RefObject<Record<string, string[]>>;
  treeCacheKey: (rootID: string, dirPath: string) => string;
  tryShowBoundSessionForRoot: (rootID: string | null | undefined, options?: { pluginQuery?: Record<string, string>; closeLeftSidebar?: boolean; }) => Promise<boolean>;
  updateSessionAgentForKey: (rootID: string, sessionKey: string, agent: string, model?: string, agentMode?: string, effort?: string, fastService?: "" | "on" | "off", planMode?: boolean, shell?: string) => void;
  updateSessionRelatedFilesForKey: (rootID: string, sessionKey: string, relatedFiles: RelatedFile[]) => void;
  updateSessionRelatedWorktreeForKey: (rootID: string, sessionKey: string, relatedWorktree: import("/home/xiaokubao/projects/mindfs/.worktree/task-35/web/src/services/sessionTypes").RelatedWorktree | null | undefined) => void;
  viewerSelection: ViewerSelection | null;
  viewerSelectionRef: React.RefObject<ViewerSelection | null>;
  workspaceOpenRef: React.RefObject<boolean>;
};

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
