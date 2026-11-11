import type { AgentConfigBackup, AgentStatus } from "../../shared/agentTypes";

type AgentProfileStatus = Pick<AgentStatus, "protocol"> | null | undefined;

export function isCodexProfileAgent(agentName: string, agent?: AgentProfileStatus): boolean {
	if (agent?.protocol) {
		return agent.protocol === "codex-sdk";
	}
	// 旧版 worker 的 agent 状态可能不带 protocol；此时按名字兜底，
	// 否则 Codex 会露出只适用于其它 agent 的 API 供应商切换入口。
	return agentName.trim().toLowerCase() === "codex";
}

// 共享配置同步按 harness 家族登记（codex / claude）；agent 名称可以是自定义的
// （codex-custom、claude-sdk 之类），所以先从 protocol / 名字归一，再回退原名。
export function agentConfigHarnessName(agentName: string, agent?: AgentProfileStatus): string {
	const name = agentName.trim();
	const protocol = String(agent?.protocol || "").trim().toLowerCase();
	if (isCodexProfileAgent(name, agent)) {
		return "codex";
	}
	if (protocol.includes("claude") || name.toLowerCase().includes("claude")) {
		return "claude";
	}
	return name;
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
