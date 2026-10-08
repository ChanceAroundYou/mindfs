import { currentUser } from "./net/authGate";
import { scopeSessionKey } from "../shared/scope";
import type { CachedSessionListRecord, CachedSessionRecord } from "./sessionCacheTypes";

const SESSION_CACHE_DB = "mindfs-session-cache";
const SESSION_CACHE_STORE = "sessions";
const SESSION_LIST_CACHE_STORE = "session-lists";
const SESSION_CACHE_VERSION = 3;

// 多项目会话列表的缓存键必须带账户：这是「跨账户串号」的入口。
// 换账户后这里若仍是常量，上一账户的全量会话会在发请求前先被渲染出来
// （App.tsx loadMultiProjectSessionGroups 先 setMultiProjectSessionGroups(缓存)），
// 而账户作用域请求回来的空 groups 只是合并进 prev、并不清空它 → 新账户看到旧账户的全部会话。
// 用 username 而非 id：同源与跨机器两种情形下它都是稳定且可读的那个标识。
export function multiRootSessionListCacheKey(): string {
  const name = String(currentUser()?.username || "").trim();
  return name ? `multi-root::${name}` : "multi-root";
}

let sessionDBPromise: Promise<IDBDatabase> | null = null;

export function buildSessionCacheKey(
  rootId: string,
  sessionKey: string,
  nodeId?: string,
): string {
  return scopeSessionKey(String(nodeId || "").trim(), rootId, sessionKey);
}

export function openSessionDB(): Promise<IDBDatabase> {
  if (typeof window === "undefined" || !("indexedDB" in window)) {
    return Promise.reject(new Error("indexeddb unavailable"));
  }
  if (sessionDBPromise) {
    return sessionDBPromise;
  }
  sessionDBPromise = new Promise((resolve, reject) => {
    const request = window.indexedDB.open(
      SESSION_CACHE_DB,
      SESSION_CACHE_VERSION,
    );
    request.onerror = () =>
      reject(request.error || new Error("failed to open indexeddb"));
    request.onupgradeneeded = (event) => {
      const db = request.result;
      if (
        (event as IDBVersionChangeEvent).oldVersion < 2 &&
        db.objectStoreNames.contains(SESSION_CACHE_STORE)
      ) {
        db.deleteObjectStore(SESSION_CACHE_STORE);
      }
      if (!db.objectStoreNames.contains(SESSION_CACHE_STORE)) {
        db.createObjectStore(SESSION_CACHE_STORE, { keyPath: "cacheKey" });
      }
      if (!db.objectStoreNames.contains(SESSION_LIST_CACHE_STORE)) {
        db.createObjectStore(SESSION_LIST_CACHE_STORE, { keyPath: "cacheKey" });
      }
    };
    request.onsuccess = () => resolve(request.result);
  });
  return sessionDBPromise;
}

export function sessionRequestToPromise<T>(request: IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    request.onsuccess = () => resolve(request.result);
    request.onerror = () =>
      reject(request.error || new Error("indexeddb request failed"));
  });
}

export function withSessionStore<T>(
  mode: IDBTransactionMode,
  run: (store: IDBObjectStore) => Promise<T>,
): Promise<T> {
  return openSessionDB().then((db) => {
    const tx = db.transaction(SESSION_CACHE_STORE, mode);
    const store = tx.objectStore(SESSION_CACHE_STORE);
    const completion = new Promise<void>((resolve, reject) => {
      tx.oncomplete = () => resolve();
      tx.onerror = () =>
        reject(tx.error || new Error("indexeddb transaction failed"));
      tx.onabort = () =>
        reject(tx.error || new Error("indexeddb transaction aborted"));
    });
    return run(store).then(async (result) => {
      await completion;
      return result;
    });
  });
}

export function withSessionListStore<T>(
  mode: IDBTransactionMode,
  run: (store: IDBObjectStore) => Promise<T>,
): Promise<T> {
  return openSessionDB().then((db) => {
    const tx = db.transaction(SESSION_LIST_CACHE_STORE, mode);
    const store = tx.objectStore(SESSION_LIST_CACHE_STORE);
    const completion = new Promise<void>((resolve, reject) => {
      tx.oncomplete = () => resolve();
      tx.onerror = () =>
        reject(tx.error || new Error("indexeddb transaction failed"));
      tx.onabort = () =>
        reject(tx.error || new Error("indexeddb transaction aborted"));
    });
    return run(store).then(async (result) => {
      await completion;
      return result;
    });
  });
}

// 单项目列表缓存同理必须带账户（见 multiRootSessionListCacheKey 的说明）：
// 不带的话「A 账户看过 P 项目 → 切到 B 账户 → 点开 P」会先渲染出 A 的会话列表。
export function sessionListCacheScope(): string {
  const name = String(currentUser()?.username || "").trim();
  return name ? `${name}::` : "";
}

export function buildSessionListCacheKey(rootId: string, nodeId?: string): string {
  const nid = String(nodeId || "").trim();
  const scope = sessionListCacheScope();
  return nid ? `${scope}${nid}::${rootId}` : `${scope}root::${rootId}`;
}

export async function readCachedSessionList<T>(cacheKey: string): Promise<T | null> {
  try {
    const record = await withSessionListStore("readonly", (store) =>
      sessionRequestToPromise(
        store.get(cacheKey) as IDBRequest<CachedSessionListRecord<T> | undefined>,
      ),
    );
    return record?.payload || null;
  } catch {
    return null;
  }
}

export async function writeCachedSessionList<T>(cacheKey: string, payload: T): Promise<void> {
  try {
    await withSessionListStore("readwrite", (store) =>
      sessionRequestToPromise(store.put({
        cacheKey,
        touchedAt: Date.now(),
        payload,
      } satisfies CachedSessionListRecord<T>)),
    );
  } catch {}
}

