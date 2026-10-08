import React from "react";
import {
  createAgentAPIProvider, createAgentConfigBackup, deleteAgentAPIProvider, deleteAgentConfigBackup,
  switchAgentAPIProvider, switchAgentConfig, syncAllAgentAPIProviders, testAgentAPIProviderModel,
} from "../../services/agentConfig";

export function useAgentConfigActions(ctx: any) {
  const { agentAPIProviderAPIKey, agentAPIProviderBaseURL, agentAPIProviderName, agentAPIProviders, agentConfigAgent, agentConfigBackups, agentConfigEnvBody, agentConfigFileSourcesBody, agentConfigFlowVersion, agentConfigName, agentConfigSwitchSelection, closeAgentConfigFlow, formatProviderTestResult, isAgentConfigBackupConflict, onAgentConfigSwitched, providerTestBusyID, selectedAgentAPIProviderID, selectedAgentConfigID, setAgentAPIProviders, setAgentConfigBackups, setAgentConfigBusy, setAgentConfigConfirmMessage, setAgentConfigError, setAgentConfigNotice, setAgentConfigStep, setAgentConfigSwitchSelection, setProviderSyncBusy, setProviderTestBusyID, setProviderTestModels, setProviderTestResults, setSelectedAgentAPIProviderID, setSelectedAgentConfigID, t } = ctx;
const saveAgentConfigBackup = React.useCallback(async (overwrite = false) => {
  if (!agentConfigName.trim()) {
    setAgentConfigError(t("agentConfig.backupNameRequired"));
    return;
  }
  const fileSources = agentConfigFileSourcesBody.split(/\r?\n/).map((line: string) => line.trim()).filter(Boolean);
  const envLines = agentConfigEnvBody.split(/\r?\n/).map((line: string) => line.trim()).filter(Boolean);
  setAgentConfigBusy(true);
  setAgentConfigError("");
  try {
    await createAgentConfigBackup({
      agent: agentConfigAgent,
      name: agentConfigName.trim(),
      fileSources,
      envLines,
      overwrite,
    });
    closeAgentConfigFlow();
  } catch (error) {
    if (isAgentConfigBackupConflict(error) && !overwrite) {
      setAgentConfigConfirmMessage(t("agentConfig.backupExistsOverwrite"));
      setAgentConfigStep("confirm");
      return;
    }
    setAgentConfigError(error instanceof Error ? error.message : t("agentConfig.saveBackupFailed"));
  } finally {
    setAgentConfigBusy(false);
  }
}, [agentConfigAgent, agentConfigEnvBody, agentConfigFileSourcesBody, agentConfigName, closeAgentConfigFlow, t]);

const saveAgentAPIProvider = React.useCallback(async () => {
  if (!agentAPIProviderName.trim()) {
    setAgentConfigError(t("agentConfig.providerNameRequired"));
    return;
  }
  if (!agentAPIProviderBaseURL.trim()) {
    setAgentConfigError(t("agentConfig.baseURLRequired"));
    return;
  }
  if (!agentAPIProviderAPIKey.trim()) {
    setAgentConfigError(t("agentConfig.apiKeyRequired"));
    return;
  }
  setAgentConfigBusy(true);
  setAgentConfigError("");
  try {
    await createAgentAPIProvider({
      name: agentAPIProviderName.trim(),
      baseUrl: agentAPIProviderBaseURL.trim(),
      apiKey: agentAPIProviderAPIKey.trim(),
    });
    closeAgentConfigFlow();
  } catch (error) {
    setAgentConfigError(error instanceof Error ? error.message : t("agentConfig.saveProviderFailed"));
  } finally {
    setAgentConfigBusy(false);
  }
}, [agentAPIProviderAPIKey, agentAPIProviderBaseURL, agentAPIProviderName, closeAgentConfigFlow, t]);

const runAgentConfigSwitch = React.useCallback(async (confirmOverwrite = false) => {
  if (!agentConfigSwitchSelection) {
    setAgentConfigError(t("agentConfig.selectConfig"));
    return;
  }
  setAgentConfigBusy(true);
  setAgentConfigError("");
  try {
    if (agentConfigSwitchSelection.type === "api_provider") {
      await switchAgentAPIProvider({ agent: agentConfigAgent, providerID: agentConfigSwitchSelection.id });
      onAgentConfigSwitched?.(agentConfigAgent);
      closeAgentConfigFlow();
      return;
    }
    const result = await switchAgentConfig({ id: agentConfigSwitchSelection.id, confirmOverwrite });
    if (result.needs_confirm) {
      setAgentConfigConfirmMessage(result.message || t("agentConfig.targetExists"));
      setAgentConfigStep("confirm");
      return;
    }
    onAgentConfigSwitched?.(agentConfigAgent);
    closeAgentConfigFlow();
  } catch (error) {
    setAgentConfigError(error instanceof Error ? error.message : t("agentConfig.switchFailed"));
  } finally {
    setAgentConfigBusy(false);
  }
}, [agentConfigAgent, agentConfigSwitchSelection, closeAgentConfigFlow, onAgentConfigSwitched, t]);

const deleteSelectedAgentConfigBackup = React.useCallback(async (id: string) => {
  const trimmedID = String(id || "").trim();
  if (!trimmedID) {
    return;
  }
  setAgentConfigBusy(true);
  setAgentConfigError("");
  try {
    const result = await deleteAgentConfigBackup(trimmedID);
    const nextBackups = (result.backups || agentConfigBackups)
      .filter((item: any) => item.agent === agentConfigAgent && item.id !== trimmedID);
    setAgentConfigBackups(nextBackups);
    if (selectedAgentConfigID === trimmedID) {
      setSelectedAgentConfigID("");
      setAgentConfigSwitchSelection(null);
    }
  } catch (error) {
    setAgentConfigError(error instanceof Error ? error.message : t("agentConfig.deleteConfigFailed"));
  } finally {
    setAgentConfigBusy(false);
  }
}, [agentConfigAgent, agentConfigBackups, selectedAgentConfigID, t]);

const deleteSelectedAgentAPIProvider = React.useCallback(async (id: string) => {
  const trimmedID = String(id || "").trim();
  if (!trimmedID) {
    return;
  }
  setAgentConfigBusy(true);
  setAgentConfigError("");
  try {
    const result = await deleteAgentAPIProvider(trimmedID);
    const nextProviders = (result.providers || agentAPIProviders)
      .filter((item: any) => item.id !== trimmedID);
    setAgentAPIProviders(nextProviders);
    if (selectedAgentAPIProviderID === trimmedID) {
      setSelectedAgentAPIProviderID("");
      setAgentConfigSwitchSelection(null);
    }
  } catch (error) {
    setAgentConfigError(error instanceof Error ? error.message : t("agentConfig.deleteProviderFailed"));
  } finally {
    setAgentConfigBusy(false);
  }
}, [agentAPIProviders, selectedAgentAPIProviderID, t]);

const syncAllAgentAPIProviderModels = React.useCallback(async () => {
  const flowVersion = agentConfigFlowVersion.current;
  setProviderSyncBusy(true);
  setAgentConfigError("");
  setAgentConfigNotice("");
  try {
    const result = await syncAllAgentAPIProviders();
    if (flowVersion !== agentConfigFlowVersion.current) return;
    const providers = result.providers || [];
    setAgentAPIProviders(providers);
    const results = result.results || [];
    const failed = results.filter((item: any) => !item.success);
    const succeeded = results.filter((item: any) => item.success);
    if (results.length === 0) {
      setAgentConfigNotice(t("agentConfig.syncProvidersEmpty"));
      return;
    }
    const summary = t("agentConfig.syncProvidersResult", {
      success: succeeded.length,
      failed: failed.length,
    });
    const sections = [summary];
    if (failed.length > 0) {
      const detail = failed
        .map((item: any) => `${item.name}: ${item.error || ""}`.trim())
        .join("\n");
      sections.push(detail);
    }
    const appliedLines: string[] = [];
    for (const item of results) {
      for (const applied of item.applied || []) {
        appliedLines.push(
          applied.success
            ? t("agentConfig.syncApplySuccess", { agent: applied.agent })
            : t("agentConfig.syncApplyFailed", {
                agent: applied.agent,
                error: applied.error || "",
              }),
        );
      }
    }
    if (appliedLines.length > 0) {
      sections.push(appliedLines.join("\n"));
    }
    setAgentConfigNotice(sections.join("\n"));
  } catch (error) {
    if (flowVersion !== agentConfigFlowVersion.current) return;
    // 同步失败时后端保留旧模型列表，这里提示错误即可。
    setAgentConfigError(error instanceof Error ? error.message : t("agentConfig.syncProvidersFailed"));
  } finally {
    setProviderSyncBusy(false);
  }
}, [t]);

const testAgentAPIProviderModelNow = React.useCallback(async (providerID: string, model: string) => {
  const trimmedProviderID = String(providerID || "").trim();
  if (!trimmedProviderID || providerTestBusyID) {
    return;
  }
  setProviderTestBusyID(trimmedProviderID);
  setAgentConfigError("");
  try {
    const result = await testAgentAPIProviderModel({
      providerID: trimmedProviderID,
      model: String(model || "").trim(),
    });
    setProviderTestResults((previous: any) => ({
      ...previous,
      [trimmedProviderID]: formatProviderTestResult(result, t),
    }));
  } catch (error) {
    setProviderTestResults((previous: any) => ({
      ...previous,
      [trimmedProviderID]: error instanceof Error ? `✗ ${error.message}` : t("agentConfig.testModelFailed"),
    }));
  } finally {
    setProviderTestBusyID("");
  }
}, [providerTestBusyID, t]);

const selectAgentConfigBackup = React.useCallback((id: string) => {
  setSelectedAgentConfigID(id);
  setSelectedAgentAPIProviderID("");
  setAgentConfigSwitchSelection(id ? { type: "backup", id } : null);
}, []);

const selectAgentAPIProvider = React.useCallback((id: string) => {
  setSelectedAgentAPIProviderID(id);
  setSelectedAgentConfigID("");
  setAgentConfigSwitchSelection(id ? { type: "api_provider", id } : null);
}, []);

const changeProviderTestModel = React.useCallback((providerID: string, model: string) => {
  const trimmedID = String(providerID || "").trim();
  if (!trimmedID) {
    return;
  }
  setProviderTestModels((previous: any) => ({ ...previous, [trimmedID]: model }));
}, []);

  return { saveAgentConfigBackup, saveAgentAPIProvider, runAgentConfigSwitch, deleteSelectedAgentConfigBackup,
    deleteSelectedAgentAPIProvider, syncAllAgentAPIProviderModels, testAgentAPIProviderModelNow,
    selectAgentConfigBackup, selectAgentAPIProvider, changeProviderTestModel };
}
