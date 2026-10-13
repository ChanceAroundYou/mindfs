import React, { memo } from "react";
import { rootBadgeStyle } from "../../shared/rootBadgeStyle";
import { NodeBadgeHeader } from "../account/NodeBadgeHeader";
import { ViewHeader } from "../../layout/ViewHeader";
import { hexToRgbaApp } from "../../app/taskIcons";
import {
  type FileEntry,
  sortDirectoryEntries,
} from "../../shared/directorySort";
import { useI18n } from "../../i18n/index";
import { useRefreshSpin } from "../../hooks/index";
import { usePwaInstall } from "../../hooks/usePwaInstall";
import { FileTreeRefreshButton } from "./FileTreeRefreshButton";
import {
  PROJECT_TREE_INDENT,
  PROJECT_TREE_ROOT_PADDING_LEFT,
  PROJECT_TREE_TAB_STORAGE_KEY,
  isProjectTreeTab,
  type AgentConfigSwitchSelection,
  type FileTreeProps,
  type ProjectTreeTab,
} from "./fileTreeTypes";
import { createRenderEntries } from "./fileTreeEntries";
import { childEntryKey, filterVisibleEntries } from "./FileTreeEntryFilters";
import { useAgentConfigActions } from "./useAgentConfigActions";
import { useAgentConfigFlowTransitions } from "./useAgentConfigFlowTransitions";
import { FileTreeSettingsMenu } from "./FileTreeSettingsMenu";
import { useFileTreeProtectedAPI } from "./useFileTreeProtectedAPI";
import { useFileTreeProjectTabAccent } from "./useFileTreeProjectTabAccent";
import { useFileTreeNodeGroups } from "./useFileTreeNodeGroups";
import { formatProviderTestResult, isAgentConfigBackupConflict } from "./fileTreeAgentConfigUtils";
import { useFileTreePreferences } from "./useFileTreePreferences";
import { useFileTreeAppearance } from "./useFileTreeAppearance";
import { useAgentConfigSelection } from "./useAgentConfigSelection";
import { useAgentLifecycleActions } from "./useAgentLifecycleActions";
import { useAgentConfigOutsideClick } from "./useAgentConfigOutsideClick";
import { useFileTreeEffects } from "./useFileTreeEffects";
import { useAgentConfigSwitchRequest } from "./useAgentConfigSwitchRequest";
import { FileTreeFooter } from "./FileTreeFooter";
import { FileTreeSessionNamingPopover } from "./FileTreeSessionNamingPopover";
import { FileTreeSendShortcutPopover } from "./FileTreeSendShortcutPopover";
import { FileTreeIdleReleasePopover } from "./FileTreeIdleReleasePopover";
import { FileTreeProjectContent } from "./FileTreeProjectContent";
import { FileTreeProjectTabs } from "./FileTreeProjectTabs";
import { NodeManagerPanel } from "../account/NodeManagerPanel";
import { AccountPanel } from "../account/AccountPanel";
import { scopeKey, treeKey } from "../../shared/scope";
import { AgentSelector } from "../agent/AgentSelector";
import { SymlinkBadge } from "./SymlinkBadge";
import {
  AgentConfigPopover,
  AgentLifecyclePopover,
  agentConfigActionRowStyle,
  agentConfigPrimaryButtonStyle,
  agentConfigSecondaryButtonStyle,
  type AgentConfigFlow,
  type AgentConfigStep,
  type AgentConfigAddTab,
  type AgentConfigSwitchTab,
} from "./AgentConfigPopover";
import { AgentConfigLineEditor } from "./AgentConfigLineEditor";
import type { AgentStatus } from "../../services/agents";
import type { AgentAPIProvider, AgentConfigBackup } from "../../services/agentConfig";
import type { NewProjectMetaLocation } from "../../services/prefs/preferences";
import { formatSendShortcut, shortcutFromKeyboardEvent, type SendShortcut } from "../../shared/sendShortcut";

/** 把单模型测试结果格式化成一行简短文案（成功/延迟/错误/简短响应）。 */

import {
  fileTreeMenuButtonStyle,
  NotificationIcon,
  InfoIcon,
  WebPushMenuItem,
  ChevronRight,
  DirectoryIconSlot,
  getFileIcon,
  ConfigArchiveIcon,
  ConfigSwitchIcon,
  AgentInstallIcon,
  OnboardingGuideIcon,
} from "./icons";


// memo 化：父组件（App）高频重渲染时，若 FileTree 数据 props 未变则跳过整棵目录树重渲染。
// 注：回调 props 多为 inline 箭头，浅比较下仍会触发重渲染；但文件树数据 props（entries/expanded 等）
// 稳定时（如仅 status/toast 变化）可避免昂贵的递归渲染。
function FileTreeInner({
  entries,
  childrenByPath,
  expanded,
  sortMode,
  showHiddenFiles = false,
  selectedDirKey,
  selectedPath,
  rootId,
  rootColor = null,
  rootNodeId = null,
  rootSessionIndicators = {},
  fileMetas = {},
  activeSessionKey,
  onSortModeChange,
  onRefresh,
  onNodeManagerRefresh,
  onSelectFile,
  onSelectRoot,
  onSelectDir,
  onToggleDir,
  renderRootExtraContent,
  renderRootWorktreeContent,
  renderRootRelatedContent,
  projectTreeTabRequest = null,
  agentConfigSwitchRequest = null,
  onAgentConfigSwitched,
  onProjectTreeTabChange,
  creatingRootName = null,
  creatingRootBusy = false,
  creatingRootExtraContent = null,
  creatingRootSubmitOnBlur = true,
  onCreateRootStart,
  onOpenProjectAdd,
  onCreateRootNameChange,
  onCreateRootSubmit,
  onCreateRootCancel,
  projectAddOverlay,
  updateActionLabel = null,
  updateActionDisabled = false,
  updateActionHelp = null,
  updateActionBusy = false,
  updateActionSummary = null,
  onUpdateAction,
  showEnterKeySendOption = false,
  enterKeySends = false,
  onEnterKeySendsChange,
  showSendShortcutOption = false,
  sendShortcut = null,
  onSendShortcutChange,
  sidebarsSwapped = false,
  onSidebarsSwappedChange,
  gitDiffSideBySide = false,
  onGitDiffSideBySideChange,
  multiProjectSessionsEnabled = false,
  onMultiProjectSessionsChange,
  fontSizePreferences = { fileSidebar: 1, main: 1, sessionSidebar: 1 },
  onFontSizePreferencesChange,
  onRunAgentLifecycleCommand,
  onRestartAgent,
  onGoHome,
  footerTopContent,
}: FileTreeProps) {
  const { locale, setLocale, t } = useI18n();
  const expandedSet = new Set(expanded);
  const [isMenuOpen, setIsMenuOpen] = React.useState(false);
  const [nodeManagerOpen, setNodeManagerOpen] = React.useState(false);
  const [accountPanelOpen, setAccountPanelOpen] = React.useState(false);
  const [projectTreeTab, setProjectTreeTab] = React.useState<ProjectTreeTab>(() => {
    if (typeof window === "undefined") {
      return "files";
    }
    try {
      const saved = window.localStorage.getItem(PROJECT_TREE_TAB_STORAGE_KEY);
      return isProjectTreeTab(saved) ? saved : "files";
    } catch {
      return "files";
    }
  });
  const handleTabRefresh = React.useCallback(
    () => onRefresh?.(projectTreeTab),
    [onRefresh, projectTreeTab],
  );
  const {
    refreshing: treeRefreshing,
    pressed: treePressed,
    setPressed: setTreePressed,
    handleClick: handleRefreshClick,
  } = useRefreshSpin(handleTabRefresh);
  const [isAppearanceMenuOpen, setIsAppearanceMenuOpen] = React.useState(false);
  const [isFontSizeMenuOpen, setIsFontSizeMenuOpen] = React.useState(false);
  const [isLocaleMenuOpen, setIsLocaleMenuOpen] = React.useState(false);
  const [isSortMenuOpen, setIsSortMenuOpen] = React.useState(false);
  const [sessionNamingOpen, setSessionNamingOpen] = React.useState(false);
  const [sessionNamingAgents, setSessionNamingAgents] = React.useState<AgentStatus[]>([]);
  const [sessionNamingAgent, setSessionNamingAgent] = React.useState("");
  const [sessionNamingModel, setSessionNamingModel] = React.useState("");
  const [sessionNamingDisabled, setSessionNamingDisabled] = React.useState(false);
  const [sessionNamingBusy, setSessionNamingBusy] = React.useState(false);
  const [sessionNamingError, setSessionNamingError] = React.useState("");
  const [idleReleaseOpen, setIdleReleaseOpen] = React.useState(false);
  const [sendShortcutOpen, setSendShortcutOpen] = React.useState(false);
  const [sendShortcutDraft, setSendShortcutDraft] = React.useState<SendShortcut | null>(sendShortcut);
  const [sendShortcutError, setSendShortcutError] = React.useState("");
  const [idleReleaseHours, setIdleReleaseHours] = React.useState("72");
  const [idleReleaseBusy, setIdleReleaseBusy] = React.useState(false);
  const [idleReleaseError, setIdleReleaseError] = React.useState("");
  const [newProjectMetaLocation, setNewProjectMetaLocation] = React.useState<NewProjectMetaLocation>("project");
  const [newProjectMetaLocationBusy, setNewProjectMetaLocationBusy] = React.useState(false);
  const { appearanceMode, changeAppearanceMode } = useFileTreeAppearance();
  const [isUpdateNotesOpen, setIsUpdateNotesOpen] = React.useState(false);
  const protectedAPIReady = useFileTreeProtectedAPI();
  const [agentConfigFlow, setAgentConfigFlow] = React.useState<AgentConfigFlow | null>(null);
  const [agentConfigStep, setAgentConfigStep] = React.useState<AgentConfigStep>("agent");
  const [agentConfigAgents, setAgentConfigAgents] = React.useState<AgentStatus[]>([]);
  const [agentConfigAgent, setAgentConfigAgent] = React.useState("");
  const [agentConfigAddTab, setAgentConfigAddTab] = React.useState<AgentConfigAddTab>("backup");
  const [agentConfigSwitchTab, setAgentConfigSwitchTab] = React.useState<AgentConfigSwitchTab>("backup");
  const [agentConfigName, setAgentConfigName] = React.useState("");
  const [agentConfigFileSourcesBody, setAgentConfigFileSourcesBody] = React.useState("");
  const [agentConfigEnvBody, setAgentConfigEnvBody] = React.useState("");
  const [agentAPIProviderName, setAgentAPIProviderName] = React.useState("");
  const [agentAPIProviderBaseURL, setAgentAPIProviderBaseURL] = React.useState("");
  const [agentAPIProviderAPIKey, setAgentAPIProviderAPIKey] = React.useState("");
  const [agentConfigBackups, setAgentConfigBackups] = React.useState<AgentConfigBackup[]>([]);
  const [agentAPIProviders, setAgentAPIProviders] = React.useState<AgentAPIProvider[]>([]);
  const [selectedAgentConfigID, setSelectedAgentConfigID] = React.useState("");
  const [selectedAgentAPIProviderID, setSelectedAgentAPIProviderID] = React.useState("");
  const [agentConfigSwitchSelection, setAgentConfigSwitchSelection] = React.useState<AgentConfigSwitchSelection | null>(null);
  const [agentConfigPreferredProviderIDs, setAgentConfigPreferredProviderIDs] = React.useState<string[]>([]);
  const [agentConfigConfirmMessage, setAgentConfigConfirmMessage] = React.useState("");
  const [agentConfigBusy, setAgentConfigBusy] = React.useState(false);
  const [agentConfigRestartingAgent, setAgentConfigRestartingAgent] = React.useState("");
  const [agentConfigError, setAgentConfigError] = React.useState("");
  const [agentConfigNotice, setAgentConfigNotice] = React.useState("");
  const agentConfigFlowVersion = React.useRef(0);
  const [providerSyncBusy, setProviderSyncBusy] = React.useState(false);
  const [providerTestBusyID, setProviderTestBusyID] = React.useState("");
  const [providerTestResults, setProviderTestResults] = React.useState<Record<string, string>>({});
  const [providerTestModels, setProviderTestModels] = React.useState<Record<string, string>>({});
  const [agentLifecycleOpen, setAgentLifecycleOpen] = React.useState(false);
  const [agentLifecycleAgents, setAgentLifecycleAgents] = React.useState<AgentStatus[]>([]);
  const [agentLifecycleBusy, setAgentLifecycleBusy] = React.useState(false);
  const [agentLifecycleRunningAgent, setAgentLifecycleRunningAgent] = React.useState("");
  const [agentLifecycleError, setAgentLifecycleError] = React.useState("");
  const menuRef = React.useRef<HTMLDivElement | null>(null);
  const agentConfigPopoverRef = React.useRef<HTMLDivElement | null>(null);
  const agentLifecyclePopoverRef = React.useRef<HTMLDivElement | null>(null);
  const sendShortcutPopoverRef = React.useRef<HTMLDivElement | null>(null);
  const accountButtonRef = React.useRef<HTMLButtonElement | null>(null);
  const updateNotesRef = React.useRef<HTMLDivElement | null>(null);
  const createInputRef = React.useRef<HTMLInputElement | null>(null);
  const previousCreatingRootNameRef = React.useRef<string | null>(null);

  const {
    isNativeApp,
    installLabel,
    installHelp,
    shouldShowInstallButton,
    shouldShowInstallHelp,
    handleInstall,
  } = usePwaInstall(t);

  const projectTabAccent = useFileTreeProjectTabAccent(rootColor, entries, rootId);
  const nodeGroups = useFileTreeNodeGroups(entries);

  const hasFooterContent = !!updateActionLabel || !!updateActionHelp || !!footerTopContent || (isNativeApp && !!onGoHome) || shouldShowInstallButton || shouldShowInstallHelp;

  useFileTreeEffects({
    creatingRootName, previousCreatingRootNameRef, createInputRef,
    isUpdateNotesOpen, updateNotesRef, setIsUpdateNotesOpen, updateActionLabel, updateActionSummary,
    isMenuOpen, menuRef, setIsMenuOpen, sendShortcutOpen, sendShortcutPopoverRef, setSendShortcutOpen,
    protectedAPIReady, setIdleReleaseHours, setNewProjectMetaLocation,
  });



  const { openSessionNaming, saveSessionNaming, openIdleSessionResourceRelease, openSendShortcut, saveSendShortcut, saveIdleSessionResourceRelease, toggleNewProjectMetaLocation } = useFileTreePreferences({
    t, rootId, sendShortcut, sendShortcutDraft, onSendShortcutChange, agentConfigFlowVersion,
    setIdleReleaseOpen, setAgentConfigNotice, setAgentConfigFlow, setAgentLifecycleOpen, setIsMenuOpen,
    setSessionNamingOpen, setSessionNamingBusy, setSessionNamingError, setSessionNamingAgents,
    setSessionNamingAgent, setSessionNamingModel, setSessionNamingDisabled, sessionNamingAgent,
    sessionNamingBusy, sessionNamingDisabled, sessionNamingModel, setIdleReleaseBusy, setIdleReleaseError,
    setIdleReleaseHours, idleReleaseBusy, idleReleaseHours, setSendShortcutDraft, setSendShortcutError, setSendShortcutOpen,
    newProjectMetaLocation, setNewProjectMetaLocation, newProjectMetaLocationBusy, setNewProjectMetaLocationBusy,
  });

  useAgentConfigSwitchRequest({ agentConfigSwitchRequest, rootId, t, setAgentLifecycleOpen, agentConfigFlowVersion, setAgentConfigNotice, setAgentConfigFlow, setAgentConfigStep, setAgentConfigAgent, setAgentConfigAddTab, setAgentConfigSwitchTab, setAgentConfigName, setAgentConfigFileSourcesBody, setAgentConfigEnvBody, setAgentAPIProviderName, setAgentAPIProviderBaseURL, setAgentAPIProviderAPIKey, setAgentConfigBackups, setAgentAPIProviders, setSelectedAgentConfigID, setSelectedAgentAPIProviderID, setAgentConfigSwitchSelection, setAgentConfigPreferredProviderIDs, setAgentConfigConfirmMessage, setAgentConfigError, setAgentConfigRestartingAgent, setIsMenuOpen, setAgentConfigBusy, setAgentConfigAgents });




  const { openAgentConfigFlow, closeAgentConfigFlow,
    openAgentLifecycleFlow, closeAgentLifecycleFlow } = useAgentConfigFlowTransitions({
    rootId, agentConfigFlowVersion, t,
    setIdleReleaseOpen, setAgentLifecycleOpen, setAgentConfigNotice, setAgentConfigFlow,
    setAgentConfigStep, setAgentConfigAgent, setAgentConfigAddTab, setAgentConfigSwitchTab,
    setAgentConfigName, setAgentConfigFileSourcesBody, setAgentConfigEnvBody,
    setAgentAPIProviderName, setAgentAPIProviderBaseURL, setAgentAPIProviderAPIKey,
    setAgentConfigBackups, setAgentAPIProviders, setSelectedAgentConfigID,
    setSelectedAgentAPIProviderID, setAgentConfigSwitchSelection, setAgentConfigPreferredProviderIDs,
    setAgentConfigConfirmMessage, setAgentConfigError, setAgentConfigRestartingAgent,
    setIsMenuOpen, setAgentConfigBusy, setAgentConfigAgents, setAgentLifecycleError,
    setAgentLifecycleBusy, setAgentLifecycleAgents, setAgentLifecycleRunningAgent,
  });

  useAgentConfigOutsideClick({ agentConfigFlow, agentConfigStep, agentLifecycleOpen, agentConfigPopoverRef, agentLifecyclePopoverRef, closeAgentConfigFlow, closeAgentLifecycleFlow });


  const { runAgentLifecycleCommand } = useAgentLifecycleActions({
    setAgentLifecycleError, setAgentLifecycleBusy, setAgentLifecycleRunningAgent, t,
    onRunAgentLifecycleCommand, closeAgentLifecycleFlow,
  });

  const { restartAgentFromConfigList, chooseAgentForConfig } = useAgentConfigSelection({
    agentConfigRestartingAgent, onRestartAgent, t, setAgentConfigRestartingAgent, setAgentConfigError,
    agentConfigAgent, setAgentConfigAgent, setAgentConfigBusy, agentConfigAgents, agentConfigFlow,
    setAgentConfigName, setAgentConfigFileSourcesBody, setAgentConfigEnvBody, setAgentAPIProviderName,
    setAgentAPIProviderBaseURL, setAgentAPIProviderAPIKey, setAgentConfigAddTab, setAgentConfigStep,
    setAgentConfigBackups, setAgentAPIProviders, setSelectedAgentConfigID, agentConfigPreferredProviderIDs,
    setSelectedAgentAPIProviderID, setAgentConfigSwitchSelection, setAgentConfigSwitchTab,
  });

  const { saveAgentConfigBackup, saveAgentAPIProvider, runAgentConfigSwitch, deleteSelectedAgentConfigBackup,
    deleteSelectedAgentAPIProvider, syncAllAgentAPIProviderModels, testAgentAPIProviderModelNow,
    selectAgentConfigBackup, selectAgentAPIProvider, changeProviderTestModel } = useAgentConfigActions({
      agentAPIProviderAPIKey, agentAPIProviderBaseURL, agentAPIProviderName, agentAPIProviders,
      agentConfigAgent, agentConfigBackups, agentConfigEnvBody, agentConfigFileSourcesBody, agentConfigFlowVersion,
      agentConfigName, agentConfigSwitchSelection, closeAgentConfigFlow, formatProviderTestResult,
      isAgentConfigBackupConflict, onAgentConfigSwitched, providerTestBusyID, selectedAgentAPIProviderID,
      selectedAgentConfigID, setAgentAPIProviders, setAgentConfigBackups, setAgentConfigBusy,
      setAgentConfigConfirmMessage, setAgentConfigError, setAgentConfigNotice, setAgentConfigStep,
      setAgentConfigSwitchSelection, setProviderSyncBusy, setProviderTestBusyID, setProviderTestModels,
      setProviderTestResults, setSelectedAgentAPIProviderID, setSelectedAgentConfigID, t,
    });

  const childKeyFor = (entry: FileEntry, entryRoot: string, sectionNodeId: string) =>
    childEntryKey(entry, entryRoot, sectionNodeId, treeKey);

  const visibleEntries = React.useCallback((items: FileEntry[], depth = 0) =>
    filterVisibleEntries(items, depth, projectTreeTab, rootId, showHiddenFiles),
    [projectTreeTab, rootId, showHiddenFiles],
  );

  const renderEntries = createRenderEntries({
    ChevronRight, DirectoryIconSlot, PROJECT_TREE_INDENT, PROJECT_TREE_ROOT_PADDING_LEFT,
    activeSessionKey, childKeyFor, childrenByPath, createInputRef, creatingRootBusy, creatingRootExtraContent,
    creatingRootName, creatingRootSubmitOnBlur, expandedSet, fileMetas, hexToRgbaApp, onCreateRootCancel,
    onCreateRootNameChange, onCreateRootSubmit, onSelectDir, onSelectFile, onSelectRoot, onToggleDir, projectTreeTab,
    renderRootExtraContent, renderRootRelatedContent, renderRootWorktreeContent, rootBadgeStyle, rootId, rootNodeId,
    rootSessionIndicators, scopeKey, selectedDirKey, selectedPath, sortDirectoryEntries, sortMode, t, treeKey, visibleEntries,
  });

  return (
    <div style={{ flex: 1, minHeight: 0, height: "100%", display: "flex", flexDirection: "column" }}>
      <ViewHeader
        padding="0 3px"
        showToggles={false}
        style={{ position: "relative", justifyContent: "space-between", gap: 0, overflow: "visible" }}
        innerStyle={{ justifyContent: "space-between", gap: 0 }}
      >
        <div style={{ display: "flex", alignItems: "center", minWidth: 0, flex: "1 1 auto", maxWidth: "calc(100% - 56px)", marginRight: "6px" }}>
          <FileTreeProjectTabs projectTreeTab={projectTreeTab} setProjectTreeTab={setProjectTreeTab} t={t} accentColor={projectTabAccent} hexToRgbaApp={hexToRgbaApp} />
        </div>
        <FileTreeRefreshButton handleRefreshClick={handleRefreshClick} setTreePressed={setTreePressed}
          t={t} treePressed={treePressed} treeRefreshing={treeRefreshing} />
        <div ref={menuRef} style={{ position: "relative", flexShrink: 0 }}>
          <button
            type="button"
            data-onboarding="sidebar-menu"
            onClick={() => {
              setIsMenuOpen((open) => {
                const nextOpen = !open;
                if (nextOpen) {
                  setIsAppearanceMenuOpen(false);
                  setIsFontSizeMenuOpen(false);
                  setIsLocaleMenuOpen(false);
                  setIsSortMenuOpen(false);
                  setSendShortcutOpen(false);
                }
                return nextOpen;
              });
            }}
            aria-label={t("fileTree.menu.open")}
            style={{
              width: "28px",
              height: "28px",
              borderRadius: "8px",
              border: "none",
              background: isMenuOpen ? "rgba(0, 0, 0, 0.06)" : "transparent",
              color: "var(--text-secondary)",
              display: "inline-flex",
              alignItems: "center",
              justifyContent: "center",
              cursor: "pointer",
              outline: "none",
            }}
          >
            <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
              <circle cx="12" cy="5" r="1.8" />
              <circle cx="12" cy="12" r="1.8" />
              <circle cx="12" cy="19" r="1.8" />
            </svg>
          </button>
          {isMenuOpen ? (
            <div
              style={{
                position: "absolute",
                top: "calc(100% + 6px)",
                right: 0,
                width: "var(--mindfs-file-menu-width, 182px)",
                maxWidth: "calc(100vw - 16px)",
                padding: "6px",
                borderRadius: "10px",
                border: "1px solid var(--border-color)",
                background: "var(--menu-bg)",
                boxShadow: "0 12px 30px rgba(15, 23, 42, 0.14)",
                zIndex: 20,
              }}
            >
                <button
                  type="button"
                  onClick={() => {
                    if (onOpenProjectAdd) {
                      onOpenProjectAdd();
                    } else {
                      onCreateRootStart?.();
                    }
                    setIsMenuOpen(false);
                    setIsAppearanceMenuOpen(false);
                    setIsLocaleMenuOpen(false);
                    setIsSortMenuOpen(false);
                  }}
                  style={{
                    width: "100%",
                    border: "none",
                    background: "transparent",
                    color: "var(--text-primary)",
                    borderRadius: "8px",
                    padding: "8px 10px",
                    display: "flex",
                    alignItems: "center",
                    gap: "8px",
                    textAlign: "left",
                    cursor: "pointer",
                    fontSize: "12px",
                  }}
                >
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round">
                    <path d="M12 5v14" />
                    <path d="M5 12h14" />
                  </svg>
                  <span>{t("fileTree.addProject")}</span>
                </button>
                <div style={{ height: "1px", background: "var(--border-color)", margin: "6px 4px" }} />
                <button type="button" onClick={() => { setIsMenuOpen(false); setNodeManagerOpen(true); }} style={fileTreeMenuButtonStyle}>
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round">
                    <rect x="3" y="3" width="18" height="7" rx="2" />
                    <rect x="3" y="14" width="18" height="7" rx="2" />
                    <path d="M7 6.5h.01" />
                    <path d="M7 17.5h.01" />
                  </svg>
                  <span>{t("nodeManager.title")}</span>
                </button>
                <button type="button" ref={accountButtonRef} onClick={() => { setIsMenuOpen(false); setAccountPanelOpen(true); }} style={fileTreeMenuButtonStyle}>
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round">
                    <path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2" />
                    <circle cx="12" cy="7" r="4" />
                  </svg>
                  <span>{t("account.title")}</span>
                </button>
                <div style={{ height: "1px", background: "var(--border-color)", margin: "6px 4px" }} />
                <button
                  type="button"
                  onClick={() => openAgentConfigFlow("backup")}
                  style={fileTreeMenuButtonStyle}
                >
                  <ConfigArchiveIcon />
                  <span>{t("fileTree.addAgentConfig")}</span>
                </button>
                <button
                  type="button"
                  onClick={() => openAgentConfigFlow("switch")}
                  style={fileTreeMenuButtonStyle}
                >
                  <ConfigSwitchIcon />
                  <span>{t("fileTree.switchAgentConfig")}</span>
                </button>
                <button
                  type="button"
                  onClick={openAgentLifecycleFlow}
                  style={fileTreeMenuButtonStyle}
                >
                  <AgentInstallIcon />
                  <span>{t("fileTree.agentInstallUpdate")}</span>
                </button>
                <button
                  type="button"
                  onClick={openSessionNaming}
                  style={fileTreeMenuButtonStyle}
                >
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                    <path d="M4 6h16" />
                    <path d="M4 12h10" />
                    <path d="M4 18h7" />
                    <path d="m17 16 2 2 3-4" />
                  </svg>
                  <span>{t("fileTree.sessionNamingAgent")}</span>
                </button>
                <button
                  type="button"
                  onClick={openIdleSessionResourceRelease}
                  style={fileTreeMenuButtonStyle}
                >
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                    <circle cx="12" cy="12" r="9" />
                    <path d="M12 7v5l3 2" />
                  </svg>
                  <span>{t("fileTree.idleSessionResourceRelease")}</span>
                  <span
                    style={{
                      marginLeft: "auto",
                      color: "var(--text-secondary)",
                      fontSize: "11px",
                      fontVariantNumeric: "tabular-nums",
                    }}
                  >
                    {idleReleaseHours || "72"}h
                  </span>
                </button>
                {!isNativeApp ? <WebPushMenuItem /> : null}
                <div style={{ height: "1px", background: "var(--border-color)", margin: "6px 4px" }} />
                <FileTreeSettingsMenu
                  t={t} locale={locale} setLocale={setLocale}
                  appearanceMode={appearanceMode} changeAppearanceMode={changeAppearanceMode}
                  isMenuOpen={isMenuOpen} setIsMenuOpen={setIsMenuOpen}
                  isAppearanceMenuOpen={isAppearanceMenuOpen} setIsAppearanceMenuOpen={setIsAppearanceMenuOpen}
                  isFontSizeMenuOpen={isFontSizeMenuOpen} setIsFontSizeMenuOpen={setIsFontSizeMenuOpen}
                  isLocaleMenuOpen={isLocaleMenuOpen} setIsLocaleMenuOpen={setIsLocaleMenuOpen}
                  isSortMenuOpen={isSortMenuOpen} setIsSortMenuOpen={setIsSortMenuOpen}
                  sortMode={sortMode} onSortModeChange={onSortModeChange}
                  fontSizePreferences={fontSizePreferences} onFontSizePreferencesChange={onFontSizePreferencesChange}
                  gitDiffSideBySide={gitDiffSideBySide} onGitDiffSideBySideChange={onGitDiffSideBySideChange}
                  showEnterKeySendOption={showEnterKeySendOption} enterKeySends={enterKeySends}
                  onEnterKeySendsChange={onEnterKeySendsChange}
                  showSendShortcutOption={showSendShortcutOption} sendShortcut={sendShortcut}
                  openSendShortcut={openSendShortcut}
                  newProjectMetaLocation={newProjectMetaLocation}
                  newProjectMetaLocationBusy={newProjectMetaLocationBusy}
                  toggleNewProjectMetaLocation={toggleNewProjectMetaLocation}
                />
            </div>
          ) : null}
          {projectAddOverlay ? (
            <div
              style={{
                position: "absolute",
                top: "calc(100% + 6px)",
                right: 0,
                zIndex: 30,
              }}
            >
              {projectAddOverlay}
            </div>
          ) : null}
        </div>
        {nodeManagerOpen ? (
          <NodeManagerPanel
            onClose={() => setNodeManagerOpen(false)}
            onRefreshAll={onNodeManagerRefresh}
          />
        ) : null}
        {accountPanelOpen ? (
          <AccountPanel onClose={() => setAccountPanelOpen(false)} anchorRef={accountButtonRef} />
        ) : null}
        {agentConfigFlow ? (
          <div
            ref={agentConfigPopoverRef}
            style={{
              position: "absolute",
              top: "calc(100% + 6px)",
              left: "8px",
              right: "3px",
              zIndex: 35,
            }}
          >
            <AgentConfigPopover
              flow={agentConfigFlow}
              step={agentConfigStep}
              agents={agentConfigAgents}
              selectedAgent={agentConfigAgent}
              addTab={agentConfigAddTab}
              switchTab={agentConfigSwitchTab}
              backupName={agentConfigName}
              fileSourcesBody={agentConfigFileSourcesBody}
              envBody={agentConfigEnvBody}
              apiProviderName={agentAPIProviderName}
              apiProviderBaseURL={agentAPIProviderBaseURL}
              apiProviderAPIKey={agentAPIProviderAPIKey}
              backups={agentConfigBackups}
              apiProviders={agentAPIProviders}
              selectedBackupID={selectedAgentConfigID}
              selectedAPIProviderID={selectedAgentAPIProviderID}
              confirmMessage={agentConfigConfirmMessage}
              busy={agentConfigBusy}
              restartingAgent={agentConfigRestartingAgent}
              error={agentConfigError}
              notice={agentConfigNotice}
              providerSyncBusy={providerSyncBusy}
              providerTestBusyID={providerTestBusyID}
              providerTestResults={providerTestResults}
              onSyncAllProviders={() => {
                void syncAllAgentAPIProviderModels();
              }}
              onTestProviderModel={(providerID, model) => {
                void testAgentAPIProviderModelNow(providerID, model);
              }}
              onProviderTestModelChange={changeProviderTestModel}
              providerTestModels={providerTestModels}
              onChooseAgent={(name) => {
                void chooseAgentForConfig(name);
              }}
              onAddTabChange={setAgentConfigAddTab}
              onSwitchTabChange={setAgentConfigSwitchTab}
              onBackupNameChange={setAgentConfigName}
              onFileSourcesChange={setAgentConfigFileSourcesBody}
              onEnvBodyChange={setAgentConfigEnvBody}
              onAPIProviderNameChange={setAgentAPIProviderName}
              onAPIProviderBaseURLChange={setAgentAPIProviderBaseURL}
              onAPIProviderAPIKeyChange={setAgentAPIProviderAPIKey}
              onSelectedBackupChange={selectAgentConfigBackup}
              onSelectedAPIProviderChange={selectAgentAPIProvider}
              onDeleteBackup={(id) => {
                void deleteSelectedAgentConfigBackup(id);
              }}
              onDeleteAPIProvider={(id) => {
                void deleteSelectedAgentAPIProvider(id);
              }}
              onSave={() => {
                if (agentConfigAddTab === "api") {
                  void saveAgentAPIProvider();
                  return;
                }
                void saveAgentConfigBackup();
              }}
              onSwitch={() => {
                void runAgentConfigSwitch(false);
              }}
              onRestartAgent={agentConfigFlow === "switch" ? restartAgentFromConfigList : undefined}
              onConfirm={() => {
                if (agentConfigFlow === "backup") {
                  void saveAgentConfigBackup(true);
                  return;
                }
                void runAgentConfigSwitch(true);
              }}
              onCancel={closeAgentConfigFlow}
            />
          </div>
        ) : null}
<FileTreeIdleReleasePopover open={idleReleaseOpen} hours={idleReleaseHours} busy={idleReleaseBusy} error={idleReleaseError} setHours={setIdleReleaseHours} close={() => setIdleReleaseOpen(false)} save={saveIdleSessionResourceRelease} t={t} rowStyle={agentConfigActionRowStyle} secondaryStyle={agentConfigSecondaryButtonStyle} primaryStyle={agentConfigPrimaryButtonStyle} />        {<FileTreeSendShortcutPopover
          sendShortcutOpen={sendShortcutOpen} sendShortcutPopoverRef={sendShortcutPopoverRef} sendShortcutDraft={sendShortcutDraft}
          sendShortcutError={sendShortcutError} setSendShortcutOpen={setSendShortcutOpen} setSendShortcutDraft={setSendShortcutDraft}
          setSendShortcutError={setSendShortcutError} shortcutFromKeyboardEvent={shortcutFromKeyboardEvent}
          formatSendShortcut={formatSendShortcut} saveSendShortcut={saveSendShortcut} t={t}
          agentConfigActionRowStyle={agentConfigActionRowStyle} agentConfigSecondaryButtonStyle={agentConfigSecondaryButtonStyle}
          agentConfigPrimaryButtonStyle={agentConfigPrimaryButtonStyle}
        />}
        {<FileTreeSessionNamingPopover
          sessionNamingOpen={sessionNamingOpen} sessionNamingAgent={sessionNamingAgent} sessionNamingModel={sessionNamingModel}
          sessionNamingAgents={sessionNamingAgents} sessionNamingBusy={sessionNamingBusy} sessionNamingDisabled={sessionNamingDisabled}
          sessionNamingError={sessionNamingError} setSessionNamingAgent={setSessionNamingAgent} setSessionNamingModel={setSessionNamingModel}
          setSessionNamingDisabled={setSessionNamingDisabled} setSessionNamingOpen={setSessionNamingOpen} saveSessionNaming={saveSessionNaming}
          t={t} AgentSelector={AgentSelector} agentConfigActionRowStyle={agentConfigActionRowStyle}
          agentConfigSecondaryButtonStyle={agentConfigSecondaryButtonStyle} agentConfigPrimaryButtonStyle={agentConfigPrimaryButtonStyle}
        />}
        {agentLifecycleOpen ? (
          <div
            ref={agentLifecyclePopoverRef}
            style={{
              position: "absolute",
              top: "calc(100% + 6px)",
              left: "8px",
              right: "3px",
              zIndex: 35,
            }}
          >
            <AgentLifecyclePopover
              agents={agentLifecycleAgents}
              busy={agentLifecycleBusy}
              runningAgent={agentLifecycleRunningAgent}
              error={agentLifecycleError}
              onRun={(agent, action) => {
                void runAgentLifecycleCommand(agent, action);
              }}
            />
          </div>
        ) : null}
      </ViewHeader>
      <div style={{ padding: "8px", flex: 1, minHeight: 0, overflow: "auto", display: "flex", flexDirection: "column" }}>
        <FileTreeProjectContent groups={nodeGroups} creatingRootName={creatingRootName} t={t} renderEntries={renderEntries} rootId={rootId} />
      </div>
<FileTreeFooter
        {...{ hasFooterContent, updateNotesRef, updateActionLabel, updateActionSummary, updateActionDisabled, updateActionBusy, updateActionHelp, isUpdateNotesOpen, setIsUpdateNotesOpen, onUpdateAction, footerTopContent, isNativeApp, onGoHome, shouldShowInstallButton, handleInstall, installLabel, shouldShowInstallHelp, installHelp, t }}
      />      <style>{`
        @keyframes mindfs-update-spin { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }
        @keyframes mindfs-bound-pulse {
          0%, 100% { opacity: 1; box-shadow: 0 0 0 1.5px rgba(37,99,235,0.14); }
          50% { opacity: 0.18; box-shadow: 0 0 0 4px rgba(37,99,235,0.08); }
        }
      `}</style>
    </div>
  );
}

export const FileTree = memo(FileTreeInner);
