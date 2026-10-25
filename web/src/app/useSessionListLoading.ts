import { useCallback } from "react";
import { CHILD_SESSION_PAGE_SIZE } from "./appMisc";
import { type Session, getCachedSessionList, saveCachedSessionList, sessionService } from "../services/session";
import { mergeSessionItems } from "../services/session/sessionListMerge";
import { toSessionItem, type SessionItem } from "./appSession";
import { reportError } from "../services/net/error";
import type { UseSessionLifecycleParams } from "./useSessionLifecycleTypes";

// 列表分页、缓存回填与列表头同步共享同一组列表归并契约。
type Params = Pick<UseSessionLifecycleParams,
  | "rootSessionKey"
  | "sessionCacheRef"
  | "setSelectedSession"
  | "currentRootIdRef"
  | "drawerSessionByRootRef"
  | "scopedRootKey"
  | "setDrawerSessionForRoot"
  | "bumpCacheVersion"
  | "getNodeIdForRoot"
  | "sessionListLoadSeqRef"
  | "sessionListAbortRef"
  | "setHasMoreSessions"
  | "setSessions"
  | "applyPinSnapshot"
  | "listLoadErrorAtRef"
  | "applyPendingToMultiProjectGroups"
  | "multiProjectPendingRef"
  | "setMultiProjectSessionGroups"
>;

export function useSessionListLoading(params: Params) {
  const {
    rootSessionKey,
    sessionCacheRef,
    setSelectedSession,
    currentRootIdRef,
    drawerSessionByRootRef,
    scopedRootKey,
    setDrawerSessionForRoot,
    bumpCacheVersion,
    getNodeIdForRoot,
    sessionListLoadSeqRef,
    sessionListAbortRef,
    setHasMoreSessions,
    setSessions,
    applyPinSnapshot,
    listLoadErrorAtRef,
    applyPendingToMultiProjectGroups,
    multiProjectPendingRef,
    setMultiProjectSessionGroups,
  } = params;
  const syncSessionHeaderFromListItem = useCallback(
    (rootID: string, item: SessionItem | null | undefined) => {
      const sessionKey = item?.key || item?.session_key || "";
      const sessionName = typeof item?.name === "string" ? item.name : "";
      if (!rootID || !sessionKey || !sessionName) return;

      const cacheKey = rootSessionKey(rootID, sessionKey);
      const cached = sessionCacheRef.current[cacheKey];
      let changed = false;
      if (cached && cached.name !== sessionName) {
        sessionCacheRef.current[cacheKey] = {
          ...(cached as any),
          name: sessionName,
          updated_at: item?.updated_at || cached.updated_at,
        } as Session;
        changed = true;
      }

      setSelectedSession((prev) => {
        const prevKey = prev?.key || prev?.session_key;
        const prevRoot =
          (prev?.root_id as string | undefined) || currentRootIdRef.current;
        if (!prev || prevKey !== sessionKey || prevRoot !== rootID) return prev;
        if (prev.name === sessionName) return prev;
        return {
          ...(prev as any),
          name: sessionName,
          updated_at: item?.updated_at || prev.updated_at,
        } as SessionItem;
      });

      const drawer = drawerSessionByRootRef.current[scopedRootKey(rootID)];
      if (drawer?.key === sessionKey && drawer.name !== sessionName) {
        setDrawerSessionForRoot(rootID, {
          ...(drawer as any),
          name: sessionName,
          updated_at: item?.updated_at || drawer.updated_at,
        } as Session);
        changed = true;
      }

      if (changed) {
        bumpCacheVersion();
      }
    },
    [rootSessionKey, setDrawerSessionForRoot, bumpCacheVersion],
  );
  const loadSessionsForRoot = useCallback(
    async (
      rootID: string,
      options?: {
        beforeTime?: string;
        afterTime?: string;
        replace?: boolean;
        force?: boolean;
      },
    ) => {
      const _nid = getNodeIdForRoot(rootID);
      const snapNode = String(_nid || "").trim();
      const snapRoot = String(rootID || "").trim();
      const seq = ++sessionListLoadSeqRef.current;
      sessionListAbortRef.current?.abort();
      const controller = new AbortController();
      sessionListAbortRef.current = controller;
      try {
        const shouldReplace = options?.replace || (!options?.beforeTime && !options?.afterTime);
        if (shouldReplace) {
          const cached = await getCachedSessionList(rootID, _nid);
          if (controller.signal.aborted || seq !== sessionListLoadSeqRef.current) { return; }
          if ((String(getNodeIdForRoot(rootID) || "").trim()) !== snapNode) { return; }
          if (cached && (options?.force || currentRootIdRef.current === rootID)) {
            const cachedItems = [...cached.items, ...cached.pinnedItems]
              .map((item) => toSessionItem(rootID, { ...(item as any), _nodeId: (item as any)._nodeId || snapNode }))
              .filter((item): item is SessionItem => !!item);
            setHasMoreSessions(cached.totalCount > cached.items.length);
            setSessions(
              applyPinSnapshot(
                mergeSessionItems([], cachedItems),
                rootID,
              ),
            );
          }
        }
        const payload = await sessionService.fetchSessions(rootID, {
          nodeId: _nid,
          beforeTime: options?.beforeTime,
          afterTime: options?.afterTime,
        });
        if (controller.signal.aborted || seq !== sessionListLoadSeqRef.current) { return; }
        if ((String(getNodeIdForRoot(rootID) || "").trim()) !== snapNode) { return; }
        if (String(currentRootIdRef.current || "").trim() !== snapRoot && !options?.force) { return; }
        const next = [
          ...payload.items,
          ...payload.pinnedItems,
        ].map((item) => toSessionItem(rootID, { ...(item as any), _nodeId: (item as any)._nodeId || snapNode })).filter((item): item is SessionItem => !!item);
        // D3: 会话打标，便于切节点过滤与 key 隔离
        if (!options?.force && currentRootIdRef.current !== rootID) return;
        // 迟到覆盖已在 seq/snap 校验后再次用 currentRootId 守卫
        if (!options?.force && String(currentRootIdRef.current || "").trim() !== snapRoot) return;
        setHasMoreSessions(payload.totalCount > payload.items.length);
        if (shouldReplace) {
          // 服务端列表接口不含 context_window，全量重拉会把它清掉（WS message_done 已同步进本地列表）。
          // 用 setSessions 回调继承旧列表的 context_window，避免徽标闪没。
          //
          // activity_at 同理，而且更刺眼：它是客户端流式活动时间戳，服务端 updated_at 是
          // **最后一条对话消息**的时间 —— agent 连跑 12 分钟工具时那个时间戳一直停在 12 分钟前。
          // session.done 后立刻 replace 重拉，若不继承，灯灭那一瞬间时间就从「刚刚」
          // 跳回「12m」。继承后仍由 mergeSessionItems 的判定决定去留（updated_at 追上就删），
          // 所以不会永久谎报活跃 —— 两处只留一条规则。
          setSessions((prev) => {
            const prevContext = new Map(
              prev
                .filter((item) => item.context_window)
                .map((item) => [
                  String(item.key || item.session_key || ""),
                  item.context_window,
                ]),
            );
            // 继承只管「把上一帧的 activity_at 搬过来」；留不留由 mergeSessionItems
            // 那条唯一规则决定（落后于 updated_at 就删），两处各写一遍迟早分叉。
            const merged = mergeSessionItems(
              [],
              next,
            ).map((item) => {
              const itemKey = String(item.key || item.session_key || "");
              const inherited = prevContext.get(itemKey);
              return inherited
                ? { ...item, context_window: inherited }
                : item;
            });
            return applyPinSnapshot(merged, rootID);
          });
          void saveCachedSessionList(rootID, payload, _nid);
          return;
        }
        setSessions((prev) =>
          applyPinSnapshot(mergeSessionItems(prev, next), rootID),
        );
      } catch (err) {
        if ((err as any)?.name === "AbortError") return;
        if (controller.signal.aborted) return;
        // 列表拉取失败不再静默：WS 抖动时列表会停留旧状态（2026-09-09 排查），
        // 可见告警让用户知道需要手动同步；10s 冷却防断网期间刷屏。
        if (Date.now() - listLoadErrorAtRef.current > 10000) {
          listLoadErrorAtRef.current = Date.now();
          reportError(
            "session.list_load_failed",
            String((err as Error)?.message || err),
            { severity: "warning", recoverable: true },
          );
        }
      }
    },
    [getNodeIdForRoot],
  );
  const loadChildSessionsForParent = useCallback(
    async (
      parent: SessionItem,
      options?: { beforeTime?: string },
    ): Promise<{ hasMore: boolean }> => {
      const rootID =
        (parent.root_id as string | undefined) || currentRootIdRef.current || "";
      const parentKey = parent.key || parent.session_key || "";
      if (!rootID || !parentKey) {
        return { hasMore: false };
      }
      const childNodeId = (parent as any)?._nodeId || getNodeIdForRoot(rootID);
      const items = await sessionService.fetchChildSessions(rootID, parentKey, {
        beforeTime: options?.beforeTime,
        limit: CHILD_SESSION_PAGE_SIZE,
        nodeId: childNodeId,
      });
      // 响应体不带 _nodeId（那是前端传输级打标），必须在这里补：mergeSessionItems
      // 按 `_nodeId::key` 归并，漏了会让这批子会话归到 `::key` 而既有条目是 `local::key`，
      // 并存成一整批重复的 React key；重复 key 会让收起子会话时留下孤儿 DOM
      // （实测展开 131 行只收到 55 行，且这批重复永久留在分组里）。
      const next = items
        .map((item) => toSessionItem(rootID, { ...(item as any), _nodeId: childNodeId }))
        .filter((item): item is SessionItem => !!item);
      if (currentRootIdRef.current === rootID && next.length > 0) {
        setSessions((prev) => mergeSessionItems(prev, next));
      }
      if (next.length > 0) {
        setMultiProjectSessionGroups((prev) =>
          applyPendingToMultiProjectGroups(
            prev.map((group) =>
              group.rootId === rootID && (group as any)._nodeId === (parent as any)?._nodeId
                ? { ...group, sessions: mergeSessionItems(group.sessions, next) }
                : group,
            ),
            multiProjectPendingRef.current,
          ),
        );
      }
      return { hasMore: items.length >= CHILD_SESSION_PAGE_SIZE };
    },
    [applyPendingToMultiProjectGroups, mergeSessionItems],
  );

  return { syncSessionHeaderFromListItem, loadSessionsForRoot, loadChildSessionsForParent };
}
