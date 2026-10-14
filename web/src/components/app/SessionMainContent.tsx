import type { ReactNode } from "react";
import type { SessionPaneProps } from "./sessionPaneTypes";
import { PendingPluginTrustView } from "./PendingPluginTrustView";

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
} from "../../services/file";
import type { FilePayload } from "../../shared/fileTypes";
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
import React, {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";

export function SessionMainContent(props: Omit<SessionPaneProps, "sessionView" | "showSessionPane">) {
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
  return renderWorkspaceView();

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
      return (
        <PendingPluginTrustView
          trust={activePendingPluginTrust}
          loading={pluginLoading}
          onDisable={handleDisablePendingPlugins}
          onTrust={handleTrustPendingPlugins}
          t={t}
        />
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

}
