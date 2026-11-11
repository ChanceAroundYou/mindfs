import React from "react";
import { fetchAgentConfigBackups, fetchAgentConfigDefaults, fetchAgentAPIProviders, fetchAgentConfigSyncStatus, fetchAgentCustomModels, saveAgentCustomModels } from "../../services/agentConfig";
import { getRootNodeId } from "../../services/net/rootNode";
import { fetchAgents } from "../../services/agents";
import { isCodexProfileAgent, switchableAgentConfigBackups } from "./agentConfigProfiles";

export function useAgentConfigSelection(ctx: any) {
  const { agentConfigRestartingAgent, onRestartAgent, t, setAgentConfigRestartingAgent, setAgentConfigError, agentConfigAgent, setAgentConfigAgent, setAgentConfigBusy, agentConfigAgents, agentConfigFlow, setAgentConfigName, setAgentConfigFileSourcesBody, setAgentConfigEnvBody, setAgentAPIProviderName, setAgentAPIProviderBaseURL, setAgentAPIProviderAPIKey, setAgentConfigDetailTab, setAgentConfigStep, setAgentConfigBackups, setAgentAPIProviders, setAgentConfigSyncStatus, setSelectedAgentConfigID, agentConfigPreferredProviderIDs, setSelectedAgentAPIProviderID, setAgentConfigSwitchSelection } = ctx;
const [agentCustomModels, setAgentCustomModels] = React.useState<string[]>([]);
const [agentCustomModelsBusy, setAgentCustomModelsBusy] = React.useState(false);
const restartAgentFromConfigList = React.useCallback(async (agentName: string) => {
  if (!agentName || !onRestartAgent || agentConfigRestartingAgent) {
    return;
  }
  setAgentConfigRestartingAgent(agentName);
  setAgentConfigError("");
  try {
    await onRestartAgent(agentName);
  } catch (error) {
    setAgentConfigError(error instanceof Error ? error.message : t("agentConfig.restartFailed"));
  } finally {
    setAgentConfigRestartingAgent("");
  }
}, [agentConfigRestartingAgent, onRestartAgent, t]);

const { agentConfigSyncStatus } = ctx;
React.useEffect(() => {
  if (agentConfigSyncStatus?.state !== "syncing") {
    return;
  }
  let active = true;
  let timer: ReturnType<typeof setTimeout>;
  const poll = async () => {
    try {
      const status = await fetchAgentConfigSyncStatus();
      if (!active) {
        return;
      }
      setAgentConfigSyncStatus(status);
      if (status.state === "syncing") {
        timer = setTimeout(poll, 1000);
      }
    } catch {
      if (active) {
        timer = setTimeout(poll, 2000);
      }
    }
  };
  timer = setTimeout(poll, 800);
  return () => {
    active = false;
    clearTimeout(timer);
  };
}, [agentConfigSyncStatus?.state, setAgentConfigSyncStatus]);

const chooseAgentForConfig = React.useCallback(async (agentName: string) => {
  setAgentConfigAgent(agentName);
  setAgentConfigError("");
  setAgentConfigBusy(true);
  try {
    const selectedAgent = agentConfigAgents.find((item: any) => item.name === agentName);
    const isCodex = isCodexProfileAgent(agentName, selectedAgent);
    if (agentConfigFlow === "provider") {
      const [providers, customModels] = await Promise.all([
        fetchAgentAPIProviders(agentName),
        fetchAgentCustomModels(agentName),
      ]);
      setAgentAPIProviders(providers);
      setAgentCustomModels(customModels);
      const preferredProvider = providers.find((provider) => agentConfigPreferredProviderIDs.includes(provider.id));
      setSelectedAgentAPIProviderID(preferredProvider?.id || "");
      setAgentConfigSwitchSelection(preferredProvider ? { type: "api_provider", id: preferredProvider.id } : null);
      setAgentConfigStep("details");
    } else {
      setAgentCustomModels([]);
      const [backups, syncStatus] = await Promise.all([
        fetchAgentConfigBackups(agentName),
        fetchAgentConfigSyncStatus(),
      ]);
      const switchableBackups = switchableAgentConfigBackups(agentName, selectedAgent, backups);
      setAgentConfigBackups(switchableBackups);
      setAgentConfigSyncStatus(syncStatus);
      const lastSelection = selectedAgent?.last_config_selection;
      const lastSharedBackup = isCodex && lastSelection?.type === "backup"
        ? switchableBackups.find((item: any) => item.id === lastSelection.id)
        : null;
      const defaultSharedBackup = isCodex
        ? switchableBackups.find((item: any) => item.id === "shared:codex-default") || switchableBackups[0]
        : null;
      const initialBackupID = lastSharedBackup?.id || defaultSharedBackup?.id || "";
      setSelectedAgentConfigID(initialBackupID);
      setSelectedAgentAPIProviderID("");
      setAgentConfigSwitchSelection(initialBackupID ? { type: "backup", id: initialBackupID } : null);
      setAgentConfigDetailTab("switch");
      setAgentConfigStep("details");
    }
  } catch (error) {
    setAgentConfigError(error instanceof Error ? error.message : t("agentConfig.loadConfigFailed"));
  } finally {
    setAgentConfigBusy(false);
  }
}, [agentConfigAgents, agentConfigFlow, agentConfigPreferredProviderIDs, t]);

  // 「备份」页签第一次打开时按 agent 默认值预填来源与环境变量；已有输入不覆盖。
const loadAgentConfigBackupDefaults = React.useCallback(async (agentName: string) => {
  if (!agentName) {
    return;
  }
  try {
    const defaults = await fetchAgentConfigDefaults(agentName);
    setAgentConfigFileSourcesBody((previous: string) => previous || (defaults.file_sources || []).join("\n"));
    setAgentConfigEnvBody((previous: string) => previous || (defaults.env_keys || []).map((key: string) => `${key}=`).join("\n"));
  } catch (error) {
    setAgentConfigError(error instanceof Error ? error.message : t("agentConfig.loadConfigFailed"));
  }
}, [t]);

  const addAgentCustomModel = React.useCallback(async (model: string) => {
    const trimmed = String(model || "").trim();
    if (!agentConfigAgent || !trimmed || agentCustomModels.includes(trimmed)) {
      return;
    }
    setAgentCustomModelsBusy(true);
    setAgentConfigError("");
    try {
      setAgentCustomModels(await saveAgentCustomModels(agentConfigAgent, [...agentCustomModels, trimmed]));
    } catch (error) {
      setAgentConfigError(error instanceof Error ? error.message : t("agentConfig.customModelSaveFailed"));
    } finally {
      setAgentCustomModelsBusy(false);
    }
  }, [agentConfigAgent, agentCustomModels, t]);

  const removeAgentCustomModel = React.useCallback(async (model: string) => {
    const trimmed = String(model || "").trim();
    if (!agentConfigAgent || !trimmed) {
      return;
    }
    setAgentCustomModelsBusy(true);
    setAgentConfigError("");
    try {
      setAgentCustomModels(await saveAgentCustomModels(agentConfigAgent, agentCustomModels.filter((item: string) => item !== trimmed)));
    } catch (error) {
      setAgentConfigError(error instanceof Error ? error.message : t("agentConfig.customModelSaveFailed"));
    } finally {
      setAgentCustomModelsBusy(false);
    }
  }, [agentConfigAgent, agentCustomModels, t]);

  return {
    restartAgentFromConfigList,
    chooseAgentForConfig,
    loadAgentConfigBackupDefaults,
    agentCustomModelsState: {
      models: agentCustomModels,
      busy: agentCustomModelsBusy,
      add: addAgentCustomModel,
      remove: removeAgentCustomModel,
    },
  };
}
