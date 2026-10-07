import type { ReadMode, FilePayload, CachedFileRecord, CachedGitDiffPayload, CachedGitDiffRecord, CacheRecord } from "./types";

const DB_NAME = "mindfs-file-cache";
const DB_VERSION = 1;
const STORE_NAME = "files";
export const GIT_DIFF_CACHE_VERSION = "v2";
const MAX_CACHE_ENTRIES = 200;
const LS_RECORD_PREFIX = "mindfs-file-cache-record:";
const LS_MAX_RECORD_BYTES = 256 * 1024;
const LS_MAX_RECORDS = 50;
// **总**字节预算。原来只有「条数」上限，50 × 256KB = 12.5MB，而 localStorage
// 只有 5MB：写到第~20 条 256KB 文件就QuotaExceededError，被 save 的 catch 吞掉
// —— 从那一刻起**所有**文件缓存静默失效，用户看不出原因。剪的方向本来是对的
// （按touchedAt 丢最旧），只是没算总量。
const LS_TOTAL_BYTES = 4 * 1024 * 1024;

export const memoryCache = new Map<string, FilePayload>();
export const gitDiffMemoryCache = new Map<string, CachedGitDiffPayload>();
// Avoid repeatedly requesting missing raw assets. File-change invalidation clears
// this cache immediately; the TTL only covers missed or disconnected WS events.
export const rawFileFailures = new Map<string, number>();
export const RAW_FILE_FAILURE_TTL_MS = 60_000;
export let dbPromise: Promise<IDBDatabase> | null = null;

export function buildCacheKey(rootId: string, path: string, readMode: ReadMode, cursor: number, nodeId?: string): string {
  const nid = String(nodeId || "").trim();
  const base = [rootId, path, readMode, String(cursor)].join("::");
  return nid ? `${nid}::${base}` : base;
}

export function buildGitDiffCacheKey(rootId: string, path: string, signature?: string, nodeId?: string): string {
  const nid = String(nodeId || "").trim();
  const base = ["git-diff", GIT_DIFF_CACHE_VERSION, rootId, path, signature || ""].join("::");
  return nid ? `${nid}::${base}` : base;
}

export function buildGitDiffCacheKeyPrefix(rootId: string, path: string, nodeId?: string): string {
  const nid = String(nodeId || "").trim();
  if (nid) return `${nid}::git-diff::${GIT_DIFF_CACHE_VERSION}::${rootId}::${path}::`;
  return `git-diff::${GIT_DIFF_CACHE_VERSION}::${rootId}::${path}::`;
}

export function buildCacheKeyPrefix(rootId: string, path: string, nodeId?: string): string {
  const nid = String(nodeId || "").trim();
  if (nid) return `${nid}::${rootId}::${path}::`;
  return `${rootId}::${path}::`;
}

export function buildRawFileFailureKey(rootId: string, path: string, nodeId?: string): string {
  const nid = String(nodeId || "").trim();
  if (nid) return `${nid}::${rootId}::${path}`;
  return `${rootId}::${path}`;
}

export function normalizeCursor(cursor?: number): number {
  return typeof cursor === "number" && cursor > 0 ? cursor : 0;
}

export function hasUsableCachedContent(file: FilePayload | null | undefined): boolean {
  if (!file) {
    return false;
  }
  if (typeof file.content === "string" && file.content.length > 0) {
    return true;
  }
  return file.encoding === "binary";
}

function getLocalStorageRecordKey(cacheKey: string): string {
  return `${LS_RECORD_PREFIX}${cacheKey}`;
}

function shouldPersistToLocalStorage(file: FilePayload): boolean {
  if (!hasUsableCachedContent(file)) {
    return false;
  }
  if (file.encoding === "binary") {
    return false;
  }
  return typeof file.content === "string" && file.content.length <= LS_MAX_RECORD_BYTES;
}

function loadCachedRecordFromLocalStorage(cacheKey: string): CachedFileRecord | null {
  if (typeof window === "undefined") {
    return null;
  }
  try {
    const raw = window.localStorage.getItem(getLocalStorageRecordKey(cacheKey));
    if (!raw) {
      return null;
    }
    const parsed = JSON.parse(raw) as CachedFileRecord | null;
    if (!parsed || parsed.key !== cacheKey || !parsed.file) {
      return null;
    }
    return parsed;
  } catch {
    return null;
  }
}

function saveCachedRecordToLocalStorage(record: CachedFileRecord): void {
  if (typeof window === "undefined") {
    return;
  }
  try {
    if (!shouldPersistToLocalStorage(record.file)) {
      window.localStorage.removeItem(getLocalStorageRecordKey(record.key));
      return;
    }
    window.localStorage.setItem(getLocalStorageRecordKey(record.key), JSON.stringify(record));
    pruneLocalStorageRecords();
  } catch {
  }
}

export function removeCachedRecordFromLocalStorage(cacheKey: string): void {
  if (typeof window === "undefined") {
    return;
  }
  try {
    window.localStorage.removeItem(getLocalStorageRecordKey(cacheKey));
  } catch {
  }
}

function listLocalStorageRecords(): CachedFileRecord[] {
  if (typeof window === "undefined") {
    return [];
  }
  const records: CachedFileRecord[] = [];
  try {
    for (let i = 0; i < window.localStorage.length; i += 1) {
      const key = window.localStorage.key(i);
      if (!key || !key.startsWith(LS_RECORD_PREFIX)) {
        continue;
      }
      const raw = window.localStorage.getItem(key);
      if (!raw) {
        continue;
      }
      try {
        const parsed = JSON.parse(raw) as CachedFileRecord | null;
        if (parsed?.key && parsed?.file) {
          records.push(parsed);
        }
      } catch {
      }
    }
  } catch {
  }
  return records;
}

function pruneLocalStorageRecords(): void {
  const records = listLocalStorageRecords();
  if (!records.length) {
    return;
  }
  // 三个上限同时生效：条数 LS_MAX_RECORDS、单条 LS_MAX_RECORD_BYTES、总字节
  // LS_TOTAL_BYTES。任一超出就丢最旧的（按 touchedAt，与会话缓存同一原则：
  // 丢用户最久没碰的，不丢正在看的）。
  //
  // 字节数**只算一次**并挂在记录上：recordBytes 要 JSON.stringify 整条记录
  // （单条可达 256KB），reduce 里算一遍、循环里再算一遍就是双倍无谓开销。
  const oldestFirst = records
    .slice()
    .sort((a, b) => a.touchedAt - b.touchedAt)
    .map((record) => ({ record, bytes: recordBytes(record) }));
  let remaining = oldestFirst.length;
  let total = oldestFirst.reduce((sum, entry) => sum + entry.bytes, 0);
  for (const entry of oldestFirst) {
    if (remaining <= LS_MAX_RECORDS && total <= LS_TOTAL_BYTES) {
      break;
    }
    total -= entry.bytes;
    remaining -= 1;
    removeCachedRecordFromLocalStorage(entry.record.key);
  }
}

/** 记录实际占用的字节数。JSON.stringify().length 是 UTF-16 码元数，对中文
 *  会低估三倍 —— 用 Blob 量真实字节，否则预算算不准。 */
function recordBytes(record: CachedFileRecord): number {
  try {
    return new Blob([JSON.stringify(record)]).size;
  } catch {
    return JSON.stringify(record).length * 2;
  }
}

function openDB(): Promise<IDBDatabase> {
  if (typeof window === "undefined" || !("indexedDB" in window)) {
    return Promise.reject(new Error("indexeddb unavailable"));
  }
  if (dbPromise) {
    return dbPromise;
  }
  dbPromise = new Promise((resolve, reject) => {
    const request = window.indexedDB.open(DB_NAME, DB_VERSION);
    request.onerror = () => reject(request.error || new Error("failed to open indexeddb"));
    request.onupgradeneeded = () => {
      const db = request.result;
      if (!db.objectStoreNames.contains(STORE_NAME)) {
        const store = db.createObjectStore(STORE_NAME, { keyPath: "key" });
        store.createIndex("touchedAt", "touchedAt", { unique: false });
      }
    };
    request.onsuccess = () => resolve(request.result);
  });
  return dbPromise;
}

function requestToPromise<T>(request: IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error || new Error("indexeddb request failed"));
  });
}

function withStore<T>(mode: IDBTransactionMode, run: (store: IDBObjectStore) => Promise<T>): Promise<T> {
  return openDB().then((db) => {
    const tx = db.transaction(STORE_NAME, mode);
    const store = tx.objectStore(STORE_NAME);
    const completion = new Promise<void>((resolve, reject) => {
      tx.oncomplete = () => resolve();
      tx.onerror = () => reject(tx.error || new Error("indexeddb transaction failed"));
      tx.onabort = () => reject(tx.error || new Error("indexeddb transaction aborted"));
    });
    return run(store).then(async (result) => {
      await completion;
      return result;
    });
  });
}

export function readMemoryCache(cacheKey: string): FilePayload | null {
  return memoryCache.get(cacheKey) || null;
}

export function writeMemoryCache(cacheKey: string, file: FilePayload): void {
  memoryCache.set(cacheKey, file);
}

export async function loadCachedRecord(cacheKey: string): Promise<CachedFileRecord | null> {
  const localRecord = loadCachedRecordFromLocalStorage(cacheKey);
  if (localRecord?.file) {
    return localRecord;
  }
  try {
    const record = await withStore("readonly", (store) =>
      requestToPromise(store.get(cacheKey) as IDBRequest<CacheRecord | undefined>),
    );
    return record?.type === "git-diff" ? null : record || null;
  } catch {
    return null;
  }
}

export async function loadCachedGitDiffRecord(cacheKey: string): Promise<CachedGitDiffRecord | null> {
  try {
    const record = await withStore("readonly", (store) =>
      requestToPromise(store.get(cacheKey) as IDBRequest<CacheRecord | undefined>),
    );
    return record?.type === "git-diff" ? record : null;
  } catch {
    return null;
  }
}

export async function saveCachedRecord(record: CachedFileRecord): Promise<void> {
  saveCachedRecordToLocalStorage(record);
  try {
    await withStore("readwrite", (store) => requestToPromise(store.put(record)));
  } catch {
  }
}

export async function saveCachedGitDiffRecord(record: CachedGitDiffRecord): Promise<void> {
  try {
    await withStore("readwrite", (store) => requestToPromise(store.put(record)));
  } catch {
  }
}

export async function deleteCachedRecords(match: (record: CacheRecord) => boolean): Promise<void> {
  try {
    await withStore("readwrite", async (store) => {
      const entries = (await requestToPromise(store.getAll() as IDBRequest<CacheRecord[]>)) || [];
      entries.forEach((entry) => {
        if (!match(entry)) {
          return;
        }
        store.delete(entry.key);
        if (entry.type === "git-diff") {
          gitDiffMemoryCache.delete(entry.key);
        } else {
          memoryCache.delete(entry.key);
          removeCachedRecordFromLocalStorage(entry.key);
        }
      });
    });
  } catch {
  }
}

export async function pruneCache(): Promise<void> {
  try {
    await withStore("readwrite", async (store) => {
      const entries = (await requestToPromise(store.getAll() as IDBRequest<CacheRecord[]>)) || [];
      if (entries.length <= MAX_CACHE_ENTRIES) {
        return;
      }
      entries
        .sort((a, b) => a.touchedAt - b.touchedAt)
        .slice(0, entries.length - MAX_CACHE_ENTRIES)
        .forEach((entry) => {
          store.delete(entry.key);
          if (entry.type === "git-diff") {
            gitDiffMemoryCache.delete(entry.key);
          } else {
            memoryCache.delete(entry.key);
            removeCachedRecordFromLocalStorage(entry.key);
          }
        });
    });
  } catch {
  }
}

export function clearSiblingMemoryCaches(rootId: string, path: string, keepKey: string): void {
  for (const key of memoryCache.keys()) {
    if (key === keepKey) continue;
    if (key.startsWith(`${rootId}::${path}::`) || key.includes(`::${rootId}::${path}::`)) {
      memoryCache.delete(key);
    }
  }
}

export async function clearSiblingPersistentCaches(rootId: string, path: string, keepKey: string): Promise<void> {
  await deleteCachedRecords((record) => {
    if (record.type === "git-diff") {
      return false;
    }
    if (record.key === keepKey) {
      return false;
    }
    return record.rootId === rootId && record.path === path;
  });
}

export async function persistExactCache(
  cacheKey: string,
  rootId: string,
  path: string,
  readMode: ReadMode,
  cursor: number,
  file: FilePayload,
): Promise<void> {
  writeMemoryCache(cacheKey, file);
  await saveCachedRecord({
    type: "file",
    key: cacheKey,
    rootId,
    path,
    readMode,
    cursor,
    touchedAt: Date.now(),
    file,
  });
  clearSiblingMemoryCaches(rootId, path, cacheKey);
  await clearSiblingPersistentCaches(rootId, path, cacheKey);
  void pruneCache();
}

export const rawFileBlobCache = new Map<string, Promise<Blob>>();
export const RAW_FILE_BLOB_CACHE_MAX = 100;
