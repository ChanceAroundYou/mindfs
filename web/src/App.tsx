import React, {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { normalizePathForRoot } from "./services/fileNavigation";
import { getViewModeSystemPrompt } from "./renderer/viewCatalog";
import { Renderer } from "./renderer/Renderer";
import {
  clearCachedSessionsForRoot,
  clearWindowedView,
  deleteCachedSession,
  getCachedMultiRootSessionList,
  getCachedSession,
  getCachedSessionList,
  getSessionWindow,
  saveCachedMultiRootSessionList,
  saveCachedSessionList,
  sessionService,
  setCachedSessionRelatedFiles,
  setWindowedView,
  SESSION_WINDOW_SIZE,
  syncSession,
  type MultiRootSessionGroup,
  type SyncSessionResult,
  type RelatedFile,
  type RelatedWorktree,
  type Session,
  type TokenUsage,
  type QueuedUserMessage,
} from "./services/session";
import { buildClientContext } from "./services/context";
import { e2eeService, type E2EEState } from "./services/e2ee";
import {
  bootstrapService,
  type BootstrapState,
} from "./services/bootstrap";
import { syncNativeReplyPollerE2EE } from "./services/replyPoller";
import {
  ProtectedAPIError,
  protectedAPIReady,
  protectedJSON as apiProtectedJSON,
  withNodeRetry,
} from "./services/api";
import { reportError } from "./services/error";
import {
  loadSendShortcut,
  persistSendShortcut,
  type SendShortcut,
} from "./services/sendShortcut";
import {
  loadFontSizePreferences,
  persistFontSizePreferences,
  type FontSizePreferences,
} from "./services/fontSize";
import {
  fetchFile,
  clearFileCacheForRoot,
  clearFileMemoryCacheForView,
  getCachedFile,
  invalidateFileCache,
  type FilePayload,
} from "./services/file";
import { getRootNodeId, setRootNodeId, setRootNodeMap } from "./services/rootNode";
import { scopeKey, scopeSessionKey, treeKey, expandKey, dirSelKey } from "./services/scope";
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
} from "./services/directorySort";
import { isUploadAbortError, uploadFiles, type UploadProgress } from "./services/upload";
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
import { appPath, appURL } from "./services/base";
import { useRefreshSpin } from "./hooks";
import { copyText } from "./services/clipboard";
import { triggerUpdate, type UpdateState } from "./services/update";
import {
  cancelScheduledWebViewCacheClear,
  scheduleWebViewCacheClearOnNextLaunch,
} from "./services/nativeCacheControl";
import {
  applyPinnedSnapshotToSessions,
  mergeSessionItems,
} from "./services/sessionListMerge";
import { currentUser } from "./services/authGate";
// 直接导入标准组件
import { AppShell } from "./layout/AppShell";
import { ModeIcon } from "./components/ModeIcon";
import {
  FileTree,
  type AgentConfigSwitchRequest,
  type ProjectTreeTab,
} from "./components/FileTree";

import { applyNodesFromServer, getActiveNode, getActiveNodeId, getNodeById, getNodes, migrateLegacySingleBase, setActiveNodeId, syncNodesFromServer } from "./services/nodeRegistry";

import { FileViewer } from "./components/FileViewer";
import { resolveGroupColor } from "./services/sessionGroupDisplay";
import { GitDiffViewer } from "./components/GitDiffViewer";
import { GitStatusPanel } from "./components/GitStatusPanel";
import { RootGitContentView } from "./components/RootGitContentView";
import { RootRelatedContentView } from "./components/RootRelatedContentView";
import { RootWorktreeContentView } from "./components/RootWorktreeContentView";
import { SessionViewer } from "./components/SessionViewer";
import { TaskBoardView } from "./components/TaskBoardView";
import { DefaultListView, type MainContentViewMode } from "./components/DefaultListView";
import { type ProjectSessionGroup } from "./components/SessionList";
import { InlineTokenText } from "./components/InlineTokenText";
import { ActionBar } from "./components/ActionBar";
import { CompactUploadProgress } from "./components/CompactUploadProgress";
import { ToastContainer } from "./components/Toast";
import { DialogHost } from "./components/DialogHost";
import { alertDialog, confirmDialog, promptDialog } from "./services/dialog";
import { BottomSheet } from "./components/BottomSheet";
import { ScheduledAgentTaskDialog } from "./components/ScheduledAgentTaskDialog";
import { TaskTemplateDialog, FieldLabelWithInfo } from "./components/TaskTemplateDialog";
import { TaskDetailPanel } from "./components/TaskDetailPanel";
import { MainViewSwitcher } from "./components/MainViewSwitcher";
import { OnboardingTour } from "./components/OnboardingTour";
import { WorktreeBranchSelector } from "./components/WorktreeBranchSelector";
import { NoWorktreeIcon } from "./components/NoWorktreeIcon";
import { renderToolIcon } from "./components/stream/ToolCallCard";
import TokenEditor, { type TokenEditorHandle } from "./components/editor/TokenEditor";
import { PromptEditor } from "./components/PromptEditor";
import { StageEditor } from "./components/StageEditor";
import { Select } from "./components/Select";
import { PanelShell, panelIconButtonStyle, CloseGlyph } from "./components/PanelShell";
import { composerInputStyle } from "./components/action/composerStyles";
import {
  type GitHubImportState,
  type LocalDirBrowserState,
  ProjectAddPopover,
  type ProjectAddMode,
} from "./components/ProjectAddPopover";
import { fetchAgents, restartAgent, type AgentStatus } from "./services/agents";
import { fetchCandidates, type CandidateItem } from "./services/candidates";
import {
  createTask,
  deleteTaskTemplate,
  fetchTaskDetails,
  fetchTaskTemplates,
  getCachedTaskDetails,
  getCachedTaskMeta,
  moveTask,
  pruneCachedTaskDetails,
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
} from "./services/onboarding";

// 类型定义

import { APP_DOCUMENT_TITLE, AppProps, CHILD_SESSION_PAGE_SIZE, MULTI_PROJECT_SESSION_LIMIT, ManagedRootPayload, SESSION_PAGE_SIZE } from "./app/appMisc";
import { MainViewMode, URLState } from "./app/appPath";
import { AttachedFileContext, Exchange, GitFileStat, MultiProjectSessionGroup, PendingSend, RelatedFileClickTarget, SessionItem, SessionMode, SessionQueueItem, SlashCommandResult, ViewerSelection, WSStatus } from "./app/appSession";
import { CANDIDATE_FETCH_DEBOUNCE_MS, DIRECTORY_SORT_OVERRIDES_STORAGE_KEY, GIT_DIFF_SIDE_BY_SIDE_STORAGE_KEY, GIT_HISTORY_EXPANDED_STORAGE_KEY, GIT_STATUS_EXPANDED_STORAGE_KEY, LAST_ROOT_NODE_STORAGE_KEY, LAST_ROOT_STORAGE_KEY, loadWorkspaceCollapsed, loadWorkspaceFilter, MAIN_VIEW_STORAGE_KEY, MOBILE_ENTER_KEY_SEND_STORAGE_KEY, PLUGIN_QUERY_STORAGE_PREFIX, saveWorkspaceCollapsed, saveWorkspaceFilter, SIDEBARS_SWAPPED_STORAGE_KEY, TASK_TEMPLATE_ALL_FILTER, TASK_TEMPLATE_SELECTION_STORAGE_KEY, TREE_SORT_STORAGE_KEY, type WorkspaceBoardFilter } from "./app/appStorage";
import { TaskInlineEditState } from "./app/appTask";
import { buildMatchInputFromPath, buildMessageWithViewContext, hasExplicitFileContext, indexManagedRoots, inferReadModeFromPlugin, managedDirAddErrorMessage, mapManagedRootsToEntries, normalizeUpdateState, shouldShowUpdateButton, toPluginInput, updateButtonLabel, updateSummaryText, useResponsive, waitForNextPaint } from "./app/appMisc";
import { basenameOfPath, buildDirectorySelectionKey, buildFileScrollKey, buildURLSearch, comparableManagedRootPath, dirnameOfPath, isDirectorySortMode, joinDisplayPath, normalizeCursor, normalizePath, parentDirsOfFile, parseFileLocation, parsePluginQuery, readURLState, relativeDisplayPathFromRoot, rootNodeKey } from "./app/appPath";
import { useWorkspaceBoard } from "./app/useWorkspaceBoard";
import { hasSessionExchanges, isTopLevelSessionItem, normalizeMode, relatedFileSelectionKey, sessionInputHistory, toSessionItem } from "./app/appSession";
import { accountScopedKey, loadGitDiffSideBySide, loadLastRootId, loadLastRootNodeId, loadLegacyMainView, loadMainView, loadMobileEnterKeySends, loadPersistedFileScrollPositions, loadPersistedPluginQuery, loadSidebarsSwapped, loadTaskCreateWorktreePreference, persistFileScrollPositions, persistPluginQuery, removeLocalStorageByPrefix, saveTaskCreateWorktreePreference } from "./app/appStorage";
import { applyStageOverride, currentTaskInputFromDetail, DEFAULT_TASK_AGENT, DEFAULT_TASK_MODEL, firstAgentStage, firstTaskInputFromDetail, firstUserInputTemplate, isTerminalKanbanTask, latestTaskStageRun, normalizeFastService, parseTaskSessionErrorDetails, parseTaskSessionErrorMessage, previousTaskInputsFromDetail, taskSessionKeysFromDetail, taskStatusLabel } from "./app/appTask";
import { useCompletionSound } from "./app/useCompletionSound";
import { useExternalSessionImport } from "./app/useExternalSessionImport";
import { useGitActions } from "./app/useGitActions";
import { useSessionSearch } from "./app/useSessionSearch";
import { useGitData } from "./app/useGitData";
import { useProjectTreeWorktrees } from "./app/useProjectTreeWorktrees";
import { useProjectLifecycle } from "./app/useProjectLifecycle";
import { useSessionSidebarView } from "./app/useSessionSidebarView";
import { useSessionStreamCache } from "./app/useSessionStreamCache";
import { useRealtimeEvents } from "./app/useRealtimeEvents";
import { useTaskTemplates } from "./app/useTaskTemplates";
import { CheckIconSmall, PlusSmallIcon, taskCardIconButtonStyle } from "./app/taskIcons";


export function App({ onGoHome }: AppProps) {
  const { t } = useI18n();
  const { playCompletionSound } = useCompletionSound();
  const pluginManagerRef = useRef<PluginManager>(new PluginManager());
  const managedRootIdsRef = useRef<Set<string>>(new Set());
  const expandedRef = useRef<string[]>([]);
  const selectedDirRef = useRef<string | null>(null);
  const fileRef = useRef<FilePayload | null>(null);
  const selectedSessionRef = useRef<SessionItem | null>(null);
  const lastMainSessionSnapshotRef = useRef<Session | null>(null);
  const sessionSearchTargetCounterRef = useRef(0);
  const currentSessionRef = useRef<SessionItem | null>(null);
  const interactionModeRef = useRef<"main" | "drawer">("main");
  const pendingDraftRef = useRef<PendingSend | null>(null);
  const pendingBySessionRef = useRef<Record<string, PendingSend>>({});
  const pendingRequestRef = useRef<Record<string, PendingSend>>({});
  const queuedMessagesBySessionRef = useRef<Record<string, SessionQueueItem[]>>({});
  const queueFrozenBySessionRef = useRef<Record<string, boolean>>({});
  const optimisticDequeuedIdsRef = useRef<Record<string, Set<string>>>({});
  const cancelRequestedBySessionRef = useRef<Record<string, boolean>>({});
  const sessionCacheRef = useRef<Record<string, Session>>({});
  const loadedSessionRef = useRef<Record<string, boolean>>({});
  const loadingSessionRef = useRef<Partial<Record<string, Promise<SyncSessionResult>>>>({});
  const staleSessionKeysRef = useRef<Set<string>>(new Set());
  const invalidTreeCacheKeysRef = useRef<Set<string>>(new Set());
  // 上一轮跨节点抓取里失败的节点。refreshManagedRoots 消费它：提示用户 + 给出重试入口。
  // 跨节点失败原本被静默吞成 []，整个节点凭空缺席且无人重拉（实测可长时间不恢复）。
  const nodeLoadFailuresRef = useRef<Array<{ id: string; name: string }>>([]);
  const notifiedNodeFailuresRef = useRef<Set<string>>(new Set());
  const notifyNodeLoadFailedRef = useRef<(node: { id: string; name: string }) => void>(() => {});
  // 目录树刷新并发守卫：按缓存键记录最后发起的请求序号，旧响应后到时直接丢弃
  const treeFetchSeqRef = useRef<Record<string, number>>({});
  const boundSessionByRootRef = useRef<Record<string, string | null>>({});
  const suppressedAutoBindSessionByRootRef = useRef<Record<string, string | null>>({});
  const drawerSessionByRootRef = useRef<Record<string, SessionItem | null>>({});
  const selectedSessionByRootRef = useRef<Record<string, string | null>>({});
  const drawerOpenByRootRef = useRef<Record<string, boolean>>({});
  const drawerScrollRef = useRef<HTMLDivElement | null>(null);
  const fileCursorRef = useRef<number>(0);
  const fileScrollPositionsRef = useRef<Record<string, number>>(
    loadPersistedFileScrollPositions(),
  );
  const pluginContentRef = useRef<HTMLDivElement | null>(null);
  const lastPluginChapterRef = useRef<string>("");
  const viewerSelectionRef = useRef<ViewerSelection | null>(null);
  const lastViewerSelectionRef = useRef<ViewerSelection | null>(null);
  const dismissedSelectionFileRef = useRef<string | null>(null);
  const lastPluginResetFileKeyRef = useRef<string>("");
  const pluginBypassRef = useRef<boolean>(false);
  const fileOpenRequestRef = useRef(0);
  const fullUpgradeAttemptRef = useRef("");
  const pluginsLoadedByRootRef = useRef<Record<string, boolean>>({});
  const pluginsLoadingByRootRef = useRef<Record<string, Promise<void>>>({});
  const pluginsTrustPendingByRootRef = useRef<Record<string, boolean>>({});
  const didInitRef = useRef(false);
  const managedRootsRequestRef = useRef<Promise<ManagedRootPayload[] | null> | null>(null);
  const handleSelectSessionRef = useRef<
    | ((
        session: any,
        options?: { preserveTaskSelection?: boolean; preserveMainView?: boolean },
      ) => Promise<void>)
    | null
  >(null);

  const [sessions, setSessions] = useState<SessionItem[]>([]);
  const sessionsRef = useRef<SessionItem[]>([]);
  const multiProjectSessionsEnabled = true;
  const [multiProjectSessionGroups, setMultiProjectSessionGroups] = useState<MultiProjectSessionGroup[]>([]);
  const [multiProjectSessionsLoading, setMultiProjectSessionsLoading] = useState(false);
  // 多项目列表全量重拉竞态守卫：只应用最后一次发起的请求结果，避免旧响应覆盖新数据
  const multiProjectLoadSeqRef = useRef(0);
  const [multiProjectPendingByKey, setMultiProjectPendingByKey] = useState<Record<string, boolean>>({});
  const multiProjectPendingRef = useRef<Record<string, boolean>>({});
  const [syncingSessionKeys, setSyncingSessionKeys] = useState<Set<string>>(
    () => new Set(),
  );
  const [hasMoreSessions, setHasMoreSessions] = useState(false);
  const [loadingOlderSessions, setLoadingOlderSessions] = useState(false);
  const [sessionListMode, setSessionListMode] = useState<"local" | "import">(
    "local",
  );
  const [availableAgents, setAvailableAgents] = useState<AgentStatus[]>([]);
  const [scheduledAgentDialogOpen, setScheduledAgentDialogOpen] = useState(false);
	  const [kanbanTasks, setKanbanTasks] = useState<KanbanTask[]>([]);
	  const [kanbanTaskCountItems, setKanbanTaskCountItems] = useState<KanbanTask[]>([]);
	  const [taskDetailsById, setTaskDetailsById] = useState<Record<string, TaskDetail>>({});
	  const [taskFirstInputById, setTaskFirstInputById] = useState<Record<string, string>>({});
	  const [taskSessionKeysById, setTaskSessionKeysById] = useState<Record<string, string[]>>({});
	  const [taskRelatedFilesById, setTaskRelatedFilesById] = useState<Record<string, RelatedFile[]>>({});
	  const taskDetailsByIdRef = useRef<Record<string, TaskDetail>>({});
	  const taskSessionKeysByIdRef = useRef<Record<string, string[]>>({});
	  const [selectedKanbanTaskId, setSelectedKanbanTaskId] = useState("");
	  const [expandedTaskInputIds, setExpandedTaskInputIds] = useState<Set<string>>(() => new Set());
  const [collapsedTaskCompletionGroups, setCollapsedTaskCompletionGroups] = useState<Set<string>>(() => new Set());
  // 看板列折叠（移动端多列换行后空间宝贵，长列默认可收起只留表头）。
  const [collapsedKanbanColumns, setCollapsedKanbanColumns] = useState<Set<string>>(() => new Set());
  const [taskInlineEdit, setTaskInlineEdit] = useState<TaskInlineEditState | null>(null);
  const [taskSessionErrorDialog, setTaskSessionErrorDialog] = useState<{ title: string; message: string; details: string[] } | null>(null);
  const [taskInlineActiveToken, setTaskInlineActiveToken] = useState<{ type: "file" | "slash" | "prompt" | "command"; query: string } | null>(null);
  const [taskInlineCandidates, setTaskInlineCandidates] = useState<CandidateItem[]>([]);
  const [taskInlineCandidateIndex, setTaskInlineCandidateIndex] = useState(0);
  const [taskInlineSaving, setTaskInlineSaving] = useState(false);
  // 新建任务面板里「用户输入模板」字段说明的展开态（与模板编辑面板同一套组件）。
  const [taskInlineHelpKey, setTaskInlineHelpKey] = useState("");
  const [taskInlineUploadProgress, setTaskInlineUploadProgress] = useState<UploadProgress | null>(null);
	  const [directoryUploadProgress, setDirectoryUploadProgress] = useState<UploadProgress | null>(null);
	  const [taskWorktreeBranches, setTaskWorktreeBranches] = useState<GitBranchesPayload>({ branches: [] });
	  const [taskWorktreeBranchesLoading, setTaskWorktreeBranchesLoading] = useState(false);
	  const [taskWorktreeBranchError, setTaskWorktreeBranchError] = useState("");
	  const [kanbanTasksLoading, setKanbanTasksLoading] = useState(false);
	  const kanbanLoadSeqRef = useRef(0);
	  const kanbanAbortRef = useRef<AbortController | null>(null);
	  const sessionListLoadSeqRef = useRef(0);
	  const sessionListAbortRef = useRef<AbortController | null>(null);
  // 列表拉取失败告警冷却：断网/抖动时重拉被多个事件反复触发，避免告警刷屏。
  const listLoadErrorAtRef = useRef(0);
  const taskInlineEditorRef = useRef<TokenEditorHandle | null>(null);
  const taskInlineCandidateItemRefs = useRef<Array<HTMLButtonElement | null>>([]);
  const taskInlineAttachmentInputRef = useRef<HTMLInputElement | null>(null);
	  const taskInlineUploadAbortRef = useRef<AbortController | null>(null);
	  const directoryUploadAbortRef = useRef<AbortController | null>(null);

	  useEffect(() => {
	    taskDetailsByIdRef.current = taskDetailsById;
	  }, [taskDetailsById]);

	  useEffect(() => {
	    taskSessionKeysByIdRef.current = taskSessionKeysById;
	  }, [taskSessionKeysById]);
  const knownTaskWorktreePathsRef = useRef<Set<string>>(new Set());
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
  const slashCopyResetTimersRef = useRef<Record<string, number>>({});
  const [queueVersion, setQueueVersion] = useState(0);
  const [interactionMode, setInteractionMode] = useState<"main" | "drawer">(
    "main",
  );
  const [agentsVersion, setAgentsVersion] = useState(0);
  const [codexRateLimitsRefreshToken, setCodexRateLimitsRefreshToken] = useState(0);
  const [isDrawerOpen, setIsDrawerOpen] = useState(false);
  const { isMobile, isTablet } = useResponsive();
  const [mobileEnterKeySends, setMobileEnterKeySends] = useState(loadMobileEnterKeySends);
  const [sendShortcut, setSendShortcut] = useState<SendShortcut | null>(loadSendShortcut);
  const [sidebarsSwapped, setSidebarsSwapped] = useState(loadSidebarsSwapped);
  const [fontSizePreferences, setFontSizePreferences] = useState<FontSizePreferences>(loadFontSizePreferences);
  const [gitDiffSideBySide, setGitDiffSideBySide] = useState(loadGitDiffSideBySide);
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
  } = useTaskTemplates({ currentRootId, scopedRootKey, getNodeIdForRoot });

  useEffect(() => {
    if (!taskInlineEdit) return;
    window.setTimeout(() => {
      taskInlineEditorRef.current?.setText(taskInlineEdit.text || "");
      // TokenEditor 的 setText 不再抢焦点（常驻挂载的编辑器会夺走整页焦点），开面板时自己聚焦。
      taskInlineEditorRef.current?.focus();
    }, 0);
  }, [taskInlineEdit?.templateId]);

  useEffect(() => {
    // 候选跟着面板的目标项目走，不跟着「当前选中项目」走 —— 工作台发起时两者不是一个。
    const tokenRootId = taskInlineEdit?.targetRootId || currentRootId;
    if (!taskInlineActiveToken || !tokenRootId) {
      setTaskInlineCandidates([]);
      setTaskInlineCandidateIndex(0);
      return;
    }
    const controller = new AbortController();
    const timer = window.setTimeout(() => {
      const selectedTemplate = taskTemplates.find((template) => template.id === taskTemplateFilter) || null;
      const agent = firstAgentStage(selectedTemplate)?.agent || "";
      fetchCandidates({
        rootId: tokenRootId,
        type: taskInlineActiveToken.type === "file"
          ? "file"
          : taskInlineActiveToken.type === "prompt"
            ? "prompt"
            : taskInlineActiveToken.type === "command"
              ? "command"
              : "skill",
        query: taskInlineActiveToken.query,
        agent: taskInlineActiveToken.type === "slash" ? agent : undefined,
        signal: controller.signal,
        nodeId: getNodeIdForRoot(tokenRootId),
      })
        .then((items) => {
          setTaskInlineCandidates(items);
          setTaskInlineCandidateIndex(0);
        })
        .catch((err) => {
          if (controller.signal.aborted) return;
          console.error("Failed to fetch task candidates:", err);
          setTaskInlineCandidates([]);
          setTaskInlineCandidateIndex(0);
        });
    }, CANDIDATE_FETCH_DEBOUNCE_MS);
    return () => {
      window.clearTimeout(timer);
      controller.abort();
    };
  }, [currentRootId, taskInlineEdit?.targetRootId, taskInlineActiveToken, taskTemplateFilter, taskTemplates]);

  const applyTaskDetails = useCallback((rootId: string, details: TaskDetail[], persist = true) => {
    const curNid = String(currentRootNodeIdRef.current || "").trim();
    const curRoot = String(currentRootIdRef.current || "");
    // 节点隔离：当前根的旧节点数据不污染视图（旧请求迟到或 WS 跨节点）
    // 不直接依赖 getNodeIdForRoot（前向引用），改用 ref + 回退表
    const sid = rootId === curRoot ? curNid : String((managedRootByIdRef.current as Record<string, any>)[rootId]?._nodeId || getRootNodeId(rootId) || "").trim();
    if (sid && curNid && sid !== curNid && rootId === curRoot) {
      const filteredByNode = details.filter((d) => {
        const did = String((d.task as any)?._nodeId || "").trim();
        return !did || did === sid;
      });
      if (filteredByNode.length === 0) return;
      details = filteredByNode;
    }
    const accepted = details.filter(
      (detail) =>
        detail?.task?.id &&
        shouldApplyTaskDetail(taskDetailsByIdRef.current[detail.task.id], detail),
    );
    if (accepted.length === 0) return;
    const nextSnapshot = { ...taskDetailsByIdRef.current };
    accepted.forEach((detail) => {
      nextSnapshot[detail.task.id] = detail;
    });
    taskDetailsByIdRef.current = nextSnapshot;
    setTaskDetailsById((prev) => {
      const next = { ...prev };
      accepted.forEach((detail) => {
        if (shouldApplyTaskDetail(next[detail.task.id], detail)) {
          next[detail.task.id] = detail;
        }
      });
      return next;
    });
    setTaskFirstInputById((prev) => {
      const next = { ...prev };
      accepted.forEach((detail) => {
        next[detail.task.id] = firstTaskInputFromDetail(detail);
      });
      return next;
    });
    setTaskSessionKeysById((prev) => {
      const next = { ...prev };
      accepted.forEach((detail) => {
        next[detail.task.id] = taskSessionKeysFromDetail(detail);
      });
      return next;
    });
    setKanbanTaskCountItems((prev) => {
      const byId = new Map(prev.map((task) => [task.id, task]));
      accepted.forEach((detail) => byId.set(detail.task.id, detail.task));
      return Array.from(byId.values()).sort((a, b) => String(b.updated_at || "").localeCompare(String(a.updated_at || "")));
    });
    if (persist) {
      const persistNode = String(currentRootNodeIdRef.current || "").trim() || String((managedRootByIdRef.current as Record<string, any>)[rootId]?._nodeId || getRootNodeId(rootId) || "").trim() || undefined;
      void upsertCachedTaskDetails(rootId, accepted, persistNode);
    }
  }, []);

  /**
   * 把「服务端全量响应里已经没有」的任务从内存里摘掉。
   *
   * 缓存以前只写不删，内存这边同样只有合并（applyTaskDetails）没有移除，于是
   * 服务端删掉的任务会被一直渲染。必须真的 delete 键：kanbanTasks 和
   * kanbanTaskCountItems 都是从 taskDetailsById 派生的，只过滤数组不改 map 的话
   * 下一轮派生又会把它们带回来。
   */
  const pruneTaskDetails = useCallback((rootId: string, keepTaskIds: Iterable<string>) => {
    const keep = new Set(Array.from(keepTaskIds, (id) => String(id || "")).filter(Boolean));
    // taskDetailsById 是跨 root 共享的，权威集合只覆盖本次拉取的那个 root ——
    // 不按 root_id 圈定的话，刷新 A 项目会把 B 项目的任务一起清掉。
    const inScope = (task: KanbanTask) => task.root_id === rootId;
    const dropped = Object.entries(taskDetailsByIdRef.current)
      .filter(([taskId, detail]) => !keep.has(String(taskId)) && inScope((detail as TaskDetail).task))
      .map(([taskId]) => taskId);
    if (dropped.length === 0) return;
    const droppedSet = new Set(dropped);
    const kept = <V,>(entries: [string, V][]) =>
      Object.fromEntries(entries.filter(([taskId]) => !droppedSet.has(taskId))) as Record<string, V>;
    taskDetailsByIdRef.current = kept(Object.entries(taskDetailsByIdRef.current));
    setTaskDetailsById((prev) => kept(Object.entries(prev)));
    // 这两张表只有 taskId → 值，没有 root 信息可判归属，所以直接吃上面算好的
    // 淘汰名单：跟着 taskDetailsById 一起摘掉，不自己再推导一遍。
    setTaskFirstInputById((prev) => kept(Object.entries(prev)));
    setTaskSessionKeysById((prev) => kept(Object.entries(prev)));
    setKanbanTaskCountItems((prev) => {
      const next = prev.filter((task) => !inScope(task) || keep.has(String(task.id)));
      return next.length === prev.length ? prev : next;
    });
  }, []);

  const loadKanbanTasks = useCallback(async (rootId?: string | null, force = false) => {
    if (!protectedAPIReady()) {
      return;
    }
    const targetRoot = rootId || currentRootIdRef.current;
    if (!targetRoot) {
      setKanbanTasks([]);
      setKanbanTaskCountItems([]);
      setTaskDetailsById({});
      setTaskFirstInputById({});
      setTaskSessionKeysById({});
      return;
    }
    // 不直接依赖 getNodeIdForRoot（前向引用），内联解析避免 TSC 提前引用
    const resolveNodeId = (rid: string): string | undefined => {
      const r = String(rid || "").trim();
      if (!r) return undefined;
      if (r === String(currentRootIdRef.current || "")) {
        const nid = String(currentRootNodeIdRef.current || "").trim();
        if (nid) return nid;
      }
      const entry = (managedRootByIdRef.current as Record<string, any>)[r];
      const enid = String(entry?._nodeId || "").trim();
      if (enid) return enid;
      return String(getRootNodeId(r) || "").trim() || undefined;
    };
    const snapNode = resolveNodeId(targetRoot) || null;
    const seq = ++kanbanLoadSeqRef.current;
    kanbanAbortRef.current?.abort();
    const controller = new AbortController();
    kanbanAbortRef.current = controller;
    setKanbanTasksLoading(true);
    try {
      const cached = await getCachedTaskDetails(targetRoot, snapNode || undefined);
      if (cached.length > 0 && seq === kanbanLoadSeqRef.current && !controller.signal.aborted && (resolveNodeId(targetRoot) || null) === snapNode) {
        applyTaskDetails(targetRoot, cached, false);
        if (seq === kanbanLoadSeqRef.current) setKanbanTasksLoading(false);
      }
      const meta = await getCachedTaskMeta(targetRoot, snapNode || undefined);
      const [details, recent] = await Promise.all([
        fetchTaskDetails(targetRoot, force ? undefined : { after: meta?.newestUpdatedAt || "" }, resolveNodeId(targetRoot)),
        !force && meta?.newestUpdatedAt
          ? fetchTaskDetails(targetRoot, { limit: 20 }, resolveNodeId(targetRoot))
          : Promise.resolve([] as TaskDetail[]),
      ]);
      if (controller.signal.aborted || seq !== kanbanLoadSeqRef.current) { return; }
      if ((resolveNodeId(targetRoot) || null) !== snapNode) { return; }
      const tagDetails = (arr: TaskDetail[]) => { for (const d of arr) (d.task as any)._nodeId = (d.task as any)._nodeId || snapNode; };
      if (details.length > 0) { tagDetails(details); applyTaskDetails(targetRoot, details); }
      if (recent.length > 0) { tagDetails(recent); applyTaskDetails(targetRoot, recent); }
      // 只有全量响应才是权威集合：增量只含更新的行、限流只含前 20 条，
      // 拿它们去淘汰会把「没被这次响应覆盖到」的任务误删。
      if (force) {
        const keepTaskIds = details.map((d) => d.task?.id);
        pruneTaskDetails(targetRoot, keepTaskIds);
        void pruneCachedTaskDetails(targetRoot, keepTaskIds, snapNode || undefined);
      }
    } catch (err) {
      if ((err as any)?.name === "AbortError") return;
      if (kanbanAbortRef.current?.signal.aborted) return;
      reportError("file.write_failed", String((err as Error)?.message || t("task.loadFailed")));
    } finally {
      if (seq === kanbanLoadSeqRef.current && !controller.signal.aborted) setKanbanTasksLoading(false);
    }
  }, [applyTaskDetails, pruneTaskDetails, t]);

  // 刷新必须走全量：增量响应在结构上就看不见「少了谁」，删除永远刷不掉。
  const kanbanRefreshSpin = useRefreshSpin(() => loadKanbanTasks(currentRootId, true));

	  useEffect(() => {
	    void loadKanbanTasks(currentRootId);
	  }, [currentRootId, currentRootNodeId, loadKanbanTasks]);

	  useEffect(() => {
	    const curNid = String(currentRootNodeIdRef.current || "").trim();
	    const allTasks = Object.values(taskDetailsById)
	      .map((detail) => detail.task)
	      .filter((task) => {
	        if (currentRootId && task.root_id !== currentRootId) return false;
	        const tid = String((task as any)._nodeId || "").trim();
	        if (tid && curNid && tid !== curNid) return false;
	        return true;
	      })
	      .sort((a, b) => String(b.updated_at || "").localeCompare(String(a.updated_at || "")));
	    const selectedTemplateId = taskTemplateFilter || "";
	    const allTemplatesSelected = selectedTemplateId === TASK_TEMPLATE_ALL_FILTER;
	    const filtered = selectedTemplateId && !allTemplatesSelected
	      ? allTasks.filter((task) => task.task_template_id === selectedTemplateId)
	      : allTasks;
	    setKanbanTaskCountItems(allTasks);
	    // 模板筛选只按 task_template_id 收窄，不要在这里再滤终态：
	    // 四块看板里「已结束」那一列唯一的任务来源就是 success/fail/cancelled，
	    // 前面多滤一道，终态卡在子看板里就凭空消失（连已完成列都进不去）。
	    setKanbanTasks(filtered);
	  }, [currentRootId, taskDetailsById, taskTemplateFilter]);

	  useEffect(() => {
	    if (!selectedKanbanTaskId) return;
	    if (kanbanTasks.some((task) => task.id === selectedKanbanTaskId)) return;
	    // 工作台上选中的任务往往属于**别的**项目，而 kanbanTasks 按 currentRootId 过滤，
	    // 天然不含它们 —— 不放行的话，刚弹出来的详情会被这个守卫立刻关掉。
	    // 详情已经在 taskDetailsById 里（openWorkspaceTaskDetail 灌的），选中是合法的。
	    if (workspaceOpenRef.current && taskDetailsByIdRef.current[selectedKanbanTaskId]) return;
	    setSelectedKanbanTaskId("");
	  }, [kanbanTasks, selectedKanbanTaskId]);

	  // 跨项目工作台状态与逻辑见 kanbanTaskPanel 定义前（workspaceOpen 等）

  const handleMoveKanbanTask = useCallback(async (task: KanbanTask, action: "next" | "run-now" | "pause" | "resume" | "complete" | "cancel") => {
    const rootId = task.root_id || currentRootIdRef.current;
    if (!rootId) return;
    let reason = "";
    if (action === "pause") {
      const input = await promptDialog({ message: t("task.reasonPrompt", { action: t("task.actionPause") }) });
      if (input === null) {
        return;
      }
      reason = input.trim();
    }
    try {
      const detail = await moveTask(rootId, task.id, action, reason, getNodeIdForRoot(rootId));
      applyTaskDetails(rootId, [detail]);
      if (detail.task.worktree_path) {
        void refreshTaskWorktree(rootId, detail.task.worktree_path);
      }
    } catch (err) {
      reportError("file.write_failed", String((err as Error)?.message || t("task.actionFailed")));
    }
  }, [applyTaskDetails, t]);

  const loadTaskWorktreeBranches = useCallback(async (rootId: string) => {
    if (!rootId) return;
    setTaskWorktreeBranchesLoading(true);
    setTaskWorktreeBranchError("");
    try {
      setTaskWorktreeBranches(await fetchGitBranches(rootId, getNodeIdForRoot(rootId)));
    } catch (error) {
      setTaskWorktreeBranches({ branches: [] });
      setTaskWorktreeBranchError(error instanceof Error ? error.message : t("worktree.loadBranchFailed"));
    } finally {
      setTaskWorktreeBranchesLoading(false);
    }
  }, [t]);

	  // 分支列表跟的是**面板的目标项目**：从工作台发起时那不是 currentRootId，
	  // 照 currentRootId 拉会把别的项目的分支带进分支选择器。
	  useEffect(() => {
	    const edit = taskInlineEdit;
	    if (!edit) return;
	    const rootId = edit.targetRootId || currentRootId || "";
	    if (!edit.createWorktree || !edit.canToggleWorktree || !rootId || managedRootByIdRef.current[rootId]?.is_git_repo !== true) {
	      setTaskWorktreeBranchError("");
	      return;
	    }
	    void loadTaskWorktreeBranches(rootId);
	  }, [currentRootId, loadTaskWorktreeBranches, taskInlineEdit?.canToggleWorktree, taskInlineEdit?.createWorktree, taskInlineEdit?.targetRootId]);

	  useEffect(() => {
	    const edit = taskInlineEdit;
	    if (!edit || !edit.canToggleWorktree) return;
	    // 同样按目标项目存：从工作台发起时把 A 项目的偏好写到 B 项目上是错的。
	    const rootId = edit.targetRootId || currentRootIdRef.current || "";
	    if (!rootId) return;
	    saveTaskCreateWorktreePreference(rootId, {
	      createWorktree: edit.createWorktree,
	      worktreeBranchMode: edit.worktreeBranchMode,
	      worktreeBranch: edit.worktreeBranch,
	    });
	  }, [
	    taskInlineEdit?.canToggleWorktree,
	    taskInlineEdit?.createWorktree,
	    taskInlineEdit?.worktreeBranch,
	    taskInlineEdit?.worktreeBranchMode,
	    taskInlineEdit?.targetRootId,
	  ]);

	  const openTaskCreateDialog = useCallback((template: TaskTemplate | null, targetRootId?: string) => {
	    const templateId = template?.id || "";
	    if (!templateId) return;
	    const initialText = firstUserInputTemplate(template);
	    // 工作台发起时可以指定项目；看板入口不传，落在当前项目上。
	    const rootId = targetRootId || currentRootIdRef.current || "";
	    const taskCanCreateWorktree = managedRootByIdRef.current[rootId]?.is_git_repo === true;
	    const worktreePref = loadTaskCreateWorktreePreference(rootId);
	    setTaskInlineEdit({
	      templateId,
	      templateName: template?.name || t("task.defaultTitle"),
	      text: initialText,
	      targetRootId: targetRootId || undefined,
	      // 只有工作台来的才带项目下拉：那里没有「当前项目」这个默认落点。
	      allowProjectSwitch: Boolean(targetRootId),
	      createWorktreePerRoot: {
	        [rootId]: {
	          createWorktree: taskCanCreateWorktree && worktreePref.createWorktree,
	          worktreeBranchMode: worktreePref.worktreeBranchMode,
	          worktreeBranch: worktreePref.worktreeBranch,
	        },
	      },
	      name: "",
	      // 首段的「立即执行」：面板开出来就照模板的面板值。
	      startImmediately: template?.stages?.[0]?.snapshot?.start_immediately === true,
	      previousInputs: [],
	      createWorktree: taskCanCreateWorktree && worktreePref.createWorktree,
	      worktreeBranchMode: worktreePref.worktreeBranchMode,
	      worktreeBranch: worktreePref.worktreeBranch,
	      canToggleWorktree: true,
	      attachments: [],
	    });
    setTaskInlineActiveToken(null);
    setTaskInlineCandidates([]);
    setTaskInlineCandidateIndex(0);
  }, [t]);

  // 面板上换项目：worktree 三个开关是**按项目存**的，换回去要还原回这个项目那份，
  // 第一次切到某项目则从 localStorage 的偏好起头。切项目不碰正文/模板/附件。
  const switchTaskInlineEditProject = useCallback((nextRootId: string) => {
    setTaskInlineEdit((prev) => {
      if (!prev || !nextRootId || nextRootId === (prev.targetRootId || currentRootIdRef.current || "")) return prev;
      const prevRootId = prev.targetRootId || currentRootIdRef.current || "";
      const stash = (state: TaskInlineEditState, rootId: string) => ({
        createWorktree: state.createWorktree,
        worktreeBranchMode: state.worktreeBranchMode,
        worktreeBranch: state.worktreeBranch,
      });
      const perRoot = {
        ...(prev.createWorktreePerRoot || {}),
        [prevRootId]: stash(prev, prevRootId),
      };
      const restored = perRoot[nextRootId];
      const canCreate = managedRootByIdRef.current[nextRootId]?.is_git_repo === true;
      const pref = restored || (() => {
        const stored = loadTaskCreateWorktreePreference(nextRootId);
        return { createWorktree: canCreate && stored.createWorktree, worktreeBranchMode: stored.worktreeBranchMode, worktreeBranch: stored.worktreeBranch };
      })();
      return {
        ...prev,
        targetRootId: nextRootId,
        createWorktreePerRoot: { ...perRoot, [nextRootId]: pref },
        createWorktree: pref.createWorktree,
        worktreeBranchMode: pref.worktreeBranchMode,
        worktreeBranch: pref.worktreeBranch,
        canToggleWorktree: true,
      };
    });
  }, []);

  const closeTaskEditDialog = useCallback(() => {
    setTaskInlineEdit((prev) => {
      prev?.attachments.forEach((attachment) => {
        if (attachment.previewUrl) URL.revokeObjectURL(attachment.previewUrl);
      });
      return null;
    });
    setTaskInlineActiveToken(null);
    setTaskInlineCandidates([]);
    setTaskInlineCandidateIndex(0);
    setTaskInlineHelpKey("");
    setTaskInlineSaving(false);
    setTaskInlineUploadProgress(null);
    taskInlineUploadAbortRef.current?.abort();
    taskInlineUploadAbortRef.current = null;
  }, []);

  const applyTaskInlineCandidate = useCallback((candidate: CandidateItem) => {
    setTaskInlineCandidates([]);
    setTaskInlineCandidateIndex(0);
    taskInlineEditorRef.current?.insertCandidate(candidate.type, candidate.name);
    taskInlineEditorRef.current?.focus();
  }, []);

  useEffect(() => {
    if (taskInlineCandidates.length === 0) {
      taskInlineCandidateItemRefs.current = [];
      return;
    }
    const activeItem = taskInlineCandidateItemRefs.current[taskInlineCandidateIndex];
    activeItem?.scrollIntoView({ block: "nearest" });
  }, [taskInlineCandidates, taskInlineCandidateIndex]);

  const appendTaskInlineAttachments = useCallback((files: File[]) => {
    if (files.length === 0) return;
    setTaskInlineEdit((prev) => {
      if (!prev) return prev;
      return {
        ...prev,
        attachments: [
          ...prev.attachments,
          ...files.map((file) => ({
            id: `${file.name}-${file.size}-${file.lastModified}-${Math.random().toString(36).slice(2, 8)}`,
            file,
            isImage: file.type.startsWith("image/"),
            previewUrl: file.type.startsWith("image/") ? URL.createObjectURL(file) : undefined,
          })),
        ],
      };
    });
  }, []);

  const handleTaskInlineAttachmentChange = useCallback((event: React.ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(event.target.files || []);
    if (files.length > 0) {
      appendTaskInlineAttachments(files);
    }
    event.currentTarget.value = "";
  }, [appendTaskInlineAttachments]);


  const removeTaskInlineAttachment = useCallback((id: string) => {
    setTaskInlineEdit((prev) => prev
      ? {
          ...prev,
          attachments: prev.attachments.filter((attachment) => {
            const keep = attachment.id !== id;
            if (!keep && attachment.previewUrl) URL.revokeObjectURL(attachment.previewUrl);
            return keep;
          }),
        }
      : prev);
  }, []);

  const saveTaskInlineEdit = useCallback(async () => {
    const edit = taskInlineEdit;
    const rootId = edit?.targetRootId || currentRootIdRef.current;
    if (!edit || !rootId) return;
    setTaskInlineSaving(true);
    setTaskInlineUploadProgress(null);
    try {
      let attachmentTokens = "";
      if (edit.attachments.length > 0) {
        const uploadAbort = new AbortController();
        taskInlineUploadAbortRef.current = uploadAbort;
        const uploaded = await uploadFiles({
          rootId,
          files: edit.attachments.map((attachment) => attachment.file),
          onProgress: setTaskInlineUploadProgress,
          signal: uploadAbort.signal,
          nodeId: getNodeIdForRoot(rootId),
        });
        attachmentTokens = uploaded.map((file) => `[file: ${file.agent_path || file.path}]`).join("\n");
      }
      const payload = [edit.text.trim(), attachmentTokens].filter(Boolean).join("\n");
      const taskCanCreateWorktree = managedRootByIdRef.current[rootId]?.is_git_repo === true;
      const createWorktree = taskCanCreateWorktree && edit.createWorktree;
      // 有 agent/模型/立即执行覆盖才带 stages（applyStageOverride 内部处理：
      // agent/model/effort 只改第一个 agent 段、startImmediately 只改首段，
      // 没覆盖时返回 undefined 让后端照模板走）。
      const overrideStages = applyStageOverride(
        taskTemplates.find((tpl) => tpl.id === edit.templateId) || null,
        { agent: edit.agentOverride, model: edit.modelOverride, effort: edit.effortOverride, startImmediately: edit.startImmediately },
      );
      const detail = await createTask(
        rootId,
        edit.templateId,
        payload,
        createWorktree,
        edit.worktreeBranchMode,
        edit.worktreeBranch,
        getNodeIdForRoot(rootId),
        {
          ...(edit.name?.trim() ? { name: edit.name.trim() } : {}),
          ...(overrideStages ? { stages: overrideStages } : {}),
        },
      );
      applyTaskDetails(rootId, [detail]);
      if (detail.task.worktree_path) {
        void refreshTaskWorktree(rootId, detail.task.worktree_path);
      }
      closeTaskEditDialog();
    } catch (err) {
      if (!isUploadAbortError(err)) {
        reportError("file.write_failed", String((err as Error)?.message || t("task.saveFailed")));
      }
      setTaskInlineSaving(false);
      setTaskInlineUploadProgress(null);
      taskInlineUploadAbortRef.current = null;
    }
  }, [applyTaskDetails, closeTaskEditDialog, taskInlineEdit, taskTemplates, t]);



  useEffect(() => {
    try {
      window.localStorage.setItem(
        MOBILE_ENTER_KEY_SEND_STORAGE_KEY,
        mobileEnterKeySends ? "1" : "0",
      );
    } catch {
      // Ignore storage failures; the setting can still apply for this session.
    }
  }, [mobileEnterKeySends]);

  useEffect(() => {
    persistSendShortcut(sendShortcut);
  }, [sendShortcut]);

  useEffect(() => {
    persistFontSizePreferences(fontSizePreferences);
  }, [fontSizePreferences]);

  useEffect(() => {
    return () => {
      Object.values(slashCopyResetTimersRef.current).forEach((timer) =>
        window.clearTimeout(timer),
      );
      slashCopyResetTimersRef.current = {};
    };
  }, []);

  useEffect(() => {
    try {
      window.localStorage.setItem(
        SIDEBARS_SWAPPED_STORAGE_KEY,
        sidebarsSwapped ? "1" : "0",
      );
    } catch {
      // Ignore storage failures; the setting can still apply for this session.
    }
  }, [sidebarsSwapped]);

  useEffect(() => {
    try {
      window.localStorage.setItem(
        GIT_DIFF_SIDE_BY_SIDE_STORAGE_KEY,
        gitDiffSideBySide ? "1" : "0",
      );
    } catch {
      // Ignore storage failures; the setting can still apply for this session.
    }
  }, [gitDiffSideBySide]);

  const [managedRootIds, setManagedRootIds] = useState<string[]>([]);
  const getRootDisplayName = useCallback((rootId: string | null | undefined): string => {
    const id = String(rootId || "").trim();
    if (!id) return "";
    const entry: any = managedRootByIdRef.current[id];
    const dn = String(entry?.display_name || "").trim();
    if (dn) return dn;
    return String(entry?.id || id);
  }, []);
  const currentRootDisplayName = getRootDisplayName(currentRootId);

  const getDisplayNodeColor = useCallback((rootId: string): string | null => {
    const rid = String(rootId || "").trim();
    if (!rid) return null;
    const curNid = String(currentRootNodeIdRef.current || "").trim();
    if (curNid) {
      const scoped = scopeKey(curNid, rid);
      const c = String((managedRootByKeyRef.current as any)[scoped]?._nodeColor || "").trim();
      if (c) return c;
    }
    return String((managedRootByIdRef.current as any)[rid]?._nodeColor || "").trim() || null;
  }, []);

  // 新建任务面板上的「项目」下拉选项。只有工作台发起的面板会用到它（看板入口
  // 项目已定），范围取跨项目会话那份清单，挂在 taskInlineEdit.targetRootId 上
  // 是为了项目增删后重算一次，不额外订阅一份 state。
  //
  // 项目名各用各的节点色，和工作台项目行、DefaultListView 的项目徽章是同一套口径。
  // 位置必须在 getDisplayNodeColor 之后 —— const 不提升，提前引用会吃到 TDZ。
  const taskCreateProjectOptions = useMemo(() => {
    const ids = Array.from(managedRootIdsRef.current);
    if (ids.length === 0 && currentRootId) ids.push(currentRootId);
    return ids
      .filter((id) => String(id || "").trim())
      .map((id) => ({
        value: id,
        label: getRootDisplayName(id) || id,
        color: getDisplayNodeColor(id) || undefined,
      }));
  }, [currentRootId, getRootDisplayName, getDisplayNodeColor, taskInlineEdit?.targetRootId]);

  // 复合键还原为裸 rootId（scoped 键为 n::r 或 r；rootId 自身不含 "::"）
  const unscopedRootId = useCallback((scoped: string): string => {
    const k = String(scoped || "");
    const idx = k.lastIndexOf("::");
    return idx >= 0 ? k.slice(idx + 2) : k;
  }, []);

  const selectRootNode = useCallback((rootId: string, nodeId?: string) => {
    const rid = String(rootId || "").trim();
    if (!rid) return;
    const nid = String(nodeId || "").trim();
    const prevNid = String(currentRootNodeIdRef.current || "").trim();
    const isSameRootSwitch = rid === String(currentRootIdRef.current || "");
    if (prevNid && nid && prevNid !== nid && isSameRootSwitch) {
      // 同名项目跨节点切换：取消上一节点的防抖重拉与请求守卫，避免旧定时器覆盖新节点
      if (sessionListReloadTimerRef.current) {
        window.clearTimeout(sessionListReloadTimerRef.current);
        sessionListReloadTimerRef.current = null;
      }
      sessionListAbortRef.current?.abort();
      kanbanAbortRef.current?.abort();
      // 递增序号使旧 in-flight 请求的 seq 校验直接失效
      kanbanLoadSeqRef.current += 1;
      sessionListLoadSeqRef.current += 1;
    }
    currentRootNodeIdRef.current = nid || null;
    setCurrentRootNodeId(nid || null);
    // 无条件更新模块级 root→node 映射，保证路由/作用域解析在任何时机都正确
    setRootNodeId(rid, nid || undefined);
    // active node 跟着选中的项目走：选中哪个项目，请求就该发到那台机器。
    // 之前 active node 只由节点切换器改，于是「本机项目 + 上次点过 pc」的组合下，
    // 所有不带 nodeId 的请求（模板、会话…）都发去了 pc，页面看着在本机、数据却是
    // 另一台机器的。项目本身知道自己在哪个节点，选中它时把 active 一起切过去。
    if (nid) {
      const known = getNodeById(nid);
      if (known && getActiveNodeId() !== nid) {
        setActiveNodeId(nid);
        window.dispatchEvent(new CustomEvent("mindfs:nodes-changed"));
      }
    }
    if (prevNid && nid && prevNid !== nid) {
      // 同名项目跨节点切换：清掉该 root 的会话选择状态，避免把另一节点的会话恢复到错误节点
      const scopedRid = scopeKey(nid, rid);
      selectedSessionByRootRef.current[scopedRid] = "";
      boundSessionByRootRef.current[scopedRid] = "";
      delete drawerSessionByRootRef.current[scopedRid];
      setSelectedSession(null);
      const prefix = scopeSessionKey(nid, rid, "");
      for (const k of Object.keys(sessionCacheRef.current)) {
        if (k.startsWith(prefix)) delete sessionCacheRef.current[k];
      }
      if (isSameRootSwitch) {
        // 同名项目跨节点：清空 Kanban/文件/git 历史/会话视图残留，避免旧节点 incrementally merged 的 taskDetailsById/旧历史/旧会话串台
        taskDetailsByIdRef.current = {};
        setTaskDetailsById({});
        setTaskFirstInputById({});
        setTaskSessionKeysById({});
        setTaskRelatedFilesById({});
        setKanbanTasks([]);
        setKanbanTaskCountItems([]);
        setKanbanTasksLoading(true);
        setSessions([]);
        clearFileMemoryCacheForView();
        clearGitHistoryCache(rid, prevNid);
        setGitStatus(null);
        setGitHistory(null);
        setGitDiff(null);
        setGitStatusLoading(true);
        setGitHistoryLoading(true);
      }
    }
    // 让按裸 rootId 的 payload 读取命中当前选中节点的数据
    const entry = managedRootByKeyRef.current[rootNodeKey(nid, rid)];
    if (entry && managedRootByIdRef.current[rid] !== entry) {
      managedRootByIdRef.current = {
        ...managedRootByIdRef.current,
        [rid]: entry,
      };
      setRootNodeMap(managedRootByIdRef.current as Record<string, any>);
    }
  }, []);

  const [rootEntries, setRootEntries] = useState<FileEntry[]>([]);
  // open_dir 回调需要根列表做根展开互斥，经 ref 引用避免依赖抖动
  const rootEntriesRef = useRef<FileEntry[]>([]);
  useEffect(() => {
    rootEntriesRef.current = rootEntries;
  }, [rootEntries]);
  const [agentConfigSwitchRequest, setAgentConfigSwitchRequest] =
    useState<AgentConfigSwitchRequest | null>(null);
  const [bootstrapState, setBootstrapState] = useState<BootstrapState>(() =>
    bootstrapService.snapshot(),
  );
  const [e2eeState, setE2eeState] = useState<E2EEState>(() =>
    e2eeService.snapshot(),
  );
  useEffect(() => {
    if (
      isMobile ||
      onboardingAutoStartRef.current ||
      !currentRootId ||
      bootstrapState.phase !== "ready" ||
      (e2eeState.required && !e2eeState.unlocked)
    ) {
      return;
    }
    onboardingAutoStartRef.current = true;
    if (!shouldAutoStartOnboarding()) return;
    const timer = window.setTimeout(() => setOnboardingOpen(true), 700);
    return () => window.clearTimeout(timer);
  }, [bootstrapState.phase, currentRootId, e2eeState.required, e2eeState.unlocked, isMobile]);
  useEffect(() => {
    if (isMobile && onboardingOpen) {
      setOnboardingOpen(false);
      setOnboardingMainContentViewRoot(null);
    }
  }, [isMobile, onboardingOpen]);
  const [e2eeSecretInput, setE2eeSecretInput] = useState("");
  useEffect(() => { syncNodesFromServer().catch(()=>{}); }, []);
  const [e2eePromptError, setE2eePromptError] = useState("");
  const [e2eePromptBusy, setE2eePromptBusy] = useState(false);
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
  const [mainView, setMainView] = useState<MainViewMode>(
    () => loadLegacyMainView() || loadMainView(),
  );
  const mainViewRef = useRef<MainViewMode>(mainView);
  // 从 chat 返回时回到上一个非 chat 模式（瞬态，不落盘）
  const lastNonChatViewRef = useRef<MainViewMode>("board");
  const [status, setStatus] = useState<WSStatus>("disconnected");
  const [file, setFile] = useState<FilePayload | null>(null);
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
  const showHiddenFiles = true;
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
  const [updateState, setUpdateState] = useState<UpdateState>(() =>
    normalizeUpdateState(null),
  );
  const [updateSubmitting, setUpdateSubmitting] = useState(false);

  const handleStartUpdate = useCallback(async () => {
    const next = normalizeUpdateState(updateState);
    const status = (next.status || "idle").toLowerCase();
    if (
      status === "downloading" ||
      status === "installing" ||
      status === "restarting"
    ) {
      return;
    }
    if (next.has_update && status === "available") {
      const target = next.latest_version
        ? `v${next.latest_version}`
        : "the latest version";
      const confirmed = await confirmDialog({
        message: t("update.confirmInstall", { target }),
        confirmLabel: t("update.install"),
      });
      if (!confirmed) {
        return;
      }
    }
    setUpdateSubmitting(true);
    try {
      await scheduleWebViewCacheClearOnNextLaunch();
      setUpdateState(normalizeUpdateState(await triggerUpdate()));
    } catch (error) {
      await cancelScheduledWebViewCacheClear();
      const message =
        error instanceof Error ? error.message : "Failed to start update";
      setUpdateState((prev) =>
        normalizeUpdateState({
          ...prev,
          status: "failed",
          message,
        }),
      );
    } finally {
      setUpdateSubmitting(false);
    }
  }, [updateState]);

  useEffect(() => {
    currentRootIdRef.current = currentRootId;
  }, [currentRootId]);
  useEffect(() => {
    currentRootNodeIdRef.current = currentRootNodeId;
  }, [currentRootNodeId]);
  useEffect(() => {
    let cancelled = false;
    if (!e2eeState.configured || (e2eeState.required && !e2eeState.unlocked)) {
      return;
    }
    fetchAgents(true, getNodeIdForRoot(currentRootId || "") as any)
      .then((items) => {
        if (cancelled) return;
        setAvailableAgents(items);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [agentsVersion, currentRootId, e2eeState.configured, e2eeState.required, e2eeState.unlocked]);
  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }
    if (currentRootId) {
      window.localStorage.setItem(accountScopedKey(LAST_ROOT_STORAGE_KEY), currentRootId);
      window.localStorage.setItem(accountScopedKey(LAST_ROOT_NODE_STORAGE_KEY), currentRootNodeId || "");
    }
  }, [currentRootId, currentRootNodeId]);
  useEffect(() => {
    expandedRef.current = expanded;
  }, [expanded]);
  useEffect(() => {
    selectedDirRef.current = selectedDir;
  }, [selectedDir]);
  useEffect(() => {
    entriesByPathRef.current = entriesByPath;
  }, [entriesByPath]);
  useEffect(() => {
    fileRef.current = file;
  }, [file]);
  useEffect(() => {
    viewerSelectionRef.current = viewerSelection;
  }, [viewerSelection]);
  useEffect(() => {
    pluginQueryRef.current = pluginQuery;
  }, [pluginQuery]);
  useEffect(() => {
    if (!file?.path || !currentRootId) return;
    const nextKey = `${currentRootId}:${file.path}`;
    if (lastPluginResetFileKeyRef.current !== nextKey) {
      pluginBypassRef.current = false;
      setPluginBypass(false);
      lastPluginResetFileKeyRef.current = nextKey;
    }
  }, [file?.path, currentRootId]);
  useEffect(() => {
    pluginBypassRef.current = pluginBypass;
  }, [pluginBypass]);
  useEffect(() => {
    selectedSessionRef.current = selectedSession;
  }, [selectedSession]);
  useEffect(() => {
    if (selectedSession || activeBoundSessionKey || interactionMode !== "main") {
      setPendingPlanMode(false);
    }
  }, [selectedSession, activeBoundSessionKey, interactionMode]);
  useEffect(() => {
    sessionsRef.current = sessions;
  }, [sessions]);
  useEffect(() => {
    multiProjectPendingRef.current = multiProjectPendingByKey;
  }, [multiProjectPendingByKey]);
  useEffect(() => {
    setPendingPlanMode(false);
  }, [currentRootId]);
  useEffect(() => {
    currentSessionRef.current = currentSession;
  }, [currentSession]);
  useEffect(() => {
    interactionModeRef.current = interactionMode;
  }, [interactionMode]);
  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }
    window.localStorage.setItem(TREE_SORT_STORAGE_KEY, treeSortMode);
  }, [treeSortMode]);
  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }
    window.localStorage.setItem(
      DIRECTORY_SORT_OVERRIDES_STORAGE_KEY,
      JSON.stringify(directorySortOverrides),
    );
  }, [directorySortOverrides]);
  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }
    window.localStorage.setItem(MAIN_VIEW_STORAGE_KEY, mainView);
  }, [mainView]);
  useEffect(() => {
    const rootID = currentRootId;
    if (!rootID) return;
    setActiveBoundSessionKey(boundSessionByRootRef.current[scopedRootKey(rootID)] || null);
    setCurrentSession(drawerSessionByRootRef.current[scopedRootKey(rootID)] || null);
    setIsDrawerOpen(!!drawerOpenByRootRef.current[scopedRootKey(rootID)]);
  }, [currentRootId]);

  const setBoundSessionForRoot = useCallback(
    (rootID: string | null | undefined, key: string | null) => {
      if (!rootID) return;
      boundSessionByRootRef.current[scopedRootKey(rootID)] = key;
      if (currentRootIdRef.current === rootID) {
        setActiveBoundSessionKey(key);
      }
    },
    [],
  );

  const setDrawerSessionForRoot = useCallback(
    (rootID: string | null | undefined, session: Session | SessionItem | null) => {
      if (!rootID) return;
      const next = toSessionItem(rootID, session);
      drawerSessionByRootRef.current[scopedRootKey(rootID)] = next;
      if (currentRootIdRef.current === rootID) {
        setCurrentSession(next);
      }
    },
    [],
  );

  const setDrawerOpenForRoot = useCallback(
    (rootID: string | null | undefined, open: boolean) => {
      if (!rootID) return;
      drawerOpenByRootRef.current[scopedRootKey(rootID)] = open;
      if (currentRootIdRef.current === rootID) {
        setIsDrawerOpen(open);
      }
    },
    [],
  );

  const resetSessionLockForRoot = useCallback(
    (rootID: string | null | undefined) => {
      const root = String(rootID || "").trim();
      if (!root) return;
      setBoundSessionForRoot(root, null);
      setDrawerSessionForRoot(root, null);
      selectedSessionByRootRef.current[scopedRootKey(root)] = null;
      setDrawerOpenForRoot(root, false);
      if (currentRootIdRef.current === root) {
        selectedSessionRef.current = null;
        setSelectedSession(null);
        setSelectedSessionLoading(false);
        interactionModeRef.current = "main";
        setInteractionMode("main");
      }
    },
    [setBoundSessionForRoot, setDrawerOpenForRoot, setDrawerSessionForRoot],
  );

  const resetLocksForRootTransition = useCallback(
    (targetRoot: string | null | undefined) => {
      const sourceRoot = currentRootIdRef.current;
      if (shouldResetSessionLockForRootChange(sourceRoot, targetRoot)) {
        resetSessionLockForRoot(sourceRoot);
        resetSessionLockForRoot(targetRoot);
      }
    },
    [resetSessionLockForRoot],
  );

  const getDirectorySortKey = useCallback(
    (rootID: string | null | undefined, dirPath: string | null | undefined) => {
      if (!rootID) {
        return "";
      }
      const normalizedDir = !dirPath || dirPath === rootID ? "." : dirPath;
      return `${rootID}:${normalizedDir}`;
    },
    [],
  );

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
  const workspaceOpen = mainView === "workspace";
  // task.updated 的处理器要按这个放行跨项目推送（板态只收当前项目），
  // 而它是被 useRealtimeEvents 消费的——只能走 ref，不能直接传值。
  const workspaceOpenRef = useRef(workspaceOpen);
  workspaceOpenRef.current = workspaceOpen;
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
  const currentDirectorySortMode = currentDirectorySortOverride || treeSortMode;

  const replaceURLState = useCallback((next: URLState) => {
    // 主面板模式默认取当前状态：调用点一律紧跟在 switchMainView 之后（或本就不切面板），
    // 因此这里补默认值即可让 URL 与按钮高亮保持同源，无需逐个调用点传参。
    const merged: URLState = { view: mainViewRef.current, ...next };
    // 多节点同名项目：URL 始终携带当前选中节点，防止刷新/回退还原到错误节点
    if (
      merged.root &&
      merged.root === currentRootIdRef.current &&
      currentRootNodeIdRef.current &&
      !merged.node
    ) {
      merged.node = currentRootNodeIdRef.current;
    }
    const search = buildURLSearch(merged);
    const target = `${window.location.pathname}${search}`;
    window.history.replaceState(null, "", target);
  }, []);

  const handleOnboardingStepChange = useCallback((stepId: string) => {
    const showingSidebar = stepId === "sidebar-menu" || stepId === "project-tabs";
    const showingSessions = stepId === "session-actions";
    const showingTasks = stepId === "project-home" || stepId === "main-menu" || stepId === "task-templates" || stepId === "task-template-menu" || stepId === "tasks";

    if (showingTasks && currentRootId) {
      selectedSessionRef.current = null;
      setSelectedSession(null);
      setSelectedSessionLoading(false);
      setFile(null);
      setGitDiff(null);
      setSelectedDir(currentRootId);
      setOnboardingMainContentViewRoot(currentRootId);
      replaceURLState({ root: currentRootId, file: "", session: "", cursor: 0, pluginQuery: {} });
    }

    if (!isMobile) {
      setIsLeftOpen(true);
      setIsRightOpen(true);
      return;
    }
    setIsLeftOpen(showingSidebar);
    setIsRightOpen(showingSessions);
  }, [currentRootId, isMobile, replaceURLState]);

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
  const clearRootScopedClientState = useCallback((rootID: string, options?: { removeLastRoot?: boolean }) => {
    const root = String(rootID || "").trim();
    if (!root) {
      return;
    }
    const sessionPrefix = `${root}::`;
    const treePrefix = `${root}:`;

    const deleteRecordKeys = <T,>(record: Record<string, T>, predicate: (key: string) => boolean) => {
      for (const key of Object.keys(record)) {
        if (predicate(key)) {
          delete record[key];
        }
      }
    };
    const deleteSessionRecordKeys = <T,>(record: Record<string, T>) => {
      deleteRecordKeys(record, (key) => key.startsWith(sessionPrefix));
    };

    clearGitHistoryCache(root);
    clearFileCacheForRoot(root);
    void clearCachedSessionsForRoot(root, getNodeIdForRoot(root));

    delete boundSessionByRootRef.current[scopedRootKey(root)];
    delete suppressedAutoBindSessionByRootRef.current[scopedRootKey(root)];
    delete drawerSessionByRootRef.current[scopedRootKey(root)];
    delete selectedSessionByRootRef.current[scopedRootKey(root)];
    delete drawerOpenByRootRef.current[scopedRootKey(root)];
    delete pluginsLoadedByRootRef.current[scopedRootKey(root)];
    delete pluginsLoadingByRootRef.current[scopedRootKey(root)];
    delete pluginsTrustPendingByRootRef.current[scopedRootKey(root)];

    deleteSessionRecordKeys(sessionCacheRef.current);
    deleteSessionRecordKeys(loadedSessionRef.current);
    deleteSessionRecordKeys(loadingSessionRef.current);
    deleteSessionRecordKeys(pendingBySessionRef.current);
    deleteSessionRecordKeys(queuedMessagesBySessionRef.current);
    deleteSessionRecordKeys(queueFrozenBySessionRef.current);
    deleteSessionRecordKeys(cancelRequestedBySessionRef.current);
    deleteSessionRecordKeys(pendingRequestRef.current);
    deleteRecordKeys(optimisticDequeuedIdsRef.current, (key) => key.startsWith(sessionPrefix));
    staleSessionKeysRef.current = new Set(
      Array.from(staleSessionKeysRef.current).filter((key) => !key.startsWith(sessionPrefix)),
    );

    deleteRecordKeys(entriesByPathRef.current, (key) => key === root || key.startsWith(treePrefix));
    setEntriesByPath((prev) => {
      const next = { ...prev };
      deleteRecordKeys(next, (key) => key === root || key.startsWith(treePrefix));
      return next;
    });
    invalidTreeCacheKeysRef.current = new Set(
      Array.from(invalidTreeCacheKeysRef.current).filter(
        (key) => key !== root && !key.startsWith(treePrefix),
      ),
    );

    setGitStatusExpandedByRoot((prev) => {
      const scoped = scopedRootKey(root);
      if (!(scoped in prev)) return prev;
      const next = { ...prev };
      delete next[scoped];
      return next;
    });
    setGitHistoryExpandedByRoot((prev) => {
      const scoped = scopedRootKey(root);
      if (!(scoped in prev)) return prev;
      const next = { ...prev };
      delete next[scoped];
      return next;
    });
    setDirectorySortOverrides((prev) => {
      let changed = false;
      const next = { ...prev };
      for (const key of Object.keys(next)) {
        if (key === root || key.startsWith(treePrefix)) {
          delete next[key];
          changed = true;
        }
      }
      return changed ? next : prev;
    });
    setMultiProjectPendingByKey((prev) => {
      let changed = false;
      const next = { ...prev };
      for (const key of Object.keys(next)) {
        if (key.startsWith(sessionPrefix)) {
          delete next[key];
          changed = true;
        }
      }
      return changed ? next : prev;
    });
    setMultiProjectSessionGroups((prev) => {
      const next = prev.filter((group) => group.rootId !== root);
      return next.length === prev.length ? prev : next;
    });
    multiProjectPendingRef.current = Object.fromEntries(
      Object.entries(multiProjectPendingRef.current).filter(([key]) => !key.startsWith(sessionPrefix)),
    );
    setSlashCommandResults((prev) => {
      let changed = false;
      const next = { ...prev };
      for (const key of Object.keys(next)) {
        if (key.startsWith(sessionPrefix)) {
          delete next[key];
          changed = true;
        }
      }
      return changed ? next : prev;
    });

    deleteRecordKeys(fileScrollPositionsRef.current, (key) => key.startsWith(sessionPrefix));
    persistFileScrollPositions(fileScrollPositionsRef.current);
    removeLocalStorageByPrefix(`${PLUGIN_QUERY_STORAGE_PREFIX}${root}:`);
    if (
      options?.removeLastRoot === true &&
      typeof window !== "undefined" &&
      window.localStorage.getItem(accountScopedKey(LAST_ROOT_STORAGE_KEY)) === root
    ) {
      window.localStorage.removeItem(accountScopedKey(LAST_ROOT_STORAGE_KEY));
      window.localStorage.removeItem(accountScopedKey(LAST_ROOT_NODE_STORAGE_KEY));
    }

    bumpCacheVersion();
    setQueueVersion((value) => value + 1);
  }, [bumpCacheVersion]);
  const clearSlashCommandResultForSession = useCallback(
    (rootID: string, sessionKey: string) => {
      const key = rootSessionKey(rootID, sessionKey);
      setSlashCommandResults((prev) => {
        if (!prev[key]) {
          return prev;
        }
        const next = { ...prev };
        delete next[key];
        return next;
      });
    },
    [rootSessionKey],
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
  const setMultiProjectSessionPending = useCallback(
    (rootID: string | null | undefined, sessionKey: string | null | undefined, pending: boolean) => {
      const resolvedRoot = String(rootID || "");
      const resolvedKey = String(sessionKey || "");
      if (!resolvedRoot || !resolvedKey) {
        return;
      }
      const key = rootSessionKey(resolvedRoot, resolvedKey);
      setMultiProjectPendingByKey((prev) => {
        const next = { ...prev };
        if (pending) {
          next[key] = true;
        } else {
          delete next[key];
        }
        multiProjectPendingRef.current = next;
        setMultiProjectSessionGroups((groups) => applyPendingToMultiProjectGroups(groups, next));
        return next;
      });
    },
    [applyPendingToMultiProjectGroups, rootSessionKey],
  );
  const resolveRootForSessionKey = useCallback(
    (sessionKey: string): string | null => {
      if (!sessionKey) return null;
      const currentRoot = currentRootIdRef.current;
      if (
        currentRoot &&
        sessionCacheRef.current[rootSessionKey(currentRoot, sessionKey)]
      ) {
        return currentRoot;
      }
      for (const [rootID, key] of Object.entries(
        boundSessionByRootRef.current,
      )) {
        if (key === sessionKey) {
          return unscopedRootId(rootID);
        }
      }
      for (const [rootID, session] of Object.entries(
        drawerSessionByRootRef.current,
      )) {
        if (session?.key === sessionKey) {
          return unscopedRootId(rootID);
        }
      }
      const suffix = `::${sessionKey}`;
      const matched = Object.keys(sessionCacheRef.current).find((key) =>
        key.endsWith(suffix),
      );
      if (!matched) return null;
      return unscopedRootId(matched.slice(0, matched.length - suffix.length));
    },
    [rootSessionKey],
  );
  const getSessionSnapshot = useCallback(
    (
      rootId: string | null | undefined,
      session: Session | SessionItem | null | undefined,
    ) => {
      if (!rootId || !session) return null;
      const key = (session as any).key || (session as any).session_key;
      if (!key) return null;
      const ck = rootSessionKey(rootId, key);
      const cached = sessionCacheRef.current[ck];
      const drawerSession = drawerSessionByRootRef.current[scopedRootKey(rootId)];
      const fallbackExchanges = Array.isArray((session as any).exchanges)
        ? ((session as any).exchanges as Exchange[])
        : [];
      const exchanges = Array.isArray((cached as any)?.exchanges)
        ? ((cached as any).exchanges as Exchange[]) || []
        : fallbackExchanges;
      const pending =
        drawerSession?.key === key
          ? !!(drawerSession as any)?.pending
          : typeof (session as any)?.pending === "boolean"
            ? !!(session as any).pending
            : typeof (cached as any)?.pending === "boolean"
              ? !!(cached as any).pending
              : undefined;
      return {
        ...(session as any),
        ...(cached as any),
        ...(drawerSession?.key === key ? (drawerSession as any) : null),
        key,
        search_seq: (session as any).search_seq,
        search_target_id: (session as any).search_target_id,
        search_snippet: (session as any).search_snippet,
        search_match_type: (session as any).search_match_type,
        exchanges,
        pending,
      } as any;
    },
    [rootSessionKey, cacheVersion],
  );

  const setSelectedPendingByKey = useCallback(
    (sessionKey: string, pending: boolean) => {
      setSelectedSession((prev) => {
        const prevKey = prev?.key || prev?.session_key;
        if (!prev || prevKey !== sessionKey) return prev;
        return { ...(prev as any), pending } as SessionItem;
      });
    },
    [],
  );

  const resolvePendingForSession = useCallback(
    (
      rootID: string | null | undefined,
      sessionKey: string | null | undefined,
      fallback?: boolean,
    ): boolean => {
      const resolvedRoot = String(rootID || "");
      const resolvedKey = String(sessionKey || "");
      if (!resolvedRoot || !resolvedKey) {
        return !!fallback;
      }
      const cacheKey = rootSessionKey(resolvedRoot, resolvedKey);
      if (pendingBySessionRef.current[cacheKey]) {
        return true;
      }
      const drawer = drawerSessionByRootRef.current[scopedRootKey(resolvedRoot)] as
        | ({ pending?: boolean; key?: string; session_key?: string } & Record<string, unknown>)
        | null
        | undefined;
      if (
        drawer &&
        (drawer.key || drawer.session_key) === resolvedKey &&
        typeof drawer.pending === "boolean"
      ) {
        return drawer.pending;
      }
      const selected = selectedSessionRef.current as
        | ({ pending?: boolean; key?: string; session_key?: string; root_id?: string } & Record<string, unknown>)
        | null
        | undefined;
      if (
        selected &&
        ((selected.root_id as string | undefined) || currentRootIdRef.current) ===
          resolvedRoot &&
        (selected.key || selected.session_key) === resolvedKey &&
        typeof selected.pending === "boolean"
      ) {
        return selected.pending;
      }
      const cached = sessionCacheRef.current[cacheKey] as
        | ({ pending?: boolean } & Record<string, unknown>)
        | null
        | undefined;
      if (cached && typeof cached.pending === "boolean") {
        return cached.pending;
      }
      return !!fallback;
    },
    [rootSessionKey],
  );

  const clearLocalPendingForSession = useCallback(
    (rootID: string | null | undefined, sessionKey: string | null | undefined) => {
      const resolvedRoot = String(rootID || "");
      const resolvedKey = String(sessionKey || "");
      if (!resolvedRoot || !resolvedKey) {
        return;
      }
      const clearPendingAck = <T,>(session: T): T => {
        const exchanges = (session as any)?.exchanges;
        if (!Array.isArray(exchanges)) {
          return session;
        }
        return {
          ...(session as any),
          exchanges: exchanges.map((exchange: any) =>
            exchange?.pending_ack === true
              ? { ...exchange, pending_ack: false }
              : exchange,
          ),
        } as T;
      };
      const cacheKey = rootSessionKey(resolvedRoot, resolvedKey);
      delete pendingBySessionRef.current[cacheKey];
      const cached = sessionCacheRef.current[cacheKey];
      if (cached && (cached.key || (cached as any).session_key) === resolvedKey) {
        sessionCacheRef.current[cacheKey] = clearPendingAck({
          ...(cached as any),
          pending: false,
        } as Session);
      }
      setSelectedSession((prev) => {
        const prevKey = prev?.key || prev?.session_key;
        const prevRoot =
          (prev?.root_id as string | undefined) || currentRootIdRef.current;
        if (!prev || prevKey !== resolvedKey || prevRoot !== resolvedRoot) {
          return prev;
        }
        return clearPendingAck({
          ...(prev as any),
          pending: false,
        } as SessionItem);
      });
      const drawer = drawerSessionByRootRef.current[scopedRootKey(resolvedRoot)];
      if (drawer && (drawer.key || (drawer as any).session_key) === resolvedKey) {
        setDrawerSessionForRoot(resolvedRoot, clearPendingAck({
          ...(drawer as any),
          pending: false,
        } as Session));
      }
      if (currentRootIdRef.current === resolvedRoot) {
        setSessions((prev) =>
          prev.map((item) => {
            const itemKey = item.key || item.session_key;
            if (itemKey !== resolvedKey) {
              return item;
            }
            return clearPendingAck({
              ...(item as any),
              pending: false,
            } as SessionItem);
          }),
        );
      }
      setMultiProjectSessionPending(resolvedRoot, resolvedKey, false);
      bumpCacheVersion();
    },
    [bumpCacheVersion, rootSessionKey, setDrawerSessionForRoot, setMultiProjectSessionPending],
  );

  const markSessionStale = useCallback(
    (rootID: string | null | undefined, sessionKey: string | null | undefined) => {
      const resolvedRoot = String(rootID || "");
      const resolvedKey = String(sessionKey || "");
      if (!resolvedRoot || !resolvedKey || resolvedKey.startsWith("pending-")) {
        return;
      }
      staleSessionKeysRef.current.add(rootSessionKey(resolvedRoot, resolvedKey));
    },
    [rootSessionKey],
  );

  const clearSessionStale = useCallback(
    (rootID: string | null | undefined, sessionKey: string | null | undefined) => {
      const resolvedRoot = String(rootID || "");
      const resolvedKey = String(sessionKey || "");
      if (!resolvedRoot || !resolvedKey) {
        return;
      }
      staleSessionKeysRef.current.delete(rootSessionKey(resolvedRoot, resolvedKey));
    },
    [rootSessionKey],
  );

  const isSessionStale = useCallback(
    (rootID: string | null | undefined, sessionKey: string | null | undefined): boolean => {
      const resolvedRoot = String(rootID || "");
      const resolvedKey = String(sessionKey || "");
      if (!resolvedRoot || !resolvedKey) {
        return false;
      }
      return staleSessionKeysRef.current.has(rootSessionKey(resolvedRoot, resolvedKey));
    },
    [rootSessionKey],
  );

  // 方案 C 重锚定序号：每次 restoreActiveSession 换窗时递增，viewer 按 _anchoredAt 一次性应用。
  const anchorSeqRef = useRef(0);

  const restoreActiveSession = useCallback(
    async (
      rootID: string | null | undefined,
      sessionKey: string | null | undefined,
    ): Promise<Session | null> => {
      const resolvedRoot = String(rootID || "");
      const resolvedKey = String(sessionKey || "");
      if (!resolvedRoot || !resolvedKey || resolvedKey.startsWith("pending-")) {
        return null;
      }
      const cacheKey = rootSessionKey(resolvedRoot, resolvedKey);
      const cachedBeforeSync = sessionCacheRef.current[cacheKey];
      const resumeCursor = sessionService.getEventCursor(
        resolvedRoot,
        resolvedKey,
      );
      const inflight = loadingSessionRef.current[cacheKey] as unknown as Promise<Session | null> | undefined;
      if (inflight) {
        const hit = await inflight;
        return hit;
      }
      const promise = (async (): Promise<Session | null> => {
        // 方案 B 首屏按需：先尝试窗口化拉取最新 50 条，失败回退全量 syncSession
        try {
          setWindowedView(resolvedKey, true);
          const win = await getSessionWindow(resolvedRoot, resolvedKey, {
            latest: SESSION_WINDOW_SIZE,
            nodeId: getNodeIdForRoot(resolvedRoot),
          });
          if (win && (win as any).session) {
            let sess: any = (win as any).session;
            sess = { ...sess, key: resolvedKey, session_key: resolvedKey };
            // 复用原 resumeCursor 的 pending 转瞬态合并逻辑
            if (resumeCursor) {
              const incomingExchanges = Array.isArray(sess.exchanges)
                ? (sess.exchanges as Exchange[])
                : [];
              const hasPendingTurn = incomingExchanges.some(
                (exchange) => Number((exchange as any)?.seq || 0) === 0,
              );
              const localTransient = Array.isArray((cachedBeforeSync as any)?.exchanges)
                ? (((cachedBeforeSync as any).exchanges as Exchange[]).filter(
                    (exchange) => Number((exchange as any)?.seq || 0) === 0,
                  ))
                : [];
              if (hasPendingTurn && localTransient.length > 0) {
                sess = {
                  ...sess,
                  exchanges: [
                    ...incomingExchanges.filter(
                      (exchange) => Number((exchange as any)?.seq || 0) > 0,
                    ),
                    ...localTransient,
                  ],
                };
              } else {
                sessionService.clearEventCursor(resolvedRoot, resolvedKey);
              }
            }
            const serverPending =
              typeof sess?.pending === "boolean" ? !!sess.pending : undefined;
            if (serverPending === false) {
              clearLocalPendingForSession(resolvedRoot, resolvedKey);
            }
            const pending =
              serverPending === false
                ? false
                : resolvePendingForSession(resolvedRoot, resolvedKey, !!serverPending);
            const anchorAt = ++anchorSeqRef.current;
            const toCache = {
              ...sess,
              key: resolvedKey,
              pending,
              _windowMeta: win.meta,
              _anchoredAt: anchorAt,
            } as Session;
            sessionCacheRef.current[cacheKey] = toCache;
            bumpCacheVersion();
            await sessionService.markSessionReady(resolvedRoot, resolvedKey);
            return toCache;
          }
          // 窗口未命中则清标记，走全量回退
          clearWindowedView(resolvedKey);
        } catch {
          clearWindowedView(resolvedKey);
        }
        // 回退：全量 syncSession（保持原有 pending/转瞬态逻辑）
        const syncResult = await syncSession(resolvedRoot, resolvedKey, {
          nodeId: getNodeIdForRoot(resolvedRoot),
        });
        let fullSession = syncResult?.session as any;
        if (!fullSession) {
          return null;
        }
        if (resumeCursor) {
          const incomingExchanges = Array.isArray((fullSession as any).exchanges)
            ? ((fullSession as any).exchanges as Exchange[])
            : [];
          const hasPendingTurn = incomingExchanges.some(
            (exchange) => Number((exchange as any)?.seq || 0) === 0,
          );
          const localTransient = Array.isArray((cachedBeforeSync as any)?.exchanges)
            ? (((cachedBeforeSync as any).exchanges as Exchange[]).filter(
                (exchange) => Number((exchange as any)?.seq || 0) === 0,
              ))
            : [];
          if (hasPendingTurn && localTransient.length > 0) {
            fullSession = {
              ...(fullSession as any),
              exchanges: [
                ...incomingExchanges.filter(
                  (exchange) => Number((exchange as any)?.seq || 0) > 0,
                ),
                ...localTransient,
              ],
            } as Session;
          } else {
            sessionService.clearEventCursor(resolvedRoot, resolvedKey);
          }
        }
        const serverPending =
          typeof (fullSession as any)?.pending === "boolean"
            ? !!(fullSession as any).pending
            : undefined;
        if (serverPending === false) {
          clearLocalPendingForSession(resolvedRoot, resolvedKey);
        }
        const pending =
          serverPending === false
            ? false
            : resolvePendingForSession(resolvedRoot, resolvedKey, !!serverPending);
        const anchorAt = ++anchorSeqRef.current;
        const anchoredExs = Array.isArray((fullSession as any)?.exchanges)
          ? ((fullSession as any).exchanges as any[])
          : [];
        const anchoredMeta = {
          total: anchoredExs.length,
          hasMore: false,
          minSeq: anchoredExs.length ? Number(anchoredExs[0]?.seq || 0) : 0,
          maxSeq: anchoredExs.length
            ? Number(anchoredExs[anchoredExs.length - 1]?.seq || 0)
            : 0,
        };
        sessionCacheRef.current[cacheKey] = {
          ...(fullSession as any),
          key: resolvedKey,
          pending,
          _windowMeta: anchoredMeta as any,
          _anchoredAt: anchorAt,
        } as Session;
        bumpCacheVersion();
        await sessionService.markSessionReady(resolvedRoot, resolvedKey);
        return {
          ...(fullSession as any),
          key: resolvedKey,
          pending,
          _windowMeta: anchoredMeta as any,
          _anchoredAt: anchorAt,
        } as Session;
      })();
      loadingSessionRef.current[cacheKey] = promise as any;
      try {
        return await promise;
      } finally {
        delete loadingSessionRef.current[cacheKey];
      }
    },
    [bumpCacheVersion, clearLocalPendingForSession, resolvePendingForSession, rootSessionKey],
  );

  const updateSessionRelatedFilesForKey = useCallback(
    (rootID: string, sessionKey: string, relatedFiles: RelatedFile[]) => {
      if (!rootID || !sessionKey) return;
      const cacheKey = rootSessionKey(rootID, sessionKey);
      const cached = sessionCacheRef.current[cacheKey];
      const nextRelatedFiles = Array.isArray(relatedFiles)
        ? [...relatedFiles]
        : [];
      if (cached) {
        sessionCacheRef.current[cacheKey] = {
          ...(cached as any),
          related_files: nextRelatedFiles,
        } as Session;
      }
      const lastMain = lastMainSessionSnapshotRef.current;
      const lastMainKey = lastMain?.key || lastMain?.session_key;
      const lastMainRoot =
        (lastMain?.root_id as string | undefined) || currentRootIdRef.current;
      if (lastMain && lastMainKey === sessionKey && lastMainRoot === rootID) {
        lastMainSessionSnapshotRef.current = {
          ...(lastMain as any),
          related_files: nextRelatedFiles,
        } as Session;
      }
      setSelectedSession((prev) => {
        const prevKey = prev?.key || prev?.session_key;
        const prevRoot =
          (prev?.root_id as string | undefined) || currentRootIdRef.current;
        if (!prev || prevKey !== sessionKey || prevRoot !== rootID) return prev;
        return {
          ...(prev as any),
          related_files: nextRelatedFiles,
        } as SessionItem;
      });
      const current = drawerSessionByRootRef.current[scopedRootKey(rootID)];
      if (current && current.key === sessionKey) {
        setDrawerSessionForRoot(rootID, {
          ...(current as any),
          related_files: nextRelatedFiles,
        } as Session);
      }
      bumpCacheVersion();
    },
    [rootSessionKey, setDrawerSessionForRoot, bumpCacheVersion],
  );

  const updateSessionRelatedWorktreeForKey = useCallback(
    (rootID: string, sessionKey: string, relatedWorktree: RelatedWorktree | null | undefined) => {
      if (!rootID || !sessionKey || !relatedWorktree) return;
      const cacheKey = rootSessionKey(rootID, sessionKey);
      const cached = sessionCacheRef.current[cacheKey];
      if (cached) {
        sessionCacheRef.current[cacheKey] = {
          ...(cached as any),
          related_worktree: relatedWorktree,
        } as Session;
      }

      const lastMain = lastMainSessionSnapshotRef.current;
      const lastMainKey = lastMain?.key || lastMain?.session_key;
      const lastMainRoot =
        (lastMain?.root_id as string | undefined) || currentRootIdRef.current;
      if (lastMain && lastMainKey === sessionKey && lastMainRoot === rootID) {
        lastMainSessionSnapshotRef.current = {
          ...(lastMain as any),
          related_worktree: relatedWorktree,
        } as Session;
      }

      setSelectedSession((prev) => {
        const prevKey = prev?.key || prev?.session_key;
        const prevRoot =
          (prev?.root_id as string | undefined) || currentRootIdRef.current;
        if (!prev || prevKey !== sessionKey || prevRoot !== rootID) return prev;
        return {
          ...(prev as any),
          related_worktree: relatedWorktree,
        } as SessionItem;
      });

      const current = drawerSessionByRootRef.current[scopedRootKey(rootID)];
      if (current && current.key === sessionKey) {
        setDrawerSessionForRoot(rootID, {
          ...(current as any),
          related_worktree: relatedWorktree,
        } as Session);
      }
      bumpCacheVersion();
    },
    [rootSessionKey, setDrawerSessionForRoot, bumpCacheVersion],
  );

  const updateSessionAgentForKey = useCallback(
    (
      rootID: string,
      sessionKey: string,
      agent: string,
      model?: string,
      agentMode?: string,
      effort?: string,
      fastService?: "" | "on" | "off",
      planMode?: boolean,
      shell?: string,
    ) => {
      if (!rootID || !sessionKey || !agent) return;
      const hasPlanMode = typeof planMode === "boolean";
      const cacheKey = rootSessionKey(rootID, sessionKey);
      const cached = sessionCacheRef.current[cacheKey];
      if (cached) {
        sessionCacheRef.current[cacheKey] = {
          ...(cached as any),
          agent,
          model: model || "",
          mode: agentMode || "",
          effort: effort || "",
          fast_service: fastService || "",
          ...(hasPlanMode ? { plan_mode: planMode } : {}),
          updated_at: new Date().toISOString(),
        } as Session;
      }
      setSelectedSession((prev) => {
        const prevKey = prev?.key || prev?.session_key;
        const prevRoot =
          (prev?.root_id as string | undefined) || currentRootIdRef.current;
        if (!prev || prevKey !== sessionKey || prevRoot !== rootID) return prev;
        return {
          ...(prev as any),
          agent,
          model: model || "",
          mode: agentMode || "",
          effort: effort || "",
          fast_service: fastService || "",
          ...(hasPlanMode ? { plan_mode: planMode } : {}),
        } as SessionItem;
      });
      const current = drawerSessionByRootRef.current[scopedRootKey(rootID)];
      if (
        current &&
        current.key === sessionKey &&
        (current.agent !== agent ||
          (current as any).model !== (model || "") ||
          (current as any).mode !== (agentMode || "") ||
          (current as any).effort !== (effort || "") ||
          ((current as any).fast_service || "") !== (fastService || "") ||
          (hasPlanMode && !!(current as any).plan_mode !== planMode))
      ) {
        setDrawerSessionForRoot(rootID, {
          ...(current as any),
          agent,
          model: model || "",
          mode: agentMode || "",
          effort: effort || "",
          fast_service: fastService || "",
          ...(hasPlanMode ? { plan_mode: planMode } : {}),
        } as Session);
      }
      bumpCacheVersion();
    },
    [rootSessionKey, setDrawerSessionForRoot, bumpCacheVersion],
  );

  const syncSessionHeaderFromListItem = useCallback(
    (rootID: string, item: SessionItem | null | undefined) => {
      const sessionKey = item?.key || item?.session_key || "";
      const sessionName = typeof item?.name === "string" ? item.name : "";
      if (!rootID || !sessionKey || !sessionName) return;

      const cacheKey = rootSessionKey(rootID, sessionKey);
      const cached = sessionCacheRef.current[cacheKey];
      let changed = false;
      if (cached && cached.name !== sessionName) {
        sessionCacheRef.current[cacheKey] = {
          ...(cached as any),
          name: sessionName,
          updated_at: item?.updated_at || cached.updated_at,
        } as Session;
        changed = true;
      }

      setSelectedSession((prev) => {
        const prevKey = prev?.key || prev?.session_key;
        const prevRoot =
          (prev?.root_id as string | undefined) || currentRootIdRef.current;
        if (!prev || prevKey !== sessionKey || prevRoot !== rootID) return prev;
        if (prev.name === sessionName) return prev;
        return {
          ...(prev as any),
          name: sessionName,
          updated_at: item?.updated_at || prev.updated_at,
        } as SessionItem;
      });

      const drawer = drawerSessionByRootRef.current[scopedRootKey(rootID)];
      if (drawer?.key === sessionKey && drawer.name !== sessionName) {
        setDrawerSessionForRoot(rootID, {
          ...(drawer as any),
          name: sessionName,
          updated_at: item?.updated_at || drawer.updated_at,
        } as Session);
        changed = true;
      }

      if (changed) {
        bumpCacheVersion();
      }
    },
    [rootSessionKey, setDrawerSessionForRoot, bumpCacheVersion],
  );
  useEffect(() => {
    const rootID = currentRootIdRef.current;
    if (!rootID || sessions.length === 0) return;
    for (const item of sessions) {
      syncSessionHeaderFromListItem(rootID, item);
    }
  }, [sessions, syncSessionHeaderFromListItem]);

  const handleSetPlanMode = useCallback(
    async (enabled: boolean, targetSessionKey?: string, targetRootId?: string) => {
      const activeRoot = targetRootId || currentRootIdRef.current;
      const session = currentSessionRef.current || drawerSessionByRootRef.current[scopedRootKey(activeRoot || "")];
      const sessionKey = targetSessionKey || session?.key || (session as any)?.session_key;
      if (!activeRoot) {
        reportError("session.sync_failed", t("session.planModeSelectFirst"));
        return;
      }
      if (!sessionKey || String(sessionKey).startsWith("pending-")) {
        setPendingPlanMode(enabled);
        return;
      }
      const now = new Date().toISOString();
      const cacheKey = rootSessionKey(activeRoot, sessionKey);
      const cached = sessionCacheRef.current[cacheKey];
      if (cached) {
        sessionCacheRef.current[cacheKey] = {
          ...(cached as any),
          plan_mode: enabled,
          updated_at: now,
        } as Session;
      }
      setSelectedSession((prev) => {
        const prevKey = prev?.key || prev?.session_key;
        const prevRoot = (prev?.root_id as string | undefined) || activeRoot;
        if (!prev || prevKey !== sessionKey || prevRoot !== activeRoot) return prev;
        return { ...(prev as any), plan_mode: enabled, updated_at: now } as SessionItem;
      });
      const drawer = drawerSessionByRootRef.current[scopedRootKey(activeRoot)];
      if (drawer?.key === sessionKey) {
        setDrawerSessionForRoot(activeRoot, {
          ...(drawer as any),
          plan_mode: enabled,
          updated_at: now,
        } as Session);
      }
      bumpCacheVersion();
      const sent = await sessionService.setPlanMode(activeRoot, sessionKey, enabled);
      if (!sent) {
        reportError("network.disconnected", t("session.planModeSwitchFailedNotReady"));
      }
    },
    [rootSessionKey, setDrawerSessionForRoot, bumpCacheVersion, t],
  );

  const promotePendingSessionForRoot = useCallback(
    (
      rootID: string,
      tempKey: string | undefined,
      sessionKey: string,
      fallback?: Session | null,
    ) => {
      const pendingKey = (tempKey || "").trim();
      if (!rootID || !pendingKey || !sessionKey || pendingKey === sessionKey) {
        return;
      }

      const pendingCacheKey = rootSessionKey(rootID, pendingKey);
      const realCacheKey = rootSessionKey(rootID, sessionKey);
      const pendingCached = sessionCacheRef.current[pendingCacheKey];
      const realCached = sessionCacheRef.current[realCacheKey];
      const drawer = drawerSessionByRootRef.current[scopedRootKey(rootID)];
      const selected = selectedSessionRef.current;
      const selectedKey = selected?.key || selected?.session_key;
      const selectedRoot =
        (selected?.root_id as string | undefined) || currentRootIdRef.current;
      const pendingName =
        (typeof (pendingCached as any)?.name === "string" &&
        (pendingCached as any).name
          ? (pendingCached as any).name
          : "") ||
        (drawer?.key === pendingKey && typeof (drawer as any)?.name === "string"
          ? ((drawer as any).name as string)
          : "") ||
        (selectedKey === pendingKey &&
        selectedRoot === rootID &&
        typeof selected?.name === "string"
          ? selected.name
          : "") ||
        t("session.new");
      const realExchanges = Array.isArray((realCached as any)?.exchanges)
        ? ((realCached as any).exchanges as Exchange[])
        : [];
      const pendingExchanges = Array.isArray((pendingCached as any)?.exchanges)
        ? ((pendingCached as any).exchanges as Exchange[])
        : [];
      const latestReal =
        pendingCached && realCached
          ? ({
              ...(pendingCached as any),
              ...(realCached as any),
              exchanges:
                realExchanges.length > 0 ? realExchanges : pendingExchanges,
            } as Session)
          : realCached || pendingCached || fallback || drawer;
      let cacheChanged = false;

      if (pendingCached) {
        sessionCacheRef.current[realCacheKey] = {
          ...(latestReal as any),
          key: sessionKey,
          name:
            (typeof (realCached as any)?.name === "string" &&
            (realCached as any).name
              ? (realCached as any).name
              : "") || pendingName,
        } as Session;
        delete sessionCacheRef.current[pendingCacheKey];
        delete loadedSessionRef.current[pendingCacheKey];
        delete loadingSessionRef.current[pendingCacheKey];
        cacheChanged = true;
      }

      if (boundSessionByRootRef.current[scopedRootKey(rootID)] === pendingKey) {
        setBoundSessionForRoot(rootID, sessionKey);
      }
      if (selectedSessionByRootRef.current[scopedRootKey(rootID)] === pendingKey) {
        selectedSessionByRootRef.current[scopedRootKey(rootID)] = sessionKey;
      }
      if (drawer?.key === pendingKey) {
        setDrawerSessionForRoot(rootID, {
          ...(drawer as any),
          ...(latestReal as any),
          key: sessionKey,
          name:
            (typeof (latestReal as any)?.name === "string" &&
            (latestReal as any).name
              ? (latestReal as any).name
              : "") || pendingName,
        } as Session);
      }
      if (currentRootIdRef.current === rootID) {
        setSessions((prev) =>
          prev.map((item) => {
            const itemKey = item.key || item.session_key;
            if (itemKey !== pendingKey) {
              return item;
            }
            return {
              ...(item as any),
              ...(latestReal as any),
              key: sessionKey,
              session_key: sessionKey,
              root_id: rootID,
              name:
                (typeof (latestReal as any)?.name === "string" &&
                (latestReal as any).name
                  ? (latestReal as any).name
                  : "") || pendingName,
              pending: true,
            } as SessionItem;
          }),
        );
      }

      // pending 会话 promote 为真实会话时，同步替换右侧多项目列表中的临时条目
      setMultiProjectSessionGroups((prev) =>
        prev.map((group) => {
          if (group.rootId !== rootID) {
            return group;
          }
          const sessions = group.sessions.map((item) => {
            const itemKey = item.key || item.session_key;
            if (itemKey !== pendingKey) {
              return item;
            }
            return {
              ...(item as any),
              ...(latestReal as any),
              key: sessionKey,
              session_key: sessionKey,
              root_id: rootID,
              name:
                (typeof (latestReal as any)?.name === "string" &&
                (latestReal as any).name
                  ? (latestReal as any).name
                  : "") || pendingName,
              pending: true,
            } as SessionItem;
          });
          return { ...group, sessions: mergeSessionItems(sessions, []) };
        }),
      );

      setSelectedSession((prev) => {
        const prevKey = prev?.key || prev?.session_key;
        const prevRoot =
          (prev?.root_id as string | undefined) || currentRootIdRef.current;
        if (!prev || prevKey !== pendingKey || prevRoot !== rootID) return prev;
        return toSessionItem(rootID, {
          ...(prev as any),
          ...(latestReal as any),
          key: sessionKey,
          session_key: sessionKey,
          root_id: rootID,
          name:
            (typeof (latestReal as any)?.name === "string" &&
            (latestReal as any).name
              ? (latestReal as any).name
              : "") || pendingName,
        });
      });

      if (cacheChanged) {
        bumpCacheVersion();
      }
    },
    [rootSessionKey, setBoundSessionForRoot, setDrawerSessionForRoot, bumpCacheVersion],
  );

  const {
    resolveRuntimeMetaForSession,
    appendAgentChunkForSession,
    appendThoughtChunkForSession,
    appendToolCallForSession,
    appendTodoUpdateForSession,
    appendPlanUpdateForSession,
    appendCompactNoticeForSession,
  } = useSessionStreamCache({
    sessionCacheRef,
    rootSessionKey,
    bumpCacheVersionDebounced,
    currentSessionRef,
    selectedSessionRef,
  });

  const attachContextWindowToLatestAssistant = useCallback(
    (
      rootID: string,
      sessionKey: string,
      contextWindow?: { totalTokens?: number; modelContextWindow?: number },
      tokenUsage?: TokenUsage,
    ) => {
      const totalTokens = Math.max(0, Number(contextWindow?.totalTokens || 0));
      const modelContextWindow = Math.max(0, Number(contextWindow?.modelContextWindow || 0));
      const hasContextWindow = totalTokens > 0 && modelContextWindow > 0;
      const normalizedTokenUsage = tokenUsage
        ? {
            inputTokens: Math.max(0, Number(tokenUsage.inputTokens || 0)),
            outputTokens: Math.max(0, Number(tokenUsage.outputTokens || 0)),
            ...(Number.isFinite(tokenUsage.cacheReadTokens)
              ? { cacheReadTokens: Math.max(0, Number(tokenUsage.cacheReadTokens)) }
              : {}),
            ...(Number.isFinite(tokenUsage.cacheWriteTokens)
              ? { cacheWriteTokens: Math.max(0, Number(tokenUsage.cacheWriteTokens)) }
              : {}),
          }
        : undefined;
      if (!hasContextWindow && !normalizedTokenUsage) {
        return;
      }
      const cacheKey = rootSessionKey(rootID, sessionKey);
      const stampList = (prevList: Exchange[]) => {
        const list = [...(prevList || [])];
        for (let i = list.length - 1; i >= 0; i -= 1) {
          const item = list[i];
          if (
            (item?.role === "agent" || item?.role === "assistant") &&
            String(item?.content || "").trim()
          ) {
            list[i] = {
              ...item,
              ...(hasContextWindow
                ? { context_window: { totalTokens, modelContextWindow } }
                : {}),
              ...(normalizedTokenUsage
                ? { token_usage: normalizedTokenUsage }
                : {}),
            };
            break;
          }
        }
        return list;
      };
      const cached = sessionCacheRef.current[cacheKey];
      if (cached) {
        const exchanges = stampList((((cached as any).exchanges || []) as Exchange[]));
        sessionCacheRef.current[cacheKey] = {
          ...(cached as any),
          exchanges,
          ...(hasContextWindow
            ? { context_window: { totalTokens, modelContextWindow } }
            : {}),
          updated_at: new Date().toISOString(),
        } as Session;
      }
      setSelectedSession((prev) => {
        const prevRoot =
          (prev?.root_id as string | undefined) || currentRootIdRef.current;
        if (!prev || prevRoot !== rootID || (prev.key || prev.session_key) !== sessionKey) {
          return prev;
        }
        return {
          ...(prev as any),
          exchanges: stampList((((prev as any).exchanges || []) as Exchange[])),
          ...(hasContextWindow
            ? { context_window: { totalTokens, modelContextWindow } }
            : {}),
        } as SessionItem;
      });
      const drawer = drawerSessionByRootRef.current[scopedRootKey(rootID)];
      if (drawer && drawer.key === sessionKey) {
        setDrawerSessionForRoot(rootID, {
          ...(drawer as any),
          exchanges: stampList((((drawer as any).exchanges || []) as Exchange[])),
          ...(hasContextWindow
            ? { context_window: { totalTokens, modelContextWindow } }
            : {}),
        } as Session);
      }
      // 列表 state 同步：message_done 直推的 context_window 只进了 cache/drawer/selected，
      // 会话列表项没有数据源（列表接口不含该字段），不补这里则徽标停在旧值，直到全量重拉。
      setSessions((prev) =>
        prev.map((item) => {
          const itemKey = item.key || item.session_key;
          if (
            itemKey !== sessionKey ||
            (item.root_id as string | undefined) !== rootID
          ) {
            return item;
          }
          return {
            ...item,
            context_window: {
              totalTokens,
              modelContextWindow,
            },
          };
        }),
      );
      bumpCacheVersion();
    },
    [bumpCacheVersion, rootSessionKey, setSessions],
  );

  const normalizeTreeResponse = useCallback((payload: any) => {
    if (payload && payload.entries)
      return { entries: payload.entries as FileEntry[] };
    return { entries: [] };
  }, []);

  const formatDirectoryLoadError = useCallback((message?: string | null) => {
    const text = String(message || "").trim();
    if (!text) {
      return t("directory.readFailed");
    }
    const lower = text.toLowerCase();
    if (
      lower.includes("access is denied") ||
      lower.includes("permission denied") ||
      lower.includes("operation not permitted") ||
      lower.includes("拒绝访问") ||
      lower.includes("权限")
    ) {
      return t("directory.permissionDenied");
    }
    return text;
  }, [t]);

  const treeCacheKey = useCallback(
    (rootID: string, dirPath: string) =>
      treeKey(getNodeIdForRoot(rootID) ?? "", rootID, dirPath || "."),
    [getNodeIdForRoot],
  );

  const refreshTreeDir = useCallback(
    async (rootID: string, dirPath: string, syncMain: boolean) => {
      const cacheKey = treeCacheKey(rootID, dirPath);
      // 并发守卫：同目录并发刷新（按钮 × WS 事件 × 导航）时只让最后发起的请求生效，
      // 防止先发起的旧响应后返回覆盖新数据（“点了刷新仍显示旧内容”的来源之一）。
      treeFetchSeqRef.current[cacheKey] = (treeFetchSeqRef.current[cacheKey] || 0) + 1;
      const seq = treeFetchSeqRef.current[cacheKey];
      // syncMain 时须仍在查看该目录才允许回写主视图，防止导航后旧响应覆盖新视图
      const matchesView = () =>
        rootID === currentRootIdRef.current &&
        (selectedDirRef.current === rootID ? "." : selectedDirRef.current || ".") === dirPath;
      try {
        const payload = await apiProtectedJSON<any>(
          appURL("/api/tree", new URLSearchParams({ root: rootID, dir: dirPath }), getNodeIdForRoot(rootID)),
        );
        if (treeFetchSeqRef.current[cacheKey] !== seq) {
          return; // 旧响应已过时，丢弃
        }
        const parsed = normalizeTreeResponse(payload);
        invalidTreeCacheKeysRef.current.delete(cacheKey);
        setEntriesByPath((prev) => ({
          ...prev,
          [cacheKey]: parsed.entries,
        }));
        if (syncMain && matchesView()) {
          setMainDirectoryError("");
          setMainEntries(parsed.entries);
        }
      } catch (error) {
        if (treeFetchSeqRef.current[cacheKey] !== seq) {
          return; // 已被更新的请求取代，错误也一并丢弃
        }
        // 失败后不信任旧缓存：标记该目录缓存失效，下次查看/刷新强制重拉真实状态
        invalidTreeCacheKeysRef.current.add(cacheKey);
        if (syncMain && matchesView()) {
          const message = String(
            (error as any)?.payload?.error || (error as any)?.payload?.message || (error as Error)?.message || "",
          );
          const display = formatDirectoryLoadError(message);
          setMainDirectoryError(display);
          reportError("file.read_failed", display, { severity: "warning", recoverable: true });
        }
      }
    },
    [formatDirectoryLoadError, getNodeIdForRoot, treeCacheKey],
  );

  const refreshCurrentFileContent = useCallback(
    async (rootID: string, changedPath: string) => {
      const currentFile = fileRef.current;
      if (!currentFile) return;
      const currentRoot = currentFile.root || currentRootIdRef.current || "";
      if (currentRoot !== rootID || currentFile.path !== changedPath) return;

      let readMode: "incremental" | "full" = "incremental";
      if (!pluginBypassRef.current) {
        try {
          const plugin = pluginManagerRef.current.match(
            rootID,
            buildMatchInputFromPath(changedPath, pluginQueryRef.current),
          );
          readMode = inferReadModeFromPlugin(plugin);
        } catch {
          readMode = "incremental";
        }
      }

      try {
        const next = await fetchFile({
          rootId: rootID,
          path: changedPath,
          nodeId: getNodeIdForRoot(String(rootID)),
          readMode,
          cursor: fileCursorRef.current || 0,
        });
        const latestFile = fileRef.current;
        const latestRoot = latestFile?.root || currentRootIdRef.current || "";
        if (
          !next ||
          !latestFile ||
          latestRoot !== rootID ||
          latestFile.path !== changedPath
        ) {
          return;
        }
        setFile({
          ...next,
          targetLine: latestFile.targetLine,
          targetColumn: latestFile.targetColumn,
        });
      } catch (err) {
        console.error("[file.refresh.changed] failed", {
          rootID,
          changedPath,
          err,
        });
      }
    },
    [],
  );

  const loadSessionsForRoot = useCallback(
    async (
      rootID: string,
      options?: {
        beforeTime?: string;
        afterTime?: string;
        replace?: boolean;
        force?: boolean;
      },
    ) => {
      const _nid = getNodeIdForRoot(rootID);
      const snapNode = String(_nid || "").trim();
      const snapRoot = String(rootID || "").trim();
      const seq = ++sessionListLoadSeqRef.current;
      sessionListAbortRef.current?.abort();
      const controller = new AbortController();
      sessionListAbortRef.current = controller;
      try {
        const shouldReplace = options?.replace || (!options?.beforeTime && !options?.afterTime);
        if (shouldReplace) {
          const cached = await getCachedSessionList(rootID, _nid);
          if (controller.signal.aborted || seq !== sessionListLoadSeqRef.current) { return; }
          if ((String(getNodeIdForRoot(rootID) || "").trim()) !== snapNode) { return; }
          if (cached && (options?.force || currentRootIdRef.current === rootID)) {
            const cachedItems = [...cached.items, ...cached.pinnedItems]
              .map((item) => toSessionItem(rootID, { ...(item as any), _nodeId: (item as any)._nodeId || snapNode }))
              .filter((item): item is SessionItem => !!item);
            setHasMoreSessions(cached.totalCount > cached.items.length);
            setSessions(
              applyPinnedSnapshotToSessions(
                mergeSessionItems([], cachedItems),
                rootID,
                cached.pinnedKeys,
              ),
            );
          }
        }
        const payload = await sessionService.fetchSessions(rootID, {
          nodeId: _nid,
          beforeTime: options?.beforeTime,
          afterTime: options?.afterTime,
        });
        if (controller.signal.aborted || seq !== sessionListLoadSeqRef.current) { return; }
        if ((String(getNodeIdForRoot(rootID) || "").trim()) !== snapNode) { return; }
        if (String(currentRootIdRef.current || "").trim() !== snapRoot && !options?.force) { return; }
        const next = [
          ...payload.items,
          ...payload.pinnedItems,
        ].map((item) => toSessionItem(rootID, { ...(item as any), _nodeId: (item as any)._nodeId || snapNode })).filter((item): item is SessionItem => !!item);
        // D3: 会话打标，便于切节点过滤与 key 隔离
        if (!options?.force && currentRootIdRef.current !== rootID) return;
        // 迟到覆盖已在 seq/snap 校验后再次用 currentRootId 守卫
        if (!options?.force && String(currentRootIdRef.current || "").trim() !== snapRoot) return;
        setHasMoreSessions(payload.totalCount > payload.items.length);
        if (shouldReplace) {
          // 服务端列表接口不含 context_window，全量重拉会把它清掉（WS message_done 已同步进本地列表）。
          // 用 setSessions 回调继承旧列表的 context_window，避免徽标闪没。
          setSessions((prev) => {
            const prevContext = new Map(
              prev
                .filter((item) => item.context_window)
                .map((item) => [
                  String(item.key || item.session_key || ""),
                  item.context_window,
                ]),
            );
            const merged = mergeSessionItems([], next).map((item) => {
              const itemKey = String(item.key || item.session_key || "");
              const inherited = prevContext.get(itemKey);
              return inherited
                ? { ...item, context_window: inherited }
                : item;
            });
            return applyPinnedSnapshotToSessions(merged, rootID, payload.pinnedKeys);
          });
          void saveCachedSessionList(rootID, payload, _nid);
          return;
        }
        setSessions((prev) =>
          applyPinnedSnapshotToSessions(mergeSessionItems(prev, next), rootID, payload.pinnedKeys),
        );
      } catch (err) {
        if ((err as any)?.name === "AbortError") return;
        if (controller.signal.aborted) return;
        // 列表拉取失败不再静默：WS 抖动时列表会停留旧状态（2026-09-09 排查），
        // 可见告警让用户知道需要手动同步；10s 冷却防断网期间刷屏。
        if (Date.now() - listLoadErrorAtRef.current > 10000) {
          listLoadErrorAtRef.current = Date.now();
          reportError(
            "session.list_load_failed",
            String((err as Error)?.message || err),
            { severity: "warning", recoverable: true },
          );
        }
      }
    },
    [getNodeIdForRoot],
  );

  const sessionListReloadTimerRef = useRef<number | null>(null);

  // WS 高频事件（session.done / ws.reconnected / session.imported / session.created）都会触发会话列表全量重拉，
  // 合并 300ms 窗口内的多次调用为最后一次，避免 agent 密集工具调用时连发 /api/sessions。
  const scheduleSessionListReload = useCallback(
    (
      rootID: string,
      options?: {
        beforeTime?: string;
        afterTime?: string;
        replace?: boolean;
        force?: boolean;
      },
    ) => {
      if (sessionListReloadTimerRef.current) {
        window.clearTimeout(sessionListReloadTimerRef.current);
      }
      sessionListReloadTimerRef.current = window.setTimeout(() => {
        sessionListReloadTimerRef.current = null;
        void loadSessionsForRoot(rootID, options);
      }, 300);
    },
    [loadSessionsForRoot],
  );

  const loadChildSessionsForParent = useCallback(
    async (
      parent: SessionItem,
      options?: { beforeTime?: string },
    ): Promise<{ hasMore: boolean }> => {
      const rootID =
        (parent.root_id as string | undefined) || currentRootIdRef.current || "";
      const parentKey = parent.key || parent.session_key || "";
      if (!rootID || !parentKey) {
        return { hasMore: false };
      }
      const items = await sessionService.fetchChildSessions(rootID, parentKey, {
        beforeTime: options?.beforeTime,
        limit: CHILD_SESSION_PAGE_SIZE,
        nodeId: (parent as any)?._nodeId || getNodeIdForRoot(rootID),
      });
      const next = items
        .map((item) => toSessionItem(rootID, item))
        .filter((item): item is SessionItem => !!item);
      if (currentRootIdRef.current === rootID && next.length > 0) {
        setSessions((prev) => mergeSessionItems(prev, next));
      }
      if (next.length > 0) {
        setMultiProjectSessionGroups((prev) =>
          applyPendingToMultiProjectGroups(
            prev.map((group) =>
              group.rootId === rootID && (group as any)._nodeId === (parent as any)?._nodeId
                ? { ...group, sessions: mergeSessionItems(group.sessions, next) }
                : group,
            ),
            multiProjectPendingRef.current,
          ),
        );
      }
      return { hasMore: items.length >= CHILD_SESSION_PAGE_SIZE };
    },
    [applyPendingToMultiProjectGroups, mergeSessionItems],
  );

  const loadMultiProjectSessionGroups = useCallback(async () => {
    if (!multiProjectSessionsEnabled || !protectedAPIReady()) {
      return;
    }
    const seq = ++multiProjectLoadSeqRef.current;
    setMultiProjectSessionsLoading(true);
    try {
      const cachedGroups = await getCachedMultiRootSessionList();
      if (cachedGroups?.length) {
        // 缓存先渲染（避免闪空），但**必须整体替换**而不是merge：这份缓存是按账户存的，
        // 换账户后它就是空的，若还往里合并就会把上一账户的会话留在屏上。
        setMultiProjectSessionGroups(
          applyPendingToMultiProjectGroups(
            cachedGroups.map((group): MultiProjectSessionGroup => {
              const cacheNid = String((group as any)?._nodeId || "").trim();
              return {
              rootId: group.rootId,
              rootName: group.rootName || managedRootByIdRef.current[group.rootId]?.display_name || group.rootId,
              // C1: 缓存重建必须补节点与颜色，否则整帧回退 PALETTE[0] 蓝 + nodeIndex=99 乱序
              _nodeId: cacheNid || undefined,
              _nodeColor: resolveGroupColor(group as any, managedRootByKeyRef.current as any, getNodes() as any) || undefined,
              latestSessionTime: group.latestSessionTime,
              sessions: applyPinnedSnapshotToSessions(
                mergeSessionItems(
                  [],
                  [...group.items, ...group.pinnedItems]
                    .map((item) => toSessionItem(group.rootId, { ...(item as any), root_id: group.rootId, _nodeId: cacheNid || undefined }))
                    .filter((item): item is SessionItem => !!item),
                ),
                group.rootId,
                group.pinnedKeys,
              ),
              totalCount: group.totalCount,
            };}),
            multiProjectPendingRef.current,
          ),
        );
      }
      const nodeIdsFromNodes = getNodes()
        .map((n) => String((n as any)?.id || "").trim())
        .filter(Boolean);
      const nodeIdsFromRoots = Array.from(
        new Set(
          Object.values(managedRootByKeyRef.current as Record<string, any>)
            .map((v) => String((v as any)?._nodeId || "").trim())
            .filter(Boolean),
        ),
      );
      const nodeIds = Array.from(new Set([...nodeIdsFromNodes, ...nodeIdsFromRoots]));
      const allGroups: Array<MultiRootSessionGroup & { _nodeId?: string }> = [];
      let nodeFetchResults: Array<Array<MultiRootSessionGroup & { _nodeId?: string }>> = [];
      // 只有**真的请求失败**的节点才保留旧分组（网络抖动时不清屏）；
      // 成功但为空 = 该账户在这个节点下确实没有会话，必须让空结果生效。
      let failedNids = new Set<string>();
      if (nodeIds.length === 0) {
        // managedRootByKeyRef 未就绪（初始化竞态）：以当前激活节点回退打标，
        // 避免首批分组 _nodeId 为空串、与 selectRootNode 后的 currentRootNodeId 不等而全部收起
        const fallbackNodeId =
          getNodeIdForRoot(String(currentRootIdRef.current || "")) ||
          String(getActiveNode()?.id || "").trim();
        try {
          const groups = await sessionService.fetchMultiRootSessions(MULTI_PROJECT_SESSION_LIMIT);
          for (const g of groups) allGroups.push({ ...g, _nodeId: fallbackNodeId });
        } catch {
          notifyNodeLoadFailedRef.current({
            id: fallbackNodeId,
            name: String(getActiveNode()?.name || fallbackNodeId),
          });
        }
      } else {
        // 跨机器也带 user=（用户名），服务端解析到对方本地的账户；对方没有这个账户时
        // 回 **200 空列表**，所以这里不需要「缺账户」分支——空就是空。
        // 但要区分「拉取失败」与「成功但为空」：两者都是 []，混为一谈会让下面把**上一个账户**
        // 的分组当成「该节点本次没返回」保留下来，于是新账户看到旧账户的全部会话（实测踩过）。
        const failed: boolean[] = [];
        nodeFetchResults = await Promise.all(nodeIds.map(async (nid, i) => {
          try {
            const gs = await sessionService.fetchMultiRootSessions(MULTI_PROJECT_SESSION_LIMIT, nid);
            failed[i] = false;
            return gs.map((g) => ({ ...g, _nodeId: nid }));
          } catch (err) {
            failed[i] = true;
            notifyNodeLoadFailedRef.current({
              id: String(nid),
              name: String(getNodeById(String(nid))?.name || nid),
            });
            return [] as Array<MultiRootSessionGroup & { _nodeId?: string }>;
          }
        }));
        failedNids = new Set(nodeIds.filter((_, i) => failed[i]));
        for (const groups of nodeFetchResults) allGroups.push(...groups);
      }
      const dedup = new Map<string, MultiRootSessionGroup & { _nodeId?: string }>();
      for (const g of allGroups) { const key = `${(g as any)._nodeId || ""}::${g.rootId}`; const prev = dedup.get(key); if (!prev || String((g as any).latestSessionTime || "") > String((prev as any).latestSessionTime || "")) dedup.set(key, g); }
      const groups = Array.from(dedup.values());
      const nextGroups = groups.map((group: MultiRootSessionGroup): MultiProjectSessionGroup => {
        const gNid = String((group as any)._nodeId || "").trim();
        const gMeta = (managedRootByKeyRef.current as Record<string, any>)[rootNodeKey(gNid, group.rootId)];
        return {
        rootId: group.rootId,
        rootName: group.rootName || gMeta?.display_name || group.rootId,
        _nodeColor: (gMeta?._nodeColor as string | undefined),
        _nodeId: (gMeta?._nodeId as string | undefined) || gNid || undefined,
        latestSessionTime: group.latestSessionTime,
        sessions: applyPinnedSnapshotToSessions(
          mergeSessionItems(
            [],
            [...group.items, ...group.pinnedItems]
              .map((item) => toSessionItem(group.rootId, { ...(item as any), root_id: group.rootId, _nodeId: gNid }))
              .filter((item): item is SessionItem => !!item),
          ),
          group.rootId,
          group.pinnedKeys,
        )
          .filter((item): item is SessionItem => !!item),
        totalCount: group.totalCount,
      }});
      if (seq !== multiProjectLoadSeqRef.current) return; // 丢弃过期响应
      // C2: 按 nid::rootId 逐组合并（保留本次未返回节点的上次分组），消除整体替换造成的项目消失/穿插帧。
      // 换账户后必须清掉旧账户的分组：凡本轮**成功**拉取过的节点，其旧分组一律不得保留——
      // 空结果（该账户在此节点没有会话）同样是有效答案，不是「没拉到」。
      setMultiProjectSessionGroups((prev) => {
        const prevList = Array.isArray(prev) ? prev : [];
        const scopeOf = (g: any) => `${String(g?._nodeId || "").trim()}::${String(g?.rootId || "")}`;
        const nextMap = new Map(nextGroups.map((g) => [scopeOf(g), g]));
        const ridSet = new Set(nextGroups.map((g) => String(g.rootId || "")).filter(Boolean));
        const queriedNids = new Set(nodeIds); // 本轮实际请求过的节点
        const merged: MultiProjectSessionGroup[] = [];
        const seen = new Set<string>();
        for (const g of prevList) {
          const nid = String((g as any)?._nodeId || "").trim();
          if (failedNids.has(nid)) {
            merged.push(g);
            continue;
          }
          const k = scopeOf(g);
          if (nextMap.has(k)) {
            merged.push(nextMap.get(k)!);
            seen.add(k);
            continue;
          }
          if (queriedNids.has(nid)) continue; // 成功拉取却未返回此分组 → 该账户下确实没有，清掉
          if (!nid && ridSet.has(String(g.rootId || ""))) continue; // 节点盲缓存组：被新数据取代
          merged.push(g);
        }
        for (const g of nextGroups) {
          const k = scopeOf(g);
          if (!seen.has(k)) {
            merged.push(g);
            seen.add(k);
          }
        }
        return applyPendingToMultiProjectGroups(merged, multiProjectPendingRef.current);
      });
      // 保存时补 _nodeColor，令缓存重建帧直接有色（C1 双保险）
      void saveCachedMultiRootSessionList(
        groups.map((g) => ({
          ...g,
          _nodeColor: resolveGroupColor(g as any, managedRootByKeyRef.current as any, getNodes() as any) || undefined,
        })) as any,
      );
    } finally {
      if (seq === multiProjectLoadSeqRef.current) {
        setMultiProjectSessionsLoading(false);
      }
    }
  }, [applyPendingToMultiProjectGroups, multiProjectSessionsEnabled]);

  const loadMoreMultiProjectSessions = useCallback(
    async (group: ProjectSessionGroup) => {
      const topLevelSessions = group.sessions.filter(isTopLevelSessionItem);
      const oldest = topLevelSessions[topLevelSessions.length - 1]?.updated_at || "";
      if (!group.rootId || !oldest) {
        return;
      }
      const previousLoaded = topLevelSessions.length;
      const groupNid = String((group as any)._nodeId || "").trim();
      const payload = await sessionService.fetchSessions(group.rootId, {
        nodeId: groupNid || getNodeIdForRoot(group.rootId),
        beforeTime: oldest,
        limit: SESSION_PAGE_SIZE,
        topLevel: true,
        includeChildren: true,
      });
      const nextItems = payload.items
        .concat(payload.pinnedItems)
        .map((item) => toSessionItem(group.rootId, { ...(item as any), root_id: group.rootId, _nodeId: groupNid }))
        .filter((item): item is SessionItem => !!item);
      setMultiProjectSessionGroups((prev) =>
        applyPendingToMultiProjectGroups(
          prev.map((current) => {
            if (String((current as any)._nodeId || "").trim() !== groupNid || current.rootId !== group.rootId) {
              return current;
            }
            const sessions = applyPinnedSnapshotToSessions(
              mergeSessionItems(current.sessions, nextItems),
              group.rootId,
              payload.pinnedKeys,
            );
            return {
              ...current,
              sessions,
              totalCount: previousLoaded + payload.totalCount,
            };
          }),
          multiProjectPendingRef.current,
        ),
      );
    },
    [applyPendingToMultiProjectGroups, mergeSessionItems],
  );

  const refreshMultiProjectReplyingSessions = useCallback(async () => {
    try {
      const payload = await apiProtectedJSON<any>(appPath("/api/replying-sessions"));
      const items = Array.isArray(payload?.sessions) ? payload.sessions : [];
      const next: Record<string, boolean> = {};
      for (const item of items) {
        const rootID = String(item?.rootId || item?.root_id || "");
        const sessionKey = String(item?.sessionKey || item?.session_key || "");
        if (rootID && sessionKey) {
          next[rootSessionKey(rootID, sessionKey)] = true;
        }
      }
      multiProjectPendingRef.current = next;
      setMultiProjectPendingByKey(next);
      setMultiProjectSessionGroups((groups) => applyPendingToMultiProjectGroups(groups, next));
    } catch (error) {
      console.warn("[multi-project-sessions] replying refresh failed", error);
    }
  }, [applyPendingToMultiProjectGroups, rootSessionKey]);

  useEffect(() => {
    if (!multiProjectSessionsEnabled) {
      return;
    }
    void refreshMultiProjectReplyingSessions();
    void loadMultiProjectSessionGroups();
  }, [loadMultiProjectSessionGroups, multiProjectSessionsEnabled, refreshMultiProjectReplyingSessions]);

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


  const handleTreeUpload = useCallback(
    async (files: File[]) => {
      const rootID = currentRootIdRef.current;
      if (!rootID || files.length === 0) return;

      const selectedDirPath =
        selectedDirRef.current === rootID ? "." : selectedDirRef.current;
      const targetDir =
        selectedDirPath ||
        (fileRef.current?.path ? dirnameOfPath(fileRef.current.path) : ".");
      setDirectoryUploadProgress(null);
      try {
        const uploadAbort = new AbortController();
        directoryUploadAbortRef.current = uploadAbort;
        const uploaded = await uploadFiles({
          rootId: rootID,
          dir: targetDir,
          files,
          onProgress: setDirectoryUploadProgress,
          signal: uploadAbort.signal,
          nodeId: getNodeIdForRoot(rootID),
        });
        uploaded.forEach((item) => {
          if (typeof item?.path === "string" && item.path) {
            invalidateFileCache(rootID, item.path);
          }
        });
        const currentDir =
          (selectedDirRef.current === rootID ? "." : selectedDirRef.current) ||
          ".";
        const syncMain =
          rootID === currentRootIdRef.current && currentDir === targetDir;
        await refreshTreeDir(rootID, targetDir, syncMain);
        setExpanded((prev) =>
          Array.from(
            new Set([
              ...prev,
              scopeKey(getNodeIdForRoot(rootID) ?? "", rootID),
              expandKey(
                getNodeIdForRoot(rootID),
                rootID,
                targetDir === "." ? rootID : targetDir,
                targetDir === ".",
              ),
            ]),
          ),
        );
      } catch (err) {
        if (!isUploadAbortError(err)) {
          reportError(
            "file.write_failed",
            String((err as Error)?.message || t("file.uploadFailed")),
          );
        }
      } finally {
        directoryUploadAbortRef.current = null;
        setDirectoryUploadProgress(null);
      }
    },
    [refreshTreeDir, t],
  );

  const handleSelectSession = useCallback(
    async (
      session: any,
      options?: { preserveTaskSelection?: boolean; preserveMainView?: boolean },
    ) => {
      const key = session?.key || session?.session_key;
      const targetRoot =
        (session?.root_id as string | undefined) || currentRootIdRef.current;
      if (!targetRoot || !key) return;
      if (!options?.preserveTaskSelection) {
        setSelectedKanbanTaskId("");
      }
      // 多节点同名项目：会话归属节点优先于当前选中节点（跨节点切换会清理旧会话态）
      const sessionNode = String((session as any)?._nodeId || "").trim();
      const nextNode =
        sessionNode ||
        (targetRoot === String(currentRootIdRef.current || "")
          ? String(currentRootNodeIdRef.current || "")
          : "") ||
        getNodeIdForRoot(targetRoot) ||
        "";
      if (
        targetRoot !== String(currentRootIdRef.current || "") ||
        (sessionNode && sessionNode !== String(currentRootNodeIdRef.current || ""))
      ) {
        setCurrentRootId(targetRoot);
        selectRootNode(targetRoot, nextNode || undefined);
      }
      setBoundSessionForRoot(targetRoot, key);
      setDrawerSessionForRoot(targetRoot, toSessionItem(targetRoot, session));
      setSelectedDir(targetRoot);
      setSelectedDirKey(
        buildDirectorySelectionKey(getNodeIdForRoot(targetRoot), targetRoot, targetRoot, true),
      );
      // 深链恢复时主面板模式以 URL 的 view 为准，不因为「有 session」就把面板硬拉回对话态。
      if (!options?.preserveMainView) {
        switchMainView("chat");
      }
      const currentDrawer = drawerSessionByRootRef.current[scopedRootKey(targetRoot)];
      const preservePending =
        currentDrawer?.key === key
          ? !!(currentDrawer as any)?.pending
          : !!(session as any)?.pending;
      const searchTargetId =
        typeof session?.search_seq === "number"
          ? `${key}:${session.search_seq}:${++sessionSearchTargetCounterRef.current}`
          : undefined;
      replaceURLState({
        root: targetRoot,
        file: "",
        session: key,
        cursor: 0,
        pluginQuery: {},
      });
      selectedSessionByRootRef.current[scopedRootKey(targetRoot)] = key;
      const cacheKey = rootSessionKey(targetRoot, key);
      setSelectedSessionLoading(true);
      setSelectedSession(
        toSessionItem(targetRoot, {
          ...(session as any),
          pending: preservePending,
          search_target_id: searchTargetId || session?.search_target_id,
        }),
      );
      setInteractionMode("main");
      setDrawerOpenForRoot(targetRoot, false);
      if (isMobile) setIsRightOpen(false);
      await waitForNextPaint();
      const applySession = (
        fullSession: Session,
        options?: { writeCache?: boolean },
      ) => {
        const shouldWriteCache = options?.writeCache !== false;
        const serverPending =
          typeof (fullSession as any)?.pending === "boolean"
            ? !!(fullSession as any).pending
            : undefined;
        const pending =
          serverPending !== undefined
            ? serverPending
            : resolvePendingForSession(targetRoot, key, preservePending);
        const normalized = {
          ...(fullSession as any),
          key,
          pending,
        } as Session;
        if (shouldWriteCache) {
          sessionCacheRef.current[cacheKey] = normalized;
        }
        setSelectedSession((prev) => {
          const prevKey = prev?.key || prev?.session_key;
          const prevRoot =
            (prev?.root_id as string | undefined) || currentRootIdRef.current;
          if (prevKey !== key || prevRoot !== targetRoot) {
            return prev;
          }
          return toSessionItem(targetRoot, {
            ...(prev as any),
            ...(normalized as any),
            key,
            session_key: key,
            root_id: targetRoot,
          });
        });
        if ((boundSessionByRootRef.current[scopedRootKey(targetRoot)] || null) === key) {
          setDrawerSessionForRoot(targetRoot, {
            ...(normalized as any),
          } as Session);
        }
        setSelectedSessionLoading(false);
        if (shouldWriteCache) {
          bumpCacheVersion();
        }
      };
      // 先即时渲染本地缓存（快），再始终同步到最新：
      // 移除原"已缓存/已加载就跳过 sync"的短路，避免陈旧缓存导致历史加载/更新滞后（#sync-lag）。
      const cached = sessionCacheRef.current[cacheKey];
      if (cached) {
        applySession(cached);
      } else {
        const persisted = await getCachedSession(targetRoot, key, getNodeIdForRoot(targetRoot));
        if (persisted) {
          applySession(persisted);
        }
      }
      try {
        const restored = await restoreActiveSession(targetRoot, key);
        if (restored) {
          applySession(restored, { writeCache: false });
          loadedSessionRef.current[cacheKey] = true;
          clearSessionStale(targetRoot, key);
        } else {
          setSelectedSessionLoading(false);
        }
      } catch (err) {
        setSelectedSessionLoading(false);
      }
    },
    [
      isMobile,
      rootSessionKey,
      bumpCacheVersion,
      clearSessionStale,
      isSessionStale,
      resolvePendingForSession,
      restoreActiveSession,
      setDrawerOpenForRoot,
      setDrawerSessionForRoot,
      switchMainView,
      replaceURLState,
    ],
  );

  const tryShowBoundSessionForRoot = useCallback(
    async (
      rootID: string | null | undefined,
      options?: {
        pluginQuery?: Record<string, string>;
        closeLeftSidebar?: boolean;
      },
    ): Promise<boolean> => {
      const resolvedRoot = String(rootID || "");
      if (!resolvedRoot) {
        return false;
      }
      if (mainViewRef.current !== "chat") {
        return false;
      }
      const selectedKey = String(
        selectedSessionByRootRef.current[scopedRootKey(resolvedRoot)] || "",
      ).trim();
      const boundKey = String(
        boundSessionByRootRef.current[scopedRootKey(resolvedRoot)] || "",
      ).trim();
      const drawerKey = String(
        drawerSessionByRootRef.current[scopedRootKey(resolvedRoot)]?.key || "",
      ).trim();
      const preferredKey =
        (selectedKey && !selectedKey.startsWith("pending-") ? selectedKey : "") ||
        (boundKey && !boundKey.startsWith("pending-") ? boundKey : "") ||
        (drawerKey && !drawerKey.startsWith("pending-") ? drawerKey : "");
      if (!preferredKey) {
        return false;
      }
      if (currentRootIdRef.current !== resolvedRoot) {
        setCurrentRootId(resolvedRoot);
      }
      setGitDiff(null);
      setFile(null);
      setMainEntries([]);
      setPluginQuery(options?.pluginQuery || {});
      setSelectedDir(resolvedRoot);
      setSelectedDirKey(
        buildDirectorySelectionKey(getNodeIdForRoot(resolvedRoot), resolvedRoot, resolvedRoot, true),
      );
      fileCursorRef.current = 0;
      const cacheKey = rootSessionKey(resolvedRoot, preferredKey);
      let initialSession =
        sessionCacheRef.current[cacheKey] ||
        (await getCachedSession(resolvedRoot, preferredKey, getNodeIdForRoot(resolvedRoot)));
      if (initialSession) {
        sessionCacheRef.current[cacheKey] = {
          ...(initialSession as any),
          key: preferredKey,
        } as Session;
      }
      await handleSelectSession(
        initialSession
          ? ({
              ...(initialSession as any),
              key: preferredKey,
              session_key: preferredKey,
              root_id: resolvedRoot,
            } as SessionItem)
          : {
              key: preferredKey,
              session_key: preferredKey,
              root_id: resolvedRoot,
            },
      );
      if (options?.closeLeftSidebar && isMobile) {
        setIsLeftOpen(false);
      }
      return true;
    },
    [handleSelectSession, isMobile, rootSessionKey, bumpCacheVersion],
  );

  // 稳定回调：SessionList/SessionCard 的 memo 依赖 props 引用稳定（onSelect 之前是 inline 箭头，每次父渲染都变）。
  const handleSelectSessionAndClose = useCallback(
    (session: SessionItem) => {
      void handleSelectSession(session);
      if (isMobile) setIsRightOpen(false);
    },
    [handleSelectSession, isMobile],
  );

  const handleDeleteSession = useCallback(
    async (session: SessionItem) => {
      const sessionKey = session?.key || session?.session_key;
      const rootID =
        (session?.root_id as string | undefined) || currentRootIdRef.current;
      if (!rootID || !sessionKey) return;

      const deleted = await sessionService.deleteSession(rootID, sessionKey, String((session as any)?._nodeId || "") || getNodeIdForRoot(rootID));
      if (!deleted) {
        reportError("session.delete_failed", t("session.deleteFailed"));
        return;
      }

      const deletedKeys = new Set<string>();
      const collectDeletedKeys = (key: string) => {
        if (!key || deletedKeys.has(key)) return;
        deletedKeys.add(key);
        for (const item of sessionsRef.current) {
          const itemKey = item.key || item.session_key || "";
          if (String(item.parent_session_key || "").trim() === key) {
            collectDeletedKeys(itemKey);
          }
        }
      };
      collectDeletedKeys(sessionKey);

      setSessions((prev) =>
        prev.filter((item) => !deletedKeys.has(item.key || item.session_key || "")),
      );

      for (const deletedKey of deletedKeys) {
        const cacheKey = rootSessionKey(rootID, deletedKey);
        delete sessionCacheRef.current[cacheKey];
        delete loadedSessionRef.current[cacheKey];
        delete loadingSessionRef.current[cacheKey];
        delete pendingBySessionRef.current[cacheKey];
        delete cancelRequestedBySessionRef.current[cacheKey];
        staleSessionKeysRef.current.delete(cacheKey);
        void deleteCachedSession(rootID, deletedKey, getNodeIdForRoot(rootID));
      }

      if (deletedKeys.has(boundSessionByRootRef.current[scopedRootKey(rootID)] || "")) {
        resetSessionLockForRoot(rootID);
      }
      if (deletedKeys.has(selectedSessionByRootRef.current[scopedRootKey(rootID)] || "")) {
        selectedSessionByRootRef.current[scopedRootKey(rootID)] = null;
      }
      if (deletedKeys.has(drawerSessionByRootRef.current[scopedRootKey(rootID)]?.key || "")) {
        setDrawerSessionForRoot(rootID, null);
        setDrawerOpenForRoot(rootID, false);
      }
      setMultiProjectSessionGroups((prev) =>
        prev.map((group) => {
          if (group.rootId !== rootID || ((session as any)?._nodeId && (group as any)._nodeId !== (session as any)?._nodeId)) {
            return group;
          }
          const sessions = group.sessions.filter(
            (item) => !deletedKeys.has(item.key || item.session_key || ""),
          );
          return {
            ...group,
            sessions,
            totalCount: Math.max(0, group.totalCount - (group.sessions.length - sessions.length)),
          };
        }).filter((group) => group.totalCount > 0 || group.sessions.length > 0),
      );
      for (const deletedKey of deletedKeys) {
        setMultiProjectSessionPending(rootID, deletedKey, false);
      }

      const selectedKey =
        selectedSessionRef.current?.key ||
        selectedSessionRef.current?.session_key;
      const selectedRoot =
        (selectedSessionRef.current?.root_id as string | undefined) ||
        currentRootIdRef.current;
      if (deletedKeys.has(selectedKey || "") && selectedRoot === rootID) {
        setSelectedSession(null);
        setSelectedSessionLoading(false);
        replaceURLState({
          root: rootID,
          file: fileRef.current?.root === rootID ? fileRef.current.path : "",
          session: "",
          cursor: fileCursorRef.current || 0,
          pluginQuery: fileRef.current?.root === rootID ? pluginQuery : {},
        });
      }

      bumpCacheVersion();
    },
    [
      bumpCacheVersion,
      pluginQuery,
      replaceURLState,
      rootSessionKey,
      setBoundSessionForRoot,
      setDrawerOpenForRoot,
      setDrawerSessionForRoot,
      setMultiProjectSessionPending,
      resetSessionLockForRoot,
    ],
  );

  const handleRenameSession = useCallback(
    async (session: SessionItem, nextName: string) => {
      const sessionKey = session?.key || session?.session_key;
      const rootID =
        (session?.root_id as string | undefined) || currentRootIdRef.current;
      if (!rootID || !sessionKey) return false;

      const trimmedName = nextName.trim();
      if (!trimmedName || trimmedName === String(session?.name || "").trim()) {
        return true;
      }

      const renamed = await sessionService.renameSession(
        rootID,
        sessionKey,
        trimmedName,
        String((session as any)?._nodeId || "") || getNodeIdForRoot(rootID),
      );
      if (!renamed) {
        reportError("session.rename_failed", t("session.renameFailed"));
        return false;
      }

      setSessions((prev) =>
        prev.map((item) =>
          (item.key || item.session_key) === sessionKey
            ? ({
                ...item,
                name: renamed.name,
                updated_at: renamed.updated_at,
              } as SessionItem)
            : item,
        ),
      );

      const cacheKey = rootSessionKey(rootID, sessionKey);
      const cached = sessionCacheRef.current[cacheKey];
      if (cached) {
        sessionCacheRef.current[cacheKey] = {
          ...cached,
          name: renamed.name,
          updated_at: renamed.updated_at,
        } as Session;
      }

      if (
        (selectedSessionRef.current?.key ||
          selectedSessionRef.current?.session_key) === sessionKey
      ) {
        setSelectedSession((prev) =>
          prev
            ? ({
                ...prev,
                name: renamed.name,
                updated_at: renamed.updated_at,
              } as SessionItem)
            : prev,
        );
      }

      if (boundSessionByRootRef.current[scopedRootKey(rootID)] === sessionKey) {
        const latest = sessionCacheRef.current[cacheKey];
        if (latest) {
          setDrawerSessionForRoot(rootID, latest);
        }
      }
      setMultiProjectSessionGroups((prev) =>
        prev.map((group) =>
          group.rootId === rootID && (!(session as any)?._nodeId || (group as any)._nodeId === (session as any)?._nodeId)
            ? {
                ...group,
                sessions: group.sessions.map((item) =>
                  (item.key || item.session_key) === sessionKey
                    ? ({
                        ...item,
                        name: renamed.name,
                        updated_at: renamed.updated_at,
                      } as SessionItem)
                    : item,
                ),
              }
            : group,
        ),
      );

      bumpCacheVersion();
      return true;
    },
    [bumpCacheVersion, rootSessionKey, setDrawerSessionForRoot],
  );

  const handlePinSession = useCallback(
    async (session: SessionItem, pinned: boolean) => {
      const sessionKey = session?.key || session?.session_key;
      const rootID =
        (session?.root_id as string | undefined) || currentRootIdRef.current;
      if (!rootID || !sessionKey) return false;

      const updated = await sessionService.setSessionPinned(rootID, sessionKey, pinned, String((session as any)?._nodeId || "") || getNodeIdForRoot(rootID));
      if (!updated) {
        reportError("session.pin_failed", t("session.pinFailed"));
        return false;
      }

      const nextItem = toSessionItem(rootID, updated);
      if (nextItem) {
        setSessions((prev) => mergeSessionItems(prev, [nextItem]));
        setMultiProjectSessionGroups((prev) =>
          prev.map((group) =>
            group.rootId === rootID && (!(session as any)?._nodeId || (group as any)._nodeId === (session as any)?._nodeId)
              ? {
                  ...group,
                  sessions: mergeSessionItems(group.sessions, [nextItem]),
                }
              : group,
          ),
        );
      }

      const cacheKey = rootSessionKey(rootID, sessionKey);
      const cached = sessionCacheRef.current[cacheKey];
      if (cached) {
        sessionCacheRef.current[cacheKey] = {
          ...cached,
          pinned_at: updated.pinned_at || undefined,
        } as Session;
      }

      if (
        (selectedSessionRef.current?.key ||
          selectedSessionRef.current?.session_key) === sessionKey
      ) {
        setSelectedSession((prev) =>
          prev
            ? ({
                ...prev,
                pinned_at: updated.pinned_at || undefined,
              } as SessionItem)
            : prev,
        );
      }

      if (boundSessionByRootRef.current[scopedRootKey(rootID)] === sessionKey) {
        const latest = sessionCacheRef.current[cacheKey];
        if (latest) {
          setDrawerSessionForRoot(rootID, latest);
        }
      }

      bumpCacheVersion();
      return true;
    },
    [bumpCacheVersion, rootSessionKey, setDrawerSessionForRoot, t],
  );

  const handleSyncSession = useCallback(
    async (session: SessionItem) => {
      const sessionKey = session?.key || session?.session_key;
      const rootID =
        (session?.root_id as string | undefined) || currentRootIdRef.current;
      if (!rootID || !sessionKey || sessionKey.startsWith("pending-")) return;

      const cacheKey = rootSessionKey(rootID, sessionKey);
      setSyncingSessionKeys((prev) => {
        const next = new Set(prev);
        next.add(cacheKey);
        next.add(sessionKey);
        return next;
      });
      try {
        const result = await syncSession(rootID, sessionKey, { full: true, nodeId: String((session as any)?._nodeId || "") || getNodeIdForRoot(rootID) });
        const synced = result.session;
        if (!synced) {
          reportError("session.sync_failed", t("session.syncFailed"));
          return;
        }
        // 点「同步」必须保住「正在等待回答的 ask_user 卡」。
        // 该卡是纯瞬时态：只存在于前端内存缓存（WS tool_call 追加的 seq=0 role=tool 条目）
        // 与服务端内存（manager.pendingToolCalls）里，两边都不会经 HTTP 下发
        // （session.Exchange 结构体没有 ToolCall 字段）。syncSession 以 IDB 缓存为 base，
        // 而 IDB 里从来没有这些瞬时条目，于是同步后卡片凭空消失（实测 2026-09-12 症状 2）。
        // 这里从内存缓存按 callId 去重后把它们合并回来；落盘后同一 callId 会出现在窗口 aux
        // 中，SessionViewer 的 overlay 对账（windowToolCallIds）会自然让位，不会重复渲染。
        // 同步会把整个会话的持久化交换灌回来（实测缓存 24 → 92），但**本轮尚未落盘的内容**
        // 只存在于内存缓存的 seq=0 瞬时条目里。原先这里只保留 role=tool 一类，于是
        // 「点同步的瞬间，正在流式输出的正文凭空消失，而 ask 卡还在」——2026-09-13 实测
        // cacheBeforeTransient=4 只保住 1 条（keptToolTransient=1），丢掉的 3 条正是非 tool
        // 的直播正文。
        // 现在两类都保留，各按「同步结果里是否已有」去重：
        //   · tool 条目按 callId（落盘后同一 callId 会出现在窗口 aux，SessionViewer 的
        //     overlay 对账 windowToolCallIds 会自然让位，不会重复渲染）
        //   · 其余条目按 role+内容（已持久化的轮次不会以 seq=0 形式被重复追加，这正是当初
        //     只留 tool 想防的事；用内容比对同样防得住）
        const localTransientTail = (() => {
          const cachedExchanges = Array.isArray(
            (sessionCacheRef.current[cacheKey] as any)?.exchanges,
          )
            ? ((sessionCacheRef.current[cacheKey] as any).exchanges as any[])
            : [];
          const present = new Set<string>();
          const presentText = new Set<string>();
          const syncedExchanges = Array.isArray((synced as any)?.exchanges)
            ? ((synced as any).exchanges as any[])
            : [];
          for (const ex of syncedExchanges) {
            const callId = `${(ex as any)?.toolCall?.callId || ""}`.trim();
            if (callId) present.add(callId);
            presentText.add(
              `${String((ex as any)?.role || "").toLowerCase()}|${String(
                (ex as any)?.content || "",
              )}`,
            );
          }
          const syncedAux = ((synced as any)?.exchange_aux || {}) as Record<
            string,
            any[]
          >;
          for (const items of Object.values(syncedAux)) {
            for (const aux of items || []) {
              const callId = `${(aux as any)?.toolcall?.callId || ""}`.trim();
              if (callId) present.add(callId);
            }
          }
          return cachedExchanges.filter((ex) => {
            if (Number((ex as any)?.seq || 0) !== 0) return false;
            const role = String((ex as any)?.role || "").toLowerCase();
            if (role === "tool") {
              const callId = `${(ex as any)?.toolCall?.callId || ""}`.trim();
              return !callId || !present.has(callId);
            }
            const content = String((ex as any)?.content || "");
            if (!content) return false;
            return !presentText.has(`${role}|${content}`);
          });
        })();
        const syncedExchanges = Array.isArray((synced as any)?.exchanges)
          ? ((synced as any).exchanges as any[])
          : [];
        const normalized = {
          ...(synced as any),
          key: sessionKey,
          exchanges: [...syncedExchanges, ...localTransientTail],
        } as Session;
        sessionCacheRef.current[cacheKey] = normalized;
        loadedSessionRef.current[cacheKey] = true;
        clearSessionStale(rootID, sessionKey);

        const nextItem = toSessionItem(rootID, normalized);
        if (nextItem) {
          setSessions((prev) => mergeSessionItems(prev, [nextItem]));
        }

        setSelectedSession((prev) => {
          const prevKey = prev?.key || prev?.session_key;
          const prevRoot =
            (prev?.root_id as string | undefined) || currentRootIdRef.current;
          if (!prev || prevKey !== sessionKey || prevRoot !== rootID) {
            return prev;
          }
          return toSessionItem(rootID, {
            ...(prev as any),
            ...(normalized as any),
            key: sessionKey,
            session_key: sessionKey,
            root_id: rootID,
          });
        });

        if (drawerSessionByRootRef.current[scopedRootKey(rootID)]?.key === sessionKey) {
          setDrawerSessionForRoot(rootID, normalized);
        }

        bumpCacheVersion();
      } catch {
        reportError("session.sync_failed", t("session.syncFailed"));
      } finally {
        setSyncingSessionKeys((prev) => {
          const next = new Set(prev);
          next.delete(cacheKey);
          next.delete(sessionKey);
          return next;
        });
      }
    },
    [
      bumpCacheVersion,
      clearSessionStale,
      mergeSessionItems,
      rootSessionKey,
      setDrawerSessionForRoot,
    ],
  );

  const handleForkAgentMessage = useCallback(
    async (
      rootID: string | null | undefined,
      sessionKey: string | null | undefined,
      seq: number,
    ) => {
      const resolvedRoot = String(rootID || currentRootIdRef.current || "").trim();
      const resolvedKey = String(sessionKey || "").trim();
      if (!resolvedRoot || !resolvedKey || seq <= 0) {
        reportError("session.sync_failed", t("session.forkMissing"));
        return;
      }
      const result = await sessionService.forkSession(resolvedRoot, resolvedKey, seq, getNodeIdForRoot(resolvedRoot));
      const forked = result?.session;
      const forkedKey = String(result?.session_key || forked?.key || "").trim();
      if (!forkedKey) {
        reportError("session.sync_failed", t("session.forkFailed"));
        return;
      }
      if (forked) {
        const normalized = {
          ...(forked as any),
          key: forkedKey,
          session_key: forkedKey,
          root_id: resolvedRoot,
        } as Session;
        const cacheKey = rootSessionKey(resolvedRoot, forkedKey);
        sessionCacheRef.current[cacheKey] = normalized;
        loadedSessionRef.current[cacheKey] = true;
        const item = toSessionItem(resolvedRoot, normalized);
        if (item) {
          setSessions((prev) => mergeSessionItems(prev, [item]));
          await handleSelectSession(item);
        } else {
          await handleSelectSession({ key: forkedKey, session_key: forkedKey, root_id: resolvedRoot });
        }
      } else {
        await handleSelectSession({ key: forkedKey, session_key: forkedKey, root_id: resolvedRoot });
      }
      void loadSessionsForRoot(resolvedRoot, { replace: true, force: true });
      bumpCacheVersion();
    },
    [
      bumpCacheVersion,
      handleSelectSession,
      loadSessionsForRoot,
      mergeSessionItems,
      rootSessionKey,
    ],
  );

  useEffect(() => {
    handleSelectSessionRef.current = handleSelectSession;
  }, [handleSelectSession]);

  const refreshSessionsAfterExternalImport = useCallback(async (rootID: string) => {
    const payload = await sessionService.fetchSessions(rootID, { nodeId: getNodeIdForRoot(rootID) });
    const next = [...payload.items, ...payload.pinnedItems]
      .map((item) => toSessionItem(rootID, item))
      .filter((item): item is SessionItem => !!item);
    setHasMoreSessions(payload.totalCount > payload.items.length);
    setSessions(applyPinnedSnapshotToSessions(mergeSessionItems([], next), rootID, payload.pinnedKeys));
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

  const handleSendMessage = useCallback(
    async (
      message: string,
      mode: SessionMode,
      agent: string,
      model?: string,
      agentMode?: string,
      effort?: string,
      fastService?: "" | "on" | "off",
      shell?: string,
      newSessionWorktree?: {
        create: boolean;
        branchMode: "new" | "existing";
        branch: string;
      },
    ) => {
      const activeRoot = currentRootIdRef.current;
      if (!activeRoot) return;
      const selected = selectedSessionRef.current;
      const selectedKey = selected?.key || selected?.session_key;
      const selectedRoot =
        (selected?.root_id as string | undefined) || activeRoot;
      const currentBoundSessionKey =
        boundSessionByRootRef.current[scopedRootKey(activeRoot)] || null;
      const resolvedBoundKey = resolveLockedSessionKey(currentBoundSessionKey);
      let sendSessionKey: string | null | undefined = resolvedBoundKey;
      let session: Session | null = null;
      if (sendSessionKey) {
        session =
          sessionCacheRef.current[rootSessionKey(activeRoot, sendSessionKey)];
        if (!session) {
          const current = currentSessionRef.current;
          if (current?.key === sendSessionKey) {
            session = current as Session;
          }
        }
        if (!session && selectedKey === sendSessionKey) {
          session = { ...(selected as any), key: sendSessionKey } as Session;
        }
      }
      let effectiveMode = mode,
        effectiveAgent = agent,
        effectiveModel = model || "",
        effectiveAgentMode = agentMode || "",
        effectiveEffort = effort || "",
        effectiveFastService = (fastService || "") as "" | "on" | "off",
        effectiveShell = shell || "";
      const messageRequestsPlanMode =
        message.trim().toLowerCase() === "/plan" ||
        message.trim().toLowerCase().startsWith("/plan ");
      const normalizedMessage = message.trim().toLowerCase();
      const messageRequestsStatus = normalizedMessage === "/status";
      const messageRequestsLogin = normalizedMessage === "/login";
      const transientSlashCommand = messageRequestsStatus
        ? "status"
        : messageRequestsLogin
          ? "login"
          : "";
      const isQueueSend =
        !!sendSessionKey &&
        !!session &&
        (((session as any).pending === true) ||
          (currentSessionRef.current?.key === sendSessionKey &&
            currentSessionRef.current?.pending === true));
      if (sendSessionKey && session) {
        const targetSessionKey = sendSessionKey;
        const previousAgent = session.agent || "";
        const useTargetSessionDefaults =
          !!currentBoundSessionKey && currentBoundSessionKey !== targetSessionKey;
        effectiveMode = normalizeMode(session.type as any);
        effectiveAgent =
          (useTargetSessionDefaults ? previousAgent : agent) ||
          previousAgent ||
          "";
        effectiveModel =
          (useTargetSessionDefaults ? session.model || "" : model) ||
          (effectiveAgent === previousAgent ? session.model || "" : "");
        effectiveAgentMode =
          (useTargetSessionDefaults ? (session as any).mode || "" : agentMode) ||
          (effectiveAgent === previousAgent ? (session as any).mode || "" : "");
        effectiveEffort =
          (useTargetSessionDefaults ? (session as any).effort || "" : effort) ||
          (effectiveAgent === previousAgent ? (session as any).effort || "" : "");
        effectiveFastService =
          (useTargetSessionDefaults
            ? (((session as any).fast_service || "") as "" | "on" | "off")
            : ((fastService || "") as "" | "on" | "off")) ||
          (effectiveAgent === previousAgent
            ? (((session as any).fast_service || "") as "" | "on" | "off")
            : "");
        effectiveShell =
          effectiveMode === "command"
            ? ((useTargetSessionDefaults ? (session as any).shell || "" : shell) ||
                (session as any).shell ||
                "")
            : "";
        updateSessionAgentForKey(
          activeRoot,
          targetSessionKey,
          effectiveAgent,
          effectiveModel,
          effectiveAgentMode,
          effectiveEffort,
          effectiveFastService,
        );
        session = {
          ...(session as any),
          agent: effectiveAgent,
          model: effectiveModel,
          mode: effectiveAgentMode,
          effort: effectiveEffort,
          fast_service: effectiveFastService,
          shell: effectiveShell,
        } as Session;
        setBoundSessionForRoot(activeRoot, targetSessionKey);
        if (!isQueueSend) {
          setSelectedPendingByKey(targetSessionKey, true);
          setDrawerSessionForRoot(activeRoot, {
            ...(session as any),
            pending: true,
          } as Session);
        }
      } else {
        if (transientSlashCommand) {
          sendSessionKey = `transient-${Date.now()}`;
          session = null;
        } else {
          sendSessionKey = undefined;
          const tempKey = `pending-${Date.now()}`;
          session = {
            key: tempKey,
            type: mode,
            agent,
            model: effectiveModel,
            mode: effectiveAgentMode,
            effort: effectiveEffort,
            fast_service: effectiveFastService,
            shell: effectiveShell,
            plan_mode: pendingPlanMode || messageRequestsPlanMode,
            name: t("session.new"),
            pending: true,
          } as any;
          setBoundSessionForRoot(activeRoot, tempKey);
        }
      }
      if (transientSlashCommand) {
        if (!sendSessionKey) return;
        if (effectiveAgent !== "codex") {
          reportError(
            "session.slash_command_failed",
            t("session.slashCommandCodexOnly", { command: transientSlashCommand }),
          );
          return;
        }
	        const requestId = sessionService.createRequestId("slash");
	        const targetSessionKey = sendSessionKey;
	        const resultKey = rootSessionKey(activeRoot, targetSessionKey);
        const runningTransientLoginKeys = Array.from(
          new Set(
            Object.values(slashCommandResults)
              .filter(
                (value) =>
                  value.rootId === activeRoot &&
                  value.command === "login" &&
                  value.status === "running",
              )
              .map((value) => value.sessionKey),
          ),
        );
        if (runningTransientLoginKeys.length > 0) {
          await Promise.all(
            runningTransientLoginKeys.map((sessionKey) =>
              sessionService.cancelMessage(activeRoot, sessionKey),
            ),
          );
        }
	        if (session) {
	          setSelectedPendingByKey(targetSessionKey, false);
	          setMultiProjectSessionPending(activeRoot, targetSessionKey, false);
	        }
	        setSessions((prev) =>
	          prev.map((item) => {
	            const itemKey = item.key || item.session_key;
	            if (itemKey !== targetSessionKey) {
	              return item;
	            }
	            return { ...(item as any), pending: false } as SessionItem;
	          }),
	        );
	        const drawerSession = drawerSessionByRootRef.current[scopedRootKey(activeRoot)];
	        if (drawerSession && drawerSession.key === targetSessionKey) {
	          setDrawerSessionForRoot(activeRoot, {
	            ...(drawerSession as any),
	            pending: false,
	          } as Session);
	        }
	        setSlashCommandResults((prev) => {
          const next = { ...prev };
          for (const [key, value] of Object.entries(prev)) {
            if (
              value.rootId === activeRoot &&
              value.sessionKey.startsWith("transient-")
            ) {
              delete next[key];
            }
          }
          next[resultKey] = {
            rootId: activeRoot,
            sessionKey: targetSessionKey,
            requestId,
            command: transientSlashCommand,
            content: "",
            status: "running",
            createdAt: Date.now(),
          };
          return next;
        });
        const sent = await sessionService.runSlashCommand(
          activeRoot,
          targetSessionKey,
          transientSlashCommand,
          effectiveAgent,
          effectiveModel || undefined,
          effectiveAgentMode || undefined,
          effectiveEffort || undefined,
          effectiveFastService,
          requestId,
        );
        if (!sent) {
          setSlashCommandResults((prev) => ({
            ...prev,
            [resultKey]: {
              rootId: activeRoot,
              sessionKey: targetSessionKey,
              requestId,
              command: transientSlashCommand,
              content: "",
              status: "failed",
              error: t("session.connectionNotReady"),
              createdAt: Date.now(),
            },
          }));
          reportError(
            "network.disconnected",
            t("session.commandSendFailedNotReady"),
          );
        }
        return;
      }
      if (sendSessionKey) {
        clearSlashCommandResultForSession(activeRoot, sendSessionKey);
      }
      const runningTransientLoginKeys = Array.from(
        new Set(
          Object.values(slashCommandResults)
            .filter(
              (value) =>
                value.rootId === activeRoot &&
                value.command === "login" &&
                value.status === "running",
            )
            .map((value) => value.sessionKey),
        ),
      );
      if (runningTransientLoginKeys.length > 0) {
        await Promise.all(
          runningTransientLoginKeys.map((sessionKey) =>
            sessionService.cancelMessage(activeRoot, sessionKey),
          ),
        );
      }
      setSlashCommandResults((prev) => {
        let changed = false;
        const next = { ...prev };
        for (const [key, value] of Object.entries(prev)) {
          if (value.rootId === activeRoot && value.sessionKey.startsWith("transient-")) {
            delete next[key];
            changed = true;
          }
        }
        return changed ? next : prev;
      });
      const now = new Date().toISOString();
      const requestId = sessionService.createRequestId("msg");
      const tempKey = sendSessionKey ? "" : session?.key || "";
      const userEx: Exchange = {
        role: "user",
        agent: effectiveAgent,
        model: effectiveModel,
        mode: effectiveAgentMode,
        effort: effectiveEffort,
        fast_service: effectiveFastService,
        content: message,
        timestamp: now,
        pending_ack: true,
      };
      if (sendSessionKey) {
        const targetSessionKey = sendSessionKey;
        setMultiProjectSessionPending(activeRoot, targetSessionKey, true);
        setSessions((prev) =>
          prev.map((item) => {
            const itemKey = item.key || item.session_key;
            if (itemKey !== targetSessionKey) {
              return item;
            }
            return {
              ...(item as any),
              pending: true,
              updated_at: now,
              agent: effectiveAgent,
              model: effectiveModel,
              mode: effectiveAgentMode,
              effort: effectiveEffort,
              fast_service: effectiveFastService,
              shell: effectiveShell,
            } as SessionItem;
          }),
        );
      }
      pendingRequestRef.current[requestId] = {
        rootId: activeRoot,
        mode: effectiveMode,
        agent: effectiveAgent,
        model: effectiveModel,
        agentMode: effectiveAgentMode,
        effort: effectiveEffort,
        fastService: effectiveFastService,
        shell: effectiveShell,
        message,
        timestamp: now,
        requestId,
        sessionKey: sendSessionKey || undefined,
        tempKey,
      };
      if (sendSessionKey && !isQueueSend) {
        const ck = rootSessionKey(activeRoot, sendSessionKey);
        const cached =
          sessionCacheRef.current[ck] || ({ ...(session as any) } as Session);
        const prevExchanges = Array.isArray((cached as any).exchanges)
          ? ((cached as any).exchanges as Exchange[])
          : [];
        sessionCacheRef.current[ck] = {
          ...(cached as any),
          exchanges: [...prevExchanges, userEx],
          mode: effectiveAgentMode,
          effort: effectiveEffort,
          fast_service: effectiveFastService,
          shell: effectiveShell,
          updated_at: now,
        } as Session;
        session = sessionCacheRef.current[ck];
        bumpCacheVersion();
      } else if (!sendSessionKey) {
        const tempSessionKey = session?.key || "";
        pendingDraftRef.current = {
          rootId: activeRoot,
          mode: effectiveMode,
          agent: effectiveAgent,
          model: effectiveModel,
          agentMode: effectiveAgentMode,
          effort: effectiveEffort,
          fastService: effectiveFastService,
          shell: effectiveShell,
          message,
          timestamp: now,
          requestId,
          tempKey,
        };
        const draftSession = {
          ...(session as any),
          exchanges: [userEx],
          updated_at: now,
        } as Session;
        if (tempSessionKey) {
          setMultiProjectSessionPending(activeRoot, tempSessionKey, true);
          sessionCacheRef.current[rootSessionKey(activeRoot, tempSessionKey)] =
            draftSession;
          const draftItem = toSessionItem(activeRoot, {
            ...(draftSession as any),
            key: tempSessionKey,
            session_key: tempSessionKey,
            root_id: activeRoot,
            created_at: now,
            updated_at: now,
            pending: true,
          });
          if (draftItem) {
            setSessions((prev) => mergeSessionItems(prev, [draftItem]));
            // 新建会话立即同步进右侧多项目列表，避免依赖 WS 重拉（断连/竞态时永不出现）
            setMultiProjectSessionGroups((prev) => {
              const existing = prev.find((g) => g.rootId === activeRoot);
              if (existing) {
                return prev.map((g) =>
                  g.rootId === activeRoot
                    ? { ...g, sessions: mergeSessionItems(g.sessions, [draftItem]) }
                    : g,
                );
              }
              const rootName =
                (managedRootByIdRef.current as Record<string, any>)[activeRoot]?.display_name ||
                activeRoot;
              return [
                {
                  rootId: activeRoot,
                  rootName,
                  latestSessionTime: draftItem.updated_at || now,
                  sessions: [draftItem],
                  totalCount: 1,
                },
                ...prev,
              ];
            });
          }
          bumpCacheVersion();
        }
        session = draftSession;
      }
      const isBoundInMain =
        !!selectedSessionRef.current &&
        selectedSessionRef.current.key === sendSessionKey &&
        interactionModeRef.current !== "drawer";
      if (!isBoundInMain) {
        setInteractionMode("drawer");
        setDrawerOpenForRoot(activeRoot, true);
      }
      if (!isQueueSend) {
        setDrawerSessionForRoot(activeRoot, {
          ...(session as any),
          pending: true,
        } as Session);
      }
      const explicitFileContext = hasExplicitFileContext(message);
      const selection =
        explicitFileContext || !attachedFileContext?.filePath
          ? undefined
          : {
              filePath: attachedFileContext.filePath,
              startLine: attachedFileContext.startLine,
              endLine: attachedFileContext.endLine,
              text: attachedFileContext.text,
            };
      const context = buildClientContext({
        currentRoot: activeRoot,
        selection,
        pluginCatalog:
          effectiveMode === "plugin" ? await getViewModeSystemPrompt() : undefined,
      });
      let outgoingMessage = message;
      const applyPendingPlanPrefix = pendingPlanMode && !sendSessionKey;
      const currentFile = fileRef.current;
      if (currentFile && !pluginBypassRef.current) {
        try {
          const pluginInput = toPluginInput(currentFile, pluginQueryRef.current);
          const plugin = pluginManagerRef.current.match(activeRoot, pluginInput);
          if (plugin?.viewContext) {
            outgoingMessage = buildMessageWithViewContext(
              message,
              pluginManagerRef.current.viewContext(plugin, pluginInput),
            );
          }
        } catch (err) {
          console.warn("[plugin/view-context] failed", err);
        }
      }
      if (applyPendingPlanPrefix) {
        outgoingMessage = `/plan ${outgoingMessage}`;
      }
      const sent = await sessionService.sendMessage(
        activeRoot,
        sendSessionKey || undefined,
        outgoingMessage,
        effectiveMode,
        effectiveAgent,
        effectiveModel || undefined,
        effectiveAgentMode || undefined,
        effectiveEffort || undefined,
        effectiveFastService,
        context,
        effectiveShell || undefined,
        requestId,
        newSessionWorktree,
      );
      if (sent && applyPendingPlanPrefix) {
        setPendingPlanMode(false);
      }
      console.info("[session/send] dispatched", { requestId, rootId: activeRoot, sessionKey: sendSessionKey || null, tempKey: tempKey || null, sent });
      if (!sent) {
        console.warn("[session/send] dispatch_failed", { requestId, rootId: activeRoot, sessionKey: sendSessionKey || null });
        reportError("network.disconnected", t("session.messageSendFailedNotReady"), {
          details: {
            requestId,
            rootId: activeRoot,
            sessionKey: sendSessionKey || null,
          },
        });
        delete pendingRequestRef.current[requestId];
      }
      if (!sent && sendSessionKey) {
        const failedSessionKey = sendSessionKey;
        setSelectedPendingByKey(failedSessionKey, false);
        setSessions((prev) =>
          prev.map((item) => {
            const itemKey = item.key || item.session_key;
            if (itemKey !== failedSessionKey) {
              return item;
            }
            return { ...(item as any), pending: false } as SessionItem;
          }),
        );
        const latest = drawerSessionByRootRef.current[scopedRootKey(activeRoot)];
        if (latest && latest.key === failedSessionKey) {
          setDrawerSessionForRoot(activeRoot, {
            ...(latest as any),
            pending: false,
          } as Session);
        }
      }
      if (!sent && !sendSessionKey && tempKey) {
        setMultiProjectSessionPending(activeRoot, tempKey, false);
        setSessions((prev) =>
          prev.filter((item) => (item.key || item.session_key) !== tempKey),
        );
        delete sessionCacheRef.current[rootSessionKey(activeRoot, tempKey)];
        if (boundSessionByRootRef.current[scopedRootKey(activeRoot)] === tempKey) {
          setBoundSessionForRoot(activeRoot, null);
        }
        const latest = drawerSessionByRootRef.current[scopedRootKey(activeRoot)];
        if (latest && latest.key === tempKey) {
          setDrawerSessionForRoot(activeRoot, {
            ...(latest as any),
            pending: false,
          } as Session);
        }
      }
    },
    [
      attachedFileContext,
      rootSessionKey,
      mergeSessionItems,
      setSelectedPendingByKey,
      bumpCacheVersion,
      clearSlashCommandResultForSession,
      slashCommandResults,
      setBoundSessionForRoot,
      setDrawerOpenForRoot,
      setDrawerSessionForRoot,
      setMultiProjectSessionPending,
      updateSessionAgentForKey,
      pendingPlanMode,
    ],
  );

  const handleRunAgentLifecycleCommand = useCallback(
    async (agentName: string, action: "install" | "update", commands: string[]) => {
      const activeRoot = currentRootIdRef.current;
      if (!activeRoot) {
        throw new Error(t("root.noProjectForCommand"));
      }
      const script = commands.map((command) => command.trim()).filter(Boolean).join("\n");
      if (!script) {
        throw new Error(t("agentConfig.noCommand"));
      }
      const previousBoundKey = boundSessionByRootRef.current[scopedRootKey(activeRoot)];
      if (previousBoundKey && !previousBoundKey.startsWith("pending-")) {
        suppressedAutoBindSessionByRootRef.current[scopedRootKey(activeRoot)] = previousBoundKey;
      }
      selectedSessionRef.current = null;
      currentSessionRef.current = null;
      setSelectedSession(null);
      selectedSessionByRootRef.current[scopedRootKey(activeRoot)] = null;
      setBoundSessionForRoot(activeRoot, null);
      setDrawerSessionForRoot(activeRoot, null);
      setInteractionMode("drawer");
      setDrawerOpenForRoot(activeRoot, true);
      await handleSendMessage(script, "command", "");
      console.info("[agent/lifecycle] command dispatched", {
        agent: agentName,
        action,
        commandCount: commands.length,
      });
    },
    [
      handleSendMessage,
      setBoundSessionForRoot,
      setDrawerOpenForRoot,
      setDrawerSessionForRoot,
      t,
    ],
  );

  const handleRestartAgent = useCallback(async (agentName: string) => {
    const nid = getNodeIdForRoot(currentRootIdRef.current || "") as any;
    await restartAgent(agentName, nid);
    const items = await fetchAgents(true, nid);
    setAvailableAgents(items);
    setAgentsVersion((v) => v + 1);
  }, []);

  const handleCancelCurrentTurn = useCallback(
    async (sessionKey: string) => {
      const activeRoot = currentRootIdRef.current;
      if (!activeRoot || !sessionKey) return;
      const cacheKey = rootSessionKey(activeRoot, sessionKey);
      cancelRequestedBySessionRef.current[cacheKey] = true;
      const sent = await sessionService.cancelMessage(activeRoot, sessionKey);
      if (!sent) {
        delete cancelRequestedBySessionRef.current[cacheKey];
        return;
      }
      clearLocalPendingForSession(activeRoot, sessionKey);
    },
    [clearLocalPendingForSession, rootSessionKey],
  );

  const markSessionPending = useCallback(
    (rootID: string, sessionKey: string) => {
      if (!rootID || !sessionKey) return;
      // 幂等：流式期间每 chunk 调用一次，已在 pending 则只更新缓存 ref，不再重复 setState。
      const cacheKey = rootSessionKey(rootID, sessionKey);
      const cached = sessionCacheRef.current[cacheKey];
      const drawer = drawerSessionByRootRef.current[scopedRootKey(rootID)];
      const alreadyPending =
        !!(cached as any)?.pending &&
        !!(drawer as any)?.pending &&
        (selectedSessionRef.current?.key || selectedSessionRef.current?.session_key) ===
          sessionKey &&
        !!(selectedSessionRef.current as any)?.pending;
      const now = new Date().toISOString();
      if (cached) {
        sessionCacheRef.current[cacheKey] = {
          ...(cached as any),
          pending: true,
          updated_at: now,
        } as Session;
      }
      if (alreadyPending) {
        return;
      }
      if (cached) {
        bumpCacheVersion();
      }
      setSelectedPendingByKey(sessionKey, true);
      if (drawer && (drawer.key || (drawer as any).session_key) === sessionKey) {
        setDrawerSessionForRoot(rootID, {
          ...(drawer as any),
          pending: true,
          updated_at: now,
        } as Session);
      }
      if (currentRootIdRef.current === rootID) {
        setSessions((prev) =>
          prev.map((item) => {
            const itemKey = item.key || item.session_key;
            if (itemKey !== sessionKey) {
              return item;
            }
            return {
              ...(item as any),
              pending: true,
              updated_at: now,
            } as SessionItem;
          }),
        );
      }
      setMultiProjectSessionPending(rootID, sessionKey, true);
    },
    [bumpCacheVersion, rootSessionKey, setDrawerSessionForRoot, setMultiProjectSessionPending, setSelectedPendingByKey],
  );

  const handleRemoveQueuedMessage = useCallback(
    async (queueId: string) => {
      const activeRoot = currentRootIdRef.current;
      const selected = selectedSessionRef.current;
      const selectedRoot =
        (selected?.root_id as string | undefined) || activeRoot || "";
      const selectedKey = selected?.key || selected?.session_key || "";
      const resolvedSelected = resolveLockedSessionKey(selectedKey);
      const boundKey = boundSessionByRootRef.current[scopedRootKey(activeRoot || "")] || null;
      const sessionKey =
        interactionModeRef.current !== "drawer" &&
        selectedRoot === activeRoot &&
        resolvedSelected
          ? resolvedSelected
          : (resolveLockedSessionKey(boundKey) ?? "");
      if (!activeRoot || !sessionKey || !queueId) return;
      await sessionService.removeQueuedMessage(activeRoot, sessionKey, queueId);
    },
    [],
  );

  const handleUpdateQueuedMessage = useCallback(
    async (queueId: string, content: string) => {
      const activeRoot = currentRootIdRef.current;
      const selected = selectedSessionRef.current;
      const selectedRoot =
        (selected?.root_id as string | undefined) || activeRoot || "";
      const selectedKey = selected?.key || selected?.session_key || "";
      const resolvedSelected = resolveLockedSessionKey(selectedKey);
      const boundKey = boundSessionByRootRef.current[scopedRootKey(activeRoot || "")] || null;
      const sessionKey =
        interactionModeRef.current !== "drawer" &&
        selectedRoot === activeRoot &&
        resolvedSelected
          ? resolvedSelected
          : (resolveLockedSessionKey(boundKey) ?? "");
      if (!activeRoot || !sessionKey || !queueId || !content.trim()) return;
      await sessionService.updateQueuedMessage(activeRoot, sessionKey, queueId, content);
    },
    [],
  );

  const handleSendQueuedMessageNow = useCallback(
    async (queueId: string) => {
      const activeRoot = currentRootIdRef.current;
      const selected = selectedSessionRef.current;
      const selectedRoot =
        (selected?.root_id as string | undefined) || activeRoot || "";
      const selectedKey = selected?.key || selected?.session_key || "";
      const resolvedSelected = resolveLockedSessionKey(selectedKey);
      const boundKey = boundSessionByRootRef.current[scopedRootKey(activeRoot || "")] || null;
      const sessionKey =
        interactionModeRef.current !== "drawer" &&
        selectedRoot === activeRoot &&
        resolvedSelected
          ? resolvedSelected
          : (resolveLockedSessionKey(boundKey) ?? "");
      if (!activeRoot || !sessionKey || !queueId) return;
      const cacheKey = rootSessionKey(activeRoot, sessionKey);
      const previousQueue = queuedMessagesBySessionRef.current[cacheKey] || [];
      const nextQueue = previousQueue.filter((item) => item.id !== queueId);
      queuedMessagesBySessionRef.current[cacheKey] = nextQueue;
      if (!optimisticDequeuedIdsRef.current[cacheKey]) {
        optimisticDequeuedIdsRef.current[cacheKey] = new Set();
      }
      optimisticDequeuedIdsRef.current[cacheKey].add(queueId);
      setQueueVersion((v) => v + 1);
      markSessionPending(activeRoot, sessionKey);
      const sent = await sessionService.sendQueuedMessageNow(activeRoot, sessionKey, queueId);
      if (!sent) {
        optimisticDequeuedIdsRef.current[cacheKey]?.delete(queueId);
        queuedMessagesBySessionRef.current[cacheKey] = previousQueue;
        setQueueVersion((v) => v + 1);
      }
    },
    [markSessionPending, rootSessionKey],
  );

  const handleNewSession = useCallback(() => {
    const rootID = currentRootIdRef.current;
    const previousBoundKey = rootID ? boundSessionByRootRef.current[scopedRootKey(rootID)] : "";
    if (rootID && previousBoundKey && !previousBoundKey.startsWith("pending-")) {
      suppressedAutoBindSessionByRootRef.current[scopedRootKey(rootID)] = previousBoundKey;
    }
    switchMainView("chat");
    selectedSessionRef.current = null;
    currentSessionRef.current = null;
    interactionModeRef.current = "main";
    setSelectedSession(null);
    if (rootID) {
      selectedSessionByRootRef.current[scopedRootKey(rootID)] = null;
    }
    resetSessionLockForRoot(rootID);
  }, [
    resetSessionLockForRoot,
    switchMainView,
  ]);

  const currentSelectionSource = useMemo(() => {
    if (file?.path) {
      return {
        path: file.path,
        name: file.name || basenameOfPath(file.path),
      };
    }
    if (gitDiff?.path) {
      return {
        path: gitDiff.path,
        name: basenameOfPath(gitDiff.path),
      };
    }
    return null;
  }, [file, gitDiff]);

  const buildAttachedFileContext = useCallback(
    (
      currentSource: { path: string; name: string } | null,
      selection: ViewerSelection | null,
    ): AttachedFileContext | null => {
      if (!currentSource?.path) {
        return null;
      }
      const matchesCurrentFile = selection?.filePath === currentSource.path;
      return {
        filePath: currentSource.path,
        fileName: currentSource.name,
        startLine: matchesCurrentFile ? selection?.startLine : undefined,
        endLine: matchesCurrentFile ? selection?.endLine : undefined,
        text: matchesCurrentFile ? selection?.text : undefined,
      };
    },
    [],
  );

  const handleRequestFileContext = useCallback(() => {
    const currentSource = currentSelectionSource;
    if (
      dismissedSelectionFileRef.current &&
      dismissedSelectionFileRef.current === currentSource?.path
    ) {
      return;
    }
    const liveSelection = viewerSelectionRef.current;
    const fallbackSelection = lastViewerSelectionRef.current;
    const nextSelection =
      liveSelection?.filePath === currentSource?.path
        ? liveSelection
        : fallbackSelection?.filePath === currentSource?.path
          ? fallbackSelection
          : null;
    const next = buildAttachedFileContext(currentSource, nextSelection);
    setAttachedFileContext(next);
  }, [buildAttachedFileContext, currentSelectionSource]);

  const handleClearFileContext = useCallback(() => {
    dismissedSelectionFileRef.current = currentSelectionSource?.path || null;
    lastViewerSelectionRef.current = null;
    setAttachedFileContext(null);
  }, [currentSelectionSource]);

  const handleViewerSelectionChange = useCallback(
    (next: ViewerSelection | null) => {
      setViewerSelection(next);
      if (next?.filePath) {
        dismissedSelectionFileRef.current = null;
        lastViewerSelectionRef.current = next;
      }
    },
    [],
  );

  useEffect(() => {
    const currentPath = currentSelectionSource?.path;
    if (!currentPath) {
      dismissedSelectionFileRef.current = null;
      lastViewerSelectionRef.current = null;
      setViewerSelection(null);
      setAttachedFileContext(null);
      return;
    }
    if (
      dismissedSelectionFileRef.current &&
      dismissedSelectionFileRef.current !== currentPath
    ) {
      dismissedSelectionFileRef.current = null;
    }
    if (lastViewerSelectionRef.current?.filePath !== currentPath) {
      lastViewerSelectionRef.current = null;
    }
    setViewerSelection((prev) =>
      prev?.filePath === currentPath ? prev : null,
    );
    setAttachedFileContext((prev) => {
      if (!prev) {
        return prev;
      }
      if (prev.filePath !== currentPath) {
        return null;
      }
      return prev;
    });
  }, [currentSelectionSource?.path]);

  useEffect(() => {
    const currentPath = currentSelectionSource?.path;
    setAttachedFileContext((prev) => {
      if (!prev || prev.filePath !== currentPath) {
        return prev;
      }
      if (!viewerSelection || viewerSelection.filePath !== currentPath) {
        return prev;
      }
      return buildAttachedFileContext(currentSelectionSource, viewerSelection);
    });
  }, [buildAttachedFileContext, currentSelectionSource, viewerSelection]);

  const rememberCurrentFileScroll = useCallback(() => {
    const currentFile = fileRef.current;
    const key = buildFileScrollKey(
      currentFile?.root || currentRootIdRef.current,
      currentFile?.path,
    );
    if (!key) return;
    if (!(key in fileScrollPositionsRef.current)) {
      fileScrollPositionsRef.current[key] = 0;
      persistFileScrollPositions(fileScrollPositionsRef.current);
    }
  }, []);

  const actionHandlers = useMemo(
    () => ({
      open: async (params: any) => {
        const requestId = ++fileOpenRequestRef.current;
        if (!params?.preserveRelatedSelection) {
          setRelatedSelectedFileKey("");
        }
        const isStale = () => fileOpenRequestRef.current !== requestId;
        const parsedLocation = parseFileLocation(String(params.path || ""));
        const root = params.root || currentRootIdRef.current;
        const rootInfo = root
          ? managedRootByIdRef.current[String(root)]
          : undefined;
        const path = normalizePathForRoot(
          parsedLocation.path,
          rootInfo?.root_path,
        );
        if (!path || !root) return;
        switchMainView("files");
        rememberCurrentFileScroll();
        setGitDiff(null);
        const currentFilePath = fileRef.current?.path || "";
        const currentFileRoot =
          fileRef.current?.root || currentRootIdRef.current || "";
        const isFileSwitch =
          currentFilePath !== String(path) || currentFileRoot !== String(root);
        if (isFileSwitch) {
          pluginBypassRef.current = false;
          setPluginBypass(false);
          // Only tear down the current file view when switching to a different file/root.
          // Reopening the same file (for example from session view back to file view) should
          // preserve the existing scroll position and DOM state until fresh content arrives.
          setFile(null);
        }
        resetLocksForRootTransition(root);
        if (currentRootIdRef.current !== root) {
          setCurrentRootId(root);
          selectRootNode(
            String(root),
            String(params.nodeId || "") ||
              getNodeIdForRoot(String(root)) ||
              undefined,
          );
        }
        setSelectedDir(String(root));
        setSelectedDirKey(null);
        const requestedCursor = normalizeCursor(params.cursor);
        const cursor = requestedCursor === null ? 0 : requestedCursor;
        const preserveQuery = !!params.preservePluginQuery;
        const pqNodeId = getNodeIdForRoot(String(root));
        const persistedQuery = loadPersistedPluginQuery(
          String(root),
          String(path),
          pqNodeId,
        );
        const urlQuery = preserveQuery
          ? parsePluginQuery(window.location.search)
          : {};
        // Priority: URL query > localStorage persisted query.
        const nextPluginQuery = preserveQuery
          ? { ...persistedQuery, ...urlQuery }
          : persistedQuery;
        setPluginQuery(nextPluginQuery);
        replaceURLState({
          root,
          file: path,
          session: "",
          cursor,
          pluginQuery: nextPluginQuery,
        });
        persistPluginQuery(String(root), String(path), nextPluginQuery, pqNodeId);

        const expandAndLoadTreeForFile = async () => {
          const dirs = parentDirsOfFile(String(path));
          const fileNodeId = getNodeIdForRoot(String(root));
          setExpanded((prev) => {
            const next = new Set(prev);
            next.add(scopeKey(fileNodeId ?? "", String(root)));
            dirs.forEach((dir) =>
              next.add(dirSelKey(fileNodeId, String(root), dir, false)),
            );
            return Array.from(next);
          });
          const toLoad = [".", ...dirs];
          for (const dir of toLoad) {
            const cacheKey = treeCacheKey(String(root), dir);
            if (
              Object.prototype.hasOwnProperty.call(
                entriesByPathRef.current,
                cacheKey,
              ) &&
              !invalidTreeCacheKeysRef.current.has(cacheKey)
            ) {
              continue;
            }
            try {
              const payload = await apiProtectedJSON<any>(
                appURL(
                  "/api/tree",
                  new URLSearchParams({ root: String(root), dir }),
                  getNodeIdForRoot(String(root)),
                ),
              );
              const parsed = normalizeTreeResponse(payload);
              invalidTreeCacheKeysRef.current.delete(cacheKey);
              setEntriesByPath((prev) => ({
                ...prev,
                [cacheKey]: parsed.entries,
              }));
            } catch {}
          }
        };

        void expandAndLoadTreeForFile();
        try {
          const fetchFileWithMode = async (
            mode: "full" | "incremental",
            timeoutMs?: number,
          ) =>
            fetchFile({
              rootId: String(root),
              path: String(path),
              nodeId: getNodeIdForRoot(String(root)),
              readMode: mode,
              cursor,
              timeoutMs,
            });

          let readMode: "incremental" | "full" =
            params.readMode === "full" ? "full" : "incremental";
          let requiresFull = params.readMode === "full";
          if (params.readMode !== "full" && params.readMode !== "incremental") {
            const currentFilePath = fileRef.current?.path || "";
            const currentFileRoot =
              fileRef.current?.root || currentRootIdRef.current || "";
            const targetPath = String(path);
            const targetRoot = String(root);
            const sameFileReload =
              pluginBypassRef.current &&
              currentFilePath === targetPath &&
              currentFileRoot === targetRoot;

            if (sameFileReload) {
              readMode = "incremental";
            } else {
              try {
                const plugin = pluginManagerRef.current.match(
                  root,
                  buildMatchInputFromPath(path, nextPluginQuery),
                );
                readMode = inferReadModeFromPlugin(plugin);
                requiresFull = readMode === "full";
              } catch {
                readMode = "incremental";
              }
            }
          }
          const cached = await getCachedFile({
            rootId: String(root),
            path: String(path),
            readMode,
            cursor,
          });
          if (cached && !isStale()) {
            setFile({
              ...cached,
              targetLine: parsedLocation.targetLine,
              targetColumn: parsedLocation.targetColumn,
            });
          }

          let next: FilePayload | null;
          try {
            next = await fetchFileWithMode(
              readMode,
              requiresFull ? undefined : readMode === "full" ? 1500 : undefined,
            );
          } catch (err) {
            if (readMode === "full" && !requiresFull) {
              next = await fetchFileWithMode("incremental");
              readMode = "incremental";
            } else {
              throw err;
            }
          }
          if (isStale()) {
            return;
          }
          if (next) {
            setFile({
              ...next,
              targetLine: parsedLocation.targetLine,
              targetColumn: parsedLocation.targetColumn,
            });
          }
          fileCursorRef.current = cursor;
          // 打开文件不解除会话选中：面板切到文件态而已，切回对话仍应看到原来那个会话。
          setDrawerOpenForRoot(root, false);
          if (isMobile) setIsLeftOpen(false);
        } catch (err) {
          console.error("[file.open] failed", { root, path, cursor, err });
        }
      },
      open_dir: async (params: any) => {
        fileOpenRequestRef.current += 1;
        const path = params.path,
          rootParam = params.root || currentRootIdRef.current,
          isToggle = !!params.toggle,
          forceDirectory = !!params.forceDirectory,
          suppressTreeExpand = !!params.suppressTreeExpand;
        if (!path || !rootParam) return;
        setGitDiff(null);
        const isActuallyRoot = params.isRoot === true;
        const root = isActuallyRoot ? path : rootParam;
        // 多节点同名项目：根级/换根打开时携带节点信息并应用为当前选中节点
        const resolvedNodeId =
          String(params.nodeId || "").trim() ||
          (root === String(currentRootIdRef.current || "")
            ? String(currentRootNodeIdRef.current || "")
            : "") ||
          getNodeIdForRoot(root) ||
          "";
        if (
          isActuallyRoot ||
          root !== String(currentRootIdRef.current || "")
        ) {
          selectRootNode(root, resolvedNodeId || undefined);
        }
        const expandedKey = expandKey(resolvedNodeId, root, path, isActuallyRoot);
        const preserveCollapsedRoot =
          isActuallyRoot &&
          suppressTreeExpand &&
          !expandedRef.current.includes(expandedKey);
        const restoreSuppressedRootExpansion = () => {
          if (!preserveCollapsedRoot) {
            return;
          }
          setExpanded((prev) => prev.filter((k) => k !== expandedKey));
        };
        const preserveQuery = !!params.preservePluginQuery;
        const nextPluginQuery = preserveQuery
          ? parsePluginQuery(window.location.search)
          : {};
        const loadDirectoryView = async (
          targetPath: string,
          targetIsRoot: boolean,
        ) => {
          const apiDir = targetIsRoot ? "." : targetPath;
          const cacheKey = treeCacheKey(root, apiDir);
          resetLocksForRootTransition(root);
          if (currentRootIdRef.current !== root) {
            // 换项目也算一次切换操作：关掉旧根的悬浮框，别留在 per-root 记忆里
            setDrawerOpenForRoot(currentRootIdRef.current, false);
            interactionModeRef.current = "main";
            setInteractionMode("main");
            setCurrentRootId(root);
          }
          // 只有「用户在左树里点文件夹/文件名」才切到文件列表；程序化打开目录一律保持当前模式
          if (params.switchToFiles === true) switchMainView("files");
          setFile(null);
          // 会话选中不在这里清：换根由上面 resetLocksForRootTransition 负责，
          // 同根内的目录切换必须保持选中（面板切走不解除选中）。
          setMainEntries([]);
          setMainDirectoryError("");
          setPluginQuery(nextPluginQuery);
          replaceURLState({
            root,
            file: "",
            session: "",
            cursor: 0,
            pluginQuery: nextPluginQuery,
          });
          const cachedEntries = entriesByPathRef.current[cacheKey];
          if (
            Object.prototype.hasOwnProperty.call(
              entriesByPathRef.current,
              cacheKey,
            ) &&
            !invalidTreeCacheKeysRef.current.has(cacheKey)
          ) {
            setMainEntries(cachedEntries || []);
            setMainDirectoryError("");
            setSelectedDir(targetPath);
            setSelectedDirKey(
              buildDirectorySelectionKey(resolvedNodeId, root, targetPath, targetIsRoot),
            );
            setFile(null);
            fileCursorRef.current = 0;
            setDrawerOpenForRoot(root, false);
            if (!isToggle && isMobile) setIsLeftOpen(false);
            return;
          }
          try {
            const payload = await apiProtectedJSON<any>(
              appURL("/api/tree", new URLSearchParams({ root, dir: apiDir }), getNodeIdForRoot(root)),
            );
            const parsed = normalizeTreeResponse(payload);
            invalidTreeCacheKeysRef.current.delete(cacheKey);
            setMainDirectoryError("");
            setEntriesByPath((prev) => ({
              ...prev,
              [cacheKey]: parsed.entries,
            }));
            setMainEntries(parsed.entries);
            setSelectedDir(targetPath);
            setSelectedDirKey(
              buildDirectorySelectionKey(resolvedNodeId, root, targetPath, targetIsRoot),
            );
            setFile(null);
            fileCursorRef.current = 0;
            setDrawerOpenForRoot(root, false);
            if (!isToggle && isMobile) setIsLeftOpen(false);
          } catch (error) {
            if (error instanceof ProtectedAPIError) {
              const message = formatDirectoryLoadError(
                typeof error.payload?.error === "string" ? error.payload.error : "",
              );
              setSelectedDir(targetPath);
              setSelectedDirKey(
                buildDirectorySelectionKey(resolvedNodeId, root, targetPath, targetIsRoot),
              );
              setMainEntries([]);
              setMainDirectoryError(message);
              reportError("file.read_failed", message);
              return;
            }
            const message = t("directory.loadFailedRetry");
            setSelectedDir(targetPath);
            setSelectedDirKey(
              buildDirectorySelectionKey(resolvedNodeId, root, targetPath, targetIsRoot),
            );
            setMainEntries([]);
            setMainDirectoryError(message);
            reportError("file.read_failed", message);
          }
        };
        const isExpanded = expandedRef.current.includes(expandedKey);
        const isCurrentExpandedRoot =
          isActuallyRoot && isExpanded && currentRootIdRef.current === root;
        if (
          isToggle &&
          ((!isActuallyRoot && isExpanded) || isCurrentExpandedRoot)
        ) {
          setExpanded((prev) => prev.filter((k) => k !== expandedKey));
          if (!isActuallyRoot) {
            const parentDir = dirnameOfPath(path);
            const parentPath = parentDir === "." ? root : parentDir;
            await loadDirectoryView(parentPath, parentDir === ".");
          }
          return;
        }
        if (isActuallyRoot) {
          resetLocksForRootTransition(path);
          setCurrentRootId(path);
          // 根树互斥：打开/激活一个根，收起其它根的展开树（只保留根自身键，子目录键原样保留）
          const otherRootKeys = new Set(
            rootEntriesRef.current
              .filter((entry) => entry.is_root === true && entry.path !== root)
              .map((entry) =>
                expandKey(String((entry as any)._nodeId || ""), entry.path, entry.path, true),
              ),
          );
          if (!suppressTreeExpand) {
            setExpanded((prev) => {
              const kept = prev.filter((k) => !otherRootKeys.has(k));
              const rootKey = expandKey(resolvedNodeId, path, path, true);
              // 换根是「减一个加一个」：长度可能不变，必须按内容判断是否已就位，
              // 不能用 prev.length===next.length 当 identity 守卫（会吞掉换根更新）。
              if (kept.length === prev.length && kept.includes(rootKey)) {
                return prev;
              }
              return Array.from(new Set([...kept, rootKey]));
            });
          } else {
            setExpanded((prev) => {
              const next = prev.filter((k) => !otherRootKeys.has(k));
              return prev.length === next.length ? prev : next;
            });
          }
          if (!forceDirectory) {
            const restored = await tryShowBoundSessionForRoot(path, {
              pluginQuery: nextPluginQuery,
              closeLeftSidebar: !isToggle,
            });
            if (restored) {
              void loadSessionsForRoot(path, { replace: true, force: true });
              if (mainViewRef.current === "files" && !mainEntriesRef.current.length && !mainDirectoryErrorRef.current) {
                // 文件态没有内容可显示：补上项目顶层目录，行为与手动切面板一致
                await actionHandlersRef.current.open_dir({
                  path,
                  root: path,
                  isRoot: true,
                  forceDirectory: true,
                });
              } else {
                await refreshTreeDir(path, ".", false);
              }
              restoreSuppressedRootExpansion();
              return;
            }
          }
        } else {
          setExpanded((prev) => Array.from(new Set([...prev, expandedKey])));
        }
        await loadDirectoryView(path, isActuallyRoot);
        restoreSuppressedRootExpansion();
      },
    }),
    [
      isMobile,
      normalizeTreeResponse,
      switchMainView,
      setDrawerOpenForRoot,
      replaceURLState,
      rememberCurrentFileScroll,
      treeCacheKey,
      tryShowBoundSessionForRoot,
      loadSessionsForRoot,
      resetLocksForRootTransition,
    ],
  );
  const actionHandlersRef = useRef(actionHandlers);
  useEffect(() => {
    actionHandlersRef.current = actionHandlers;
  }, [actionHandlers]);

  // 会话恢复入口（open_dir 开根 / 首屏 boot / popstate）共用：
  // 会话在场就直接返回，目录一次都不加载——带着 view=files 进来时主区就是空的。
  const showBoundSessionOrRootDir = useCallback(
    async (
      rootID: string,
      options?: {
        pluginQuery?: Record<string, string>;
        closeLeftSidebar?: boolean;
        loadSessionsOnRestore?: boolean;
        suppressTreeExpand?: boolean;
      },
    ) => {
      const restored = await tryShowBoundSessionForRoot(rootID, {
        pluginQuery: options?.pluginQuery,
        closeLeftSidebar: options?.closeLeftSidebar,
      });
      if (!restored) {
        await actionHandlersRef.current.open_dir({
          path: rootID,
          root: rootID,
          isRoot: true,
          forceDirectory: true,
          suppressTreeExpand: options?.suppressTreeExpand,
        });
        return;
      }
      if (options?.loadSessionsOnRestore) {
        void loadSessionsForRoot(rootID, { replace: true, force: true });
      }
      // 文件态没有内容可显示：把当前项目顶层目录补上，行为与手动切面板一致
      if (
        mainViewRef.current === "files" &&
        !mainEntriesRef.current.length &&
        !mainDirectoryErrorRef.current
      ) {
        await actionHandlersRef.current.open_dir({
          path: rootID,
          root: rootID,
          isRoot: true,
          forceDirectory: true,
          suppressTreeExpand: true,
        });
        return;
      }
      await refreshTreeDir(rootID, ".", false);
    },
    [loadSessionsForRoot, refreshTreeDir, tryShowBoundSessionForRoot],
  );

  const openRelatedFileDiff = useCallback(
    async (rootID: string, file: RelatedFileClickTarget) => {
      const path = String(file?.path || "").trim();
      const head = String(file?.head || "").trim();
      const repoKind = String(file?.repo_kind || "").trim();
      if (!rootID || !path) {
        return;
      }
      setRelatedSelectedFileKey(relatedFileSelectionKey(file));
      if (repoKind === "plain") {
        actionHandlers.open({ path, root: rootID, preserveRelatedSelection: true });
        return;
      }
      if (!head && !file?.repo_path) {
        const gitItem = (gitStatus?.items || []).find(
          (item) => item.path === path,
        );
        if (gitItem) {
          void openGitDiff(rootID, gitItem, { preserveRelatedSelection: true });
          return;
        }
        actionHandlers.open({ path, root: rootID, preserveRelatedSelection: true });
        return;
      }
      fileOpenRequestRef.current += 1;
      switchMainView("files");
      setSelectedSession(null);
      setSelectedSessionLoading(false);
      setFile(null);
      setGitDiff(null);
      replaceURLState({
        root: rootID,
        file: "",
        session: "",
        cursor: 0,
        pluginQuery: {},
      });
      try {
        const next = await fetchGitRelatedFileDiff(rootID, file, getNodeIdForRoot(rootID));
        const rootPath = managedRootByIdRef.current[rootID]?.root_path;
        const repoPath = String(file?.repo_path || "").trim();
        const displayPath = repoPath
          ? relativeDisplayPathFromRoot(rootPath, joinDisplayPath(repoPath, path))
          : path;
        if (displayPath) {
          next.display_path = displayPath;
        }
        setGitDiff(next);
        resetLocksForRootTransition(rootID);
        if (currentRootIdRef.current !== rootID) {
          setCurrentRootId(rootID);
        }
        if (isMobile) {
          setIsLeftOpen(false);
        }
      } catch (err) {
        const message =
      err instanceof Error ? err.message : t("git.relatedFileDiffFailed");
        console.error("[git.related-file.diff] failed", {
          rootID,
          path,
          head,
          err,
        });
        reportError("git.related_file_diff_failed", message, {
          severity: "warning",
          recoverable: true,
          details: {
            root: rootID,
            path,
            head,
            payload: err instanceof ProtectedAPIError ? err.payload : undefined,
          },
        });
      }
    },
    [
      actionHandlers,
      gitStatus,
      isMobile,
      openGitDiff,
      replaceURLState,
      switchMainView,
      resetLocksForRootTransition,
    ],
  );

  const loadManagedRootPayloads = useCallback(async (opts?: { force?: boolean }) => {
    if (bootstrapService.snapshot().phase !== "ready") {
      return null;
    }
    if (!opts?.force && managedRootsRequestRef.current) {
      return managedRootsRequestRef.current;
    }
    if (opts?.force) {
      managedRootsRequestRef.current = null;
    }
    const request = (async () => {
      nodeLoadFailuresRef.current = [];
      try {
        try { migrateLegacySingleBase(); } catch {}
        // 冷启动时 getNodes() 首屏只有 local，需等待服务端节点同步后再取全量
        try {
          if (!opts?.force) {
            await syncNodesFromServer().catch(() => {});
          }
        } catch {}
        const nodes = getNodes();
        const targets = nodes;
        if (!targets.length) {
          const dirs = await apiProtectedJSON<ManagedRootPayload[]>(appPath("/api/dirs"));
          return Array.isArray(dirs) ? dirs : [];
        }
        const nodeFailures: Array<{ id: string; name: string }> = [];
        // 跨机器也带 user=（用户名，见 base.ts）：对方按用户名解析到它本地的账户，
        // 取到的确实是「我的」数据，不再是它主账户的回落。
        // 对方没有这个账户时服务端回 **200 空列表**（不是 404）——所以这里没有
        // 缺账户的特殊分支：空就是空，节点照常显示，只是没有项目。
        const results = await Promise.all(targets.map(async (n) => {
          try {
            const dirs = await withNodeRetry(() => apiProtectedJSON<ManagedRootPayload[]>(appPath("/api/dirs", n.id)));
            return (Array.isArray(dirs) ? dirs : []).map((d: any) => ({ ...d, _nodeId: (d as any)._nodeId || n.id, _nodeColor: n.color, _nodeName: n.name }));
          } catch (err) {
            nodeFailures.push({ id: String(n.id), name: String(n.name || n.id) });
            return [] as ManagedRootPayload[];
          }
        }));
        nodeLoadFailuresRef.current = nodeFailures;
        const flat = results.flat() as ManagedRootPayload[];
        const seen = new Set<string>();
        const deduped: ManagedRootPayload[] = [];
        for (const d of flat) { const id = String((d as any).id || ""); const nid = String((d as any)._nodeId || ""); const key = `${nid}::${id}`; if (!id || seen.has(key)) continue; seen.add(key); deduped.push(d); }
        return deduped;
      } catch {
        return null;
      }
    })().finally(() => {
      managedRootsRequestRef.current = null;
    });
    managedRootsRequestRef.current = request;
    return request;
  }, [bootstrapState.phase]);

  const refreshManagedRoots = useCallback(async () => {
    const dirs = await loadManagedRootPayloads();
    if (!dirs) {
      return;
    }
    // 抓取失败的节点：提示 + 给重试入口。恢复后清掉记录，下次再失败能重新提示。
    const failedNodeIds = new Set(nodeLoadFailuresRef.current.map((f) => f.id));
    for (const id of Array.from(notifiedNodeFailuresRef.current)) {
      if (!failedNodeIds.has(id)) notifiedNodeFailuresRef.current.delete(id);
    }
    for (const failed of nodeLoadFailuresRef.current) {
      notifyNodeLoadFailedRef.current(failed);
    }
    const nextDirs = Array.isArray(dirs) ? dirs : [];
    const nextRootIds = nextDirs.map((dir) => dir.id).filter(Boolean);
    const previousRootById = managedRootByIdRef.current;
    const indexed = indexManagedRoots(nextDirs as ManagedRootPayload[]);
    const nextRootById = indexed.byId;
    let clearedRootScopedState = false;
    for (const rootID of Object.keys(previousRootById)) {
      if (!(rootID in nextRootById)) {
        clearRootScopedClientState(rootID, { removeLastRoot: true });
        clearedRootScopedState = true;
      }
    }
    for (const rootID of nextRootIds) {
      const previousPath = comparableManagedRootPath(previousRootById[rootID]?.root_path);
      const nextPath = comparableManagedRootPath(nextRootById[rootID]?.root_path);
      if (previousPath && nextPath && previousPath !== nextPath) {
        clearRootScopedClientState(rootID);
        clearedRootScopedState = true;
      }
    }
    managedRootByKeyRef.current = { ...managedRootByKeyRef.current, ...indexed.byKey };
    // D2: 保留当前选中根的 bare 槽位，避免同名项目被本地蓝覆盖（瞬时全蓝根因）
    {
      const rid = String(currentRootIdRef.current || "").trim();
      const curNid = String(currentRootNodeIdRef.current || "").trim();
      if (rid && curNid) {
        const scoped = scopeKey(curNid, rid);
        const keep = (indexed.byKey as any)[scoped] || (managedRootByKeyRef.current as any)[scoped];
        if (keep) nextRootById[rid] = keep;
      }
    }
    managedRootByIdRef.current = nextRootById;
    setRootNodeMap(managedRootByIdRef.current as Record<string, any>);
    {
      const curRid = String(currentRootIdRef.current || "").trim();
      const curNid = String(currentRootNodeIdRef.current || "").trim();
      const curSlot = curRid && curNid ? `${curNid}::${curRid}` : "(none)";
    }
    // 任何索引变化（新增节点/项目/路径）都应重拉多节点会话，确保 PC 等远端分组首屏即出现
    // 之前仅在 cleared/keySetChanged 时触发，导致清缓存冷启动 PC 迟迟不出现、需点会话才补齐
    if (multiProjectSessionsEnabled) {
      void refreshMultiProjectReplyingSessions();
      void loadMultiProjectSessionGroups();
    }

    managedRootIdsRef.current = new Set(nextRootIds);
    setManagedRootIds(nextRootIds);
    setRootEntries(mapManagedRootsToEntries(nextDirs));
    Object.keys(selectedSessionByRootRef.current).forEach((rootID) => {
      if (!nextRootIds.includes(rootID)) {
        delete selectedSessionByRootRef.current[scopedRootKey(rootID)];
      }
    });

    if (nextRootIds.length === 0) {
      setCurrentRootId(null);
      setSelectedDir(null);
      setSelectedDirKey(null);
      setMainEntries([]);
      setFile(null);
      setSelectedSession(null);
      setSelectedSessionLoading(false);
      setSessions([]);
      setCurrentSession(null);
      setActiveBoundSessionKey(null);
      selectedSessionByRootRef.current = {};
      setInteractionMode("main");
      setIsDrawerOpen(false);
      replaceURLState({
        root: "",
        file: "",
        session: "",
        cursor: 0,
        pluginQuery: {},
      });
      return;
    }

    const currentRoot = currentRootIdRef.current;
    if (currentRoot && nextRootIds.includes(currentRoot)) {
      return;
    }

    const lastRoot = loadLastRootId();
    const lastNode = loadLastRootNodeId();
    const nextRoot =
      lastRoot && nextRootIds.includes(lastRoot) ? lastRoot : nextRootIds[0];
    const preferredDir =
      nextDirs.find(
        (d) => d.id === nextRoot && (!lastNode || (d as any)._nodeId === lastNode),
      ) ||
      nextDirs.find((d) => d.id === nextRoot);
    await actionHandlersRef.current.open_dir({
      path: nextRoot,
      root: nextRoot,
      preservePluginQuery: true,
      isRoot: true,
      nodeId: (preferredDir as any)?._nodeId || undefined,
    });
  }, [
    clearRootScopedClientState,
    loadManagedRootPayloads,
    loadMultiProjectSessionGroups,
    multiProjectSessionsEnabled,
    refreshMultiProjectReplyingSessions,
    replaceURLState,
  ]);

  useEffect(() => {
    const h = () => { void refreshManagedRoots(); };
    window.addEventListener("mindfs:nodes-changed", h);
    return () => window.removeEventListener("mindfs:nodes-changed", h);
  }, [refreshManagedRoots]);

  // 同一节点在一次故障里只提示一次（刷新很频繁，每次都弹会淹掉界面）。
  const notifyNodeLoadFailed = useCallback(
    (node: { id: string; name: string }) => {
      if (notifiedNodeFailuresRef.current.has(node.id)) return;
      notifiedNodeFailuresRef.current.add(node.id);
      reportError("node.load_failed", t("error.node.loadFailed", { name: node.name }), {
        severity: "warning",
        recoverable: true,
        details: { nodeId: node.id },
        retryAction: async () => {
          notifiedNodeFailuresRef.current.delete(node.id);
          await refreshManagedRoots();
        },
      });
    },
    [refreshManagedRoots, t],
  );
  useEffect(() => {
    notifyNodeLoadFailedRef.current = notifyNodeLoadFailed;
  }, [notifyNodeLoadFailed]);

  const applyManagedRootRename = useCallback(
    (oldRootID: string, rootPayload: ManagedRootPayload | null | undefined) => {
      const oldID = String(oldRootID || "").trim();
      const nextID = String(rootPayload?.id || "").trim();
      if (!oldID || !nextID) {
        return false;
      }

      const previousRoot =
        managedRootByIdRef.current[oldID] ||
        managedRootByIdRef.current[nextID] ||
        ({} as ManagedRootPayload);
      const nextRoot = {
        ...previousRoot,
        ...rootPayload,
        id: nextID,
      } as ManagedRootPayload;

      const nextRootById = { ...managedRootByIdRef.current };
      delete nextRootById[oldID];
      nextRootById[nextID] = nextRoot;
      managedRootByIdRef.current = nextRootById;
      setRootNodeMap(nextRootById as Record<string, any>);

      const moveRecordKey = <T,>(record: Record<string, T>) => {
        if (oldID === nextID) {
          return;
        }
        for (const k of Object.keys(record)) {
          const idx = k.lastIndexOf("::");
          const rid = idx >= 0 ? k.slice(idx + 2) : k;
          if (rid !== oldID) {
            continue;
          }
          const nextKey = idx >= 0 ? `${k.slice(0, idx + 2)}${nextID}` : nextID;
          record[nextKey] = record[k];
          delete record[k];
        }
      };
      moveRecordKey(boundSessionByRootRef.current);
      moveRecordKey(suppressedAutoBindSessionByRootRef.current);
      moveRecordKey(drawerSessionByRootRef.current);
      moveRecordKey(selectedSessionByRootRef.current);
      moveRecordKey(drawerOpenByRootRef.current);
      moveRecordKey(pluginsLoadedByRootRef.current);
      moveRecordKey(pluginsLoadingByRootRef.current);

      const moveStateRecord = <T,>(record: Record<string, T>) => {
        if (oldID === nextID) {
          return record;
        }
        const next: Record<string, T> = {};
        for (const [k, v] of Object.entries(record)) {
          const idx = k.lastIndexOf("::");
          const rid = idx >= 0 ? k.slice(idx + 2) : k;
          const nextKey = idx >= 0 ? `${k.slice(0, idx + 2)}${nextID}` : nextID;
          next[rid === oldID ? nextKey : k] = v;
        }
        return next;
      };
      setGitStatusExpandedByRoot((prev) => moveStateRecord(prev));
      setGitHistoryExpandedByRoot((prev) => moveStateRecord(prev));

      const remapSessionCacheKey = (key: string): string | null => {
        const parts = key.split("::");
        if (parts.length === 3) {
          if (parts[1] !== oldID) return null;
          return `${parts[0]}::${nextID}::${parts.slice(2).join("::")}`;
        }
        if (parts.length === 2) {
          if (parts[0] !== oldID) return null;
          return `${nextID}::${parts[1]}`;
        }
        return null;
      };
      const moveCacheRecord = <T,>(record: Record<string, T>) => {
        if (oldID === nextID) {
          return;
        }
        for (const key of Object.keys(record)) {
          const nextKey = remapSessionCacheKey(key);
          if (nextKey === null) continue;
          record[nextKey] = record[key];
          delete record[key];
        }
      };
      moveCacheRecord(sessionCacheRef.current);
      moveCacheRecord(loadedSessionRef.current);
      if (oldID !== nextID) {
        staleSessionKeysRef.current = new Set(
          Array.from(staleSessionKeysRef.current)
            .map((key) => remapSessionCacheKey(key) ?? key),
        );
      }

      const moveTreeRecord = <T,>(record: Record<string, T>) => {
        if (oldID === nextID) {
          return record;
        }
        const next = { ...record };
        for (const key of Object.keys(record)) {
          const afterScope = key.includes("::")
            ? key.slice(key.lastIndexOf("::") + 2)
            : key;
          const idx = afterScope.indexOf(":");
          const rid = idx >= 0 ? afterScope.slice(0, idx) : afterScope;
          if (rid !== oldID) {
            continue;
          }
          const head = key.slice(0, key.length - afterScope.length);
          const nextAfter = `${nextID}${idx >= 0 ? afterScope.slice(idx) : ""}`;
          next[`${head}${nextAfter}`] = record[key];
          delete next[key];
        }
        return next;
      };
      entriesByPathRef.current = moveTreeRecord(entriesByPathRef.current);
      setEntriesByPath((prev) => moveTreeRecord(prev));
      if (oldID !== nextID) {
        invalidTreeCacheKeysRef.current = new Set(
          Array.from(invalidTreeCacheKeysRef.current).map((key) => {
            const afterScope = key.includes("::")
              ? key.slice(key.lastIndexOf("::") + 2)
              : key;
            const idx = afterScope.indexOf(":");
            const rid = idx >= 0 ? afterScope.slice(0, idx) : afterScope;
            if (rid !== oldID) return key;
            const head = key.slice(0, key.length - afterScope.length);
            return `${head}${nextID}${idx >= 0 ? afterScope.slice(idx) : ""}`;
          }),
        );
      }

      setManagedRootIds((prev) => {
        const source = prev.length ? prev : Array.from(managedRootIdsRef.current);
        let replaced = false;
        const nextIds = source.map((id) => {
          if (id !== oldID) {
            return id;
          }
          replaced = true;
          return nextID;
        });
        if (!replaced && !nextIds.includes(nextID)) {
          nextIds.push(nextID);
        }
        const deduped = Array.from(new Set(nextIds.filter(Boolean)));
        managedRootIdsRef.current = new Set(deduped);
        setRootEntries(
          mapManagedRootsToEntries(
            deduped
              .map((id) => managedRootByIdRef.current[id])
              .filter((dir): dir is ManagedRootPayload => !!dir),
          ),
        );
        return deduped;
      });
      return true;
    },
    [],
  );

  const normalizeComparableRootPath = useCallback((value: string): string => {
    let normalized = String(value || "").trim().replace(/\\/g, "/").replace(/\/+$/, "");
    if (/^[a-z]:/i.test(normalized)) {
      normalized = normalized.toLowerCase();
    }
    return normalized;
  }, []);

  const findManagedRootByPath = useCallback((path: string): ManagedRootPayload | null => {
    const target = normalizeComparableRootPath(path);
    if (!target) {
      return null;
    }
    for (const root of Object.values(managedRootByIdRef.current)) {
      if (normalizeComparableRootPath(root.root_path || "") === target) {
        return root;
      }
    }
    return null;
  }, [normalizeComparableRootPath]);

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

  const handleRenameCurrentRoot = useCallback(
    async (nextName: string) => {
      const rootID = currentRootIdRef.current;
      const trimmedName = String(nextName || "").trim();
      if (!rootID || !trimmedName) {
        return false;
      }
      const currentDisplayName = String((managedRootByIdRef.current[rootID] as any)?.display_name || managedRootByIdRef.current[rootID]?.id || "").trim();
      if (trimmedName === currentDisplayName) {
        return true;
      }
      try {
        const renamed = await apiProtectedJSON<ManagedRootPayload>(
          appPath(`/api/dirs/${encodeURIComponent(rootID)}/display-name`),
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ display_name: trimmedName }),
          },
        );
        // Display-name rename never changes id/path: patch local map in place via refresh; keep compatibility with physical rename helper
        if (String(renamed?.id || "").trim() && String(renamed.id).trim() !== rootID) {
          applyManagedRootRename(rootID, renamed);
          await actionHandlersRef.current.open_dir({
            path: renamed.id,
            root: renamed.id,
            isRoot: true,
            forceDirectory: true,
          });
        } else {
          // Same id: merge display_name in place without shuffling per-root caches (session keys stay valid)
          const merged = { ...(managedRootByIdRef.current[rootID] || {} as ManagedRootPayload), ...(renamed || {}), id: rootID } as ManagedRootPayload;
          const nextById = { ...managedRootByIdRef.current, [rootID]: merged };
          managedRootByIdRef.current = nextById as Record<string, ManagedRootPayload>;
          const renameNid = String((merged as any)._nodeId || "").trim();
          if (renameNid) managedRootByKeyRef.current[rootNodeKey(renameNid, rootID)] = merged;
          setRootNodeMap(nextById as Record<string, any>);
          const ids = Array.from(managedRootIdsRef.current);
          setRootEntries(mapManagedRootsToEntries(ids.map((id) => nextById[id]).filter(Boolean) as ManagedRootPayload[]));
          setMultiProjectSessionGroups((prev) =>
            prev.map((g) =>
              g.rootId === rootID
                ? { ...g, rootName: String((merged as any)?.display_name || (merged as any)?.id || g.rootName || rootID) }
                : g,
            ),
          );
        }
        return true;
      } catch (err) {
        reportError(
          "root.rename_failed",
          managedDirAddErrorMessage(err, t("root.renameProjectFailed"), t),
        );
        return false;
      }
    },
    [applyManagedRootRename, t],
  );

  const handleRemoveCurrentRoot = useCallback(async () => {
    const rootID = currentRootIdRef.current;
    if (!rootID) {
      return;
    }
    const rootInfo = managedRootByIdRef.current[rootID];
    const rootPath = rootInfo?.root_path || "";
    if (!rootPath) {
      reportError("root.delete_failed", t("root.missingPathRemove"));
      return;
    }
    if (!await confirmDialog({ message: t("root.confirmRemove", { name: rootID }), danger: true })) {
      return;
    }
    try {
      await apiProtectedJSON<any>(
        appURL("/api/dirs", new URLSearchParams({ path: rootPath }), getNodeIdForRoot(rootID)),
        {
          method: "DELETE",
        },
      );
      clearRootScopedClientState(rootID, { removeLastRoot: true });
      await refreshManagedRoots();
    } catch (err) {
      reportError(
        "root.delete_failed",
        String((err as Error)?.message || t("root.removeFailed")),
      );
    }
  }, [clearRootScopedClientState, refreshManagedRoots, t]);

  const handleRemoveCurrentWorktree = useCallback(async () => {
    const rootID = currentRootIdRef.current;
    if (!rootID) {
      return;
    }
    if (!await confirmDialog({ message: t("worktree.confirmRemove", { name: rootID }), danger: true })) {
      return;
    }
    try {
      await removeGitWorktree(rootID);
      clearRootScopedClientState(rootID, { removeLastRoot: true });
      await refreshManagedRoots();
    } catch (err) {
      reportError(
        "git.worktree_remove_failed",
        String((err as Error)?.message || t("worktree.removeFailed")),
      );
    }
  }, [clearRootScopedClientState, refreshManagedRoots, t]);

  const ensurePluginsLoaded = useCallback(async (rootId: string) => {
    if (!rootId || pluginsLoadedByRootRef.current[scopedRootKey(rootId)]) {
      return;
    }
    if (pluginsTrustPendingByRootRef.current[scopedRootKey(rootId)]) {
      return;
    }
    const inflight = pluginsLoadingByRootRef.current[scopedRootKey(rootId)];
    if (inflight) {
      await inflight;
      return;
    }
    setPluginLoading(true);
    const request = scanPluginSources(rootId, managedRootByIdRef.current[rootId]?.root_path || rootId, getNodeIdForRoot(rootId))
      .then(async (bundle) => {
        const snapshot = snapshotFromPluginSources(bundle);
        if (bundle.plugins.length > 0 && !isPluginSnapshotTrusted(snapshot, readTrustedPluginSet(rootId))) {
          pluginManagerRef.current.clear(rootId);
          pluginsTrustPendingByRootRef.current[scopedRootKey(rootId)] = true;
          setPendingPluginTrust({ rootId, bundle });
          setPluginVersion((v) => v + 1);
          return;
        }
        const plugins = await loadPluginsFromSources(bundle.plugins);
        pluginManagerRef.current.set(rootId, plugins);
        pluginsLoadedByRootRef.current[scopedRootKey(rootId)] = true;
        setPluginVersion((v) => v + 1);
      })
      .catch(() => {
        pluginManagerRef.current.clear(rootId);
        pluginsLoadedByRootRef.current[scopedRootKey(rootId)] = true;
        setPluginVersion((v) => v + 1);
      })
      .finally(() => {
        delete pluginsLoadingByRootRef.current[scopedRootKey(rootId)];
        setPluginLoading(false);
      });
    pluginsLoadingByRootRef.current[scopedRootKey(rootId)] = request;
    await request;
  }, []);

  const handleTrustPendingPlugins = useCallback(async () => {
    const pending = pendingPluginTrust;
    if (!pending) return;
    setPluginLoading(true);
    try {
      const snapshot = snapshotFromPluginSources(pending.bundle);
      saveTrustedPluginSet(pending.rootId, snapshot);
      const plugins = await loadPluginsFromSources(pending.bundle.plugins);
      pluginManagerRef.current.set(pending.rootId, plugins);
      pluginsLoadedByRootRef.current[scopedRootKey(pending.rootId)] = true;
      delete pluginsTrustPendingByRootRef.current[scopedRootKey(pending.rootId)];
      setPendingPluginTrust((current) => current?.rootId === pending.rootId ? null : current);
      setPluginVersion((v) => v + 1);
    } finally {
      setPluginLoading(false);
    }
  }, [pendingPluginTrust]);

  const handleDisablePendingPlugins = useCallback(() => {
    const pending = pendingPluginTrust;
    if (!pending) return;
    pluginManagerRef.current.clear(pending.rootId);
    pluginsLoadedByRootRef.current[scopedRootKey(pending.rootId)] = true;
    delete pluginsTrustPendingByRootRef.current[scopedRootKey(pending.rootId)];
    setPendingPluginTrust((current) => current?.rootId === pending.rootId ? null : current);
    setPluginVersion((v) => v + 1);
  }, [pendingPluginTrust]);

  const invalidatePluginsForRoot = useCallback((rootId: string) => {
    if (!rootId) return;
    pluginManagerRef.current.clear(rootId);
    delete pluginsLoadedByRootRef.current[scopedRootKey(rootId)];
    delete pluginsLoadingByRootRef.current[scopedRootKey(rootId)];
    delete pluginsTrustPendingByRootRef.current[scopedRootKey(rootId)];
    setPendingPluginTrust((current) => current?.rootId === rootId ? null : current);
    setPluginVersion((v) => v + 1);
    if (rootId === currentRootIdRef.current) {
      void ensurePluginsLoaded(rootId).catch(() => {});
    }
  }, [ensurePluginsLoaded]);

  const pluginHandlers = useMemo(
    () => ({
      open: async (params: Record<string, unknown>) => {
        await actionHandlers.open(params);
      },
      open_dir: async (params: Record<string, unknown>) => {
        await actionHandlers.open_dir(params);
      },
      select_session: async (params: Record<string, unknown>) => {
        const key = typeof params?.key === "string" ? params.key : "";
        if (!key) return;
        const root = currentRootIdRef.current;
        if (!root) return;
        const matched = sessions.find(
          (item) => (item.key || item.session_key) === key,
        );
        if (matched) {
          await handleSelectSession(matched);
          return;
        }
        await handleSelectSession({ key, session_key: key, root_id: root });
      },
      navigate: async (params: Record<string, unknown>) => {
        const current = readURLState();
        const nextRoot = current.root || currentRootIdRef.current || "";
        const nextPath =
          typeof params?.path === "string" ? params.path : current.file;
        const explicitCursor = normalizeCursor(params?.cursor);
        const rawQuery =
          params?.query &&
          typeof params.query === "object" &&
          !Array.isArray(params.query)
            ? (params.query as Record<string, unknown>)
            : null;

        const nextPluginQuery: Record<string, string> = {
          ...current.pluginQuery,
        };
        if (rawQuery) {
          Object.entries(rawQuery).forEach(([key, value]) => {
            if (!key) return;
            nextPluginQuery[key] = String(value);
          });
        }

        let nextCursor = current.cursor;
        if (explicitCursor !== null) {
          nextCursor = explicitCursor;
        } else if (
          typeof params?.path === "string" &&
          params.path !== current.file
        ) {
          nextCursor = 0;
        }
        if (!nextPath) {
          nextCursor = 0;
        }

        const nextState: URLState = {
          root: nextRoot || "",
          file: nextPath || "",
          session: "",
          cursor: nextCursor,
          pluginQuery: nextPluginQuery,
        };
        replaceURLState(nextState);
        if (nextState.file) {
          persistPluginQuery(
            nextState.root,
            nextState.file,
            nextState.pluginQuery,
            getNodeIdForRoot(nextState.root),
          );
        }

        const rootChanged =
          (nextState.root || "") !== (currentRootIdRef.current || "");
        const fileChanged =
          (nextState.file || "") !== (fileRef.current?.path || "");
        const pluginChanged =
          JSON.stringify(nextState.pluginQuery) !==
          JSON.stringify(current.pluginQuery);

        if (pluginChanged) {
          setPluginQuery(nextState.pluginQuery);
        }

        if (!nextState.file) {
          if (nextState.root) {
            await actionHandlers.open_dir({
              path: nextState.root,
              root: nextState.root,
              preservePluginQuery: true,
              isRoot: true,
            });
          }
          return;
        }

        const cursorChanged = nextState.cursor !== fileCursorRef.current;
        if (rootChanged || fileChanged || cursorChanged) {
          await actionHandlers.open({
            path: nextState.file,
            root: nextState.root,
            cursor: nextState.cursor,
            preservePluginQuery: true,
          });
        }
      },
    }),
    [actionHandlers, sessions, handleSelectSession, replaceURLState],
  );

  const handleSessionChipClick = useCallback(
    (sessionKey: string, rootOverride?: string | null) => {
      if (!sessionKey) return;
      const root = rootOverride || file?.root || currentRootIdRef.current;
      if (!root) return;
      const matched = sessions.find((item) => {
        const key = item.key || item.session_key;
        return key === sessionKey;
      });
      if (matched) {
        handleSelectSession(matched);
        return;
      }
      handleSelectSession({
        key: sessionKey,
        session_key: sessionKey,
        root_id: root,
      });
    },
    [file, sessions, handleSelectSession],
  );

	  const handleTaskSessionDrawerOpen = useCallback(
    (sessionKey: string, rootOverride?: string | null, taskId?: string) => {
      const key = String(sessionKey || "").trim();
      const root = rootOverride || currentRootIdRef.current;
      if (!key || !root) return;
      // 任务会话就是普通会话：直接走主面板装配（selectedSession + main 模式 + URL + 同步），
      // 不再走 drawer 浮层——drawer 模式下在会话面板里切换工作台/看板没有全屏语境。
      const matched = sessions.find((item) => (item.key || item.session_key) === key);
      const cached = sessionCacheRef.current[rootSessionKey(root, key)];
      const initial = cached || matched || {
        key,
        session_key: key,
        root_id: root,
        task_id: taskId || "",
      };
      void handleSelectSession(initial as any, { preserveTaskSelection: true });
    },
    [
      currentRootIdRef,
      handleSelectSession,
      rootSessionKey,
      sessionCacheRef,
      sessions,
    ],
  );

  const refreshTaskRelatedFiles = useCallback(async (
	    root: string,
	    taskId: string,
	    sessionKeys: string[],
	  ) => {
	    const keys = Array.from(new Set(
	      sessionKeys
	        .map((key) => String(key || "").trim())
	        .filter(Boolean),
	    ));
	    if (!root || !taskId || keys.length === 0) return;
	    const relatedFileGroups = await Promise.all(
	      keys.map(async (sessionKey) => {
	        const relatedFiles = await sessionService.getSessionRelatedFiles(root, sessionKey, getNodeIdForRoot(root));
	        await setCachedSessionRelatedFiles(root, sessionKey, relatedFiles, getNodeIdForRoot(root));
	        updateSessionRelatedFilesForKey(root, sessionKey, relatedFiles);
	        return relatedFiles;
	      }),
	    );
	    setTaskRelatedFilesById((prev) => ({ ...prev, [taskId]: mergeRelatedFileGroups(relatedFileGroups) }));
	  }, [updateSessionRelatedFilesForKey]);

	  const refreshTasksForRelatedSession = useCallback((root: string, sessionKey: string) => {
	    const taskIds = taskIdsForUpdatedSession(taskSessionKeysByIdRef.current, sessionKey);
	    taskIds.forEach((taskId) => {
	      const detail = taskDetailsByIdRef.current[taskId];
	      const task = detail?.task;
	      if (!task || task.root_id !== root) return;
	      const sessionKeys = Array.from(new Set(
	        [...(taskSessionKeysByIdRef.current[taskId] || []), task.main_session_key]
	          .map((key) => String(key || "").trim())
	          .filter(Boolean),
	      ));
	      void refreshTaskRelatedFiles(root, taskId, sessionKeys).catch((error) => {
	        console.error("[task.related_files] refresh from session event failed", { root, taskId, sessionKey, error });
	      });
	    });
	  }, [refreshTaskRelatedFiles]);

	  const handleSelectKanbanTask = useCallback((task: KanbanTask) => {
	    const taskId = String(task.id || "");
	    if (!taskId) return;
	    setSelectedKanbanTaskId((prev) => prev === taskId ? "" : taskId);
	    const root = task.root_id || currentRootIdRef.current || "";
	    const sessionKeys = Array.from(new Set(
	      [...(taskSessionKeysById[taskId] || []), task.main_session_key]
	        .map((key) => String(key || "").trim())
	        .filter(Boolean),
	    ));
	    if (!root || sessionKeys.length === 0) return;
	    void refreshTaskRelatedFiles(root, taskId, sessionKeys)
	      .catch((error) => {
	        console.error("[task.related_files] failed", { root, taskId, sessionKeys, error });
	      });
	  }, [refreshTaskRelatedFiles, taskSessionKeysById]);

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

  const handleFileViewerPathClick = useCallback(
    (path: string) => {
      if (!file) return;
      const root = file.root || currentRootIdRef.current;
      if (!root) return;
      actionHandlers.open_dir({
        path: path === "." ? root : path,
        root,
        isRoot: path === ".",
        suppressTreeExpand: path === ".",
      });
    },
    [file, actionHandlers],
  );

  const handleFileViewerFileClick = useCallback(
    (path: string) => {
      if (!file) return;
      const root = file.root || currentRootIdRef.current;
      if (!root) return;
      actionHandlers.open({ path, root });
    },
    [file, actionHandlers],
  );

  const handleGitDiffPathClick = useCallback(
    (path: string) => {
      const root = currentRootIdRef.current;
      if (!root) return;
      setGitDiff(null);
      actionHandlers.open_dir({
        path: path === "." ? root : path,
        root,
        isRoot: path === ".",
        suppressTreeExpand: path === ".",
      });
    },
    [actionHandlers],
  );

  const handleDirectoryPathClick = useCallback(
    (path: string) => {
      const root = currentRootIdRef.current;
      if (!root) return;
      actionHandlers.open_dir({
        path: path === "." ? root : path,
        root,
        isRoot: path === ".",
        suppressTreeExpand: path === ".",
      });
    },
    [actionHandlers],
  );

  const visibleMainEntries = useMemo(
    () =>
      showHiddenFiles
        ? mainEntries
        : mainEntries.filter((entry) => !entry.name.startsWith(".")),
    [mainEntries, showHiddenFiles],
  );

  const gitFileStatsByPath = useMemo<Record<string, GitFileStat>>(() => {
    const items = gitStatus?.items || [];
    return Object.fromEntries(
      items.map((item) => [
        item.path,
        {
          status: item.status,
          additions: item.additions,
          deletions: item.deletions,
        },
      ]),
    );
  }, [gitStatus]);

  useEffect(() => {
    if (!currentRootId) return;
    sessionService.connect(currentRootId, getNodeIdForRoot(currentRootId));
  }, [currentRootId, currentRootNodeId]);

  useEffect(
    () => () => {
      sessionService.disconnect();
      setStatus("disconnected");
    },
    [],
  );

  useEffect(() => {
    if (!currentRootId) {
      setGitStatus(null);
      setGitHistory(null);
      setGitDiff(null);
      setSessionListMode("local");
      setExternalSessions([]);
      setExternalSelectedKey("");
      return;
    }
    void refreshGitStatus(currentRootId);
    void refreshGitHistory(currentRootId);
    setGitDiff(null);
  }, [currentRootId, currentRootNodeId, refreshGitHistory, refreshGitStatus]);

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
      setSelectedPendingByKey,
      setSelectedSession,
      setSessions,
      setSlashCommandResults,
      setStatus,
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
      loadSessionsForRoot,
      markSessionPending,
      markSessionStale,
      playCompletionSound,
      promotePendingSessionForRoot,
      refreshCurrentFileContent,
      refreshGitStatus,
      refreshManagedRoots,
      refreshMultiProjectReplyingSessions,
      refreshTaskWorktree,
      refreshTasksForRelatedSession,
      refreshTreeDir,
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

  useEffect(() => {
    if (!currentRootId) return;
    if (pluginsLoadedByRootRef.current[scopedRootKey(currentRootId)]) return;
    void ensurePluginsLoaded(currentRootId).catch(() => {});
  }, [currentRootId, ensurePluginsLoaded]);

  const handleLoadOlderSessions = useCallback(async () => {
    const rootID = currentRootIdRef.current;
    const oldest =
      sessionsRef.current[sessionsRef.current.length - 1]?.updated_at || "";
    if (!rootID || !oldest || loadingOlderSessions) {
      return;
    }
    setLoadingOlderSessions(true);
    try {
      const payload = await sessionService.fetchSessions(rootID, {
        beforeTime: oldest,
        nodeId: getNodeIdForRoot(rootID),
      });
      const next = [...payload.items, ...payload.pinnedItems]
        .map((item) => toSessionItem(rootID, item))
        .filter((item): item is SessionItem => !!item);
      setHasMoreSessions(payload.totalCount > payload.items.length);
      setSessions((prev) =>
        applyPinnedSnapshotToSessions(mergeSessionItems(prev, next), rootID, payload.pinnedKeys),
      );
    } finally {
      setLoadingOlderSessions(false);
    }
  }, [getNodeIdForRoot, loadingOlderSessions]);

  useEffect(() => {
    if (didInitRef.current) {
      return;
    }
    didInitRef.current = true;
    let cancelled = false;
    let settled = false;
    void (async () => {
      try {
        const bootstrap = await bootstrapService.start();
        if (bootstrap.phase !== "ready") {
          didInitRef.current = false;
          return;
        }
        const dirs = await loadManagedRootPayloads();
        if (!dirs) {
          return;
        }
        if (cancelled || !dirs.length) {
          return;
        }
        const nextDirs = dirs as ManagedRootPayload[];
        const ids = nextDirs.map((d) => d.id);
        const indexed = indexManagedRoots(nextDirs);
        managedRootByKeyRef.current = indexed.byKey;
        managedRootByIdRef.current = indexed.byId;
        setRootNodeMap(managedRootByIdRef.current as Record<string, any>);
        managedRootIdsRef.current = new Set(ids);
        setManagedRootIds(ids);
        setRootEntries(mapManagedRootsToEntries(nextDirs));
        const urlState = readURLState();
        const lastRoot = loadLastRootId();
        const lastNode = loadLastRootNodeId();
        const urlNode = String(urlState.node || "");
        const nodeHint = urlNode || lastNode || "";
        const preferredRoot =
          urlState.root && ids.includes(urlState.root)
            ? urlState.root
            : lastRoot && ids.includes(lastRoot)
              ? lastRoot
              : ids[0];
        const preferredDir =
          nextDirs.find(
            (d) => d.id === preferredRoot && (!nodeHint || (d as any)._nodeId === nodeHint),
          ) ||
          nextDirs.find((d) => d.id === preferredRoot);
        setCurrentRootId(preferredRoot);
        selectRootNode(
          preferredRoot,
          urlNode || (preferredDir as any)?._nodeId || undefined,
        );
        // 初始化竞态兜底：refs 就绪后补一次多项目会话加载，覆盖首屏早期空/回退 _nodeId 批次
        if (multiProjectSessionsEnabled) {
          void loadMultiProjectSessionGroups();
        }
        setPluginQuery(urlState.pluginQuery);
        // URL 里的 view 是主面板模式的唯一真相源；没有才回落 localStorage（useState 初值已读）。
        if (urlState.view) {
          switchMainView(urlState.view);
        }
        if (urlState.session) {
          if (cancelled) return;
          await handleSelectSessionRef.current?.(
            {
              key: urlState.session,
              session_key: urlState.session,
              root_id: preferredRoot,
            },
            // 深链恢复：URL 明确写了非 chat 的面板时，别被「有 session」拉回对话态
            {
              preserveMainView:
                !!urlState.view && urlState.view !== "chat",
            },
          );
        } else if (urlState.file) {
          await ensurePluginsLoaded(preferredRoot);
          if (cancelled) return;
          actionHandlersRef.current.open({
            path: urlState.file,
            root: preferredRoot,
            cursor: urlState.cursor,
            preservePluginQuery: true,
          });
        } else {
          if (cancelled) return;
          await showBoundSessionOrRootDir(preferredRoot, {
            pluginQuery: urlState.pluginQuery,
            loadSessionsOnRestore: true,
          });
        }
      } catch (err) {
        if (cancelled) {
          return;
        }
        reportError(
          "app.init_failed",
          String((err as Error)?.message || err || t("app.initFailed")),
        );
      } finally {
        settled = true;
      }
    })();
    return () => {
      cancelled = true;
      if (!settled) {
        didInitRef.current = false;
      }
    };
  }, [
    ensurePluginsLoaded,
    loadManagedRootPayloads,
    loadMultiProjectSessionGroups,
    loadSessionsForRoot,
    refreshTreeDir,
    showBoundSessionOrRootDir,
    bootstrapState.phase,
  ]);

  useEffect(() => {
    return bootstrapService.subscribe((state) => {
      setBootstrapState(state);
      setE2eeState(state.e2ee);
    });
  }, []);

  useEffect(() => {
    document.title = APP_DOCUMENT_TITLE;
  }, []);

  useEffect(() => {
    return e2eeService.subscribe((state) => {
      setE2eeState(state);
    });
  }, []);

  useEffect(() => {
    void syncNativeReplyPollerE2EE().catch((err) => {
      console.warn("[ReplyPoller] Failed to sync E2EE state:", err);
    });
  }, [e2eeState.nodeId, e2eeState.required, e2eeState.unlocked]);

  useEffect(() => {
    if (!e2eeState.required) {
      setE2eeSecretInput("");
      setE2eePromptError("");
    }
  }, [e2eeState.required, e2eeState.secretPresent]);

  useEffect(() => {
    if (bootstrapState.phase !== "ready") {
      return;
    }
    setAgentsVersion((v) => v + 1);
    void loadTaskTemplates();
    void loadKanbanTasks(currentRootIdRef.current);
    if (multiProjectSessionsEnabled) {
      void loadMultiProjectSessionGroups();
    }
  }, [
    bootstrapState.phase,
    loadKanbanTasks,
    loadMultiProjectSessionGroups,
    loadTaskTemplates,
    multiProjectSessionsEnabled,
  ]);

  const describeE2EEPromptError = useCallback((err: unknown) => {
    const code = err instanceof Error ? String(err.message || "").trim() : "";
    switch (code) {
      case "e2ee_proof_invalid":
        return t("e2ee.invalidProof");
      case "e2ee_secure_context_required":
      case "e2ee_webcrypto_unavailable":
        return t("e2ee.secureContextRequired");
      case "e2ee_secret_missing":
        return t("e2ee.secretMissing");
      case "e2ee_open_invalid_response":
        return t("e2ee.invalidResponse");
      default:
        if (code.startsWith("e2ee_open_failed_")) {
          return t("e2ee.openFailed");
        }
        return t("e2ee.failed");
    }
  }, [t]);

  const submitE2EESecret = useCallback(async () => {
    const trimmed = e2eeSecretInput.trim();
    if (!trimmed) {
      setE2eePromptError(t("e2ee.codeRequired"));
      return;
    }
    setE2eePromptBusy(true);
    setE2eePromptError("");
    try {
      await bootstrapService.submitPairingSecret(trimmed);
      didInitRef.current = false;
      setE2eeSecretInput("");
    } catch (err) {
      setE2eePromptError(describeE2EEPromptError(err));
    } finally {
      setE2eePromptBusy(false);
    }
  }, [describeE2EEPromptError, e2eeSecretInput, t]); // ponytail: e2ee removed, pairing dead code kept for type compat

  useEffect(() => {
    function handlePopState() {
      const state = readURLState();
      if (state.root) {
        setCurrentRootId(state.root);
        selectRootNode(state.root, state.node || undefined);
      }
      setPluginQuery(state.pluginQuery);
      if (state.view) {
        // 后退/前进时按钮高亮与面板都从 URL 的 view 恢复，保持同源
        switchMainView(state.view);
      }
      if (!state.root) {
        return;
      }
      if (state.session) {
        const currentSessionKey =
          selectedSessionRef.current?.key ||
          selectedSessionRef.current?.session_key ||
          "";
        const currentSessionRoot =
          (selectedSessionRef.current?.root_id as string | undefined) ||
          currentRootIdRef.current ||
          "";
        if (
          state.session !== currentSessionKey ||
          state.root !== currentSessionRoot
        ) {
          void handleSelectSessionRef.current?.(
            {
              key: state.session,
              session_key: state.session,
              root_id: state.root,
            },
            { preserveMainView: !!state.view && state.view !== "chat" },
          );
        }
        return;
      }
      if (state.file) {
        const currentPath = fileRef.current?.path || "";
        const currentRoot = currentRootIdRef.current || "";
        const currentCursor = fileCursorRef.current;
        if (
          state.file !== currentPath ||
          state.root !== currentRoot ||
          state.cursor !== currentCursor
        ) {
          actionHandlers.open({
            path: state.file,
            root: state.root,
            cursor: state.cursor,
            preservePluginQuery: true,
          });
        }
        return;
      }
      void (async () => {
        await showBoundSessionOrRootDir(state.root, {
          pluginQuery: state.pluginQuery,
          loadSessionsOnRestore: true,
        });
      })();
    }

    window.addEventListener("popstate", handlePopState);
    return () => window.removeEventListener("popstate", handlePopState);
  }, [actionHandlers, loadSessionsForRoot, refreshTreeDir, showBoundSessionOrRootDir]);

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
  // 「会话就在主区」必须同时满足：主面板确实是对话态。否则在文件态下会话被选中却看不见，
  // 蓝环会被误判成「已在主区」而隐藏，文件态的悬浮框就再也打不开了。
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

  useEffect(() => {
    if (!file || pluginBypass || !matchedPlugin) return;
    if (inferReadModeFromPlugin(matchedPlugin) !== "full") return;
    if (!file.truncated) return;
    const root = file.root || currentRootId;
    if (!root) return;
    const upgradeKey = `${root}:${file.path}:${matchedPlugin.name}:${JSON.stringify(pluginQuery)}`;
    if (fullUpgradeAttemptRef.current === upgradeKey) return;
    fullUpgradeAttemptRef.current = upgradeKey;
    void actionHandlers.open({
      path: file.path,
      root,
      cursor: fileCursorRef.current || 0,
      readMode: "full",
      preservePluginQuery: true,
    });
  }, [
    file,
    pluginBypass,
    matchedPlugin,
    currentRootId,
    pluginQuery,
    actionHandlers,
  ]);

  const pluginRender = useMemo(() => {
    if (!file || pluginBypass || !matchedPlugin) return null;
    const input = toPluginInput(file, pluginQuery);
    try {
      const output = pluginManagerRef.current.run(matchedPlugin, input);
      return { plugin: matchedPlugin, output, error: "" };
    } catch (err: any) {
      return {
        plugin: matchedPlugin,
        output: null,
        error: String(err?.message || err || "plugin process failed"),
      };
    }
  }, [file, pluginBypass, matchedPlugin, pluginQuery]);

  useEffect(() => {
    if (!file || pluginBypass || !pluginRender?.output) {
      return;
    }
    const handleSelectionChange = () => {
      const root = pluginContentRef.current;
      const selection = window.getSelection();
      if (!root || !selection || selection.rangeCount === 0 || selection.isCollapsed) {
        handleViewerSelectionChange(null);
        return;
      }
      const range = selection.getRangeAt(0);
      const commonAncestor = range.commonAncestorContainer;
      if (!root.contains(commonAncestor)) {
        handleViewerSelectionChange(null);
        return;
      }
      const text = selection.toString();
      if (!text.trim()) {
        handleViewerSelectionChange(null);
        return;
      }
      handleViewerSelectionChange({
        filePath: file.path,
        text,
      });
    };
    document.addEventListener("selectionchange", handleSelectionChange);
    return () => {
      document.removeEventListener("selectionchange", handleSelectionChange);
      handleViewerSelectionChange(null);
    };
  }, [file, pluginBypass, pluginRender?.output, handleViewerSelectionChange]);

  useEffect(() => {
    if (!file || pluginBypass || !pluginRender?.output) {
      lastPluginChapterRef.current = "";
      return;
    }
    const chapterKey = `${file.path}:${pluginQuery.chapter || "1"}`;
    if (!lastPluginChapterRef.current) {
      lastPluginChapterRef.current = chapterKey;
      return;
    }
    if (lastPluginChapterRef.current === chapterKey) {
      return;
    }
    lastPluginChapterRef.current = chapterKey;
    requestAnimationFrame(() => {
      pluginContentRef.current?.scrollTo({ top: 0, behavior: "auto" });
    });
  }, [file, pluginBypass, pluginRender?.output, pluginQuery.chapter]);

  const pluginRendererKey = `${currentRootId || ""}:${file?.path || ""}:${fileCursorRef.current}:${JSON.stringify(pluginQuery)}`;
  const pluginThemeVars = useMemo(() => {
    const theme = pluginRender?.plugin?.theme;
    if (!theme) return null;
    return {
      "--vp-overlay-bg": theme.overlayBg,
      "--vp-surface-bg": theme.surfaceBg,
      "--vp-surface-bg-elevated": theme.surfaceBgElevated,
      "--vp-text": theme.text,
      "--vp-text-muted": theme.textMuted,
      "--vp-border": theme.border,
      "--vp-primary": theme.primary,
      "--vp-primary-text": theme.primaryText,
      "--vp-radius": theme.radius,
      "--vp-shadow": theme.shadow,
      "--vp-focus-ring": theme.focusRing,
      "--vp-danger": theme.danger,
      "--vp-warning": theme.warning,
      "--vp-success": theme.success,
    } as React.CSSProperties;
  }, [pluginRender]);

	  const selectedSessionSnapshot = useMemo(
	    () => {
	      if (!selectedSession) {
	        return null;
      }
      return getSessionSnapshot(
        selectedSession.root_id || currentRootId,
        selectedSession,
      );
	    },
	    [selectedSession, selectedSessionLoading, currentRootId, getSessionSnapshot],
	  );
	  const sessionByKey = useMemo(() => sessions.reduce<Record<string, SessionItem>>((acc, session) => {
	    const key = session.key || session.session_key || "";
	    if (key) acc[key] = session;
	    return acc;
	  }, {}), [sessions]);

	  const selectedKanbanTask = useMemo(
	    () => kanbanTasks.find((task) => task.id === selectedKanbanTaskId) || null,
	    [kanbanTasks, selectedKanbanTaskId],
	  );
	  const selectedKanbanTaskSessionKey = useMemo(() => {
	    if (!selectedKanbanTask) return "";
	    const keys = taskSessionKeysById[selectedKanbanTask.id] || [];
	    return keys[0] || selectedKanbanTask.main_session_key || "";
	  }, [selectedKanbanTask, taskSessionKeysById]);
	  const selectedKanbanTaskSessionSnapshot = useMemo(() => {
	    if (!selectedKanbanTask || !selectedKanbanTaskSessionKey) return null;
	    const root = selectedKanbanTask.root_id || currentRootId || "";
	    const session = sessionByKey[selectedKanbanTaskSessionKey] || {
	      key: selectedKanbanTaskSessionKey,
	      session_key: selectedKanbanTaskSessionKey,
	      root_id: root,
	      task_id: selectedKanbanTask.id,
	    };
	    return getSessionSnapshot(root, session as any);
	  }, [currentRootId, getSessionSnapshot, selectedKanbanTask, selectedKanbanTaskSessionKey, sessionByKey]);

	  useEffect(() => {
    if (selectedSessionSnapshot) {
      lastMainSessionSnapshotRef.current = selectedSessionSnapshot as Session;
    }
  }, [selectedSessionSnapshot]);

  useEffect(() => {
    const sessionKey =
      selectedSession?.key || selectedSession?.session_key || "";
    const rootID =
      (selectedSession?.root_id as string | undefined) || currentRootId || "";
    if (!rootID || !sessionKey || sessionKey.startsWith("pending-")) {
      return;
    }
    if (selectedSessionLoading) {
      return;
    }
    const cacheKey = rootSessionKey(rootID, sessionKey);
    const isStale = isSessionStale(rootID, sessionKey);
    const cached = sessionCacheRef.current[cacheKey];
    if (!isStale && loadedSessionRef.current[cacheKey]) {
      return;
    }
    if (!isStale && hasSessionExchanges(cached)) {
      return;
    }
    if (!isStale && hasSessionExchanges(selectedSessionSnapshot as Session | null)) {
      return;
    }
    if (loadingSessionRef.current[cacheKey]) {
      return;
    }
    void restoreActiveSession(rootID, sessionKey).then((restored) => {
      if (!restored) {
        return;
      }
      loadedSessionRef.current[cacheKey] = true;
      clearSessionStale(rootID, sessionKey);
      setSelectedSession((prev) => {
        const prevKey = prev?.key || prev?.session_key;
        const prevRoot =
          (prev?.root_id as string | undefined) || currentRootIdRef.current;
        if (!prev || prevKey !== sessionKey || prevRoot !== rootID) {
          return prev;
        }
        return toSessionItem(rootID, {
          ...(prev as any),
          ...(restored as any),
          key: sessionKey,
          session_key: sessionKey,
          root_id: rootID,
        });
      });
      if (boundSessionByRootRef.current[scopedRootKey(rootID)] === sessionKey) {
        setDrawerSessionForRoot(rootID, restored);
      }
    });
  }, [
    selectedSession,
    selectedSessionLoading,
    selectedSessionSnapshot,
    currentRootId,
    rootSessionKey,
    bumpCacheVersion,
    clearSessionStale,
    isSessionStale,
    resolvePendingForSession,
    restoreActiveSession,
    setDrawerSessionForRoot,
  ]);

  const handleSelectedSessionFileClick = useCallback(
    (target: string | RelatedFileClickTarget) => {
      setProjectTreeTabRequest((prev) => ({
        tab: "related",
        nonce: (prev?.nonce || 0) + 1,
      }));
      const root =
        (selectedSessionRef.current?.root_id as string | undefined) ||
        currentRootIdRef.current;
      if (!root) return;
      setExpanded((prev) =>
        Array.from(new Set([...prev, scopeKey(getNodeIdForRoot(root) ?? "", root)])),
      );
      const file =
        typeof target === "string" ? { path: target } : target;
      void openRelatedFileDiff(root, file);
    },
    [openRelatedFileDiff],
  );

  const drawerSessionSnapshot = useMemo(
    () =>
      currentSession ? getSessionSnapshot(currentRootId, currentSession) : null,
    [currentSession, currentRootId, getSessionSnapshot],
  );

  const rootSessionIndicators = useMemo(() => {
    const next: Record<string, { bound?: boolean; pending?: boolean }> = {};
    for (const [scopedRid, dir] of Object.entries(
      managedRootByKeyRef.current as Record<string, any>,
    )) {
      const root = String(dir?.id || "");
      if (!root) continue;
      const nid = scopedRid.includes("::")
        ? scopedRid.slice(0, scopedRid.lastIndexOf("::"))
        : "";
      const boundKey = String(boundSessionByRootRef.current[scopedRid] || "").trim();
      const hasBound = !!boundKey && !boundKey.startsWith("pending-");
      const pendingPrefix = scopeSessionKey(nid, root, "");
      const hasPendingSession = Object.entries(multiProjectPendingByKey).some(
        ([key, pending]) => pending && key.startsWith(pendingPrefix),
      );
      if (!hasBound && !hasPendingSession) {
        continue;
      }
      const drawer = drawerSessionByRootRef.current[scopedRid] as
        | (Session & { pending?: boolean })
        | null
        | undefined;
      const selected =
        ((selectedSession?.root_id as string | undefined) || currentRootId) ===
          root &&
        (selectedSession?.key || selectedSession?.session_key) === boundKey
          ? ((selectedSession as any) as { pending?: boolean })
          : null;
      const pending =
        hasPendingSession ||
        (drawer?.key === boundKey && !!drawer?.pending) ||
        !!selected?.pending;
      next[scopedRid] = { bound: hasBound || hasPendingSession, pending };
    }
    return next;
  }, [
    managedRootIds,
    selectedSession,
    currentSession,
    currentRootId,
    activeBoundSessionKey,
    cacheVersion,
    multiProjectPendingByKey,
    rootSessionKey,
  ]);

  const handleDrawerSessionFileClick = useCallback(
    (target: string | RelatedFileClickTarget) => {
      const root = currentRootIdRef.current;
      if (!root) return;
      const file =
        typeof target === "string" ? { path: target } : target;
      void openRelatedFileDiff(root, file);
    },
    [openRelatedFileDiff],
  );

  const handleRemoveSessionRelatedFile = useCallback(
    async (
      rootID: string | null | undefined,
      sessionKey: string | undefined,
      path: string,
      head = "",
      repoPath = "",
      repoKind = "",
    ) => {
      const resolvedRoot = rootID || currentRootIdRef.current;
      const resolvedKey = sessionKey || "";
      if (!resolvedRoot || !resolvedKey || !path) return;
      const removed = await sessionService.removeSessionRelatedFile(
        resolvedRoot,
        resolvedKey,
        path,
        head,
        repoPath,
        repoKind,
      );
      if (!removed) return;
      const relatedFiles = await sessionService.getSessionRelatedFiles(
        resolvedRoot,
        resolvedKey,
        getNodeIdForRoot(resolvedRoot),
      );
      await setCachedSessionRelatedFiles(resolvedRoot, resolvedKey, relatedFiles, getNodeIdForRoot(resolvedRoot));
      updateSessionRelatedFilesForKey(resolvedRoot, resolvedKey, relatedFiles);
      if (selectedKanbanTaskId) {
        const removedKey = [repoKind || "", repoPath || "", head || "", path || ""].join("\0");
        setTaskRelatedFilesById((prev) => {
          const current = prev[selectedKanbanTaskId] || [];
          return {
            ...prev,
            [selectedKanbanTaskId]: current.filter((file) =>
              [file.repo_kind || "", file.repo_path || "", file.head || "", file.path || ""].join("\0") !== removedKey,
            ),
          };
        });
      }
    },
    [selectedKanbanTaskId, updateSessionRelatedFilesForKey],
  );

  const handleAskUserAnswer = useCallback(
    async (input: {
      rootId: string;
      sessionKey: string;
      agent?: string;
      toolUseId: string;
      answers: Record<string, string>;
    }) => {
      await sessionService.answerQuestion(
        input.rootId,
        input.sessionKey,
        input.agent,
        input.toolUseId,
        input.answers,
      );
    },
    [],
  );

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
  const slashCommandResultForSession = (
    rootID: string | null | undefined,
    session: { key?: string; session_key?: string } | null | undefined,
  ) => {
    const sessionKey = session?.key || session?.session_key || "";
    const resolvedRoot = rootID || "";
    if (!resolvedRoot) {
      return null;
    }
    if (!sessionKey) {
      let best: SlashCommandResult | null = null;
      for (const value of Object.values(slashCommandResults)) {
        if (value.rootId !== resolvedRoot || !value.sessionKey.startsWith("transient-")) {
          continue;
        }
        if (!best || (value.createdAt || 0) > (best.createdAt || 0)) {
          best = value;
        }
      }
      return best;
    }
    return slashCommandResults[rootSessionKey(resolvedRoot, sessionKey)] || null;
  };
  const renderRootSlashCommandResult = (result: SlashCommandResult | null) => {
    if (!result || !result.sessionKey.startsWith("transient-")) {
      return null;
    }
    const commandLabel = `/${result.command || "status"}`;
    const loginNotice = result.loginNotice;
    const isLogin = (result.command || "") === "login";
    const fallback =
      result.status === "running"
        ? isLogin
          ? t("session.loginWaiting")
          : t("session.statusFetching")
        : "";
    const content = result.error || loginNotice?.error || result.content || fallback;
    const loginCodeCopyKey = loginNotice?.loginId
      ? `login-code:${loginNotice.loginId}`
      : `login-code:${result.sessionKey}`;
    const loginCodeCopied = !!copiedSlashCommandKeys[loginCodeCopyKey];
    return (
      <div
        style={{
          width: "100%",
          boxSizing: "border-box",
          border: "1px solid rgba(148,163,184,0.36)",
          background: "rgba(148,163,184,0.10)",
          borderRadius: "8px",
          padding: "10px 12px",
          color: "var(--text-primary)",
        }}
      >
        <div
          style={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            gap: "8px",
            marginBottom: content || loginNotice?.userCode ? "6px" : 0,
            fontSize: "11px",
            lineHeight: 1.4,
            color: "var(--text-secondary)",
          }}
        >
          <span style={{ fontFamily: "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace" }}>
            {commandLabel}
          </span>
          <span>{result.status === "running" ? t("session.statusRunning") : result.status === "failed" ? t("session.statusFailed") : t("session.statusComplete")}</span>
        </div>
        {isLogin && loginNotice?.userCode ? (
          <div style={{ display: "flex", flexDirection: "column", gap: "8px", fontSize: "13px", lineHeight: 1.5 }}>
            {loginNotice.verificationUrl ? (
              <a
                href={loginNotice.verificationUrl}
                target="_blank"
                rel="noreferrer"
                style={{ color: "var(--accent)", overflowWrap: "anywhere" }}
              >
                {loginNotice.verificationUrl}
              </a>
            ) : null}
            <div style={{ display: "flex", alignItems: "center", gap: "8px", flexWrap: "wrap" }}>
              <span
                style={{
                  fontFamily: "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace",
                  fontSize: "20px",
                  letterSpacing: "0",
                }}
              >
                {loginNotice.userCode}
              </span>
              <button
                type="button"
                onClick={() => {
                  const userCode = loginNotice.userCode || "";
                  if (!userCode) {
                    reportError("clipboard.write_failed", t("session.emptyCodeCannotCopy"));
                    return;
                  }
                  void copyText(userCode)
                    .then(() => {
                      setCopiedSlashCommandKeys((prev) => ({
                        ...prev,
                        [loginCodeCopyKey]: true,
                      }));
                      if (slashCopyResetTimersRef.current[loginCodeCopyKey]) {
                        window.clearTimeout(
                          slashCopyResetTimersRef.current[loginCodeCopyKey],
                        );
                      }
                      slashCopyResetTimersRef.current[loginCodeCopyKey] =
                        window.setTimeout(() => {
                          setCopiedSlashCommandKeys((prev) => {
                            const next = { ...prev };
                            delete next[loginCodeCopyKey];
                            return next;
                          });
                          delete slashCopyResetTimersRef.current[
                            loginCodeCopyKey
                          ];
                        }, 1000);
                    })
                    .catch((err) => {
                      reportError(
                        "clipboard.write_failed",
                        String((err as Error)?.message || t("session.copyFailed")),
                      );
                    });
                }}
                style={{
                  display: "inline-flex",
                  alignItems: "center",
                  justifyContent: "center",
                  width: "22px",
                  height: "22px",
                  border: "none",
                  background: "transparent",
                  color: "var(--text-secondary)",
                  borderRadius: "6px",
                  padding: 0,
                  cursor: "pointer",
                }}
                aria-label={loginCodeCopied ? t("session.codeCopied") : t("session.copyCode")}
                title={loginCodeCopied ? t("session.copied") : t("session.copyCode")}
              >
                {loginCodeCopied ? (
                  <span
                    aria-hidden="true"
                    style={{ fontSize: "13px", fontWeight: 800, lineHeight: 1 }}
                  >
                    ✓
                  </span>
                ) : (
                  <svg
                    xmlns="http://www.w3.org/2000/svg"
                    width="14"
                    height="14"
                    viewBox="0 0 24 24"
                    aria-hidden="true"
                  >
                    <path
                      fill="currentColor"
                      d="M20 2H10c-1.1 0-2 .9-2 2v10c0 1.1.9 2 2 2h10c1.1 0 2-.9 2-2V4c0-1.1-.9-2-2-2m0 12H10V4h10z"
                    />
                    <path
                      fill="currentColor"
                      d="M14 20H4V10h2V8H4c-1.1 0-2 .9-2 2v10c0 1.1.9 2 2 2h10c1.1 0 2-.9 2-2v-2h-2z"
                    />
                  </svg>
                )}
              </button>
            </div>
            {result.status === "complete" ? (
              <div style={{ color: "var(--text-secondary)" }}>
                {t("session.loginComplete")}{loginNotice.planType ? ` · ${loginNotice.planType}` : ""}
              </div>
            ) : null}
          </div>
        ) : content ? (
          <div
            style={{
              fontSize: "13px",
              lineHeight: "1.6",
              whiteSpace: "pre-wrap",
              overflowWrap: "anywhere",
            }}
          >
            {content}
          </div>
        ) : null}
      </div>
    );
  };
  const currentRootSlashCommandResult = slashCommandResultForSession(currentRootId, null);
  // The memory badge is always rendered above the composer, so floating
  // session controls must always clear that overlay row.
  const sessionViewerComposerOverlayInset = 20;
  const sessionView = (
    <SessionViewer
      session={selectedSessionSnapshot}
      agents={availableAgents}
      slashCommandResult={slashCommandResultForSession(
        selectedSession?.root_id || currentRootId,
        selectedSessionSnapshot,
      )}
      targetSeq={selectedSession?.search_seq}
      targetSeqRequestKey={selectedSession?.search_target_id}
      composerOverlayInset={sessionViewerComposerOverlayInset}
      loading={selectedSessionLoading}
      rootId={selectedSession?.root_id || currentRootId}
      rootDisplayName={getRootDisplayName(selectedSession?.root_id || currentRootId)}
      rootColor={getDisplayNodeColor(String(selectedSession?.root_id || currentRootId || ""))}
      rootPath={
        managedRootByIdRef.current[
          selectedSession?.root_id || currentRootId || ""
        ]?.root_path || null
      }
      gitFileStatsByPath={gitFileStatsByPath}
      onFileClick={handleSelectedSessionFileClick}
      onRootClick={(root) => {
        void actionHandlers.open_dir({
          path: root,
          root,
          isRoot: true,
          forceDirectory: true,
          suppressTreeExpand: true,
        });
      }}
      onRemoveRelatedFile={(path, head, repoPath, repoKind) =>
        void handleRemoveSessionRelatedFile(
          selectedSession?.root_id || currentRootId,
          selectedSessionSnapshot?.key || selectedSessionSnapshot?.session_key,
          path,
          head,
          repoPath,
          repoKind,
        )
      }
      onAskUserAnswer={handleAskUserAnswer}
      onEditUserMessage={handleEditUserMessage}
      onForkAgentMessage={(seq) =>
        void handleForkAgentMessage(
          selectedSession?.root_id || currentRootId,
          selectedSessionSnapshot?.key || selectedSessionSnapshot?.session_key,
          seq,
        )
      }
    />
  );


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
  const relatedWorktree = selectedKanbanTask?.worktree_path
    ? {
        root_id: selectedKanbanTask.root_id,
        path: selectedKanbanTask.worktree_path,
      }
    : relatedSessionSnapshot?.related_worktree || null;

  const refreshProjectTreeRelatedFiles = useCallback(async () => {
    try {
      if (selectedKanbanTask) {
        const root = selectedKanbanTask.root_id || relatedSessionRootId || currentRootId || "";
        const taskId = String(selectedKanbanTask.id || "");
        const sessionKeys = Array.from(new Set(
          [
            ...(taskSessionKeysByIdRef.current[taskId] || []),
            selectedKanbanTask.main_session_key,
          ]
            .map((key) => String(key || "").trim())
            .filter(Boolean),
        ));
        if (root && taskId && sessionKeys.length > 0) {
          await refreshTaskRelatedFiles(root, taskId, sessionKeys);
        }
        return;
      }
      const root = relatedSessionRootId || currentRootId || "";
      const sessionKey = String(relatedSessionKey || "").trim();
      if (!root || !sessionKey) {
        return;
      }
      const relatedFiles = await sessionService.getSessionRelatedFiles(root, sessionKey);
      await setCachedSessionRelatedFiles(root, sessionKey, relatedFiles);
      updateSessionRelatedFilesForKey(root, sessionKey, relatedFiles);
    } catch (error) {
      console.error("[session.related_files] manual refresh failed", { error });
    }
  }, [
    currentRootId,
    refreshTaskRelatedFiles,
    relatedSessionKey,
    relatedSessionRootId,
    selectedKanbanTask,
    updateSessionRelatedFilesForKey,
  ]);

  // 「管理节点」面板的刷新：先拉节点注册表（拿别的设备改过的节点），
  // 再让 refreshManagedRoots 逐节点重拉 dirs 并触发多项目会话/回复态重载。
  // 不要再额外调 loadManagedRootPayloads({force:true})——refreshManagedRoots 内部已经会调它。
  const handleNodeManagerRefresh = useCallback(async () => {
    await syncNodesFromServer().catch(() => {});
    await refreshManagedRoots();
  }, [refreshManagedRoots]);

  const handleProjectTreeRefresh = useCallback(async (tab: ProjectTreeTab) => {
    const root = currentRootIdRef.current;
    if (!root) {
      return;
    }
    switch (tab) {
      case "files": {
        // 强制拉取真实状态：当前目录 + 侧边栏已加载（展开/访问过）的所有目录全部重拉，
        // 而非仅刷新选中目录——展开的子目录若不重拉，刷新后仍显示旧条目。
        const selDir = selectedDirRef.current === root ? "." : (selectedDirRef.current || ".");
        const treeBase = scopeKey(getNodeIdForRoot(root) ?? "", root);
        const targets = new Set<string>([selDir]);
        for (const key of Object.keys(entriesByPathRef.current)) {
          if (key === treeBase) {
            targets.add(".");
          } else if (key.startsWith(`${treeBase}:`)) {
            targets.add(key.slice(treeBase.length + 1));
          }
        }
        await Promise.all(
          [...targets].map((dir) => refreshTreeDir(root, dir, dir === selDir)),
        );
        if (fileRef.current?.path) {
          // 主视图正打开文件时，刷新同时强制重读该文件内容（WS 断开时唯一的重读入口）
          void refreshCurrentFileContent(root, fileRef.current.path);
        }
        return;
      }
      case "git":
        await Promise.all([
          refreshGitStatus(root),
          refreshGitHistory(root, { waitForIncremental: true }),
        ]);
        return;
      case "worktrees":
        await refreshProjectTreeWorktrees(root);
        return;
      case "related":
        await Promise.all([
          refreshProjectTreeRelatedFiles(),
          refreshGitStatus(root),
        ]);
    }
  }, [
    getNodeIdForRoot,
    refreshCurrentFileContent,
    refreshGitHistory,
    refreshGitStatus,
    refreshProjectTreeRelatedFiles,
    refreshProjectTreeWorktrees,
    refreshTreeDir,
  ]);

  useEffect(() => {
    const rootID = String(relatedWorktree?.root_id || "");
    const worktreePath = String(relatedWorktree?.path || "");
    if (projectTreeTab !== "worktrees" || !rootID || !worktreePath) {
      return;
    }
    void expandProjectTreeWorktree(rootID, worktreePath);
  }, [
    expandProjectTreeWorktree,
    projectTreeTab,
    relatedWorktree?.path,
    relatedWorktree?.root_id,
  ]);

  const renderRootWorktreeContent = (root: string): React.ReactNode => (
    <RootWorktreeContentView
      root={root}
      currentRootId={currentRootId}
      scopedRootKey={scopedRootKey}
      managedRootByIdRef={managedRootByIdRef}
      relatedWorktree={relatedWorktree}
      worktrees={{
        worktreeItemsByRoot,
        worktreeLoadingByRoot,
        worktreeErrorByRoot,
        expandedWorktreeByRoot,
        worktreeStatusByPath,
        worktreeStatusLoadingByPath,
        toggleProjectTreeWorktree,
      }}
      findManagedRootByPath={findManagedRootByPath}
      openGitDiff={openGitDiff}
      actionHandlers={actionHandlers}
    />
  );
  const selectedSessionRelatedFiles = useMemo(() => {
    const rawRelated = selectedKanbanTask
      ? taskRelatedFilesById[selectedKanbanTask.id] || []
      : relatedSessionSnapshot?.related_files || (relatedSessionSnapshot as any)?.outputs || [];
    return (Array.isArray(rawRelated) ? rawRelated : [])
      .map((file: RelatedFile | string | { path?: unknown; name?: unknown; head?: unknown; repo_path?: unknown; repo_name?: unknown; repo_kind?: unknown; root_id?: unknown }) => {
        const path = typeof file === "string"
          ? file
          : typeof file?.path === "string"
            ? file.path
            : "";
        const rawName =
          typeof file !== "string" ? (file as { name?: unknown }).name : "";
        const name = typeof rawName === "string"
          ? rawName
          : path.split("/").pop() || path;
        const head = typeof file !== "string" && typeof file?.head === "string"
          ? file.head
          : "";
        const repoPath = typeof file !== "string" && typeof file?.repo_path === "string"
          ? file.repo_path
          : "";
        const repoName = typeof file !== "string" && typeof file?.repo_name === "string"
          ? file.repo_name
          : repoPath.split(/[\\/]/).filter(Boolean).pop() || "";
        const repoKind = typeof file !== "string" && typeof file?.repo_kind === "string"
          ? file.repo_kind
          : "";
        const rootID = typeof file !== "string" && typeof file?.root_id === "string"
          ? file.root_id
          : "";
        return { path, name, head, repo_path: repoPath, repo_name: repoName, repo_kind: repoKind, root_id: rootID };
      })
      .filter((file) => file.path);
  }, [relatedSessionSnapshot, selectedKanbanTask, taskRelatedFilesById]);
  const selectedSessionRelatedFileGroups = useMemo(
    () => {
      const currentRootPath = normalizePath(
        managedRootByIdRef.current[relatedSessionRootId || currentRootId || ""]?.root_path || "",
      );
      const repoGroups = selectedSessionRelatedFiles.reduce<
        Array<{
          key: string;
          repoPath: string;
          repoName: string;
          repoKind: string;
          headGroups: Array<{ key: string; head: string; files: typeof selectedSessionRelatedFiles }>;
        }>
      >((groups, file) => {
        const head = file.head || "";
        const rawRepoPath = file.repo_path || "";
        const isCurrentRepoRecord =
          !rawRepoPath ||
          file.repo_name === t("session.currentProject") ||
          (currentRootPath && normalizePath(rawRepoPath) === currentRootPath);
        const repoPath = isCurrentRepoRecord ? "" : rawRepoPath;
        const rawRepoKind = file.repo_kind || "";
        const repoKind = isCurrentRepoRecord && rawRepoKind !== "plain" ? "" : rawRepoKind;
        const repoKey = `${repoKind}\0${repoPath}`;
        let repoGroup = groups.find((group) => group.key === repoKey);
        if (!repoGroup) {
          repoGroup = {
            key: repoKey,
            repoPath,
            repoName: isCurrentRepoRecord
              ? t("session.currentProject")
              : file.repo_name || repoPath.split(/[\\/]/).filter(Boolean).pop() || t("session.currentProject"),
            repoKind,
            headGroups: [],
          };
          groups.push(repoGroup);
        }
        const headKey = `${repoKey}\0${head}`;
        const existing = repoGroup.headGroups.find((group) => group.key === headKey);
        if (existing) {
          existing.files.push(file);
        } else {
          repoGroup.headGroups.push({
            key: headKey,
            head,
            files: [file],
          });
        }
        return groups;
      }, []);
      return repoGroups.flatMap((repoGroup) =>
        repoGroup.headGroups.map((headGroup) => ({
          key: headGroup.key,
          head: headGroup.head,
          repoPath: repoGroup.repoPath,
          repoName: repoGroup.repoName,
          repoKind: repoGroup.repoKind,
          files: headGroup.files,
        })),
      );
    },
    [currentRootId, relatedSessionRootId, selectedSessionRelatedFiles],
  );
  const gitStatsRefreshKey = useMemo(
    () =>
      Object.entries(gitFileStatsByPath)
        .sort(([left], [right]) => left.localeCompare(right))
        .map(([path, stats]) => `${path}:${stats.status}:${stats.additions}:${stats.deletions}`)
        .join("|"),
    [gitFileStatsByPath],
  );
  const selectedRelatedFileStatsByKey = useRelatedFileStats(
    relatedSessionRootId || currentRootId,
    selectedSessionRelatedFiles,
    gitStatsRefreshKey,
    relatedSessionNodeId,
  );
  const renderRootRelatedContent = (root: string): React.ReactNode => (
    <RootRelatedContentView
      root={root}
      currentRootId={currentRootId}
      relatedSessionRootId={relatedSessionRootId}
      relatedSessionSnapshot={relatedSessionSnapshot}
      relatedSessionKey={relatedSessionKey}
      relatedSelectedPath={relatedSelectedPath}
      relatedSelectedFileKey={relatedSelectedFileKey}
      relatedFileSelectionKey={relatedFileSelectionKey}
      managedRootByIdRef={managedRootByIdRef}
      selectedKanbanTask={selectedKanbanTask}
      selectedSessionRelatedFiles={selectedSessionRelatedFiles}
      selectedSessionRelatedFileGroups={selectedSessionRelatedFileGroups}
      selectedRelatedFileStatsByKey={selectedRelatedFileStatsByKey}
      gitFileStatsByPath={gitFileStatsByPath}
      normalizePath={normalizePath}
      handleSelectedSessionFileClick={handleSelectedSessionFileClick}
      handleRemoveSessionRelatedFile={handleRemoveSessionRelatedFile}
    />
  );
  const renderRootGitContent = (root: string): React.ReactNode => (
    <RootGitContentView
      root={root}
      currentRootId={currentRootId}
      scopedRootKey={scopedRootKey}
      managedRootByIdRef={managedRootByIdRef}
      gitStatus={gitStatus}
      gitStatusByRoot={gitStatusByRoot}
      gitStatusLoading={gitStatusLoading}
      gitStatusLoadingByRoot={gitStatusLoadingByRoot}
      gitHistory={gitHistory}
      gitHistoryByRoot={gitHistoryByRoot}
      gitHistoryLoading={gitHistoryLoading}
      gitHistoryLoadingMore={gitHistoryLoadingMore}
      gitHistoryLoadingByRoot={gitHistoryLoadingByRoot}
      gitStatusExpandedByRoot={gitStatusExpandedByRoot}
      gitHistoryExpandedByRoot={gitHistoryExpandedByRoot}
      setGitStatusExpandedByRoot={setGitStatusExpandedByRoot}
      setGitHistoryExpandedByRoot={setGitHistoryExpandedByRoot}
      refreshGitStatus={refreshGitStatus}
      refreshGitHistory={refreshGitHistory}
      loadMoreGitHistory={loadMoreGitHistory}
      openGitDiff={openGitDiff}
      openGitCommitDiff={openGitCommitDiff}
      handleGitPull={handleGitPull}
      handleGitPush={handleGitPush}
      handleGitCommit={handleGitCommit}
      handleGitStageItem={handleGitStageItem}
      handleGitUnstageItem={handleGitUnstageItem}
      handleGitDiscardItem={handleGitDiscardItem}
      switchGitBranch={switchGitBranch}
      actionHandlers={actionHandlers}
      projectSortMode={treeSortMode}
    />
  );
  // 四块 = 未开始 / 执行中 / 待审核 / 已结束（已结束内分 完成、取消；失败并入取消）。
  const kanbanStageColumns: Array<{
    index: number;
    name: string;
    role: "user" | "agent";
    tasks: KanbanTask[];
    groups?: Array<{ key: string; name: string; tone: "success" | "danger" | "muted"; tasks: KanbanTask[] }>;
  }> = [
    {
      index: 0,
      name: t("task.column.pending"),
      role: "user" as const,
      tasks: kanbanTasks.filter((task) => task.status === "pending"),
    },
    {
      index: 1,
      name: t("task.column.running"),
      role: "agent" as const,
      tasks: kanbanTasks.filter((task) => task.status === "running" || task.status === "queued" || task.status === "paused"),
    },
    {
      index: 2,
      name: t("task.column.waitingUser"),
      role: "user" as const,
      tasks: kanbanTasks.filter((task) => task.status === "waiting_user"),
    },
    {
      index: 3,
      // 已结束 = 完成 + 取消；失败合并进取消。
      name: t("task.column.ended"),
      role: "user" as const,
      tasks: kanbanTasks.filter((task) => task.status === "success" || task.status === "fail" || task.status === "cancelled"),
      groups: [{
        key: "success",
        name: t("task.group.completed"),
        tone: "success" as const,
        tasks: kanbanTasks.filter((task) => task.status === "success"),
      }, {
        key: "cancelled",
        name: t("task.group.cancelled"),
        tone: "muted" as const,
        tasks: kanbanTasks.filter((task) => task.status === "fail" || task.status === "cancelled"),
      }],
      // 别在这里滤掉空分组：TaskBoardView 用「groups 非空」决定走不走分组渲染，
      // 滤掉「已完成」后，只要「已取消」有任务就整组看不见——列头却仍按 tasks.length
      // 显示两状态总和，于是出现「列头有数字、里面找不到对应组」。空组由渲染层显示成 0。
    },
  ];
  // 跨项目工作台：按项目聚合的跨节点任务总览（扇出与建组都在 useWorkspaceBoard 内）。
  // refreshToken 由刷新按钮驱动；WS 的高频推送靠 C1 放行的 task.updated 增量更新就地生效，
  // 不驱动整包重拉（否则多节点扇出会被事件风暴打爆）。
  const [workspaceBoardToken, setWorkspaceBoardToken] = useState(0);
  const [workspaceFilter, setWorkspaceFilter] = useState<WorkspaceBoardFilter>(loadWorkspaceFilter);
  const [workspaceCollapsedKeys, setWorkspaceCollapsedKeys] = useState<Set<string>>(loadWorkspaceCollapsed);
  const toggleWorkspaceProject = useCallback((key: string) => {
    setWorkspaceCollapsedKeys((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      saveWorkspaceCollapsed(next);
      return next;
    });
  }, []);
  const changeWorkspaceFilter = useCallback((filter: WorkspaceBoardFilter) => {
    setWorkspaceFilter(filter);
    saveWorkspaceFilter(filter);
  }, []);
  const workspaceBoard = useWorkspaceBoard({
    enabled: workspaceOpen,
    refreshToken: workspaceBoardToken,
    managedRootIds,
    getRootDisplayName,
    getNodeColor: getDisplayNodeColor,
    getNodeId: getNodeIdForRoot,
    fallbackNodeId: getNodeIdForRoot(String(currentRootIdRef.current || "")) || String(getActiveNode()?.id || ""),
    filter: workspaceFilter,
  });
  // 左下角四态切换器：只切面板 + 关掉悬浮框。
  // 会话选中与面板是正交的两条线——切面板一律不解除选中（面板不显示而已）。
  const handleMainViewSwitcherChange = useCallback((mode: MainViewMode) => {
    const rootID = currentRootIdRef.current;
    if (rootID) setDrawerOpenForRoot(rootID, false);
    interactionModeRef.current = "main";
    setInteractionMode("main");
    switchMainView(mode);
    // 切到文件面板但从没点过任何目录时，主区是空的：把当前项目顶层目录补上。
    // 已经有内容（含报错态）就别动，用户点过的目录优先于这个默认值。
    if (mode === "files" && rootID && !mainEntriesRef.current.length && !mainDirectoryErrorRef.current) {
      const currentDir = selectedDirRef.current || "";
      // 尚停留在上一个项目时（selectedDir 属于别的 root），直接落在新项目根目录
      if (!currentDir || currentDir === "." || currentDir === rootID || currentDir.startsWith(`${rootID}/`)) {
        void actionHandlersRef.current.open_dir({
          path: rootID,
          root: rootID,
          isRoot: true,
          forceDirectory: true,
        });
      }
    }
    // 只改 URL 的 view，保留 root/node/session/pluginQuery。
    // 离开文件态时把 file 摘掉，否则会写出「view=board 且 file=…」的自相矛盾深链。
    const current = readURLState();
    replaceURLState({
      ...current,
      root: current.root || rootID || "",
      file: mode === "files" ? current.file : "",
      view: mode,
    });
  }, [replaceURLState, setDrawerOpenForRoot, switchMainView]);

  const openWorkspaceProject = useCallback(async (rootId: string) => {
    if (!rootId) return;
    // R6：工作台里点项目 = 切到该项目并落到「项目看板」，而不是回到文件列表
    await actionHandlersRef.current.open_dir({
      path: rootId,
      root: rootId,
      isRoot: true,
      nodeId: getNodeIdForRoot(rootId) || undefined,
      preservePluginQuery: true,
    });
    switchMainView("board");
    setTaskTemplateFilter(TASK_TEMPLATE_ALL_FILTER);
  }, [setTaskTemplateFilter, switchMainView]);
  // 工作台的快速发起不自己建任务：挑完项目 + 模板就打开看板那套新建任务面板，
  // 模板、worktree、agent、附件全都跟项目看板里一致，不在工作台上复制第二份实现。
  const handleWorkspaceCreateTask = useCallback((rootId: string, _nodeId: string, template: TaskTemplate) => {
    openTaskCreateDialog(template, rootId);
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
  const kanbanTaskPanel = (
    <TaskBoardView
      workspaceOpen={workspaceOpen}
      currentRootId={currentRootId}
      currentRootIdRef={currentRootIdRef}
      workspaceBoard={workspaceBoard}
      workspaceFilter={workspaceFilter}
      onWorkspaceFilterChange={changeWorkspaceFilter}
      workspaceCollapsedKeys={workspaceCollapsedKeys}
      onToggleWorkspaceProject={toggleWorkspaceProject}
      onRefreshWorkspaceBoard={() => setWorkspaceBoardToken((token) => token + 1)}
      managedRootIds={managedRootIds}
      getRootDisplayName={getRootDisplayName}
      openWorkspaceProject={openWorkspaceProject}
      openWorkspaceTaskDetail={openWorkspaceTaskDetail}
      handleWorkspaceCreateTask={handleWorkspaceCreateTask}
      setSelectedKanbanTaskId={setSelectedKanbanTaskId}
      kanbanTasksLoading={kanbanTasksLoading}
      kanbanStageColumns={kanbanStageColumns}
      selectedKanbanTaskId={selectedKanbanTaskId}
      expandedTaskInputIds={expandedTaskInputIds}
      setExpandedTaskInputIds={setExpandedTaskInputIds}
      collapsedTaskCompletionGroups={collapsedTaskCompletionGroups}
      setCollapsedTaskCompletionGroups={setCollapsedTaskCompletionGroups}
      collapsedKanbanColumns={collapsedKanbanColumns}
      setCollapsedKanbanColumns={setCollapsedKanbanColumns}
      taskFirstInputById={taskFirstInputById}
      taskSessionKeysById={taskSessionKeysById}
      sessionByKey={sessionByKey}
      getDisplayNodeColor={getDisplayNodeColor}
      handleSelectKanbanTask={handleSelectKanbanTask}
      handleMoveKanbanTask={handleMoveKanbanTask}
      openTaskCreateDialog={openTaskCreateDialog}
      handleTaskSessionDrawerOpen={handleTaskSessionDrawerOpen}
      setTaskSessionErrorDialog={setTaskSessionErrorDialog}
      loadKanbanTasks={loadKanbanTasks}
      templateControls={{
        taskTemplateActionMenuRef,
        taskTemplateActionMenuOpen,
        setTaskTemplateActionMenuOpen,
        taskCreateTemplateMenuRef,
        taskCreateTemplateMenuOpen,
        setTaskCreateTemplateMenuOpen,
        taskTemplates,
        taskTemplateFilter,
        setTaskTemplateFilter,
        openTaskTemplateEditor,
        handleDeleteTaskTemplate,
      }}
    />

  );

  if (mainView === "chat" && !selectedSession) {
    // chat 模式但没有选中会话：给一个明确空态，而不是把看板/文件列表塞回来（否则看起来像「自己跳走了」）
    workspaceView = (
      <div
        style={{
          flex: 1,
          minHeight: 0,
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          padding: 24,
          color: "var(--text-secondary)",
          fontSize: 13,
        }}
      >
        {t("view.chatEmpty")}
      </div>
    );
  } else if (activePendingPluginTrust) {
    workspaceView = (
      <div
        style={{
          flex: 1,
          minHeight: 0,
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          padding: 24,
          background: "var(--mindfs-main-bg, transparent)",
        }}
      >
        <section
          style={{
            width: "min(720px, 100%)",
            border: "1px solid var(--border-color)",
            borderRadius: 8,
            background: "var(--mindfs-panel-bg, var(--bg-primary))",
            padding: 20,
            display: "flex",
            flexDirection: "column",
            gap: 14,
          }}
        >
          <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
            <ModeIcon type="plugin" size={18} />
            <strong style={{ fontSize: 15, color: "var(--text-primary)" }}>
              {t("plugin.trustTitle")}
            </strong>
          </div>
          <div style={{ fontSize: 13, color: "var(--text-secondary)", lineHeight: 1.55 }}>
            {t("plugin.trustRisk")}
          </div>
          <div style={{ fontSize: 12, color: "var(--text-secondary)", lineHeight: 1.5 }}>
            <div>{t("plugin.trustRoot", { path: activePendingPluginTrust.bundle.rootPath })}</div>
            <div>{t("plugin.trustCount", { count: activePendingPluginTrust.bundle.plugins.length })}</div>
          </div>
          <div
            style={{
              border: "1px solid var(--border-color)",
              borderRadius: 6,
              overflow: "hidden",
            }}
          >
            {activePendingPluginTrust.bundle.plugins.map((plugin) => (
              <div
                key={plugin.path}
                style={{
                  display: "grid",
                  gridTemplateColumns: "minmax(0, 1fr) auto",
                  gap: 12,
                  padding: "8px 10px",
                  borderBottom: "1px solid var(--border-color)",
                  fontSize: 12,
                }}
              >
                <span style={{ color: "var(--text-primary)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                  {plugin.path}
                </span>
                <span style={{ color: "var(--text-secondary)", fontFamily: "monospace" }}>
                  {plugin.sha256.slice(0, 12)}
                </span>
              </div>
            ))}
          </div>
          <div style={{ display: "flex", justifyContent: "flex-end", gap: 10 }}>
            <button
              type="button"
              onClick={handleDisablePendingPlugins}
              style={{
                border: "1px solid var(--border-color)",
                background: "transparent",
                borderRadius: 6,
                padding: "6px 10px",
                cursor: "pointer",
                color: "var(--text-secondary)",
              }}
            >
              {t("plugin.trustDisable")}
            </button>
            <button
              type="button"
              onClick={() => {
                void handleTrustPendingPlugins();
              }}
              style={{
                border: "1px solid var(--accent-color)",
                background: "var(--accent-color)",
                borderRadius: 6,
                padding: "6px 10px",
                cursor: "pointer",
                color: "var(--accent-foreground, #fff)",
              }}
            >
              {pluginLoading ? t("plugin.loading") : t("plugin.trustAllow")}
            </button>
          </div>
        </section>
      </div>
    );
  } else if (gitDiff) {
    workspaceView = (
      <GitDiffViewer
        diff={gitDiff}
        root={currentRootId}
        rootDisplayName={currentRootDisplayName}
        rootColor={getDisplayNodeColor(String(currentRootId || ""))}
        sideBySide={gitDiffSideBySide}
        onPathClick={handleGitDiffPathClick}
        onSessionClick={(sessionKey) =>
          handleSessionChipClick(sessionKey, currentRootIdRef.current)
        }
        onSelectionChange={handleViewerSelectionChange}
      />
    );
  } else if (file) {
    if (pluginRender && pluginRender.output) {
      workspaceView = (
        <div
          style={{
            display: "flex",
            flexDirection: "column",
            flex: 1,
            minHeight: 0,
          }}
        >
          <div
            style={{
              height: "36px",
              borderBottom: "1px solid var(--border-color)",
              padding: "0 12px",
              display: "flex",
              alignItems: "center",
              justifyContent: "space-between",
              background: "var(--mindfs-topbar-bg, transparent)",
              fontSize: 12,
              color: "var(--text-secondary)",
            }}
          >
            <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
              <ModeIcon type="plugin" size={16} />
              <span>{pluginRender.plugin.name}</span>
              {pluginLoading ? (
                <span style={{ opacity: 0.7 }}>{t("plugin.loading")}</span>
              ) : null}
            </div>
            <button
              type="button"
              onClick={() => {
                void switchToRawFileView();
              }}
              style={{
                border: "1px solid var(--border-color)",
                background: "transparent",
                borderRadius: 6,
                padding: "3px 8px",
                cursor: "pointer",
                fontSize: 12,
                color: "var(--text-secondary)",
              }}
            >
              {t("plugin.rawFile")}
            </button>
          </div>
          <div
            ref={pluginContentRef}
            className="plugin-shadcn-sandbox"
            style={{
              ...pluginThemeVars,
              flex: 1,
              minHeight: 0,
              overflow: "auto",
              padding: 12,
            }}
          >
            <Renderer
              key={pluginRendererKey}
              tree={pluginRender.output.tree as any}
              initialState={
                (pluginRender.output.data || {}) as Record<string, unknown>
              }
              handlers={pluginHandlers}
            />
          </div>
        </div>
      );
    } else {
      workspaceView = (
        <div
          style={{
            display: "flex",
            flexDirection: "column",
            flex: 1,
            minHeight: 0,
          }}
        >
          {pluginBypass && matchedPlugin ? (
            <div
              style={{
                borderBottom: "1px solid var(--border-color)",
                padding: "8px 12px",
                fontSize: 12,
                color: "var(--text-secondary)",
                display: "flex",
                justifyContent: "space-between",
                alignItems: "center",
              }}
            >
              <span>{t("plugin.rawViewActive", { name: matchedPlugin.name })}</span>
              <button
                type="button"
                onClick={() => {
                  void switchToPluginView();
                }}
                style={{
                  border: "1px solid var(--border-color)",
                  background: "transparent",
                  borderRadius: 6,
                  padding: "3px 8px",
                  cursor: "pointer",
                  fontSize: 12,
                  color: "var(--text-secondary)",
                }}
              >
                {t("plugin.usePlugin")}
              </button>
            </div>
          ) : null}
          {pluginRender && pluginRender.error ? (
            <div
              style={{
                borderBottom: "1px solid var(--border-color)",
                padding: "8px 12px",
                fontSize: 12,
                color: "#d97706",
                display: "flex",
                justifyContent: "space-between",
                alignItems: "center",
              }}
            >
              <span>
                {t("plugin.renderFailedFallback", { name: pluginRender.plugin.name })}
              </span>
              <button
                type="button"
                onClick={() => setPluginBypass(true)}
                style={{
                  border: "1px solid var(--border-color)",
                  background: "transparent",
                  borderRadius: 6,
                  padding: "3px 8px",
                  cursor: "pointer",
                  fontSize: 12,
                  color: "var(--text-secondary)",
                }}
              >
                {t("plugin.ignorePlugin")}
              </button>
            </div>
          ) : null}
          <FileViewer
            file={file}
            rootDisplayName={getRootDisplayName(file?.root || currentRootId)}
            isVisible={!selectedSession}
            rootColor={getDisplayNodeColor(String(file?.root || currentRootId || ""))}
            onSelectionChange={handleViewerSelectionChange}
            initialScrollTop={
              fileScrollPositionsRef.current[currentFileScrollKey] || 0
            }
            onScrollTopChange={(scrollTop) => {
              if (!currentFileScrollKey) return;
              fileScrollPositionsRef.current[currentFileScrollKey] = scrollTop;
              persistFileScrollPositions(fileScrollPositionsRef.current);
            }}
            onSessionClick={(sessionKey) =>
              handleSessionChipClick(
                sessionKey,
                file?.root || currentRootIdRef.current,
              )
            }
            onPathClick={handleFileViewerPathClick}
            onFileClick={handleFileViewerFileClick}
          />
        </div>
      );
    }
  } else {
    workspaceView = (
      <DefaultListView
        root={currentRootId || undefined}
        rootDisplayName={currentRootDisplayName || undefined}
        path={selectedDir || ""}
        entries={currentMainContentView === "file-browser" ? visibleMainEntries : []}
        errorMessage={currentMainContentView === "file-browser" ? mainDirectoryError : ""}
        topContent={currentMainContentView === "task-kanban" ? kanbanTaskPanel : null}
        showHiddenFiles={showHiddenFiles}
        sortMode={currentDirectorySortMode}
        sortControlValue={currentDirectorySortOverride || "inherit"}
        currentViewMode={currentMainContentView}
        workspaceMode={workspaceOpen}
        workspaceProjectCount={workspaceBoard.projects.length}
        uploadProgress={directoryUploadProgress}
        onCancelUpload={() => directoryUploadAbortRef.current?.abort()}
        onSortModeChange={(nextMode) => {
          const rootID = currentRootIdRef.current;
          const nextKey = getDirectorySortKey(rootID, selectedDirRef.current);
          if (!nextKey) {
            return;
          }
          setDirectorySortOverrides((prev) => {
            if (nextMode === "inherit") {
              if (!(nextKey in prev)) {
                return prev;
              }
              const next = { ...prev };
              delete next[nextKey];
              return next;
            }
            return { ...prev, [nextKey]: nextMode };
          });
        }}
        onUploadFiles={handleTreeUpload}
        onRenameRoot={handleRenameCurrentRoot}
        onRemoveRoot={handleRemoveCurrentRoot}
        isGitRepo={managedRootByIdRef.current[currentRootId || ""]?.is_git_repo === true}
        isGitWorktree={managedRootByIdRef.current[currentRootId || ""]?.is_git_worktree === true}
        enableGitHistoryToggle={false}
        onCreateWorktree={handleOpenWorktreeLocation}
        onSwitchWorktree={handleSwitchWorktreeStart}
        onRemoveWorktree={handleRemoveCurrentWorktree}
        onOpenScheduledAgentTasks={() => setScheduledAgentDialogOpen(true)}
        menuOverlay={
          projectAddMode === "worktree_location"
            ? projectAddOverlay
            : worktreeSwitchOpen
              ? worktreeSwitchOverlay
              : worktreeCreateOverlay
        }
        onItemClick={(e) =>
          e.is_dir
            ? actionHandlers.open_dir({ path: e.path })
            : actionHandlers.open({ path: e.path })
        }
        onPathClick={handleDirectoryPathClick}
        rootColor={getDisplayNodeColor(String(currentRootId || ""))}
      />
    );
  }

  useEffect(() => {
    const body = document.body;
    if (!pluginThemeVars || pluginBypass || !pluginRender?.output) {
      body.removeAttribute("data-plugin-theme");
      body.style.removeProperty("--vp-overlay-bg");
      body.style.removeProperty("--vp-surface-bg");
      body.style.removeProperty("--vp-surface-bg-elevated");
      body.style.removeProperty("--vp-text");
      body.style.removeProperty("--vp-text-muted");
      body.style.removeProperty("--vp-border");
      body.style.removeProperty("--vp-primary");
      body.style.removeProperty("--vp-primary-text");
      body.style.removeProperty("--vp-radius");
      body.style.removeProperty("--vp-shadow");
      body.style.removeProperty("--vp-focus-ring");
      body.style.removeProperty("--vp-danger");
      body.style.removeProperty("--vp-warning");
      body.style.removeProperty("--vp-success");
      return;
    }
    body.setAttribute("data-plugin-theme", "1");
    Object.entries(pluginThemeVars).forEach(([key, value]) => {
      body.style.setProperty(key, String(value));
    });
    return () => {
      body.removeAttribute("data-plugin-theme");
      body.style.removeProperty("--vp-overlay-bg");
      body.style.removeProperty("--vp-surface-bg");
      body.style.removeProperty("--vp-surface-bg-elevated");
      body.style.removeProperty("--vp-text");
      body.style.removeProperty("--vp-text-muted");
      body.style.removeProperty("--vp-border");
      body.style.removeProperty("--vp-primary");
      body.style.removeProperty("--vp-primary-text");
      body.style.removeProperty("--vp-radius");
      body.style.removeProperty("--vp-shadow");
      body.style.removeProperty("--vp-focus-ring");
      body.style.removeProperty("--vp-danger");
      body.style.removeProperty("--vp-warning");
      body.style.removeProperty("--vp-success");
    };
  }, [pluginThemeVars, pluginBypass, pluginRender]);

  const switchToRawFileView = useCallback(async () => {
    if (!file) return;
    const root = file.root || currentRootIdRef.current;
    if (!root) return;
    pluginBypassRef.current = true;
    setPluginBypass(true);
    await actionHandlers.open({
      path: file.path,
      root,
      cursor: fileCursorRef.current || 0,
      readMode: "incremental",
      preservePluginQuery: true,
    });
  }, [file, actionHandlers]);

  const switchToPluginView = useCallback(async () => {
    if (!file) return;
    const root = file.root || currentRootIdRef.current;
    if (!root) return;
    pluginBypassRef.current = false;
    setPluginBypass(false);
    await actionHandlers.open({
      path: file.path,
      root,
      cursor: fileCursorRef.current || 0,
      preservePluginQuery: true,
    });
  }, [file, actionHandlers]);

  const showUpdateButton = shouldShowUpdateButton(updateState);
  const updateBusy =
    updateSubmitting ||
    ["downloading", "installing", "restarting"].includes(
      (updateState.status || "").toLowerCase(),
    );
  const updateLabel = updateButtonLabel(updateState, t);
  const updateHelp = updateState.message || updateSummaryText(updateState, t);
  const updateSummary = updateSummaryText(updateState, t);

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
    multiProjectSessionGroups,
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
    loadMoreMultiProjectSessions,
    loadChildSessionsForParent,
    sessionSearchResults,
    sessions,
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
        sidebar={
          <div style={{ flex: 1, minHeight: 0, display: "flex", flexDirection: "column" }}>
            <div style={{ flex: 1, minHeight: 0, overflow: "auto" }}>
              <FileTree
            entries={rootEntries}
            childrenByPath={entriesByPath}
            expanded={expanded}
            sortMode={treeSortMode}
            showHiddenFiles={showHiddenFiles}
            onSortModeChange={setTreeSortMode}
            onRefresh={handleProjectTreeRefresh}
            onNodeManagerRefresh={handleNodeManagerRefresh}
            selectedDirKey={selectedDirKey}
            selectedPath={file?.path}
            rootId={currentRootId}
            rootNodeId={currentRootNodeId}
            rootColor={getDisplayNodeColor(String(currentRootId || ""))}
            rootSessionIndicators={rootSessionIndicators}
            creatingRootName={
              creatingRootKind === "worktree" ? null : creatingRootName
            }
            creatingRootBusy={creatingRootBusy}
            onOpenProjectAdd={handleOpenProjectAdd}
            onCreateRootStart={handleCreateRootStart}
            onCreateRootNameChange={setCreatingRootName}
            onCreateRootSubmit={() => {
              void handleCreateRootSubmit();
            }}
            onCreateRootCancel={handleCreateRootCancel}
            projectAddOverlay={
              projectAddMode === "worktree_location" ? null : projectAddOverlay
            }
            onSelectFile={(e, r) => {
              actionHandlers.open({ path: e.path, root: r, nodeId: (e as any)._nodeId });
              if (isMobile) setIsLeftOpen(false);
            }}
            onSelectRoot={(e, r) => {
              // 点项目名不切主面板（工作台除外），只按当前面板语境换内容。
              const mode = mainViewRef.current;
              const target = String(r || e.path || "");
              if (
                mode === "chat" &&
                target === String(currentRootIdRef.current || "")
              ) {
                // 对话态点本项目名：无动作
                return;
              }
              void actionHandlers.open_dir({
                path: e.path,
                root: r,
                isRoot: e.is_root === true,
                // 点根行即展开该项目根的树（配合根树互斥，其它根自动收起）
                toggle: false,
                nodeId: (e as any)._nodeId,
              });
              // 工作台：点任何项目 = 进该项目的项目看板
              if (mode === "workspace") switchMainView("board");
            }}
            onSelectDir={(e, r) =>
              // 点文件夹名：无条件切到文件面板并显示该目录
              actionHandlers.open_dir({
                path: e.path,
                root: r,
                toggle: false,
                isRoot: false,
                nodeId: (e as any)._nodeId,
                switchToFiles: true,
              })
            }
            onToggleDir={(e, r) =>
              actionHandlers.open_dir({
                path: e.path,
                root: r,
                toggle: true,
                isRoot: e.is_root === true,
                nodeId: (e as any)._nodeId,
              })
            }
            renderRootExtraContent={renderRootGitContent}
            renderRootWorktreeContent={renderRootWorktreeContent}
            renderRootRelatedContent={renderRootRelatedContent}
            projectTreeTabRequest={projectTreeTabRequest}
            agentConfigSwitchRequest={agentConfigSwitchRequest}
            onAgentConfigSwitched={(agentName) => {
              if (agentName.trim().toLowerCase() === "codex") {
                setCodexRateLimitsRefreshToken((value) => value + 1);
              }
            }}
            onProjectTreeTabChange={setProjectTreeTab}
            updateActionLabel={showUpdateButton ? updateLabel : null}
            updateActionDisabled={updateBusy}
            updateActionHelp={showUpdateButton ? updateHelp : ""}
            updateActionBusy={updateBusy}
            updateActionSummary={showUpdateButton ? updateSummary : ""}
            onUpdateAction={() => {
              void handleStartUpdate();
            }}
            showEnterKeySendOption={isMobile}
            enterKeySends={mobileEnterKeySends}
            onEnterKeySendsChange={setMobileEnterKeySends}
            showSendShortcutOption={!isMobile}
            sendShortcut={sendShortcut}
            onSendShortcutChange={setSendShortcut}
            sidebarsSwapped={sidebarsSwapped}
            onSidebarsSwappedChange={setSidebarsSwapped}
            gitDiffSideBySide={gitDiffSideBySide}
            onGitDiffSideBySideChange={setGitDiffSideBySide}
            fontSizePreferences={fontSizePreferences}
            onFontSizePreferencesChange={setFontSizePreferences}
            onRunAgentLifecycleCommand={handleRunAgentLifecycleCommand}
            onRestartAgent={handleRestartAgent}
            onGoHome={onGoHome}
          />
            </div>
            <MainViewSwitcher
              value={mainView}
              onChange={handleMainViewSwitcherChange}
              accentColor={getDisplayNodeColor(String(currentRootId || "")) || undefined}
            />
          </div>
        }
        rightSidebar={sessionSidebar}
        main={
          <div
            data-onboarding="workspace"
            style={{
              width: "100%",
              flex: 1,
              minHeight: 0,
              minWidth: 0,
              display: "flex",
              flexDirection: "column",
              position: "relative",
            }}
          >
            <div
              style={{
                flex: 1,
                minHeight: 0,
                minWidth: 0,
                overflow: "hidden",
                display: "flex",
                flexDirection: "column",
              }}
            >
              <div
                style={{
                  display: showSessionPane ? "flex" : "none",
                  flex: 1,
                  minHeight: 0,
                  minWidth: 0,
                }}
              >
                {sessionView}
              </div>
              <div
                style={{
                  display: showSessionPane ? "none" : "flex",
                  flex: 1,
                  minHeight: 0,
                  minWidth: 0,
                  flexDirection: "column",
                }}
              >
                {workspaceView}
              </div>
            </div>
          </div>
        }
        footer={
          <div
            style={{
              width: "100%",
              minWidth: 0,
              display: "flex",
              flexDirection: "column",
              alignItems: "stretch",
              background: "var(--content-bg)",
            }}
          >
            {currentRootSlashCommandResult ? (
              <div
                style={{
                  width: "100%",
                  boxSizing: "border-box",
                  padding: isMobile ? "0 0 6px" : "0 16px 8px",
                }}
              >
                {renderRootSlashCommandResult(currentRootSlashCommandResult)}
              </div>
            ) : null}
            <ActionBar
              status={status}
              agentsVersion={agentsVersion}
              codexRateLimitsRefreshToken={codexRateLimitsRefreshToken}
              currentRootId={currentRootId}
              currentRootIsGitRepo={managedRootByIdRef.current[currentRootId || ""]?.is_git_repo === true}
              currentSession={actionBarSession}
              pendingPlanMode={pendingPlanMode}
              rootColor={getDisplayNodeColor(String(currentRootId || ""))}
              attachedFileContext={attachedFileContext}
              canOpenSessionDrawer={canOpenSessionDrawer}
              hideComposer={mainView === "board" || mainView === "workspace"}
              sessionDrawerOpen={isDrawerOpen}
              detachedBoundSession={detachedBoundSession}
              editDraftRequest={editDraftRequest}
              queuedMessages={actionBarQueuedMessages}
              inputHistory={actionBarInputHistory}
              onSendMessage={handleSendMessage}
              onSetPlanMode={handleSetPlanMode}
              onCancelCurrentTurn={handleCancelCurrentTurn}
              onRemoveQueuedMessage={handleRemoveQueuedMessage}
              onUpdateQueuedMessage={handleUpdateQueuedMessage}
              onSendQueuedMessageNow={handleSendQueuedMessageNow}
              mobileEnterKeySends={mobileEnterKeySends}
              sendShortcut={sendShortcut}
              onNewSession={handleNewSession}
              onRequestFileContext={handleRequestFileContext}
              onClearFileContext={handleClearFileContext}
              onToggleLeftSidebar={() => setIsLeftOpen((v) => !v)}
              onToggleRightSidebar={() => setIsRightOpen((v) => !v)}
              sidebarsSwapped={sidebarsSwapped}
              onSessionClick={() => {
              const rootID = currentRootIdRef.current;
              // 直接用 canOpenSessionDrawer：它已经把「会话是否真的在主区」算全了。
              // 这里若单独重算一份（漏掉 mainView 前提），文件态会被误判成「已在主区」而点了没反应。
              if (!canOpenSessionDrawer) return;
              const isDrawerCurrentlyOpen =
                !!drawerOpenByRootRef.current[scopedRootKey(rootID || "")];
              if (isDrawerCurrentlyOpen) {
                interactionModeRef.current = "main";
                setInteractionMode("main");
                setDrawerOpenForRoot(rootID, false);
                return;
              }
              setInteractionMode("drawer");
              setDrawerOpenForRoot(rootID, true);
              }}
            />
          </div>
        }
        drawer={
          <BottomSheet
            // 悬浮框只属于文件态：离开文件态一律不显示。
            isOpen={isDrawerOpen && mainView === "files"}
            contentRef={drawerScrollRef}
            onClose={() => {
              interactionModeRef.current = "main";
              setInteractionMode("main");
              setDrawerOpenForRoot(currentRootIdRef.current, false);
            }}
            onExpand={() => {
              handleSelectSession(currentSession, {
                preserveTaskSelection:
                  !!currentSession?.task_id &&
                  currentSession.task_id === selectedKanbanTaskId,
              });
              setDrawerOpenForRoot(currentRootIdRef.current, false);
            }}
          >
            {drawerSessionSnapshot ? (
              <SessionViewer
                session={drawerSessionSnapshot}
                agents={availableAgents}
                slashCommandResult={slashCommandResultForSession(
                  currentRootId,
                  drawerSessionSnapshot,
                )}
                targetSeq={currentSession?.search_seq}
                targetSeqRequestKey={currentSession?.search_target_id}
                composerOverlayInset={sessionViewerComposerOverlayInset}
                loading={
                  drawerLoadingSessionByRoot[currentRootId || ""] ===
                  (drawerSessionSnapshot.key || drawerSessionSnapshot.session_key)
                }
                rootId={currentRootId}
                rootColor={getDisplayNodeColor(String(currentRootId || ""))}
                rootPath={
                  managedRootByIdRef.current[currentRootId || ""]?.root_path ||
                  null
                }
                interactionMode="drawer"
                scrollContainerRef={drawerScrollRef}
                gitFileStatsByPath={gitFileStatsByPath}
                onFileClick={handleDrawerSessionFileClick}
                onRootClick={(root) => {
                  void actionHandlers.open_dir({
                    path: root,
                    root,
                    isRoot: true,
                    forceDirectory: true,
                    suppressTreeExpand: true,
                  });
                }}
                onRemoveRelatedFile={(path, head, repoPath, repoKind) =>
                  void handleRemoveSessionRelatedFile(
                    currentRootId,
                    drawerSessionSnapshot?.key || drawerSessionSnapshot?.session_key,
                    path,
                    head,
                    repoPath,
                    repoKind,
                  )
                }
                onAskUserAnswer={handleAskUserAnswer}
                onEditUserMessage={handleEditUserMessage}
                onForkAgentMessage={(seq) =>
                  void handleForkAgentMessage(
                    currentRootId,
                    drawerSessionSnapshot?.key || drawerSessionSnapshot?.session_key,
                    seq,
                  )
                }
              />
            ) : (
              <div style={{ padding: "40px", textAlign: "center" }}>
                {t("task.startHint")}
              </div>
            )}
          </BottomSheet>
        }
      />
      {!isMobile ? <OnboardingTour
        open={onboardingOpen}
        isMobile={isMobile}
        onStepChange={handleOnboardingStepChange}
        onComplete={() => {
          completeOnboarding();
          setOnboardingOpen(false);
          setOnboardingMainContentViewRoot(null);
          if (isMobile) {
            setIsLeftOpen(false);
            setIsRightOpen(false);
          }
        }}
        onDismiss={() => {
          dismissOnboarding();
          setOnboardingOpen(false);
          setOnboardingMainContentViewRoot(null);
          if (isMobile) {
            setIsLeftOpen(false);
            setIsRightOpen(false);
          }
        }}
      /> : null}
      {bootstrapState.phase === "needs_pairing" &&
        e2eeState.required &&
        !e2eeState.unlocked ? (
        <div
          style={{
            position: "fixed",
            inset: 0,
            background: "rgba(15, 23, 42, 0.46)",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            padding: "24px",
            zIndex: 2000,
          }}
        >
          <div
            style={{
              width: "min(460px, 100%)",
              background: "#fff",
              borderRadius: "20px",
              padding: "24px",
              boxShadow: "0 28px 80px rgba(15, 23, 42, 0.22)",
              display: "flex",
              flexDirection: "column",
              gap: "14px",
            }}
	          >
	            <div style={{ fontSize: "20px", fontWeight: 700, color: "#0f172a" }}>
	              {t("e2ee.title")}
	            </div>
	            <input
	              type="text"
              value={e2eeSecretInput}
              onChange={(event) => {
                setE2eeSecretInput(event.target.value);
                if (e2eePromptError) {
                  setE2eePromptError("");
                }
              }}
              onKeyDown={(event) => {
                if (event.key === "Enter" && !e2eePromptBusy) {
                  void submitE2EESecret();
                }
              }}
	              placeholder={t("e2ee.placeholder")}
              autoFocus
              spellCheck={false}
              style={{
                width: "100%",
                borderRadius: "14px",
                border: "1px solid rgba(148, 163, 184, 0.4)",
                padding: "14px 16px",
                fontSize: "14px",
                outline: "none",
              }}
            />
            {e2eePromptError ? (
              <div style={{ color: "#dc2626", fontSize: "13px" }}>
                {e2eePromptError}
              </div>
            ) : null}
            <div style={{ display: "flex", justifyContent: "space-between", gap: "12px" }}>
              <button
                type="button"
                onClick={() => {
                  setE2eeSecretInput("");
                  setE2eePromptError("");
                }}
                style={{
                  border: "none",
                  background: "transparent",
                  color: "#64748b",
                  padding: 0,
                  cursor: "pointer",
                }}
              >
                {t("e2ee.clear")}
              </button>
              <button
                type="button"
                onClick={() => void submitE2EESecret()}
                disabled={e2eePromptBusy}
                style={{
                  border: "none",
                  borderRadius: "999px",
                  background: e2eePromptBusy ? "#94a3b8" : "#0f172a",
                  color: "#fff",
                  padding: "10px 18px",
                  cursor: e2eePromptBusy ? "not-allowed" : "pointer",
                  fontWeight: 600,
                }}
              >
                {e2eePromptBusy ? t("e2ee.verifying") : t("e2ee.continue")}
              </button>
            </div>
          </div>
        </div>
      ) : null}
      {taskInlineEdit ? (
	        (() => {
	          // worktree 开关跟的是**面板的目标项目**：从工作台发起时那不是当前项目，
	          // 写死 currentRootId 会让「非 git 项目也显示 worktree 开关」。
	          const taskInlineTargetRootId = taskInlineEdit.targetRootId || currentRootId || "";
	          const taskInlineCanCreateWorktree = managedRootByIdRef.current[taskInlineTargetRootId]?.is_git_repo === true;
	          const showTaskWorktreeControls = taskInlineCanCreateWorktree && taskInlineEdit.canToggleWorktree;
	          const taskWorktreeControlsEditable = taskInlineEdit.canToggleWorktree;
	          // 编辑区里的 agent 选择器：默认值就是「下一个 agent 阶段」在模板里
	          // 的 agent/model，用户没动过就不写覆盖，让后端照模板走。
	          const taskInlineTemplate = taskTemplates.find((tpl) => tpl.id === taskInlineEdit.templateId) || null;
	          const taskInlineTemplateAgent = firstAgentStage(taskInlineTemplate);
	          const taskInlineHasAgentStage = !!taskInlineTemplateAgent;
	          // 喂给 StageEditor 的那一段：user 段（角色不可切、没有段名），
	          // 但带上「下一个 agent 阶段」的 agent/model/effort，选择器才有默认值。
	          // auto_advance 照模板首段的来，面板上可以临时改。
	          const createTaskInputStage: StageTemplate = {
	            name: "",
	            role: "user",
	            prompt_template: taskInlineEdit.text,
	            // 模板里第一个 agent 段没写 agent/model 时退回 claude + sonnet。
	            agent: taskInlineEdit.agentOverride || taskInlineTemplateAgent?.agent || DEFAULT_TASK_AGENT,
	            model: taskInlineEdit.modelOverride || taskInlineTemplateAgent?.model || DEFAULT_TASK_MODEL,
	            effort: taskInlineEdit.effortOverride || taskInlineTemplateAgent?.effort || "",
	            start_immediately: taskInlineEdit.startImmediately === true,
	          };
	          return (
        <PanelShell
          width={640}
          onClose={() => { if (!taskInlineSaving) closeTaskEditDialog(); }}
          closeOnOverlayClick
          hasUnsavedChanges={() => String(taskInlineEdit?.text || "").trim() !== ""}
          headerRight={(requestClose) => (
            <button type="button" aria-label={t("common.close")} title={t("common.close")} onClick={requestClose} style={panelIconButtonStyle()}>
              <CloseGlyph />
            </button>
          )}
          title={(
            <div style={{ display: "flex", alignItems: "center", flexWrap: "wrap", gap: "8px", minWidth: 0 }}>
              <div style={{ fontSize: "13px", fontWeight: 800, color: "var(--text-color)", whiteSpace: "nowrap" }}>
                {t("task.createDialogTitle", {
                  name: taskInlineEdit.templateName || t("task.defaultTitle"),
                })}
              </div>
              {taskInlineEdit.allowProjectSwitch && taskCreateProjectOptions.length > 1 ? (
                <div style={{ minWidth: "120px", maxWidth: "160px" }}>
                  <Select
                    value={taskInlineTargetRootId}
                    ariaLabel={t("task.workspaceSelectProject")}
                    onChange={(value) => switchTaskInlineEditProject(value)}
                    options={taskCreateProjectOptions}
                    size="panel"
                  />
                </div>
              ) : null}
              {taskTemplates.length > 0 ? (
                <div style={{ minWidth: "120px", maxWidth: "160px" }}>
                  <Select
                    value={taskInlineEdit.templateId}
                    ariaLabel={t("task.selectTemplate")}
                    onChange={(value) => {
                      const picked = taskTemplates.find((tpl) => tpl.id === value);
                      if (!picked) return;
                      setTaskInlineEdit((prev) => prev ? { ...prev, templateId: picked.id || "", templateName: picked.name, text: firstUserInputTemplate(picked), startImmediately: picked.stages?.[0]?.snapshot?.start_immediately === true } : prev);
                      setTaskInlineActiveToken(null);
                      setTaskInlineCandidates([]);
                    }}
                    options={taskTemplates.map((tpl) => ({ value: tpl.id || "", label: tpl.name }))}
                    size="panel"
                  />
                </div>
              ) : null}
              {showTaskWorktreeControls ? (
                <button
                  type="button"
                  onClick={() => {
                    if (!taskWorktreeControlsEditable) return;
                    setTaskInlineEdit((prev) => prev ? { ...prev, createWorktree: !prev.createWorktree } : prev);
                  }}
                  disabled={taskInlineSaving || !taskWorktreeControlsEditable}
                  aria-label={taskInlineEdit.createWorktree ? t("task.worktreeTitle") : t("task.noWorktreeTitle")}
                  title={taskInlineEdit.createWorktree ? t("task.worktreeTitle") : t("task.noWorktreeTitle")}
                  style={{
                    height: "26px",
                    borderRadius: "6px",
                    border: taskInlineEdit.createWorktree ? "1px solid rgba(22, 163, 74, 0.28)" : "1px solid var(--border-color)",
                    background: taskInlineEdit.createWorktree ? "rgba(22, 163, 74, 0.08)" : "rgba(100, 116, 139, 0.10)",
                    color: taskInlineEdit.createWorktree ? "#15803d" : "var(--text-secondary)",
                    padding: "0 8px",
                    fontSize: "12px",
                    fontWeight: 700,
                    display: "inline-flex",
                    alignItems: "center",
                    justifyContent: "center",
                    gap: "3px",
                    flexShrink: 0,
                  }}
                >
                  {taskInlineEdit.createWorktree ? "worktree" : (
                    <>
                      <NoWorktreeIcon size={12} />
                      worktree
                    </>
                  )}
                </button>
              ) : null}
              {showTaskWorktreeControls && taskInlineEdit.createWorktree ? (
                <div style={{ display: "flex", alignItems: "center", gap: "6px", minWidth: 0 }}>
                  <WorktreeBranchSelector
                    branchMode={taskInlineEdit.worktreeBranchMode}
                    branch={taskInlineEdit.worktreeBranch}
                    branches={taskWorktreeBranches.branches}
                    disabled={taskInlineSaving || !taskWorktreeControlsEditable}
                    height={26}
                    maxWidth={isMobile ? 160 : 240}
                    menuAlign={isMobile ? "left" : "right"}
                    menuPlacement="bottom"
                    onChange={(nextMode, nextBranch) => {
                      setTaskInlineEdit((prev) => {
                        if (!prev) return prev;
                        return { ...prev, worktreeBranchMode: nextMode, worktreeBranch: nextBranch };
                      });
                    }}
                  />
                </div>
              ) : null}
            </div>
          )}
        >
          <div style={{ padding: "12px", overflow: "visible", position: "relative", minHeight: 0, display: "flex", flexDirection: "column" }}>
              {taskInlineActiveToken && taskInlineCandidates.length > 0 ? (
                <div
                  style={{
                    position: "absolute",
                    left: "12px",
                    right: "12px",
                    bottom: "calc(100% + 6px)",
                    maxHeight: isMobile ? "min(42vh, 260px)" : "260px",
                    overflowY: "auto",
                    border: "1px solid var(--menu-border)",
                    borderRadius: "8px",
                    background: "var(--menu-bg)",
                    boxShadow: "0 12px 28px rgba(15, 23, 42, 0.14)",
                    zIndex: 2,
                  }}
                >
                  {taskInlineCandidates.map((candidate, index) => (
                    <button
                      key={`${candidate.type}:${candidate.name}`}
                      ref={(node) => {
                        taskInlineCandidateItemRefs.current[index] = node;
                      }}
                      type="button"
                      onMouseDown={(event) => {
                        event.preventDefault();
                        applyTaskInlineCandidate(candidate);
                      }}
                      style={{
                        width: "100%",
                        border: "none",
                        borderTop: index === 0 ? "none" : "1px solid var(--menu-divider)",
                        background: index === taskInlineCandidateIndex ? "var(--menu-active-bg)" : "transparent",
                        color: "var(--text-primary)",
                        display: "flex",
                        flexDirection: "column",
                        alignItems: "flex-start",
                        gap: "2px",
                        padding: "9px 10px",
                        textAlign: "left",
                        cursor: "pointer",
                      }}
                      onMouseEnter={() => setTaskInlineCandidateIndex(index)}
                    >
                      <span style={{ fontSize: "13px", fontWeight: 700 }}>
                        {candidate.type === "file" ? `@${candidate.name}` : candidate.type === "prompt" ? `#${candidate.name}` : candidate.type === "slash_command" ? `/${candidate.name}` : candidate.name}
                      </span>
                      {candidate.description ? (
                        <span style={{ fontSize: "11px", color: "var(--text-secondary)" }}>{candidate.description}</span>
                      ) : null}
                    </button>
                  ))}
                </div>
              ) : null}
              {taskInlineEdit.previousInputs.length > 0 ? (
                <div
                  style={{
                    marginBottom: "10px",
                    display: "flex",
                    flexDirection: "column",
                    gap: "8px",
                    maxHeight: isMobile ? "18dvh" : "180px",
                    overflowY: "auto",
                  }}
                >
                  {taskInlineEdit.previousInputs.map((item) => (
                    <div
                      key={item.id}
                      style={{
                        border: "1px solid var(--border-color)",
                        borderRadius: "8px",
                        background: "rgba(100, 116, 139, 0.08)",
                        padding: "8px 9px",
                      }}
                    >
                      <div
                        style={{
                          marginBottom: "5px",
                          fontSize: "11px",
                          fontWeight: 800,
                          color: "var(--text-secondary)",
                        }}
                      >
                        {item.label}
                      </div>
                      <div
                        style={{
                          whiteSpace: "pre-wrap",
                          wordBreak: "break-word",
                          fontSize: "12px",
                          lineHeight: 1.45,
                          color: "var(--text-color)",
                        }}
                      >
                        <InlineTokenText content={item.input} />
                      </div>
                    </div>
                  ))}
                </div>
              ) : null}
              <StageEditor
                editorRef={taskInlineEditorRef}
                stage={createTaskInputStage}
                agents={availableAgents}
                isFirstStage
                /* 任务名和 user 开关同排：以前这个输入框在 StageEditor 外面
                   自己占一行，user 芯片被挤到下一排去了。样式照抄模板编辑
                   面板的段名输入框。 */
                leading={(
                  <input
                    value={taskInlineEdit.name}
                    onChange={(event) => setTaskInlineEdit((prev) => prev ? { ...prev, name: event.target.value } : prev)}
                    placeholder={t("task.namePlaceholder")}
                    style={{ ...composerInputStyle, height: "26px", width: "180px", flex: "0 0 180px", fontWeight: 800 }}
                  />
                )}
                onChange={(patch) => setTaskInlineEdit((prev) => prev ? {
                  ...prev,
                  text: patch.prompt_template ?? prev.text,
                  ...(patch.agent ? { agentOverride: patch.agent } : {}),
                  ...(patch.model !== undefined ? { modelOverride: patch.model } : {}),
                  ...(patch.effort !== undefined ? { effortOverride: patch.effort } : {}),
                  // 首段的「立即执行」：面板上可临时改，勾上创建后直接开跑。
                  ...(patch.start_immediately !== undefined ? { startImmediately: patch.start_immediately } : {}),
                } : prev)}
                /* 字段说明跟模板编辑面板共用一套：新建任务这里填的就是模板里
                   那个「用户输入模板」的预填内容。 */
                label={(
                  <FieldLabelWithInfo
                    label={t("taskTemplate.userInputTemplate")}
                    info={t("taskTemplate.userInputTemplateInfo")}
                    helpKey="task-input"
                    openHelpKey={taskInlineHelpKey}
                    setOpenHelpKey={setTaskInlineHelpKey}
                  />
                )}
                /* 这个编辑区本身是 user 段，但新建时要在这里挑「下一个 agent 阶段」
                   用哪个 agent/模型，所以把 PromptEditor 自带的 AgentSelector 打开。
                   默认值取模板里第一个 agent 段的 agent/model（createTaskInputStage）。 */
                showAgentSelector={taskInlineHasAgentStage}
                onSend={() => void saveTaskInlineEdit()}
                sending={taskInlineSaving}
                sendDisabled={!taskInlineEdit.text.trim()}
                placeholder={t("task.editPlaceholder")}
              />
              {taskInlineEdit.attachments.length > 0 || taskInlineUploadProgress ? (
                <div style={{ marginTop: "10px", display: "flex", flexWrap: "wrap", gap: "8px" }}>
                  {taskInlineEdit.attachments.map((attachment) => attachment.isImage && attachment.previewUrl ? (
                    <div
                      key={attachment.id}
                      style={{
                        width: "54px",
                        height: "54px",
                        borderRadius: "8px",
                        border: "1px solid var(--border-color)",
                        background: "rgba(100, 116, 139, 0.10)",
                        position: "relative",
                        overflow: "hidden",
                      }}
                    >
                      <img src={attachment.previewUrl} alt={attachment.file.name} style={{ width: "100%", height: "100%", objectFit: "cover", display: "block" }} />
                      <button
                        type="button"
                        onClick={() => removeTaskInlineAttachment(attachment.id)}
                        disabled={taskInlineSaving}
                        aria-label={t("task.removeAttachment", { name: attachment.file.name })}
                        style={{
                          position: "absolute",
                          top: "2px",
                          right: "2px",
                          width: "18px",
                          height: "18px",
                          border: "none",
                          borderRadius: "999px",
                          background: "rgba(15, 23, 42, 0.72)",
                          color: "#fff",
                          cursor: taskInlineSaving ? "not-allowed" : "pointer",
                          padding: 0,
                          lineHeight: "18px",
                        }}
                      >
                        ×
                      </button>
                    </div>
                  ) : (
                    <span
                      key={attachment.id}
                      style={{
                        maxWidth: "100%",
                        border: "1px solid var(--border-color)",
                        borderRadius: "999px",
                        background: "rgba(100, 116, 139, 0.10)",
                        color: "var(--text-color)",
                        display: "inline-flex",
                        alignItems: "center",
                        gap: "6px",
                        padding: "4px 7px",
                        fontSize: "12px",
                      }}
                    >
                      <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{attachment.file.name}</span>
                      <button
                        type="button"
                        onClick={() => removeTaskInlineAttachment(attachment.id)}
                        disabled={taskInlineSaving}
                        style={{
                          border: "none",
                          background: "transparent",
                          color: "var(--text-secondary)",
                          cursor: taskInlineSaving ? "not-allowed" : "pointer",
                          padding: 0,
                        }}
                      >
                        ×
                      </button>
                    </span>
                  ))}
                  <CompactUploadProgress
                    progress={taskInlineUploadProgress}
                    label={t("upload.attachmentsProgress")}
                    statusLabel={t("upload.inProgress")}
                    cancelLabel={t("upload.cancel")}
                    onCancel={() => taskInlineUploadAbortRef.current?.abort()}
                  />
                </div>
              ) : null}
              <input
                ref={taskInlineAttachmentInputRef}
                type="file"
                multiple
                style={{ display: "none" }}
                onChange={handleTaskInlineAttachmentChange}
              />
            </div>
    </PanelShell>
          );
        })()
      ) : null}
      {taskSessionErrorDialog ? (
        <div
          style={{
            position: "fixed",
            inset: 0,
            zIndex: 96,
            background: "rgba(15, 23, 42, 0.28)",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            padding: "24px",
          }}
          onMouseDown={(event) => {
            if (event.target === event.currentTarget) {
              setTaskSessionErrorDialog(null);
            }
          }}
        >
          <section
            style={{
              width: "min(460px, 100%)",
              borderRadius: "10px",
              border: "1px solid rgba(217, 119, 6, 0.22)",
              background: "var(--menu-bg)",
              boxShadow: "0 24px 60px rgba(15, 23, 42, 0.24)",
              overflow: "hidden",
            }}
          >
            <div style={{ padding: "12px 14px", borderBottom: "1px solid var(--border-color)", display: "flex", alignItems: "center", justifyContent: "space-between", gap: "10px" }}>
              <div style={{ minWidth: 0, fontSize: "13px", fontWeight: 800, color: "var(--text-color)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                {taskSessionErrorDialog.title}
              </div>
              <button type="button" aria-label={t("task.closeErrorInfo")} onClick={() => setTaskSessionErrorDialog(null)} style={taskCardIconButtonStyle()}>
                ×
              </button>
            </div>
            <div style={{ padding: "14px", display: "flex", flexDirection: "column", gap: "10px" }}>
              <div
                style={{
                  padding: "10px 12px",
                  borderRadius: "8px",
                  background: "rgba(217, 119, 6, 0.08)",
                  border: "1px solid rgba(217, 119, 6, 0.18)",
                  color: "var(--text-color)",
                  fontSize: "12px",
                  lineHeight: 1.5,
                  whiteSpace: "pre-wrap",
                  overflowWrap: "anywhere",
                }}
              >
                {taskSessionErrorDialog.message}
              </div>
              {taskSessionErrorDialog.details.map((detail) => (
                <div
                  key={detail}
                  style={{
                    padding: "10px 12px",
                    borderRadius: "8px",
                    background: "rgba(100, 116, 139, 0.08)",
                    border: "1px solid var(--border-color)",
                    color: "var(--text-secondary)",
                    fontSize: "12px",
                    lineHeight: 1.5,
                    whiteSpace: "pre-wrap",
                    overflowWrap: "anywhere",
                  }}
                >
                  {detail}
                </div>
              ))}
            </div>
          </section>
        </div>
      ) : null}
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
        nodeId={currentRootId ? getNodeIdForRoot(currentRootId) : undefined}
      />
      {selectedKanbanTask ? (
        <TaskDetailPanel
          detail={taskDetailsById[selectedKanbanTask.id] || { task: selectedKanbanTask, stage_runs: [], events: [] }}
          agents={availableAgents}
          // 详情面板可能从工作台的就地入口打开（任务属于别的项目/节点），
          // 路由必须按任务自己的项目走，写死当前项目会把编辑打到错节点。
          nodeId={getNodeIdForRoot(selectedKanbanTask.root_id || currentRootId || "")}
          onClose={() => setSelectedKanbanTaskId("")}
          onOpenSession={(sessionKey) => handleTaskSessionDrawerOpen(sessionKey, selectedKanbanTask.root_id || currentRootIdRef.current, selectedKanbanTask.id)}
          onMoved={(next) => applyTaskDetails(next.task.root_id || currentRootIdRef.current || "", [next])}
          accentColor={getDisplayNodeColor(String(selectedKanbanTask.root_id || currentRootId || "")) || undefined}
        />
      ) : null}
      <ToastContainer />
      <DialogHost />
    </>
  );
}
