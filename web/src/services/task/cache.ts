import type { TaskDetail } from "./types";

type CachedTaskRecord = {
  cacheKey: string;
  rootId: string;
  taskId: string;
  updatedAt: string;
  detail: TaskDetail;
};

type CachedTaskMeta = {
  key: string;
  rootId: string;
  newestUpdatedAt: string;
  oldestUpdatedAt: string;
  /**
   * 归档状态的水位（max archived_at）。必须与 updated_at 分开记：归档刻意不推动
   * updated_at（它是看板排序键 + 自动归档的兜底起算点），只按 updated_at 取增量，
   * 服务端归档过的行永远不会再发回来，缓存里那张卡就一直停在「完成」列。
   */
  newestArchivedAt: string;
  lastSyncedAt: string;
};

/**
 * 一次响应里两种变化的各自最新值。
 *
 * 不能合成一个 max：归档之后任务又更新过的话，行的 updated_at 会盖过 archived_at，
 * 落后的客户端就再也收不到这条归档变化了。
 */
export function taskChangeWatermarks(details: TaskDetail[]): { newestUpdatedAt: string; newestArchivedAt: string } {
  let newestUpdatedAt = "";
  let newestArchivedAt = "";
  for (const detail of details) {
    const updatedAt = String(detail?.task?.updated_at || "");
    if (updatedAt > newestUpdatedAt) newestUpdatedAt = updatedAt;
    const archivedAt = String(detail?.task?.archived_at || "");
    if (archivedAt > newestArchivedAt) newestArchivedAt = archivedAt;
  }
  return { newestUpdatedAt, newestArchivedAt };
}

/**
 * 老缓存 meta 缺 `newestArchivedAt`（本字段是后加的）时的一次性补齐水位。
 *
 * 不补的话，升级前写下的缓存永远不会去问「哪些行被归档过」：服务端早就归档、
 * 客户端还把它们当活动卡 —— 而且这批行的 updated_at 也不会再动，增量永远够不着。
 */
export const TASK_ARCHIVE_BACKFILL_WATERMARK = "1970-01-01T00:00:00Z";

/**
 * 增量同步的过滤条件（不含状态/分页）。force / 无缓存 = 全量。
 *
 * 两个水位一起给：看板要的是「任务更新过、或被归档过」的行 —— 归档是唯一一种
 * 不推动 updated_at 的变化（自动归档那一拍更是没有任何 HTTP 响应能让界面知道）。
 */
export function taskSyncFilters(
  meta: { newestUpdatedAt?: string; newestArchivedAt?: string } | null,
  force: boolean,
): { after?: string; archivedAfter?: string } | undefined {
  if (force || !meta) return undefined;
  const after = String(meta.newestUpdatedAt || "");
  const archivedAfter = meta.newestArchivedAt === undefined
    ? TASK_ARCHIVE_BACKFILL_WATERMARK
    : String(meta.newestArchivedAt || "");
  if (!after && !archivedAfter) return undefined;
  return { after, archivedAfter };
}

const TASK_CACHE_DB = "mindfs-task-cache";
const TASK_CACHE_VERSION = 1;
const TASK_STORE = "tasks";
const TASK_META_STORE = "meta";

function taskCacheKey(rootId: string, taskId: string, nodeId?: string): string {
  const nid = String(nodeId || "").trim();
  return nid ? `${nid}::${rootId}::${taskId}` : `${rootId}::${taskId}`;
}

function taskMetaKey(rootId: string, nodeId?: string): string {
  const nid = String(nodeId || "").trim();
  return nid ? `${nid}::root::${rootId}` : `root::${rootId}`;
}


function taskRequest<T>(request: IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error || new Error("indexeddb request failed"));
  });
}

function openTaskCacheDB(): Promise<IDBDatabase> {
  if (typeof window === "undefined" || !("indexedDB" in window)) {
    return Promise.reject(new Error("indexeddb unavailable"));
  }
  return new Promise((resolve, reject) => {
    const request = window.indexedDB.open(TASK_CACHE_DB, TASK_CACHE_VERSION);
    request.onupgradeneeded = () => {
      const db = request.result;
      if (!db.objectStoreNames.contains(TASK_STORE)) {
        const store = db.createObjectStore(TASK_STORE, { keyPath: "cacheKey" });
        store.createIndex("rootId", "rootId", { unique: false });
        store.createIndex("updatedAt", "updatedAt", { unique: false });
      }
      if (!db.objectStoreNames.contains(TASK_META_STORE)) {
        db.createObjectStore(TASK_META_STORE, { keyPath: "key" });
      }
    };
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error || new Error("indexeddb open failed"));
  });
}

async function withTaskStore<T>(
  mode: IDBTransactionMode,
  fn: (stores: { tasks: IDBObjectStore; meta: IDBObjectStore }) => Promise<T> | T,
): Promise<T> {
  const db = await openTaskCacheDB();
  try {
    const tx = db.transaction([TASK_STORE, TASK_META_STORE], mode);
    const result = await fn({
      tasks: tx.objectStore(TASK_STORE),
      meta: tx.objectStore(TASK_META_STORE),
    });
    await new Promise<void>((resolve, reject) => {
      tx.oncomplete = () => resolve();
      tx.onerror = () => reject(tx.error || new Error("indexeddb transaction failed"));
      tx.onabort = () => reject(tx.error || new Error("indexeddb transaction aborted"));
    });
    return result;
  } finally {
    db.close();
  }
}

export async function getCachedTaskDetails(rootId: string, nodeId?: string): Promise<TaskDetail[]> {
  const nid = String(nodeId || "").trim();
  try {
    return await withTaskStore("readonly", async ({ tasks }) => {
      const index = tasks.index("rootId");
      const records = await taskRequest(index.getAll(rootId) as IDBRequest<CachedTaskRecord[]>);
      if (nid) {
        const scoped = records.filter((r) => String(r.cacheKey || "").startsWith(`${nid}::`));
        if (scoped.length > 0) {
          const byId = new Map<string, CachedTaskRecord>();
          for (const rec of scoped) {
            const prev = byId.get(rec.taskId);
            if (!prev || String(rec.updatedAt || "") > String(prev.updatedAt || "")) byId.set(rec.taskId, rec);
          }
          return Array.from(byId.values())
            .map((record) => record.detail)
            .filter((detail) => detail?.task?.id)
            .sort((a, b) => String(b.task.updated_at || "").localeCompare(String(a.task.updated_at || "")));
        }
        return [];
      }
      const byId = new Map<string, CachedTaskRecord>();
      for (const rec of records) {
        const prev = byId.get(rec.taskId);
        if (!prev || String(rec.updatedAt || "") > String(prev.updatedAt || "")) byId.set(rec.taskId, rec);
      }
      return Array.from(byId.values())
        .map((record) => record.detail)
        .filter((detail) => detail?.task?.id)
        .sort((a, b) => String(b.task.updated_at || "").localeCompare(String(a.task.updated_at || "")));
    });
  } catch {
    return [];
  }
}

export async function getCachedTaskMeta(rootId: string, nodeId?: string): Promise<CachedTaskMeta | null> {
  const nid = String(nodeId || "").trim();
  try {
    return await withTaskStore("readonly", async ({ meta }) => {
      if (nid) {
        const scoped = await taskRequest(meta.get(taskMetaKey(rootId, nid)) as IDBRequest<CachedTaskMeta | undefined>);
        if (scoped) return scoped;
      }
      const value = await taskRequest(meta.get(taskMetaKey(rootId)) as IDBRequest<CachedTaskMeta | undefined>);
      return value || null;
    });
  } catch {
    return null;
  }
}

export async function upsertCachedTaskDetails(rootId: string, details: TaskDetail[], nodeId?: string): Promise<void> {
  const valid = details.filter((detail) => detail?.task?.id);
  if (valid.length === 0) return;
  const nid = String(nodeId || "").trim();
  try {
    await withTaskStore("readwrite", async ({ tasks, meta }) => {
      const metaKey = taskMetaKey(rootId, nid || undefined);
      let currentMeta = await taskRequest(meta.get(metaKey) as IDBRequest<CachedTaskMeta | undefined>);
      const updatedValues = valid
        .map((detail) => String(detail.task.updated_at || ""))
        .filter(Boolean);
      for (const detail of valid) {
        const taskId = detail.task.id;
        await taskRequest(tasks.put({
          cacheKey: taskCacheKey(rootId, taskId, nid || undefined),
          rootId,
          taskId,
          updatedAt: String(detail.task.updated_at || ""),
          detail,
        } satisfies CachedTaskRecord));
      }
      const changed = taskChangeWatermarks(valid);
      const newest = changed.newestUpdatedAt > (currentMeta?.newestUpdatedAt || "")
        ? changed.newestUpdatedAt
        : (currentMeta?.newestUpdatedAt || "");
      const newestArchivedAt = changed.newestArchivedAt > (currentMeta?.newestArchivedAt || "")
        ? changed.newestArchivedAt
        : (currentMeta?.newestArchivedAt || "");
      const oldest = updatedValues.reduce((min, value) => !min || value < min ? value : min, currentMeta?.oldestUpdatedAt || "");
      currentMeta = {
        key: metaKey,
        rootId,
        newestUpdatedAt: newest,
        oldestUpdatedAt: oldest,
        newestArchivedAt,
        lastSyncedAt: new Date().toISOString(),
      };
      await taskRequest(meta.put(currentMeta));
    });
  } catch {}
}

/**
 * 淘汰「服务端已经没有、缓存里还留着」的任务。
 *
 * 缓存以前只 put 不 delete，配合增量拉取（after=newestUpdatedAt，只取更新的行）
 * 就等于把删除永久吞掉：任务在服务端消失后，响应里永远不会出现它，缓存就一直
 * 喂给你，跨设备还各存各的。实测手机上看得到 #11/#13，而两台机器的库里都查不到。
 *
 * keepTaskIds 必须是**全量**响应的集合（不带 after / limit，服务端会返回该 root
 * 的全部任务，见 task_store.go:275）。拿增量或限流响应当权威集合会误删。
 */
export async function pruneCachedTaskDetails(rootId: string, keepTaskIds: Iterable<string>, nodeId?: string): Promise<string[]> {
  const keep = new Set(Array.from(keepTaskIds, (id) => String(id || "")).filter(Boolean));
  const nid = String(nodeId || "").trim();
  try {
    return await withTaskStore("readwrite", async ({ tasks }) => {
      const index = tasks.index("rootId");
      const records = await taskRequest(index.getAll(rootId) as IDBRequest<CachedTaskRecord[]>);
      // 按 root + node 圈定：同名项目跨节点时各存各的，淘汰不能波及另一台机器。
      const scoped = nid ? records.filter((r) => String(r.cacheKey || "").startsWith(`${nid}::`)) : records;
      const dropped: string[] = [];
      for (const rec of scoped) {
        if (keep.has(String(rec.taskId || ""))) continue;
        dropped.push(String(rec.taskId || ""));
        await taskRequest(tasks.delete(rec.cacheKey));
      }
      return dropped;
    });
  } catch {
    return [];
  }
}
