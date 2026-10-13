import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import { FileEditStore } from "../services/fileEditing";
import { bootstrapService } from "../services/net/bootstrap";
import { fetchEditableFile, saveTextFile } from "../services/file";
import { scopeKey } from "../shared/scope";
import { DEFAULT_DIRECTORY_SORT_MODE } from "../services/prefs/directorySort";
import { syncNodesFromServer } from "../services/net/nodeRegistry";
import { fetchCandidates } from "../services/candidates";
import { useI18n } from "../i18n";
import { shouldAutoStartOnboarding } from "../services/prefs/onboarding";
import { ManagedRootPayload, normalizeUpdateState, useResponsive } from "./appMisc";
import { useAppRefSync } from "./useAppRefSync";
import { useAppProjectDisplay } from "./useAppProjectDisplay";
import { useAppRootBindingSetters } from "./useAppRootBindingSetters";
import { useAppSessionPins } from "./useAppSessionPins";
import { useAppReplaceURLState } from "./useAppReplaceURLState";
import { MainViewMode, isDirectorySortMode, readURLState } from "./appPath";
import { useBackLayer } from "../hooks/useBackNavigation";
import { AttachedFileContext, MultiProjectSessionGroup, SessionItem, SlashCommandResult, ViewerSelection, WSStatus } from "./appSession";
import { CANDIDATE_FETCH_DEBOUNCE_MS, DIRECTORY_SORT_OVERRIDES_STORAGE_KEY, TREE_SORT_STORAGE_KEY } from "./appStorage";
import { getDirectorySortKey } from "./appUtils";
import { firstAgentStage } from "./appTask";
import { useAppStateRefs } from "./useAppStateRefs";
import { useAppRootSelection } from "./useAppRootSelection";
import { useCompletionSound } from "./useCompletionSound";
import { useGitData } from "./useGitData";
import { useProjectTreeWorktrees } from "./useProjectTreeWorktrees";
import { useTaskTemplates } from "./useTaskTemplates";
import { useTaskWorkspaceState } from "./useTaskWorkspaceState";
import { useUiPreferences } from "./useUiPreferences";
import { useTaskInlineCandidates } from "./useTaskInlineCandidates";
import { useTaskBoard } from "./useTaskBoard";
import { useTaskComposer } from "./useTaskComposer";
import type { BootstrapState } from "../services/net/bootstrap";
import type { FilePayload } from "../services/file";
import type { GitDiffPayload } from "../services/git";
import type { DirectorySortMode, FileEntry } from "../services/prefs/directorySort";
import type { PluginSourceBundle } from "../plugins/manager";
import type { UpdateState } from "../services/update";
import type { AgentConfigSwitchRequest, ProjectTreeTab } from "../components/file/fileTreeTypes";
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

  const [sessions, setSessions] = useState<SessionItem[]>([]);
  const sessionsRef = useRef<SessionItem[]>([]);
  // 显式字面量类型：这两个常量会被 return 出去，对象属性位会把 true / 300 拓宽成 boolean / number，
  // 而下游 useAppSessionLayer / useAppDomainLayer / useAppViewComposition 的签名按原 App 的推断写成
  // true / 300，拓宽后会 TS2322。不要删掉这里的标注。
  const multiProjectSessionsEnabled: true = true;
  // session.created 现在是所有建会话路径的统一信号（看板/定时/子会话/fork），
  // 一次动作可能连发好几条；合并 300ms 窗口内的多次重拉，避免逐节点扇出打爆接口。
  const MULTI_PROJECT_RELOAD_DEBOUNCE_MS: 300 = 300;
  const [multiProjectSessionGroups, setMultiProjectSessionGroups] = useState<MultiProjectSessionGroup[]>([]);
  // 多项目分组的 ref 镜像：删除会话时要跨分组收集整棵子树，只看 sessionsRef 会漏掉
  // 只存在于分组里的子会话（它们随后会被提升成顶层行，把面板撑爆）。
  const multiProjectSessionGroupsRef = useRef<MultiProjectSessionGroup[]>([]);
  const [multiProjectSessionsLoading, setMultiProjectSessionsLoading] = useState(false);
  // 多项目列表全量重拉竞态守卫：只应用最后一次发起的请求结果，避免旧响应覆盖新数据
  const multiProjectLoadSeqRef = useRef(0);
  const [multiProjectPendingByKey, setMultiProjectPendingByKey] = useState<Record<string, boolean>>({});
  const multiProjectPendingRef = useRef<Record<string, boolean>>({});
  const { applyPinSnapshot } = useAppSessionPins();
  const [syncingSessionKeys, setSyncingSessionKeys] = useState<Set<string>>(
    () => new Set(),
  );
  // 归档区：默认不拉任何数据，面板底部的入口行只留摘要，点开才懒加载。
  // 归档数据按项目分组存（面板和主列表一样按项目平铺），不进两棵列表 state。
  const [archivedGroups, setArchivedGroups] = useState<MultiProjectSessionGroup[]>([]);
  const [archiveOpen, setArchiveOpen] = useState(false);
  const [archiveLoading, setArchiveLoading] = useState(false);
  // 归档/取消归档后 +1，强制下面的懒加载 effect 重拉一次（否则摘要数字会停留在旧值）。
  const [archiveReloadToken, setArchiveReloadToken] = useState(0);
  const [hasMoreSessions, setHasMoreSessions] = useState(false);
  const [loadingOlderSessions, setLoadingOlderSessions] = useState(false);
  const [sessionListMode, setSessionListMode] = useState<"local" | "import">(
    "local",
  );
  const [availableAgents, setAvailableAgents] = useState<AgentStatus[]>([]);
  const [scheduledAgentDialogOpen, setScheduledAgentDialogOpen] = useState(false);
  const { kanbanTasks, setKanbanTasks, kanbanTaskCountItems, setKanbanTaskCountItems, taskDetailsById, setTaskDetailsById, taskFirstInputById, setTaskFirstInputById, taskSessionKeysById, setTaskSessionKeysById, taskRelatedFilesById, setTaskRelatedFilesById, taskDetailsByIdRef, workspaceBoardRefreshRef, taskSessionKeysByIdRef, selectedKanbanTaskId, setSelectedKanbanTaskId, expandedTaskInputIds, setExpandedTaskInputIds, overflowingTaskInputs, setOverflowingTaskInputs, collapsedTaskCompletionGroups, setCollapsedTaskCompletionGroups, collapsedKanbanColumns, setCollapsedKanbanColumns, taskInlineEdit, setTaskInlineEdit, taskSessionErrorDialog, setTaskSessionErrorDialog, taskInlineActiveToken, setTaskInlineActiveToken, taskInlineCandidates, setTaskInlineCandidates, taskInlineCandidateIndex, setTaskInlineCandidateIndex, taskInlineSaving, setTaskInlineSaving, taskInlineHelpKey, setTaskInlineHelpKey, taskInlineUploadProgress, setTaskInlineUploadProgress, directoryUploadProgress, setDirectoryUploadProgress, taskWorktreeBranches, setTaskWorktreeBranches, taskWorktreeBranchesLoading, setTaskWorktreeBranchesLoading, taskWorktreeBranchError, setTaskWorktreeBranchError, kanbanTasksLoading, setKanbanTasksLoading, kanbanLoadSeqRef, kanbanAbortRef, refreshTaskWorktreeRef, taskWorktreeBranchRequestRef, sessionListLoadSeqRef, sessionListAbortRef, listLoadErrorAtRef, taskInlineEditorRef, taskInlineCandidateItemRefs, taskInlineAttachmentInputRef, taskInlineUploadAbortRef, directoryUploadAbortRef, knownTaskWorktreePathsRef } = useTaskWorkspaceState();
  const [selectedSession, setSelectedSession] = useState<SessionItem | null>(
    null,
  );
  const [selectedSessionLoading, setSelectedSessionLoading] = useState(false);
  const [drawerLoadingSessionByRoot, setDrawerLoadingSessionByRoot] = useState<
    Record<string, string>
  >({});
  const [activeBoundSessionKey, setActiveBoundSessionKey] = useState<
    string | null
  >(null);
  const [pendingPlanMode, setPendingPlanMode] = useState(false);
  const [currentSession, setCurrentSession] = useState<SessionItem | null>(null);
  const [cacheVersion, setCacheVersion] = useState(0);
  const [slashCommandResults, setSlashCommandResults] = useState<
    Record<string, SlashCommandResult>
  >({});
  const [copiedSlashCommandKeys, setCopiedSlashCommandKeys] = useState<
    Record<string, true>
  >({});
  const [queueVersion, setQueueVersion] = useState(0);
  const [interactionMode, setInteractionMode] = useState<"main" | "drawer">(
    "main",
  );
  const [agentsVersion, setAgentsVersion] = useState(0);
  const [codexRateLimitsRefreshToken, setCodexRateLimitsRefreshToken] = useState(0);
  const [isDrawerOpen, setIsDrawerOpen] = useState(false);
  const { isMobile, isTablet } = useResponsive();
  const [isLeftOpen, setIsLeftOpen] = useState(() => window.innerWidth >= 768);
  const [isRightOpen, setIsRightOpen] = useState(
    () => window.innerWidth >= 768,
  );
  const [onboardingOpen, setOnboardingOpen] = useState(false);
  const [onboardingMainContentViewRoot, setOnboardingMainContentViewRoot] =
    useState<string | null>(null);
  const onboardingAutoStartRef = useRef(false);
  const [currentRootId, setCurrentRootId] = useState<string | null>(null);
  const currentRootIdRef = useRef<string | null>(null);
  const [currentRootNodeId, setCurrentRootNodeId] = useState<string | null>(null);
  const currentRootNodeIdRef = useRef<string | null>(null);
  const managedRootByIdRef = useRef<Record<string, ManagedRootPayload>>({});
  // 多节点同名项目的完整索引：nodeId::rootId → payload（byId 回退表只保留一份）
  const managedRootByKeyRef = useRef<Record<string, ManagedRootPayload>>({});

  const getNodeIdForRoot = useCallback((rootId: string): string | undefined => {
    const rid = String(rootId || "").trim();
    if (!rid) return undefined;
    // 当前选中的项目优先按其选中节点路由，避免同名项目被多节点表覆盖到错误节点
    if (rid === String(currentRootIdRef.current || "")) {
      const nid = String(currentRootNodeIdRef.current || "").trim();
      if (nid) return nid;
    }
    const entry = (managedRootByIdRef.current as Record<string, any>)[rid];
    const nid = String(entry?._nodeId || "").trim();
    return nid || undefined;
  }, []);

  // 按当前作用域解析复合键：当前根 → 选中节点；其他根 → 模块 map/裸回退
  const scopedRootKey = useCallback(
    (rootId: string): string =>
      scopeKey(getNodeIdForRoot(String(rootId || "")) ?? "", String(rootId || "")),
    [getNodeIdForRoot],
  );

  const {
    taskTemplateActionMenuRef,
    taskCreateTemplateMenuRef,
    taskTemplates,
    taskTemplateDialogOpen,
    setTaskTemplateDialogOpen,
    taskTemplateDialogTemplate,
    taskTemplateFilter,
    setTaskTemplateFilter,
    taskTemplateActionMenuOpen,
    setTaskTemplateActionMenuOpen,
    taskCreateTemplateMenuOpen,
    setTaskCreateTemplateMenuOpen,
    loadTaskTemplates,
    openTaskTemplateEditor,
    handleTaskTemplateSaved,
    handleDeleteTaskTemplate,
  } = useTaskTemplates({ currentRootId, scopedRootKey });

  useEffect(() => {
    if (!taskInlineEdit) return;
    window.setTimeout(() => {
      taskInlineEditorRef.current?.setText(taskInlineEdit.text || "");
      // TokenEditor 的 setText 不再抢焦点（常驻挂载的编辑器会夺走整页焦点），开面板时自己聚焦。
      taskInlineEditorRef.current?.focus();
    }, 0);
  }, [taskInlineEdit?.templateId]);

  useTaskInlineCandidates({AbortController, CANDIDATE_FETCH_DEBOUNCE_MS, currentRootId, fetchCandidates, firstAgentStage, getNodeIdForRoot, setTaskInlineCandidateIndex, setTaskInlineCandidates, taskInlineActiveToken, taskInlineEdit, taskTemplateFilter, taskTemplates, useEffect});

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
  const [editDraftRequest, setEditDraftRequest] = useState<{
    id: number;
    content: string;
  } | null>(null);
  const [entriesByPath, setEntriesByPath] = useState<
    Record<string, FileEntry[]>
  >({});
  const entriesByPathRef = useRef<Record<string, FileEntry[]>>({});
  const [expanded, setExpanded] = useState<string[]>([]);
  const [selectedDir, setSelectedDir] = useState<string | null>(null);
  const [selectedDirKey, setSelectedDirKey] = useState<string | null>(null);
  const [mainEntries, setMainEntries] = useState<FileEntry[]>([]);
  const [mainDirectoryError, setMainDirectoryError] = useState("");
  const mainEntriesRef = useRef<FileEntry[]>([]);
  mainEntriesRef.current = mainEntries;
  const mainDirectoryErrorRef = useRef("");
  mainDirectoryErrorRef.current = mainDirectoryError;
  const isGitRepo = useCallback(
    (rootID: string) => managedRootByIdRef.current[rootID]?.is_git_repo === true,
    [],
  );

  const {
    gitStatus,
    setGitStatus,
    gitStatusLoading,
    setGitStatusLoading,
    gitHistory,
    setGitHistory,
    gitHistoryLoading,
    setGitHistoryLoading,
    gitHistoryLoadingMore,
    gitStatusByRoot,
    setGitStatusByRoot,
    gitStatusLoadingByRoot,
    gitHistoryByRoot,
    setGitHistoryByRoot,
    gitHistoryLoadingByRoot,
    gitStatusExpandedByRoot,
    setGitStatusExpandedByRoot,
    gitHistoryExpandedByRoot,
    setGitHistoryExpandedByRoot,
    refreshGitStatus,
    refreshGitHistory,
    loadMoreGitHistory,
  } = useGitData({
    currentRootId,
    currentRootIdRef,
    scopedRootKey,
    getNodeIdForRoot,
    isGitRepo,
  });
  const [gitDiff, setGitDiff] = useState<GitDiffPayload | null>(null);
  const [relatedSelectedFileKey, setRelatedSelectedFileKey] = useState("");
  const [treeSortMode, setTreeSortMode] = useState<DirectorySortMode>(() => {
    if (typeof window === "undefined") {
      return DEFAULT_DIRECTORY_SORT_MODE;
    }
    const saved = window.localStorage.getItem(TREE_SORT_STORAGE_KEY);
    return isDirectorySortMode(saved) ? saved : DEFAULT_DIRECTORY_SORT_MODE;
  });
  const [directorySortOverrides, setDirectorySortOverrides] = useState<
    Record<string, DirectorySortMode>
  >(() => {
    if (typeof window === "undefined") {
      return {};
    }
    try {
      const saved = window.localStorage.getItem(
        DIRECTORY_SORT_OVERRIDES_STORAGE_KEY,
      );
      if (!saved) {
        return {};
      }
      const parsed = JSON.parse(saved) as Record<string, string>;
      return Object.fromEntries(
        Object.entries(parsed).filter(([, value]) =>
          isDirectorySortMode(value),
        ),
      ) as Record<string, DirectorySortMode>;
    } catch {
      return {};
    }
  });
  /* 冷启动落点（2026-10-03 用户决策，取代 R10 的「恢复上次模式」）：
     **有会话 id 进那个对话，否则落工作台。** localStorage 与 URL 的 view 两条路都会
     把「上次用的模式」带进冷启动：mindfs-main-view 每次切换都写，view 也被
     replaceURLState 每次导航抄进地址栏。所以「兜底值是 workspace」从来不是启动行为，
     只是缺省时的读数 —— 唯一的启动指令是 URL 的 session，不是 view。
     用户在会话内切到文件/对话仍然完全正常，只是不跨冷启动继承。 */
  const mainViewRef = useRef<MainViewMode>(mainView);
  // 从 chat 返回时回到上一个非 chat 模式（瞬态，不落盘）。初值与冷启动落点一致。
  const lastNonChatViewRef = useRef<MainViewMode>("workspace");
  const [status, setStatus] = useState<WSStatus>("disconnected");
  const [file, setFile] = useState<FilePayload | null>(null);
  const currentFileEditing = useSyncExternalStore(fileEditStore.subscribe, () => !!file?.root && fileEditStore.has(file.root, file.path));
  const [viewerSelection, setViewerSelection] =
    useState<ViewerSelection | null>(null);
  const [attachedFileContext, setAttachedFileContext] =
    useState<AttachedFileContext | null>(null);
  const [pluginVersion, setPluginVersion] = useState(0);
  const [pluginLoading, setPluginLoading] = useState(false);
  const [pendingPluginTrust, setPendingPluginTrust] = useState<{
    rootId: string;
    bundle: PluginSourceBundle;
  } | null>(null);
  const [pluginBypass, setPluginBypass] = useState(false);
  const [pluginQuery, setPluginQuery] = useState<Record<string, string>>(
    () => readURLState().pluginQuery,
  );
  const pluginQueryRef = useRef<Record<string, string>>(
    readURLState().pluginQuery,
  );
  // 同上：返回值会被下游层与 JSX 组件按字面量 true 接收，不能拓宽成 boolean。
  const showHiddenFiles: true = true;
  const [projectTreeTabRequest, setProjectTreeTabRequest] = useState<{
    tab: ProjectTreeTab;
    nonce: number;
  } | null>(null);
  const [projectTreeTab, setProjectTreeTab] = useState<ProjectTreeTab>("files");
  const {
    worktreeItemsByRoot,
    worktreeLoadingByRoot,
    worktreeErrorByRoot,
    expandedWorktreeByRoot,
    worktreeStatusByPath,
    worktreeStatusLoadingByPath,
    loadProjectTreeWorktrees,
    loadProjectTreeWorktreeStatus,
    toggleProjectTreeWorktree,
    expandProjectTreeWorktree,
    refreshProjectTreeWorktrees,
    refreshTaskWorktree,
  } = useProjectTreeWorktrees({
    currentRootId,
    currentRootIdRef,
    projectTreeTab,
    scopedRootKey,
    getNodeIdForRoot,
    knownTaskWorktreePathsRef,
  });
  // 把后置初始化的 refreshTaskWorktree 转发给 useTaskBoard（通过 ref 避免 TDZ）
  refreshTaskWorktreeRef.current = refreshTaskWorktree;
  const [updateState, setUpdateState] = useState<UpdateState>(() =>
    normalizeUpdateState(null),
  );
  const [updateSubmitting, setUpdateSubmitting] = useState(false);


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
    multiProjectLoadSeqRef, multiProjectPendingByKey, multiProjectPendingRef, multiProjectSessionGroups,
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
    setMultiProjectPendingByKey, setMultiProjectSessionGroups, setMultiProjectSessionsLoading, setOnboardingMainContentViewRoot,
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
