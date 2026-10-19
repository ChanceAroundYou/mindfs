import React, { memo } from "react";
import { rootBadgeStyle } from "../../shared/rootBadgeStyle";
import { NodeBadgeHeader } from "../account/NodeBadgeHeader";
import { ViewHeader } from "../../layout/ViewHeader";
import { hexToRgba as hexToRgbaApp } from "../../shared/hexToRgba";
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
  type AgentConfigSwitchSelection,
  type FileTreeProps,
  type ProjectTreeTab,
} from "./fileTreeTypes";
import { createRenderEntries } from "./fileTreeEntries";
import { childEntryKey, filterVisibleEntries } from "./FileTreeEntryFilters";
import { useAgentConfigActions } from "./useAgentConfigActions";
import { useAgentConfigFlowTransitions } from "./useAgentConfigFlowTransitions";
import { FileTreeHeaderOverlays } from "./FileTreeHeaderOverlays";
import { AgentSelector } from "../agent/AgentSelector";
import { AgentConfigPopover, AgentLifecyclePopover, agentConfigActionRowStyle, agentConfigPrimaryButtonStyle, agentConfigSecondaryButtonStyle, type AgentConfigFlow, type AgentConfigStep, type AgentConfigAddTab, type AgentConfigSwitchTab } from "./AgentConfigPopover";
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
import { FileTreeProjectContent } from "./FileTreeProjectContent";
import { FileTreeProjectTabs } from "./FileTreeProjectTabs";
import { scopeKey, treeKey } from "../../shared/scope";
import { SymlinkBadge } from "./SymlinkBadge";
import { AgentConfigLineEditor } from "./AgentConfigLineEditor";
import type { AgentAPIProvider, AgentConfigBackup, AgentStatus } from "../../shared/agentTypes";
import type { NewProjectMetaLocation } from "../../shared/preferencesTypes";
import { formatSendShortcut, shortcutFromKeyboardEvent, type SendShortcut } from "../../shared/sendShortcut";

/** 把单模型测试结果格式化成一行简短文案（成功/延迟/错误/简短响应）。 */

import {
  NotificationIcon,
  InfoIcon,
  ChevronRight,
  DirectoryIconSlot,
  getFileIcon,
  OnboardingGuideIcon,
  fileTreeMenuButtonStyle,
  WebPushMenuItem,
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
  agentConfigSwitchRequest = null,
  onAgentConfigSwitched,
  projectTreeTab = "files",
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
  // 页签是**受控**的：唯一真相在 App 层（useAppFileWorkspaceState，默认 "files"）。
  // 原先这里自己读 localStorage 初始化，而那个键**从来没人写** —— 侧边栏于是永远
  // 停在一个陈旧的 "git" 上（用户实测「打开文件树就跳到 git 页」）。
  const handleTabChange = React.useCallback(
    (tab: ProjectTreeTab) => {
      onProjectTreeTabChange?.(tab);
    },
    [onProjectTreeTabChange],
  );
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
          <FileTreeProjectTabs projectTreeTab={projectTreeTab} setProjectTreeTab={handleTabChange} t={t} accentColor={projectTabAccent} hexToRgbaApp={hexToRgbaApp} />
        </div>
        <FileTreeRefreshButton handleRefreshClick={handleRefreshClick} setTreePressed={setTreePressed}
          t={t} treePressed={treePressed} treeRefreshing={treeRefreshing} />
        <FileTreeHeaderOverlays
          ctx={{
            t,
            menuRef,
            isMenuOpen,
            setIsMenuOpen,
            setIsAppearanceMenuOpen,
            setIsFontSizeMenuOpen,
            setIsLocaleMenuOpen,
            setIsSortMenuOpen,
            setSendShortcutOpen,
            onOpenProjectAdd,
            onCreateRootStart,
            setNodeManagerOpen,
            fileTreeMenuButtonStyle,
            accountButtonRef,
            setAccountPanelOpen,
            openAgentConfigFlow,
            openAgentLifecycleFlow,
            openSessionNaming,
            openIdleSessionResourceRelease,
            isNativeApp,
            WebPushMenuItem,
            locale,
            setLocale,
            appearanceMode,
            changeAppearanceMode,
            isAppearanceMenuOpen,
            isFontSizeMenuOpen,
            isLocaleMenuOpen,
            isSortMenuOpen,
            sortMode,
            onSortModeChange,
            fontSizePreferences,
            onFontSizePreferencesChange,
            gitDiffSideBySide,
            onGitDiffSideBySideChange,
            showEnterKeySendOption,
            enterKeySends,
            onEnterKeySendsChange,
            showSendShortcutOption,
            sendShortcut,
            openSendShortcut,
            newProjectMetaLocation,
            newProjectMetaLocationBusy,
            toggleNewProjectMetaLocation,
            projectAddOverlay,
            nodeManagerOpen,
            onNodeManagerRefresh,
            accountPanelOpen,
            agentConfigFlow,
            agentConfigPopoverRef,
            agentConfigStep,
            agentConfigAgents,
            agentConfigAgent,
            agentConfigAddTab,
            agentConfigSwitchTab,
            agentConfigName,
            agentConfigFileSourcesBody,
            agentConfigEnvBody,
            agentAPIProviderName,
            agentAPIProviderBaseURL,
            agentAPIProviderAPIKey,
            agentConfigBackups,
            agentAPIProviders,
            selectedAgentConfigID,
            selectedAgentAPIProviderID,
            agentConfigConfirmMessage,
            agentConfigBusy,
            agentConfigRestartingAgent,
            agentConfigError,
            agentConfigNotice,
            providerSyncBusy,
            providerTestBusyID,
            providerTestResults,
            syncAllAgentAPIProviderModels,
            testAgentAPIProviderModelNow,
            changeProviderTestModel,
            providerTestModels,
            chooseAgentForConfig,
            setAgentConfigAddTab,
            setAgentConfigSwitchTab,
            setAgentConfigName,
            setAgentConfigFileSourcesBody,
            setAgentConfigEnvBody,
            setAgentAPIProviderName,
            setAgentAPIProviderBaseURL,
            setAgentAPIProviderAPIKey,
            selectAgentConfigBackup,
            selectAgentAPIProvider,
            deleteSelectedAgentConfigBackup,
            deleteSelectedAgentAPIProvider,
            saveAgentAPIProvider,
            saveAgentConfigBackup,
            runAgentConfigSwitch,
            restartAgentFromConfigList,
            closeAgentConfigFlow,
            idleReleaseOpen,
            idleReleaseHours,
            setIdleReleaseHours,
            idleReleaseBusy,
            idleReleaseError,
            setIdleReleaseOpen,
            saveIdleSessionResourceRelease,
            agentConfigActionRowStyle,
            agentConfigSecondaryButtonStyle,
            agentConfigPrimaryButtonStyle,
            sendShortcutOpen,
            sendShortcutPopoverRef,
            sendShortcutDraft,
            sendShortcutError,
            saveSendShortcut,
            setSendShortcutDraft,
            setSendShortcutError,
            shortcutFromKeyboardEvent,
            formatSendShortcut,
            sessionNamingOpen,
            sessionNamingAgent,
            sessionNamingModel,
            sessionNamingAgents,
            sessionNamingBusy,
            sessionNamingDisabled,
            sessionNamingError,
            setSessionNamingAgent,
            setSessionNamingModel,
            setSessionNamingDisabled,
            setSessionNamingOpen,
            saveSessionNaming,
            AgentSelector,
            agentLifecycleOpen,
            agentLifecyclePopoverRef,
            agentLifecycleAgents,
            agentLifecycleBusy,
            agentLifecycleRunningAgent,
            agentLifecycleError,
            runAgentLifecycleCommand
          }}
        />
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
        @keyframes mindfs-hollow-pulse {
          0%, 100% { opacity: 1; }
          50% { opacity: 0.35; }
        }
      `}</style>
    </div>
  );
}

export const FileTree = memo(FileTreeInner);
