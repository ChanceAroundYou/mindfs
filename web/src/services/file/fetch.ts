import { appURL } from "../net/base";
import { e2eeService } from "../net/e2ee";
import { getRootNodeId } from "../net/rootNode";
import type { ReadMode, FilePayload, FetchFileParams, CachedGitDiffPayload, FileResponse } from "./types";
import {
  buildCacheKey,
  buildGitDiffCacheKey,
  buildGitDiffCacheKeyPrefix,
  buildCacheKeyPrefix,
  buildRawFileFailureKey,
  normalizeCursor,
  readMemoryCache,
  writeMemoryCache,
  loadCachedRecord,
  loadCachedGitDiffRecord,
  saveCachedRecord,
  saveCachedGitDiffRecord,
  deleteCachedRecords,
  removeCachedRecordFromLocalStorage,
  pruneCache,
  clearSiblingMemoryCaches,
  clearSiblingPersistentCaches,
  persistExactCache,
  hasUsableCachedContent,
  memoryCache,
  gitDiffMemoryCache,
  rawFileFailures,
  rawFileBlobCache,
  GIT_DIFF_CACHE_VERSION,
} from "./cache";

export function buildFileURL(rootId: string, path: string, readMode: ReadMode, cursor: number, mtime?: string, nodeId?: string): string {
  const queryParams = new URLSearchParams({
    root: rootId,
    path,
    read: readMode,
  });
  if (cursor > 0) {
    queryParams.set("cursor", String(cursor));
  }
  if (mtime) {
    queryParams.set("mtime", mtime);
  }
  return appURL("/api/file", queryParams, nodeId);
}

export function createFetchOptions(timeoutMs?: number): {
  controller: AbortController | null;
  timer: number | null;
  init?: RequestInit;
} {
  if (!timeoutMs || timeoutMs <= 0) {
    return { controller: null, timer: null };
  }
  const controller = new AbortController();
  const timer = window.setTimeout(() => controller.abort(), timeoutMs);
  return {
    controller,
    timer,
    init: { signal: controller.signal },
  };
}

export async function fetchResponse(url: string, init?: RequestInit): Promise<Response> {
  return fetch(url, init);
}

export async function getCachedFile(params: Omit<FetchFileParams, "timeoutMs">): Promise<FilePayload | null> {
  const readMode = params.readMode || "incremental";
  const cursor = normalizeCursor(params.cursor);
  const nid = String((params as any).nodeId || getRootNodeId(params.rootId) || "").trim();
  const cacheKey = buildCacheKey(params.rootId, params.path, readMode, cursor, nid || undefined);
  if (nid) {
    const inMemory = readMemoryCache(cacheKey);
    if (inMemory) return inMemory;
    const record = await loadCachedRecord(cacheKey);
    if (!record?.file) return null;
    return record.file;
  }
  const inMemory = readMemoryCache(cacheKey);
  if (inMemory) return inMemory;
  let record = await loadCachedRecord(cacheKey);
  if (!record?.file) return null;
  return record.file;
}

export function invalidateFileCache(rootId: string, path: string): void {
  rawFileFailures.delete(buildRawFileFailureKey(rootId, path));
  // 跨节点隔离：同时清掉 nid:: 前缀的残留
  for (const key of Array.from(rawFileFailures.keys())) {
    if (key.endsWith(`::${rootId}::${path}`)) rawFileFailures.delete(key);
  }
  // raw blob 缓存（图片等）无 TTL，文件更新后必须随失效流程清除，否则同路径旧图持续复用
  for (const key of Array.from(rawFileBlobCache.keys())) {
    if (key.startsWith(`${rootId}:${path}:`)) rawFileBlobCache.delete(key);
  }
  const prefix = buildCacheKeyPrefix(rootId, path);
  const diffPrefix = buildGitDiffCacheKeyPrefix(rootId, path);
  for (const key of Array.from(memoryCache.keys())) {
    if (key.startsWith(prefix) || key.includes(`::${rootId}::${path}::`)) {
      memoryCache.delete(key);
      removeCachedRecordFromLocalStorage(key);
    }
  }
  for (const key of Array.from(gitDiffMemoryCache.keys())) {
    if (key.startsWith(diffPrefix) || key.includes(`::${rootId}::${path}::`)) {
      gitDiffMemoryCache.delete(key);
    }
  }
  void deleteCachedRecords((record) => record.rootId === rootId && record.path === path);
}

export function clearFileCacheForRoot(rootId: string): void {
  const prefix = `${rootId}::`;
  const diffPrefix = `git-diff::${GIT_DIFF_CACHE_VERSION}::${rootId}::`;
  for (const key of rawFileFailures.keys()) {
    if (key.startsWith(prefix) || key.includes(`::${rootId}::`)) {
      rawFileFailures.delete(key);
    }
  }
  for (const key of memoryCache.keys()) {
    if (key.startsWith(prefix) || key.includes(`::${rootId}::`)) {
      memoryCache.delete(key);
      removeCachedRecordFromLocalStorage(key);
    }
  }
  for (const key of gitDiffMemoryCache.keys()) {
    if (key.startsWith(diffPrefix) || key.includes(`::${rootId}::`)) {
      gitDiffMemoryCache.delete(key);
    }
  }
  void deleteCachedRecords((record) => record.rootId === rootId);
}

export function clearFileMemoryCacheForView(): void {
  for (const key of Array.from(memoryCache.keys())) {
    memoryCache.delete(key);
    removeCachedRecordFromLocalStorage(key);
  }
  for (const key of Array.from(gitDiffMemoryCache.keys())) {
    gitDiffMemoryCache.delete(key);
  }
  rawFileFailures.clear();
  rawFileBlobCache.clear();
}

export async function getCachedGitDiff(
  rootId: string,
  path: string,
  signature?: string,
  nodeId?: string,
): Promise<CachedGitDiffPayload | null> {
  const nid = String(nodeId || getRootNodeId(rootId) || "").trim();
  const cacheKey = buildGitDiffCacheKey(rootId, path, signature, nid || undefined);
  if (nid) {
    const inMemory = gitDiffMemoryCache.get(cacheKey);
    if (inMemory) return inMemory;
    const record = await loadCachedGitDiffRecord(cacheKey);
    if (!record?.diff) return null;
    return record.diff;
  }
  const inMemory = gitDiffMemoryCache.get(cacheKey);
  if (inMemory) return inMemory;
  let record = await loadCachedGitDiffRecord(cacheKey);
  if (!record?.diff) return null;
  return record.diff;
}

export async function setCachedGitDiff(
  rootId: string,
  path: string,
  diff: CachedGitDiffPayload,
  signature?: string,
  nodeId?: string,
): Promise<void> {
  const nid = String(nodeId || getRootNodeId(rootId) || "").trim();
  const cacheKey = buildGitDiffCacheKey(rootId, path, signature, nid || undefined);
  gitDiffMemoryCache.set(cacheKey, diff);
  await saveCachedGitDiffRecord({
    type: "git-diff",
    key: cacheKey,
    rootId,
    path,
    touchedAt: Date.now(),
    diff,
  });
  void pruneCache();
}

export async function fetchFile(params: FetchFileParams): Promise<FilePayload | null> {
  params = { ...params, nodeId: params.nodeId || getRootNodeId(params.rootId) };
  const readMode = params.readMode || "incremental";
  const cursor = normalizeCursor(params.cursor);
  const nid = String(params.nodeId || "").trim();
  const cacheKey = buildCacheKey(params.rootId, params.path, readMode, cursor, nid || undefined);
  const cachedFile = await getCachedFile({
    rootId: params.rootId,
    path: params.path,
    readMode,
    cursor,
    nodeId: params.nodeId,
  } as any);
  const validationMTime =
    !params.fresh && hasUsableCachedContent(cachedFile) && typeof cachedFile?.mtime === "string" && cachedFile.mtime
      ? cachedFile.mtime
      : "";
  const request = createFetchOptions(params.timeoutMs);

  try {
    const requestURL = buildFileURL(params.rootId, params.path, readMode, cursor, validationMTime || undefined, params.nodeId);
    const headers = e2eeService.isRequired()
      ? await e2eeService.fileProofHeaders("GET", requestURL)
      : undefined;
    const response = await fetchResponse(
      requestURL,
      { ...request.init, headers },
    );

    if (response.status === 304) {
      if (cachedFile) {
        return cachedFile;
      }
      const record = await loadCachedRecord(cacheKey);
      if (hasUsableCachedContent(record?.file)) {
        writeMemoryCache(cacheKey, record!.file);
        return record!.file;
      }
      const retryURL = buildFileURL(params.rootId, params.path, readMode, cursor, undefined, params.nodeId);
      const retry = await fetchResponse(
        retryURL,
        {
          ...request.init,
          headers: e2eeService.isRequired()
            ? await e2eeService.fileProofHeaders("GET", retryURL)
            : headers,
        },
      );
      if (!retry.ok) {
        throw new Error(`open file failed after 304 retry: status=${retry.status}`);
      }
      const retryPayload = await e2eeService.parseProtectedJSONResponse<FileResponse>(retry);
      const retryFile = await unwrapFileResponse(retryPayload);
      if (!retryFile) {
        return null;
      }
      await persistExactCache(cacheKey, params.rootId, params.path, readMode, cursor, retryFile);
      return retryFile;
    }

    if (!response.ok) {
      if (response.status === 401 && e2eeService.isRequired()) {
        const payload = (await response.json().catch(() => ({}))) as { error?: string };
        if (e2eeService.handleServerError(String(payload.error || ""))) {
          return fetchFile(params);
        }
      }
      throw new Error(`open file failed: status=${response.status}`);
    }

    const payload = await e2eeService.parseProtectedJSONResponse<FileResponse>(response);
    const file = await unwrapFileResponse(payload);
    if (!file) {
      return null;
    }

    await persistExactCache(cacheKey, params.rootId, params.path, readMode, cursor, file);
    return file;
  } finally {
    if (request.timer !== null) {
      window.clearTimeout(request.timer);
    }
  }
}

async function unwrapFileResponse(payload: FileResponse): Promise<FilePayload | null> {
  if (payload?.file) {
    return payload.file;
  }
  return null;
}
