import { appURL } from "../net/base";
import { getRootNodeId } from "../net/rootNode";
import { protectedJSON } from "../net/api";
import type { GitHistoryPayload, GitCommitFilesPayload, GitHistoryItem, GitDiffPayload, GitStatusItem } from "./types";

const DEFAULT_HISTORY_LIMIT = 10;
const HISTORY_LIST_STORAGE_PREFIX = "mindfs.git.history.list:";
const COMMIT_FILES_STORAGE_PREFIX = "mindfs.git.history.files:";
export const COMMIT_DIFF_STORAGE_PREFIX = "mindfs.git.history.diff.v2:";

type GitHistoryCacheEntry = {
  items: GitHistoryItem[];
  hasMore: boolean;
  remoteHead?: string;
};

const gitHistoryListCache = new Map<string, GitHistoryCacheEntry>();
const gitHistoryInflight = new Map<string, Promise<GitHistoryPayload>>();
const gitCommitFilesCache = new Map<string, GitCommitFilesPayload>();
const gitCommitFilesInflight = new Map<string, Promise<GitCommitFilesPayload>>();
export const gitCommitDiffCache = new Map<string, GitDiffPayload>();
export const gitCommitDiffInflight = new Map<string, Promise<GitDiffPayload>>();

// 这三个 Map 一律**有上限**。没有上限的缓存在长会话里只增不减，而
// gitCommitDiffCache 存的是**完整 diff 正文** —— 用户报的「标签页闪退」里，
// 长期开着标签页翻 git 历史就是一条真实的内存上涨路径。
//
// 淘汰取「最旧的那条」：Map 保持插入序，所以删第一条即近似 LRU。
// 上限对齐同仓库 file.ts 的 MAX_CACHE_ENTRIES（200），够翻历史用。
const gitCacheMaxEntries = 200;

export function setBounded<K, V>(cache: Map<K, V>, key: K, value: V): void {
  cache.set(key, value);
  if (cache.size <= gitCacheMaxEntries) return;
  const oldest = cache.keys().next();
  if (!oldest.done && oldest.value !== undefined) {
    cache.delete(oldest.value);
  }
}

// localStorage 里 commit diff / commit files 的**条数**上限。
// localStorage 是同步的且有配额，写超了 writeStorageJSON 会静默失败（try/catch 吞掉），
// 症状是「缓存莫名丢了」。这里封顶到 32 —— 翻 git 历史够用。
//
// 各浏览器按插入序存放，所以取 keys 数组的前半段删掉即「保留最近的 maxEntries 条」。
// 淘汰是近似的（没有可靠的插入序保证），但目的只是不让总量一直涨。
export const commitDiffStorageMaxEntries = 32;

export function capStorageByPrefix(prefix: string, maxEntries: number): void {
  if (!canUseStorage()) return;
  const keys: string[] = [];
  for (let i = 0; i < window.localStorage.length; i += 1) {
    const key = window.localStorage.key(i);
    if (key && key.startsWith(prefix)) keys.push(key);
  }
  if (keys.length <= maxEntries) return;
  for (const key of keys.slice(0, keys.length - maxEntries)) {
    window.localStorage.removeItem(key);
  }
}

function canUseStorage(): boolean {
  return typeof window !== "undefined" && !!window.localStorage;
}

export function readStorageJSON<T>(key: string): T | null {
  if (!canUseStorage()) return null;
  try {
    const raw = window.localStorage.getItem(key);
    return raw ? JSON.parse(raw) as T : null;
  } catch {
    return null;
  }
}

export function writeStorageJSON(key: string, value: unknown): void {
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

export function gitScopedRoot(rootId: string, nodeId?: string): string {
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

export function commitDiffStorageKey(rootId: string, commit: string, oldPath: string, path: string, nodeId?: string): string {
  return `${COMMIT_DIFF_STORAGE_PREFIX}${encodeURIComponent(gitScopedRoot(rootId, nodeId))}:${encodeURIComponent(commit)}:${encodeURIComponent(oldPath)}:${encodeURIComponent(path)}`;
}

function getHistoryCacheEntry(rootId: string, nodeId?: string): GitHistoryCacheEntry | null {
  const nid = String(nodeId || "").trim();
  const scoped = gitScopedRoot(rootId, nid);
  const cached = gitHistoryListCache.get(scoped);
  if (cached) {
    return cached;
  }
  // 兼容裸键迁移：旧版本以裸 rootId 缓存，命中后归到新键。
  // 只在**内存**里迁：持久层已删（见下），重启后没有旧数据可迁。
  if (nid) {
    const bareKey = String(rootId || "").trim();
    const bare = gitHistoryListCache.get(bareKey);
    if (bare) {
      setBounded(gitHistoryListCache, scoped, bare);
      gitHistoryListCache.delete(bareKey);
      return bare;
    }
  }
  return null;
}

// 提交列表**只**留内存缓存，不落localStorage（2026-10-05）。
//
// 它没有失效机制：别人 push、本机 commit 之后，持久层里的列表永远不会更新，
// 而 remoteHead 会让 UI 以为「已是最新」—— 症状是明明有新提交却看不到。
// 重新获取成本很低（服务端跑本地 git log），缓存收益远小于「列表停在过去」
// 的风险。commit 的文件列表与 diff 仍留持久层：那是单次提交视图，
// 不可变且可能很大。
function setHistoryCacheEntry(rootId: string, entry: GitHistoryCacheEntry, nodeId?: string): void {
  const scoped = gitScopedRoot(rootId, nodeId);
  setBounded(gitHistoryListCache, scoped, entry);
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
      // 历史列表已不落盘（setHistoryCacheEntry 只写内存），这两行是清理**旧版本**
      // 留下的死数据 —— 删了它们，老用户本地就不会再攒一份永不更新的陈旧列表。
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
    setBounded(gitCommitFilesCache, key, persisted);
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
    setBounded(gitCommitFilesCache, key, normalized);
    writeStorageJSON(commitFilesStorageKey(rootId, commit, nid || undefined), normalized);
    capStorageByPrefix(COMMIT_FILES_STORAGE_PREFIX, commitDiffStorageMaxEntries);
    return normalized;
  }).finally(() => {
    gitCommitFilesInflight.delete(key);
  });
  gitCommitFilesInflight.set(key, promise);
  return promise;
}
