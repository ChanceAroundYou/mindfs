import { useCallback } from "react";
import { toSessionItem, type SessionItem } from "./appSession";
import { useI18n } from "../i18n";
import { reportError } from "../services/net/error";
import { isTransientExchange, syncSession, type Session } from "../services/session";
import { mergeSessionItems } from "../services/session/sessionListMerge";
import type { UseSessionLifecycleParams } from "./useSessionLifecycleTypes";

// 手动同步必须把仍在内存中的瞬时尾部合回全量同步快照。
type Params = Pick<UseSessionLifecycleParams,
  | "rootSessionKey"
  | "setSyncingSessionKeys"
  | "currentRootIdRef"
  | "sessionCacheRef"
  | "getNodeIdForRoot"
  | "clearSessionStale"
  | "loadedSessionRef"
  | "setSessions"
  | "setSelectedSession"
  | "drawerSessionByRootRef"
  | "scopedRootKey"
  | "setDrawerSessionForRoot"
  | "bumpCacheVersion"
>;

export function useSessionSyncAction(params: Params) {
  const { t } = useI18n();
  const {
    rootSessionKey,
    setSyncingSessionKeys,
    currentRootIdRef,
    sessionCacheRef,
    getNodeIdForRoot,
    clearSessionStale,
    loadedSessionRef,
    setSessions,
    setSelectedSession,
    drawerSessionByRootRef,
    scopedRootKey,
    setDrawerSessionForRoot,
    bumpCacheVersion,
  } = params;
  const handleSyncSession = useCallback(
    async (session: SessionItem) => {
      const sessionKey = session?.key || session?.session_key;
      const rootID =
        (session?.root_id as string | undefined) || currentRootIdRef.current;
      if (!rootID || !sessionKey || sessionKey.startsWith("pending-")) return;

      const cacheKey = rootSessionKey(rootID, sessionKey);
      setSyncingSessionKeys((prev) => {
        const next = new Set(prev);
        next.add(cacheKey);
        next.add(sessionKey);
        return next;
      });
      try {
        const result = await syncSession(rootID, sessionKey, { full: true, nodeId: String((session as any)?._nodeId || "") || getNodeIdForRoot(rootID) });
        const synced = result.session;
        if (!synced) {
          reportError("session.sync_failed", t("session.syncFailed"));
          return;
        }
        // 点「同步」必须保住「正在等待回答的 ask_user 卡」。
        // 该卡是纯瞬时态：只存在于前端内存缓存（WS tool_call 追加的 seq=0 role=tool 条目）
        // 与服务端内存（manager.pendingToolCalls）里，两边都不会经 HTTP 下发
        // （session.Exchange 结构体没有 ToolCall 字段）。syncSession 以 IDB 缓存为 base，
        // 而 IDB 里从来没有这些瞬时条目，于是同步后卡片凭空消失（实测 2026-09-12 症状 2）。
        // 这里从内存缓存按 callId 去重后把它们合并回来；落盘后同一 callId 会出现在窗口 aux
        // 中，SessionViewer 的 overlay 对账（windowToolCallIds）会自然让位，不会重复渲染。
        // 同步会把整个会话的持久化交换灌回来（实测缓存 24 → 92），但**本轮尚未落盘的内容**
        // 只存在于内存缓存的 seq=0 瞬时条目里。原先这里只保留 role=tool 一类，于是
        // 「点同步的瞬间，正在流式输出的正文凭空消失，而 ask 卡还在」——2026-09-13 实测
        // cacheBeforeTransient=4 只保住 1 条（keptToolTransient=1），丢掉的 3 条正是非 tool
        // 的直播正文。
        // 现在两类都保留，各按「同步结果里是否已有」去重：
        //   · tool 条目按 callId（落盘后同一 callId 会出现在窗口 aux，SessionViewer 的
        //     overlay 对账 windowToolCallIds 会自然让位，不会重复渲染）
        //   · 其余条目按 role+内容（已持久化的轮次不会以 seq=0 形式被重复追加，这正是当初
        //     只留 tool 想防的事；用内容比对同样防得住）
        //
        // **为什么这里不用 composeLoadedExchanges**：那条规则是「服务端回包只含已落盘行，
        // 所以本地瞬时行无脑全留」。手动同步不同 —— 它把整个会话灌回来，刚落盘的那一轮
        // 已经以**真实 seq** 出现在回包里，此时本地那几份 seq=0 的拷贝就成了陈旧副本，
        // 再留就是同一段正文显示两遍。所以这条路径必须比对后剔除，判据也更强（callId 或
        // role+内容）。两条规则形状不同是**有意的**，不是漏统一。
        const localTransientTail = (() => {
          const cachedExchanges = Array.isArray(
            (sessionCacheRef.current[cacheKey] as any)?.exchanges,
          )
            ? ((sessionCacheRef.current[cacheKey] as any).exchanges as any[])
            : [];
          const present = new Set<string>();
          const presentText = new Set<string>();
          const syncedExchanges = Array.isArray((synced as any)?.exchanges)
            ? ((synced as any).exchanges as any[])
            : [];
          for (const ex of syncedExchanges) {
            const callId = `${(ex as any)?.toolCall?.callId || ""}`.trim();
            if (callId) present.add(callId);
            presentText.add(
              `${String((ex as any)?.role || "").toLowerCase()}|${String(
                (ex as any)?.content || "",
              )}`,
            );
          }
          const syncedAux = ((synced as any)?.exchange_aux || {}) as Record<
            string,
            any[]
          >;
          for (const items of Object.values(syncedAux)) {
            for (const aux of items || []) {
              const callId = `${(aux as any)?.toolcall?.callId || ""}`.trim();
              if (callId) present.add(callId);
            }
          }
          return cachedExchanges.filter((ex) => {
            if (!isTransientExchange(ex)) return false;
            const role = String((ex as any)?.role || "").toLowerCase();
            if (role === "tool") {
              const callId = `${(ex as any)?.toolCall?.callId || ""}`.trim();
              return !callId || !present.has(callId);
            }
            const content = String((ex as any)?.content || "");
            if (!content) return false;
            return !presentText.has(`${role}|${content}`);
          });
        })();
        const syncedExchanges = Array.isArray((synced as any)?.exchanges)
          ? ((synced as any).exchanges as any[])
          : [];
        const normalized = {
          ...(synced as any),
          key: sessionKey,
          exchanges: [...syncedExchanges, ...localTransientTail],
        } as Session;
        sessionCacheRef.current[cacheKey] = normalized;
        loadedSessionRef.current[cacheKey] = true;
        clearSessionStale(rootID, sessionKey);

        const nextItem = toSessionItem(rootID, normalized);
        if (nextItem) {
          setSessions((prev) => mergeSessionItems(prev, [nextItem]));
        }

        setSelectedSession((prev) => {
          const prevKey = prev?.key || prev?.session_key;
          const prevRoot =
            (prev?.root_id as string | undefined) || currentRootIdRef.current;
          if (!prev || prevKey !== sessionKey || prevRoot !== rootID) {
            return prev;
          }
          return toSessionItem(rootID, {
            ...(prev as any),
            ...(normalized as any),
            key: sessionKey,
            session_key: sessionKey,
            root_id: rootID,
          });
        });

        if (drawerSessionByRootRef.current[scopedRootKey(rootID)]?.key === sessionKey) {
          setDrawerSessionForRoot(rootID, normalized);
        }

        bumpCacheVersion();
      } catch {
        reportError("session.sync_failed", t("session.syncFailed"));
      } finally {
        setSyncingSessionKeys((prev) => {
          const next = new Set(prev);
          next.delete(cacheKey);
          next.delete(sessionKey);
          return next;
        });
      }
    },
    [
      bumpCacheVersion,
      clearSessionStale,
      mergeSessionItems,
      rootSessionKey,
      setDrawerSessionForRoot,
    ],
  );

  return { handleSyncSession };
}
