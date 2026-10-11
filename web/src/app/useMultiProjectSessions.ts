/**
 * 多项目会话组与待处理提升 —— A2+A3 簇（3 个单元，~370 行）。
 *
 * 覆盖：跨节点会话组加载与分页、待处理会话提升为正式会话。
 *
 * 做法：hook，参数面 UseMultiProjectSessionsParams 即完整契约；函数体逐字搬入
 * （保持源码正则断言可命中，配合 source-map 注册）。
 */
import { useCallback, useMemo, type MutableRefObject, type Dispatch, type SetStateAction } from "react";
import { MULTI_PROJECT_SESSION_LIMIT, ManagedRootPayload, SESSION_PAGE_SIZE, useResponsive } from "./appMisc";
import { rootNodeKey } from "./appPath";
import { Exchange, MultiProjectSessionGroup, SessionItem, isTopLevelSessionItem, toSessionItem } from "./appSession";
import { useActionHandlers } from "./useActionHandlers";
import { useCompletionSound } from "./useCompletionSound";
import { useExternalSessionImport } from "./useExternalSessionImport";
import { useGitActions } from "./useGitActions";
import { useGitData } from "./useGitData";
import { useProjectLifecycle } from "./useProjectLifecycle";
import { useProjectTreeWorktrees } from "./useProjectTreeWorktrees";
import { useSendMessage } from "./useSendMessage";
import { useSessionCache } from "./useSessionCache";
import { useSessionLifecycle } from "./useSessionLifecycle";
import { useSessionSearch } from "./useSessionSearch";
import { useSessionSidebarView } from "./useSessionSidebarView";
import { useSessionStreamCache } from "./useSessionStreamCache";
import { useTaskTemplates } from "./useTaskTemplates";
import { type ProjectSessionGroup } from "../components/session/SessionList";
import { useI18n } from "../i18n";
import { protectedAPIReady } from "../services/net/api";
import { getActiveNode, getNodeById, getNodes } from "../services/net/nodeRegistry";
import { type MultiRootSessionGroup, type Session, getCachedMultiRootSessionList, saveCachedMultiRootSessionList, sessionService } from "../services/session";
import { resolveGroupColor } from "../services/sessionGroupDisplay";
import { mergeSessionItems } from "../services/sessionListMerge";

export type UseMultiProjectSessionsParams = {
  applyPendingToMultiProjectGroups: (groups: MultiProjectSessionGroup[], pendingByKey: Record<string, boolean>) => any;
  applyPinSnapshot: <T extends { root_id?: string; pinned_at?: string | null }>(items: T[], rootId: string,) => T[];
  boundSessionByRootRef: MutableRefObject<Record<string, string | null>>;
  bumpCacheVersion: () => any;
  currentRootIdRef: MutableRefObject<string | null>;
  currentRootNodeId: string | null;
  drawerSessionByRootRef: MutableRefObject<Record<string, SessionItem | null>>;
  getNodeIdForRoot: (rootId: string) => string | undefined;
  loadedSessionRef: ReturnType<typeof useSessionCache>["loadedSessionRef"];
  loadingSessionRef: ReturnType<typeof useSessionCache>["loadingSessionRef"];
  managedRootByIdRef: MutableRefObject<Record<string, ManagedRootPayload>>;
  managedRootByKeyRef: MutableRefObject<Record<string, ManagedRootPayload>>;
  multiProjectLoadSeqRef: MutableRefObject<number>;
  multiProjectPendingRef: MutableRefObject<Record<string, boolean>>;
  multiProjectSessionsEnabled: any;
  notifyNodeLoadFailedRef: MutableRefObject<(node: { id: string; name: string }) => void>;
  rootSessionKey: (rootId: string, sessionKey: string) => any;
  scopedRootKey: (rootId: string) => string;
  selectRootNode: (rootId: string, nodeId?: string) => any;
  selectedSessionByRootRef: MutableRefObject<Record<string, string | null>>;
  selectedSessionRef: MutableRefObject<SessionItem | null>;
  sessionCacheRef: ReturnType<typeof useSessionCache>["sessionCacheRef"];
  sessions: SessionItem[];
  setBoundSessionForRoot: (rootID: string | null | undefined, key: string | null) => any;
  setDrawerSessionForRoot: (rootID: string | null | undefined, session: Session | SessionItem | null) => any;
  setMultiProjectSessionGroups: Dispatch<SetStateAction<MultiProjectSessionGroup[]>>;
  setMultiProjectSessionPending: (rootID: string | null | undefined, sessionKey: string | null | undefined, pending: boolean) => any;
  setMultiProjectSessionsLoading: Dispatch<SetStateAction<boolean>>;
  setSelectedSession: Dispatch<SetStateAction<SessionItem | null>>;
  setSessions: Dispatch<SetStateAction<SessionItem[]>>;
};

export function useMultiProjectSessions(params: UseMultiProjectSessionsParams) {
  const { t } = useI18n();
  const {
    applyPendingToMultiProjectGroups,
    applyPinSnapshot,
    boundSessionByRootRef,
    bumpCacheVersion,
    currentRootIdRef,
    currentRootNodeId,
    drawerSessionByRootRef,
    getNodeIdForRoot,
    loadedSessionRef,
    loadingSessionRef,
    managedRootByIdRef,
    managedRootByKeyRef,
    multiProjectLoadSeqRef,
    multiProjectPendingRef,
    multiProjectSessionsEnabled,
    notifyNodeLoadFailedRef,
    rootSessionKey,
    scopedRootKey,
    selectRootNode,
    selectedSessionByRootRef,
    selectedSessionRef,
    sessionCacheRef,
    sessions,
    setBoundSessionForRoot,
    setDrawerSessionForRoot,
    setMultiProjectSessionGroups,
    setMultiProjectSessionPending,
    setMultiProjectSessionsLoading,
    setSelectedSession,
    setSessions,
  } = params;

  const promotePendingSessionForRoot = useCallback(
    (
      rootID: string,
      tempKey: string | undefined,
      sessionKey: string,
      fallback?: Session | null,
    ) => {
      const pendingKey = (tempKey || "").trim();
      if (!rootID || !pendingKey || !sessionKey || pendingKey === sessionKey) {
        return;
      }

      const pendingCacheKey = rootSessionKey(rootID, pendingKey);
      const realCacheKey = rootSessionKey(rootID, sessionKey);
      const pendingCached = sessionCacheRef.current[pendingCacheKey];
      const realCached = sessionCacheRef.current[realCacheKey];
      const drawer = drawerSessionByRootRef.current[scopedRootKey(rootID)];
      const selected = selectedSessionRef.current;
      const selectedKey = selected?.key || selected?.session_key;
      const selectedRoot =
        (selected?.root_id as string | undefined) || currentRootIdRef.current;
      const pendingName =
        (typeof (pendingCached as any)?.name === "string" &&
        (pendingCached as any).name
          ? (pendingCached as any).name
          : "") ||
        (drawer?.key === pendingKey && typeof (drawer as any)?.name === "string"
          ? ((drawer as any).name as string)
          : "") ||
        (selectedKey === pendingKey &&
        selectedRoot === rootID &&
        typeof selected?.name === "string"
          ? selected.name
          : "") ||
        t("session.new");
      const realExchanges = Array.isArray((realCached as any)?.exchanges)
        ? ((realCached as any).exchanges as Exchange[])
        : [];
      const pendingExchanges = Array.isArray((pendingCached as any)?.exchanges)
        ? ((pendingCached as any).exchanges as Exchange[])
        : [];
      const latestReal =
        pendingCached && realCached
          ? ({
              ...(pendingCached as any),
              ...(realCached as any),
              exchanges:
                realExchanges.length > 0 ? realExchanges : pendingExchanges,
            } as Session)
          : realCached || pendingCached || fallback || drawer;
      let cacheChanged = false;

      if (pendingCached) {
        sessionCacheRef.current[realCacheKey] = {
          ...(latestReal as any),
          key: sessionKey,
          name:
            (typeof (realCached as any)?.name === "string" &&
            (realCached as any).name
              ? (realCached as any).name
              : "") || pendingName,
        } as Session;
        delete sessionCacheRef.current[pendingCacheKey];
        delete loadedSessionRef.current[pendingCacheKey];
        delete loadingSessionRef.current[pendingCacheKey];
        cacheChanged = true;
      }

      if (boundSessionByRootRef.current[scopedRootKey(rootID)] === pendingKey) {
        setBoundSessionForRoot(rootID, sessionKey);
      }
      if (selectedSessionByRootRef.current[scopedRootKey(rootID)] === pendingKey) {
        selectedSessionByRootRef.current[scopedRootKey(rootID)] = sessionKey;
      }
      if (drawer?.key === pendingKey) {
        setDrawerSessionForRoot(rootID, {
          ...(drawer as any),
          ...(latestReal as any),
          key: sessionKey,
          name:
            (typeof (latestReal as any)?.name === "string" &&
            (latestReal as any).name
              ? (latestReal as any).name
              : "") || pendingName,
        } as Session);
      }
      if (currentRootIdRef.current === rootID) {
        setSessions((prev) =>
          prev.map((item) => {
            const itemKey = item.key || item.session_key;
            if (itemKey !== pendingKey) {
              return item;
            }
            return {
              ...(item as any),
              ...(latestReal as any),
              key: sessionKey,
              session_key: sessionKey,
              root_id: rootID,
              name:
                (typeof (latestReal as any)?.name === "string" &&
                (latestReal as any).name
                  ? (latestReal as any).name
                  : "") || pendingName,
            } as SessionItem;
          }),
        );
      }

      // pending-* 临时键换成真键时，把「在跑」这条乐观标记一起搬过去 ——
      // 唯一真相是 multiProjectPendingByKey，键不跟着搬就会在 promote 那一刻灭灯。
      if (multiProjectPendingRef.current[pendingCacheKey]) {
        setMultiProjectSessionPending(rootID, pendingKey, false);
        setMultiProjectSessionPending(rootID, sessionKey, true);
      }

      // pending 会话 promote 为真实会话时，同步替换右侧多项目列表中的临时条目
      setMultiProjectSessionGroups((prev) =>
        prev.map((group) => {
          if (group.rootId !== rootID) {
            return group;
          }
          const sessions = group.sessions.map((item) => {
            const itemKey = item.key || item.session_key;
            if (itemKey !== pendingKey) {
              return item;
            }
            return {
              ...(item as any),
              ...(latestReal as any),
              key: sessionKey,
              session_key: sessionKey,
              root_id: rootID,
              name:
                (typeof (latestReal as any)?.name === "string" &&
                (latestReal as any).name
                  ? (latestReal as any).name
                  : "") || pendingName,
            } as SessionItem;
          });
          return { ...group, sessions: mergeSessionItems(sessions, []) };
        }),
      );

      setSelectedSession((prev) => {
        const prevKey = prev?.key || prev?.session_key;
        const prevRoot =
          (prev?.root_id as string | undefined) || currentRootIdRef.current;
        if (!prev || prevKey !== pendingKey || prevRoot !== rootID) return prev;
        return toSessionItem(rootID, {
          ...(prev as any),
          ...(latestReal as any),
          key: sessionKey,
          session_key: sessionKey,
          root_id: rootID,
          name:
            (typeof (latestReal as any)?.name === "string" &&
            (latestReal as any).name
              ? (latestReal as any).name
              : "") || pendingName,
        });
      });

      if (cacheChanged) {
        bumpCacheVersion();
      }
    },
    [rootSessionKey, setBoundSessionForRoot, setDrawerSessionForRoot, bumpCacheVersion],
  );
  const loadMultiProjectSessionGroups = useCallback(async () => {
    if (!multiProjectSessionsEnabled || !protectedAPIReady()) {
      return;
    }
    const seq = ++multiProjectLoadSeqRef.current;
    setMultiProjectSessionsLoading(true);
    try {
      const cachedGroups = await getCachedMultiRootSessionList();
      if (cachedGroups?.length) {
        // 缓存先渲染（避免闪空），但**必须整体替换**而不是merge：这份缓存是按账户存的，
        // 换账户后它就是空的，若还往里合并就会把上一账户的会话留在屏上。
        setMultiProjectSessionGroups(
          applyPendingToMultiProjectGroups(
            cachedGroups.map((group): MultiProjectSessionGroup => {
              const cacheNid = String((group as any)?._nodeId || "").trim();
              return {
              rootId: group.rootId,
              rootName: group.rootName || managedRootByIdRef.current[group.rootId]?.display_name || group.rootId,
              // C1: 缓存重建必须补节点与颜色，否则整帧回退 PALETTE[0] 蓝 + nodeIndex=99 乱序
              _nodeId: cacheNid || undefined,
              _nodeColor: resolveGroupColor(group as any, managedRootByKeyRef.current as any, getNodes() as any) || undefined,
              latestSessionTime: group.latestSessionTime,
              sessions: applyPinSnapshot(
                mergeSessionItems(
                  [],
                  [...group.items, ...group.pinnedItems]
                    .map((item) => toSessionItem(group.rootId, { ...(item as any), root_id: group.rootId, _nodeId: cacheNid || undefined }))
                    .filter((item): item is SessionItem => !!item),
                ),
                group.rootId,
              ),
              totalCount: group.totalCount,
            };}),
            multiProjectPendingRef.current,
          ),
        );
      }
      const nodeIdsFromNodes = getNodes()
        .map((n) => String((n as any)?.id || "").trim())
        .filter(Boolean);
      const nodeIdsFromRoots = Array.from(
        new Set(
          Object.values(managedRootByKeyRef.current as Record<string, any>)
            .map((v) => String((v as any)?._nodeId || "").trim())
            .filter(Boolean),
        ),
      );
      const nodeIds = Array.from(new Set([...nodeIdsFromNodes, ...nodeIdsFromRoots]));
      const allGroups: Array<MultiRootSessionGroup & { _nodeId?: string }> = [];
      let nodeFetchResults: Array<Array<MultiRootSessionGroup & { _nodeId?: string }>> = [];
      // 只有**真的请求失败**的节点才保留旧分组（网络抖动时不清屏）；
      // 成功但为空 = 该账户在这个节点下确实没有会话，必须让空结果生效。
      let failedNids = new Set<string>();
      if (nodeIds.length === 0) {
        // managedRootByKeyRef 未就绪（初始化竞态）：以当前激活节点回退打标，
        // 避免首批分组 _nodeId 为空串、与 selectRootNode 后的 currentRootNodeId 不等而全部收起
        const fallbackNodeId =
          getNodeIdForRoot(String(currentRootIdRef.current || "")) ||
          String(getActiveNode()?.id || "").trim();
        try {
          const groups = await sessionService.fetchMultiRootSessions(MULTI_PROJECT_SESSION_LIMIT);
          for (const g of groups) allGroups.push({ ...g, _nodeId: fallbackNodeId });
        } catch {
          notifyNodeLoadFailedRef.current({
            id: fallbackNodeId,
            name: String(getActiveNode()?.name || fallbackNodeId),
          });
        }
      } else {
        // 跨机器也带 user=（用户名），服务端解析到对方本地的账户；对方没有这个账户时
        // 回 **200 空列表**，所以这里不需要「缺账户」分支——空就是空。
        // 但要区分「拉取失败」与「成功但为空」：两者都是 []，混为一谈会让下面把**上一个账户**
        // 的分组当成「该节点本次没返回」保留下来，于是新账户看到旧账户的全部会话（实测踩过）。
        const failed: boolean[] = [];
        nodeFetchResults = await Promise.all(nodeIds.map(async (nid, i) => {
          try {
            const gs = await sessionService.fetchMultiRootSessions(MULTI_PROJECT_SESSION_LIMIT, nid);
            failed[i] = false;
            return gs.map((g) => ({ ...g, _nodeId: nid }));
          } catch (err) {
            failed[i] = true;
            notifyNodeLoadFailedRef.current({
              id: String(nid),
              name: String(getNodeById(String(nid))?.name || nid),
            });
            return [] as Array<MultiRootSessionGroup & { _nodeId?: string }>;
          }
        }));
        failedNids = new Set(nodeIds.filter((_, i) => failed[i]));
        for (const groups of nodeFetchResults) allGroups.push(...groups);
      }
      const dedup = new Map<string, MultiRootSessionGroup & { _nodeId?: string }>();
      for (const g of allGroups) { const key = `${(g as any)._nodeId || ""}::${g.rootId}`; const prev = dedup.get(key); if (!prev || String((g as any).latestSessionTime || "") > String((prev as any).latestSessionTime || "")) dedup.set(key, g); }
      const groups = Array.from(dedup.values());
      const nextGroups = groups.map((group: MultiRootSessionGroup): MultiProjectSessionGroup => {
        const gNid = String((group as any)._nodeId || "").trim();
        const gMeta = (managedRootByKeyRef.current as Record<string, any>)[rootNodeKey(gNid, group.rootId)];
        return {
        rootId: group.rootId,
        rootName: group.rootName || gMeta?.display_name || group.rootId,
        _nodeColor: (gMeta?._nodeColor as string | undefined),
        _nodeId: (gMeta?._nodeId as string | undefined) || gNid || undefined,
        latestSessionTime: group.latestSessionTime,
        sessions: applyPinSnapshot(
          mergeSessionItems(
            [],
            [...group.items, ...group.pinnedItems]
              .map((item) => toSessionItem(group.rootId, { ...(item as any), root_id: group.rootId, _nodeId: gNid }))
              .filter((item): item is SessionItem => !!item),
          ),
          group.rootId,
        )
          .filter((item): item is SessionItem => !!item),
        totalCount: group.totalCount,
      }});
      if (seq !== multiProjectLoadSeqRef.current) return; // 丢弃过期响应
      // C2: 按 nid::rootId 逐组合并（保留本次未返回节点的上次分组），消除整体替换造成的项目消失/穿插帧。
      // 换账户后必须清掉旧账户的分组：凡本轮**成功**拉取过的节点，其旧分组一律不得保留——
      // 空结果（该账户在此节点没有会话）同样是有效答案，不是「没拉到」。
      setMultiProjectSessionGroups((prev) => {
        const prevList = Array.isArray(prev) ? prev : [];
        const scopeOf = (g: any) => `${String(g?._nodeId || "").trim()}::${String(g?.rootId || "")}`;
        const nextMap = new Map(nextGroups.map((g) => [scopeOf(g), g]));
        const ridSet = new Set(nextGroups.map((g) => String(g.rootId || "")).filter(Boolean));
        const queriedNids = new Set(nodeIds); // 本轮实际请求过的节点
        const merged: MultiProjectSessionGroup[] = [];
        const seen = new Set<string>();
        for (const g of prevList) {
          const nid = String((g as any)?._nodeId || "").trim();
          if (failedNids.has(nid)) {
            // 节点不可达：**保留旧分组是对的**（不清屏），但必须打标 ——
            // B3：断联时列表里那批会话只有骨架，正文现拉现挂。标出来让分组头显示
            // 「节点连不上」，用户才知道是那边断了，而不是这批会话坏了。
            merged.push({ ...g, _unreachable: true } as MultiProjectSessionGroup);
            continue;
          }
          const k = scopeOf(g);
          if (nextMap.has(k)) {
            merged.push(nextMap.get(k)!);
            seen.add(k);
            continue;
          }
          if (queriedNids.has(nid)) continue; // 成功拉取却未返回此分组 → 该账户下确实没有，清掉
          if (!nid && ridSet.has(String(g.rootId || ""))) continue; // 节点盲缓存组：被新数据取代
          merged.push(g);
        }
        for (const g of nextGroups) {
          const k = scopeOf(g);
          if (!seen.has(k)) {
            merged.push(g);
            seen.add(k);
          }
        }
        return applyPendingToMultiProjectGroups(merged, multiProjectPendingRef.current);
      });
      // 保存时补 _nodeColor，令缓存重建帧直接有色（C1 双保险）
      void saveCachedMultiRootSessionList(
        groups.map((g) => ({
          ...g,
          _nodeColor: resolveGroupColor(g as any, managedRootByKeyRef.current as any, getNodes() as any) || undefined,
        })) as any,
      );
    } finally {
      if (seq === multiProjectLoadSeqRef.current) {
        setMultiProjectSessionsLoading(false);
      }
    }
  }, [applyPendingToMultiProjectGroups, multiProjectSessionsEnabled]);
  const loadMoreMultiProjectSessions = useCallback(
    async (group: ProjectSessionGroup) => {
      const topLevelSessions = group.sessions.filter(isTopLevelSessionItem);
      const oldest = topLevelSessions[topLevelSessions.length - 1]?.updated_at || "";
      if (!group.rootId || !oldest) {
        return;
      }
      const previousLoaded = topLevelSessions.length;
      const groupNid = String((group as any)._nodeId || "").trim();
      const payload = await sessionService.fetchSessions(group.rootId, {
        nodeId: groupNid || getNodeIdForRoot(group.rootId),
        beforeTime: oldest,
        limit: SESSION_PAGE_SIZE,
        topLevel: true,
        includeChildren: true,
      });
      const nextItems = payload.items
        .concat(payload.pinnedItems)
        .map((item) => toSessionItem(group.rootId, { ...(item as any), root_id: group.rootId, _nodeId: groupNid }))
        .filter((item): item is SessionItem => !!item);
      setMultiProjectSessionGroups((prev) =>
        applyPendingToMultiProjectGroups(
          prev.map((current) => {
            if (String((current as any)._nodeId || "").trim() !== groupNid || current.rootId !== group.rootId) {
              return current;
            }
            const sessions = applyPinSnapshot(
              mergeSessionItems(current.sessions, nextItems),
              group.rootId,
            );
            return {
              ...current,
              sessions,
              totalCount: previousLoaded + payload.totalCount,
            };
          }),
          multiProjectPendingRef.current,
        ),
      );
    },
    [applyPendingToMultiProjectGroups, mergeSessionItems],
  );

  return {
    promotePendingSessionForRoot,
    loadMultiProjectSessionGroups,
    loadMoreMultiProjectSessions,
  };
}
