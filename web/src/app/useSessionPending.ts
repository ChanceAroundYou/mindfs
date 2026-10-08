/**
 * 会话 pending / stale 状态 —— A2b 簇（~197 行）。
 *
 * 覆盖：快照合成（getSessionSnapshot）、pending 解析与本地清除、stale 标记/清除/查询。
 * 这些是纯状态读写，依赖面窄、无前向引用（L1418-1620 区间 0 个 TDZ 风险）。
 *
 * 做法：hook，参数面 UseSessionPendingParams 即完整契约；函数体逐字搬入
 * （保持源码正则断言可命中，配合 source-map 注册）。
 */
import { useCallback, type MutableRefObject, type Dispatch, type SetStateAction } from "react";
import { Exchange, PendingSend, SessionItem } from "./appSession";
import { useSessionCache } from "./useSessionCache";
import { settlePendingAcks, type Session } from "../services/session";

export interface UseSessionPendingParams {
  bumpCacheVersion: () => any;
  cacheVersion: number;
  currentRootIdRef: MutableRefObject<string | null>;
  drawerSessionByRootRef: MutableRefObject<Record<string, SessionItem | null>>;
  pendingBySessionRef: MutableRefObject<Record<string, PendingSend>>;
  rootSessionKey: (rootId: string, sessionKey: string) => any;
  scopedRootKey: (rootId: string) => string;
  selectedSessionRef: MutableRefObject<SessionItem | null>;
  sessionCacheRef: ReturnType<typeof useSessionCache>["sessionCacheRef"];
  sessionLoadFailureRef: ReturnType<typeof useSessionCache>["sessionLoadFailureRef"];
  setDrawerSessionForRoot: (rootID: string | null | undefined, session: Session | SessionItem | null) => any;
  setMultiProjectSessionPending: (rootID: string | null | undefined, sessionKey: string | null | undefined, pending: boolean) => any;
  setSelectedSession: Dispatch<SetStateAction<SessionItem | null>>;
  setSessions: Dispatch<SetStateAction<SessionItem[]>>;
  staleSessionKeysRef: ReturnType<typeof useSessionCache>["staleSessionKeysRef"];
}

export function useSessionPending(params: UseSessionPendingParams) {
  const {
    bumpCacheVersion,
    cacheVersion,
    currentRootIdRef,
    drawerSessionByRootRef,
    pendingBySessionRef,
    rootSessionKey,
    scopedRootKey,
    selectedSessionRef,
    sessionCacheRef,
    sessionLoadFailureRef,
    setDrawerSessionForRoot,
    setMultiProjectSessionPending,
    setSelectedSession,
    setSessions,
    staleSessionKeysRef,
  } = params;

  const getSessionSnapshot = useCallback(
    (
      rootId: string | null | undefined,
      session: Session | SessionItem | null | undefined,
    ) => {
      if (!rootId || !session) return null;
      const key = (session as any).key || (session as any).session_key;
      if (!key) return null;
      const ck = rootSessionKey(rootId, key);
      const cached = sessionCacheRef.current[ck];
      const drawerSession = drawerSessionByRootRef.current[scopedRootKey(rootId)];
      const fallbackExchanges = Array.isArray((session as any).exchanges)
        ? ((session as any).exchanges as Exchange[])
        : [];
      const exchanges = Array.isArray((cached as any)?.exchanges)
        ? ((cached as any).exchanges as Exchange[]) || []
        : fallbackExchanges;
      const pending =
        drawerSession?.key === key
          ? !!(drawerSession as any)?.pending
          : typeof (session as any)?.pending === "boolean"
            ? !!(session as any).pending
            : typeof (cached as any)?.pending === "boolean"
              ? !!(cached as any).pending
              : undefined;
      return {
        ...(session as any),
        ...(cached as any),
        ...(drawerSession?.key === key ? (drawerSession as any) : null),
        key,
        search_seq: (session as any).search_seq,
        search_target_id: (session as any).search_target_id,
        search_snippet: (session as any).search_snippet,
        search_match_type: (session as any).search_match_type,
        exchanges,
        pending,
      } as any;
    },
    [rootSessionKey, cacheVersion],
  );

  const setSelectedPendingByKey = useCallback(
    (sessionKey: string, pending: boolean) => {
      setSelectedSession((prev) => {
        const prevKey = prev?.key || prev?.session_key;
        if (!prev || prevKey !== sessionKey) return prev;
        return { ...(prev as any), pending } as SessionItem;
      });
    },
    [],
  );

  const resolvePendingForSession = useCallback(
    (
      rootID: string | null | undefined,
      sessionKey: string | null | undefined,
      fallback?: boolean,
    ): boolean => {
      const resolvedRoot = String(rootID || "");
      const resolvedKey = String(sessionKey || "");
      if (!resolvedRoot || !resolvedKey) {
        return !!fallback;
      }
      const cacheKey = rootSessionKey(resolvedRoot, resolvedKey);
      if (pendingBySessionRef.current[cacheKey]) {
        return true;
      }
      const drawer = drawerSessionByRootRef.current[scopedRootKey(resolvedRoot)] as
        | ({ pending?: boolean; key?: string; session_key?: string } & Record<string, unknown>)
        | null
        | undefined;
      if (
        drawer &&
        (drawer.key || drawer.session_key) === resolvedKey &&
        typeof drawer.pending === "boolean"
      ) {
        return drawer.pending;
      }
      const selected = selectedSessionRef.current as
        | ({ pending?: boolean; key?: string; session_key?: string; root_id?: string } & Record<string, unknown>)
        | null
        | undefined;
      if (
        selected &&
        ((selected.root_id as string | undefined) || currentRootIdRef.current) ===
          resolvedRoot &&
        (selected.key || selected.session_key) === resolvedKey &&
        typeof selected.pending === "boolean"
      ) {
        return selected.pending;
      }
      const cached = sessionCacheRef.current[cacheKey] as
        | ({ pending?: boolean } & Record<string, unknown>)
        | null
        | undefined;
      if (cached && typeof cached.pending === "boolean") {
        return cached.pending;
      }
      return !!fallback;
    },
    [rootSessionKey],
  );

  const clearLocalPendingForSession = useCallback(
    (rootID: string | null | undefined, sessionKey: string | null | undefined) => {
      const resolvedRoot = String(rootID || "");
      const resolvedKey = String(sessionKey || "");
      if (!resolvedRoot || !resolvedKey) {
        return;
      }
      const clearPendingAck = <T,>(session: T): T => {
        const exchanges = (session as any)?.exchanges;
        if (!Array.isArray(exchanges)) {
          return session;
        }
        return settlePendingAcks(session);
      };
      const cacheKey = rootSessionKey(resolvedRoot, resolvedKey);
      delete pendingBySessionRef.current[cacheKey];
      const cached = sessionCacheRef.current[cacheKey];
      if (cached && (cached.key || (cached as any).session_key) === resolvedKey) {
        sessionCacheRef.current[cacheKey] = clearPendingAck({
          ...(cached as any),
          pending: false,
        } as Session);
      }
      setSelectedSession((prev) => {
        const prevKey = prev?.key || prev?.session_key;
        const prevRoot =
          (prev?.root_id as string | undefined) || currentRootIdRef.current;
        if (!prev || prevKey !== resolvedKey || prevRoot !== resolvedRoot) {
          return prev;
        }
        return clearPendingAck({
          ...(prev as any),
          pending: false,
        } as SessionItem);
      });
      const drawer = drawerSessionByRootRef.current[scopedRootKey(resolvedRoot)];
      if (drawer && (drawer.key || (drawer as any).session_key) === resolvedKey) {
        setDrawerSessionForRoot(resolvedRoot, clearPendingAck({
          ...(drawer as any),
          pending: false,
        } as Session));
      }
      if (currentRootIdRef.current === resolvedRoot) {
        setSessions((prev) =>
          prev.map((item) => {
            const itemKey = item.key || item.session_key;
            if (itemKey !== resolvedKey) {
              return item;
            }
            return clearPendingAck({
              ...(item as any),
              pending: false,
            } as SessionItem);
          }),
        );
      }
      setMultiProjectSessionPending(resolvedRoot, resolvedKey, false);
      bumpCacheVersion();
    },
    [bumpCacheVersion, rootSessionKey, setDrawerSessionForRoot, setMultiProjectSessionPending],
  );

  const markSessionStale = useCallback(
    (rootID: string | null | undefined, sessionKey: string | null | undefined) => {
      const resolvedRoot = String(rootID || "");
      const resolvedKey = String(sessionKey || "");
      if (!resolvedRoot || !resolvedKey || resolvedKey.startsWith("pending-")) {
        return;
      }
      staleSessionKeysRef.current.add(rootSessionKey(resolvedRoot, resolvedKey));
      // CUSTOM(G-AU): 显式标脏就是「重新拉一次」的信号，必须同时解禁失败冷却 ——
      // 否则 404 的会话被标脏后依然拉不动，用户点重试毫无反应。
      delete sessionLoadFailureRef.current[rootSessionKey(resolvedRoot, resolvedKey)];
    },
    [rootSessionKey],
  );

  const clearSessionStale = useCallback(
    (rootID: string | null | undefined, sessionKey: string | null | undefined) => {
      const resolvedRoot = String(rootID || "");
      const resolvedKey = String(sessionKey || "");
      if (!resolvedRoot || !resolvedKey) {
        return;
      }
      staleSessionKeysRef.current.delete(rootSessionKey(resolvedRoot, resolvedKey));
    },
    [rootSessionKey],
  );

  const isSessionStale = useCallback(
    (rootID: string | null | undefined, sessionKey: string | null | undefined): boolean => {
      const resolvedRoot = String(rootID || "");
      const resolvedKey = String(sessionKey || "");
      if (!resolvedRoot || !resolvedKey) {
        return false;
      }
      return staleSessionKeysRef.current.has(rootSessionKey(resolvedRoot, resolvedKey));
    },
    [rootSessionKey],
  );

  return {
    getSessionSnapshot,
    setSelectedPendingByKey,
    resolvePendingForSession,
    clearLocalPendingForSession,
    markSessionStale,
    clearSessionStale,
    isSessionStale,
  };
}
