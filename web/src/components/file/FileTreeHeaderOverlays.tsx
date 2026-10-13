import { AgentSelector } from "../agent/AgentSelector";
import { AccountPanel } from "../account/AccountPanel";
import { NodeManagerPanel } from "../account/NodeManagerPanel";
import {
  AgentConfigPopover,
  AgentLifecyclePopover,
  agentConfigActionRowStyle,
  agentConfigPrimaryButtonStyle,
  agentConfigSecondaryButtonStyle,
} from "./AgentConfigPopover";
import { FileTreeSettingsMenu } from "./FileTreeSettingsMenu";
import { FileTreeIdleReleasePopover } from "./FileTreeIdleReleasePopover";
import { FileTreeSendShortcutPopover } from "./FileTreeSendShortcutPopover";
import { FileTreeSessionNamingPopover } from "./FileTreeSessionNamingPopover";
import {
  AgentInstallIcon,
  ConfigArchiveIcon,
  ConfigSwitchIcon,
  fileTreeMenuButtonStyle,
  WebPushMenuItem,
} from "./icons";

type FileTreeHeaderOverlaysProps = { ctx: Record<string, any> };

export function FileTreeHeaderOverlays({ ctx }: FileTreeHeaderOverlaysProps) {
  const { t,
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
    runAgentLifecycleCommand } = ctx;
  return <>
        <div ref={menuRef} style={{ position: "relative", flexShrink: 0 }}>
          <button
            type="button"
            data-onboarding="sidebar-menu"
            onClick={() => {
              setIsMenuOpen((open: boolean) => {
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
  </>;
}
