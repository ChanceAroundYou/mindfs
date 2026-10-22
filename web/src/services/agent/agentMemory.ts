import { protectedJSON } from "../net/api";
import { appPath } from "../net/base";

export type AgentMemoryItem = {
  name: string;
  process_count: number;
  memory_bytes: number;
  session_count: number;
};

export type AgentMemorySnapshot = {
  total_memory_bytes: number;
  measured_at: string;
  idle_hours: number;
  agents: AgentMemoryItem[];
};

export type AgentSessionReleaseResult = AgentMemorySnapshot & {
  released_sessions: number;
  failed_sessions: number;
};

export async function fetchAgentMemory(nodeId?: string): Promise<AgentMemorySnapshot> {
  return protectedJSON<AgentMemorySnapshot>(appPath("/api/agents/memory", nodeId));
}

export async function releaseAgentSessions(nodeId?: string): Promise<AgentSessionReleaseResult> {
  return protectedJSON<AgentSessionReleaseResult>(appPath("/api/agents/release-idle", nodeId), {
    method: "POST",
  });
}
