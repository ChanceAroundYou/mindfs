import type { MultiRootSessionGroup, Session, SessionListPayload } from "./session";
import {
  buildSessionListCacheKey,
  multiRootSessionListCacheKey,
  readCachedSessionList,
  sessionListCacheScope,
  writeCachedSessionList,
} from "./sessionIndexedDb";

// 会话**列表**的缓存读写（从 services/session.ts 抽出，2026-10-07）。
// 与 sessionCacheStore（单会话）分开：这里的键是「账户 + 节点 + 项目」，
// 值是一页列表的摘要，不是整份会话。
export function getCachedSessionList(rootId: string, nodeId?: string): Promise<SessionListPayload | null> {
  if (!rootId) return Promise.resolve(null);
  const nid = String(nodeId || "").trim();
  const primary = buildSessionListCacheKey(rootId, nid || undefined);
  return readCachedSessionList<SessionListPayload>(primary).then((hit) => {
    if (hit) return hit;
    return null;
  });
}

export function saveCachedSessionList(rootId: string, payload: SessionListPayload, nodeId?: string): Promise<void> {
  if (!rootId) return Promise.resolve();
  return writeCachedSessionList(buildSessionListCacheKey(rootId, nodeId), payload);
}

export function getCachedMultiRootSessionList(): Promise<MultiRootSessionGroup[] | null> {
  return readCachedSessionList<MultiRootSessionGroup[]>(multiRootSessionListCacheKey());
}

export function saveCachedMultiRootSessionList(groups: MultiRootSessionGroup[]): Promise<void> {
  return writeCachedSessionList(multiRootSessionListCacheKey(), groups);
}
