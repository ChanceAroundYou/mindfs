/**
 * session.user_message WS 事件处理器。
 *
 * 从 realtimeWsHandlers.ts 拆出（2026-10-10）：该 handler 195 行，是最大的单个
 * WS 事件处理器，逻辑自包含（用户消息合并进 session cache），拆出后
 * realtimeWsHandlers.ts 从 883 行降到 ~688 行。
 *
 * CUSTOM(G-BA)：上游没有这个拆分。合上游时若 realtimeWsHandlers.ts 被整体覆盖，
 * 需要把本文件的内容合回去（或重新拆分），否则 session.user_message 处理会丢失。
 */
import type { RealtimeEventsContext } from "./useRealtimeEventsContext";
import { normalizeFastService } from "./appTask";
import type { Exchange } from "./appSession";
import type { Session } from "../services/session";

export function handleSessionUserMessage(
  ctx: RealtimeEventsContext,
  event: any,
  payload: any,
): void {
  const {
    refs: {
      currentRootNodeIdRef,
      sessionCacheRef,
      currentSessionRef,
      selectedSessionRef,
      sessionsRef,
    },
    setters: {
      setMultiProjectSessionPending,
    },
    actions: {
      updateSessionAgentForKey,
      bumpCacheVersion,
      loadSessionsForRoot,
      scheduleMultiProjectSessionReload,
    },
    values: {
      multiProjectSessionsEnabled,
      rootSessionKey,
      t,
    },
  } = ctx;

  const wsPayloadNid2 = String((payload as any)?._nodeId || (payload as any)?.nodeId || (payload as any)?.session?._nodeId || "").trim();
  const wsCurNid2 = String(currentRootNodeIdRef.current || "").trim();
  if (wsPayloadNid2 && wsCurNid2 && wsPayloadNid2 !== wsCurNid2) { return; }
  if (
    typeof payload?.session_key === "string" &&
    typeof payload?.root_id === "string"
  ) {
    const rootID = payload.root_id;
    const sessionKey = payload.session_key;
    setMultiProjectSessionPending(rootID, sessionKey, true);
    const exchange = payload.exchange;
    const sessionMeta = payload.session;
    const cacheKey = rootSessionKey(rootID, sessionKey);
    console.info("[session/ws] user_message", {
      rootID,
      sessionKey,
      sessionAgent: sessionMeta?.agent || "",
      exchangeAgent: exchange?.agent || "",
      cachedAgent: (sessionCacheRef.current[cacheKey] as any)?.agent || "",
      currentAgent:
        currentSessionRef.current?.key === sessionKey
          ? ((currentSessionRef.current as any)?.agent || "")
          : "",
      selectedAgent:
        (selectedSessionRef.current?.key || selectedSessionRef.current?.session_key) === sessionKey
          ? ((selectedSessionRef.current as any)?.agent || "")
          : "",
    });
    const cached =
      sessionCacheRef.current[cacheKey] ||
      ({
        key: sessionKey,
        type: sessionMeta?.type || "chat",
        agent: sessionMeta?.agent || exchange?.agent || "",
        model: sessionMeta?.model || exchange?.model || "",
        mode: sessionMeta?.mode || exchange?.mode || "",
        effort: sessionMeta?.effort || exchange?.effort || "",
        fast_service:
          normalizeFastService(sessionMeta?.fast_service) ||
          normalizeFastService(exchange?.fast_service),
        plan_mode:
          typeof sessionMeta?.plan_mode === "boolean"
            ? sessionMeta.plan_mode
            : false,
        name: sessionMeta?.name || t("session.new"),
        created_at:
          sessionMeta?.created_at ||
          exchange?.timestamp ||
          new Date().toISOString(),
        updated_at:
          sessionMeta?.updated_at ||
          exchange?.timestamp ||
          new Date().toISOString(),
        exchanges: [],
      } as any);
    const prevExchanges = Array.isArray((cached as any).exchanges)
      ? ((cached as any).exchanges as Exchange[])
      : [];
    const incomingMindFSID = String((exchange as any)?.mindfs_id || "").trim();
    const incomingUserSeq = Number((exchange as any)?.seq || 0);
    // 持久用户行先按 MindFS 身份、再按真实 seq 幂等定位；只有无身份乐观回声
    // 才回退到 role+content 认领。相同正文不代表同一轮，避免重试消息合并错行。
    const identityIndex = incomingMindFSID
      ? prevExchanges.findIndex(
          (item) => item.role === "user" && item.mindfs_id === incomingMindFSID,
        )
      : -1;
    const seqIndex =
      incomingUserSeq > 0
        ? prevExchanges.findIndex(
            (item) => Number((item as any)?.seq || 0) === incomingUserSeq,
          )
        : -1;
    let mergeIndex = identityIndex >= 0 ? identityIndex : seqIndex;
    if (mergeIndex < 0) {
      for (let i = prevExchanges.length - 1; i >= 0; i -= 1) {
        const item = prevExchanges[i];
        if (
          item.role === "user" &&
          item.content === exchange?.content &&
          !Number((item as any)?.seq || 0)
        ) {
          mergeIndex = i;
          break;
        }
      }
    }
    const pushExchange = {
      role: "user",
      agent: exchange?.agent || "",
      model: exchange?.model || "",
      model_display_name: exchange?.model_display_name || "",
      mode: exchange?.mode || "",
      effort: exchange?.effort || "",
      fast_service: exchange?.fast_service || "",
      content: exchange?.content || "",
      timestamp:
        exchange?.timestamp || new Date().toISOString(),
      pending_ack: false,
      ...(incomingMindFSID ? { mindfs_id: incomingMindFSID } : {}),
      ...(incomingUserSeq > 0 ? { seq: incomingUserSeq } : {}),
    } as Exchange;
    const nextExchanges =
      mergeIndex >= 0
        ? [
            ...prevExchanges.slice(0, mergeIndex),
            {
              ...prevExchanges[mergeIndex],
              ...(incomingMindFSID ? { mindfs_id: incomingMindFSID } : {}),
              ...(incomingUserSeq > 0 ? { seq: incomingUserSeq } : {}),
              timestamp:
                exchange?.timestamp || prevExchanges[mergeIndex].timestamp,
              model: exchange?.model || prevExchanges[mergeIndex].model,
              model_display_name:
                exchange?.model_display_name ||
                prevExchanges[mergeIndex].model_display_name,
              mode: exchange?.mode || prevExchanges[mergeIndex].mode,
              effort:
                exchange?.effort || prevExchanges[mergeIndex].effort,
              fast_service:
                exchange?.fast_service ||
                prevExchanges[mergeIndex].fast_service,
              pending_ack: false,
            },
            ...prevExchanges.slice(mergeIndex + 1),
          ]
        : [...prevExchanges, pushExchange];
    sessionCacheRef.current[cacheKey] = {
      ...(cached as any),
      ...(sessionMeta || {}),
      key: sessionKey,
      agent:
        sessionMeta?.agent ||
        exchange?.agent ||
        (cached as any).agent ||
        "",
      model:
        sessionMeta?.model ||
        exchange?.model ||
        (cached as any).model ||
        "",
      mode:
        sessionMeta?.mode ||
        exchange?.mode ||
        (cached as any).mode ||
        "",
      effort:
        sessionMeta?.effort ||
        exchange?.effort ||
        (cached as any).effort ||
        "",
      fast_service:
        normalizeFastService(sessionMeta?.fast_service) ||
        normalizeFastService(exchange?.fast_service) ||
        normalizeFastService((cached as any).fast_service),
      plan_mode:
        typeof sessionMeta?.plan_mode === "boolean"
          ? sessionMeta.plan_mode
          : !!(cached as any).plan_mode,
      exchanges: nextExchanges,
      model_display_name:
        sessionMeta?.model_display_name ||
        exchange?.model_display_name ||
        (cached as any).model_display_name ||
        "",
      updated_at:
        sessionMeta?.updated_at ||
        exchange?.timestamp ||
        new Date().toISOString(),
    } as Session;
    const runtimeAgent = sessionMeta?.agent || exchange?.agent || "";
    if (runtimeAgent) {
      updateSessionAgentForKey(
        rootID,
        sessionKey,
        runtimeAgent,
        sessionMeta?.model || exchange?.model || "",
        sessionMeta?.mode || exchange?.mode || "",
        sessionMeta?.effort || exchange?.effort || "",
        normalizeFastService(sessionMeta?.fast_service) ||
          normalizeFastService(exchange?.fast_service),
        typeof sessionMeta?.plan_mode === "boolean"
          ? sessionMeta.plan_mode
          : undefined,
      );
    }
    bumpCacheVersion();
    const newest2 = sessionsRef.current[0]?.updated_at || "";
    void loadSessionsForRoot(
      rootID,
      newest2 ? { afterTime: newest2 } : { replace: true },
    );
    if (multiProjectSessionsEnabled) {
      scheduleMultiProjectSessionReload();
    }
  }
}
