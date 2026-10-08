import React from "react";
import { fetchAgents } from "../../services/agents";
import { getRootNodeId } from "../../services/net/rootNode";

export function useAgentConfigSwitchRequest(ctx: any) {
  const { agentConfigSwitchRequest, rootId, t, setAgentLifecycleOpen, agentConfigFlowVersion, setAgentConfigNotice, setAgentConfigFlow, setAgentConfigStep, setAgentConfigAgent, setAgentConfigAddTab, setAgentConfigSwitchTab, setAgentConfigName, setAgentConfigFileSourcesBody, setAgentConfigEnvBody, setAgentAPIProviderName, setAgentAPIProviderBaseURL, setAgentAPIProviderAPIKey, setAgentConfigBackups, setAgentAPIProviders, setSelectedAgentConfigID, setSelectedAgentAPIProviderID, setAgentConfigSwitchSelection, setAgentConfigPreferredProviderIDs, setAgentConfigConfirmMessage, setAgentConfigError, setAgentConfigRestartingAgent, setIsMenuOpen, setAgentConfigBusy, setAgentConfigAgents } = ctx;
React.useEffect(() => {
  if (!agentConfigSwitchRequest) {
    return;
  }
  const providerIDs = (agentConfigSwitchRequest.providerIDs || [])
    .map((id: unknown) => String(id || "").trim())
    .filter(Boolean);
  setAgentLifecycleOpen(false);
  agentConfigFlowVersion.current += 1;
  setAgentConfigNotice("");
  setAgentConfigFlow("switch");
  setAgentConfigStep("agent");
  setAgentConfigAgent("");
  setAgentConfigAddTab("backup");
  setAgentConfigSwitchTab("api_provider");
  setAgentConfigName("");
  setAgentConfigFileSourcesBody("");
  setAgentConfigEnvBody("");
  setAgentAPIProviderName("");
  setAgentAPIProviderBaseURL("");
  setAgentAPIProviderAPIKey("");
  setAgentConfigBackups([]);
  setAgentAPIProviders([]);
  setSelectedAgentConfigID("");
  setSelectedAgentAPIProviderID("");
  setAgentConfigSwitchSelection(null);
  setAgentConfigPreferredProviderIDs(providerIDs);
  setAgentConfigConfirmMessage("");
  setAgentConfigError("");
  setAgentConfigRestartingAgent("");
  setIsMenuOpen(false);
  setAgentConfigBusy(true);
  fetchAgents(true, getRootNodeId(rootId || "") as any)
    .then((items) => {
      setAgentConfigAgents(items.filter((item) => item.installed));
    })
    .catch((error) => {
      setAgentConfigError(error instanceof Error ? error.message : t("agentConfig.loadAgentFailed"));
    })
    .finally(() => setAgentConfigBusy(false));
}, [agentConfigSwitchRequest?.nonce, rootId, t]);
}
