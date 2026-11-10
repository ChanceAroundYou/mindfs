import type { AgentConfigBackup, AgentStatus } from "../../shared/agentTypes";

type AgentProfileStatus = Pick<AgentStatus, "protocol"> | null | undefined;

export function isCodexProfileAgent(agentName: string, agent?: AgentProfileStatus): boolean {
	if (agent) {
		return agent.protocol === "codex-sdk";
	}
	return agentName.trim().toLowerCase() === "codex";
}

export function switchableAgentConfigBackups(
  agentName: string,
  agent: AgentProfileStatus,
  backups: AgentConfigBackup[],
): AgentConfigBackup[] {
  return isCodexProfileAgent(agentName, agent)
    ? backups.filter((item) => item.source === "shared")
    : backups;
}
