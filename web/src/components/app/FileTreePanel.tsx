// 从 App.tsx 抽出的渲染区域（机械搬移，JSX 逐字未改）。
import { FileTree } from "../file/FileTree";
import type { AgentConfigSwitchRequest, ProjectTreeTab } from "../file/fileTreeTypes";
import { MainViewSwitcher } from "../shell/MainViewSwitcher";
import type { Dispatch, SetStateAction, RefObject, ReactNode } from "react";
import React, {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import type { SendShortcut } from "../../shared/sendShortcut";
import type { FontSizePreferences } from "../../shared/fontSize";
import type { FilePayload } from "../../shared/fileTypes";
import type { DirectorySortMode, FileEntry } from "../../shared/directorySort";
import {
  type GitHubImportState,
  type LocalDirBrowserState,
  ProjectAddPopover,
  type ProjectAddMode,
} from "../file/ProjectAddPopover";
import { MainViewMode, URLState } from "../../app/appPath";
import type { ActionHandlers } from "../../app/useActionHandlers";

export interface FileTreePanelProps {
  actionHandlers: ActionHandlers;
  agentConfigSwitchRequest: AgentConfigSwitchRequest | null;
  creatingRootBusy: boolean;
  creatingRootKind: "root" | "worktree";
  creatingRootName: string | null;
  currentRootId: string | null;
  currentRootIdRef: RefObject<string | null>;
  currentRootNodeId: string | null;
  entriesByPath: Record<string, FileEntry[]>;
  expanded: string[];
  file: FilePayload | null;
  fontSizePreferences: FontSizePreferences;
  getDisplayNodeColor: (rootId: string) => string | null;
  gitDiffSideBySide: boolean;
  handleCreateRootCancel: () => void;
  handleCreateRootStart: (parentPath?: string | null | undefined) => void;
  handleCreateRootSubmit: () => Promise<void>;
  handleMainViewSwitcherChange: (mode: MainViewMode) => void;
  handleNodeManagerRefresh: () => Promise<void>;
  handleOpenProjectAdd: () => void;
  handleProjectTreeRefresh: (tab: ProjectTreeTab) => Promise<void>;
  handleRestartAgent: (agentName: string) => Promise<void>;
  handleRunAgentLifecycleCommand: (agentName: string, action: "install" | "update", commands: string[]) => Promise<void>;
  handleStartUpdate: () => Promise<void>;
  isMobile: boolean;
  mainView: MainViewMode;
  mainViewRef: RefObject<MainViewMode>;
  mobileEnterKeySends: boolean;
  onGoHome: any;
  projectAddMode: ProjectAddMode | null;
  projectAddOverlay: React.JSX.Element | null;
  projectTreeTabRequest: { tab: ProjectTreeTab; nonce: number; } | null;
  renderRootGitContent: (root: string) => ReactNode;
  renderRootRelatedContent: (root: string) => ReactNode;
  renderRootWorktreeContent: (root: string) => ReactNode;
  rootEntries: FileEntry[];
  rootSessionIndicators: Record<string, { bound?: boolean | undefined; pending?: boolean | undefined; }>;
  selectedDirKey: string | null;
  sendShortcut: SendShortcut | null;
  setCodexRateLimitsRefreshToken: Dispatch<SetStateAction<number>>;
  setCreatingRootName: Dispatch<SetStateAction<string | null>>;
  setFontSizePreferences: Dispatch<SetStateAction<FontSizePreferences>>;
  setGitDiffSideBySide: Dispatch<SetStateAction<boolean>>;
  setIsLeftOpen: Dispatch<SetStateAction<boolean>>;
  setMobileEnterKeySends: Dispatch<SetStateAction<boolean>>;
  setProjectTreeTab: Dispatch<SetStateAction<ProjectTreeTab>>;
  setSendShortcut: Dispatch<SetStateAction<SendShortcut | null>>;
  setSidebarsSwapped: Dispatch<SetStateAction<boolean>>;
  setTreeSortMode: Dispatch<SetStateAction<DirectorySortMode>>;
  showHiddenFiles: true;
  showUpdateButton: boolean;
  sidebarsSwapped: boolean;
  switchMainView: (mode: MainViewMode) => void;
  treeSortMode: DirectorySortMode;
  updateBusy: boolean;
  updateHelp: string;
  updateLabel: string;
  updateSummary: string;
}

export function FileTreePanel(props: FileTreePanelProps) {
  const actionHandlers = props.actionHandlers;
  const agentConfigSwitchRequest = props.agentConfigSwitchRequest;
  const creatingRootBusy = props.creatingRootBusy;
  const creatingRootKind = props.creatingRootKind;
  const creatingRootName = props.creatingRootName;
  const currentRootId = props.currentRootId;
  const currentRootIdRef = props.currentRootIdRef;
  const currentRootNodeId = props.currentRootNodeId;
  const entriesByPath = props.entriesByPath;
  const expanded = props.expanded;
  const file = props.file;
  const fontSizePreferences = props.fontSizePreferences;
  const getDisplayNodeColor = props.getDisplayNodeColor;
  const gitDiffSideBySide = props.gitDiffSideBySide;
  const handleCreateRootCancel = props.handleCreateRootCancel;
  const handleCreateRootStart = props.handleCreateRootStart;
  const handleCreateRootSubmit = props.handleCreateRootSubmit;
  const handleMainViewSwitcherChange = props.handleMainViewSwitcherChange;
  const handleNodeManagerRefresh = props.handleNodeManagerRefresh;
  const handleOpenProjectAdd = props.handleOpenProjectAdd;
  const handleProjectTreeRefresh = props.handleProjectTreeRefresh;
  const handleRestartAgent = props.handleRestartAgent;
  const handleRunAgentLifecycleCommand = props.handleRunAgentLifecycleCommand;
  const handleStartUpdate = props.handleStartUpdate;
  const isMobile = props.isMobile;
  const mainView = props.mainView;
  const mainViewRef = props.mainViewRef;
  const mobileEnterKeySends = props.mobileEnterKeySends;
  const onGoHome = props.onGoHome;
  const projectAddMode = props.projectAddMode;
  const projectAddOverlay = props.projectAddOverlay;
  const projectTreeTabRequest = props.projectTreeTabRequest;
  const renderRootGitContent = props.renderRootGitContent;
  const renderRootRelatedContent = props.renderRootRelatedContent;
  const renderRootWorktreeContent = props.renderRootWorktreeContent;
  const rootEntries = props.rootEntries;
  const rootSessionIndicators = props.rootSessionIndicators;
  const selectedDirKey = props.selectedDirKey;
  const sendShortcut = props.sendShortcut;
  const setCodexRateLimitsRefreshToken = props.setCodexRateLimitsRefreshToken;
  const setCreatingRootName = props.setCreatingRootName;
  const setFontSizePreferences = props.setFontSizePreferences;
  const setGitDiffSideBySide = props.setGitDiffSideBySide;
  const setIsLeftOpen = props.setIsLeftOpen;
  const setMobileEnterKeySends = props.setMobileEnterKeySends;
  const setProjectTreeTab = props.setProjectTreeTab;
  const setSendShortcut = props.setSendShortcut;
  const setSidebarsSwapped = props.setSidebarsSwapped;
  const setTreeSortMode = props.setTreeSortMode;
  const showHiddenFiles = props.showHiddenFiles;
  const showUpdateButton = props.showUpdateButton;
  const sidebarsSwapped = props.sidebarsSwapped;
  const switchMainView = props.switchMainView;
  const treeSortMode = props.treeSortMode;
  const updateBusy = props.updateBusy;
  const updateHelp = props.updateHelp;
  const updateLabel = props.updateLabel;
  const updateSummary = props.updateSummary;

  return (
    <>
    {
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
    </>
  );
}
