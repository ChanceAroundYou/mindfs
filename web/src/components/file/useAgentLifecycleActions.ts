import React from "react";
import type { AgentStatus } from "../../services/agents";
type AgentLifecycleCommandAction = "install" | "update";

export function useAgentLifecycleActions(ctx: any) {
  const { agentLifecycleError, setAgentLifecycleError, setAgentLifecycleBusy, setAgentLifecycleRunningAgent, t, onRunAgentLifecycleCommand, closeAgentLifecycleFlow } = ctx;
const runAgentLifecycleCommand = React.useCallback(async (agent: AgentStatus, action: AgentLifecycleCommandAction) => {
  const commands = action === "install" ? agent.install_commands || [] : agent.update_commands || [];
  if (commands.length === 0) {
    setAgentLifecycleError(t("agentConfig.noCommand"));
    return;
  }
  setAgentLifecycleBusy(true);
  setAgentLifecycleRunningAgent(agent.name);
  setAgentLifecycleError("");
  try {
    await onRunAgentLifecycleCommand?.(agent.name, action, commands);
    closeAgentLifecycleFlow();
  } catch (error) {
    setAgentLifecycleError(error instanceof Error ? error.message : t("agentConfig.commandStartFailed"));
  } finally {
    setAgentLifecycleBusy(false);
    setAgentLifecycleRunningAgent("");
  }
}, [closeAgentLifecycleFlow, onRunAgentLifecycleCommand, t]);

  return { runAgentLifecycleCommand };
}
