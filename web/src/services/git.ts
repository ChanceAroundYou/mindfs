import { appURL } from "./base";
import { getRootNodeId } from "./rootNode";
import { protectedJSON } from "./api";
import { getCachedGitDiff, setCachedGitDiff, type CachedGitDiffPayload } from "./file";

export type GitStatusCode = "M" | "A" | "D" | "R" | "??";

export type GitStatusItem = {
  path: string;
  display_path?: string;
  old_path?: string;
  status: GitStatusCode;
  staged?: boolean;
  additions: number;
  deletions: number;
  is_dir?: boolean;
};

export type GitStatusPayload = {
  available: boolean;
  branch?: string;
  dirty_count: number;
  items: GitStatusItem[];
};

export type GitDiffPayload = CachedGitDiffPayload & {
  path: string;
  display_path?: string;
  old_path?: string;
  status: GitStatusCode | string;
  additions: number;
  deletions: number;
  content: string;
  commit?: string;
  base_head?: string;
  target_head?: string;
  source?: "worktree" | "commit" | "commit_range";
};

export type GitHistoryItem = {
  hash: string;
  message: string;
  commit_time: string;
  remote?: boolean;
};

export type GitHistoryPayload = {
  available: boolean;
  items: GitHistoryItem[];
  has_more: boolean;
  commit_missing?: boolean;
  remote_head?: string;
};

export type GitCommitFilesPayload = {
  commit: string;
  items: GitStatusItem[];
};

export type GitBranchItem = {
  name: string;
  current: boolean;
};

export type GitBranchesPayload = {
  current?: string;
  branches: GitBranchItem[];
};

export type GitWorktreeItem = {
  path: string;
  branch?: string;
  head?: string;
  current: boolean;
};

export type GitWorktreesPayload = {
  items: GitWorktreeItem[];
};

export type GitActionPayload = {
  output: string;
  status: GitStatusPayload;
};

function normalizeGitStatusPayload(payload: any): GitStatusPayload {
  return {
    available: payload?.available === true,
    branch: typeof payload?.branch === "string" ? payload.branch : undefined,
    dirty_count: Number(payload?.dirty_count) || 0,
    items: Array.isArray(payload?.items) ? payload.items as GitStatusItem[] : [],
  };
}

function normalizeGitActionPayload(payload: any): GitActionPayload {
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

const DEFAULT_HISTORY_LIMIT = 10;
const HISTORY_LIST_STORAGE_PREFIX = "mindfs.git.history.list:";
const COMMIT_FILES_STORAGE_PREFIX = "mindfs.git.history.files:";
const COMMIT_DIFF_STORAGE_PREFIX = "mindfs.git.history.diff.v2:";

type GitHistoryCacheEntry = {
  items: GitHistoryItem[];
  hasMore: boolean;
  remoteHead?: string;
};

const gitHistoryListCache = new Map<string, GitHistoryCacheEntry>();
const gitHistoryInflight = new Map<string, Promise<GitHistoryPayload>>();
const gitCommitFilesCache = new Map<string, GitCommitFilesPayload>();
const gitCommitFilesInflight = new Map<string, Promise<GitCommitFilesPayload>>();
const gitCommitDiffCache = new Map<string, GitDiffPayload>();
const gitCommitDiffInflight = new Map<string, Promise<GitDiffPayload>>();

function canUseStorage(): boolean {
  return typeof window !== "undefined" && !!window.localStorage;
}

function readStorageJSON<T>(key: string): T | null {
  if (!canUseStorage()) return null;
  try {
    const raw = window.localStorage.getItem(key);
    return raw ? JSON.parse(raw) as T : null;
  } catch {
    return null;
  }
}

function writeStorageJSON(key: string, value: unknown): void {
  if (!canUseStorage()) return;
  try {
    window.localStorage.setItem(key, JSON.stringify(value));
  } catch {}
}

function removeStorageByPrefix(prefix: string): void {
  if (!canUseStorage()) return;
  for (const key of Array.from({ length: window.localStorage.length }, (_, index) => window.localStorage.key(index)).filter(Boolean) as string[]) {
    if (key.startsWith(prefix)) {
      window.localStorage.removeItem(key);
    }
  }
}

function gitScopedRoot(rootId: string, nodeId?: string): string {
  const nid = String(nodeId || "").trim();
  const rid = String(rootId || "").trim();
  return nid ? `${nid}::${rid}` : rid;
}

function historyListStorageKey(rootId: string, nodeId?: string): string {
  return `${HISTORY_LIST_STORAGE_PREFIX}${encodeURIComponent(gitScopedRoot(rootId, nodeId))}`;
}

function commitFilesStorageKey(rootId: string, commit: string, nodeId?: string): string {
  return `${COMMIT_FILES_STORAGE_PREFIX}${encodeURIComponent(gitScopedRoot(rootId, nodeId))}:${encodeURIComponent(commit)}`;
}

function commitDiffStorageKey(rootId: string, commit: string, oldPath: string, path: string, nodeId?: string): string {
  return `${COMMIT_DIFF_STORAGE_PREFIX}${encodeURIComponent(gitScopedRoot(rootId, nodeId))}:${encodeURIComponent(commit)}:${encodeURIComponent(oldPath)}:${encodeURIComponent(path)}`;
}

function getHistoryCacheEntry(rootId: string, nodeId?: string): GitHistoryCacheEntry | null {
  const nid = String(nodeId || "").trim();
  const scoped = gitScopedRoot(rootId, nid);
  const cached = gitHistoryListCache.get(scoped);
  if (cached) {
    return cached;
  }
  const persisted = readStorageJSON<GitHistoryCacheEntry>(historyListStorageKey(rootId, nid || undefined));
  if (persisted && Array.isArray(persisted.items)) {
    const normalized = {
      items: persisted.items.filter((item) => !!item?.hash),
      hasMore: persisted.hasMore === true,
      remoteHead: typeof persisted.remoteHead === "string" ? persisted.remoteHead : undefined,
    };
    normalized.items = applyRemoteHead(normalized.items, normalized.remoteHead);
    gitHistoryListCache.set(scoped, normalized);
    return normalized;
  }
  // 兼容裸键迁移：旧数据以裸 rootId 存，命中后以新键重写并删除裸键避免第二节点继承
  if (nid) {
    const bareKey = String(rootId || "").trim();
    const bare = gitHistoryListCache.get(bareKey);
    if (bare) {
      gitHistoryListCache.set(scoped, bare);
      writeStorageJSON(historyListStorageKey(rootId, nid), bare);
      gitHistoryListCache.delete(bareKey);
      if (canUseStorage()) window.localStorage.removeItem(historyListStorageKey(rootId));
      return bare;
    }
    const barePersisted = readStorageJSON<GitHistoryCacheEntry>(historyListStorageKey(rootId));
    if (barePersisted && Array.isArray(barePersisted.items)) {
      const normalized = {
        items: barePersisted.items.filter((item) => !!item?.hash),
        hasMore: barePersisted.hasMore === true,
        remoteHead: typeof barePersisted.remoteHead === "string" ? barePersisted.remoteHead : undefined,
      };
      normalized.items = applyRemoteHead(normalized.items, normalized.remoteHead);
      gitHistoryListCache.set(scoped, normalized);
      writeStorageJSON(historyListStorageKey(rootId, nid), normalized);
      if (canUseStorage()) window.localStorage.removeItem(historyListStorageKey(rootId));
      return normalized;
    }
  }
  return null;
}

function setHistoryCacheEntry(rootId: string, entry: GitHistoryCacheEntry, nodeId?: string): void {
  const scoped = gitScopedRoot(rootId, nodeId);
  gitHistoryListCache.set(scoped, entry);
  writeStorageJSON(historyListStorageKey(rootId, String(nodeId || "").trim() || undefined), entry);
}

function normalizeGitHistoryPayload(payload: any): GitHistoryPayload {
  return {
    available: payload?.available === true,
    items: Array.isArray(payload?.items)
      ? payload.items
          .map((item: any) => ({
            hash: typeof item?.hash === "string" ? item.hash : "",
            message: typeof item?.message === "string" ? item.message : "",
            commit_time: typeof item?.commit_time === "string" ? item.commit_time : "",
            remote: item?.remote === true,
          }))
          .filter((item: GitHistoryItem) => !!item.hash)
      : [],
    has_more: payload?.has_more === true,
    commit_missing: payload?.commit_missing === true,
    remote_head: typeof payload?.remote_head === "string" ? payload.remote_head : undefined,
  };
}

function applyRemoteHead(items: GitHistoryItem[], remoteHead?: string): GitHistoryItem[] {
  if (!remoteHead) {
    return items;
  }
  const remoteHeadIndex = items.findIndex((item) => item.hash === remoteHead);
  if (remoteHeadIndex < 0) {
    return items;
  }
  return items.map((item, index) => (
    index >= remoteHeadIndex ? { ...item, remote: true } : item
  ));
}

function mergeHistoryItems(existing: GitHistoryItem[], next: GitHistoryItem[]): GitHistoryItem[] {
  const seen = new Set(existing.map((item) => item.hash));
  const merged = existing.slice();
  next.forEach((item) => {
    if (!seen.has(item.hash)) {
      seen.add(item.hash);
      merged.push(item);
    }
  });
  return merged;
}

export function getCachedGitHistory(rootId: string, nodeId?: string): GitHistoryPayload | null {
  const nid = String(nodeId ?? getRootNodeId(rootId) ?? "").trim();
  const cached = getHistoryCacheEntry(rootId, nid || undefined);
  if (!cached) {
    return null;
  }
  return {
    available: true,
    items: cached.items.slice(),
    has_more: cached.hasMore,
    remote_head: cached.remoteHead,
  };
}

export function getCachedGitHistoryHead(rootId: string, limit: number | string = DEFAULT_HISTORY_LIMIT, nodeId?: string): GitHistoryPayload | null {
  // 兼容旧调用：getCachedGitHistoryHead(rootId, nodeIdString)
  if (typeof limit === "string") {
    nodeId = limit as unknown as string;
    limit = DEFAULT_HISTORY_LIMIT;
  }
  const nid = String(nodeId ?? getRootNodeId(rootId) ?? "").trim();
  const cached = getHistoryCacheEntry(rootId, nid || undefined);
  if (!cached) {
    return null;
  }
  return {
    available: true,
    items: (cached.items as GitHistoryItem[]).slice(0, limit as number),
    has_more: cached.items.length > (limit as number) || cached.hasMore,
    remote_head: cached.remoteHead,
  };
}

export function clearGitHistoryCache(rootId?: string, nodeId?: string): void {
  const nid = String(nodeId ?? "").trim();
  const rid = String(rootId ?? "").trim();
  if (rid) {
    if (nid) {
      const scoped = gitScopedRoot(rid, nid);
      gitHistoryListCache.delete(scoped);
      if (canUseStorage()) window.localStorage.removeItem(historyListStorageKey(rid, nid));
      removeStorageByPrefix(`${COMMIT_FILES_STORAGE_PREFIX}${encodeURIComponent(scoped)}:`);
      removeStorageByPrefix(`${COMMIT_DIFF_STORAGE_PREFIX}${encodeURIComponent(scoped)}:`);
    } else {
      // 清该 root 在所有节点下的历史（含旧裸键与新 nid:: 键）
      for (const k of Array.from(gitHistoryListCache.keys())) {
        if (k === rid || k.endsWith(`::${rid}`)) gitHistoryListCache.delete(k);
      }
      if (canUseStorage()) {
        window.localStorage.removeItem(historyListStorageKey(rid));
        for (const k of Array.from({ length: window.localStorage.length }, (_, i) => window.localStorage.key(i)).filter(Boolean) as string[]) {
          if (k.startsWith(HISTORY_LIST_STORAGE_PREFIX) && (k === `${HISTORY_LIST_STORAGE_PREFIX}${encodeURIComponent(rid)}` || k.includes(encodeURIComponent(`::${rid}`)))) {
            window.localStorage.removeItem(k);
          }
          if ((k.startsWith(COMMIT_FILES_STORAGE_PREFIX) || k.startsWith(COMMIT_DIFF_STORAGE_PREFIX)) && k.includes(encodeURIComponent(rid))) {
            // commit 缓存键形如 prefix + encode(nid::rid) + :commit，是否含 rid 即属该 root
            window.localStorage.removeItem(k);
          }
        }
      }
    }
  } else {
    gitHistoryListCache.clear();
    removeStorageByPrefix(HISTORY_LIST_STORAGE_PREFIX);
    removeStorageByPrefix(COMMIT_FILES_STORAGE_PREFIX);
    removeStorageByPrefix(COMMIT_DIFF_STORAGE_PREFIX);
  }
  const clearMap = (cache: Map<string, unknown>) => {
    for (const key of Array.from(cache.keys())) {
      if (!rid) { cache.delete(key); continue; }
      if (!nid) {
        if (key === rid || key.startsWith(`${rid}:`) || key.includes(`::${rid}:`) || key.endsWith(`::${rid}`) || key.includes(`::${rid}::`)) cache.delete(key);
      } else {
        const scoped = gitScopedRoot(rid, nid);
        if (key === scoped || key.startsWith(`${scoped}:`) || key.startsWith(`${nid}::${rid}:`)) cache.delete(key);
      }
    }
  };
  clearMap(gitHistoryInflight as Map<string, unknown>);
  clearMap(gitCommitFilesCache as Map<string, unknown>);
  clearMap(gitCommitFilesInflight as Map<string, unknown>);
  clearMap(gitCommitDiffCache as Map<string, unknown>);
  clearMap(gitCommitDiffInflight as Map<string, unknown>);
}

export async function fetchGitHistory(
  rootId: string,
  options?: { beforeCommit?: string; afterCommit?: string; limit?: number; force?: boolean; nodeId?: string },
): Promise<GitHistoryPayload> {
  options = { ...options, nodeId: options?.nodeId || getRootNodeId(rootId) } as any;
  const nid = String((options as any)?.nodeId || "").trim();
  const limit = options?.limit || DEFAULT_HISTORY_LIMIT;
  const beforeCommit = options?.beforeCommit || "";
  const afterCommit = options?.afterCommit || "";
  const cached = getHistoryCacheEntry(rootId, nid || undefined);
  if (!options?.force && !beforeCommit && !afterCommit && cached) {
    return {
      available: true,
      items: cached.items.slice(0, limit),
      has_more: cached.items.length > limit || cached.hasMore,
      remote_head: cached.remoteHead,
    };
  }
  if (!options?.force && beforeCommit && cached) {
    const index = cached.items.findIndex((item) => item.hash === beforeCommit);
    if (index >= 0) {
      const cachedPage = cached.items.slice(index + 1, index + 1 + limit);
      if (cachedPage.length > 0 || !cached.hasMore) {
        return { available: true, items: cachedPage, has_more: cached.hasMore, remote_head: cached.remoteHead };
      }
    }
  }

  const key = `${nid ? `${nid}::` : ""}${rootId}:${beforeCommit}:${afterCommit}:${limit}`;
  const inflight = gitHistoryInflight.get(key);
  if (inflight) {
    return inflight;
  }
  const promise = protectedJSON<any>(
    appURL(
      "/api/git/history",
      new URLSearchParams({
        root: rootId,
        limit: String(limit),
        ...(beforeCommit ? { before_commit: beforeCommit } : {}),
        ...(afterCommit ? { after_commit: afterCommit } : {}),
      }),
      options?.nodeId,
    ),
  ).then((payload) => {
    const normalized = normalizeGitHistoryPayload(payload);
    if (normalized.commit_missing) {
      clearGitHistoryCache(rootId, nid || undefined);
      return normalized;
    }
    const existing = getHistoryCacheEntry(rootId, nid || undefined);
    if (!normalized.available) {
      return normalized;
    }
    if (!beforeCommit && !afterCommit) {
      const remoteHead = normalized.remote_head;
      setHistoryCacheEntry(rootId, {
        items: applyRemoteHead(normalized.items.slice(), remoteHead),
        hasMore: normalized.has_more,
        remoteHead,
      }, nid || undefined);
    } else if (beforeCommit) {
      const remoteHead = normalized.remote_head || existing?.remoteHead;
      setHistoryCacheEntry(rootId, {
        items: applyRemoteHead(mergeHistoryItems(existing?.items || [], normalized.items), remoteHead),
        hasMore: normalized.has_more,
        remoteHead,
      }, nid || undefined);
    } else if (afterCommit) {
      // afterCommit is a change probe. A reset/rebase can leave afterCommit as an
      // existing object that is no longer in the current HEAD history, so blindly
      // prepending new commits to the cached list can show stale commits.
    }
    return {
      ...normalized,
      items: applyRemoteHead(normalized.items, normalized.remote_head),
    };
  }).finally(() => {
    gitHistoryInflight.delete(key);
  });
  gitHistoryInflight.set(key, promise);
  return promise;
}

export async function fetchGitCommitFiles(rootId: string, commit: string, nodeId?: string): Promise<GitCommitFilesPayload> {
  nodeId = nodeId || getRootNodeId(rootId);
  const nid = String(nodeId || "").trim();
  const scoped = gitScopedRoot(rootId, nid || undefined);
  const key = `${scoped}:${commit}`;
  const cached = gitCommitFilesCache.get(key);
  if (cached) {
    return cached;
  }
  const persisted = readStorageJSON<GitCommitFilesPayload>(commitFilesStorageKey(rootId, commit, nid || undefined));
  if (persisted && Array.isArray(persisted.items)) {
    gitCommitFilesCache.set(key, persisted);
    return persisted;
  }
  const inflight = gitCommitFilesInflight.get(key);
  if (inflight) {
    return inflight;
  }
  const promise = protectedJSON<any>(
    appURL("/api/git/commit/files", new URLSearchParams({ root: rootId, commit }), nodeId),
  ).then((payload) => {
    const normalized = {
      commit: typeof payload?.commit === "string" ? payload.commit : commit,
      items: Array.isArray(payload?.items) ? payload.items as GitStatusItem[] : [],
    };
    gitCommitFilesCache.set(key, normalized);
    writeStorageJSON(commitFilesStorageKey(rootId, commit, nid || undefined), normalized);
    return normalized;
  }).finally(() => {
    gitCommitFilesInflight.delete(key);
  });
  gitCommitFilesInflight.set(key, promise);
  return promise;
}

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
    gitCommitDiffCache.set(key, persisted);
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
    gitCommitDiffCache.set(key, diff);
    writeStorageJSON(commitDiffStorageKey(rootId, commit, item.old_path || "", path, nid || undefined), diff);
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
