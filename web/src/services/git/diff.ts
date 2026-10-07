import { appURL } from "../net/base";
import { getRootNodeId } from "../net/rootNode";
import { protectedJSON } from "../net/api";
import type { GitStatusItem, GitDiffPayload } from "./types";
import { getCachedGitDiff, setCachedGitDiff, type CachedGitDiffPayload } from "../file";
import {
  gitScopedRoot,
  gitCommitDiffCache,
  gitCommitDiffInflight,
  commitDiffStorageKey,
  setBounded,
  readStorageJSON,
  writeStorageJSON,
  capStorageByPrefix,
  COMMIT_DIFF_STORAGE_PREFIX,
  commitDiffStorageMaxEntries,
} from "./history";

export function buildGitDiffCacheSignature(item?: Partial<GitStatusItem> | null): string {
  if (!item) {
    return "";
  }
  return [
    item.status || "",
    item.old_path || "",
    Number(item.additions) || 0,
    Number(item.deletions) || 0,
  ].join(":");
}

export async function fetchGitDiff(
  rootId: string,
  path: string,
  options?: { cacheSignature?: string; repoPath?: string; nodeId?: string },
): Promise<GitDiffPayload> {
  options = { ...options, nodeId: options?.nodeId || getRootNodeId(rootId) } as any;
  const cacheSignature = options?.cacheSignature || "";
  const repoPath = String(options?.repoPath || "").trim();
  if (!repoPath) {
    const cached = await getCachedGitDiff(rootId, path, cacheSignature, options?.nodeId);
    if (cached) {
      return cached as GitDiffPayload;
    }
  }

  const params = new URLSearchParams({ root: rootId, path });
  if (repoPath) {
    params.set("repo_path", repoPath);
  }
  const payload = await protectedJSON<any>(appURL("/api/git/diff", params, options?.nodeId));
  const diff = {
    path: typeof payload?.path === "string" ? payload.path : path,
    display_path: typeof payload?.display_path === "string" ? payload.display_path : undefined,
    old_path: typeof payload?.old_path === "string" ? payload.old_path : undefined,
    status: typeof payload?.status === "string" ? payload.status : "M",
    additions: Number(payload?.additions) || 0,
    deletions: Number(payload?.deletions) || 0,
    content: typeof payload?.content === "string" ? payload.content : "",
    file_meta: Array.isArray(payload?.file_meta) ? payload.file_meta : [],
    source: "worktree" as const,
  };
  if (!repoPath) {
    await setCachedGitDiff(rootId, path, diff, cacheSignature, options?.nodeId);
  }
  return diff;
}

export async function fetchGitCommitDiff(
  rootId: string,
  commit: string,
  item: Pick<GitStatusItem, "path" | "old_path" | "status" | "additions" | "deletions">,
  nodeId?: string,
): Promise<GitDiffPayload> {
  nodeId = nodeId || getRootNodeId(rootId);
  const nid = String(nodeId || "").trim();
  const scoped = gitScopedRoot(rootId, nid || undefined);
  const path = item.path;
  const key = `${scoped}:${commit}:${item.old_path || ""}:${path}`;
  const cached = gitCommitDiffCache.get(key);
  if (cached) {
    return cached;
  }
  const persisted = readStorageJSON<GitDiffPayload>(commitDiffStorageKey(rootId, commit, item.old_path || "", path, nid || undefined));
  if (persisted && typeof persisted.content === "string") {
    setBounded(gitCommitDiffCache, key, persisted);
    return persisted;
  }
  const inflight = gitCommitDiffInflight.get(key);
  if (inflight) {
    return inflight;
  }
  const promise = protectedJSON<any>(
    appURL("/api/git/commit/diff", new URLSearchParams({ root: rootId, commit, path }), nodeId),
  ).then((payload) => {
    const diff = {
    path: typeof payload?.path === "string" ? payload.path : path,
    display_path: typeof payload?.display_path === "string" ? payload.display_path : undefined,
    old_path: typeof payload?.old_path === "string" ? payload.old_path : item.old_path,
      status: typeof payload?.status === "string" ? payload.status : item.status,
      additions: Number(payload?.additions) || Number(item.additions) || 0,
      deletions: Number(payload?.deletions) || Number(item.deletions) || 0,
      content: typeof payload?.content === "string" ? payload.content : "",
      file_meta: Array.isArray(payload?.file_meta) ? payload.file_meta : [],
      commit,
      source: "commit" as const,
    };
    setBounded(gitCommitDiffCache, key, diff);
    writeStorageJSON(commitDiffStorageKey(rootId, commit, item.old_path || "", path, nid || undefined), diff);
    capStorageByPrefix(COMMIT_DIFF_STORAGE_PREFIX, commitDiffStorageMaxEntries);
    return diff;
  }).finally(() => {
    gitCommitDiffInflight.delete(key);
  });
  gitCommitDiffInflight.set(key, promise);
  return promise;
}

export async function fetchGitRelatedFileDiff(
  rootId: string,
  file: { path: string; head?: string; repo_path?: string; repo_kind?: string },
  nodeId?: string,
): Promise<GitDiffPayload> {
  nodeId = nodeId || getRootNodeId(rootId);
  const path = file.path;
  const head = file.head || "";
  const params = new URLSearchParams({ root: rootId, path });
  if (head) {
    params.set("head", head);
  }
  if (file.repo_path) {
    params.set("repo_path", file.repo_path);
  }
  if (file.repo_kind) {
    params.set("repo_kind", file.repo_kind);
  }
  const payload = await protectedJSON<any>(appURL("/api/git/related-file/diff", params, nodeId));
  return {
    path: typeof payload?.path === "string" ? payload.path : path,
    display_path: typeof payload?.display_path === "string" ? payload.display_path : undefined,
    old_path: typeof payload?.old_path === "string" ? payload.old_path : undefined,
    status: typeof payload?.status === "string" ? payload.status : "M",
    additions: Number(payload?.additions) || 0,
    deletions: Number(payload?.deletions) || 0,
    content: typeof payload?.content === "string" ? payload.content : "",
    file_meta: Array.isArray(payload?.file_meta) ? payload.file_meta : [],
    base_head: typeof payload?.base_head === "string" ? payload.base_head : head,
    target_head: typeof payload?.target_head === "string" ? payload.target_head : undefined,
    source: payload?.source === "commit_range" ? "commit_range" : "worktree",
  };
}
