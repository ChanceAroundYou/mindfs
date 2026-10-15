import React, { useCallback, useEffect, useMemo } from "react";
import { fetchTaskDetails } from "../services/tasks";
import { useAppKanbanTaskPanel } from "./useAppKanbanTaskPanel";
import { useAppRootContentRenderers } from "./useAppRootContentRenderers";
import { useAppSessionView } from "./useAppSessionView";
import { useAppKanbanStageColumns } from "./useAppKanbanStageColumns";
import { useAppSlashCommandResult } from "./useAppSlashCommandResult";
import { useAppMainViewActions } from "./useAppMainViewActions";
import { useAppWorkspaceActions } from "./useAppWorkspaceActions";
import { GitFileStat, MultiProjectSessionGroup, SessionItem, SlashCommandResult } from "./appSession";
import { ManagedRootPayload, shouldShowUpdateButton, updateButtonLabel, updateSummaryText } from "./appMisc";
import { applyPresenceToItems } from "../shared/sessionPresence";
import { MainViewMode, buildFileScrollKey } from "./appPath";
import type { URLState } from "./appPath";
import { useAppWorkspaceBoardComposition } from "./useAppWorkspaceBoardComposition";
import { useSessionSidebarView } from "./useSessionSidebarView";
import { useRootContentView } from "./useRootContentView";
import { useProjectTreeActions } from "./useProjectTreeActions";
import { usePluginThemeVars } from "./usePluginThemeVars";
import { useSessionRelatedFiles } from "./useSessionRelatedFiles";
import type { KanbanTask, TaskDetail, TaskTemplate } from "../services/tasks";
import type { ActionHandlers } from "./useActionHandlers";
import type { AgentStatus } from "../services/agents";
import type { DirectorySortMode, FileEntry } from "../services/prefs/directorySort";
import type { FilePayload } from "../services/file";
import type { GitDiffPayload } from "../services/git";
import type { GitHistoryItem, GitHistoryPayload, GitStatusItem, GitStatusPayload, GitWorktreeItem } from "../services/git";
import type { MessageKey, MessageParams } from "../i18n";
import type { PluginSourceBundle } from "../plugins/manager";
import type { PluginOutput, ViewPlugin } from "../plugins/manager";
import type { ProjectSessionGroup } from "../components/session/SessionList";
import type { ProjectTreeTab } from "../components/file/fileTreeTypes";
import type { RelatedFile, Session } from "../services/session";
import type { RelatedFileClickTarget } from "./appSession";
import type { UpdateState } from "../services/update";

/**
 * 见 docs/plans/app-tsx-under-1000.md：分层聚合 hook。
 */
export type { UseAppViewCompositionParams } from "./useAppViewCompositionTypes";
import type { UseAppViewCompositionParams } from "./useAppViewCompositionTypes";


/**
 * 本层由 App.tsx 原样搬出，hook 调用顺序与搬出前逐字一致。
 */
export function useAppViewComposition(params: UseAppViewCompositionParams) {
  const { actionHandlers, actionHandlersRef, activeBoundSessionKey, applyTaskDetails,
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
    selectedSession, selectedSessionLoading, selectedSessionRef, selectedSessionSnapshot, selectedSessionPresence,
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
    worktreeItemsByRoot, worktreeLoadingByRoot, worktreeStatusByPath, worktreeStatusLoadingByPath } = params;

  // 「委托中」三态在这条链上唯一一次算好、盖到列表项上（列表/头部/底部三处同源）。
  // 放在这一层是因为只有这里有 pending 真相表：单项目列表的会话项本身不保证带
  // 最新 pending（轮询只写分组），委托态若读项上的 pending 会永远算不出来。
  // 逐张卡各算一遍是 O(n²)（流式期间 30ms 一次重渲染），故按列表算一次。
  const sessionsWithPresence = useMemo(() => {
    const projected = sessions.map((item) => {
      const key = item.key || item.session_key || "";
      const root = String((item as any).root_id || currentRootId || "");
      const pending = !!multiProjectPendingByKey[rootSessionKey(root, key)];
      return pending === !!item.pending ? item : ({ ...item, pending } as SessionItem);
    });
    return applyPresenceToItems(projected);
  }, [sessions, currentRootId, multiProjectPendingByKey, rootSessionKey]);

  const groupsWithPresence = useMemo(
    () =>
      multiProjectSessionGroups.map((group) => ({
        ...group,
        sessions: applyPresenceToItems(group.sessions || []),
      })),
    [multiProjectSessionGroups],
  );

  const {
    handleSelectedSessionFileClick,
    drawerSessionSnapshot,
    rootSessionIndicators,
    handleDrawerSessionFileClick,
    handleRemoveSessionRelatedFile,
  } = useSessionRelatedFiles({
    activeBoundSessionKey,
    boundSessionByRootRef,
    cacheVersion,
    currentRootId,
    currentRootIdRef,
    currentSession,
    drawerSessionByRootRef,
    getNodeIdForRoot,
    getSessionSnapshot,
    managedRootByKeyRef,
    managedRootIds,
    multiProjectPendingByKey,
    openRelatedFileDiff,
    rootSessionKey,
    selectedKanbanTaskId,
    selectedSession,
    selectedSessionRef,
    setExpanded,
    setProjectTreeTabRequest,
    setTaskRelatedFilesById,
    updateSessionRelatedFilesForKey,
  });


  const { handleAskUserAnswer, openWorkspaceProject } = useAppWorkspaceActions({
    actionHandlersRef,
    getNodeIdForRoot,
    switchMainView,
    setTaskTemplateFilter,
  });

  const handleEditUserMessage = useCallback((content: string) => {
    setEditDraftRequest((prev) => ({
      id: (prev?.id || 0) + 1,
      content,
    }));
  }, []);

  const currentFileScrollKey = buildFileScrollKey(
    file?.root || currentRootId,
    file?.path,
  );
  const { slashCommandResultForSession
  } = useAppSlashCommandResult({
    slashCommandResults, rootSessionKey,
  });
  const currentRootSlashCommandResult = slashCommandResultForSession(currentRootId, null);
  // The memory badge is always rendered above the composer, so floating
  // session controls must always clear that overlay row.
  //
  // 类型标注 `: 20` 不是装饰：`SessionSheet` 把这个 prop 声明成**字面量类型** `20`
  // （`sessionViewerComposerOverlayInset: 20`，等于一个编译期断言）。
  // 原先它就在 App.tsx 里、直接传下去，`const x = 20` 的类型是字面量 `20`，没问题；
  // 现在它要经过本 hook 的**返回对象**，而对象字面量的属性类型会**加宽**
  // 字面量（`{ x }` 的 x 变成 `number`），于是 App.tsx 报 TS2322。
  // 显式标注把字面量类型钉住，运行时零变化。
  const sessionViewerComposerOverlayInset: 20 = 20;
  const { sessionView
  } = useAppSessionView({
    selectedSessionSnapshot, selectedSessionPresence, availableAgents, slashCommandResultForSession, selectedSession,
    currentRootId, sessionViewerComposerOverlayInset, selectedSessionLoading, getRootDisplayName,
    getDisplayNodeColor, managedRootByIdRef, gitFileStatsByPath, handleSelectedSessionFileClick,
    actionHandlers, handleRemoveSessionRelatedFile, handleAskUserAnswer, handleEditUserMessage,
    handleForkAgentMessage,
  });


  let workspaceView: React.ReactNode;
  const gitStatusAvailable = gitStatus?.available === true;
  const gitHistoryAvailable = gitHistory?.available === true;
  const gitStatusExpanded = currentRootId ? gitStatusExpandedByRoot[scopedRootKey(currentRootId)] !== false : true;
  const gitHistoryExpandedCommits = currentRootId ? gitHistoryExpandedByRoot[scopedRootKey(currentRootId)] || {} : {};
  const shouldRenderGitPanel =
    gitStatusLoading || gitStatusAvailable;
  const shouldRenderGitHistoryPanel =
    gitHistoryLoading || (gitHistoryAvailable && (gitHistory?.items.length || 0) > 0);
  const activePendingPluginTrust =
    pendingPluginTrust && pendingPluginTrust.rootId === currentRootId ? pendingPluginTrust : null;
  const relatedSessionSnapshot =
    selectedKanbanTaskSessionSnapshot ||
    selectedSessionSnapshot ||
    drawerSessionSnapshot ||
    lastMainSessionSnapshotRef.current;
  const relatedSessionRootId =
    (relatedSessionSnapshot?.root_id as string | undefined) ||
    selectedKanbanTask?.root_id ||
    (selectedSession?.root_id as string | undefined) ||
    currentRootId;
  const relatedSessionKey = relatedSessionSnapshot?.key || relatedSessionSnapshot?.session_key;
  const relatedSessionNodeId =
    String((relatedSessionSnapshot as any)?._nodeId || "").trim() || undefined;
  const relatedSelectedPath = gitDiff?.path || file?.path || "";
  // 任务侧优先，但不是无条件优先：任务记录的 worktree 目录已经被删时（worktree_missing，
  // 服务端派生），拿它去展开 git 面板只会打开一个不存在的目录 —— 而会话侧可能已经
  // repoint 干净、指向一个还活着的树。这种情况下让会话侧说话。
  const taskWorktree = selectedKanbanTask?.worktree_path && selectedKanbanTask.worktree_missing !== true
    ? {
        root_id: selectedKanbanTask.root_id,
        path: selectedKanbanTask.worktree_path,
      }
    : null;
  const {
    relatedWorktree,
    selectedSessionRelatedFiles,
    selectedSessionRelatedFileGroups,
    gitStatsRefreshKey,
    selectedRelatedFileStatsByKey,
  } = useRootContentView({
    selectedKanbanTask,
    taskRelatedFilesById,
    relatedSessionSnapshot,
    relatedSessionRootId,
    relatedSessionNodeId,
    currentRootId,
    managedRootByIdRef,
    gitFileStatsByPath,
    t,
  });

  const {
    refreshProjectTreeRelatedFiles,
    handleNodeManagerRefresh,
    handleProjectTreeRefresh,
  } = useProjectTreeActions({
    currentRootId,
    currentRootIdRef,
    relatedSessionRootId,
    relatedSessionKey,
    selectedKanbanTask,
    taskSessionKeysByIdRef,
    selectedDirRef,
    entriesByPathRef,
    fileRef,
    relatedWorktree,
    projectTreeTab,
    getNodeIdForRoot,
    refreshTreeDir,
    refreshCurrentFileContent,
    refreshGitStatus,
    refreshGitHistory,
    refreshProjectTreeWorktrees,
    expandProjectTreeWorktree,
    refreshTaskRelatedFiles,
    updateSessionRelatedFilesForKey,
    refreshManagedRoots,
  });
  const { renderRootWorktreeContent, renderRootRelatedContent, renderRootGitContent
  } = useAppRootContentRenderers({
    currentRootId, scopedRootKey, managedRootByIdRef, relatedWorktree,
    worktreeItemsByRoot, worktreeLoadingByRoot, worktreeErrorByRoot, expandedWorktreeByRoot,
    worktreeStatusByPath, worktreeStatusLoadingByPath, toggleProjectTreeWorktree, findManagedRootByPath,
    openGitDiff, actionHandlers, relatedSessionRootId, relatedSessionSnapshot,
    relatedSessionKey, relatedSelectedPath, relatedSelectedFileKey, selectedKanbanTask,
    selectedSessionRelatedFiles, selectedSessionRelatedFileGroups, selectedRelatedFileStatsByKey, gitFileStatsByPath,
    handleSelectedSessionFileClick, handleRemoveSessionRelatedFile, gitStatus, gitStatusByRoot,
    gitStatusLoading, gitStatusLoadingByRoot, gitHistory, gitHistoryByRoot,
    gitHistoryLoading, gitHistoryLoadingMore, gitHistoryLoadingByRoot, gitStatusExpandedByRoot,
    gitHistoryExpandedByRoot, setGitStatusExpandedByRoot, setGitHistoryExpandedByRoot, refreshGitStatus,
    refreshGitHistory, loadMoreGitHistory, openGitCommitDiff, handleGitPull,
    handleGitPush, handleGitCommit, handleGitStageItem, handleGitUnstageItem,
    handleGitDiscardItem, switchGitBranch, treeSortMode,
  });
  // 四块 = 未开始 / 执行中 / 待审核 / 已结束（已结束内分 完成、取消；失败并入取消）。
  const { kanbanStageColumns
  } = useAppKanbanStageColumns({
    kanbanTasks, t,
  });
  const {
    workspaceBoard, workspaceFilter, changeWorkspaceFilter, workspaceCollapsedKeys,
    toggleWorkspaceProject, setWorkspaceBoardToken,
  } = useAppWorkspaceBoardComposition({
    workspaceOpen, workspaceBoardRefreshRef, managedRootIds, getRootDisplayName,
    getDisplayNodeColor, getNodeIdForRoot, currentRootIdRef, taskDetailsById,
  });
  // 左下角四态切换器：只切面板 + 关掉悬浮框。
  // 会话选中与面板是正交的两条线——切面板一律不解除选中（面板不显示而已）。
  const { handleMainViewSwitcherChange, switchToRawFileView, switchToPluginView
  } = useAppMainViewActions({
    currentRootIdRef, setDrawerOpenForRoot, interactionModeRef, setInteractionMode,
    mainViewRef, switchMainView, file, gitDiff,
    mainEntriesRef, mainDirectoryErrorRef, selectedDirRef, actionHandlersRef,
    actionHandlers, replaceURLState, pluginBypassRef, setPluginBypass,
    fileCursorRef,
  });

  // 工作台的快速发起不自己建任务：挑完项目 + 模板就打开看板那套新建任务面板，
  // 模板、worktree、agent、附件全都跟项目看板里一致，不在工作台上复制第二份实现。
  const handleWorkspaceCreateTask = useCallback((rootId: string, nodeId: string, template: TaskTemplate) => {
    openTaskCreateDialog(template, rootId, nodeId);
  }, [openTaskCreateDialog]);
  // 工作台里点任务卡：留在工作台，就地弹任务详情面板。
  // 不走 openWorkspaceProject —— 那会切项目、切看板、改 URL，等于把人甩出工作台。
  // overview 只带任务不带 stage_runs/events（详情面板要后者），所以按项目内任务号
  // 取那一条完整详情，灌进 taskDetailsById 后由 selectedKanbanTask 接手。
  const openWorkspaceTaskDetail = useCallback(async (item: { root_id: string; nodeId: string; task: KanbanTask }) => {
    const rootId = item.root_id;
    const taskNumber = Number(item.task?.task_number || 0);
    if (!rootId || !item.task?.id) return;
    // 先选中，卡片立刻有选中态；详情随后补上。面板此时短暂显示 overview 那份基础信息。
    setSelectedKanbanTaskId(item.task.id);
    const details = await fetchTaskDetails(rootId, { taskNumber }, item.nodeId).catch(() => [] as TaskDetail[]);
    const detail = details.find((entry) => entry.task?.id === item.task.id) || details[0];
    if (!detail) return;
    applyTaskDetails(rootId, [detail]);
  }, [applyTaskDetails]);
  const { kanbanTaskPanel
  } = useAppKanbanTaskPanel({
    workspaceOpen, currentRootId, currentRootIdRef, workspaceBoard,
    workspaceFilter, changeWorkspaceFilter, workspaceCollapsedKeys, toggleWorkspaceProject,
    setWorkspaceBoardToken, managedRootIds, getRootDisplayName, openWorkspaceProject,
    openWorkspaceTaskDetail, handleWorkspaceCreateTask, setSelectedKanbanTaskId, kanbanTasksLoading,
    kanbanStageColumns, selectedKanbanTaskId, expandedTaskInputIds, setExpandedTaskInputIds,
    collapsedTaskCompletionGroups, setCollapsedTaskCompletionGroups, collapsedKanbanColumns, setCollapsedKanbanColumns,
    taskFirstInputById, taskSessionKeysById, boardSessionByKey, getDisplayNodeColor,
    handleSelectKanbanTask, handleMoveKanbanTask, openTaskCreateDialog, handleTaskSessionDrawerOpen,
    setTaskSessionErrorDialog, loadKanbanTasks, taskTemplateActionMenuRef, taskTemplateActionMenuOpen,
    setTaskTemplateActionMenuOpen, taskCreateTemplateMenuRef, taskCreateTemplateMenuOpen, setTaskCreateTemplateMenuOpen,
    taskTemplates, taskTemplateFilter, setTaskTemplateFilter, openTaskTemplateEditor,
    handleDeleteTaskTemplate,
  });


  usePluginThemeVars({document, pluginBypass, pluginRender, pluginThemeVars, useEffect});



  // 更新状态由 WS 的 app.update 推送（pushInitialAppUpdate 在连接时推一次），
  // 不是启动时同步就绪的。首帧 updateState 还是 normalizeUpdateState(null) 的全空
  // 初值，若直接拿它判可见性，就会先按「没更新」画一帧、收到推送后再翻出来 ——
  // 与 PWA 安装按钮同一种闪烁。WS 连上之前一律不画，连上后状态已由服务端推来。
  const showUpdateButton = status === "connected" && shouldShowUpdateButton(updateState);
  const updateBusy =
    updateSubmitting ||
    ["downloading", "installing", "restarting"].includes(
      (updateState.status || "").toLowerCase(),
    );
  const updateLabel = updateButtonLabel(updateState, t);
  const updateHelp = updateState.message || updateSummaryText(updateState, t);
  const updateSummary = updateSummaryText(updateState, t);

  // 归档视图：在右栏列内跟会话列表互换，不出全屏浮层。
  const openArchivePanel = useCallback(() => {
    setArchiveOpen(true);
  }, []);
  const closeArchivePanel = useCallback(() => {
    setArchiveOpen(false);
  }, []);
  // 选中归档会话 = 要去读它。读完顺手退回列表，右栏停在归档视图上会让人
  // 以为「对话没打开」（主区变了，右栏没变）。
  const handleSelectArchivedSession = useCallback(
    (session: SessionItem) => {
      handleSelectSessionAndClose(session);
      setArchiveOpen(false);
    },
    [handleSelectSessionAndClose],
  );

  const { sessionImportMenu, sessionSidebar } = useSessionSidebarView({
    sessionListMode,
    importMenuRef,
    importMenuOpen,
    setImportMenuOpen,
    availableAgents,
    externalImportAgent,
    setExternalImportAgent,
    setExternalSelectedKey,
    enterImportMode,
    externalFilterBound,
    setExternalFilterBound,
    externalSessions,
    externalSelectedKey,
    importingExternalSessionKeys,
    selectedExternalImportKeys,
    loadingExternalSessions,
    externalSessionsError,
    loadingOlderExternalSessions,
    confirmingExternalImport,
    hasMoreExternalSessions,
    exitImportMode,
    toggleExternalImportSelection,
    toggleAllExternalImportSelection,
    handleConfirmExternalImport,
    handleLoadOlderExternalSessions,
    multiProjectSessionsEnabled,
    sessionSearchOpen,
    sessionSearchResultsMode,
    multiProjectSessionGroups: groupsWithPresence,
    activeBoundSessionKey,
    currentRootId,
    currentRootNodeId,
    treeSortMode,
    multiProjectSessionsLoading,
    syncingSessionKeys,
    handleSelectSessionAndClose,
    handleSyncSession,
    handlePinSession,
    handleRenameSession,
    handleDeleteSession,
    handleArchiveSession,
    openArchivePanel,
    closeArchivePanel,
    archiveOpen,
    archiveLoading,
    archivedGroups,
    handleSelectArchivedSession,
    loadMoreMultiProjectSessions,
    loadChildSessionsForParent,
    sessionSearchResults,
    sessions: sessionsWithPresence,
    sessionSearchQuery,
    sessionSearchLoading,
    toggleSessionSearch,
    handleSearchQueryChange,
    executeSessionSearch,
    closeSessionSearch,
    openSessionSearch,
    handleLoadOlderSessions,
    loadingOlderSessions,
    hasMoreSessions,
  });

  return {
    activePendingPluginTrust, currentFileScrollKey, currentRootSlashCommandResult, drawerSessionSnapshot,
    handleAskUserAnswer, handleDrawerSessionFileClick, handleEditUserMessage, handleMainViewSwitcherChange,
    handleNodeManagerRefresh, handleProjectTreeRefresh, handleRemoveSessionRelatedFile, kanbanTaskPanel,
    renderRootGitContent, renderRootRelatedContent, renderRootWorktreeContent, rootSessionIndicators,
    sessionSidebar, sessionView, sessionViewerComposerOverlayInset, showUpdateButton,
    slashCommandResultForSession, switchToPluginView, switchToRawFileView, updateBusy,
    updateHelp, updateLabel, updateSummary, workspaceBoard
  };
}
