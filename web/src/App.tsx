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


  useAppRefSync({
    currentRootIdRef, currentRootId, currentRootNodeIdRef, currentRootNodeId,
    setAvailableAgents, agentsVersion, e2eeState, getNodeIdForRoot,
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
    sessionListReloadTimerRef,
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
    sessionLoadFailureRef, sessionSearchTargetCounterRef, sessions, sessionsRef,
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
