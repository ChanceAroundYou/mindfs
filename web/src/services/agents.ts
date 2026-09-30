import { appPath } from "./base";
import { protectedAPIReady, protectedJSON } from "./api";

// Agent status service

export type AgentStatus = {
  name: string;
  protocol?: string;
  brief?: string;
  installed: boolean;
  available: boolean;
  version?: string;
  error?: string;
  last_probe?: string;
  current_model_id?: string;
  current_mode_id?: string;
  default_model_id?: string;
  default_effort?: string;
  default_fast_service?: string;
  last_config_selection?: AgentLastConfigSelection;
  supports_api_provider_switch?: boolean;
  supported_api_provider_protocols?: string[];
  supports_fast_service?: boolean;
  efforts?: string[];
  models?: AgentModelInfo[];
  modes?: AgentModeInfo[];
  models_error?: string;
  modes_error?: string;
  commands?: AgentCommandInfo[];
  commands_error?: string;
  install_commands?: string[];
  update_commands?: string[];
};

export type AgentLastConfigSelection = {
  type?: string;
  id?: string;
  name?: string;
};

export type AgentModelInfo = {
  id: string;
  name: string;
  description?: string;
  hidden?: boolean;
  supportEffort?: boolean;
  efforts?: string[];
};

export type AgentModeInfo = {
  id: string;
  name: string;
  description?: string;
};

export type AgentCommandInfo = {
  name: string;
  description?: string;
  argument_hint?: string;
};

export type ShellStatus = {
  id: string;
  name?: string;
  label: string;
  command: string;
  resolved_command?: string;
  args?: string[];
  default?: boolean;
};

const VALID_EFFORTS = ["low", "medium", "high", "xhigh", "max", "ultra"] as const;
function normalizeEfforts(input: unknown): string[] | undefined {
  if (!Array.isArray(input)) {
    return undefined;
  }
  const seen = new Set<string>();
  const efforts: string[] = [];
  for (const item of input) {
    const value = String(item || "").trim().toLowerCase();
    if (!VALID_EFFORTS.includes(value as (typeof VALID_EFFORTS)[number])) {
      continue;
    }
    if (seen.has(value)) {
      continue;
    }
    seen.add(value);
    efforts.push(value);
  }
  return efforts;
}

function normalizeAgentStatus(input: unknown): AgentStatus | null {
  if (!input || typeof input !== "object") {
    return null;
  }
  const agent = input as AgentStatus;
  const models = Array.isArray(agent.models)
    ? agent.models.map((model) => ({
        ...model,
        efforts: normalizeEfforts(model.efforts),
      }))
    : agent.models;
  return {
    ...agent,
    efforts: normalizeEfforts(agent.efforts),
    models,
    default_fast_service:
      typeof agent.default_fast_service === "string"
        ? agent.default_fast_service
        : "",
    supports_fast_service: !!agent.supports_fast_service,
  };
}

type AgentCache = { agents: AgentStatus[]; shells: ShellStatus[]; at: number };
const agentCacheByNode = new Map<string, AgentCache>();
const catalogCacheByNode = new Map<string, AgentCache>();
const inflightByNode = new Map<string, Promise<{ agents: AgentStatus[]; shells: ShellStatus[] }>>();
const inflightCatalogByNode = new Map<string, Promise<{ agents: AgentStatus[]; shells: ShellStatus[] }>>();
function cacheKey(nodeId?: string) {
  return String(nodeId || "__local__");
}
const CACHE_TTL = 30000; // 30 seconds

function normalizeShellStatus(input: unknown): ShellStatus | null {
  if (!input || typeof input !== "object") {
    return null;
  }
  const shell = input as ShellStatus;
  const id = String(shell.id || shell.command || "").trim();
  const command = String(shell.command || id).trim();
  if (!id || !command) {
    return null;
  }
  return {
    id,
    command,
    name: typeof shell.name === "string" ? shell.name : undefined,
    resolved_command: typeof shell.resolved_command === "string" ? shell.resolved_command : undefined,
    label: String(shell.name || shell.label || id).trim() || id,
    args: Array.isArray(shell.args) ? shell.args.map((item) => String(item)) : undefined,
    default: !!shell.default,
  };
}

async function fetchAgentRuntime(force = false, includeAll = false, nodeId?: string): Promise<{ agents: AgentStatus[]; shells: ShellStatus[] }> {
  const key = cacheKey(nodeId);
  const cached = (includeAll ? catalogCacheByNode : agentCacheByNode).get(key);
  const agentCache = cached?.agents || [];
  const agentLastFetch = cached?.at || 0;
  const inFlight = (includeAll ? inflightCatalogByNode : inflightByNode).get(key) || null;
  if (!force && agentCache.length > 0 && Date.now() - agentLastFetch < CACHE_TTL) {
    return { agents: agentCache, shells: cached?.shells || [] };
  }
  if (inFlight) {
    return inFlight;
  }
  if (!protectedAPIReady()) {
    return { agents: agentCache, shells: cached?.shells || [] };
  }

  const request = (async () => {
    const now = Date.now();
    const data = await protectedJSON<any>(appPath(includeAll ? "/api/agents?all=1" : "/api/agents", nodeId));
    const agentItems: unknown[] = Array.isArray(data) ? data : Array.isArray(data?.agents) ? data.agents : [];
    const shellItems: unknown[] = Array.isArray(data?.shells) ? data.shells : [];
    const nextAgents = agentItems
      ? agentItems.map(normalizeAgentStatus).filter((item): item is AgentStatus => item !== null)
      : [];
    const nextShells = shellItems.map(normalizeShellStatus).filter((item): item is ShellStatus => item !== null);
    const entry = { agents: nextAgents, shells: nextShells, at: now };
    if (includeAll) catalogCacheByNode.set(key, entry);
    else agentCacheByNode.set(key, entry);
    return { agents: nextAgents, shells: nextShells };
  })();
  if (includeAll) {
    inflightCatalogByNode.set(key, request);
  } else {
    inflightByNode.set(key, request);
  }
  try {
    return await request;
  } catch (err) {
    console.error("Failed to fetch agents:", err);
    return { agents: agentCache, shells: cached?.shells || [] };
  } finally {
    if (includeAll) {
      inflightCatalogByNode.delete(key);
    } else {
      inflightByNode.delete(key);
    }
  }
}

export async function fetchAgents(force = false, nodeId?: string): Promise<AgentStatus[]> {
  const data = await fetchAgentRuntime(force, false, nodeId);
  return data.agents;
}

export async function fetchAgentCatalog(force = false, nodeId?: string): Promise<AgentStatus[]> {
  const data = await fetchAgentRuntime(force, true, nodeId);
  return data.agents;
}

export async function restartAgent(agent: string, nodeId?: string): Promise<{ restarting: boolean; agent: string }> {
  return protectedJSON<{ restarting: boolean; agent: string }>(appPath("/api/agents/restart", nodeId), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ agent }),
  });
}

export async function fetchShells(force = false, nodeId?: string): Promise<ShellStatus[]> {
  const data = await fetchAgentRuntime(force, false, nodeId);
  return data.shells;
}
