import type { Session, SessionWindow, SessionWindowOptions, SyncSessionResult } from "./session";
// sessionService 的实例仍住在 session.ts（那是整条 WS 传输的所在）。这里只在
// **调用时**解引用，不在模块顶层碰它 —— 所以这条环是安全的。
import { sessionService } from "./session";
import { cloneSession, getCachedSession, saveCachedSession } from "./sessionCacheStore";
import { appendSessionDelta, getSessionMaxSeq, toPersistentExchangeAux, withSessionMeta } from "./sessionExchangeMerge";

// 窗口化视图的内存标记与增量同步（从 services/session.ts 抽出，2026-10-07）。
//
// 这一簇决定「打开一个会话时读多少」：窗口态下 syncSession 走增量复用、
// 跳过 truncated 全量回补；清掉标记即回退全量路径（灰度回滚开关）。



/**
 * 窗口化视图内存标记（方案 B）。SessionViewer 进入窗口视图时置位，
 * 使 syncSession 走增量复用、跳过 truncated 全量回补、且不写 truncated 标记。
 * 清标记即回退全量路径（见 3.3 / 8 灰度回滚）。
 */
const windowedViewKeys = new Set<string>();

export function isWindowedView(sessionKey: string): boolean {
  return windowedViewKeys.has(sessionKey);
}

export function setWindowedView(sessionKey: string, on = true): void {
  if (on) {
    windowedViewKeys.add(sessionKey);
  } else {
    windowedViewKeys.delete(sessionKey);
  }
}

export function clearWindowedView(sessionKey: string): void {
  windowedViewKeys.delete(sessionKey);
}

export async function syncSession(
  rootId: string,
  sessionKey: string,
  options?: { full?: boolean; nodeId?: string; windowedView?: boolean },
): Promise<SyncSessionResult> {
  const base = await getCachedSession(rootId, sessionKey, options?.nodeId);
  // 窗口化视图（内存标记或显式选项）以增量复用为主：跳过 truncated 全量回补，
  // 也不写 truncated 标记（见 3.3 / 8 灰度回滚）。全量路径仍保留 truncated→全量拉取。
  const windowed = isWindowedView(sessionKey) || !!options?.windowedView;
  const baseTruncated = windowed ? false : !!(base as any)?.truncated;
  const seq = baseTruncated ? 0 : getSessionMaxSeq(base);
  // truncated 只需轻量 GET 全量（非 syncExternalSession 的手动转录同步重端点）。
  const incoming = baseTruncated
    ? await sessionService.getSession(rootId, sessionKey, 0, options?.nodeId)
    : options?.full
      ? await sessionService.syncExternalSession(rootId, sessionKey, seq, options?.nodeId)
      : await sessionService.getSession(rootId, sessionKey, seq, options?.nodeId);
  if (!incoming) {
    return { session: base, hasDelta: false };
  }
  const effectiveBase = baseTruncated ? null : base;
  const incomingExchanges = Array.isArray(incoming.exchanges)
    ? incoming.exchanges
    : [];
  const persistedDelta = incomingExchanges.filter((exchange: any) => {
    const exchangeSeq = Number((exchange as any)?.seq || 0);
    return Number.isFinite(exchangeSeq) && exchangeSeq > 0;
  });
  const transientTail = incomingExchanges.filter(
    (exchange) => Number((exchange as any)?.seq || 0) === 0,
  );
  const persistedSession = appendSessionDelta(effectiveBase, {
    ...incoming,
    key: sessionKey,
    exchanges: persistedDelta,
    exchange_aux: toPersistentExchangeAux(incoming.exchange_aux),
  });
  if (!persistedSession) {
    return { session: null, hasDelta: false };
  }
  await saveCachedSession(rootId, persistedSession, options?.nodeId);
  const displaySession = withSessionMeta(persistedSession, {
    ...incoming,
    key: sessionKey,
    exchanges: [...(persistedSession.exchanges || []), ...transientTail],
    exchange_aux: persistedSession.exchange_aux,
  });
  return {
    session: displaySession ? cloneSession(displaySession) : null,
    hasDelta: persistedDelta.length > 0,
  };
}

/** 窗口化拉取单段会话数据（方案 B）。委托给 sessionService.getSessionWindow。 */
export async function getSessionWindow(
  rootId: string,
  sessionKey: string,
  opts?: SessionWindowOptions,
): Promise<SessionWindow | null> {
  return sessionService.getSessionWindow(rootId, sessionKey, opts);
}

/** 增量语义别名：即现有 getSession(?seq 增量)。供 SessionViewer 跨窗口流式追加复用。 */
export async function getSessionIncrement(
  rootId: string,
  sessionKey: string,
  seq?: number,
  nodeId?: string,
): Promise<Session | null> {
  return sessionService.getSession(rootId, sessionKey, seq, nodeId);
}
