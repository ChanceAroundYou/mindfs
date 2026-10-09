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
  const [fileEditStore] = useState(() => new FileEditStore({ load: fetchEditableFile, save: saveTextFile }));
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
  // 会话缓存（快照表 + 加载状态/失败记录/LRU 淘汰）搬进 app/useSessionCache.ts：
  // 策略集中在一处，App.tsx 只留读写点。refs 语义与变量名不变，调用点零改动。
  const {
    sessionCacheRef,
    loadedSessionRef,
    loadingSessionRef,
    sessionLoadFailureRef,
    sessionCacheAccessRef,
    staleSessionKeysRef,
    enforceSessionCacheBound,
  } = useSessionCache();
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
  // session.created 现在是所有建会话路径的统一信号（看板/定时/子会话/fork），
  // 一次动作可能连发好几条；合并 300ms 窗口内的多次重拉，避免逐节点扇出打爆接口。
  const MULTI_PROJECT_RELOAD_DEBOUNCE_MS = 300;
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
  const { kanbanTasks, setKanbanTasks, kanbanTaskCountItems, setKanbanTaskCountItems, taskDetailsById, setTaskDetailsById, taskFirstInputById, setTaskFirstInputById, taskSessionKeysById, setTaskSessionKeysById, taskRelatedFilesById, setTaskRelatedFilesById, taskDetailsByIdRef, workspaceBoardRefreshRef, taskSessionKeysByIdRef, selectedKanbanTaskId, setSelectedKanbanTaskId, expandedTaskInputIds, setExpandedTaskInputIds, overflowingTaskInputs, setOverflowingTaskInputs, collapsedTaskCompletionGroups, setCollapsedTaskCompletionGroups, collapsedKanbanColumns, setCollapsedKanbanColumns, taskInlineEdit, setTaskInlineEdit, taskSessionErrorDialog, setTaskSessionErrorDialog, taskInlineActiveToken, setTaskInlineActiveToken, taskInlineCandidates, setTaskInlineCandidates, taskInlineCandidateIndex, setTaskInlineCandidateIndex, taskInlineSaving, setTaskInlineSaving, taskInlineHelpKey, setTaskInlineHelpKey, taskInlineUploadProgress, setTaskInlineUploadProgress, directoryUploadProgress, setDirectoryUploadProgress, taskWorktreeBranches, setTaskWorktreeBranches, taskWorktreeBranchesLoading, setTaskWorktreeBranchesLoading, taskWorktreeBranchError, setTaskWorktreeBranchError, kanbanTasksLoading, setKanbanTasksLoading, kanbanLoadSeqRef, kanbanAbortRef, refreshTaskWorktreeRef, sessionListLoadSeqRef, sessionListAbortRef, listLoadErrorAtRef, taskInlineEditorRef, taskInlineCandidateItemRefs, taskInlineAttachmentInputRef, taskInlineUploadAbortRef, directoryUploadAbortRef, knownTaskWorktreePathsRef } = useTaskWorkspaceState();
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
    managedRootByIdRef,
    taskInlineEdit,
    taskTemplates,
    taskInlineCandidates,
    taskInlineCandidateIndex,
    taskInlineUploadAbortRef,
    taskInlineEditorRef,
    taskInlineCandidateItemRefs,
    refreshTaskWorktreeRef,
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
  const {
    getRootDisplayName,
    getDisplayNodeColor,
    taskCreateProjectOptions,
  } = useAppProjectDisplay({
    currentRootId,
    currentRootNodeIdRef,
    managedRootByIdRef,
    managedRootByKeyRef,
    managedRootIdsRef,
    taskInlineEdit,
  });
  const currentRootDisplayName = getRootDisplayName(currentRootId);



  // 复合键还原为裸 rootId（scoped 键为 n::r 或 r；rootId 自身不含 "::"）
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
  // 把后置初始化的 refreshTaskWorktree 转发给 useTaskBoard（通过 ref 避免 TDZ）
  refreshTaskWorktreeRef.current = refreshTaskWorktree;
  const [updateState, setUpdateState] = useState<UpdateState>(() =>
    normalizeUpdateState(null),
  );
  const [updateSubmitting, setUpdateSubmitting] = useState(false);


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
    multiProjectSessionGroupsRef.current = multiProjectSessionGroups;
  }, [multiProjectSessionGroups]);
  useEffect(() => {
    multiProjectPendingRef.current = multiProjectPendingByKey;
  }, [multiProjectPendingByKey]);
  useEffect(() => {
    setPendingPlanMode(false);
  }, [currentRootId]);
  // 切项目时顺带刷新主节点的会话置顶 —— 这就是约定的「跨设备同步」时机。
  //
  // 用户明确不要跨设备实时（那要一条主节点 → 各 worker 浏览器的新通道），
  // 也不要每次都刷：「切换项目等操作的时候顺带请求一下置顶」正是这个意思。
  // 不刷新的话，设备 A 置顶后设备 B 切项目也看不到 —— 那正是被报的那个症状。
  useEffect(() => {
    if (!currentRootId || !protectedAPIReady()) return;
    // 拉不到就保持当前那份：置顶是装饰性排序，一个装饰性请求失败不该把
    // 正在看的列表闪成「全不置顶」。writePins 内部按指纹判断要不要通知订
    // 阅者，所以「服务端没变化」不会引起任何重排。
    void refreshPinsFromServer().catch(() => {});
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
    const rootID = currentRootId;
    if (!rootID) return;
    setActiveBoundSessionKey(boundSessionByRootRef.current[scopedRootKey(rootID)] || null);
    setCurrentSession(drawerSessionByRootRef.current[scopedRootKey(rootID)] || null);
    setIsDrawerOpen(!!drawerOpenByRootRef.current[scopedRootKey(rootID)]);
  }, [currentRootId]);

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
  const {
    handleStartUpdate,
    handleOnboardingStepChange,
    describeE2EEPromptError,
    submitE2EESecret,
  } = useAppBootstrapActions({
    currentRootId,
    didInitRef,
    e2eeSecretInput,
    isMobile,
    replaceURLState,
    selectedSessionRef,
    setE2eePromptBusy,
    setE2eePromptError,
    setE2eeSecretInput,
    setFile,
    setGitDiff,
    setIsLeftOpen,
    setIsRightOpen,
    setOnboardingMainContentViewRoot,
    setSelectedDir,
    setSelectedSession,
    setSelectedSessionLoading,
    setUpdateState,
    setUpdateSubmitting,
    t,
    updateState,
  });

  /* Android 硬件返回键。刻意不接回调：installBackNavigation 内部一律走
     history.back()，让返回键汇进 popstate 那条路，和浏览器后退 / iOS 侧滑
     保持同一套语义。以前这里是独立的一套「关层 / 退视图」逻辑，两套各走一遍，
     一次返回要按两下才对。 */
  useEffect(() => installBackNavigation(), []);


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
    setSelectedPendingByKey,
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

  const { formatDirectoryLoadError } = useAppFormatDirectoryLoadError({ t });

  const treeCacheKey = useCallback(
    (rootID: string, dirPath: string) =>
      treeKey(getNodeIdForRoot(rootID) ?? "", rootID, dirPath || "."),
    [getNodeIdForRoot],
  );




  const {
    refreshTreeDir,
    refreshCurrentFileContent,
  } = useFileRefresh({
    currentRootIdRef,
    file,
    fileCursorRef,
    fileEditStore,
    fileRef,
    formatDirectoryLoadError,
    getNodeIdForRoot,
    invalidTreeCacheKeysRef,
    pluginBypassRef,
    pluginManagerRef,
    pluginQueryRef,
    selectedDirRef,
    setEntriesByPath,
    setFile,
    setMainDirectoryError,
    setMainEntries,
    treeCacheKey,
    treeFetchSeqRef,
  });
  const sessionListReloadTimerRef = useRef<number | null>(null);

  // WS 高频事件（session.done / ws.reconnected / session.imported / session.created）都会触发会话列表全量重拉，
  // 合并 300ms 窗口内的多次调用为最后一次，避免 agent 密集工具调用时连发 /api/sessions。
  const {
    scheduleSessionListReload,
    handleLoadOlderSessions,
  } = useAppSessionReload({
    sessionListReloadTimerRef,
    loadSessionsForRoot,
    currentRootIdRef,
    sessionsRef,
    loadingOlderSessions,
    setLoadingOlderSessions,
    getNodeIdForRoot,
    setHasMoreSessions,
    setSessions,
    applyPinSnapshot,
  });



  const multiProjectReloadTimerRef = useRef<number | null>(null);

  /**
   * 合并窗口内多次「多项目会话重拉」为最后一次。
   *
   * 什么时候会连发：session.created 现在是**所有**建会话路径的统一信号了
   * （看板任务、定时任务、子会话、fork），而一次看板动作可能连带建多个会话；
   * agent 密集起子代理时更是一串 created。原来这些路径压根不发 created，所以
   * 裸调 loadMultiProjectSessionGroups 不觉得多；统一信号一加，
   * 每次 created 都会扇出一次 `/api/sessions?multi_root=1`（逐节点各一次），
   * 密集建会话时能把接口打爆。
   *
   * 窗口取 300ms，与 scheduleSessionListReload 同一档：单个节点的
   * `/api/sessions?multi_root=1` 实测 100–400ms，300ms 足够合并一串事件，
   * 又短到用户感知不到延迟。
   */
  const scheduleMultiProjectSessionReload = useCallback(() => {
    if (multiProjectReloadTimerRef.current !== null) {
      window.clearTimeout(multiProjectReloadTimerRef.current);
    }
    multiProjectReloadTimerRef.current = window.setTimeout(() => {
      multiProjectReloadTimerRef.current = null;
      void loadMultiProjectSessionGroups();
    }, MULTI_PROJECT_RELOAD_DEBOUNCE_MS);
  }, [loadMultiProjectSessionGroups]);

  // 卸载时清掉挂着的定时器：否则切走页面后还会打一次扇出
  useEffect(
    () => () => {
      if (multiProjectReloadTimerRef.current !== null) {
        window.clearTimeout(multiProjectReloadTimerRef.current);
        multiProjectReloadTimerRef.current = null;
      }
    },
    [],
  );


  const { refreshMultiProjectReplyingSessions } = useReplyingSessionPoll({
    applyPendingToMultiProjectGroups,
    bumpCacheVersion,
    currentRootIdRef,
    currentRootNodeIdRef,
    drawerSessionByRootRef,
    loadMultiProjectSessionGroups,
    managedRootByKeyRef,
    multiProjectPendingByKey,
    multiProjectPendingRef,
    multiProjectSessionsEnabled,
    rootSessionKey,
    scopedRootKey,
    selectedSessionRef,
    sessionCacheRef,
    setDrawerSessionForRoot,
    setMultiProjectPendingByKey,
    setMultiProjectSessionGroups,
    setSelectedSession,
  });


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
    setSessionSearchResults,
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


  const {
    handleCreateBlankFile,
    handleTreeUpload,
    handleSelectSessionAndClose,
    handlePinSession,
    pruneSubtreeFromSessionState,
  } = useSessionListActions({
    currentRootIdRef,
    directoryUploadAbortRef,
    fileRef,
    getNodeIdForRoot,
    isMobile,
    multiProjectSessionGroupsRef,
    selectedDirRef,
    sessionsRef,
    setDirectoryUploadProgress,
    setExpanded,
    setIsRightOpen,
    setMultiProjectSessionGroups,
    setSessions,
    handleSelectSession,
    refreshTreeDir,
    t,
  });

  const {
    handleDeleteSession,
    handleArchiveSession,
  } = useSessionArchive({
    boundSessionByRootRef,
    bumpCacheVersion,
    cancelRequestedBySessionRef,
    currentRootIdRef,
    drawerSessionByRootRef,
    file,
    fileCursorRef,
    fileRef,
    getNodeIdForRoot,
    loadedSessionRef,
    loadingSessionRef,
    managedRootByIdRef,
    multiProjectSessionGroupsRef,
    pendingBySessionRef,
    pluginQuery,
    pruneSubtreeFromSessionState,
    replaceURLState,
    resetSessionLockForRoot,
    rootSessionKey,
    scopedRootKey,
    selectedSessionByRootRef,
    selectedSessionRef,
    sessionCacheRef,
    sessions,
    sessionsRef,
    setArchiveReloadToken,
    setArchivedGroups,
    setBoundSessionForRoot,
    setDrawerOpenForRoot,
    setDrawerSessionForRoot,
    setMultiProjectSessionGroups,
    setMultiProjectSessionPending,
    setSelectedSession,
    setSelectedSessionLoading,
    setSessionSearchResults,
    setSessions,
    staleSessionKeysRef,
  });


  useArchiveSync({SESSION_PAGE_SIZE, archiveOpen, archiveReloadToken, getNodeIdForRoot, managedRootByKeyRef, managedRootIdsRef, rootNodeKey, sessionService, setArchiveLoading, setArchivedGroups, toSessionItem, useEffect});



  useEffect(() => {
    handleSelectSessionRef.current = handleSelectSession;
  }, [handleSelectSession]);

  const refreshSessionsAfterExternalImport = useCallback(async (rootID: string) => {
    const listNodeId = getNodeIdForRoot(rootID);
    const payload = await sessionService.fetchSessions(rootID, { nodeId: listNodeId });
    const next = [...payload.items, ...payload.pinnedItems]
      // 节点归属必须打：主列表首屏加载打了 _nodeId，这里不打就会与既有条目
      // 并存成重复的 React key（同一 mergeSessionItems 归并口径）。
      .map((item) => toSessionItem(rootID, { ...(item as any), _nodeId: (item as any)._nodeId || listNodeId }))
      .filter((item): item is SessionItem => !!item);
    setHasMoreSessions(payload.totalCount > payload.items.length);
    setSessions(applyPinSnapshot(mergeSessionItems([], next), rootID));
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

  const { handleSendMessage } = useSendMessage({
    boundSessionByRootRef,
    currentRootIdRef,
    currentSessionRef,
    drawerSessionByRootRef,
    fileRef,
    interactionModeRef,
    mainViewRef,
    managedRootByIdRef,
    pendingDraftRef,
    pendingRequestRef,
    pluginBypassRef,
    pluginManagerRef,
    pluginQueryRef,
    selectedSessionByRootRef,
    selectedSessionRef,
    sessionCacheRef,
    setSessions,
    setSlashCommandResults,
    setMultiProjectSessionGroups,
    setInteractionMode,
    setPendingPlanMode,
    setSelectedSession,
    setSelectedPendingByKey,
    attachedFileContext,
    pendingPlanMode,
    slashCommandResults,
    setBoundSessionForRoot,
    setDrawerSessionForRoot,
    setMultiProjectSessionPending,
    setDrawerOpenForRoot,
    bumpCacheVersion,
    clearLocalPendingForSession,
    clearSlashCommandResultForSession,
    rootSessionKey,
    scopedRootKey,
    updateSessionAgentForKey,
  });


  const { handleForkAgentMessage, handleRunAgentLifecycleCommand
  } = useAppAgentActions({
    boundSessionByRootRef, bumpCacheVersion, currentRootIdRef, currentSessionRef,
    getNodeIdForRoot, handleSelectSession, handleSendMessage, loadSessionsForRoot,
    loadedSessionRef, rootSessionKey, scopedRootKey, selectedSessionByRootRef,
    selectedSessionRef, sessionCacheRef, setBoundSessionForRoot, setDrawerOpenForRoot,
    setDrawerSessionForRoot, setInteractionMode, setSelectedSession, setSessions,
    suppressedAutoBindSessionByRootRef, t,
  });
  const handleRestartAgent = useCallback(async (agentName: string) => {
    const nid = getNodeIdForRoot(currentRootIdRef.current || "") as any;
    await restartAgent(agentName, nid);
    const items = await fetchAgents(true, nid);
    setAvailableAgents(items);
    setAgentsVersion((v) => v + 1);
  }, []);

  const {
    handleCancelCurrentTurn,
    handleNewSession,
    currentSelectionSource,
    rememberCurrentFileScroll,
  } = useAppSessionSelection({
    currentRootIdRef,
    boundSessionByRootRef,
    scopedRootKey,
    suppressedAutoBindSessionByRootRef,
    switchMainView,
    selectedSessionRef,
    currentSessionRef,
    interactionModeRef,
    setSelectedSession,
    selectedSessionByRootRef,
    resetSessionLockForRoot,
    rootSessionKey,
    cancelRequestedBySessionRef,
    clearLocalPendingForSession,
    file,
    gitDiff,
    fileRef,
    fileScrollPositionsRef,
  });

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
    optimisticDequeuedIdsRef,
    queuedMessagesBySessionRef,
    rootSessionKey,
    scopedRootKey,
    selectedSessionRef,
    sessionCacheRef,
    setDrawerSessionForRoot,
    setMultiProjectSessionPending,
    setQueueVersion,
    setSelectedPendingByKey,
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

  useEffect(() => {
    if (!currentRootId) return;
    if (pluginsLoadedByRootRef.current[scopedRootKey(currentRootId)]) return;
    void ensurePluginsLoaded(currentRootId).catch(() => {});
  }, [currentRootId, ensurePluginsLoaded]);


  useAppInit({actionHandlersRef, bootstrapService, bootstrapState, didInitRef, ensurePluginsLoaded, handleSelectSessionRef, indexManagedRoots, loadLastRootId, loadLastRootNodeId, loadManagedRootPayloads, loadMultiProjectSessionGroups, loadSessionsForRoot, managedRootByIdRef, managedRootByKeyRef, managedRootIdsRef, mapManagedRootsToEntries, multiProjectSessionsEnabled, readURLState, refreshTreeDir, reportError, selectRootNode, setCurrentRootId, setManagedRootIds, setPluginQuery, setRootEntries, setRootNodeMap, showBoundSessionOrRootDir, switchMainView, t, useEffect});

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
    if (!file || currentFileEditing || pluginBypass || !matchedPlugin) return;
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
    currentFileEditing,
    pluginBypass,
    matchedPlugin,
    currentRootId,
    pluginQuery,
    actionHandlers,
  ]);


  const { pluginRender, pluginThemeVars } = useAppPluginRender({
    file,
    pluginBypass,
    matchedPlugin,
    pluginQuery,
    pluginManagerRef,
  });

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

  const {
    selectedSessionSnapshot,
    sessionByKey,
    boardSessionByKey,
    selectedKanbanTask,
    selectedKanbanTaskSessionKey,
    selectedKanbanTaskSessionSnapshot,
  } = useAppSessionSnapshots({
    selectedSession,
    selectedSessionLoading,
    currentRootId,
    sessions,
    multiProjectSessionGroups,
    selectedKanbanTaskId,
    kanbanTasks,
    taskDetailsById,
    taskSessionKeysById,
    getSessionSnapshot,
  });



	  useEffect(() => {
    if (selectedSessionSnapshot) {
      lastMainSessionSnapshotRef.current = selectedSessionSnapshot as Session;
    }
  }, [selectedSessionSnapshot]);

  // CUSTOM(G-AU): 加载 effect 的依赖只能是**身份**（键 / 根 / 布尔），绝不能是
  // selectedSessionSnapshot 这个对象。它的身份随 cacheVersion 每次 bump 都变
  // （getSessionSnapshot 的 dep 数组里有 cacheVersion），而流式输出每 30ms 就 bump 一次、
  // 每个 tool_call/todo_update/plan_update 还会同步再 bump 一次 —— 于是「加载 →
  // bump → 新快照 → 再加载」自持成环。会话存在时 loadedSessionRef 会兜住，
  // **404 的会话永远兜不住**：实测一个已删会话被拉到 22–25 次/秒、持续 18 小时
  // （单小时 5232 次、累计 7800+ 次 404），把浏览器每域 6 条连接全占满 →
  // 其它请求全部排队 → 界面卡死 → 标签页被杀 → 自动重载。
  const loadSessionKey = selectedSession?.key || selectedSession?.session_key || "";
  const loadRootID =
    (selectedSession?.root_id as string | undefined) || currentRootId || "";
  const loadSnapshotHasExchanges = hasSessionExchanges(
    selectedSessionSnapshot as Session | null,
  );

  // 选中项一变就解禁失败记录：换会话/换项目本来就该重新拉一次。
  useEffect(() => {
    sessionLoadFailureRef.current = {};
  }, [loadSessionKey, loadRootID]);

  useSessionRestore({boundSessionByRootRef, bumpCacheVersion, clearSessionStale, currentRootId, currentRootIdRef, hasSessionExchanges, isSessionStale, loadRootID, loadSessionKey, loadSnapshotHasExchanges, loadedSessionRef, loadingSessionRef, resolvePendingForSession, restoreActiveSession, rootSessionKey, scopedRootKey, selectedSessionLoading, sessionCacheRef, sessionLoadFailureRef, setDrawerSessionForRoot, setSelectedSession, toSessionItem, useEffect});

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
  const sessionViewerComposerOverlayInset = 20;
  const { sessionView
  } = useAppSessionView({
    selectedSessionSnapshot, availableAgents, slashCommandResultForSession, selectedSession,
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
  // 卡片的新鲜度取自 taskDetailsById（applyTaskDetails 的唯一落点）。
  // 这里刻意由 state 派生而**不是**每次现读 ref：工作台那两个 useMemo 靠依赖变化
  // 才重算，走 ref 的话 React 看不见任何变化 —— 整条新鲜度路径会静默失效
  // （卡片照样纹丝不动，且没有任何报错可查）。useMemo 保住引用稳定，
  // 又让它在 taskDetailsById 真变时换新身份。
  const liveTasksById = useMemo(() => {
    const out: Record<string, KanbanTask> = {};
    for (const detail of Object.values(taskDetailsById)) {
      if (detail?.task?.id) out[detail.task.id] = detail.task;
    }
    return out;
  }, [taskDetailsById]);
  const getLiveTask = useCallback((taskId: string) => liveTasksById[taskId], [liveTasksById]);
  const workspaceBoard = useWorkspaceBoard({
    enabled: workspaceOpen,
    refreshToken: workspaceBoardToken,
    managedRootIds,
    getRootDisplayName,
    getNodeColor: getDisplayNodeColor,
    getLiveTask,
    getNodeId: getNodeIdForRoot,
    fallbackNodeId: getNodeIdForRoot(String(currentRootIdRef.current || "")) || String(getActiveNode()?.id || ""),
    filter: workspaceFilter,
  });
  // 渲染时挂上去，早期调用点（卡片动作、WS 处理器）转发到它。见 workspaceBoardRefreshRef。
  workspaceBoardRefreshRef.current = workspaceBoard.refresh;
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
