/**
 * session.accepted WS 事件处理器。
 *
 * 从 realtimeWsHandlers.ts 拆出（2026-10-10）：该 handler 104 行，处理会话
 * 接受事件（匹配 pending 请求、提升 temp key 到真实 session key、更新缓存）。
 *
 * CUSTOM(G-BA)：上游没有这个拆分。合上游时若 realtimeWsHandlers.ts 被整体覆盖，
 * 需要把本文件的内容合回去（或重新拆分），否则 session.accepted 处理会丢失。
 */
import type { RealtimeEventsContext } from "./useRealtimeEventsContext";
import { isSamePendingEcho } from "../services/session";
import type { Exchange } from "./appSession";
import type { Session } from "../services/session";
import type { SessionItem } from "./appSession";

export function handleSessionAccepted(
  ctx: RealtimeEventsContext,
  event: any,
  payload: any,
): void {
  const {
    refs: {
      currentRootNodeIdRef,
      pendingRequestRef,
      pendingBySessionRef,
      pendingDraftRef,
      sessionCacheRef,
      drawerSessionByRootRef,
    },
    setters: {
      setMultiProjectSessionPending,
      setDrawerSessionForRoot,
    },
    actions: {
      promotePendingSessionForRoot,
      bumpCacheVersion,
    },
    values: {
      rootSessionKey,
      scopedRootKey,
    },
  } = ctx;

  {
    const wsPayloadNid = String((payload as any)?._nodeId || (payload as any)?.nodeId || "").trim();
    const wsCurNid = String(currentRootNodeIdRef.current || "").trim();
    if (wsPayloadNid && wsCurNid && wsPayloadNid !== wsCurNid) { return; }
  }
  const requestId =
    typeof payload?.request_id === "string" ? payload.request_id : "";
  const pending = pendingRequestRef.current[requestId];
  if (!requestId || !pending) {
    console.warn("[session/ws] accepted_without_pending", { requestId, payloadSessionKey: typeof payload?.session_key === "string" ? payload.session_key : null });
    return;
  }
  console.info("[session/ws] accepted", { requestId, rootId: pending.rootId, sessionKey: pending.sessionKey || null, tempKey: pending.tempKey || null });
  delete pendingRequestRef.current[requestId];
  const acceptedTimestamp =
    typeof payload?.timestamp === "string" &&
    !Number.isNaN(Date.parse(payload.timestamp))
      ? payload.timestamp
      : pending.timestamp;
  const acceptedSessionKey =
    typeof payload?.session_key === "string" ? payload.session_key : "";
  if (!pending.sessionKey && pending.tempKey && acceptedSessionKey) {
    const cacheKey = rootSessionKey(pending.rootId, acceptedSessionKey);
    pendingBySessionRef.current[cacheKey] = {
      ...pending,
      timestamp: acceptedTimestamp,
      sessionKey: acceptedSessionKey,
      model_display_name:
        typeof payload?.model_display_name === "string"
          ? payload.model_display_name
          : "",
    };
    setMultiProjectSessionPending(pending.rootId, pending.tempKey, false);
    setMultiProjectSessionPending(pending.rootId, acceptedSessionKey, true);
    if (pendingDraftRef.current?.requestId === pending.requestId) {
      pendingDraftRef.current = null;
    }
    promotePendingSessionForRoot(
      pending.rootId,
      pending.tempKey,
      acceptedSessionKey,
      sessionCacheRef.current[cacheKey] || null,
    );
  }
  const markAccepted = (
    sess: Session | SessionItem | null | undefined,
  ): Session | null => {
    if (!sess) return null;
    // 服务端若在 accepted 里带了 seq（当前版本不带，OnStart 之后才可知），
    // 一并写入，使乐观条目立即成为「已持久化条目」。
    const acceptedSeq = Number((payload as any)?.seq || 0);
    const exchanges = Array.isArray((sess as any).exchanges)
      ? ((sess as any).exchanges as Exchange[]).map((exchange) =>
          isSamePendingEcho(exchange, pending)
            ? {
                ...exchange,
                timestamp: acceptedTimestamp,
                pending_ack: false,
                ...(acceptedSeq > 0 ? { seq: acceptedSeq } : {}),
                model_display_name:
                  exchange.model_display_name ||
                  (typeof payload?.model_display_name === "string"
                    ? payload.model_display_name
                    : ""),
              }
            : exchange,
        )
      : [];
    return {
      ...(sess as any),
      exchanges,
      model_display_name:
        (sess as any).model_display_name ||
        (typeof payload?.model_display_name === "string"
          ? payload.model_display_name
          : ""),
      updated_at: acceptedTimestamp,
    } as Session;
  };
  const acceptedTargetKey = pending.sessionKey || acceptedSessionKey;
  if (acceptedTargetKey) {
    const cacheKey = rootSessionKey(pending.rootId, acceptedTargetKey);
    const accepted = markAccepted(sessionCacheRef.current[cacheKey]);
    if (accepted) {
      sessionCacheRef.current[cacheKey] = accepted;
      bumpCacheVersion();
    }
  }
  const latestDrawer = drawerSessionByRootRef.current[scopedRootKey(pending.rootId)];
  const drawerKey = latestDrawer?.key || "";
  if (
    drawerKey &&
    (drawerKey === pending.sessionKey ||
      drawerKey === pending.tempKey ||
      drawerKey === acceptedSessionKey)
  ) {
    const accepted = markAccepted(latestDrawer);
    if (accepted) {
      setDrawerSessionForRoot(pending.rootId, accepted);
    }
  }
  return;
}
