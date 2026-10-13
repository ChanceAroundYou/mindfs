// 从 App.tsx 抽出的渲染区域（机械搬移，JSX 逐字未改）。
import { Renderer } from "../../renderer/Renderer";
import {
  fetchFile,
  fetchEditableFile,
  saveTextFile,
  clearFileCacheForRoot,
  clearFileMemoryCacheForView,
  getCachedFile,
  invalidateFileCache,
  type FilePayload,
} from "../../services/file";
import { ViewHeader } from "../../layout/ViewHeader";
import { ModeIcon } from "../agent/ModeIcon";
import { FileViewer } from "../file/FileViewer";
import { GitDiffViewer } from "../git/GitDiffViewer";
import { DefaultListView, type MainContentViewMode } from "../file/DefaultListView";
import { getDirectorySortKey, normalizeComparableRootPath, normalizeTreeResponse, unscopedRootId } from "../../app/appUtils";
import { accountScopedKey, loadGitDiffSideBySide, loadLastRootId, loadLastRootNodeId, loadMobileEnterKeySends, loadPersistedFileScrollPositions, loadPersistedPluginQuery, loadSidebarsSwapped, loadTaskCreateWorktreePreference, persistFileScrollPositions, persistPluginQuery, removeLocalStorageByPrefix, saveTaskCreateWorktreePreference } from "../../app/appStorage";
import type { Dispatch, SetStateAction, RefObject, CSSProperties } from "react";
import { FileEditStore, fileEditKey } from "../../services/fileEditing";
import {
  buildGitDiffCacheSignature,
  clearGitHistoryCache,
  fetchGitDiff,
  fetchGitRelatedFileDiff,
  fetchGitBranches,
  removeGitWorktree,
  type GitBranchesPayload,
  type GitDiffPayload,
} from "../../services/git";
import {
  DEFAULT_DIRECTORY_SORT_MODE,
  type DirectorySortMode,
  type FileEntry,
} from "../../services/prefs/directorySort";
import { fileTokenPath, formatFileToken, isUploadAbortError, uploadFiles, type UploadProgress } from "../../services/upload";
import {
  PluginManager,
  loadPluginsFromSources,
  scanPluginSources,
  snapshotFromPluginSources,
  type PluginSourceBundle,
  type PluginInput,
} from "../../plugins/manager";
import {
  type GitHubImportState,
  type LocalDirBrowserState,
  ProjectAddPopover,
  type ProjectAddMode,
} from "../file/ProjectAddPopover";
import { APP_DOCUMENT_TITLE, AppProps, CHILD_SESSION_PAGE_SIZE, MULTI_PROJECT_SESSION_LIMIT, ManagedRootPayload, SESSION_PAGE_SIZE } from "../../app/appMisc";
import { MainViewMode, URLState } from "../../app/appPath";
import { AttachedFileContext, Exchange, GitFileStat, MultiProjectSessionGroup, PendingSend, RelatedFileClickTarget, SessionItem, SessionMode, SessionQueueItem, SlashCommandResult, ViewerSelection, WSStatus } from "../../app/appSession";
import type { ActionHandlers } from "../../app/useActionHandlers";
import type { ViewPlugin } from "../../plugins/manager";
import type { PluginOutput } from "../../plugins/manager";
import type { GitStatusPayload } from "../../services/git/types";
import type { WorkspaceBoard } from "../../app/useWorkspaceBoard";
import { useI18n } from "../../i18n";
import type { ReactNode } from "react";
import React, {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";

export interface SessionPaneProps {
  sessionView: React.JSX.Element;
  showSessionPane: boolean;
  actionHandlers: ActionHandlers;
  activePendingPluginTrust: { rootId: string; bundle: PluginSourceBundle; } | null;
  clearFileCacheForRoot: any;
  currentDirectorySortMode: DirectorySortMode;
  currentDirectorySortOverride: DirectorySortMode | undefined;
  currentFileEditing: boolean;
  currentFileScrollKey: string;
  currentMainContentView: "task-kanban" | "file-browser";
  currentRootDisplayName: string;
  currentRootId: string | null;
  currentRootIdRef: RefObject<string | null>;
  directoryUploadAbortRef: RefObject<AbortController | null>;
  directoryUploadProgress: UploadProgress | null;
  file: FilePayload | null;
  fileEditStore: FileEditStore;
  fileRef: RefObject<FilePayload | null>;
  fileScrollPositionsRef: RefObject<Record<string, number>>;
  getDirectorySortKey: any;
  getDisplayNodeColor: (rootId: string) => string | null;
  getRootDisplayName: (rootId: string | null | undefined) => string;
  gitDiff: GitDiffPayload | null;
  gitDiffSideBySide: boolean;
  handleCreateBlankFile: () => Promise<void>;
  handleDirectoryPathClick: (path: string) => void;
  handleDisablePendingPlugins: () => void;
  handleFileViewerFileClick: (path: string) => void;
  handleFileViewerPathClick: (path: string) => void;
  handleGitDiffPathClick: (path: string) => void;
  handleOpenWorktreeLocation: () => void;
  handleRemoveCurrentRoot: () => Promise<void>;
  handleRemoveCurrentWorktree: () => Promise<void>;
  handleRenameCurrentRoot: (nextName: string) => Promise<boolean>;
  handleSessionChipClick: (sessionKey: string, rootOverride?: string | null | undefined) => void;
  handleSwitchWorktreeStart: () => void;
  handleTreeUpload: (files: File[]) => Promise<void>;
  handleTrustPendingPlugins: () => Promise<void>;
  handleViewerSelectionChange: (next: ViewerSelection | null) => void;
  kanbanTaskPanel: React.JSX.Element;
  mainDirectoryError: string;
  mainView: MainViewMode;
  managedRootByIdRef: RefObject<Record<string, ManagedRootPayload>>;
  matchedPlugin: ViewPlugin | null;
  persistFileScrollPositions: any;
  pluginBypass: boolean;
  pluginContentRef: RefObject<HTMLDivElement | null>;
  pluginHandlers: { open: (params: Record<string, unknown>) => Promise<void>; open_dir: (params: Record<string, unknown>) => Promise<void>; select_session: (params: Record<string, unknown>) => Promise<void>; navigate: (params: Record<string, unknown>) => Promise<void>; };
  pluginLoading: boolean;
  pluginRender: { plugin: ViewPlugin; output: PluginOutput; error: string; } | { plugin: ViewPlugin; output: null; error: string; } | null;
  pluginRendererKey: string;
  pluginThemeVars: CSSProperties | null;
  projectAddMode: ProjectAddMode | null;
  projectAddOverlay: React.JSX.Element | null;
  refreshGitStatus: (rootID: string) => Promise<GitStatusPayload | null>;
  refreshTreeDir: (rootID: string, dirPath: string, syncMain: boolean) => Promise<void>;
  selectedDir: string | null;
  selectedDirRef: RefObject<string | null>;
  selectedSession: SessionItem | null;
  setDirectorySortOverrides: Dispatch<SetStateAction<Record<string, DirectorySortMode>>>;
  setEntriesByPath: Dispatch<SetStateAction<Record<string, FileEntry[]>>>;
  setFile: Dispatch<SetStateAction<FilePayload | null>>;
  setPluginBypass: Dispatch<SetStateAction<boolean>>;
  setScheduledAgentDialogOpen: Dispatch<SetStateAction<boolean>>;
  showHiddenFiles: true;
  switchToPluginView: () => Promise<void>;
  switchToRawFileView: () => Promise<void>;
  t: ReturnType<typeof useI18n>["t"];
  visibleMainEntries: FileEntry[];
  workspaceBoard: WorkspaceBoard;
  workspaceOpen: boolean;
  worktreeCreateOverlay: React.JSX.Element | null;
  worktreeSwitchOpen: boolean;
  worktreeSwitchOverlay: React.JSX.Element | null;
}

export function SessionPane(props: SessionPaneProps) {
  const sessionView = props.sessionView;
  const showSessionPane = props.showSessionPane;

  const actionHandlers = props.actionHandlers;
  const activePendingPluginTrust = props.activePendingPluginTrust;
  const clearFileCacheForRoot = props.clearFileCacheForRoot;
  const currentDirectorySortMode = props.currentDirectorySortMode;
  const currentDirectorySortOverride = props.currentDirectorySortOverride;
  const currentFileEditing = props.currentFileEditing;
  const currentFileScrollKey = props.currentFileScrollKey;
  const currentMainContentView = props.currentMainContentView;
  const currentRootDisplayName = props.currentRootDisplayName;
  const currentRootId = props.currentRootId;
  const currentRootIdRef = props.currentRootIdRef;
  const directoryUploadAbortRef = props.directoryUploadAbortRef;
  const directoryUploadProgress = props.directoryUploadProgress;
  const file = props.file;
  const fileEditStore = props.fileEditStore;
  const fileRef = props.fileRef;
  const fileScrollPositionsRef = props.fileScrollPositionsRef;
  const getDirectorySortKey = props.getDirectorySortKey;
  const getDisplayNodeColor = props.getDisplayNodeColor;
  const getRootDisplayName = props.getRootDisplayName;
  const gitDiff = props.gitDiff;
  const gitDiffSideBySide = props.gitDiffSideBySide;
  const handleCreateBlankFile = props.handleCreateBlankFile;
  const handleDirectoryPathClick = props.handleDirectoryPathClick;
  const handleDisablePendingPlugins = props.handleDisablePendingPlugins;
  const handleFileViewerFileClick = props.handleFileViewerFileClick;
  const handleFileViewerPathClick = props.handleFileViewerPathClick;
  const handleGitDiffPathClick = props.handleGitDiffPathClick;
  const handleOpenWorktreeLocation = props.handleOpenWorktreeLocation;
  const handleRemoveCurrentRoot = props.handleRemoveCurrentRoot;
  const handleRemoveCurrentWorktree = props.handleRemoveCurrentWorktree;
  const handleRenameCurrentRoot = props.handleRenameCurrentRoot;
  const handleSessionChipClick = props.handleSessionChipClick;
  const handleSwitchWorktreeStart = props.handleSwitchWorktreeStart;
  const handleTreeUpload = props.handleTreeUpload;
  const handleTrustPendingPlugins = props.handleTrustPendingPlugins;
  const handleViewerSelectionChange = props.handleViewerSelectionChange;
  const kanbanTaskPanel = props.kanbanTaskPanel;
  const mainDirectoryError = props.mainDirectoryError;
  const mainView = props.mainView;
  const managedRootByIdRef = props.managedRootByIdRef;
  const matchedPlugin = props.matchedPlugin;
  const persistFileScrollPositions = props.persistFileScrollPositions;
  const pluginBypass = props.pluginBypass;
  const pluginContentRef = props.pluginContentRef;
  const pluginHandlers = props.pluginHandlers;
  const pluginLoading = props.pluginLoading;
  const pluginRender = props.pluginRender;
  const pluginRendererKey = props.pluginRendererKey;
  const pluginThemeVars = props.pluginThemeVars;
  const projectAddMode = props.projectAddMode;
  const projectAddOverlay = props.projectAddOverlay;
  const refreshGitStatus = props.refreshGitStatus;
  const refreshTreeDir = props.refreshTreeDir;
  const selectedDir = props.selectedDir;
  const selectedDirRef = props.selectedDirRef;
  const selectedSession = props.selectedSession;
  const setDirectorySortOverrides = props.setDirectorySortOverrides;
  const setEntriesByPath = props.setEntriesByPath;
  const setFile = props.setFile;
  const setPluginBypass = props.setPluginBypass;
  const setScheduledAgentDialogOpen = props.setScheduledAgentDialogOpen;
  const showHiddenFiles = props.showHiddenFiles;
  const switchToPluginView = props.switchToPluginView;
  const switchToRawFileView = props.switchToRawFileView;
  const t = props.t;
  const visibleMainEntries = props.visibleMainEntries;
  const workspaceBoard = props.workspaceBoard;
  const workspaceOpen = props.workspaceOpen;
  const worktreeCreateOverlay = props.worktreeCreateOverlay;
  const worktreeSwitchOpen = props.worktreeSwitchOpen;
  const worktreeSwitchOverlay = props.worktreeSwitchOverlay;
  const workspaceView = renderWorkspaceView();

  function renderWorkspaceView(): ReactNode {
  if (mainView === "chat" && !selectedSession) {
      // chat 模式但没有选中会话：给一个明确空态，而不是把看板/文件列表塞回来（否则看起来像「自己跳走了」）。
      // 空态也必须带 ViewHeader：移动端侧栏切换按钮只挂在各视图 header 上，没有 header
      // 就一个按钮都没有，等于卡死。桌面端因此多一条 36px 空 header，与选中会话时一致。
      return (
        <div style={{ flex: 1, minHeight: 0, display: "flex", flexDirection: "column" }}>
          <ViewHeader />
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
        </div>
      );
    } else if (activePendingPluginTrust) {
      // 插件信任面板同样顶替整个主区，也必须带 ViewHeader（理由同上：移动端没有 header
      // 就没有侧栏切换按钮）。桌面端多一条 36px 空 header。
      return (
        <div style={{ flex: 1, minHeight: 0, display: "flex", flexDirection: "column" }}>
          <ViewHeader />
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
        </div>
      );
    } else if (gitDiff && currentMainContentView === "file-browser") {
      return (
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
    } else if (file && currentMainContentView === "file-browser") {
      if (pluginRender && pluginRender.output) {
        return (
          <div
            style={{
              display: "flex",
              flexDirection: "column",
              flex: 1,
              minHeight: 0,
            }}
          >
            <ViewHeader padding="0 12px">
            <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", width: "100%", minWidth: 0, fontSize: 12, color: "var(--text-secondary)" }}>
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
            </ViewHeader>
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
        return (
          <div
            style={{
              display: "flex",
              flexDirection: "column",
              flex: 1,
              minHeight: 0,
            }}
          >
            {pluginBypass && matchedPlugin && !currentFileEditing ? (
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
              rootPath={managedRootByIdRef.current[file?.root || ""]?.root_path}
              onFileOperationComplete={async () => {
                if (!file?.root) return;
                const parent = file.path.split("/").slice(0, -1).join("/") || ".";
                clearFileCacheForRoot(file.root);
                setEntriesByPath({});
                await refreshTreeDir(file.root, parent, false);
                await actionHandlers.open_dir({ root: file.root, path: parent === "." ? file.root : parent, isRoot: parent === ".", forceDirectory: true });
                void refreshGitStatus(file.root);
              }}
              editStore={fileEditStore}
              onFileUpdated={(next) => {
                const current = fileRef.current;
                if (current?.root === next.root && current?.path === next.path) {
                  setFile({ ...current, ...next, file_meta: current.file_meta });
                }
              }}
              onFileSaved={(next) => {
                const current = fileRef.current;
                if (current?.root === next.root && current?.path === next.path) {
                  setFile({ ...current, ...next, file_meta: current.file_meta });
                }
                if (next.root) void refreshGitStatus(next.root);
              }}
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
      return (
        <DefaultListView
          root={currentRootId || undefined}
          rootPath={managedRootByIdRef.current[currentRootId || ""]?.root_path}
          onFileOperationComplete={async () => {
            if (!currentRootId) return;
            const parent = (selectedDir || "").split("/").slice(0, -1).join("/") || ".";
            clearFileCacheForRoot(currentRootId);
            setEntriesByPath({});
            await refreshTreeDir(currentRootId, parent, false);
            await actionHandlers.open_dir({ root: currentRootId, path: parent === "." ? currentRootId : parent, isRoot: parent === ".", forceDirectory: true });
            void refreshGitStatus(currentRootId);
          }}
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
          onCreateBlankFile={handleCreateBlankFile}
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
  }

  return (
    <>
    {
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
    </>
  );
}
