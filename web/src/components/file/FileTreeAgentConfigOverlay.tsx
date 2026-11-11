import { AgentConfigPopover } from "./AgentConfigPopover";

// 「Agent 配置 / 供应商和模型」浮层：从 FileTree 的 ctx 里取数据与动作，
// 单独成文件是为了把 FileTreeHeaderOverlays 的编排体积留在硬线以内。
export function FileTreeAgentConfigOverlay({ ctx }: { ctx: Record<string, any> }) {
  const {
    agentConfigFlow, agentConfigPopoverRef, agentConfigStep, agentConfigAgents, agentConfigAgent,
    agentConfigDetailTab, agentConfigShowProviderForm, agentConfigName, agentConfigFileSourcesBody,
    agentConfigEnvBody, agentAPIProviderName, agentAPIProviderBaseURL, agentAPIProviderAPIKey,
    agentConfigBackups, agentAPIProviders, selectedAgentConfigID, selectedAgentAPIProviderID,
    agentConfigConfirmMessage, agentConfigConfirmAction, agentConfigBusy, agentConfigRestartingAgent,
    agentConfigError, agentConfigNotice, agentConfigSyncStatus, agentConfigSyncBusy, retryAgentConfigSync,
    providerSyncBusy, providerTestBusyID, providerTestResults, syncAllAgentAPIProviderModels,
    testAgentAPIProviderModelNow, changeProviderTestModel, providerTestModels, agentCustomModelsState,
    chooseAgentForConfig, loadAgentConfigBackupDefaults, setAgentConfigDetailTab, setAgentConfigShowProviderForm,
    setAgentConfigName, setAgentConfigFileSourcesBody, setAgentConfigEnvBody, setAgentAPIProviderName,
    setAgentAPIProviderBaseURL, setAgentAPIProviderAPIKey, selectAgentConfigBackup, selectAgentAPIProvider,
    deleteSelectedAgentConfigBackup, deleteSelectedAgentAPIProvider, saveAgentAPIProvider,
    saveAgentConfigBackup, runAgentConfigSwitch, restartAgentFromConfigList, closeAgentConfigFlow,
  } = ctx;
  if (!agentConfigFlow) {
    return null;
  }
  return (
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
        detailTab={agentConfigDetailTab}
        showProviderForm={agentConfigShowProviderForm}
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
        syncStatus={agentConfigSyncStatus}
        syncBusy={agentConfigSyncBusy}
        onRetrySync={() => {
          void retryAgentConfigSync();
        }}
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
        customModels={agentCustomModelsState.models}
        customModelsBusy={agentCustomModelsState.busy}
        onAddCustomModel={(model) => {
          void agentCustomModelsState.add(model);
        }}
        onRemoveCustomModel={(model) => {
          void agentCustomModelsState.remove(model);
        }}
        onChooseAgent={(name) => {
          void chooseAgentForConfig(name);
        }}
        onDetailTabChange={(tab) => {
          setAgentConfigDetailTab(tab);
          if (tab === "backup") {
            void loadAgentConfigBackupDefaults(agentConfigAgent);
          }
        }}
        onShowProviderFormChange={setAgentConfigShowProviderForm}
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
        onSaveBackup={() => {
          void saveAgentConfigBackup();
        }}
        onSaveProvider={() => {
          void saveAgentAPIProvider();
        }}
        onSwitch={() => {
          void runAgentConfigSwitch(false);
        }}
        onRestartAgent={restartAgentFromConfigList}
        onConfirm={() => {
          if (agentConfigConfirmAction === "backup") {
            void saveAgentConfigBackup(true);
            return;
          }
          void runAgentConfigSwitch(true);
        }}
        onCancel={closeAgentConfigFlow}
      />
    </div>
  );
}