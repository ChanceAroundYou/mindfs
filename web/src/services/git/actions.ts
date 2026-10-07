import { appURL } from "../net/base";
import { getRootNodeId } from "../net/rootNode";
import { protectedJSON } from "../net/api";
import type { GitStatusPayload, GitActionPayload, GitBranchesPayload, GitWorktreesPayload, GitBranchItem, GitStatusItem, GitWorktreeItem } from "./types";
import { normalizeGitStatusPayload, normalizeGitActionPayload } from "./status";

export async function fetchGitBranches(rootId: string, nodeId?: string): Promise<GitBranchesPayload> {
  nodeId = nodeId || getRootNodeId(rootId);
  const payload = await protectedJSON<any>(appURL("/api/git/branches", new URLSearchParams({ root: rootId }), nodeId));
  return {
    current: typeof payload?.current === "string" ? payload.current : undefined,
    branches: Array.isArray(payload?.branches)
      ? payload.branches
          .map((item: any) => ({
            name: typeof item?.name === "string" ? item.name : "",
            current: item?.current === true,
          }))
          .filter((item: GitBranchItem) => !!item.name)
      : [],
  };
}

export async function checkoutGitBranch(rootId: string, branch: string, nodeId?: string): Promise<GitStatusPayload> {
  nodeId = nodeId || getRootNodeId(rootId);
  const payload = await protectedJSON<any>(appURL("/api/git/checkout", undefined, nodeId), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ root: rootId, branch }),
  });
  return normalizeGitStatusPayload(payload?.status || {});
}

export async function pullGit(rootId: string, nodeId?: string): Promise<GitActionPayload> {
  nodeId = nodeId || getRootNodeId(rootId);
  const payload = await protectedJSON<any>(appURL("/api/git/pull", undefined, nodeId), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ root: rootId }),
  });
  return normalizeGitActionPayload(payload);
}

export async function pushGit(rootId: string, nodeId?: string): Promise<GitActionPayload> {
  nodeId = nodeId || getRootNodeId(rootId);
  const payload = await protectedJSON<any>(appURL("/api/git/push", undefined, nodeId), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ root: rootId }),
  });
  return normalizeGitActionPayload(payload);
}

export async function commitGit(rootId: string, message: string, nodeId?: string): Promise<GitActionPayload> {
  nodeId = nodeId || getRootNodeId(rootId);
  const payload = await protectedJSON<any>(appURL("/api/git/commit", undefined, nodeId), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ root: rootId, message }),
  });
  return normalizeGitActionPayload(payload);
}

export async function stageGitItem(rootId: string, item: Pick<GitStatusItem, "path">, nodeId?: string): Promise<GitActionPayload> {
  nodeId = nodeId || getRootNodeId(rootId);
  const payload = await protectedJSON<any>(appURL("/api/git/stage", undefined, nodeId), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ root: rootId, path: item.path }),
  });
  return normalizeGitActionPayload(payload);
}

export async function unstageGitItem(rootId: string, item: Pick<GitStatusItem, "path">, nodeId?: string): Promise<GitActionPayload> {
  nodeId = nodeId || getRootNodeId(rootId);
  const payload = await protectedJSON<any>(appURL("/api/git/unstage", undefined, nodeId), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ root: rootId, path: item.path }),
  });
  return normalizeGitActionPayload(payload);
}

export async function discardGitItem(rootId: string, item: Pick<GitStatusItem, "path" | "status">, nodeId?: string): Promise<GitActionPayload> {
  nodeId = nodeId || getRootNodeId(rootId);
  const payload = await protectedJSON<any>(appURL("/api/git/discard", undefined, nodeId), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ root: rootId, path: item.path, status: item.status }),
  });
  return normalizeGitActionPayload(payload);
}

export async function fetchGitWorktrees(rootId: string, nodeId?: string): Promise<GitWorktreesPayload> {
  nodeId = nodeId || getRootNodeId(rootId);
  const payload = await protectedJSON<any>(appURL("/api/git/worktrees", new URLSearchParams({ root: rootId }), nodeId));
  return {
    items: Array.isArray(payload?.items)
      ? payload.items
          .map((item: any) => ({
            path: typeof item?.path === "string" ? item.path : "",
            branch: typeof item?.branch === "string" ? item.branch : undefined,
            head: typeof item?.head === "string" ? item.head : undefined,
            current: item?.current === true,
          }))
          .filter((item: GitWorktreeItem) => !!item.path)
      : [],
  };
}

export async function createGitWorktree(input: {
  rootId: string;
  parentPath: string;
  name: string;
  branchMode: "new" | "existing";
  branch?: string;
  nodeId?: string;
}): Promise<any> {
  return protectedJSON<any>(appURL("/api/git/worktrees", undefined, input.nodeId), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      root: input.rootId,
      parent_path: input.parentPath,
      name: input.name,
      branch_mode: input.branchMode,
      branch: input.branch || "",
    }),
  });
}

export async function removeGitWorktree(rootId: string, nodeId?: string): Promise<any> {
  nodeId = nodeId || getRootNodeId(rootId);
  return protectedJSON<any>(appURL("/api/git/worktrees", undefined, nodeId), {
    method: "DELETE",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ root: rootId }),
  });
}
