import { appURL } from "../net/base";
import { getRootNodeId } from "../net/rootNode";
import type { FilePayload } from "./types";
import { createFetchOptions, buildFileURL, fetchResponse, invalidateFileCache } from "./fetch";
import { rawFileFailures, RAW_FILE_FAILURE_TTL_MS, rawFileBlobCache, RAW_FILE_BLOB_CACHE_MAX } from "./cache";

// 成功 blob 缓存：多图 Markdown 中同一路径的图片在组件重挂载/重复渲染时复用，避免重复请求。
// 缓存的是 Promise（并发去重：同 key 同时发起只打一次网络），LRU 上限逐出；页面刷新即清空。
// ponytail: 无 TTL，文件内容更新后同路径在缓存逐出前仍返回旧图；如需要可加时间戳失效。


export function fetchProofProtectedBlob(params: {
  rootId: string;
  path: string;
  timeoutMs?: number;
  nodeId?: string;
}): Promise<Blob> {
  params = { ...params, nodeId: params.nodeId || getRootNodeId(params.rootId) };
  const cacheKey = `${params.rootId}:${params.path}:${params.nodeId || ""}`;
  const cached = rawFileBlobCache.get(cacheKey);
  if (cached) return cached;
  const promise = doFetchProofProtectedBlob(params, cacheKey);
  rawFileBlobCache.set(cacheKey, promise);
  if (rawFileBlobCache.size > RAW_FILE_BLOB_CACHE_MAX) {
    const oldestKey = rawFileBlobCache.keys().next().value;
    if (oldestKey !== undefined) {
      rawFileBlobCache.delete(oldestKey);
    }
  }
  promise.catch(() => {
    if (rawFileBlobCache.get(cacheKey) === promise) {
      rawFileBlobCache.delete(cacheKey);
    }
  });
  return promise;
}

async function doFetchProofProtectedBlob(
  params: { rootId: string; path: string; timeoutMs?: number; nodeId?: string },
  cacheKey: string,
): Promise<Blob> {
  const request = createFetchOptions(params.timeoutMs);
  try {
    const failedAt = rawFileFailures.get(cacheKey);
    if (failedAt !== undefined) {
      if (Date.now() - failedAt < RAW_FILE_FAILURE_TTL_MS) {
        throw new Error("open raw file failed: status=404 (cached)");
      }
      rawFileFailures.delete(cacheKey);
    }
    const baseURL = buildFileURL(params.rootId, params.path, "full", 0, undefined, params.nodeId);
    const rawURL = withRawFlag(
      baseURL,
    );
    const response = await fetchResponse(rawURL, request.init);
    if (!response.ok) {
      if (response.status === 404) {
        rawFileFailures.set(cacheKey, Date.now());
      }
      throw new Error(`open raw file failed: status=${response.status}`);
    }
    rawFileFailures.delete(cacheKey);
    return response.blob();
  } finally {
    if (request.timer !== null) {
      window.clearTimeout(request.timer);
    }
  }
}

function withRawFlag(url: string): string {
  const target = new URL(url, window.location.origin);
  target.searchParams.set("raw", "1");
  if (target.origin === window.location.origin) {
    return `${target.pathname}${target.search}`;
  }
  return target.toString();
}

// Editor reads always bypass the preview cache and request a bounded, full snapshot.
export async function fetchEditableFile(rootId: string, path: string): Promise<FilePayload & { revision: string }> {
  const url = appURL("/api/file", new URLSearchParams({ root: rootId, path, read: "full", edit: "1" }));
  return requestEditableFile(url, { cache: "no-store" });
}

export async function saveTextFile(rootId: string, path: string, content: string, revision: string): Promise<FilePayload & { revision: string }> {
  const url = appURL("/api/file", new URLSearchParams({ root: rootId, path }));
  const file = await requestEditableFile(url, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ content, base_revision: revision }),
  });
  invalidateFileCache(rootId, path);
  return file;
}

async function requestEditableFile(url: string, init: RequestInit): Promise<FilePayload & { revision: string }> {
  const response = await fetch(url, init);
  const payload = (await response.json()) as { file?: FilePayload & { revision: string }; error?: string };
  if (!response.ok) {
    if (response.status === 409) throw new Error("file_edit_conflict");
    if (response.status === 413) throw new Error("file_edit_too_large");
    throw new Error(payload.error || `HTTP ${response.status}`);
  }
  if (!payload.file?.revision) throw new Error("file_not_editable");
  return payload.file;
}
