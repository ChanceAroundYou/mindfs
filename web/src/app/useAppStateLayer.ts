import { useCallback, useEffect, useRef, useState } from "react";
import { FileEditStore } from "../services/fileEditing";
import { bootstrapService } from "../services/net/bootstrap";
import { fetchEditableFile, saveTextFile } from "../services/file";
import { syncNodesFromServer } from "../services/net/nodeRegistry";
import { useI18n } from "../i18n";
import { shouldAutoStartOnboarding } from "../services/prefs/onboarding";
import { useAppRefSync } from "./useAppRefSync";
import { useAppProjectDisplay } from "./useAppProjectDisplay";
import { useAppRootBindingSetters } from "./useAppRootBindingSetters";
import { useAppReplaceURLState } from "./useAppReplaceURLState";
import { MainViewMode } from "./appPath";
import { useBackLayer } from "../hooks/useBackLayer";
import { SessionItem, SlashCommandResult } from "./appSession";
import { getDirectorySortKey } from "./appUtils";
import { useAppStateRefs } from "./useAppStateRefs";
import { useAppSessionListState } from "./useAppSessionListState";
import { useAppSessionSelectionState } from "./useAppSessionSelectionState";
import { useAppResponsiveState } from "./useAppResponsiveState";
import { useAppRootScopeState } from "./useAppRootScopeState";
import { useAppTaskTemplateState } from "./useAppTaskTemplateState";
import { useAppRootSelection } from "./useAppRootSelection";
import { useAppFileWorkspaceState } from "./useAppFileWorkspaceState";
import { useCompletionSound } from "./useCompletionSound";
import { useTaskWorkspaceState } from "./useTaskWorkspaceState";
import { useUiPreferences } from "./useUiPreferences";
import { useTaskBoard } from "./useTaskBoard";
import { useTaskComposer } from "./useTaskComposer";
import type { BootstrapState } from "../services/net/bootstrap";
import type { FileEntry } from "../services/prefs/directorySort";
import type { AgentConfigSwitchRequest } from "../components/file/fileTreeTypes";
import type { MainContentViewMode } from "../components/file/DefaultListView";
import type { AgentStatus } from "../services/agents";

/**
 * 见 docs/plans/app-tsx-under-1000.md：分层聚合 hook。
 */
export type UseAppStateLayerParams = {
};

/**
 * 本层由 App.tsx 原样搬出，hook 调用顺序与搬出前逐字一致。
 */
export function useAppStateLayer(params: UseAppStateLayerParams) {
  const [fileEditStore] = useState(() => new FileEditStore({ load: fetchEditableFile, save: saveTextFile }));
  const { t } = useI18n();
  const { playCompletionSound } = useCompletionSound();
  const {
    pluginManagerRef, managedRootIdsRef, expandedRef, selectedDirRef, fileRef, selectedSessionRef,
    lastMainSessionSnapshotRef, sessionSearchTargetCounterRef, currentSessionRef, interactionModeRef,
    pendingDraftRef, pendingBySessionRef, pendingRequestRef, queuedMessagesBySessionRef,
    queueFrozenBySessionRef, optimisticDequeuedIdsRef, cancelRequestedBySessionRef,
    sessionCacheRef, loadedSessionRef, loadingSessionRef, sessionLoadFailureRef,
    sessionCacheAccessRef, staleSessionKeysRef, enforceSessionCacheBound, invalidTreeCacheKeysRef,
    nodeLoadFailuresRef, notifiedNodeFailuresRef, notifyNodeLoadFailedRef, treeFetchSeqRef,
    boundSessionByRootRef, suppressedAutoBindSessionByRootRef, drawerSessionByRootRef,
    selectedSessionByRootRef, drawerOpenByRootRef, drawerScrollRef, fileCursorRef,
    fileScrollPositionsRef, pluginContentRef, lastPluginChapterRef, viewerSelectionRef,
    lastViewerSelectionRef, dismissedSelectionFileRef, lastPluginResetFileKeyRef, pluginBypassRef,
    fileOpenRequestRef, fullUpgradeAttemptRef, pluginsLoadedByRootRef, pluginsLoadingByRootRef,
    pluginsTrustPendingByRootRef, didInitRef, managedRootsRequestRef, handleSelectSessionRef,
  } = useAppStateRefs();

  const {
    sessions, setSessions, sessionsRef, multiProjectSessionsEnabled, MULTI_PROJECT_RELOAD_DEBOUNCE_MS,
    multiProjectSessionGroups, setMultiProjectSessionGroups, multiProjectSessionGroupsRef,
    multiProjectSessionsLoading, setMultiProjectSessionsLoading, multiProjectLoadSeqRef,
    multiProjectPendingByKey, setMultiProjectPendingByKey, multiProjectPendingRef, applyPinSnapshot,
    multiProjectAwaitingByKey, setMultiProjectAwaitingByKey, multiProjectAwaitingRef,
    syncingSessionKeys, setSyncingSessionKeys, archivedGroups, setArchivedGroups, archiveOpen,
    setArchiveOpen, archiveLoading, setArchiveLoading, archiveReloadToken, setArchiveReloadToken,
    hasMoreSessions, setHasMoreSessions, loadingOlderSessions, setLoadingOlderSessions,
    sessionListMode, setSessionListMode,
  } = useAppSessionListState();
  const [availableAgents, setAvailableAgents] = useState<AgentStatus[]>([]);
  const [scheduledAgentDialogOpen, setScheduledAgentDialogOpen] = useState(false);
  const { kanbanTasks, setKanbanTasks, kanbanTaskCountItems, setKanbanTaskCountItems, taskDetailsById, setTaskDetailsById, taskFirstInputById, setTaskFirstInputById, taskSessionKeysById, setTaskSessionKeysById, taskRelatedFilesById, setTaskRelatedFilesById, taskDetailsByIdRef, workspaceBoardRefreshRef, taskSessionKeysByIdRef, selectedKanbanTaskId, setSelectedKanbanTaskId, expandedTaskInputIds, setExpandedTaskInputIds, overflowingTaskInputs, setOverflowingTaskInputs, collapsedTaskCompletionGroups, setCollapsedTaskCompletionGroups, collapsedKanbanColumns, setCollapsedKanbanColumns, taskInlineEdit, setTaskInlineEdit, taskSessionErrorDialog, setTaskSessionErrorDialog, taskInlineActiveToken, setTaskInlineActiveToken, taskInlineCandidates, setTaskInlineCandidates, taskInlineCandidateIndex, setTaskInlineCandidateIndex, taskInlineSaving, setTaskInlineSaving, taskInlineHelpKey, setTaskInlineHelpKey, taskInlineUploadProgress, setTaskInlineUploadProgress, directoryUploadProgress, setDirectoryUploadProgress, taskWorktreeBranches, setTaskWorktreeBranches, taskWorktreeBranchesLoading, setTaskWorktreeBranchesLoading, taskWorktreeBranchError, setTaskWorktreeBranchError, kanbanTasksLoading, setKanbanTasksLoading, kanbanLoadSeqRef, kanbanAbortRef, refreshTaskWorktreeRef, taskWorktreeBranchRequestRef, sessionListLoadSeqRef, sessionListAbortRef, listLoadErrorAtRef, taskInlineEditorRef, taskInlineCandidateItemRefs, taskInlineAttachmentInputRef, taskInlineUploadAbortRef, directoryUploadAbortRef, knownTaskWorktreePathsRef } = useTaskWorkspaceState();
  const {
    selectedSession, setSelectedSession, selectedSessionLoading, setSelectedSessionLoading,
    drawerLoadingSessionByRoot, setDrawerLoadingSessionByRoot, activeBoundSessionKey,
    setActiveBoundSessionKey, pendingPlanMode, setPendingPlanMode, currentSession, setCurrentSession,
    cacheVersion, setCacheVersion, slashCommandResults, setSlashCommandResults, copiedSlashCommandKeys,
    setCopiedSlashCommandKeys, queueVersion, setQueueVersion, interactionMode, setInteractionMode,
    agentsVersion, setAgentsVersion, codexRateLimitsRefreshToken, setCodexRateLimitsRefreshToken,
    isDrawerOpen, setIsDrawerOpen,
  } = useAppSessionSelectionState();
  const {
    isMobile, isTablet, isLeftOpen, setIsLeftOpen, isRightOpen, setIsRightOpen,
    onboardingOpen, setOnboardingOpen, onboardingMainContentViewRoot,
    setOnboardingMainContentViewRoot, onboardingAutoStartRef,
  } = useAppResponsiveState();
  const {
    currentRootId, setCurrentRootId, currentRootIdRef, currentRootNodeId, setCurrentRootNodeId,
    currentRootNodeIdRef, managedRootByIdRef, managedRootByKeyRef, getNodeIdForRoot, scopedRootKey,
  } = useAppRootScopeState();

  const {
    taskTemplateActionMenuRef, taskCreateTemplateMenuRef, taskTemplates, taskTemplateDialogOpen,
    setTaskTemplateDialogOpen, taskTemplateDialogTemplate, taskTemplateFilter, setTaskTemplateFilter,
    taskTemplateActionMenuOpen, setTaskTemplateActionMenuOpen, taskCreateTemplateMenuOpen,
    setTaskCreateTemplateMenuOpen, loadTaskTemplates, openTaskTemplateEditor,
    handleTaskTemplateSaved, handleDeleteTaskTemplate,
  } = useAppTaskTemplateState({
    currentRootId, scopedRootKey, getNodeIdForRoot, taskInlineEdit, taskInlineEditorRef,
    taskInlineActiveToken, setTaskInlineCandidateIndex, setTaskInlineCandidates,
  });

  const [mainView, setMainView] = useState<MainViewMode>(() => "workspace");
  const workspaceOpen = mainView === "workspace";
  const workspaceOpenRef = useRef(workspaceOpen);
  workspaceOpenRef.current = workspaceOpen;

  const {
    applyTaskDetails,
    pruneTaskDetails,
    refreshWorkspaceBoard,
    loadKanbanTasks,
    handleMoveKanbanTask,
    kanbanRefreshSpin,
  } = useTaskBoard({
    currentRootId,
    currentRootNodeId,
    currentRootIdRef,
    currentRootNodeIdRef,
    managedRootByIdRef,
    taskDetailsById,
    taskDetailsByIdRef,
    taskTemplateFilter,
    kanbanTasks,
    selectedKanbanTaskId,
    kanbanLoadSeqRef,
    kanbanAbortRef,
    workspaceBoardRefreshRef,
    workspaceOpenRef,
    refreshTaskWorktreeRef,
    setTaskDetailsById,
    setTaskFirstInputById,
    setTaskSessionKeysById,
    setKanbanTaskCountItems,
    setKanbanTasks,
    setKanbanTasksLoading,
    setSelectedKanbanTaskId,
    setTaskSessionErrorDialog,
    getNodeIdForRoot,
    t,
  });

  const {
    loadTaskWorktreeBranches,
    openTaskCreateDialog,
    switchTaskInlineEditProject,
    closeTaskEditDialog,
    applyTaskInlineCandidate,
    appendTaskInlineAttachments,
    handleTaskInlineAttachmentChange,
    removeTaskInlineAttachment,
    saveTaskInlineEdit,
  } = useTaskComposer({
    currentRootId,
    currentRootIdRef,
    currentRootNodeIdRef,
    managedRootByIdRef,
    managedRootByKeyRef,
    taskInlineEdit,
    taskTemplates,
    taskInlineCandidates,
    taskInlineCandidateIndex,
    taskInlineUploadAbortRef,
    taskInlineEditorRef,
    taskInlineCandidateItemRefs,
    refreshTaskWorktreeRef,
    taskWorktreeBranchRequestRef,
    setTaskInlineEdit,
    setTaskInlineActiveToken,
    setTaskInlineCandidates,
    setTaskInlineCandidateIndex,
    setTaskInlineHelpKey,
    setTaskInlineSaving,
    setTaskInlineUploadProgress,
    setTaskWorktreeBranches,
    setTaskWorktreeBranchesLoading,
    setTaskWorktreeBranchError,
    getNodeIdForRoot,
    t,
    applyTaskDetails,
  });

  const {
    slashCopyResetTimersRef,
    mobileEnterKeySends,
    setMobileEnterKeySends,
    sendShortcut,
    setSendShortcut,
    sidebarsSwapped,
    setSidebarsSwapped,
    fontSizePreferences,
    setFontSizePreferences,
    gitDiffSideBySide,
    setGitDiffSideBySide,
  } = useUiPreferences();
  const [managedRootIds, setManagedRootIds] = useState<string[]>([]);
  const selectRootNode = useAppRootSelection({
    currentRootNodeIdRef,
    currentRootIdRef,
    getSessionListReloadTimerRef: () => sessionListReloadTimerRef,
    sessionListAbortRef,
    kanbanAbortRef,
    kanbanLoadSeqRef,
    sessionListLoadSeqRef,
    setCurrentRootNodeId,
    selectedSessionByRootRef,
    boundSessionByRootRef,
    drawerSessionByRootRef,
    setSelectedSession,
    sessionCacheRef,
    taskDetailsByIdRef,
    setTaskDetailsById,
    setTaskFirstInputById,
    setTaskSessionKeysById,
    setTaskRelatedFilesById,
    setKanbanTasks,
    setKanbanTaskCountItems,
    setKanbanTasksLoading,
    setSessions,
    getGitState: () => ({ setGitStatus, setGitHistory, setGitDiff, setGitStatusLoading, setGitHistoryLoading }),
    managedRootByKeyRef,
    managedRootByIdRef,
  });

  const [rootEntries, setRootEntries] = useState<FileEntry[]>([]);
  // open_dir 回调需要根列表做根展开互斥，经 ref 引用避免依赖抖动
  const rootEntriesRef = useRef<FileEntry[]>([]);
  useEffect(() => {
    rootEntriesRef.current = rootEntries;
  }, [rootEntries]);
  const {
    getRootDisplayName,
    getDisplayNodeColor,
    taskCreateProjectOptions,
  } = useAppProjectDisplay({
    currentRootId,
    currentRootNodeId,
    currentRootNodeIdRef,
    managedRootByIdRef,
    managedRootByKeyRef,
    managedRootIdsRef,
    rootEntries,
    getNodeIdForRoot,
    taskInlineEdit,
  });
  const currentRootDisplayName = getRootDisplayName(currentRootId);
  const [agentConfigSwitchRequest, setAgentConfigSwitchRequest] =
    useState<AgentConfigSwitchRequest | null>(null);
  const [bootstrapState, setBootstrapState] = useState<BootstrapState>(() =>
    bootstrapService.snapshot(),
  );
  useEffect(() => {
    if (
      isMobile ||
      onboardingAutoStartRef.current ||
      !currentRootId ||
      bootstrapState.phase !== "ready"
    ) {
      return;
    }
    onboardingAutoStartRef.current = true;
    if (!shouldAutoStartOnboarding()) return;
    const timer = window.setTimeout(() => setOnboardingOpen(true), 700);
    return () => window.clearTimeout(timer);
  }, [bootstrapState.phase, currentRootId, isMobile]);
  useEffect(() => {
    if (isMobile && onboardingOpen) {
      setOnboardingOpen(false);
      setOnboardingMainContentViewRoot(null);
    }
  }, [isMobile, onboardingOpen]);
  useEffect(() => { syncNodesFromServer().catch(()=>{}); }, []);
  const {
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
    pluginQuery, setPluginQuery, pluginQueryRef, showHiddenFiles, projectTreeTabRequest,
    setProjectTreeTabRequest, projectTreeTab, setProjectTreeTab, worktreeItemsByRoot,
    worktreeLoadingByRoot, worktreeErrorByRoot, expandedWorktreeByRoot, worktreeStatusByPath,
    worktreeStatusLoadingByPath, loadProjectTreeWorktrees, loadProjectTreeWorktreeStatus,
    toggleProjectTreeWorktree, expandProjectTreeWorktree, refreshProjectTreeWorktrees,
    refreshTaskWorktree, updateState, setUpdateState, updateSubmitting, setUpdateSubmitting,
  } = useAppFileWorkspaceState({
    currentRootId, currentRootIdRef, currentRootNodeId, currentRootNodeIdRef,
    managedRootByIdRef, scopedRootKey, getNodeIdForRoot, mainView, fileEditStore,
    knownTaskWorktreePathsRef, refreshTaskWorktreeRef,
  });

  useAppRefSync({
    currentRootIdRef, currentRootId, currentRootNodeIdRef, currentRootNodeId,
    setAvailableAgents, agentsVersion, getNodeIdForRoot,
    expandedRef, expanded, selectedDirRef, selectedDir,
    entriesByPathRef, entriesByPath, fileRef, file,
    viewerSelectionRef, viewerSelection, pluginQueryRef, pluginQuery,
    lastPluginResetFileKeyRef, pluginBypassRef, setPluginBypass, pluginBypass,
    selectedSessionRef, selectedSession, activeBoundSessionKey, setActiveBoundSessionKey,
    interactionMode, setPendingPlanMode, sessionsRef, sessions,
    multiProjectSessionGroupsRef, multiProjectSessionGroups, multiProjectPendingRef, multiProjectPendingByKey,
    multiProjectAwaitingRef, multiProjectAwaitingByKey,
    currentSessionRef, currentSession, interactionModeRef, treeSortMode,
    directorySortOverrides, scopedRootKey, boundSessionByRootRef, setCurrentSession,
    drawerSessionByRootRef, setIsDrawerOpen, drawerOpenByRootRef,
  });
  // 切项目时顺带刷新主节点的会话置顶 —— 这就是约定的「跨设备同步」时机。
  //
  // 用户明确不要跨设备实时（那要一条主节点 → 各 worker 浏览器的新通道），
  // 也不要每次都刷：「切换项目等操作的时候顺带请求一下置顶」正是这个意思。
  // 不刷新的话，设备 A 置顶后设备 B 切项目也看不到 —— 那正是被报的那个症状。

  const {
    setBoundSessionForRoot,
    setDrawerSessionForRoot,
    setDrawerOpenForRoot,
    resetSessionLockForRoot,
    resetLocksForRootTransition,
  } = useAppRootBindingSetters({
    boundSessionByRootRef,
    scopedRootKey,
    currentRootIdRef,
    setActiveBoundSessionKey,
    drawerSessionByRootRef,
    setCurrentSession,
    drawerOpenByRootRef,
    setIsDrawerOpen,
    selectedSessionByRootRef,
    selectedSessionRef,
    setSelectedSession,
    setSelectedSessionLoading,
    interactionModeRef,
    setInteractionMode,
  });





  const currentDirectorySortKey = getDirectorySortKey(
    currentRootId,
    selectedDir,
  );
  const currentDirectorySortOverride = currentDirectorySortKey
    ? directorySortOverrides[currentDirectorySortKey]
    : undefined;
  // 主区内容派生：workspace/board 走看板容器，files 走文件列表；chat 由会话视图占据主区。
  const currentMainContentView: MainContentViewMode =
    currentRootId && onboardingMainContentViewRoot === currentRootId
      ? "task-kanban"
      : mainView === "files"
        ? "file-browser"
        : "task-kanban";
  // 跨项目工作台是「模式」，不再是「没有打开项目」的副产物（后者会被自动选根冲掉）。
  // task.updated 的处理器要按这个放行跨项目推送（板态只收当前项目），
  // 而它是被 useRealtimeEvents 消费的——只能走 ref，不能直接传值。
  // 会话面板只在对话态显示。判据是 mainView 而不是 selectedSession——
  // 否则看板/文件/工作台态下，仍被选中的会话会盖住主区（切面板不解除选中）。
  const showSessionPane = mainView === "chat" && !!selectedSession;
  const switchMainView = useCallback((mode: MainViewMode) => {
    if (mode !== "chat") {
      lastNonChatViewRef.current = mode;
    }
    mainViewRef.current = mode;
    setMainView(mode);
  }, []);

  /* 覆盖层接进返回栈：按返回/侧滑时先关最上面那一层，而不是退视图或退出应用。
     四个接线点摆在一起是因为它们互为兄弟（都是「盖在主区之上」的东西），
     放在一起才看得出「返回」的优先级：弹窗 > 面板 > 悬浮框 > 退视图。
     关闭动作都复用各层既有的关闭路径，所以 PanelShell 的「放弃改动？」确认照旧生效。 */
  useBackLayer(scheduledAgentDialogOpen, () => setScheduledAgentDialogOpen(false));
  useBackLayer(taskTemplateDialogOpen, () => setTaskTemplateDialogOpen(false));
  useBackLayer(!!taskInlineEdit, () => {
    // 保存中不给关：和面板上那个 × 按钮同一条规则。
    if (!taskInlineSaving) closeTaskEditDialog();
  });
  useBackLayer(isDrawerOpen && mainView === "files", () => {
    interactionModeRef.current = "main";
    setInteractionMode("main");
    setDrawerOpenForRoot(currentRootIdRef.current, false);
  });

  const currentDirectorySortMode = currentDirectorySortOverride || treeSortMode;

  const { replaceURLState } = useAppReplaceURLState({
    mainViewRef,
    currentRootIdRef,
    currentRootNodeIdRef,
  });

  // 会话列表重拉防抖定时器。刻意声明在「状态/ref 层」而不是 useAppSessionLayer：
  // 本层靠前的跨节点切换回调（见 selectRootNode 同名函数内）要清掉它，拆层前是同文件闭包，
  // 拆层后必须由本层持有、再作为参数传给会话层。放在本层末尾是为了让它在 hook 序列里位移最小。
  const sessionListReloadTimerRef = useRef<number | null>(null);

  return {
    MULTI_PROJECT_RELOAD_DEBOUNCE_MS, activeBoundSessionKey, agentConfigSwitchRequest, agentsVersion,
    applyPinSnapshot, applyTaskDetails, applyTaskInlineCandidate, archiveLoading,
    archiveOpen, archiveReloadToken, archivedGroups, attachedFileContext,
    availableAgents, bootstrapState, boundSessionByRootRef, cacheVersion,
    cancelRequestedBySessionRef, closeTaskEditDialog, codexRateLimitsRefreshToken, collapsedKanbanColumns,
    collapsedTaskCompletionGroups, copiedSlashCommandKeys, currentDirectorySortMode, currentDirectorySortOverride,
    currentFileEditing, currentMainContentView, currentRootDisplayName, currentRootId,
    currentRootIdRef, currentRootNodeId, currentRootNodeIdRef, currentSession,
    currentSessionRef, didInitRef, directoryUploadAbortRef, directoryUploadProgress,
    dismissedSelectionFileRef, drawerLoadingSessionByRoot, drawerOpenByRootRef, drawerScrollRef,
    drawerSessionByRootRef, editDraftRequest, enforceSessionCacheBound, entriesByPath,
    entriesByPathRef, expandProjectTreeWorktree, expanded, expandedRef,
    expandedTaskInputIds, expandedWorktreeByRoot, file, fileCursorRef,
    fileEditStore, fileOpenRequestRef, fileRef, fileScrollPositionsRef,
    fontSizePreferences, fullUpgradeAttemptRef, getDisplayNodeColor, getNodeIdForRoot,
    getRootDisplayName, gitDiff, gitDiffSideBySide, gitHistory,
    gitHistoryByRoot, gitHistoryExpandedByRoot, gitHistoryLoading, gitHistoryLoadingByRoot,
    gitHistoryLoadingMore, gitStatus, gitStatusByRoot, gitStatusExpandedByRoot,
    gitStatusLoading, gitStatusLoadingByRoot, handleDeleteTaskTemplate, handleMoveKanbanTask,
    handleSelectSessionRef, handleTaskInlineAttachmentChange, handleTaskTemplateSaved, hasMoreSessions,
    interactionMode, interactionModeRef, invalidTreeCacheKeysRef, isDrawerOpen,
    isLeftOpen, isMobile, isRightOpen, kanbanTasks,
    kanbanTasksLoading, lastMainSessionSnapshotRef, lastNonChatViewRef, lastPluginChapterRef,
    lastViewerSelectionRef, listLoadErrorAtRef, loadKanbanTasks, loadMoreGitHistory,
    loadTaskTemplates, loadedSessionRef, loadingOlderSessions, loadingSessionRef,
    mainDirectoryError, mainDirectoryErrorRef, mainEntries, mainEntriesRef,
    mainView, mainViewRef, managedRootByIdRef, managedRootByKeyRef,
    managedRootIds, managedRootIdsRef, managedRootsRequestRef, mobileEnterKeySends,
    multiProjectLoadSeqRef, multiProjectPendingByKey, multiProjectPendingRef, multiProjectAwaitingByKey, multiProjectAwaitingRef, multiProjectSessionGroups,
    multiProjectSessionGroupsRef, multiProjectSessionsEnabled, multiProjectSessionsLoading, nodeLoadFailuresRef,
    notifiedNodeFailuresRef, notifyNodeLoadFailedRef, onboardingOpen, openTaskCreateDialog,
    openTaskTemplateEditor, optimisticDequeuedIdsRef, pendingBySessionRef, pendingDraftRef,
    pendingPlanMode, pendingPluginTrust, pendingRequestRef, playCompletionSound,
    pluginBypass, pluginBypassRef, pluginContentRef, pluginLoading,
    pluginManagerRef, pluginQuery, pluginQueryRef, pluginVersion,
    pluginsLoadedByRootRef, pluginsLoadingByRootRef, pluginsTrustPendingByRootRef, projectTreeTab,
    projectTreeTabRequest, pruneTaskDetails, queueFrozenBySessionRef, queueVersion,
    queuedMessagesBySessionRef, refreshGitHistory, refreshGitStatus, refreshProjectTreeWorktrees,
    refreshTaskWorktree, refreshWorkspaceBoard, relatedSelectedFileKey, removeTaskInlineAttachment,
    replaceURLState, resetLocksForRootTransition, resetSessionLockForRoot, rootEntries,
    rootEntriesRef, saveTaskInlineEdit, scheduledAgentDialogOpen, scopedRootKey,
    selectRootNode, selectedDir, selectedDirKey, selectedDirRef,
    selectedKanbanTaskId, selectedSession, selectedSessionByRootRef, selectedSessionLoading,
    selectedSessionRef, sendShortcut, sessionCacheAccessRef, sessionCacheRef,
    sessionListAbortRef, sessionListLoadSeqRef, sessionListMode, sessionListReloadTimerRef,
    sessionLoadFailureRef, sessionSearchTargetCounterRef, sessions, sessionsRef, setActiveBoundSessionKey,
    setAgentsVersion, setArchiveLoading, setArchiveOpen, setArchiveReloadToken,
    setArchivedGroups, setAttachedFileContext, setAvailableAgents, setBootstrapState,
    setBoundSessionForRoot, setCacheVersion, setCodexRateLimitsRefreshToken, setCollapsedKanbanColumns,
    setCollapsedTaskCompletionGroups, setCopiedSlashCommandKeys, setCurrentRootId, setCurrentSession,
    setDirectorySortOverrides, setDirectoryUploadProgress, setDrawerOpenForRoot, setDrawerSessionForRoot,
    setEditDraftRequest, setEntriesByPath, setExpanded, setExpandedTaskInputIds,
    setFile, setFontSizePreferences, setGitDiff, setGitDiffSideBySide,
    setGitHistory, setGitHistoryExpandedByRoot, setGitStatus, setGitStatusByRoot,
    setGitStatusExpandedByRoot, setHasMoreSessions, setInteractionMode, setIsDrawerOpen,
    setIsLeftOpen, setIsRightOpen, setLoadingOlderSessions, setMainDirectoryError,
    setMainEntries, setMainView, setManagedRootIds, setMobileEnterKeySends,
    setMultiProjectPendingByKey, setMultiProjectAwaitingByKey, setMultiProjectSessionGroups, setMultiProjectSessionsLoading, setOnboardingMainContentViewRoot,
    setOnboardingOpen, setPendingPlanMode, setPendingPluginTrust, setPluginBypass,
    setPluginLoading, setPluginQuery, setPluginVersion, setProjectTreeTab,
    setProjectTreeTabRequest, setQueueVersion, setRelatedSelectedFileKey, setRootEntries,
    setScheduledAgentDialogOpen, setSelectedDir, setSelectedDirKey, setSelectedKanbanTaskId,
    setSelectedSession, setSelectedSessionLoading, setSendShortcut, setSessionListMode,
    setSessions, setSidebarsSwapped, setSlashCommandResults, setStatus,
    setSyncingSessionKeys, setTaskCreateTemplateMenuOpen, setTaskInlineActiveToken, setTaskInlineCandidateIndex,
    setTaskInlineCandidates, setTaskInlineEdit, setTaskInlineHelpKey, setTaskRelatedFilesById,
    setTaskSessionErrorDialog, setTaskTemplateActionMenuOpen, setTaskTemplateDialogOpen, setTaskTemplateFilter,
    setTreeSortMode, setUpdateState, setUpdateSubmitting, setViewerSelection,
    showHiddenFiles, showSessionPane, sidebarsSwapped, slashCommandResults,
    slashCopyResetTimersRef, staleSessionKeysRef, status, suppressedAutoBindSessionByRootRef,
    switchMainView, switchTaskInlineEditProject, syncingSessionKeys, t,
    taskCreateProjectOptions, taskCreateTemplateMenuOpen, taskCreateTemplateMenuRef, taskDetailsById,
    taskDetailsByIdRef, taskFirstInputById, taskInlineActiveToken, taskInlineAttachmentInputRef,
    taskInlineCandidateIndex, taskInlineCandidateItemRefs, taskInlineCandidates, taskInlineEdit,
    taskInlineEditorRef, taskInlineHelpKey, taskInlineSaving, taskInlineUploadAbortRef,
    taskInlineUploadProgress, taskRelatedFilesById, taskSessionErrorDialog, taskSessionKeysById,
    taskSessionKeysByIdRef, taskTemplateActionMenuOpen, taskTemplateActionMenuRef, taskTemplateDialogOpen,
    taskTemplateDialogTemplate, taskTemplateFilter, taskTemplates, taskWorktreeBranches,
    toggleProjectTreeWorktree, treeFetchSeqRef, treeSortMode, updateState,
    updateSubmitting, viewerSelection, viewerSelectionRef, workspaceBoardRefreshRef,
    workspaceOpen, workspaceOpenRef, worktreeErrorByRoot, worktreeItemsByRoot,
    worktreeLoadingByRoot, worktreeStatusByPath, worktreeStatusLoadingByPath
  };
}
