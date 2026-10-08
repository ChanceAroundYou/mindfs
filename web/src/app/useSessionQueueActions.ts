/**
 * 会话 pending 标记 + 队列消息操作 —— A2b/A5 尾部（~132 行）。
 *
 * 覆盖：markSessionPending（流式期间幂等地把会话标成「回复中」，同时写缓存 /
 * 选中项 / 抽屉 / 列表 / 多项目 pending 五处），以及队列消息的删除、改写、
 * 「立即发送」（带乐观出队 + 失败回滚）。
 *
 * 做法：hook，参数面 UseSessionQueueActionsParams 即完整契约；函数体逐字搬入
 * （保持源码正则断言可命中，配合 source-map 注册）。
 */
import { useCallback, type MutableRefObject, type Dispatch, type SetStateAction } from "react";
import { SessionItem, SessionQueueItem } from "./appSession";
import { useSessionCache } from "./useSessionCache";
import { useSessionPending } from "./useSessionPending";
import { sessionService, type Session } from "../services/session";
import { resolveLockedSessionKey } from "../services/sessionLock";

export interface UseSessionQueueActionsParams {
  boundSessionByRootRef: MutableRefObject<Record<string, string | null>>;
  bumpCacheVersion: () => any;
  currentRootIdRef: MutableRefObject<string | null>;
  drawerSessionByRootRef: MutableRefObject<Record<string, SessionItem | null>>;
  interactionModeRef: MutableRefObject<"main" | "drawer">;
  optimisticDequeuedIdsRef: MutableRefObject<Record<string, Set<string>>>;
  queuedMessagesBySessionRef: MutableRefObject<Record<string, SessionQueueItem[]>>;
  rootSessionKey: (rootId: string, sessionKey: string) => any;
  scopedRootKey: (rootId: string) => string;
  selectedSessionRef: MutableRefObject<SessionItem | null>;
  sessionCacheRef: ReturnType<typeof useSessionCache>["sessionCacheRef"];
  setDrawerSessionForRoot: (rootID: string | null | undefined, session: Session | SessionItem | null) => any;
  setMultiProjectSessionPending: (rootID: string | null | undefined, sessionKey: string | null | undefined, pending: boolean) => any;
  setQueueVersion: Dispatch<SetStateAction<number>>;
  setSelectedPendingByKey: ReturnType<typeof useSessionPending>["setSelectedPendingByKey"];
  setSessions: Dispatch<SetStateAction<SessionItem[]>>;
}

export function useSessionQueueActions(params: UseSessionQueueActionsParams) {
  const {
    boundSessionByRootRef,
    bumpCacheVersion,
    currentRootIdRef,
    drawerSessionByRootRef,
    interactionModeRef,
    optimisticDequeuedIdsRef,
    queuedMessagesBySessionRef,
    rootSessionKey,
    scopedRootKey,
    selectedSessionRef,
    sessionCacheRef,
    setDrawerSessionForRoot,
    setMultiProjectSessionPending,
    setQueueVersion,
    setSelectedPendingByKey,
    setSessions,
  } = params;

  const markSessionPending = useCallback(
    (rootID: string, sessionKey: string) => {
      if (!rootID || !sessionKey) return;
      // 幂等：流式期间每 chunk 调用一次，已在 pending 则只更新缓存 ref，不再重复 setState。
      const cacheKey = rootSessionKey(rootID, sessionKey);
      const cached = sessionCacheRef.current[cacheKey];
      const drawer = drawerSessionByRootRef.current[scopedRootKey(rootID)];
      const alreadyPending =
        !!(cached as any)?.pending &&
        !!(drawer as any)?.pending &&
        (selectedSessionRef.current?.key || selectedSessionRef.current?.session_key) ===
          sessionKey &&
        !!(selectedSessionRef.current as any)?.pending;
      const now = new Date().toISOString();
      if (cached) {
        sessionCacheRef.current[cacheKey] = {
          ...(cached as any),
          pending: true,
          updated_at: now,
        } as Session;
      }
      if (alreadyPending) {
        return;
      }
      if (cached) {
        bumpCacheVersion();
      }
      setSelectedPendingByKey(sessionKey, true);
      if (drawer && (drawer.key || (drawer as any).session_key) === sessionKey) {
        setDrawerSessionForRoot(rootID, {
          ...(drawer as any),
          pending: true,
          updated_at: now,
        } as Session);
      }
      if (currentRootIdRef.current === rootID) {
        setSessions((prev) =>
          prev.map((item) => {
            const itemKey = item.key || item.session_key;
            if (itemKey !== sessionKey) {
              return item;
            }
            return {
              ...(item as any),
              pending: true,
              updated_at: now,
            } as SessionItem;
          }),
        );
      }
      setMultiProjectSessionPending(rootID, sessionKey, true);
    },
    [bumpCacheVersion, rootSessionKey, setDrawerSessionForRoot, setMultiProjectSessionPending, setSelectedPendingByKey],
  );

  const handleRemoveQueuedMessage = useCallback(
    async (queueId: string) => {
      const activeRoot = currentRootIdRef.current;
      const selected = selectedSessionRef.current;
      const selectedRoot =
        (selected?.root_id as string | undefined) || activeRoot || "";
      const selectedKey = selected?.key || selected?.session_key || "";
      const resolvedSelected = resolveLockedSessionKey(selectedKey);
      const boundKey = boundSessionByRootRef.current[scopedRootKey(activeRoot || "")] || null;
      const sessionKey =
        interactionModeRef.current !== "drawer" &&
        selectedRoot === activeRoot &&
        resolvedSelected
          ? resolvedSelected
          : (resolveLockedSessionKey(boundKey) ?? "");
      if (!activeRoot || !sessionKey || !queueId) return;
      await sessionService.removeQueuedMessage(activeRoot, sessionKey, queueId);
    },
    [],
  );

  const handleUpdateQueuedMessage = useCallback(
    async (queueId: string, content: string) => {
      const activeRoot = currentRootIdRef.current;
      const selected = selectedSessionRef.current;
      const selectedRoot =
        (selected?.root_id as string | undefined) || activeRoot || "";
      const selectedKey = selected?.key || selected?.session_key || "";
      const resolvedSelected = resolveLockedSessionKey(selectedKey);
      const boundKey = boundSessionByRootRef.current[scopedRootKey(activeRoot || "")] || null;
      const sessionKey =
        interactionModeRef.current !== "drawer" &&
        selectedRoot === activeRoot &&
        resolvedSelected
          ? resolvedSelected
          : (resolveLockedSessionKey(boundKey) ?? "");
      if (!activeRoot || !sessionKey || !queueId || !content.trim()) return;
      await sessionService.updateQueuedMessage(activeRoot, sessionKey, queueId, content);
    },
    [],
  );

  const handleSendQueuedMessageNow = useCallback(
    async (queueId: string) => {
      const activeRoot = currentRootIdRef.current;
      const selected = selectedSessionRef.current;
      const selectedRoot =
        (selected?.root_id as string | undefined) || activeRoot || "";
      const selectedKey = selected?.key || selected?.session_key || "";
      const resolvedSelected = resolveLockedSessionKey(selectedKey);
      const boundKey = boundSessionByRootRef.current[scopedRootKey(activeRoot || "")] || null;
      const sessionKey =
        interactionModeRef.current !== "drawer" &&
        selectedRoot === activeRoot &&
        resolvedSelected
          ? resolvedSelected
          : (resolveLockedSessionKey(boundKey) ?? "");
      if (!activeRoot || !sessionKey || !queueId) return;
      const cacheKey = rootSessionKey(activeRoot, sessionKey);
      const previousQueue = queuedMessagesBySessionRef.current[cacheKey] || [];
      const nextQueue = previousQueue.filter((item) => item.id !== queueId);
      queuedMessagesBySessionRef.current[cacheKey] = nextQueue;
      if (!optimisticDequeuedIdsRef.current[cacheKey]) {
        optimisticDequeuedIdsRef.current[cacheKey] = new Set();
      }
      optimisticDequeuedIdsRef.current[cacheKey].add(queueId);
      setQueueVersion((v) => v + 1);
      markSessionPending(activeRoot, sessionKey);
      const sent = await sessionService.sendQueuedMessageNow(activeRoot, sessionKey, queueId);
      if (!sent) {
        optimisticDequeuedIdsRef.current[cacheKey]?.delete(queueId);
        queuedMessagesBySessionRef.current[cacheKey] = previousQueue;
        setQueueVersion((v) => v + 1);
      }
    },
    [markSessionPending, rootSessionKey],
  );

  return {
    markSessionPending,
    handleRemoveQueuedMessage,
    handleUpdateQueuedMessage,
    handleSendQueuedMessageNow,
  };
}
