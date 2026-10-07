import { appURL } from "../net/base";
import { getRootNodeId } from "../net/rootNode";
import { protectedJSON } from "../net/api";
import type { GitStatusPayload, GitActionPayload, GitStatusItem } from "./types";

export function normalizeGitStatusPayload(payload: any): GitStatusPayload {
  return {
    available: payload?.available === true,
    branch: typeof payload?.branch === "string" ? payload.branch : undefined,
    dirty_count: Number(payload?.dirty_count) || 0,
    items: Array.isArray(payload?.items) ? payload.items as GitStatusItem[] : [],
  };
}

export function normalizeGitActionPayload(payload: any): GitActionPayload {
  return {
    output: typeof payload?.output === "string" ? payload.output : "",
    status: normalizeGitStatusPayload(payload?.status || {}),
  };
}

export async function fetchGitStatus(rootId: string, nodeId?: string): Promise<GitStatusPayload> {
  nodeId = nodeId || getRootNodeId(rootId);
  const payload = await protectedJSON<any>(appURL("/api/git/status", new URLSearchParams({ root: rootId }), nodeId));
  return normalizeGitStatusPayload(payload);
}

export async function fetchGitStatusByPath(path: string, nodeId?: string): Promise<GitStatusPayload> {
  const payload = await protectedJSON<any>(appURL("/api/git/status", new URLSearchParams({ path }), nodeId));
  return normalizeGitStatusPayload(payload);
}
