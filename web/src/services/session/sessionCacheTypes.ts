import type { Session } from "./sessionTypes";
export type CachedSessionRecord = { cacheKey: string; rootId: string; nodeId?: string; sessionKey: string; touchedAt: number; session: Session };
export type CachedSessionListRecord<T> = { cacheKey: string; touchedAt: number; payload: T };
