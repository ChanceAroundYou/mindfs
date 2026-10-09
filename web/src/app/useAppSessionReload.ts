import { useCallback } from "react";

import { mergeSessionItems } from "../services/sessionListMerge";
import { sessionService } from "../services/session";
import { toSessionItem, type SessionItem } from "./appSession";

export type UseAppSessionReloadParams = {
  sessionListReloadTimerRef: React.RefObject<number | null>;
  loadSessionsForRoot: (
    rootID: string,
    options?: {
      beforeTime?: string;
      afterTime?: string;
      replace?: boolean;
      force?: boolean;
    },
  ) => Promise<void>;
  currentRootIdRef: React.RefObject<string | null>;
  sessionsRef: React.RefObject<SessionItem[]>;
  loadingOlderSessions: boolean;
  setLoadingOlderSessions: React.Dispatch<React.SetStateAction<boolean>>;
  getNodeIdForRoot: (rootId: string) => string | undefined;
  setHasMoreSessions: React.Dispatch<React.SetStateAction<boolean>>;
  setSessions: React.Dispatch<React.SetStateAction<SessionItem[]>>;
  applyPinSnapshot: <T extends { root_id?: string; pinned_at?: string | null }>(
    items: T[],
    rootId: string,
  ) => T[];
};

/**
 * 会话列表的两种「再拉一次」：防抖的整表重载，和往上翻的加载更早。
 *
 * 两者都只动同一份 sessions，且都必须走 applyPinSnapshot —— 列表回包不带置顶，
 * 不盖就等于把置顶区抹掉（见 useAppSessionPins 的决策注释）。
 */
export function useAppSessionReload(params: UseAppSessionReloadParams) {
  const {
    sessionListReloadTimerRef,
    loadSessionsForRoot,
    currentRootIdRef,
    sessionsRef,
    loadingOlderSessions,
    setLoadingOlderSessions,
    getNodeIdForRoot,
    setHasMoreSessions,
    setSessions,
    applyPinSnapshot,
  } = params;

  const scheduleSessionListReload = useCallback(
    (
      rootID: string,
      options?: {
        beforeTime?: string;
        afterTime?: string;
        replace?: boolean;
        force?: boolean;
      },
    ) => {
      if (sessionListReloadTimerRef.current) {
        window.clearTimeout(sessionListReloadTimerRef.current);
      }
      sessionListReloadTimerRef.current = window.setTimeout(() => {
        sessionListReloadTimerRef.current = null;
        void loadSessionsForRoot(rootID, options);
      }, 300);
    },
    [loadSessionsForRoot],
  );

  const handleLoadOlderSessions = useCallback(async () => {
    const rootID = currentRootIdRef.current;
    const oldest =
      sessionsRef.current[sessionsRef.current.length - 1]?.updated_at || "";
    if (!rootID || !oldest || loadingOlderSessions) {
      return;
    }
    setLoadingOlderSessions(true);
    try {
      const listNodeId = getNodeIdForRoot(rootID);
      const payload = await sessionService.fetchSessions(rootID, {
        beforeTime: oldest,
        nodeId: listNodeId,
      });
      const next = [...payload.items, ...payload.pinnedItems]
        // 同上：加载更多并进的是同一份 sessions，节点归属必须与首屏一致
        .map((item) =>
          toSessionItem(rootID, {
            ...(item as any),
            _nodeId: (item as any)._nodeId || listNodeId,
          }),
        )
        .filter((item): item is SessionItem => !!item);
      setHasMoreSessions(payload.totalCount > payload.items.length);
      setSessions((prev) => applyPinSnapshot(mergeSessionItems(prev, next), rootID));
    } finally {
      setLoadingOlderSessions(false);
    }
  }, [getNodeIdForRoot, loadingOlderSessions]);

  return { scheduleSessionListReload, handleLoadOlderSessions };
}