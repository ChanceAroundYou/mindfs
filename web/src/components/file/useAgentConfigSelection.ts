import React from "react";
import { fetchAgentConfigBackups, fetchAgentConfigDefaults, fetchAgentAPIProviders, fetchAgentConfigSyncStatus } from "../../services/agentConfig";
import { getRootNodeId } from "../../services/net/rootNode";
import { fetchAgents } from "../../services/agents";

export function useAgentConfigSelection(ctx: any) {
  const { agentConfigRestartingAgent, onRestartAgent, t, setAgentConfigRestartingAgent, setAgentConfigError, agentConfigAgent, setAgentConfigAgent, setAgentConfigBusy, agentConfigAgents, agentConfigFlow, setAgentConfigName, setAgentConfigFileSourcesBody, setAgentConfigEnvBody, setAgentAPIProviderName, setAgentAPIProviderBaseURL, setAgentAPIProviderAPIKey, setAgentConfigAddTab, setAgentConfigStep, setAgentConfigBackups, setAgentAPIProviders, setAgentConfigSyncStatus, setSelectedAgentConfigID, agentConfigPreferredProviderIDs, setSelectedAgentAPIProviderID, setAgentConfigSwitchSelection, setAgentConfigSwitchTab } = ctx;
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
    const supportsAPIProvider = Boolean(selectedAgent?.supports_api_provider_switch);
    if (agentConfigFlow === "backup") {
      const defaults = await fetchAgentConfigDefaults(agentName);
      setAgentConfigName("");
      setAgentConfigFileSourcesBody((defaults.file_sources || []).join("\n"));
      setAgentConfigEnvBody((defaults.env_keys || []).map((key) => `${key}=`).join("\n"));
      setAgentAPIProviderName("");
      setAgentAPIProviderBaseURL("");
      setAgentAPIProviderAPIKey("");
      setAgentConfigAddTab("backup");
      setAgentConfigStep("details");
    } else {
      const [backups, providers, syncStatus] = await Promise.all([
        fetchAgentConfigBackups(agentName),
        supportsAPIProvider ? fetchAgentAPIProviders(agentName) : Promise.resolve([]),
        agentName.toLowerCase() === "codex" ? fetchAgentConfigSyncStatus() : Promise.resolve(null),
      ]);
      setAgentConfigBackups(backups);
      setAgentAPIProviders(providers);
      setAgentConfigSyncStatus(syncStatus);
      setSelectedAgentConfigID("");
      const preferredProvider = providers.find((provider) => agentConfigPreferredProviderIDs.includes(provider.id));
      setSelectedAgentAPIProviderID(preferredProvider?.id || "");
      setAgentConfigSwitchSelection(preferredProvider ? { type: "api_provider", id: preferredProvider.id } : null);
      setAgentConfigSwitchTab(supportsAPIProvider && selectedAgent?.last_config_selection?.type === "api_provider" ? "api_provider" : "backup");
      if (supportsAPIProvider && agentConfigPreferredProviderIDs.length > 0) {
        setAgentConfigSwitchTab("api_provider");
      }
      setAgentConfigStep("details");
    }
  } catch (error) {
    setAgentConfigError(error instanceof Error ? error.message : t("agentConfig.loadConfigFailed"));
  } finally {
    setAgentConfigBusy(false);
  }
}, [agentConfigAgents, agentConfigFlow, agentConfigPreferredProviderIDs, t]);

  return { restartAgentFromConfigList, chooseAgentForConfig };
}
