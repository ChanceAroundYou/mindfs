import { useCallback } from "react";
import type { Dispatch, MutableRefObject, SetStateAction } from "react";
import { fetchAgentCatalog, fetchAgents, type AgentStatus } from "../../services/agents";
import type { AgentAPIProvider, AgentConfigBackup } from "../../services/agentConfig";
import type { I18nContextValue } from "../../i18n/index";
import { getRootNodeId } from "../../services/net/rootNode";
import type {
  AgentConfigAddTab,
  AgentConfigFlow,
  AgentConfigStep,
  AgentConfigSwitchTab,
} from "./AgentConfigPopover";
import type { AgentConfigSwitchSelection } from "./fileTreeTypes";

export type AgentConfigFlowTransitionsCtx = {
  rootId: string | null | undefined;
  agentConfigFlowVersion: MutableRefObject<number>;
  t: I18nContextValue["t"];
  fetchAgents: typeof fetchAgents;
  fetchAgentCatalog: typeof fetchAgentCatalog;
  setIdleReleaseOpen: Dispatch<SetStateAction<boolean>>;
  setAgentLifecycleOpen: Dispatch<SetStateAction<boolean>>;
  setAgentConfigNotice: Dispatch<SetStateAction<string>>;
  setAgentConfigFlow: Dispatch<SetStateAction<AgentConfigFlow | null>>;
  setAgentConfigStep: Dispatch<SetStateAction<AgentConfigStep>>;
  setAgentConfigAgent: Dispatch<SetStateAction<string>>;
  setAgentConfigAddTab: Dispatch<SetStateAction<AgentConfigAddTab>>;
  setAgentConfigSwitchTab: Dispatch<SetStateAction<AgentConfigSwitchTab>>;
  setAgentConfigName: Dispatch<SetStateAction<string>>;
  setAgentConfigFileSourcesBody: Dispatch<SetStateAction<string>>;
  setAgentConfigEnvBody: Dispatch<SetStateAction<string>>;
  setAgentAPIProviderName: Dispatch<SetStateAction<string>>;
  setAgentAPIProviderBaseURL: Dispatch<SetStateAction<string>>;
  setAgentAPIProviderAPIKey: Dispatch<SetStateAction<string>>;
  setAgentConfigBackups: Dispatch<SetStateAction<AgentConfigBackup[]>>;
  setAgentAPIProviders: Dispatch<SetStateAction<AgentAPIProvider[]>>;
  setSelectedAgentConfigID: Dispatch<SetStateAction<string>>;
  setSelectedAgentAPIProviderID: Dispatch<SetStateAction<string>>;
  setAgentConfigSwitchSelection: Dispatch<SetStateAction<AgentConfigSwitchSelection | null>>;
  setAgentConfigPreferredProviderIDs: Dispatch<SetStateAction<string[]>>;
  setAgentConfigConfirmMessage: Dispatch<SetStateAction<string>>;
  setAgentConfigError: Dispatch<SetStateAction<string>>;
  setAgentConfigRestartingAgent: Dispatch<SetStateAction<string>>;
  setIsMenuOpen: Dispatch<SetStateAction<boolean>>;
  setAgentConfigBusy: Dispatch<SetStateAction<boolean>>;
  setAgentConfigAgents: Dispatch<SetStateAction<AgentStatus[]>>;
  setAgentLifecycleError: Dispatch<SetStateAction<string>>;
  setAgentLifecycleBusy: Dispatch<SetStateAction<boolean>>;
  setAgentLifecycleAgents: Dispatch<SetStateAction<AgentStatus[]>>;
  setAgentLifecycleRunningAgent: Dispatch<SetStateAction<string>>;
};

/**
 * 「agent 配置浮层」与「agent 生命周期浮层」四组开关迁移。
 *
 * 从 FileTree 抽出（2026-10-08）：它们全是同一批 setter 的定序重置 + 一次
 * fetchAgents / fetchAgentCatalog，彼此之间有先后依赖（开一个流程要先关另一个、
 * 先重置表单再拉列表），拆散了才看得见顺序。依赖数组逐字保留。
 */
export function useAgentConfigFlowTransitions(ctx: AgentConfigFlowTransitionsCtx) {
  const { rootId, agentConfigFlowVersion, t, fetchAgents, fetchAgentCatalog, setIdleReleaseOpen, setAgentLifecycleOpen, setAgentConfigNotice, setAgentConfigFlow, setAgentConfigStep, setAgentConfigAgent, setAgentConfigAddTab, setAgentConfigSwitchTab, setAgentConfigName, setAgentConfigFileSourcesBody, setAgentConfigEnvBody, setAgentAPIProviderName, setAgentAPIProviderBaseURL, setAgentAPIProviderAPIKey, setAgentConfigBackups, setAgentAPIProviders, setSelectedAgentConfigID, setSelectedAgentAPIProviderID, setAgentConfigSwitchSelection, setAgentConfigPreferredProviderIDs, setAgentConfigConfirmMessage, setAgentConfigError, setAgentConfigRestartingAgent, setIsMenuOpen, setAgentConfigBusy, setAgentConfigAgents, setAgentLifecycleError, setAgentLifecycleBusy, setAgentLifecycleAgents, setAgentLifecycleRunningAgent } = ctx;

  const openAgentConfigFlow = useCallback((flow: AgentConfigFlow) => {
    setIdleReleaseOpen(false);
    setAgentLifecycleOpen(false);
    agentConfigFlowVersion.current += 1;
    setAgentConfigNotice("");
    setAgentConfigFlow(flow);
    setAgentConfigStep("agent");
    setAgentConfigAgent("");
    setAgentConfigAddTab("backup");
    setAgentConfigSwitchTab("backup");
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
    setAgentConfigPreferredProviderIDs([]);
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
  }, [t, rootId]);

  const closeAgentConfigFlow = useCallback(() => {
    agentConfigFlowVersion.current += 1;
    setAgentConfigNotice("");
    setAgentConfigFlow(null);
    setAgentConfigStep("agent");
    setAgentConfigError("");
    setAgentConfigConfirmMessage("");
    setAgentConfigSwitchSelection(null);
    setAgentConfigSwitchTab("backup");
    setAgentConfigRestartingAgent("");
  }, []);

  const openAgentLifecycleFlow = useCallback(() => {
    agentConfigFlowVersion.current += 1;
    setAgentConfigNotice("");
    setAgentConfigFlow(null);
    setAgentLifecycleOpen(true);
    setIsMenuOpen(false);
    setAgentLifecycleError("");
    setAgentLifecycleBusy(true);
    fetchAgentCatalog(true, getRootNodeId(rootId || "") as any)
      .then((items) => {
        setAgentLifecycleAgents(items);
      })
      .catch((error) => {
        setAgentLifecycleError(error instanceof Error ? error.message : t("agentConfig.loadAgentFailed"));
      })
      .finally(() => setAgentLifecycleBusy(false));
  }, [rootId, t]);

  const closeAgentLifecycleFlow = useCallback(() => {
    setAgentLifecycleOpen(false);
    setAgentLifecycleError("");
    setAgentLifecycleRunningAgent("");
  }, []);

  return {
    openAgentConfigFlow,
    closeAgentConfigFlow,
    openAgentLifecycleFlow,
    closeAgentLifecycleFlow,
  };
}
