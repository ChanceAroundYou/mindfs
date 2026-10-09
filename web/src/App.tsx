import React, {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import { FileEditStore, fileEditKey } from "./services/fileEditing";
import { normalizePathForRoot, shouldRedirectToRelayNodes } from "./services/fileNavigation";
import { getViewModeSystemPrompt } from "./renderer/viewCatalog";
import { Renderer } from "./renderer/Renderer";
import { SESSION_WINDOW_SIZE, clearCachedSessionsForRoot, clearWindowedView, composeLoadedExchanges, deleteCachedSession, deleteCachedSessionLists, getCachedMultiRootSessionList, getCachedSession, getCachedSessionList, getSessionWindow, isTransientExchange, saveCachedMultiRootSessionList, saveCachedSessionList, sessionService, setCachedSessionRelatedFiles, setWindowedView, settlePendingAcks, syncSession, type MultiRootSessionGroup, type QueuedUserMessage, type RelatedFile, type Session } from "./services/session";
import { buildClientContext } from "./services/prefs/context";
import { e2eeService, type E2EEState } from "./services/net/e2ee";
import {
  bootstrapService,
  type BootstrapState,
} from "./services/net/bootstrap";
import { syncNativeReplyPollerE2EE } from "./services/net/replyPoller";
import {
  protectedAPIReady,
  protectedJSON as apiProtectedJSON,
} from "./services/net/api";
import { reportError } from "./services/net/error";
import {
  loadSendShortcut,
  persistSendShortcut,
  type SendShortcut,
} from "./services/prefs/sendShortcut";
import {
  loadFontSizePreferences,
  persistFontSizePreferences,
  type FontSizePreferences,
} from "./services/prefs/fontSize";
import {
  fetchFile,
  fetchEditableFile,
  saveTextFile,
  clearFileCacheForRoot,
  clearFileMemoryCacheForView,
  getCachedFile,
  type FilePayload,
} from "./services/file";
import { getRootNodeId, setRootNodeId, setRootNodeMap } from "./services/net/rootNode";
import { scopeKey, scopeSessionKey, treeKey, dirSelKey } from "./shared/scope";
import {
  buildGitDiffCacheSignature,
  clearGitHistoryCache,
  fetchGitDiff,
  fetchGitRelatedFileDiff,
  fetchGitBranches,
  removeGitWorktree,
  type GitBranchesPayload,
  type GitDiffPayload,
} from "./services/git";
import {
  relatedFileStatKey,
  useRelatedFileStats,
} from "./hooks/useRelatedFileStats";
import {
  DEFAULT_DIRECTORY_SORT_MODE,
  type DirectorySortMode,
  type FileEntry,
} from "./services/prefs/directorySort";
import { fileTokenPath, formatFileToken, type UploadProgress } from "./services/upload";
import { isPlanCommand, withPlanPrefix } from "./components/action/inputTransforms";
import {
  PluginManager,
  loadPluginsFromSources,
  scanPluginSources,
  snapshotFromPluginSources,
  type PluginSourceBundle,
  type PluginInput,
} from "./plugins/manager";
import {
  isPluginSnapshotTrusted,
  readTrustedPluginSet,
  saveTrustedPluginSet,
} from "./plugins/trust";
import { appPath, appURL } from "./services/net/base";
import { useRefreshSpin } from "./hooks";
import { copyText } from "./services/platform/clipboard";
import { triggerUpdate, type UpdateState } from "./services/update";
import {
  cancelScheduledWebViewCacheClear,
  scheduleWebViewCacheClearOnNextLaunch,
} from "./services/platform/nativeCacheControl";
import {
  applyPinnedSnapshotToSessions as applyPinSnapshotRaw,
  mergeSessionItems,
} from "./services/sessionListMerge";
import { currentUser } from "./services/net/authGate";
// 直接导入标准组件
import { AppShell } from "./layout/AppShell";
import { ViewHeader } from "./layout/ViewHeader";
import { ModeIcon } from "./components/agent/ModeIcon";
import { FileTree } from "./components/file/FileTree";
import type { AgentConfigSwitchRequest, ProjectTreeTab } from "./components/file/fileTreeTypes";

import { applyNodesFromServer, getActiveNode, getActiveNodeId, getNodeById, getNodes, migrateLegacySingleBase, setActiveNodeId, syncNodesFromServer } from "./services/net/nodeRegistry";
import { refreshPinsFromServer, writePins } from "./services/pins";

import { FileViewer } from "./components/file/FileViewer";
import { resolveGroupColor } from "./services/sessionGroupDisplay";
import { GitDiffViewer } from "./components/git/GitDiffViewer";
import { GitStatusPanel } from "./components/git/GitStatusPanel";
import { RootGitContentView } from "./components/root/RootGitContentView";
import { RootRelatedContentView } from "./components/root/RootRelatedContentView";
import { RootWorktreeContentView } from "./components/root/RootWorktreeContentView";
import { SessionViewer } from "./components/session/SessionViewer";
import { TaskBoardView } from "./components/task/TaskBoardView";
import { DefaultListView, type MainContentViewMode } from "./components/file/DefaultListView";
import { type ProjectSessionGroup } from "./components/session/SessionList";
import { InlineTokenText } from "./components/common/InlineTokenText";
import { ActionBar } from "./components/action/ActionBar";
import { CompactUploadProgress } from "./components/file/CompactUploadProgress";
import { ToastContainer } from "./components/shell/Toast";
import { DialogHost } from "./components/shell/DialogHost";
import { confirmDialog } from "./services/dialog";
import { BottomSheet } from "./components/shell/BottomSheet";
import { ScheduledAgentTaskDialog } from "./components/task/ScheduledAgentTaskDialog";
import { TaskTemplateDialog, FieldLabelWithInfo } from "./components/task/TaskTemplateDialog";
import { TaskDetailPanel } from "./components/task/TaskDetailPanel";
import { MainViewSwitcher } from "./components/shell/MainViewSwitcher";
import { OnboardingTour } from "./components/shell/OnboardingTour";
import { WorktreeBranchSelector } from "./components/task/WorktreeBranchSelector";
import { NoWorktreeIcon } from "./components/common/NoWorktreeIcon";
import { renderToolIcon } from "./components/stream/ToolCallCard";
import TokenEditor, { type TokenEditorHandle } from "./components/editor/TokenEditor";
import { PromptEditor } from "./components/editor/PromptEditor";
import { StageEditor } from "./components/task/StageEditor";
import { Select } from "./components/common/Select";
import { PanelShell } from "./components/shell/PanelShell";
import { composerInputStyle } from "./components/action/composerStyles";
import {
  type GitHubImportState,
  type LocalDirBrowserState,
  ProjectAddPopover,
  type ProjectAddMode,
} from "./components/file/ProjectAddPopover";
import { fetchAgents, restartAgent, type AgentStatus } from "./services/agents";
import { fetchCandidates, type CandidateItem } from "./services/candidates";
import {
  beginTaskFinishWorktree,
  createTask,
  deleteTask,
  deleteTaskTemplate,
  // FinishWorktreeConflict 是 class 不是 type：下面要用 instanceof 分流
  // （冲突要列文件清单，其它失败只报一句）。
  FinishWorktreeConflict,
  fetchTaskDetails,
  fetchTaskTemplates,
  getCachedTaskDetails,
  getCachedTaskMeta,
  moveTask,
  pruneCachedTaskDetails,
  rebuildTaskWorktree,
  saveTaskTemplate,
  upsertCachedTaskDetails,
  type KanbanTask,
  type StageRun,
  type TaskDetail,
  type TaskOverviewItem,
  type StageTemplate,
  type TaskTemplate,
} from "./services/tasks";
import { shouldApplyTaskDetail } from "./services/taskDetailOrder";
import { mergeRelatedFileGroups, taskIdsForUpdatedSession } from "./services/taskRelatedFiles";
import {

  resolveLockedSessionKey,
  shouldResetSessionLockForRootChange,
} from "./services/sessionLock";
import { useI18n, type MessageKey, type MessageParams } from "./i18n";
import {
  completeOnboarding,
  dismissOnboarding,
  shouldAutoStartOnboarding,
} from "./services/prefs/onboarding";

// 类型定义

import { APP_DOCUMENT_TITLE, AppProps, CHILD_SESSION_PAGE_SIZE, MULTI_PROJECT_SESSION_LIMIT, ManagedRootPayload, SESSION_PAGE_SIZE } from "./app/appMisc";
import { useAppPluginRenderEffects } from "./app/useAppPluginRenderEffects";
import { useAppBootstrapEffects } from "./app/useAppBootstrapEffects";
import { useAppRefSync } from "./app/useAppRefSync";
import { useAppKanbanTaskPanel } from "./app/useAppKanbanTaskPanel";
import { useAppRootContentRenderers } from "./app/useAppRootContentRenderers";
import { useAppSessionView } from "./app/useAppSessionView";
import { useAppKanbanStageColumns } from "./app/useAppKanbanStageColumns";
import { useAppSlashCommandResult } from "./app/useAppSlashCommandResult";
import { useAppMainViewActions } from "./app/useAppMainViewActions";
import { useAppBoundSessionDir } from "./app/useAppBoundSessionDir";
import { useAppRootResolution } from "./app/useAppRootResolution";
import { useAppWorkspaceActions } from "./app/useAppWorkspaceActions";
import { useAppSessionSnapshots } from "./app/useAppSessionSnapshots";
import { useAppFileNavigation } from "./app/useAppFileNavigation";
import { useAppProjectDisplay } from "./app/useAppProjectDisplay";
import { useAppPluginTrust } from "./app/useAppPluginTrust";
import { useAppPluginRender } from "./app/useAppPluginRender";
import { useAppSessionSelection } from "./app/useAppSessionSelection";
import { useAppRootBindingSetters } from "./app/useAppRootBindingSetters";
import { useAppSessionKeyState } from "./app/useAppSessionKeyState";
import { useAppSessionReload } from "./app/useAppSessionReload";
import { useAppBootstrapActions } from "./app/useAppBootstrapActions";
import { useAppSessionPins } from "./app/useAppSessionPins";
import { useAppReplaceURLState } from "./app/useAppReplaceURLState";
import { MainViewMode } from "./app/appPath";
import { useAppFormatDirectoryLoadError } from "./app/useAppFormatDirectoryLoadError";
import { closeTopBackLayer, consumeViewHistoryEntry, hasBackLayer, installBackNavigation, pushViewHistoryEntry, useBackLayer } from "./app/useBackNavigation";
import { AttachedFileContext, Exchange, GitFileStat, MultiProjectSessionGroup, PendingSend, SessionItem, SessionMode, SessionQueueItem, SlashCommandResult, ViewerSelection, WSStatus } from "./app/appSession";
import { CANDIDATE_FETCH_DEBOUNCE_MS, DIRECTORY_SORT_OVERRIDES_STORAGE_KEY, GIT_DIFF_SIDE_BY_SIDE_STORAGE_KEY, GIT_HISTORY_EXPANDED_STORAGE_KEY, GIT_STATUS_EXPANDED_STORAGE_KEY, LAST_ROOT_NODE_STORAGE_KEY, LAST_ROOT_STORAGE_KEY, loadWorkspaceCollapsed, loadWorkspaceFilter, MOBILE_ENTER_KEY_SEND_STORAGE_KEY, PLUGIN_QUERY_STORAGE_PREFIX, saveWorkspaceCollapsed, saveWorkspaceFilter, SIDEBARS_SWAPPED_STORAGE_KEY, TASK_TEMPLATE_ALL_FILTER, TASK_TEMPLATE_SELECTION_STORAGE_KEY, TREE_SORT_STORAGE_KEY, type WorkspaceBoardFilter } from "./app/appStorage";
import { TaskInlineEditState } from "./app/appTask";
import { buildMatchInputFromPath, buildMessageWithViewContext, hasExplicitFileContext, indexManagedRoots, inferReadModeFromPlugin, managedDirAddErrorMessage, mapManagedRootsToEntries, normalizeUpdateState, shouldShowUpdateButton, toPluginInput, updateButtonLabel, updateSummaryText, useResponsive, waitForNextPaint } from "./app/appMisc";
import { basenameOfPath, buildDirectorySelectionKey, buildFileScrollKey, comparableManagedRootPath, isDirectorySortMode, joinDisplayPath, normalizeCursor, normalizePath, parentDirsOfFile, parseFileLocation, parsePluginQuery, readURLState, relativeDisplayPathFromRoot, rootNodeKey } from "./app/appPath";
import { useWorkspaceBoard } from "./app/useWorkspaceBoard";
import { useSessionCache } from "./app/useSessionCache";
import { useSendMessage } from "./app/useSendMessage";
import { useActionHandlers, type ActionHandlers } from "./app/useActionHandlers";
import { useSessionLifecycle } from "./app/useSessionLifecycle";
import { getDirectorySortKey, normalizeComparableRootPath, normalizeTreeResponse, unscopedRootId } from "./app/appUtils";
import { hasSessionExchanges, isSessionShownInMain, isTopLevelSessionItem, normalizeMode, relatedFileSelectionKey, sessionInputHistory, shouldAutoSelectNewSession, toSessionItem } from "./app/appSession";
import { buildSessionJumpTarget, resolveSessionJumpRoot } from "./app/sessionJump";
import { accountScopedKey, loadGitDiffSideBySide, loadLastRootId, loadLastRootNodeId, loadMobileEnterKeySends, loadPersistedFileScrollPositions, loadPersistedPluginQuery, loadSidebarsSwapped, loadTaskCreateWorktreePreference, persistFileScrollPositions, persistPluginQuery, removeLocalStorageByPrefix, saveTaskCreateWorktreePreference } from "./app/appStorage";
import { applyStageOverride, currentTaskInputFromDetail, DEFAULT_TASK_AGENT, DEFAULT_TASK_MODEL, firstAgentStage, firstTaskInputFromDetail, firstUserInputTemplate, isTerminalKanbanTask, latestTaskStageRun, normalizeFastService, parseTaskSessionErrorDetails, parseTaskSessionErrorMessage, previousTaskInputsFromDetail, taskSessionKeysFromDetail, taskStagesForCreate, taskStatusLabel } from "./app/appTask";
import { useCompletionSound } from "./app/useCompletionSound";
import { useExternalSessionImport } from "./app/useExternalSessionImport";
import { useGitActions } from "./app/useGitActions";
import { useSessionSearch } from "./app/useSessionSearch";
import { useGitData } from "./app/useGitData";
import { useProjectTreeWorktrees } from "./app/useProjectTreeWorktrees";
import { useAppAgentActions } from "./app/useAppAgentActions";
import { useAppTaskDrawer } from "./app/useAppTaskDrawer";
import { useAppProjectRemoval } from "./app/useAppProjectRemoval";
import { useProjectLifecycle } from "./app/useProjectLifecycle";
import { useSessionSidebarView } from "./app/useSessionSidebarView";
import { useAppViewComposition } from "./app/useAppViewComposition";
import { useAppDomainLayer } from "./app/useAppDomainLayer";
import { useAppSessionLayer } from "./app/useAppSessionLayer";
import { useAppStateLayer } from "./app/useAppStateLayer";
import { useSessionStreamCache } from "./app/useSessionStreamCache";
import { useRealtimeEvents } from "./app/useRealtimeEvents";
import { useTaskTemplates } from "./app/useTaskTemplates";
import { useTaskWorkspaceState } from "./app/useTaskWorkspaceState";
import { useTaskWorkspaceEffects } from "./app/useTaskWorkspaceEffects";
import { useRootContentView } from "./app/useRootContentView";
import { useProjectTreeActions } from "./app/useProjectTreeActions";
import { useUiPreferences } from "./app/useUiPreferences";
import { CheckIconSmall, PlusSmallIcon, taskCardIconButtonStyle } from "./app/taskIcons";
import { useMultiProjectSessions } from "./app/useMultiProjectSessions";
import { useSessionArchive } from "./app/useSessionArchive";
import { useManagedRoots } from "./app/useManagedRoots";
import { usePluginHandlers } from "./app/usePluginHandlers";
import { useFileRefresh } from "./app/useFileRefresh";
import { useRelatedFileDiff } from "./app/useRelatedFileDiff";
import { TaskSessionErrorDialog } from "./components/app/TaskSessionErrorDialog";
import { TaskDetailPanelGate } from "./components/app/TaskDetailPanelGate";
import { E2eePrompt } from "./components/app/E2eePrompt";
import { OnboardingGate } from "./components/app/OnboardingGate";
import { TaskInlineEditPanel } from "./components/app/TaskInlineEditPanel";
import { SessionSheet } from "./components/app/SessionSheet";
import { AppActionBar } from "./components/app/AppActionBar";
import { SessionPane } from "./components/app/SessionPane";
import { FileTreePanel } from "./components/app/FileTreePanel";
import { renderRootSlashCommandResult } from "./app/renderRootSlashCommandResult";
import { usePopStateSync } from "./app/usePopStateSync";
import { useArchiveSync } from "./app/useArchiveSync";
import { useSessionRestore } from "./app/useSessionRestore";
import { useTaskInlineCandidates } from "./app/useTaskInlineCandidates";
import { usePluginThemeVars } from "./app/usePluginThemeVars";
import { useAppInit } from "./app/useAppInit";
import { useTaskBoard } from "./app/useTaskBoard";
import { useTaskComposer } from "./app/useTaskComposer";
import { useAttachedFileContext } from "./app/useAttachedFileContext";
import { useReplyingSessionPoll } from "./app/useReplyingSessionPoll";
import { useSessionPending } from "./app/useSessionPending";
import { useSessionMetadata } from "./app/useSessionMetadata";
import { useSessionListActions } from "./app/useSessionListActions";
import { useSessionRelatedFiles } from "./app/useSessionRelatedFiles";
import { useSessionQueueActions } from "./app/useSessionQueueActions";


export function App({ onGoHome }: AppProps) {
  const { MULTI_PROJECT_RELOAD_DEBOUNCE_MS,
    activeBoundSessionKey,
    agentConfigSwitchRequest,
    agentsVersion,
    applyPinSnapshot,
    applyTaskDetails,
    applyTaskInlineCandidate,
    archiveLoading,
    archiveOpen,
    archiveReloadToken,
    archivedGroups,
    attachedFileContext,
    availableAgents,
    bootstrapState,
    boundSessionByRootRef,
    cacheVersion,
    cancelRequestedBySessionRef,
    closeTaskEditDialog,
    codexRateLimitsRefreshToken,
    collapsedKanbanColumns,
    collapsedTaskCompletionGroups,
    copiedSlashCommandKeys,
    currentDirectorySortMode,
    currentDirectorySortOverride,
    currentFileEditing,
    currentMainContentView,
    currentRootDisplayName,
    currentRootId,
    currentRootIdRef,
    currentRootNodeId,
    currentRootNodeIdRef,
    currentSession,
    currentSessionRef,
    didInitRef,
    directoryUploadAbortRef,
    directoryUploadProgress,
    dismissedSelectionFileRef,
    drawerLoadingSessionByRoot,
    drawerOpenByRootRef,
    drawerScrollRef,
    drawerSessionByRootRef,
    e2eePromptBusy,
    e2eePromptError,
    e2eeSecretInput,
    e2eeState,
    editDraftRequest,
    enforceSessionCacheBound,
    entriesByPath,
    entriesByPathRef,
    expandProjectTreeWorktree,
    expanded,
    expandedRef,
    expandedTaskInputIds,
    expandedWorktreeByRoot,
    file,
    fileCursorRef,
    fileEditStore,
    fileOpenRequestRef,
    fileRef,
    fileScrollPositionsRef,
    fontSizePreferences,
    fullUpgradeAttemptRef,
    getDisplayNodeColor,
    getNodeIdForRoot,
    getRootDisplayName,
    gitDiff,
    gitDiffSideBySide,
    gitHistory,
    gitHistoryByRoot,
    gitHistoryExpandedByRoot,
    gitHistoryLoading,
    gitHistoryLoadingByRoot,
    gitHistoryLoadingMore,
    gitStatus,
    gitStatusByRoot,
    gitStatusExpandedByRoot,
    gitStatusLoading,
    gitStatusLoadingByRoot,
    handleDeleteTaskTemplate,
    handleMoveKanbanTask,
    handleSelectSessionRef,
    handleTaskInlineAttachmentChange,
    handleTaskTemplateSaved,
    hasMoreSessions,
    interactionMode,
    interactionModeRef,
    invalidTreeCacheKeysRef,
    isDrawerOpen,
    isLeftOpen,
    isMobile,
    isRightOpen,
    kanbanTasks,
    kanbanTasksLoading,
    lastMainSessionSnapshotRef,
    lastNonChatViewRef,
    lastPluginChapterRef,
    lastViewerSelectionRef,
    listLoadErrorAtRef,
    loadKanbanTasks,
    loadMoreGitHistory,
    loadTaskTemplates,
    loadedSessionRef,
    loadingOlderSessions,
    loadingSessionRef,
    mainDirectoryError,
    mainDirectoryErrorRef,
    mainEntries,
    mainEntriesRef,
    mainView,
    mainViewRef,
    managedRootByIdRef,
    managedRootByKeyRef,
    managedRootIds,
    managedRootIdsRef,
    managedRootsRequestRef,
    mobileEnterKeySends,
    multiProjectLoadSeqRef,
    multiProjectPendingByKey,
    multiProjectPendingRef,
    multiProjectSessionGroups,
    multiProjectSessionGroupsRef,
    multiProjectSessionsEnabled,
    multiProjectSessionsLoading,
    nodeLoadFailuresRef,
    notifiedNodeFailuresRef,
    notifyNodeLoadFailedRef,
    onboardingOpen,
    openTaskCreateDialog,
    openTaskTemplateEditor,
    optimisticDequeuedIdsRef,
    pendingBySessionRef,
    pendingDraftRef,
    pendingPlanMode,
    pendingPluginTrust,
    pendingRequestRef,
    playCompletionSound,
    pluginBypass,
    pluginBypassRef,
    pluginContentRef,
    pluginLoading,
    pluginManagerRef,
    pluginQuery,
    pluginQueryRef,
    pluginVersion,
    pluginsLoadedByRootRef,
    pluginsLoadingByRootRef,
    pluginsTrustPendingByRootRef,
    projectTreeTab,
    projectTreeTabRequest,
    pruneTaskDetails,
    queueFrozenBySessionRef,
    queueVersion,
    queuedMessagesBySessionRef,
    refreshGitHistory,
    refreshGitStatus,
    refreshProjectTreeWorktrees,
    refreshTaskWorktree,
    refreshWorkspaceBoard,
    relatedSelectedFileKey,
    removeTaskInlineAttachment,
    replaceURLState,
    resetLocksForRootTransition,
    resetSessionLockForRoot,
    rootEntries,
    rootEntriesRef,
    saveTaskInlineEdit,
    scheduledAgentDialogOpen,
    scopedRootKey,
    selectRootNode,
    selectedDir,
    selectedDirKey,
    selectedDirRef,
    selectedKanbanTaskId,
    selectedSession,
    selectedSessionByRootRef,
    selectedSessionLoading,
    selectedSessionRef,
    sendShortcut,
    sessionCacheAccessRef,
    sessionCacheRef,
    sessionListAbortRef,
    sessionListLoadSeqRef,
    sessionListMode,
    sessionListReloadTimerRef,
    sessionLoadFailureRef,
    sessionSearchTargetCounterRef,
    sessions,
    sessionsRef,
    setActiveBoundSessionKey,
    setAgentsVersion,
    setArchiveLoading,
    setArchiveOpen,
    setArchiveReloadToken,
    setArchivedGroups,
    setAttachedFileContext,
    setAvailableAgents,
    setBootstrapState,
    setBoundSessionForRoot,
    setCacheVersion,
    setCodexRateLimitsRefreshToken,
    setCollapsedKanbanColumns,
    setCollapsedTaskCompletionGroups,
    setCopiedSlashCommandKeys,
    setCurrentRootId,
    setCurrentSession,
    setDirectorySortOverrides,
    setDirectoryUploadProgress,
    setDrawerOpenForRoot,
    setDrawerSessionForRoot,
    setE2eePromptBusy,
    setE2eePromptError,
    setE2eeSecretInput,
    setE2eeState,
    setEditDraftRequest,
    setEntriesByPath,
    setExpanded,
    setExpandedTaskInputIds,
    setFile,
    setFontSizePreferences,
    setGitDiff,
    setGitDiffSideBySide,
    setGitHistory,
    setGitHistoryExpandedByRoot,
    setGitStatus,
    setGitStatusByRoot,
    setGitStatusExpandedByRoot,
    setHasMoreSessions,
    setInteractionMode,
    setIsDrawerOpen,
    setIsLeftOpen,
    setIsRightOpen,
    setLoadingOlderSessions,
    setMainDirectoryError,
    setMainEntries,
    setMainView,
    setManagedRootIds,
    setMobileEnterKeySends,
    setMultiProjectPendingByKey,
    setMultiProjectSessionGroups,
    setMultiProjectSessionsLoading,
    setOnboardingMainContentViewRoot,
    setOnboardingOpen,
    setPendingPlanMode,
    setPendingPluginTrust,
    setPluginBypass,
    setPluginLoading,
    setPluginQuery,
    setPluginVersion,
    setProjectTreeTab,
    setProjectTreeTabRequest,
    setQueueVersion,
    setRelatedSelectedFileKey,
    setRootEntries,
    setScheduledAgentDialogOpen,
    setSelectedDir,
    setSelectedDirKey,
    setSelectedKanbanTaskId,
    setSelectedSession,
    setSelectedSessionLoading,
    setSendShortcut,
    setSessionListMode,
    setSessions,
    setSidebarsSwapped,
    setSlashCommandResults,
    setStatus,
    setSyncingSessionKeys,
    setTaskCreateTemplateMenuOpen,
    setTaskInlineActiveToken,
    setTaskInlineCandidateIndex,
    setTaskInlineCandidates,
    setTaskInlineEdit,
    setTaskInlineHelpKey,
    setTaskRelatedFilesById,
    setTaskSessionErrorDialog,
    setTaskTemplateActionMenuOpen,
    setTaskTemplateDialogOpen,
    setTaskTemplateFilter,
    setTreeSortMode,
    setUpdateState,
    setUpdateSubmitting,
    setViewerSelection,
    showHiddenFiles,
    showSessionPane,
    sidebarsSwapped,
    slashCommandResults,
    slashCopyResetTimersRef,
    staleSessionKeysRef,
    status,
    suppressedAutoBindSessionByRootRef,
    switchMainView,
    switchTaskInlineEditProject,
    syncingSessionKeys,
    t,
    taskCreateProjectOptions,
    taskCreateTemplateMenuOpen,
    taskCreateTemplateMenuRef,
    taskDetailsById,
    taskDetailsByIdRef,
    taskFirstInputById,
    taskInlineActiveToken,
    taskInlineAttachmentInputRef,
    taskInlineCandidateIndex,
    taskInlineCandidateItemRefs,
    taskInlineCandidates,
    taskInlineEdit,
    taskInlineEditorRef,
    taskInlineHelpKey,
    taskInlineSaving,
    taskInlineUploadAbortRef,
    taskInlineUploadProgress,
    taskRelatedFilesById,
    taskSessionErrorDialog,
    taskSessionKeysById,
    taskSessionKeysByIdRef,
    taskTemplateActionMenuOpen,
    taskTemplateActionMenuRef,
    taskTemplateDialogOpen,
    taskTemplateDialogTemplate,
    taskTemplateFilter,
    taskTemplates,
    taskWorktreeBranches,
    toggleProjectTreeWorktree,
    treeFetchSeqRef,
    treeSortMode,
    updateState,
    updateSubmitting,
    viewerSelection,
    viewerSelectionRef,
    workspaceBoardRefreshRef,
    workspaceOpen,
    workspaceOpenRef,
    worktreeErrorByRoot,
    worktreeItemsByRoot,
    worktreeLoadingByRoot,
    worktreeStatusByPath,
    worktreeStatusLoadingByPath,
  } = useAppStateLayer({
  });
  const { appendAgentChunkForSession,
    appendCompactNoticeForSession,
    appendPlanUpdateForSession,
    appendThoughtChunkForSession,
    appendTodoUpdateForSession,
    appendToolCallForSession,
    attachContextWindowToLatestAssistant,
    bumpCacheVersion,
    clearSessionStale,
    closeSessionSearch,
    confirmingExternalImport,
    currentSelectionSource,
    enterImportMode,
    executeSessionSearch,
    exitImportMode,
    externalFilterBound,
    externalImportAgent,
    externalSelectedKey,
    externalSessions,
    externalSessionsError,
    formatDirectoryLoadError,
    getSessionSnapshot,
    handleArchiveSession,
    handleCancelCurrentTurn,
    handleConfirmExternalImport,
    handleCreateBlankFile,
    handleDeleteSession,
    handleForkAgentMessage,
    handleGitCommit,
    handleGitDiscardItem,
    handleGitPull,
    handleGitPush,
    handleGitStageItem,
    handleGitUnstageItem,
    handleImportedSessionConfirmed,
    handleLoadOlderExternalSessions,
    handleLoadOlderSessions,
    handleNewSession,
    handleOnboardingStepChange,
    handlePinSession,
    handleRenameSession,
    handleRestartAgent,
    handleRunAgentLifecycleCommand,
    handleSearchQueryChange,
    handleSelectSession,
    handleSelectSessionAndClose,
    handleSendMessage,
    handleSetPlanMode,
    handleStartUpdate,
    handleSyncSession,
    handleTreeUpload,
    hasMoreExternalSessions,
    importMenuOpen,
    importMenuRef,
    importingExternalSessionKeys,
    isSessionStale,
    loadChildSessionsForParent,
    loadMoreMultiProjectSessions,
    loadMultiProjectSessionGroups,
    loadSessionsForRoot,
    loadingExternalSessions,
    loadingOlderExternalSessions,
    markSessionStale,
    openGitCommitDiff,
    openGitDiff,
    openSessionSearch,
    promotePendingSessionForRoot,
    refreshCurrentFileContent,
    refreshMultiProjectReplyingSessions,
    refreshTreeDir,
    rememberCurrentFileScroll,
    resolvePendingForSession,
    resolveRootForSessionKey,
    restoreActiveSession,
    rootSessionKey,
    scheduleMultiProjectSessionReload,
    scheduleSessionListReload,
    selectedExternalImportKeys,
    sessionSearchLoading,
    sessionSearchOpen,
    sessionSearchQuery,
    sessionSearchResults,
    sessionSearchResultsMode,
    setExternalFilterBound,
    setExternalImportAgent,
    setExternalSelectedKey,
    setExternalSessions,
    setImportMenuOpen,
    setMultiProjectSessionPending,
    setSelectedPendingByKey,
    submitE2EESecret,
    switchGitBranch,
    toggleAllExternalImportSelection,
    toggleExternalImportSelection,
    toggleSessionSearch,
    treeCacheKey,
    tryShowBoundSessionForRoot,
    updateSessionAgentForKey,
    updateSessionRelatedFilesForKey,
    updateSessionRelatedWorktreeForKey,
  } = useAppSessionLayer({
    MULTI_PROJECT_RELOAD_DEBOUNCE_MS, applyPinSnapshot, archiveOpen, archiveReloadToken,
    attachedFileContext, boundSessionByRootRef, cacheVersion, cancelRequestedBySessionRef,
    currentRootId, currentRootIdRef, currentRootNodeId, currentRootNodeIdRef,
    currentSessionRef, didInitRef, directoryUploadAbortRef, drawerOpenByRootRef,
    drawerSessionByRootRef, e2eeSecretInput, enforceSessionCacheBound, file,
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
    setDirectoryUploadProgress, setDrawerOpenForRoot, setDrawerSessionForRoot, setE2eePromptBusy,
    setE2eePromptError, setE2eeSecretInput, setEntriesByPath, setExpanded,
    setFile, setGitDiff, setGitStatus, setGitStatusByRoot,
    setHasMoreSessions, setInteractionMode, setIsLeftOpen, setIsRightOpen,
    setLoadingOlderSessions, setMainDirectoryError, setMainEntries, setMultiProjectPendingByKey,
    setMultiProjectSessionGroups, setMultiProjectSessionsLoading, setOnboardingMainContentViewRoot, setPendingPlanMode,
    setPluginQuery, setRelatedSelectedFileKey, setSelectedDir, setSelectedDirKey,
    setSelectedKanbanTaskId, setSelectedSession, setSelectedSessionLoading, setSessionListMode,
    setSessions, setSlashCommandResults, setSyncingSessionKeys, setUpdateState,
    setUpdateSubmitting, slashCommandResults, staleSessionKeysRef, status,
    suppressedAutoBindSessionByRootRef, switchMainView, t, treeFetchSeqRef,
    updateState,
  });

  const { actionBarInputHistory,
    actionBarQueuedMessages,
    actionBarSession,
    actionHandlers,
    actionHandlersRef,
    boardSessionByKey,
    canOpenSessionDrawer,
    creatingRootBusy,
    creatingRootKind,
    creatingRootName,
    detachedBoundSession,
    findManagedRootByPath,
    gitFileStatsByPath,
    handleClearFileContext,
    handleCreateRootCancel,
    handleCreateRootStart,
    handleCreateRootSubmit,
    handleDirectoryPathClick,
    handleDisablePendingPlugins,
    handleFileViewerFileClick,
    handleFileViewerPathClick,
    handleGitDiffPathClick,
    handleOpenProjectAdd,
    handleOpenWorktreeLocation,
    handleRemoveCurrentRoot,
    handleRemoveCurrentWorktree,
    handleRemoveQueuedMessage,
    handleRenameCurrentRoot,
    handleRequestFileContext,
    handleSelectKanbanTask,
    handleSendQueuedMessageNow,
    handleSessionChipClick,
    handleSwitchWorktreeStart,
    handleTaskSessionDrawerOpen,
    handleTrustPendingPlugins,
    handleUpdateQueuedMessage,
    handleViewerSelectionChange,
    matchedPlugin,
    openRelatedFileDiff,
    pluginHandlers,
    pluginRender,
    pluginRendererKey,
    pluginThemeVars,
    projectAddMode,
    projectAddOverlay,
    refreshManagedRoots,
    refreshTaskRelatedFiles,
    selectedKanbanTask,
    selectedKanbanTaskSessionSnapshot,
    selectedSessionSnapshot,
    setCreatingRootName,
    visibleMainEntries,
    worktreeCreateOverlay,
    worktreeSwitchOpen,
    worktreeSwitchOverlay,
  } = useAppDomainLayer({
    activeBoundSessionKey, appendAgentChunkForSession, appendCompactNoticeForSession, appendPlanUpdateForSession,
    appendThoughtChunkForSession, appendTodoUpdateForSession, appendToolCallForSession, applyTaskDetails,
    attachContextWindowToLatestAssistant, bootstrapState, boundSessionByRootRef, bumpCacheVersion,
    cancelRequestedBySessionRef, clearSessionStale, currentFileEditing, currentRootId,
    currentRootIdRef, currentRootNodeId, currentRootNodeIdRef, currentSelectionSource,
    currentSession, currentSessionRef, didInitRef, dismissedSelectionFileRef,
    drawerOpenByRootRef, drawerSessionByRootRef, e2eeState, entriesByPathRef,
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
    managedRootsRequestRef, markSessionStale, multiProjectPendingRef, multiProjectSessionGroups,
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
    setDrawerOpenForRoot, setDrawerSessionForRoot, setE2eePromptError, setE2eeSecretInput,
    setE2eeState, setEntriesByPath, setExpanded, setExternalSelectedKey,
    setExternalSessions, setFile, setGitDiff, setGitHistory,
    setGitHistoryExpandedByRoot, setGitStatus, setGitStatusExpandedByRoot, setInteractionMode,
    setIsDrawerOpen, setIsLeftOpen, setMainDirectoryError, setMainEntries,
    setMainView, setManagedRootIds, setMultiProjectPendingByKey, setMultiProjectSessionGroups,
    setMultiProjectSessionPending, setPendingPluginTrust, setPluginBypass, setPluginLoading,
    setPluginQuery, setPluginVersion, setQueueVersion, setRelatedSelectedFileKey,
    setRootEntries, setSelectedDir, setSelectedDirKey, setSelectedKanbanTaskId,
    setSelectedPendingByKey, setSelectedSession, setSelectedSessionLoading, setSessionListMode,
    setSessions, setSlashCommandResults, setStatus, setTaskRelatedFilesById,
    setTaskSessionErrorDialog, setUpdateState, setViewerSelection, showHiddenFiles,
    staleSessionKeysRef, suppressedAutoBindSessionByRootRef, switchMainView, t,
    taskDetailsById, taskDetailsByIdRef, taskSessionKeysById, taskSessionKeysByIdRef,
    treeCacheKey, tryShowBoundSessionForRoot, updateSessionAgentForKey, updateSessionRelatedFilesForKey,
    updateSessionRelatedWorktreeForKey, viewerSelection, viewerSelectionRef, workspaceOpenRef,
  });

  const { activePendingPluginTrust,
    currentFileScrollKey,
    currentRootSlashCommandResult,
    drawerSessionSnapshot,
    handleAskUserAnswer,
    handleDrawerSessionFileClick,
    handleEditUserMessage,
    handleMainViewSwitcherChange,
    handleNodeManagerRefresh,
    handleProjectTreeRefresh,
    handleRemoveSessionRelatedFile,
    kanbanTaskPanel,
    renderRootGitContent,
    renderRootRelatedContent,
    renderRootWorktreeContent,
    rootSessionIndicators,
    sessionSidebar,
    sessionView,
    sessionViewerComposerOverlayInset,
    showUpdateButton,
    slashCommandResultForSession,
    switchToPluginView,
    switchToRawFileView,
    updateBusy,
    updateHelp,
    updateLabel,
    updateSummary,
    workspaceBoard,
  } = useAppViewComposition({
    actionHandlers, actionHandlersRef, activeBoundSessionKey, applyTaskDetails,
    archiveLoading, archiveOpen, archivedGroups, availableAgents,
    boardSessionByKey, boundSessionByRootRef, cacheVersion, closeSessionSearch,
    collapsedKanbanColumns, collapsedTaskCompletionGroups, confirmingExternalImport, currentRootId,
    currentRootIdRef, currentRootNodeId, currentSession, drawerSessionByRootRef,
    enterImportMode, entriesByPathRef, executeSessionSearch, exitImportMode,
    expandProjectTreeWorktree, expandedTaskInputIds, expandedWorktreeByRoot, externalFilterBound,
    externalImportAgent, externalSelectedKey, externalSessions, externalSessionsError,
    file, fileCursorRef, fileRef, findManagedRootByPath,
    getDisplayNodeColor, getNodeIdForRoot, getRootDisplayName, getSessionSnapshot,
    gitDiff, gitFileStatsByPath, gitHistory, gitHistoryByRoot,
    gitHistoryExpandedByRoot, gitHistoryLoading, gitHistoryLoadingByRoot, gitHistoryLoadingMore,
    gitStatus, gitStatusByRoot, gitStatusExpandedByRoot, gitStatusLoading,
    gitStatusLoadingByRoot, handleArchiveSession, handleConfirmExternalImport, handleDeleteSession,
    handleDeleteTaskTemplate, handleForkAgentMessage, handleGitCommit, handleGitDiscardItem,
    handleGitPull, handleGitPush, handleGitStageItem, handleGitUnstageItem,
    handleLoadOlderExternalSessions, handleLoadOlderSessions, handleMoveKanbanTask, handlePinSession,
    handleRenameSession, handleSearchQueryChange, handleSelectKanbanTask, handleSelectSessionAndClose,
    handleSyncSession, handleTaskSessionDrawerOpen, hasMoreExternalSessions, hasMoreSessions,
    importMenuOpen, importMenuRef, importingExternalSessionKeys, interactionModeRef,
    kanbanTasks, kanbanTasksLoading, lastMainSessionSnapshotRef, loadChildSessionsForParent,
    loadKanbanTasks, loadMoreGitHistory, loadMoreMultiProjectSessions, loadingExternalSessions,
    loadingOlderExternalSessions, loadingOlderSessions, mainDirectoryErrorRef, mainEntriesRef,
    mainViewRef, managedRootByIdRef, managedRootByKeyRef, managedRootIds,
    multiProjectPendingByKey, multiProjectSessionGroups, multiProjectSessionsEnabled, multiProjectSessionsLoading,
    openGitCommitDiff, openGitDiff, openRelatedFileDiff, openSessionSearch,
    openTaskCreateDialog, openTaskTemplateEditor, pendingPluginTrust, pluginBypass,
    pluginBypassRef, pluginRender, pluginThemeVars, projectTreeTab,
    refreshCurrentFileContent, refreshGitHistory, refreshGitStatus, refreshManagedRoots,
    refreshProjectTreeWorktrees, refreshTaskRelatedFiles, refreshTreeDir, relatedSelectedFileKey,
    replaceURLState, rootSessionKey, scopedRootKey, selectedDirRef,
    selectedExternalImportKeys, selectedKanbanTask, selectedKanbanTaskId, selectedKanbanTaskSessionSnapshot,
    selectedSession, selectedSessionLoading, selectedSessionRef, selectedSessionSnapshot,
    sessionListMode, sessionSearchLoading, sessionSearchOpen, sessionSearchQuery,
    sessionSearchResults, sessionSearchResultsMode, sessions, setArchiveOpen,
    setCollapsedKanbanColumns, setCollapsedTaskCompletionGroups, setDrawerOpenForRoot, setEditDraftRequest,
    setExpanded, setExpandedTaskInputIds, setExternalFilterBound, setExternalImportAgent,
    setExternalSelectedKey, setGitHistoryExpandedByRoot, setGitStatusExpandedByRoot, setImportMenuOpen,
    setInteractionMode, setPluginBypass, setProjectTreeTabRequest, setSelectedKanbanTaskId,
    setTaskCreateTemplateMenuOpen, setTaskRelatedFilesById, setTaskSessionErrorDialog, setTaskTemplateActionMenuOpen,
    setTaskTemplateFilter, slashCommandResults, switchGitBranch, switchMainView,
    syncingSessionKeys, t, taskCreateTemplateMenuOpen, taskCreateTemplateMenuRef,
    taskDetailsById, taskFirstInputById, taskRelatedFilesById, taskSessionKeysById,
    taskSessionKeysByIdRef, taskTemplateActionMenuOpen, taskTemplateActionMenuRef, taskTemplateFilter,
    taskTemplates, toggleAllExternalImportSelection, toggleExternalImportSelection, toggleProjectTreeWorktree,
    toggleSessionSearch, treeSortMode, updateSessionRelatedFilesForKey, updateState,
    updateSubmitting, workspaceBoardRefreshRef, workspaceOpen, worktreeErrorByRoot,
    worktreeItemsByRoot, worktreeLoadingByRoot, worktreeStatusByPath, worktreeStatusLoadingByPath,
  });

  return (
    <>
      <AppShell
        leftOpen={isLeftOpen}
        rightOpen={isRightOpen}
        sidebarsSwapped={sidebarsSwapped}
        fileSidebarFontScale={fontSizePreferences.fileSidebar}
        mainFontScale={fontSizePreferences.main}
        sessionSidebarFontScale={fontSizePreferences.sessionSidebar}
        onCloseLeft={() => setIsLeftOpen(false)}
        onCloseRight={() => setIsRightOpen(false)}
        onOpenLeft={() => setIsLeftOpen(true)}
        onOpenRight={() => setIsRightOpen(true)}
sidebar={<
        FileTreePanel
        actionHandlers={actionHandlers}
        agentConfigSwitchRequest={agentConfigSwitchRequest}
        creatingRootBusy={creatingRootBusy}
        creatingRootKind={creatingRootKind}
        creatingRootName={creatingRootName}
        currentRootId={currentRootId}
        currentRootIdRef={currentRootIdRef}
        currentRootNodeId={currentRootNodeId}
        entriesByPath={entriesByPath}
        expanded={expanded}
        file={file}
        fontSizePreferences={fontSizePreferences}
        getDisplayNodeColor={getDisplayNodeColor}
        gitDiffSideBySide={gitDiffSideBySide}
        handleCreateRootCancel={handleCreateRootCancel}
        handleCreateRootStart={handleCreateRootStart}
        handleCreateRootSubmit={handleCreateRootSubmit}
        handleMainViewSwitcherChange={handleMainViewSwitcherChange}
        handleNodeManagerRefresh={handleNodeManagerRefresh}
        handleOpenProjectAdd={handleOpenProjectAdd}
        handleProjectTreeRefresh={handleProjectTreeRefresh}
        handleRestartAgent={handleRestartAgent}
        handleRunAgentLifecycleCommand={handleRunAgentLifecycleCommand}
        handleStartUpdate={handleStartUpdate}
        isMobile={isMobile}
        mainView={mainView}
        mainViewRef={mainViewRef}
        mobileEnterKeySends={mobileEnterKeySends}
        onGoHome={onGoHome}
        projectAddMode={projectAddMode}
        projectAddOverlay={projectAddOverlay}
        projectTreeTabRequest={projectTreeTabRequest}
        renderRootGitContent={renderRootGitContent}
        renderRootRelatedContent={renderRootRelatedContent}
        renderRootWorktreeContent={renderRootWorktreeContent}
        rootEntries={rootEntries}
        rootSessionIndicators={rootSessionIndicators}
        selectedDirKey={selectedDirKey}
        sendShortcut={sendShortcut}
        setCodexRateLimitsRefreshToken={setCodexRateLimitsRefreshToken}
        setCreatingRootName={setCreatingRootName}
        setFontSizePreferences={setFontSizePreferences}
        setGitDiffSideBySide={setGitDiffSideBySide}
        setIsLeftOpen={setIsLeftOpen}
        setMobileEnterKeySends={setMobileEnterKeySends}
        setProjectTreeTab={setProjectTreeTab}
        setSendShortcut={setSendShortcut}
        setSidebarsSwapped={setSidebarsSwapped}
        setTreeSortMode={setTreeSortMode}
        showHiddenFiles={showHiddenFiles}
        showUpdateButton={showUpdateButton}
        sidebarsSwapped={sidebarsSwapped}
        switchMainView={switchMainView}
        treeSortMode={treeSortMode}
        updateBusy={updateBusy}
        updateHelp={updateHelp}
        updateLabel={updateLabel}
        updateSummary={updateSummary}
        />}
        rightSidebar={sessionSidebar}
main={<
        SessionPane
        sessionView={sessionView}
        showSessionPane={showSessionPane}
        actionHandlers={actionHandlers}
                activePendingPluginTrust={activePendingPluginTrust}
                clearFileCacheForRoot={clearFileCacheForRoot}
                currentDirectorySortMode={currentDirectorySortMode}
                currentDirectorySortOverride={currentDirectorySortOverride}
                currentFileEditing={currentFileEditing}
                currentFileScrollKey={currentFileScrollKey}
                currentMainContentView={currentMainContentView}
                currentRootDisplayName={currentRootDisplayName}
                currentRootId={currentRootId}
                currentRootIdRef={currentRootIdRef}
                directoryUploadAbortRef={directoryUploadAbortRef}
                directoryUploadProgress={directoryUploadProgress}
                file={file}
                fileEditStore={fileEditStore}
                fileRef={fileRef}
                fileScrollPositionsRef={fileScrollPositionsRef}
                getDirectorySortKey={getDirectorySortKey}
                getDisplayNodeColor={getDisplayNodeColor}
                getRootDisplayName={getRootDisplayName}
                gitDiff={gitDiff}
                gitDiffSideBySide={gitDiffSideBySide}
                handleCreateBlankFile={handleCreateBlankFile}
                handleDirectoryPathClick={handleDirectoryPathClick}
                handleDisablePendingPlugins={handleDisablePendingPlugins}
                handleFileViewerFileClick={handleFileViewerFileClick}
                handleFileViewerPathClick={handleFileViewerPathClick}
                handleGitDiffPathClick={handleGitDiffPathClick}
                handleOpenWorktreeLocation={handleOpenWorktreeLocation}
                handleRemoveCurrentRoot={handleRemoveCurrentRoot}
                handleRemoveCurrentWorktree={handleRemoveCurrentWorktree}
                handleRenameCurrentRoot={handleRenameCurrentRoot}
                handleSessionChipClick={handleSessionChipClick}
                handleSwitchWorktreeStart={handleSwitchWorktreeStart}
                handleTreeUpload={handleTreeUpload}
                handleTrustPendingPlugins={handleTrustPendingPlugins}
                handleViewerSelectionChange={handleViewerSelectionChange}
                kanbanTaskPanel={kanbanTaskPanel}
                mainDirectoryError={mainDirectoryError}
                mainView={mainView}
                managedRootByIdRef={managedRootByIdRef}
                matchedPlugin={matchedPlugin}
                persistFileScrollPositions={persistFileScrollPositions}
                pluginBypass={pluginBypass}
                pluginContentRef={pluginContentRef}
                pluginHandlers={pluginHandlers}
                pluginLoading={pluginLoading}
                pluginRender={pluginRender}
                pluginRendererKey={pluginRendererKey}
                pluginThemeVars={pluginThemeVars}
                projectAddMode={projectAddMode}
                projectAddOverlay={projectAddOverlay}
                refreshGitStatus={refreshGitStatus}
                refreshTreeDir={refreshTreeDir}
                selectedDir={selectedDir}
                selectedDirRef={selectedDirRef}
                selectedSession={selectedSession}
                setDirectorySortOverrides={setDirectorySortOverrides}
                setEntriesByPath={setEntriesByPath}
                setFile={setFile}
                setPluginBypass={setPluginBypass}
                setScheduledAgentDialogOpen={setScheduledAgentDialogOpen}
                showHiddenFiles={showHiddenFiles}
                switchToPluginView={switchToPluginView}
                switchToRawFileView={switchToRawFileView}
                t={t}
                visibleMainEntries={visibleMainEntries}
                workspaceBoard={workspaceBoard}
                workspaceOpen={workspaceOpen}
                worktreeCreateOverlay={worktreeCreateOverlay}
                worktreeSwitchOpen={worktreeSwitchOpen}
                worktreeSwitchOverlay={worktreeSwitchOverlay}
        />}
footer={<
        AppActionBar
        actionBarInputHistory={actionBarInputHistory}
        actionBarQueuedMessages={actionBarQueuedMessages}
        actionBarSession={actionBarSession}
        actionHandlers={actionHandlers}
        agentsVersion={agentsVersion}
        attachedFileContext={attachedFileContext}
        canOpenSessionDrawer={canOpenSessionDrawer}
        codexRateLimitsRefreshToken={codexRateLimitsRefreshToken}
        currentRootId={currentRootId}
        currentRootIdRef={currentRootIdRef}
        currentRootSlashCommandResult={currentRootSlashCommandResult}
        detachedBoundSession={detachedBoundSession}
        drawerOpenByRootRef={drawerOpenByRootRef}
        editDraftRequest={editDraftRequest}
        getDisplayNodeColor={getDisplayNodeColor}
        handleCancelCurrentTurn={handleCancelCurrentTurn}
        handleClearFileContext={handleClearFileContext}
        handleNewSession={handleNewSession}
        handleRemoveQueuedMessage={handleRemoveQueuedMessage}
        handleRequestFileContext={handleRequestFileContext}
        handleSelectSession={handleSelectSession}
        handleSendMessage={handleSendMessage}
        handleSendQueuedMessageNow={handleSendQueuedMessageNow}
        handleSetPlanMode={handleSetPlanMode}
        handleUpdateQueuedMessage={handleUpdateQueuedMessage}
        interactionModeRef={interactionModeRef}
        isDrawerOpen={isDrawerOpen}
        isMobile={isMobile}
        mainView={mainView}
        managedRootByIdRef={managedRootByIdRef}
        mobileEnterKeySends={mobileEnterKeySends}
        normalizeMode={normalizeMode}
        pendingPlanMode={pendingPlanMode}
        renderRootSlashCommandResult={renderRootSlashCommandResult({copiedSlashCommandKeys, copyText, reportError, setCopiedSlashCommandKeys, slashCopyResetTimersRef, t})}
        scopedRootKey={scopedRootKey}
        sendShortcut={sendShortcut}
        setDrawerOpenForRoot={setDrawerOpenForRoot}
        setInteractionMode={setInteractionMode}
        status={status}
        />}
drawer={<
        SessionSheet
        BottomSheet={BottomSheet}
        actionHandlers={actionHandlers}
        availableAgents={availableAgents}
        currentRootId={currentRootId}
        currentRootIdRef={currentRootIdRef}
        currentSession={currentSession}
        drawerLoadingSessionByRoot={drawerLoadingSessionByRoot}
        drawerScrollRef={drawerScrollRef}
        drawerSessionSnapshot={drawerSessionSnapshot}
        getDisplayNodeColor={getDisplayNodeColor}
        gitFileStatsByPath={gitFileStatsByPath}
        handleAskUserAnswer={handleAskUserAnswer}
        handleDrawerSessionFileClick={handleDrawerSessionFileClick}
        handleEditUserMessage={handleEditUserMessage}
        handleForkAgentMessage={handleForkAgentMessage}
        handleRemoveSessionRelatedFile={handleRemoveSessionRelatedFile}
        handleSelectSession={handleSelectSession}
        interactionModeRef={interactionModeRef}
        isDrawerOpen={isDrawerOpen}
        mainView={mainView}
        managedRootByIdRef={managedRootByIdRef}
        selectedKanbanTaskId={selectedKanbanTaskId}
        sessionViewerComposerOverlayInset={sessionViewerComposerOverlayInset}
        setDrawerOpenForRoot={setDrawerOpenForRoot}
        setInteractionMode={setInteractionMode}
        slashCommandResultForSession={slashCommandResultForSession}
        t={t}
        />}
      />
<OnboardingGate
      completeOnboarding={completeOnboarding}
      dismissOnboarding={dismissOnboarding}
      handleOnboardingStepChange={handleOnboardingStepChange}
      isMobile={isMobile}
      onboardingOpen={onboardingOpen}
      setIsLeftOpen={setIsLeftOpen}
      setIsRightOpen={setIsRightOpen}
      setOnboardingMainContentViewRoot={setOnboardingMainContentViewRoot}
      setOnboardingOpen={setOnboardingOpen}
      />
<E2eePrompt
      bootstrapState={bootstrapState}
      e2eePromptBusy={e2eePromptBusy}
      e2eePromptError={e2eePromptError}
      e2eeSecretInput={e2eeSecretInput}
      e2eeState={e2eeState}
      setE2eePromptError={setE2eePromptError}
      setE2eeSecretInput={setE2eeSecretInput}
      submitE2EESecret={submitE2EESecret}
      t={t}
      />
<TaskInlineEditPanel
      DEFAULT_TASK_AGENT={DEFAULT_TASK_AGENT}
      DEFAULT_TASK_MODEL={DEFAULT_TASK_MODEL}
      PanelShell={PanelShell}
      applyTaskInlineCandidate={applyTaskInlineCandidate}
      availableAgents={availableAgents}
      closeTaskEditDialog={closeTaskEditDialog}
      composerInputStyle={composerInputStyle}
      currentRootId={currentRootId}
      firstAgentStage={firstAgentStage}
      firstUserInputTemplate={firstUserInputTemplate}
      handleTaskInlineAttachmentChange={handleTaskInlineAttachmentChange}
      isMobile={isMobile}
      managedRootByIdRef={managedRootByIdRef}
      removeTaskInlineAttachment={removeTaskInlineAttachment}
      saveTaskInlineEdit={saveTaskInlineEdit}
      setTaskInlineActiveToken={setTaskInlineActiveToken}
      setTaskInlineCandidateIndex={setTaskInlineCandidateIndex}
      setTaskInlineCandidates={setTaskInlineCandidates}
      setTaskInlineEdit={setTaskInlineEdit}
      setTaskInlineHelpKey={setTaskInlineHelpKey}
      switchTaskInlineEditProject={switchTaskInlineEditProject}
      t={t}
      taskCreateProjectOptions={taskCreateProjectOptions}
      taskInlineActiveToken={taskInlineActiveToken}
      taskInlineAttachmentInputRef={taskInlineAttachmentInputRef}
      taskInlineCandidateIndex={taskInlineCandidateIndex}
      taskInlineCandidateItemRefs={taskInlineCandidateItemRefs}
      taskInlineCandidates={taskInlineCandidates}
      taskInlineEdit={taskInlineEdit}
      taskInlineEditorRef={taskInlineEditorRef}
      taskInlineHelpKey={taskInlineHelpKey}
      taskInlineSaving={taskInlineSaving}
      taskInlineUploadAbortRef={taskInlineUploadAbortRef}
      taskInlineUploadProgress={taskInlineUploadProgress}
      taskTemplates={taskTemplates}
      taskWorktreeBranches={taskWorktreeBranches}
      />
<TaskSessionErrorDialog
      setTaskSessionErrorDialog={setTaskSessionErrorDialog}
      t={t}
      taskCardIconButtonStyle={taskCardIconButtonStyle}
      taskSessionErrorDialog={taskSessionErrorDialog}
      />
      <ScheduledAgentTaskDialog
        open={scheduledAgentDialogOpen}
        rootId={currentRootId}
        agents={availableAgents}
        onClose={() => setScheduledAgentDialogOpen(false)}
      />
      <TaskTemplateDialog
        open={taskTemplateDialogOpen}
        agents={availableAgents}
        template={taskTemplateDialogTemplate}
        onClose={() => setTaskTemplateDialogOpen(false)}
        onSaved={handleTaskTemplateSaved}
        currentRootId={currentRootId}
      />
<TaskDetailPanelGate
      applyTaskDetails={applyTaskDetails}
      availableAgents={availableAgents}
      currentRootId={currentRootId}
      currentRootIdRef={currentRootIdRef}
      getDisplayNodeColor={getDisplayNodeColor}
      getNodeIdForRoot={getNodeIdForRoot}
      handleMoveKanbanTask={handleMoveKanbanTask}
      handleTaskSessionDrawerOpen={handleTaskSessionDrawerOpen}
      selectedKanbanTask={selectedKanbanTask}
      setSelectedKanbanTaskId={setSelectedKanbanTaskId}
      taskDetailsById={taskDetailsById}
      />
      <ToastContainer />
      <DialogHost />
    </>
  );
}
